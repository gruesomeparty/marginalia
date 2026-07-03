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

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	events, err := s.opts.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.Render(w, s.opts.Doc, events, s.opts.Author); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDoc(w http.ResponseWriter, _ *http.Request) {
	events, err := s.opts.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"doc": s.opts.Doc, "events": events, "author": s.opts.Author})
}

func (s *Server) handleGetFeedback(w http.ResponseWriter, _ *http.Request) {
	events, err := s.opts.Store.Load()
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
	if e.Doc == "" {
		e.Doc = s.opts.Doc.Path
	}
	if e.Author == "" {
		e.Author = s.opts.Author
	}
	if e.Ts == "" {
		e.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.opts.Store.Append(e); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}
