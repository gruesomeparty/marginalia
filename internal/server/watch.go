package server

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// watchInterval is how often --watch looks for a change. Fast enough that a
// save is reflected by the time the reviewer switches windows, slow enough to
// be invisible.
const watchInterval = 500 * time.Millisecond

// stamp is what "the file changed" means here: size and modification time.
type stamp struct {
	size int64
	mod  time.Time
}

func stampOf(path string) (stamp, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return stamp{}, false
	}
	return stamp{size: info.Size(), mod: info.ModTime()}, true
}

// Revision counts re-parses. The page polls it to notice that the document it
// is showing has moved on.
func (s *Server) Revision() uint64 { return s.revision.Load() }

// watch re-parses documents whose file changed, until ctx is cancelled.
//
// It polls rather than using filesystem notifications on purpose: editors save
// by writing a temp file and renaming it over the original, which breaks a
// watch held on the original inode. Comparing size and mtime a couple of times
// a second cannot miss that, needs no dependency, and behaves the same on macOS
// and Linux.
func (s *Server) watch(ctx context.Context) {
	// Start from the stamps taken when the server was built, not from the
	// files as they are now: a save between startup and the first tick would
	// otherwise be adopted as the baseline and never noticed.
	seen := make(map[string]stamp, len(s.stamps))
	for path, st := range s.stamps {
		seen[path] = st
	}
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.rescan(seen)
		}
	}
}

// rescan re-parses every document whose stamp moved. A file that has become
// unreadable or unparseable is left as it was: the reviewer keeps the last good
// render instead of losing the page mid-edit, and the next good save recovers.
func (s *Server) rescan(seen map[string]stamp) {
	for i := range s.docs {
		e := &s.docs[i]
		path := s.docOf(e).Path
		st, ok := stampOf(path)
		if !ok || st == seen[path] {
			continue
		}
		seen[path] = st
		doc, err := document.Parse(path)
		if err != nil {
			continue
		}
		s.setDoc(e, doc)
		s.revision.Add(1)
	}
}

// docOf reads an entry's current parse. Watching swaps it under the handlers,
// so every read goes through here.
func (s *Server) docOf(e *Entry) *document.Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return e.Doc
}

func (s *Server) setDoc(e *Entry, doc *document.Document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Doc = doc
}

// atomic counter type used by Server; kept here beside its reader.
type revisionCounter = atomic.Uint64
