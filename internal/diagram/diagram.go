// Package diagram renders mermaid diagrams to SVG so a reviewer can look at
// the flow instead of reading it.
//
// Rendering happens here, on the server, through mermaid's own CLI, and the
// result is inlined into the page as SVG. That is what lets the picture reach
// the static share page too: an SVG needs no script, makes no request, and
// survives a strict CSP — none of which is true of shipping mermaid's own
// JavaScript to the browser. When the CLI is not installed the page falls
// back to the anchored source it has always shown; nothing in the review loop
// depends on a renderer being present.
package diagram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTimeout bounds one render. mermaid-cli starts a headless browser, so
// the first call is slow and a hung one must not hold a page open.
const DefaultTimeout = 45 * time.Second

// Renderer turns mermaid source into SVG using mermaid-cli (`mmdc`).
type Renderer struct {
	// Bin is the mmdc executable. Empty means no renderer is available and
	// every call returns ErrUnavailable.
	Bin     string
	Args    []string // extra arguments, e.g. -p for a puppeteer config
	Timeout time.Duration
	// Cache holds rendered SVGs keyed by the source's hash. It lives in the
	// user's cache directory, never beside the document: the document and its
	// folder are the reviewer's, and this is our scratch.
	Cache string
}

// ErrUnavailable reports that no mermaid renderer was found.
var ErrUnavailable = errors.New("diagram: no mermaid renderer (mmdc) available")

// Find locates a renderer: an explicit path first, then MARGINALIA_MMDC, then
// `mmdc` on PATH. The returned Renderer is always usable; ask Available
// before relying on it.
func Find(explicit string) *Renderer {
	r := &Renderer{Timeout: DefaultTimeout, Cache: cacheDir()}
	if extra := strings.Fields(os.Getenv("MARGINALIA_MMDC_ARGS")); len(extra) > 0 {
		r.Args = extra
	}
	for _, candidate := range []string{explicit, os.Getenv("MARGINALIA_MMDC")} {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			r.Bin = path
			return r
		}
		// An explicit path that is not on PATH but is executable as given.
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			r.Bin = candidate
			return r
		}
	}
	if path, err := exec.LookPath("mmdc"); err == nil {
		r.Bin = path
	}
	return r
}

// Available reports whether diagrams can be rendered.
func (r *Renderer) Available() bool { return r != nil && r.Bin != "" }

func cacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "marginalia", "diagrams")
}

// SVG renders mermaid source, returning sanitized SVG. Identical source is
// rendered once: a re-parse under --watch re-renders every diagram in the
// document, and starting a browser per diagram per keystroke would make watch
// mode useless.
func (r *Renderer) SVG(src string) (string, error) {
	if !r.Available() {
		return "", ErrUnavailable
	}
	key := hash(src)
	if svg, ok := r.cached(key); ok {
		return svg, nil
	}
	svg, err := r.render(src)
	if err != nil {
		return "", err
	}
	r.store(key, svg)
	return svg, nil
}

// hash keys the cache by what was rendered and by how we post-process it, so
// a change to either invalidates old entries.
func hash(src string) string {
	sum := sha256.Sum256([]byte("v1\x00" + src))
	return hex.EncodeToString(sum[:])
}

func (r *Renderer) cached(key string) (string, bool) {
	if r.Cache == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(r.Cache, key+".svg"))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// store writes a cache entry, and shrugs off failure: a cache that cannot be
// written is slow, not broken.
func (r *Renderer) store(key, svg string) {
	if r.Cache == "" {
		return
	}
	if err := os.MkdirAll(r.Cache, 0o755); err != nil {
		return
	}
	tmp := filepath.Join(r.Cache, key+".tmp")
	if err := os.WriteFile(tmp, []byte(svg), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, filepath.Join(r.Cache, key+".svg")); err != nil {
		_ = os.Remove(tmp)
	}
}

// render shells out to mermaid-cli. It writes to a temp directory rather than
// beside the document: rendering a review must never put a file in the
// author's tree.
func (r *Renderer) render(src string) (string, error) {
	dir, err := os.MkdirTemp("", "marginalia-diagram-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	in := filepath.Join(dir, "diagram.mmd")
	out := filepath.Join(dir, "diagram.svg")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		return "", err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := append([]string{"-i", in, "-o", out, "-b", "transparent"}, r.Args...)
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", filepath.Base(r.Bin), err, strings.TrimSpace(stderr.String()))
	}
	svg, err := os.ReadFile(out)
	if err != nil {
		return "", fmt.Errorf("%s produced no SVG: %w", filepath.Base(r.Bin), err)
	}
	return sanitize(string(svg)), nil
}
