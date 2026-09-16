// Package mcpserver exposes Marginalia over MCP, so an agent hands a document
// to a human with a tool call and reads the answers back as typed results
// instead of scraping a startup banner and parsing JSONL.
//
// Two things about this surface are deliberate and load-bearing:
//
// Reads are backed by the log on disk, not by the running HTTP server. The
// append-only file is the canonical channel — `suggestions` already proves the
// whole document+log+Materialize pipeline works with no server at all — so
// feedback_since, review_status and await_review_done keep working after the
// agent's session ends, and any number of agents can follow the same review
// without coordinating.
//
// No tool writes to a feedback log. The log is the human's answers; an agent
// that could append to it could answer its own question, and review_done in
// particular is the one signal the whole handover exists to produce.
package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// confine resolves a caller-supplied path under the root this server was
// started with, and refuses anything outside it.
//
// A relative path is taken as relative to the root. This is the one genuinely
// new risk MCP brings. A human typing `marginalia
// serve spec.md` chose that file; a tool argument can come from text the model
// read, and `.json`/`.yaml` are supported inputs — so without a root, "review
// ~/.config/gh/hosts.yml" renders an OAuth token onto a listening socket. The
// check runs before anything is parsed or served.
func (s *Server) confine(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths given — name the document to review")
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		// A relative path resolves against the root, not the process's
		// working directory: the agent calling the tool does not share a
		// notion of "here" with this process, and the root is the one
		// location both ends agreed on.
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(s.root, abs)
		}
		abs, err := filepath.Abs(abs)
		if err != nil {
			return nil, err
		}
		// Resolve symlinks so a link inside the root cannot point outside it.
		// A path that does not exist yet has nothing to resolve; let the
		// parser produce the honest "no such file" instead.
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		rel, err := filepath.Rel(s.root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s is outside this server's root (%s) — start `marginalia mcp` with --root to widen it", p, s.root)
		}
		out = append(out, abs)
	}
	return out, nil
}

// resolveRoot fixes the boundary once, at startup, where an operator chose it:
// the --root flag, else MARGINALIA_MCP_ROOT, else the working directory.
func resolveRoot(explicit string) (string, error) {
	root := explicit
	if root == "" {
		root = os.Getenv("MARGINALIA_MCP_ROOT")
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("root %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %s is not a directory", root)
	}
	return abs, nil
}
