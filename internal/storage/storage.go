// Package storage is where Marginalia's persistent state lives: every byte a
// review needs to survive a process ending goes through a Store, and nothing
// else does. The source document `serve` reads, --watch's polling of it and
// the diagram cache are the user's own files or disposable caches, and stay
// on plain os calls.
//
// The interface has two primitives because the state has two shapes, and the
// shape of the API is the invariant:
//
//   - Blobs are immutable. PutBlob is put-if-absent: an existing key cannot be
//     overwritten through this API, so a published snapshot is written once
//     and metadata that changes is written as a new versioned key.
//   - Logs are append-only. There is AppendLog, ReadLog and a change token —
//     and no method that rewrites, truncates or deletes a line, so "feedback
//     events are never rewritten or deleted" is structural rather than a
//     convention each caller keeps.
//
// Plus one prefix move, for archiving: it relocates whole reviews and never
// edits what it moves.
//
// Disk is the default backend everywhere and writes `<doc>.feedback.jsonl`
// byte for byte as it was written before this package existed. R2 (PRD §5.9)
// is the first remote one. Any backend passes the storagetest suite.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotFound is returned by GetBlob for a key nobody has written.
	ErrNotFound = errors.New("storage: not found")
	// ErrExists is returned by PutBlob for a key that already holds a blob.
	// It is the put-if-absent guarantee: the stored bytes are untouched.
	ErrExists = errors.New("storage: key exists")
	// ErrInvalidKey is returned for a key the layout does not allow — empty,
	// absolute, or escaping its prefix with "..".
	ErrInvalidKey = errors.New("storage: invalid key")
	// ErrInvalidLine is returned by AppendLog for a line containing a
	// newline, which would read back as two events.
	ErrInvalidLine = errors.New("storage: a log line cannot contain a newline")
)

// Token is a cheap marker of a log's state. Two tokens compare equal when
// nothing was appended between them, and differ after an append; nothing else
// about them is defined. A wait compares tokens instead of re-reading the log
// — the size+mtime stamp the disk backend has always used, made abstract.
type Token string

// Store is one backend. Keys are slash-separated paths relative to the
// store's root ("reviews/abc/log/spec.md"); a prefix is a string prefix of
// keys, so a caller meaning a directory passes the trailing slash.
//
// Every method is safe for concurrent use. Every error is wrapped, so
// callers test with errors.Is.
type Store interface {
	// PutBlob writes data under key if, and only if, nothing is there yet.
	// It returns ErrExists otherwise and leaves the stored bytes untouched.
	PutBlob(ctx context.Context, key string, data []byte) error
	// GetBlob returns the blob under key, or ErrNotFound.
	GetBlob(ctx context.Context, key string) ([]byte, error)
	// List returns every key with the prefix — blobs and logs alike — in
	// lexical order.
	List(ctx context.Context, prefix string) ([]string, error)

	// AppendLog adds one line to the log under key, creating the log if it
	// does not exist. The line is durable when the call returns.
	AppendLog(ctx context.Context, key string, line []byte) error
	// ReadLog returns every line of the log under key in append order,
	// without the line terminators. A log nobody has appended to is empty,
	// not an error: that is every log before the first comment.
	ReadLog(ctx context.Context, key string) ([][]byte, error)
	// LogToken returns the log's current Token; see Token.
	LogToken(ctx context.Context, key string) (Token, error)

	// Move relocates every key under fromPrefix to the same path under
	// toPrefix. Contents are never changed, and an interrupted move can be
	// run again to completion: keys already moved are simply not under
	// fromPrefix any more.
	Move(ctx context.Context, fromPrefix, toPrefix string) error
}

// CheckKey is the one rule every backend applies to a key before touching
// anything: relative, slash-separated, no empty segment, no "." or "..".
// Exported so a caller composing keys from input (a document name an agent
// sent, say) can refuse early with the same answer the store would give.
func CheckKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "\\\x00") {
			return fmt.Errorf("%w: %q", ErrInvalidKey, key)
		}
	}
	return nil
}

// CheckPrefix is CheckKey for a prefix: the same rules, except that a prefix
// may be empty (everything) or end with a slash (a directory).
func CheckPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	return CheckKey(strings.TrimSuffix(prefix, "/"))
}

// checkLine refuses what would break the one-line-one-event rule.
func checkLine(line []byte) error {
	if bytes.IndexByte(line, '\n') >= 0 {
		return ErrInvalidLine
	}
	return nil
}
