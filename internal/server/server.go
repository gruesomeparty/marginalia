package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// Entry is one document of the served review set: its parsed blocks, its own
// append-only log, and how it appears in the navigation tree.
type Entry struct {
	Doc   *document.Document
	Store *feedback.Store
	Label string // display name in the navigation tree
	Rel   string // path relative to the set root; also this document's URL
}

// Options configures a review Server.
type Options struct {
	// Doc and Store serve a single document. Docs serves a review set and
	// takes precedence when both are supplied.
	Doc   *document.Document
	Store *feedback.Store
	Docs  []Entry

	Title    string // session title from an index, if any
	Root     string // directory the documents' Rel paths are relative to
	Index    string // index file that shaped the set, "" when discovered
	Excluded int    // supported files the index left out
	Nested   bool   // group the navigation tree by directory

	// Review is how the agent framed this review: the vocabulary the
	// reviewer answers in, any instructions, and which blocks are read-only.
	// Nil means the default review — the built-in actions and nothing else.
	Review *review.Config
	// Theme is the palette the pages are painted in (web.ThemeFor).
	Theme web.Theme

	Author string
	Host   string
	Port   int
	Open   bool
	Watch  bool // re-parse a document when its file changes
}

// Server serves the review pages and feedback API.
type Server struct {
	opts   Options
	docs   []Entry
	byRel  map[string]*Entry
	byPath map[string]*Entry
	mux    *http.ServeMux

	// mu guards each entry's parsed document, which --watch replaces while
	// handlers are reading it.
	mu       sync.RWMutex
	revision revisionCounter
	// stamps records each document's file as it was when the server was
	// built, so the watcher's baseline predates anything it should catch.
	stamps map[string]stamp
}

// New builds a Server with routes registered.
func New(opts Options) *Server {
	if opts.Review == nil {
		opts.Review = review.Default()
	}
	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.docs = opts.Docs
	if len(s.docs) == 0 && opts.Doc != nil {
		s.docs = []Entry{{Doc: opts.Doc, Store: opts.Store, Label: opts.Doc.Path, Rel: opts.Doc.Path}}
	}
	s.byRel = make(map[string]*Entry, len(s.docs))
	s.byPath = make(map[string]*Entry, len(s.docs))
	s.stamps = make(map[string]stamp, len(s.docs))
	for i := range s.docs {
		e := &s.docs[i]
		s.byRel[e.Rel] = e
		s.byPath[e.Doc.Path] = e
		if st, ok := stampOf(e.Doc.Path); ok {
			s.stamps[e.Doc.Path] = st
		}
	}
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /d/{doc...}", s.handleDocPage)
	s.mux.HandleFunc("GET /api/doc", s.handleDoc)
	s.mux.HandleFunc("GET /api/feedback", s.handleGetFeedback)
	s.mux.HandleFunc("GET /api/resolution", s.handleResolution)
	s.mux.HandleFunc("GET /api/revision", s.handleRevision)
	s.mux.HandleFunc("POST /api/feedback", s.handlePostFeedback)
	s.mux.HandleFunc("POST /api/session_done", s.handleSessionDone)
	return s
}

// Handler exposes the mux for tests.
func (s *Server) Handler() http.Handler { return s.mux }

// Run starts the server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve runs the HTTP server on ln until ctx is cancelled, then shuts down
// gracefully — and waits for the drain to finish before returning, so a
// comment saved as the reviewer closes the tab is on disk before the process
// exits.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	httpSrv := &http.Server{Handler: s.mux, ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan struct{})
	defer close(stopped)
	drained := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-stopped:
			return // Serve failed on its own; there is nothing to drain
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drained <- httpSrv.Shutdown(shutCtx)
	}()
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	s.announce(url)
	if s.opts.Watch {
		go s.watch(ctx)
	}
	if s.opts.Open {
		_ = openBrowser(url)
	}
	err := httpSrv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ErrServerClosed means Shutdown was called, which only happens above:
	// wait for the in-flight requests it is draining.
	return <-drained
}

// announce prints where the review is and where feedback lands. A curated set
// says how many supported files its index left out, so a mistyped index entry
// reads as an exclusion rather than as an empty folder.
func (s *Server) announce(url string) {
	// The review's own vocabulary last, so it is the line above the prompt:
	// an agent that configured actions needs to see they took.
	defer s.announceReview()
	if s.opts.Watch {
		defer fmt.Println("marginalia: watching for changes; the page offers a reload when a document moves on")
	}
	if len(s.docs) == 1 {
		fmt.Printf("marginalia: serving %s at %s\n", s.docs[0].Doc.Path, url)
		fmt.Printf("marginalia: feedback → %s\n", s.docs[0].Store.Path())
		return
	}
	fmt.Printf("marginalia: serving %d documents at %s\n", len(s.docs), url)
	for _, e := range s.docs {
		fmt.Printf("marginalia:   %s → %s\n", e.Rel, e.Store.Path())
	}
	if s.opts.Index != "" {
		fmt.Printf("marginalia: set curated by %s", s.opts.Index)
		if s.opts.Excluded > 0 {
			fmt.Printf("; %d supported file(s) under %s excluded by it", s.opts.Excluded, s.opts.Root)
		}
		fmt.Println()
	}
}

// strayNotes names the documents that requester notes address but this server
// does not serve, in the order they were configured.
func (s *Server) strayNotes() []string {
	served := make(map[string]bool, 2*len(s.docs))
	for i := range s.docs {
		e := &s.docs[i]
		served[e.Rel] = true
		served[s.docOf(e).Path] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range s.opts.Review.Notes {
		if n.Doc == "" || served[n.Doc] || seen[n.Doc] {
			continue
		}
		seen[n.Doc] = true
		out = append(out, n.Doc)
	}
	return out
}

// announceReview says what the review asks for, when it asks for anything
// beyond the default: the words the reviewer will answer in are the words the
// agent has to read back out of the log.
func (s *Server) announceReview() {
	cfg := s.opts.Review
	if len(cfg.Custom) == 0 && len(cfg.ReadOnly) == 0 && len(cfg.Skip) == 0 &&
		len(cfg.Notes) == 0 && !cfg.RequireVerdict && cfg.Instructions == "" {
		return
	}
	var says []string
	for _, a := range cfg.Actions() {
		says = append(says, a.Type)
	}
	fmt.Printf("marginalia: review actions: %s\n", strings.Join(says, ", "))
	if len(cfg.ReadOnly) > 0 {
		fmt.Printf("marginalia: read-only: %s\n", strings.Join(cfg.ReadOnly, ", "))
	}
	if len(cfg.Skip) > 0 {
		fmt.Printf("marginalia: skipped: %s\n", strings.Join(cfg.Skip, ", "))
	}
	if n := len(cfg.Notes); n > 0 {
		fmt.Printf("marginalia: %d note(s) from the requester on the page\n", n)
	}
	// A note that names a document this server does not serve would render
	// nowhere at all. Say so: silently dropping the agent's own question is
	// the one outcome worse than a wrong anchor.
	for _, name := range s.strayNotes() {
		fmt.Printf("marginalia: note for %q — no such document in this review\n", name)
	}
	if cfg.RequireVerdict {
		fmt.Println("marginalia: every block needs a verdict before the review can be marked done")
	}
}
