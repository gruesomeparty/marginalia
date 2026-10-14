package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// tmpDir is the one name under a Disk root that is not a key: blobs are
// written there first and linked into place, so a crash mid-write can never
// leave a partial blob under a real key.
const tmpDir = ".marginalia-tmp"

// Disk is the default backend: keys are files under a root directory. It is
// what every local mode uses, with the root being the document's own
// directory and the log key "<doc>.feedback.jsonl" — the file agents have
// always tailed, byte for byte.
type Disk struct {
	root string
	// mu serialises appends. A single O_APPEND write is atomic on every
	// platform CI runs, so this is belt and braces for the one process that
	// holds several handles to the same log.
	mu sync.Mutex
}

// NewDisk returns a backend rooted at dir. The directory need not exist yet;
// it is created on the first write.
func NewDisk(dir string) *Disk { return &Disk{root: dir} }

// Root is the directory keys resolve under.
func (d *Disk) Root() string { return d.root }

func (d *Disk) path(key string) (string, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	if strings.HasPrefix(key, tmpDir+"/") || key == tmpDir {
		return "", fmt.Errorf("%w: %q is reserved", ErrInvalidKey, key)
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

// PutBlob writes to a temporary file, syncs it, then hard-links it under the
// key. link(2) fails if the target exists, which is the put-if-absent check
// and the write in one atomic step — there is no window in which a second
// writer could slip between the two.
func (d *Disk) PutBlob(_ context.Context, key string, data []byte) error {
	target, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	tmp, err := d.writeTemp(data)
	if err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := os.Link(tmp, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("storage: put %s: %w", key, ErrExists)
		}
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

// writeTemp writes data to a fresh file under the temp directory and syncs
// it, returning its path.
func (d *Disk) writeTemp(data []byte) (string, error) {
	dir := filepath.Join(d.root, tmpDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	path := filepath.Join(dir, hex.EncodeToString(b[:]))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (d *Disk) GetBlob(_ context.Context, key string) ([]byte, error) {
	path, err := d.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("storage: get %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return data, nil
}

// List walks the deepest directory the prefix names and keeps the keys that
// start with it. The temp directory is not a key and is skipped.
func (d *Disk) List(_ context.Context, prefix string) ([]string, error) {
	if err := CheckPrefix(prefix); err != nil {
		return nil, err
	}
	start := d.root
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		start = filepath.Join(d.root, filepath.FromSlash(prefix[:i]))
	}
	keys := []string{}
	err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // nothing under this prefix yet
			}
			return err
		}
		rel, relErr := filepath.Rel(d.root, path)
		if relErr != nil {
			return relErr
		}
		key := filepath.ToSlash(rel)
		if entry.IsDir() {
			if key == tmpDir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("storage: list %s: %w", prefix, err)
	}
	sort.Strings(keys)
	return keys, nil
}

// AppendLog is exactly the write the feedback log has always had: open with
// O_APPEND|O_CREATE, write the line and a newline, fsync, close.
func (d *Disk) AppendLog(_ context.Context, key string, line []byte) error {
	path, err := d.path(key)
	if err != nil {
		return err
	}
	if err := checkLine(line); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("storage: append %s: %w", key, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("storage: append %s: %w", key, err)
	}
	if _, err := f.Write(append(bytes.Clone(line), '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("storage: append %s: %w", key, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("storage: append %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("storage: append %s: %w", key, err)
	}
	return nil
}

func (d *Disk) ReadLog(_ context.Context, key string) ([][]byte, error) {
	path, err := d.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read %s: %w", key, err)
	}
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// LogToken is size and mtime, which is what every wait in Marginalia has
// compared since --watch: it cannot miss an append, and it needs no state in
// the process.
func (d *Disk) LogToken(_ context.Context, key string) (Token, error) {
	path, err := d.path(key)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storage: token %s: %w", key, err)
	}
	return Token(fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())), nil
}

// Move relinks each file under fromPrefix beneath toPrefix and unlinks the
// original, one key at a time, so a move interrupted halfway leaves every key
// in exactly one of the two places and a second run finishes the job. A
// target that already exists is the trace of such an interruption — the
// bytes are the same, blobs being immutable — and only the source is removed.
func (d *Disk) Move(ctx context.Context, fromPrefix, toPrefix string) error {
	if err := CheckPrefix(fromPrefix); err != nil {
		return err
	}
	if err := CheckPrefix(toPrefix); err != nil {
		return err
	}
	if fromPrefix == "" {
		return fmt.Errorf("%w: refusing to move everything", ErrInvalidKey)
	}
	keys, err := d.List(ctx, fromPrefix)
	if err != nil {
		return err
	}
	for _, key := range keys {
		src, err := d.path(key)
		if err != nil {
			return err
		}
		dstKey := toPrefix + strings.TrimPrefix(key, fromPrefix)
		dst, err := d.path(dstKey)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("storage: move %s: %w", key, err)
		}
		if err := os.Link(src, dst); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("storage: move %s: %w", key, err)
		}
		if err := os.Remove(src); err != nil {
			return fmt.Errorf("storage: move %s: %w", key, err)
		}
		d.pruneEmpty(filepath.Dir(src))
	}
	return nil
}

// pruneEmpty removes the directories a move emptied, from dir up towards the
// root, stopping at the first one still holding something. Best effort: an
// empty directory left behind is untidy, not wrong.
func (d *Disk) pruneEmpty(dir string) {
	root := filepath.Clean(d.root)
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}
