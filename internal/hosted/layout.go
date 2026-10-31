// Package hosted is `marginalia host`: one always-on process serving many
// reviews, for agents that share no filesystem with the reviewer (PRD §5.9).
//
// Everything a review is lives in a storage.Store under one key prefix, laid
// out the same on every backend:
//
//	reviews/<id>/meta/<n>.json   versioned metadata; the highest n wins
//	reviews/<id>/rev/<n>/<name>  revision n of each document, immutable
//	reviews/<id>/log/<name>      the document's append-only feedback log
//	reviews/<id>/archived/<ns>   when it was archived, and by whom
//	reviews/<id>/restored/<ns>   when it was brought back, and by whom
//
// An archived review is the same tree under archive/<id>/, moved whole. The
// two markers carry their time in the key, so the one listing startup does
// already says when each happened; the blob's bytes name who, for a reader.
//
// Startup lists that prefix once and rebuilds every review in memory; from
// then on the process is the single writer, logs are served from memory and
// written through, and nothing polls the store.
package hosted

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	prefixReviews = "reviews/"
	prefixArchive = "archive/"
)

// idBytes is 16 bytes of randomness: 128 bits, which is what "unguessable"
// has to mean for a URL that is handed around in chats. The id is not the
// auth boundary — the edge is — but a guessable one would make the edge the
// only boundary.
const idBytes = 16

// NewID returns a fresh, URL-safe review id.
func NewID() string {
	var b [idBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("hosted: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// idPattern is what a review id looks like, so a route can refuse a
// malformed one before it becomes a key.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// ValidID reports whether s has the shape of a review id.
func ValidID(s string) bool { return idPattern.MatchString(s) }

func metaKey(id string, n int) string {
	return prefixReviews + id + "/meta/" + strconv.Itoa(n) + ".json"
}

func revKey(id string, n int, name string) string {
	return prefixReviews + id + "/rev/" + strconv.Itoa(n) + "/" + name
}

func logKey(id, name string) string { return prefixReviews + id + "/log/" + name }

// Marker kinds: an archive or a restore, recorded as its own blob.
const (
	markArchived = "archived"
	markRestored = "restored"
)

// markKey is a marker under prefix ("reviews/" or "archive/"), named by its
// time: zero-padded nanoseconds, so keys sort in the order they happened.
func markKey(prefix, id, kind string, at time.Time) string {
	return fmt.Sprintf("%s%s/%s/%020d", prefix, id, kind, at.UnixNano())
}

// markTime reads the time out of a marker key, or reports it is not one.
func markTime(prefix, id, kind, key string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(key, prefix+id+"/"+kind+"/")
	if !ok {
		return time.Time{}, false
	}
	ns, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || ns <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, ns).UTC(), true
}

// metaVersion reads n out of "reviews/<id>/meta/<n>.json", or -1.
func metaVersion(id, key string) int { return metaVersionUnder(prefixReviews, id, key) }

// metaVersionUnder is metaVersion under either prefix.
func metaVersionUnder(prefix, id, key string) int {
	rest, ok := strings.CutPrefix(key, prefix+id+"/meta/")
	if !ok {
		return -1
	}
	rest, ok = strings.CutSuffix(rest, ".json")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return -1
	}
	return n
}

// reviewOf reads the id out of any key under reviews/.
func reviewOf(key string) (string, bool) { return idUnder(prefixReviews, key) }

// idUnder reads the id out of any key under prefix.
func idUnder(prefix, key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", false
	}
	id, _, ok := strings.Cut(rest, "/")
	if !ok || !ValidID(id) {
		return "", false
	}
	return id, true
}

// Meta is what a review is, apart from its documents and its log: who
// published it, how it is framed, and which revision is current. It is
// written as a new versioned blob on every publish and never rewritten.
type Meta struct {
	ID       string   `json:"id"`
	Title    string   `json:"title,omitempty"`
	Agent    string   `json:"agent,omitempty"`
	Revision int      `json:"revision"`
	Docs     []string `json:"docs"`
	Review   string   `json:"review,omitempty"` // shipped preset
	Config   string   `json:"config,omitempty"` // inline review YAML, layered over it
	Theme    string   `json:"theme,omitempty"`
	Created  string   `json:"created"`
	Updated  string   `json:"updated"` // when this revision was published
	// Bytes is the documents' size summed over every revision so far — a
	// running total carried on the metadata, so the stored-bytes budget can
	// be rebuilt at startup from the one listing it already does.
	Bytes int64 `json:"bytes,omitempty"`
	// From is each document's log length when this revision was published.
	// The logs outlive a republish, so a review_done before that point
	// answered an earlier revision, not this one. Absent on revision 1 and
	// on metadata written before it existed, which reads as zero: every
	// review_done counts, as it always did.
	From map[string]int `json:"from,omitempty"`
	// Retired is the log length of each document an earlier revision served
	// and this one does not. No page or tool writes to those logs any more,
	// but their events still count toward the agent wait's cursor, which
	// must never move back.
	Retired map[string]uint64 `json:"retired,omitempty"`
}

// maxNameLen bounds a document name. Long enough for any real file name,
// short enough that a name cannot be used as a payload.
const maxNameLen = 128

// CheckName is the rule for a document name an agent sends: one path
// segment, not hidden, not a traversal, and short. A name is a key segment
// and a URL segment and a display label, and the same check serves all
// three. It refuses rather than cleans: a name that had to be repaired is a
// name the agent did not mean.
func CheckName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a document needs a name — the extension picks the format (plan.md, api.proto, flow.mmd)")
	case len(name) > maxNameLen:
		return fmt.Errorf("document name %q is longer than %d characters", name, maxNameLen)
	case strings.ContainsAny(name, "/\\"):
		return fmt.Errorf("document name %q must be a single path segment — no directories", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("document name %q must not start with a dot", name)
	case name == ".." || strings.ContainsAny(name, "\x00\n\r"):
		return fmt.Errorf("document name %q is not a file name", name)
	case strings.TrimSpace(name) != name:
		return fmt.Errorf("document name %q has leading or trailing whitespace", name)
	}
	return nil
}
