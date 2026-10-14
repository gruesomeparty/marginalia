package storagetest

import (
	"context"
	"sync/atomic"

	"github.com/gruesomeparty/marginalia/internal/storage"
)

// Counting wraps a store and counts what reaches it, so a test can show that
// a code path costs the backend nothing — the inbox after startup, a wait —
// or exactly what it should: one write per comment.
type Counting struct {
	storage.Store
	reads  atomic.Int64
	writes atomic.Int64
}

// NewCounting wraps s.
func NewCounting(s storage.Store) *Counting { return &Counting{Store: s} }

// Reads is how many GetBlob, List, ReadLog and LogToken calls reached the
// backend.
func (c *Counting) Reads() int64 { return c.reads.Load() }

// Writes is how many PutBlob, AppendLog and Move calls reached the backend.
func (c *Counting) Writes() int64 { return c.writes.Load() }

// Reset zeroes both counters, typically after startup.
func (c *Counting) Reset() { c.reads.Store(0); c.writes.Store(0) }

func (c *Counting) PutBlob(ctx context.Context, key string, data []byte) error {
	c.writes.Add(1)
	return c.Store.PutBlob(ctx, key, data)
}

func (c *Counting) GetBlob(ctx context.Context, key string) ([]byte, error) {
	c.reads.Add(1)
	return c.Store.GetBlob(ctx, key)
}

func (c *Counting) List(ctx context.Context, prefix string) ([]string, error) {
	c.reads.Add(1)
	return c.Store.List(ctx, prefix)
}

func (c *Counting) AppendLog(ctx context.Context, key string, line []byte) error {
	c.writes.Add(1)
	return c.Store.AppendLog(ctx, key, line)
}

func (c *Counting) ReadLog(ctx context.Context, key string) ([][]byte, error) {
	c.reads.Add(1)
	return c.Store.ReadLog(ctx, key)
}

func (c *Counting) LogToken(ctx context.Context, key string) (storage.Token, error) {
	c.reads.Add(1)
	return c.Store.LogToken(ctx, key)
}

func (c *Counting) Move(ctx context.Context, from, to string) error {
	c.writes.Add(1)
	return c.Store.Move(ctx, from, to)
}
