package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"

	"github.com/gruesomeparty/marginalia/internal/server"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// live is one review being served: the page a human is looking at, and the
// means to stop it. It is in memory and dies with the process — no PID file,
// no port registry, no daemon. That is survivable precisely because every
// comment is on disk the instant it is saved and every read here is
// disk-backed: when the agent's session ends, the human's page stops saving,
// but nothing already said is lost and the next agent turn still reads it all.
type live struct {
	id     string
	url    string
	docs   []string
	cancel context.CancelFunc
	done   chan error // closed when Serve returns, after its shutdown drain
}

func (s *Server) lookup(id string) *live {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

// start builds a review server the same way `serve` does and runs it on an
// ephemeral port, so several reviews can be open at once without the agent
// picking numbers.
func (s *Server) start(paths []string, opts session.Options) (*live, *server.Server, error) {
	opts.Host = s.opts.Host
	opts.Log = s.opts.Log
	srv, err := session.Build(paths, opts)
	if err != nil {
		return nil, nil, err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.opts.Host, opts.Port))
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &live{id: newID(), url: "http://" + ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() {
		l.done <- srv.Serve(ctx, ln)
		close(l.done)
	}()
	return l, srv, nil
}

func (s *Server) remember(l *live) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[l.id] = l
}

// stop cancels one review and waits for its drain, so a comment saved as the
// tool call arrives still reaches disk.
func (s *Server) stop(id string) error {
	s.mu.Lock()
	l := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if l == nil {
		return errUnknownSession(id)
	}
	l.cancel()
	<-l.done
	return nil
}

// Close stops every review this server started and waits for all of them.
func (s *Server) Close() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.stop(id)
	}
}

func errUnknownSession(id string) error {
	return fmt.Errorf("no review session %q is running here — pass `paths` instead: every read works from the log on disk, with or without a server", id)
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
