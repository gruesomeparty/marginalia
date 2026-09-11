package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// maxFeedbackBody caps a POSTed event. The largest legitimate one is a
// suggest_edit carrying a replacement block, so a megabyte is generous; the
// point is that an unbounded body should not be readable into memory even on
// a trusted interface.
const maxFeedbackBody = 1 << 20

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// entry resolves which document a request is about: an explicit `doc` (a Rel
// path or the document's own path), else the first document in the set.
func (s *Server) entry(r *http.Request) *Entry {
	if key := r.URL.Query().Get("doc"); key != "" {
		return s.lookup(key)
	}
	return &s.docs[0]
}

func (s *Server) lookup(key string) *Entry {
	if e, ok := s.byRel[key]; ok {
		return e
	}
	if e, ok := s.byPath[key]; ok {
		return e
	}
	return nil
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	s.renderPage(w, &s.docs[0])
}

func (s *Server) handleDocPage(w http.ResponseWriter, r *http.Request) {
	e := s.lookup(r.PathValue("doc"))
	if e == nil {
		http.NotFound(w, r)
		return
	}
	s.renderPage(w, e)
}

// resolution materializes a document's log against the document as it now
// reads, so a re-render can say which notes still apply, which predate an edit
// and which have lost their block entirely.
func (s *Server) resolution(e *Entry, events []feedback.Event) feedback.Resolution {
	doc := s.docOf(e)
	hashes := make(map[string]string, len(doc.Blocks))
	order := make([]string, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		hashes[b.ID] = b.Hash
		order = append(order, b.ID)
	}
	return feedback.Materialize(events, hashes, order)
}

// renderPage renders one document plus the navigation state of the whole set,
// which means reading every log: cheap, and it keeps the sidebar's counts and
// done ticks true on every load. The page is rendered into memory first: once
// bytes are on the wire the status is already sent, and a half-written
// document under a 200 is worse than an honest 500.
func (s *Server) renderPage(w http.ResponseWriter, current *Entry) {
	page := web.Page{
		Doc:      s.docOf(current),
		Watch:    s.opts.Watch,
		Revision: s.Revision(),
		Author:   s.opts.Author,
		Title:    s.opts.Title,
		Nested:   s.opts.Nested,
		Review:   s.opts.Review,
	}
	setDone := true
	for i := range s.docs {
		e := &s.docs[i]
		events, err := e.Store.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if e == current {
			page.Events = events
			page.Resolution = s.resolution(e, events)
		}
		nav := web.NavDoc{
			Label:   e.Label,
			Rel:     e.Rel,
			Count:   countComments(events),
			Done:    hasReviewDone(events),
			Current: e == current,
		}
		if !nav.Done {
			setDone = false
		}
		page.Docs = append(page.Docs, nav)
	}
	page.SetDone = setDone && len(s.docs) > 1
	var buf bytes.Buffer
	if err := web.Render(&buf, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func countComments(events []feedback.Event) int {
	n := 0
	for _, e := range events {
		if e.Type != feedback.TypeReviewDone {
			n++
		}
	}
	return n
}

func hasReviewDone(events []feedback.Event) bool {
	for _, e := range events {
		if e.Type == feedback.TypeReviewDone {
			return true
		}
	}
	return false
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	events, err := e.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"doc":        s.docOf(e),
		"events":     events,
		"author":     s.opts.Author,
		"docs":       s.docList(),
		"resolution": s.resolution(e, events),
		// The vocabulary is part of the answer: an agent reading `blocker`
		// out of the log needs to see that it was asked for, and what it was
		// labelled when the human tapped it.
		"review": web.ReviewInfo(s.opts.Review),
	})
}

// docList describes the served set so an agent reading the API knows what the
// human was handed, not just the document it asked about.
func (s *Server) docList() []map[string]string {
	out := make([]map[string]string, 0, len(s.docs))
	for i := range s.docs {
		e := &s.docs[i]
		out = append(out, map[string]string{"path": s.docOf(e).Path, "rel": e.Rel, "label": e.Label})
	}
	return out
}

// handleResolution serves the second-pass view an agent needs after revising a
// document: the current state per block, which notes an edit has outrun, and
// which have lost their block altogether.
func (s *Server) handleResolution(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	events, err := e.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.resolution(e, events))
}

