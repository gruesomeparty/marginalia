package hosted

import (
	"context"
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
