package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

const proseDoc = "# Spec\n\nThe cap is 500. Retries use no backoff. Failures go to the log.\n"

func prose(t *testing.T) (*Server, *feedback.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(proseDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(path)
	return New(Options{Doc: doc, Store: store, Author: "tester"}), store, path
}

// The server derives the anchor from the quote alone, so the page cannot write
// one that disagrees with the text the server will resolve it against.
func TestServerDerivesTheSubAnchorFromTheQuote(t *testing.T) {
	s, store, _ := prose(t)
	rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Quote: "The cap is 500. Retries use no backoff.", Hash: blockHash(t, s, "1/2"),
		Type: "comment", Text: "no backoff is wrong",
		// The page sends nothing but what was selected — and a start that is
		// plainly wrong, to prove it is not trusted.
		Sub: &feedback.Sub{Quote: "Retries use no backoff.", Start: 9999},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Sub == nil {
		t.Fatalf("the anchor did not reach disk: %+v", got)
	}
	sub := got[0].Sub
	if sub.Start != 16 {
		t.Errorf("start = %d, want 16 — the server's own offset, not the page's", sub.Start)
	}
	if sub.Prefix != "The cap is 500. " && !strings.HasSuffix(sub.Prefix, "The cap is 500. ") {
		t.Errorf("prefix = %q", sub.Prefix)
	}
	if !strings.HasPrefix(sub.Suffix, " Failures go to the log.") {
		t.Errorf("suffix = %q", sub.Suffix)
	}
	if sub.Hash != feedback.SubHash("Retries use no backoff.") {
		t.Errorf("hash = %q", sub.Hash)
	}
	// And the block anchor is untouched: a reader that knows nothing about
	// sub-anchors still sees an ordinary note on block 1/2.
	if got[0].Block != "1/2" || got[0].Hash != blockHash(t, s, "1/2") || got[0].Quote == "" {
		t.Errorf("the block anchor must survive intact: %+v", got[0])
	}
}

// blockHash is the current hash of one block, as the page would have read it
// out of the payload.
func blockHash(t *testing.T, s *Server, id string) string {
	t.Helper()
	for _, b := range s.docOf(&s.docs[0]).Blocks {
		if b.ID == id {
			return b.Hash
		}
	}
	t.Fatalf("no block %s", id)
	return ""
}

// A selection that is not in the block is refused rather than stored as an
// anchor nothing could ever resolve.
func TestASelectionThatIsNotInTheBlockIsRefused(t *testing.T) {
	s, store, _ := prose(t)
	rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Type: "comment", Text: "x",
		Sub: &feedback.Sub{Quote: "a sentence from some other document"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not in block") {
		t.Errorf("the error should say what went wrong: %q", rr.Body.String())
	}
	if got, _ := store.Load(); len(got) != 0 {
		t.Errorf("a refused anchor must append nothing: %+v", got)
	}
}

// An empty selection is a note about the whole block, said clumsily — not an
// error, and not a stored anchor.
func TestAnEmptySelectionIsJustABlockNote(t *testing.T) {
	s, store, _ := prose(t)
	if rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Type: "comment", Text: "x", Sub: &feedback.Sub{Quote: "   "},
	}); rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Load()
	if len(got) != 1 || got[0].Sub != nil {
		t.Fatalf("expected a plain block note: %+v", got)
	}
}

// The whole point, end to end through the API the page reads: an unrelated
// edit to the paragraph leaves the note standing, and an edit to its own
// sentence makes it stale.
func TestResolutionJudgesASubAnchoredNoteByItsSentence(t *testing.T) {
	s, _, path := prose(t)
	if rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Type: "comment", Text: "no backoff is wrong",
		Sub: &feedback.Sub{Quote: "Retries use no backoff."},
	}); rr.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", rr.Code, rr.Body.String())
	}

	reparse := func(t *testing.T, body string) feedback.Note {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		doc, err := document.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		s.setDoc(&s.docs[0], doc)
		rr := get(t, s, "/api/resolution")
		var res feedback.Resolution
		if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		for _, st := range res.States {
			if st.Block == "1/2" {
				return st.Current
			}
		}
		t.Fatalf("no state for 1/2: %+v", res.States)
		return feedback.Note{}
	}

	n := reparse(t, "# Spec\n\nThe cap is 2000, raised last week. Retries use no backoff. Failures go to the log.\n")
	if n.Stale {
		t.Error("a different sentence changed — this note still applies")
	}
	if n.At == nil {
		t.Fatal("the page needs to know where the quote landed now")
	}

	n = reparse(t, "# Spec\n\nThe cap is 500. Retries use exponential backoff. Failures go to the log.\n")
	if !n.Stale {
		t.Error("its own sentence changed — that is stale")
	}
	if n.At != nil {
		t.Error("a note that did not locate must not claim a position")
	}
}
