package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/web"
)

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
	hashes := make(map[string]string, len(e.Doc.Blocks))
	order := make([]string, 0, len(e.Doc.Blocks))
	for _, b := range e.Doc.Blocks {
		hashes[b.ID] = b.Hash
		order = append(order, b.ID)
	}
	return feedback.Materialize(events, hashes, order)
}

// renderPage renders one document plus the navigation state of the whole set,
// which means reading every log: cheap, and it keeps the sidebar's counts and
// done ticks true on every load.
func (s *Server) renderPage(w http.ResponseWriter, current *Entry) {
	page := web.Page{
		Doc:    current.Doc,
		Author: s.opts.Author,
		Title:  s.opts.Title,
		Nested: s.opts.Nested,
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.Render(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
		"doc":        e.Doc,
		"events":     events,
		"author":     s.opts.Author,
		"docs":       s.docList(),
		"resolution": s.resolution(e, events),
	})
}

// docList describes the served set so an agent reading the API knows what the
// human was handed, not just the document it asked about.
func (s *Server) docList() []map[string]string {
	out := make([]map[string]string, 0, len(s.docs))
	for _, e := range s.docs {
		out = append(out, map[string]string{"path": e.Doc.Path, "rel": e.Rel, "label": e.Label})
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
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if !feedback.ValidType(e.Type) {
		http.Error(w, "invalid event type", http.StatusBadRequest)
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
	if err := s.append(target, &e); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// handleSessionDone marks the whole set reviewed: one review_done per
// document, so an agent watching any single log sees the handover finish.
func (s *Server) handleSessionDone(w http.ResponseWriter, _ *http.Request) {
	written := make([]feedback.Event, 0, len(s.docs))
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
	e.Doc = target.Doc.Path
	if e.Author == "" {
		e.Author = s.opts.Author
	}
	if e.Ts == "" {
		e.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	return target.Store.Append(*e)
}
