package cmd

import (
	"bufio"
	"encoding/json"
	"io"
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

	// And the notices went somewhere — just not down the protocol. The
	// review server prints them from its own goroutine, and exec copies
	// the child's stderr on another, so give them a moment to land.
	deadline = time.After(10 * time.Second)
	for !strings.Contains(stderr.String(), "marginalia: serving") {
		select {
		case <-deadline:
			t.Fatalf("the startup notice went nowhere; stderr was:\n%s", stderr.String())
		case <-time.After(50 * time.Millisecond):
		}
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

// An MCP endpoint reachable from other machines with no credential is not a
// configuration anyone meant. The command refuses before it binds anything,
// and names the variable that would make it acceptable.
func TestMCPHTTPOffLoopbackNeedsAToken(t *testing.T) {
	t.Setenv("MARGINALIA_MCP_TOKEN", "")
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"mcp", "--http", "0.0.0.0:0", "--root", t.TempDir()})
	err := executeRoot(root)
	if err == nil {
		t.Fatal("mcp --http 0.0.0.0:0 started with no token set")
	}
	if !strings.Contains(err.Error(), "MARGINALIA_MCP_TOKEN") {
		t.Errorf("the refusal does not name MARGINALIA_MCP_TOKEN: %v", err)
	}
}

// `host` binds a page server and an MCP endpoint on one address, so the same
// rule holds: off loopback, no token means no start.
func TestHostOffLoopbackNeedsAToken(t *testing.T) {
	t.Setenv("MARGINALIA_MCP_TOKEN", "")
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"host", "--addr", "0.0.0.0:0", "--store", "file://" + t.TempDir()})
	err := executeRoot(root)
	if err == nil {
		t.Fatal("host --addr 0.0.0.0:0 started with no token set")
	}
	if !strings.Contains(err.Error(), "MARGINALIA_MCP_TOKEN") {
		t.Errorf("the refusal does not name MARGINALIA_MCP_TOKEN: %v", err)
	}
}

// A store backend that does not exist yet is a feature request, not a typo,
// and the command says so before touching anything.
func TestHostRefusesAnUnknownStoreScheme(t *testing.T) {
	t.Setenv("MARGINALIA_MCP_TOKEN", "tok")
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"host", "--addr", "127.0.0.1:0", "--store", "s3://bucket"})
	err := executeRoot(root)
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("got %v, want a refusal advertising request-feature", err)
	}
}

// A fixed human identity on every page request is a laptop convenience, and
// on any other interface it is everyone being the reviewer.
func TestHostDevIdentityIsLoopbackOnly(t *testing.T) {
	t.Setenv("MARGINALIA_MCP_TOKEN", "tok")
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"host", "--addr", "0.0.0.0:0", "--store", "file://" + t.TempDir(), "--insecure-dev-identity", "me@example.com"})
	err := executeRoot(root)
	if err == nil || !strings.Contains(err.Error(), "insecure-dev-identity") {
		t.Fatalf("got %v, want a refusal naming the flag", err)
	}
}

// Without a dev identity, page routes need the edge's JWT configured — both
// halves of it.
func TestHostNeedsAHumanIdentity(t *testing.T) {
	t.Setenv("MARGINALIA_MCP_TOKEN", "tok")
	for _, args := range [][]string{
		{},
		{"--human-jwt-issuer", "https://team.cloudflareaccess.com"},
		{"--human-jwt-audience", "abc"},
	} {
		root := newRootCmd()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"host", "--addr", "127.0.0.1:0", "--store", "file://" + t.TempDir()}, args...))
		if err := executeRoot(root); err == nil {
			t.Errorf("host %v started with no complete human identity", args)
		}
	}
}

// The public URL is every link the host hands out with a path appended, so
// a value that is not an origin is refused at startup rather than surfacing
// as a broken link in a notification.
func TestPublicOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"":                        "",
		"https://review.example":  "https://review.example",
		"https://review.example/": "https://review.example",
		"http://localhost:8080":   "http://localhost:8080",
	} {
		got, err := publicOrigin(raw)
		if err != nil || got != want {
			t.Errorf("publicOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"review.example",
		"ftp://review.example",
		"https://",
		"https://review.example/marginalia",
		"https://review.example/?a=b",
		"https://review.example/#top",
		"https://user@review.example",
	} {
		if got, err := publicOrigin(raw); err == nil {
			t.Errorf("publicOrigin(%q) = %q, want a refusal", raw, got)
		}
	}
}

// A container is configured without a command line, so the public URL has
// an environment variable like every other setting; it is checked the same
// way the flag is, and it is what the OAuth resource is named after.
func TestHostReadsThePublicURLFromTheEnvironment(t *testing.T) {
	t.Setenv("MARGINALIA_MCP_TOKEN", "tok")
	run := func() error {
		root := newRootCmd()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{"host", "--addr", "127.0.0.1:0", "--store", "file://" + t.TempDir()})
		return executeRoot(root)
	}

	t.Setenv(PublicURLEnv, "review.example")
	if err := run(); err == nil || !strings.Contains(err.Error(), "review.example") {
		t.Fatalf("got %v, want a refusal naming the malformed value", err)
	}

	// With the URL from the environment, an OAuth issuer is satisfied and
	// startup gets as far as asking for a human identity.
	t.Setenv(PublicURLEnv, "https://review.example")
	t.Setenv(OAuthIssuerEnv, "https://auth.example")
	t.Setenv(OAuthJWKSEnv, "https://auth.example/jwks")
	err := run()
	if err == nil || strings.Contains(err.Error(), "public-url") || !strings.Contains(err.Error(), "human identity") {
		t.Fatalf("got %v, want the public URL accepted and the missing human identity named", err)
	}
}

func TestReviewersFromPrefersTheFlagAndDropsBlanks(t *testing.T) {
	if got := reviewersFrom(nil, "a@x, ,b@x,"); strings.Join(got, "|") != "a@x|b@x" {
		t.Errorf("env: %q", got)
	}
	if got := reviewersFrom([]string{"c@x"}, "a@x"); strings.Join(got, "|") != "c@x" {
		t.Errorf("flag over env: %q", got)
	}
	if got := reviewersFrom(nil, ""); got != nil {
		t.Errorf("unset: %q", got)
	}
}
