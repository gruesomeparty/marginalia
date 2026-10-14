package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/gruesomeparty/marginalia/internal/storage"
)

// Store is an append-only JSONL feedback log: beside a document on disk in
// every local mode, or under a review's key prefix in whatever backend a
// hosted instance runs on. Either way it is the same two operations over a
// storage.Store, and the backend has no way to rewrite or delete a line.
type Store struct {
	backend storage.Store
	key     string
	// path is where the log is, in the words the operator and the agent
	// use: a file path for the disk backend, the key for any other.
	path string
}

// NewStore returns a Store writing to "<docPath>.feedback.jsonl", on disk,
// beside the document — the file every agent has always tailed, byte for
// byte. The backend is rooted at the document's directory so the key is the
// file's own name and nothing in the layout changes.
func NewStore(docPath string) *Store {
	dir, base := filepath.Split(docPath)
	if dir == "" {
		dir = "."
	}
	return &Store{
		backend: storage.NewDisk(dir),
		key:     base + ".feedback.jsonl",
		path:    docPath + ".feedback.jsonl",
	}
}

// OpenStore returns a Store over any backend, at key. This is what hosted
// mode uses: the key names the review and the document, and the backend is
// whatever --store selected.
func OpenStore(backend storage.Store, key string) *Store {
	return &Store{backend: backend, key: key, path: key}
}

// Path returns where the log is: the JSONL file path on disk, or the key in
// another backend.
func (s *Store) Path() string { return s.path }

// Append writes one event as a JSON line. Never rewrites existing lines —
// the backend has no operation that could.
func (s *Store) Append(e Event) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.backend.AppendLog(context.Background(), s.key, line)
}

// Load reads all events in chronological (append) order.
func (s *Store) Load() ([]Event, error) {
	lines, err := s.backend.ReadLog(context.Background(), s.key)
	if err != nil {
		return nil, err
	}
	var events []Event
	for i, line := range lines {
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("feedback: line %d: %w", i+1, err)
		}
		events = append(events, e)
	}
	return events, nil
}

// Changed returns the log's change token: equal across two calls when nothing
// was appended between them, different after an append. A wait compares
// tokens instead of re-reading the log.
func (s *Store) Changed() (storage.Token, error) {
	return s.backend.LogToken(context.Background(), s.key)
}
