package cmd

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Under stdio MCP, stdout carries JSON-RPC frames. A single startup banner on
// it corrupts the protocol for the whole session — and the failure looks like
// a malformed client, not like a stray Println. So this runs the real binary
// and asserts every stdout line is a message, with the banners on stderr.
func TestMCPKeepsStdoutForTheProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "marginalia")
	build := exec.Command("go", "build", "-o", bin, "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spec.md"), []byte("# Spec\n\nCapped at 500.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "mcp", "--root", root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The child writes stderr from its own goroutine while this one reads it
	// at the end, so the buffer has to be safe to share.
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	send := func(v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stdin.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2026-07-28", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	// Serving a document is what prints the banners, so it must be part of
	// this test rather than initialize alone.
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "review_document", "arguments": map[string]any{"paths": []string{"spec.md"}},
	}})

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	deadline := time.After(60 * time.Second)
	var sawServe bool
	for !sawServe {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("the server closed stdout before answering")
			}
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatalf("stdout carried something that is not a JSON-RPC message — the protocol is corrupted:\n%s", line)
			}
			if id, _ := msg["id"].(float64); id == 2 {
				if _, bad := msg["error"]; bad {
					t.Fatalf("review_document failed: %s", line)
				}
				sawServe = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for the server to answer")
		}
	}
	_ = stdin.Close()

	// And the notices went somewhere — just not down the protocol.
	if notices := stderr.String(); !strings.Contains(notices, "marginalia: serving") {
		t.Errorf("the startup notice went nowhere; stderr was:\n%s", notices)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}
