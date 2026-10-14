package storage

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
)

// WriteThrough keeps logs in memory and writes through to a backend. It is
// what a hosted instance runs on: single instance, single writer, so memory
// is authoritative once a log has been read, and every page render, wait
// and inbox listing after startup costs the backend nothing — which matters
// when every backend call is billed. Appends go to the backend first, so a
// line is durable before memory sees it, and the change token is a counter
// that moves with each append rather than anything the backend has to be
// asked for.
//
// Blobs and listings pass straight through: a blob is read once, at startup
// or on publish, and parsed into memory by whoever asked.
//
// The one thing this cannot see is another writer appending to the backend
// behind it. That is the single-writer rule, not a limitation to work
// around: a second instance on the same store is a misconfiguration the deploy
// must prevent.
type WriteThrough struct {
	backend Store
	mu      sync.Mutex
	logs    map[string]*cachedLog
}

type cachedLog struct {
	lines [][]byte
	gen   uint64
}

// NewWriteThrough wraps backend. Nothing is read until asked for.
func NewWriteThrough(backend Store) *WriteThrough {
	return &WriteThrough{backend: backend, logs: map[string]*cachedLog{}}
}

// Backend is the store being written through to.
func (w *WriteThrough) Backend() Store { return w.backend }

func (w *WriteThrough) PutBlob(ctx context.Context, key string, data []byte) error {
	return w.backend.PutBlob(ctx, key, data)
}

func (w *WriteThrough) GetBlob(ctx context.Context, key string) ([]byte, error) {
	return w.backend.GetBlob(ctx, key)
}

func (w *WriteThrough) List(ctx context.Context, prefix string) ([]string, error) {
	return w.backend.List(ctx, prefix)
}

// load returns the cached log, reading it through from the backend the
// first time. Called with mu held.
func (w *WriteThrough) load(ctx context.Context, key string) (*cachedLog, error) {
	if c, ok := w.logs[key]; ok {
		return c, nil
	}
	lines, err := w.backend.ReadLog(ctx, key)
	if err != nil {
		return nil, err
	}
	c := &cachedLog{lines: lines}
	w.logs[key] = c
	return c, nil
}

func (w *WriteThrough) AppendLog(ctx context.Context, key string, line []byte) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	if err := checkLine(line); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// Read through before the first append, so what memory holds afterwards
	// is the whole log and not just the lines written since startup.
	c, err := w.load(ctx, key)
	if err != nil {
		return err
	}
	if err := w.backend.AppendLog(ctx, key, line); err != nil {
		return err
	}
	c.lines = append(c.lines, bytes.Clone(line))
	c.gen++
	return nil
}

func (w *WriteThrough) ReadLog(ctx context.Context, key string) ([][]byte, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	c, err := w.load(ctx, key)
	if err != nil {
		return nil, err
	}
	if c.lines == nil {
		return nil, nil
	}
	out := make([][]byte, len(c.lines))
	for i, l := range c.lines {
		out[i] = bytes.Clone(l)
	}
	return out, nil
}

func (w *WriteThrough) LogToken(ctx context.Context, key string) (Token, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	c, err := w.load(ctx, key)
	if err != nil {
		return "", err
	}
	return Token(strconv.FormatUint(c.gen, 10)), nil
}

// Move moves in the backend and forgets what it held under the old prefix:
// the moved logs read through again from where they now are, the next time
// anyone asks.
func (w *WriteThrough) Move(ctx context.Context, fromPrefix, toPrefix string) error {
	if err := w.backend.Move(ctx, fromPrefix, toPrefix); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for key := range w.logs {
		if strings.HasPrefix(key, fromPrefix) || strings.HasPrefix(key, toPrefix) {
			delete(w.logs, key)
		}
	}
	return nil
}
