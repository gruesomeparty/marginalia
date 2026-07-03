package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

// Options configures a review Server.
type Options struct {
	Doc    *document.Document
	Store  *feedback.Store
	Author string
	Host   string
	Port   int
	Open   bool
}

// Server serves the review page and feedback API.
type Server struct {
	opts Options
	mux  *http.ServeMux
}

// New builds a Server with routes registered.
func New(opts Options) *Server {
	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /api/doc", s.handleDoc)
	s.mux.HandleFunc("GET /api/feedback", s.handleGetFeedback)
	s.mux.HandleFunc("POST /api/feedback", s.handlePostFeedback)
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
	httpSrv := &http.Server{Handler: s.mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("marginalia: serving %s at %s\n", s.opts.Doc.Path, url)
	fmt.Printf("marginalia: feedback → %s\n", s.opts.Store.Path())
	if s.opts.Open {
		_ = openBrowser(url)
	}
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
