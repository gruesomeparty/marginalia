package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Store is an append-only JSONL feedback log beside a document.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a Store writing to "<docPath>.feedback.jsonl".
func NewStore(docPath string) *Store { return &Store{path: docPath + ".feedback.jsonl"} }

// Path returns the JSONL file path.
func (s *Store) Path() string { return s.path }

// Append writes one event as a JSON line. Never rewrites existing lines.
func (s *Store) Append(e Event) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads all events in chronological (file) order.
func (s *Store) Load() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []Event
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("feedback: line %d: %w", i+1, err)
		}
		events = append(events, e)
	}
	return events, nil
}
