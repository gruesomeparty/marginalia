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
	Doc    string           `json:"doc"`
	Events []feedback.Event `json:"events"`
	Cursor int              `json:"cursor"`
	Done   bool             `json:"done"`
}

// DocStatus is one document's log materialized against the document as it now
// reads — the second-pass view, including which suggested edits are safe to
// apply. It is deliberately the same shape the HTTP API and `suggestions`
// already serve: a parallel schema would be a copy waiting to drift.
type DocStatus struct {
	Doc      string                `json:"doc"`
	Blocks   int                   `json:"blocks"`
	Comments int                   `json:"comments"`
	Stale    int                   `json:"stale"`
	Orphaned int                   `json:"orphaned"`
	Done     bool                  `json:"done"`
	States   []feedback.State      `json:"states"`
	Ready    []ReadySuggestion     `json:"applicable_suggestions"`
	Needs    []feedback.Suggestion `json:"suggestions_needing_confirmation"`
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

// eventsSince reads one document's log from the cursor on.
func eventsSince(path string, cursor int) (DocEvents, error) {
	events, err := feedback.NewStore(path).Load()
	if err != nil {
		return DocEvents{}, err
	}
	out := DocEvents{Doc: path, Cursor: len(events), Events: []feedback.Event{}}
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
	out.Events = append(out.Events, events[cursor:]...)
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
	hashes := make(map[string]string, len(doc.Blocks))
	text := make(map[string]string, len(doc.Blocks))
	order := make([]string, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		hashes[b.ID] = b.Hash
		text[b.ID] = b.PlainText
		order = append(order, b.ID)
	}
	res := feedback.Materialize(events, hashes, order)
	ready, needs := res.Suggestions()
	out := DocStatus{
		Doc: path, Blocks: len(doc.Blocks),
		Comments: res.Comments, Stale: res.Stale, Orphaned: res.Orphaned, Done: res.Done,
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