// handleRevision lets the page notice that a document it is showing has been
// re-parsed under it. Deliberately tiny: the page polls it while --watch is on.
func (s *Server) handleRevision(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"rev": s.Revision(), "watch": s.opts.Watch})
}

func (s *Server) handleGetFeedback(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	events, err := e.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handlePostFeedback(w http.ResponseWriter, r *http.Request) {
	var e feedback.Event
	r.Body = http.MaxBytesReader(w, r.Body, maxFeedbackBody)
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	// The event names its own document, but only a document in the served set
	// may be written to: a stale page must not be able to append anywhere.
	target := &s.docs[0]
	if e.Doc != "" {
		if target = s.lookup(e.Doc); target == nil {
			http.Error(w, "unknown document", http.StatusBadRequest)
			return
		}
	} else if key := r.URL.Query().Get("doc"); key != "" {
		if target = s.lookup(key); target == nil {
			http.Error(w, "unknown document", http.StatusBadRequest)
			return
		}
	}
	if code, err := s.check(target, &e); err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if err := s.append(target, &e); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// check is the server-side gate on what a page may write. The page renders
// the same rules, but rendering them is not enforcing them: a tab left open
// across a restart, or a script pointed at the API, must not be able to
// invent vocabulary the agent never asked for or comment on a block it
// handed over read-only.
func (s *Server) check(target *Entry, e *feedback.Event) (int, error) {
	cfg := s.opts.Review
	if e.Type == feedback.TypeReviewDone {
		// review_done is the protocol, not review vocabulary — but it is
		// what require_verdict withholds.
		return s.checkVerdicts(target)
	}
	if err := cfg.Validate(e.Type, e.Text, e.Fields); err != nil {
		return http.StatusBadRequest, err
	}
	if cfg.Locked(e.Block) {
		return http.StatusForbidden, fmt.Errorf("block %s is read-only in this review", e.Block)
	}
	return http.StatusOK, nil
}

// checkVerdicts enforces require_verdict: the review is not done while a
// commentable block has nothing said about it. The error says how many are
// left, so the page can tell the reviewer rather than just refusing.
func (s *Server) checkVerdicts(target *Entry) (int, error) {
	if !s.opts.Review.RequireVerdict {
		return http.StatusOK, nil
	}
	events, err := target.Store.Load()
	if err != nil {
		return http.StatusInternalServerError, err
	}
	answered := make(map[string]bool, len(events))
	for _, ev := range events {
		if ev.Type != feedback.TypeReviewDone {
			answered[ev.Block] = true
		}
	}
	missing := 0
	// The live parse, not the one from startup: under --watch the document
	// may have gained or lost blocks since, and the gate is about what the
	// reviewer is looking at now.
	for _, b := range s.docOf(target).Blocks {
		if !answered[b.ID] && !s.opts.Review.Locked(b.ID) {
			missing++
		}
	}
	if missing > 0 {
		return http.StatusConflict, fmt.Errorf("this review asks for a verdict on every block: %d still to go", missing)
	}
	return http.StatusOK, nil
}

// handleSessionDone marks the whole set reviewed: one review_done per
// document, so an agent watching any single log sees the handover finish.
func (s *Server) handleSessionDone(w http.ResponseWriter, _ *http.Request) {
	written := make([]feedback.Event, 0, len(s.docs))
	for i := range s.docs {
		e := &s.docs[i]
		if code, err := s.checkVerdicts(e); err != nil {
			http.Error(w, fmt.Sprintf("%s: %s", e.Rel, err), code)
			return
		}
	}
	for i := range s.docs {
		e := &s.docs[i]
		event := feedback.Event{Type: feedback.TypeReviewDone, Text: "session"}
		if err := s.append(e, &event); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		written = append(written, event)
	}
	writeJSON(w, http.StatusCreated, written)
}

// append fills in what the page left out and writes the event to that
// document's log.
func (s *Server) append(target *Entry, e *feedback.Event) error {
	e.Doc = s.docOf(target).Path
	if e.Author == "" {
		e.Author = s.opts.Author
	}
	if e.Ts == "" {
		e.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	return target.Store.Append(*e)
}
