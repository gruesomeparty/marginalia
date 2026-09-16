package mcpserver

import (
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/reviewset"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// DocEvents is one document's new feedback, and where the caller has read up
// to. Cursor is a line count, not a timestamp: the log is append-only and read
// in file order, so an integer is exact, monotone, and needs nothing stored on
// either side.
type DocEvents struct {
	Doc    string  `json:"doc"`
	Events []Noted `json:"events"`
	Cursor int     `json:"cursor"`
	Done   bool    `json:"done"`
}

// Noted is an event as a tool reports it: the event, flattened, plus the id
// that `reply_to_note` names it by. Embedding rather than a wrapper field
// because the id is not extra information about the event — it *is* the
// event, hashed, and a caller reading the log should not have to go somewhere
// else to find out what to answer.
type Noted struct {
	feedback.Event
	ID string `json:"id"`
}

// DocStatus is one document's log materialized against the document as it now
// reads — the second-pass view, including which suggested edits are safe to
// apply. It is deliberately the same shape the HTTP API and `suggestions`
// already serve: a parallel schema would be a copy waiting to drift.
type DocStatus struct {
	Doc      string `json:"doc"`
	Blocks   int    `json:"blocks"`
	Comments int    `json:"comments"`
	Replies  int    `json:"replies"`
	// The revision loop, as a queue length: what still wants doing, what the
	// agent says it did and the reviewer has not ruled on yet, and what is
	// settled. Reading these beats re-reading every state.
	Outstanding int                   `json:"outstanding"`
	Addressed   int                   `json:"addressed"`
	Confirmed   int                   `json:"confirmed"`
	Reopened    int                   `json:"reopened"`
	Stale       int                   `json:"stale"`
	Orphaned    int                   `json:"orphaned"`
	Done        bool                  `json:"done"`
	States      []feedback.State      `json:"states"`
	Ready       []ReadySuggestion     `json:"applicable_suggestions"`
	Needs       []feedback.Suggestion `json:"suggestions_needing_confirmation"`
}

// ReadySuggestion carries the block's current text beside the replacement, so
// an agent can patch precisely without re-deriving anchoring — the same thing
// `marginalia suggestions --json` reports.
type ReadySuggestion struct {
	feedback.Suggestion
	Current string `json:"current"`
}

// docsOf resolves what a tool call named into document paths, confined to the
// server's root. A session id is a convenience; the paths are the identity, so
// every read works whether or not a server is running.
func (s *Server) docsOf(sessionID string, paths []string) ([]string, error) {
	if sessionID != "" {
		live := s.lookup(sessionID)
		if live == nil {
			return nil, session.Advertise(errUnknownSession(sessionID))
		}
		return live.docs, nil
	}
	confined, err := s.confine(paths)
	if err != nil {
		return nil, err
	}
	set, err := reviewset.Load(confined)
	if err != nil {
		return nil, session.RouteSetError(err)
	}
	out := make([]string, 0, len(set.Docs))
	for _, d := range set.Docs {
		out = append(out, d.Path)
	}
	return out, nil
}

// normalizeCursor re-keys a caller's cursor to the paths this server actually
// serves.
//
// The two spellings are not hypothetical: on macOS /var is a symlink to
// /private/var, so a caller that passes "spec.md" under a temp root holds a
// different string than the resolved one the tools hand back. Keyed naively,
// its cursor would match nothing and every call would re-deliver the whole log
// — silently, which is the worst way for a cursor to fail.
func (s *Server) normalizeCursor(cursor map[string]int) map[string]int {
	if len(cursor) == 0 {
		return cursor
	}
	out := make(map[string]int, len(cursor))
	for key, at := range cursor {
		resolved, err := s.confine([]string{key})
		if err != nil || len(resolved) != 1 {
			// Not a path this server would serve; keep it as given rather
			// than dropping it, so the mismatch is visible in the result.
			out[key] = at
			continue
		}
		out[resolved[0]] = at
	}
	return out
}

// eventsSince reads one document's log from the cursor on.
func eventsSince(path string, cursor int) (DocEvents, error) {
	events, err := feedback.NewStore(path).Load()
	if err != nil {
		return DocEvents{}, err
	}
	out := DocEvents{Doc: path, Cursor: len(events), Events: []Noted{}}
	for _, e := range events {
		if e.Type == feedback.TypeReviewDone {
			out.Done = true
		}
	}
	// A cursor past the end can only mean the log was rewritten, which the
	// append-only rule forbids. Start over rather than skip silently.
	if cursor < 0 || cursor > len(events) {
		cursor = 0
	}
	for _, e := range events[cursor:] {
		out.Events = append(out.Events, Noted{Event: e, ID: feedback.NoteID(e)})
	}
	return out, nil
}

// statusOf materializes one document's log against the document as it now
// reads. This is the pipeline `cmd/suggestions.go` runs with no server, which
// is exactly why the MCP reads need none either.
func statusOf(path string) (DocStatus, error) {
	doc, err := document.Parse(path)
	if err != nil {
		return DocStatus{}, err
	}
	events, err := feedback.NewStore(path).Load()
	if err != nil {
		return DocStatus{}, err
	}
	blocks := make([]feedback.Block, 0, len(doc.Blocks))
	text := make(map[string]string, len(doc.Blocks))
	for _, b := range doc.Blocks {
		blocks = append(blocks, feedback.Block{ID: b.ID, Hash: b.Hash, Text: b.PlainText})
		text[b.ID] = b.PlainText
	}
	res := feedback.Materialize(events, blocks)
	ready, needs := res.Suggestions()
	out := DocStatus{
		Doc: path, Blocks: len(doc.Blocks),
		Comments: res.Comments, Replies: res.Replies,
		Outstanding: res.Outstanding, Addressed: res.Addressed,
		Confirmed: res.Confirmed, Reopened: res.Reopened,
		Stale: res.Stale, Orphaned: res.Orphaned, Done: res.Done,
		States: res.States,
		Ready:  make([]ReadySuggestion, 0, len(ready)),
		Needs:  needs,
	}
	for _, s := range ready {
		out.Ready = append(out.Ready, ReadySuggestion{Suggestion: s, Current: text[s.Block]})
	}
	if out.Needs == nil {
		out.Needs = []feedback.Suggestion{}
	}
	if out.States == nil {
		out.States = []feedback.State{}
	}
	return out, nil
}
