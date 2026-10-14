package storage

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Memory is a backend that forgets everything when the process does. It is
// the reference implementation the conformance suite is written against, and
// what tests of hosted mode run on so a test of `host` has no hidden os
// dependency.
type Memory struct {
	mu    sync.Mutex
	blobs map[string][]byte
	logs  map[string][][]byte
	// gen counts appends per log, which is all a change token has to do.
	gen map[string]uint64
}

// NewMemory returns an empty in-memory backend.
func NewMemory() *Memory {
	return &Memory{blobs: map[string][]byte{}, logs: map[string][][]byte{}, gen: map[string]uint64{}}
}

func (m *Memory) PutBlob(_ context.Context, key string, data []byte) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.blobs[key]; taken {
		return fmt.Errorf("storage: put %s: %w", key, ErrExists)
	}
	if _, taken := m.logs[key]; taken {
		return fmt.Errorf("storage: put %s: %w", key, ErrExists)
	}
	m.blobs[key] = bytes.Clone(data)
	return nil
}

func (m *Memory) GetBlob(_ context.Context, key string) ([]byte, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.blobs[key]
	if !ok {
		return nil, fmt.Errorf("storage: get %s: %w", key, ErrNotFound)
	}
	return bytes.Clone(data), nil
}

func (m *Memory) List(_ context.Context, prefix string) ([]string, error) {
	if err := CheckPrefix(prefix); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := []string{}
	for key := range m.blobs {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	for key := range m.logs {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (m *Memory) AppendLog(_ context.Context, key string, line []byte) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	if err := checkLine(line); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.blobs[key]; taken {
		return fmt.Errorf("storage: append %s: %w", key, ErrExists)
	}
	m.logs[key] = append(m.logs[key], bytes.Clone(line))
	m.gen[key]++
	return nil
}

func (m *Memory) ReadLog(_ context.Context, key string) ([][]byte, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lines := m.logs[key]
	if lines == nil {
		return nil, nil
	}
	out := make([][]byte, 0, len(lines))
	for _, l := range lines {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		out = append(out, bytes.Clone(l))
	}
	return out, nil
}

func (m *Memory) LogToken(_ context.Context, key string) (Token, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.logs[key]; !ok {
		return "", nil
	}
	return Token(strconv.FormatUint(m.gen[key], 10)), nil
}

func (m *Memory) Move(_ context.Context, fromPrefix, toPrefix string) error {
	if err := CheckPrefix(fromPrefix); err != nil {
		return err
	}
	if err := CheckPrefix(toPrefix); err != nil {
		return err
	}
	if fromPrefix == "" {
		return fmt.Errorf("%w: refusing to move everything", ErrInvalidKey)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, data := range m.blobs {
		if strings.HasPrefix(key, fromPrefix) {
			m.blobs[toPrefix+strings.TrimPrefix(key, fromPrefix)] = data
			delete(m.blobs, key)
		}
	}
	for key, lines := range m.logs {
		if strings.HasPrefix(key, fromPrefix) {
			dst := toPrefix + strings.TrimPrefix(key, fromPrefix)
			m.logs[dst] = lines
			m.gen[dst] = m.gen[key]
			delete(m.logs, key)
			delete(m.gen, key)
		}
	}
	return nil
}
