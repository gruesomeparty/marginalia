package hosted

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/gruesomeparty/marginalia/internal/storage"
)

// metered sits between the write-through cache and the backend and keeps the
// running size of every log: measured once when the cache reads a log
// through at startup, grown on every append, and released when a prefix is
// moved to the archive. It is how the stored-bytes cap is tracked without
// ever listing the store. Documents are not measured here — their bytes
// ride on the review's metadata as a running total — so what this layer
// sees is exactly the one kind of write it cannot otherwise know about: a
// note being appended by the page or a tool.
type metered struct {
	storage.Store
	h  *Host
	mu sync.Mutex
	// bytes is each log's size as this layer last knew it, by key.
	bytes map[string]int64
	// gate and frozen stop appends under a review's prefix while it is
	// moved, and after: an append already routed to the review's server
	// finishes before the move starts (freeze waits for it), and one
	// arriving later is refused rather than landing beside the archived
	// tree, where a restore would have to choose between the two logs.
	gate   sync.RWMutex
	frozen map[string]bool
}

// errArchived is what an append to a frozen review gets.
var errArchived = errors.New("hosted: this review has been archived")

// freeze refuses appends under prefix from now on, once every append in
// flight has finished.
func (m *metered) freeze(prefix string) {
	m.gate.Lock()
	defer m.gate.Unlock()
	if m.frozen == nil {
		m.frozen = map[string]bool{}
	}
	m.frozen[prefix] = true
}

// thaw lets appends under prefix through again.
func (m *metered) thaw(prefix string) {
	m.gate.Lock()
	defer m.gate.Unlock()
	delete(m.frozen, prefix)
}

func (m *metered) ReadLog(ctx context.Context, key string) ([][]byte, error) {
	lines, err := m.Store.ReadLog(ctx, key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if _, known := m.bytes[key]; !known {
		var n int64
		for _, l := range lines {
			n += int64(len(l)) + 1
		}
		m.bytes[key] = n
		m.h.recordStored(n)
	}
	m.mu.Unlock()
	return lines, nil
}

func (m *metered) AppendLog(ctx context.Context, key string, line []byte) error {
	m.gate.RLock()
	defer m.gate.RUnlock()
	for prefix := range m.frozen {
		if strings.HasPrefix(key, prefix) {
			return errArchived
		}
	}
	if err := m.Store.AppendLog(ctx, key, line); err != nil {
		return err
	}
	n := int64(len(line)) + 1
	m.mu.Lock()
	m.bytes[key] += n
	m.mu.Unlock()
	m.h.recordStored(n)
	return nil
}

func (m *metered) Move(ctx context.Context, from, to string) error {
	if err := m.Store.Move(ctx, from, to); err != nil {
		return err
	}
	var freed int64
	m.mu.Lock()
	for key, n := range m.bytes {
		if strings.HasPrefix(key, from) {
			freed += n
			delete(m.bytes, key)
		}
	}
	m.mu.Unlock()
	m.h.recordStored(-freed)
	return nil
}
