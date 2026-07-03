# Marginalia M1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship PRD milestone M1 — a Go binary that serves a markdown document as a block-anchored review page, persisting reviewer feedback as append-only JSONL — plus the full CI/CD pipeline and an installable Claude plugin.

**Architecture:** Single Go binary (cobra subcommands). `internal/document` walks goldmark's AST to assign each top-level block a stable ID (`section/ordinal`), a 90-char quote, and a 12-hex-char content hash. `internal/feedback` is an append-only JSONL store. `internal/server` serves a self-contained HTML page (`internal/web`, `go:embed`) that POSTs each comment straight to disk. CI/CD adds strict testing, coverage gating, CodeQL, Renovate/Dependabot, and release-please + GoReleaser. The repo doubles as its own Claude plugin marketplace.

**Tech Stack:** Go 1.26, spf13/cobra, yuin/goldmark (+GFM), stdlib `net/http` (Go 1.22 method-routing mux), GitHub Actions, golangci-lint v2, GoReleaser v2, release-please v4.

## Global Constraints

- Module path: `github.com/suTerminus/marginalia` — copy verbatim in every import.
- Go version floor: `1.26`.
- **Invariant — never mutate the source document.** Only ever read the input `.md`.
- **Invariant — feedback is append-only.** Only `O_APPEND`; never truncate/rewrite `<doc>.feedback.jsonl`.
- **Invariant — no export step in server mode.** Every comment POSTs to disk immediately.
- **Invariant — never use the Clipboard API** anywhere in the web page.
- **Invariant — self-contained page.** Inline CSS/JS only, no external requests, system fonts, CSP-safe.
- Commit messages MUST be Conventional Commits (`feat:`, `fix:`, `chore:`, `docs:`, `test:`, `ci:`, `build:`) — release-please depends on this. Every commit ends with the `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer.
- Version-sensitive tooling — **before writing these config files, verify current schema via Context7 or official docs**: golangci-lint (v2 config), GoReleaser (v2), release-please (v4 action + config), Claude Code plugin `plugin.json` + `marketplace.json`.

---

## Shared Interfaces (locked — all tasks must match these names/types exactly)

**`internal/document`**
```go
type Block struct {
	ID        string `json:"id"`      // "5.3/2"
	Section   string `json:"section"` // "5.3"
	Ordinal   int    `json:"ordinal"` // 1-based within section, counts the heading
	Kind      string `json:"kind"`    // heading|paragraph|list|code|table|blockquote|thematic_break|...
	Level     int    `json:"level"`   // heading level, 0 otherwise
	Quote     string `json:"quote"`   // first ~90 runes of plain text
	Hash      string `json:"hash"`    // sha256(normalized plain text)[:12]
	HTML      string `json:"html"`    // goldmark-rendered HTML of this block only
	PlainText string `json:"text"`    // normalized plain text
}
type Document struct {
	Path   string  `json:"path"`
	Blocks []Block `json:"blocks"`
}
func Parse(path string) (*Document, error)
func ParseBytes(path string, src []byte) (*Document, error)
```

**`internal/feedback`**
```go
type Event struct {
	Doc    string `json:"doc"`
	Block  string `json:"block"`
	Quote  string `json:"quote"`
	Hash   string `json:"hash"`
	Type   string `json:"type"`
	Text   string `json:"text"`
	Author string `json:"author"`
	Ts     string `json:"ts"`
}
const (
	TypeComment=".."; TypeSuggestEdit=".."; TypeQuestion=".."; TypeApprove=".."; TypeReject=".."; TypeReviewDone=".."
) // exact string values in Task 3
func ValidType(t string) bool
type Store struct { /* path string; mu sync.Mutex */ }
func NewStore(docPath string) *Store   // path = docPath + ".feedback.jsonl"
func (s *Store) Path() string
func (s *Store) Append(e Event) error
func (s *Store) Load() ([]Event, error)
func Resolve(events []Event) map[string]Event
```

**`internal/web`**
```go
func Render(w io.Writer, doc *document.Document, events []feedback.Event, author string) error
```

**`internal/server`**
```go
type Options struct { Doc *document.Document; Store *feedback.Store; Author, Host string; Port int; Open bool }
type Server struct { /* ... */ }
func New(opts Options) *Server
func (s *Server) Handler() http.Handler   // for tests
func (s *Server) Run(ctx context.Context) error
```

**`cmd`**
```go
var version, commit, date string  // ldflags-injected
func Execute()                    // calls os.Exit on error
```

---

## Phase A — Go binary (M1)

### Task 1: Go module + CLI skeleton

**Files:**
- Create: `go.mod` (via `go mod init`)
- Create: `main.go`
- Create: `cmd/root.go`, `cmd/version.go`
- Test: `cmd/version_test.go`

**Interfaces:**
- Produces: `cmd.Execute()`, `cmd.version/commit/date` vars, `marginalia version` subcommand.

- [ ] **Step 1: Init module + deps**

```bash
go mod init github.com/suTerminus/marginalia
go get github.com/spf13/cobra@latest
go get github.com/yuin/goldmark@latest
go mod tidy
```
Expected: `go.mod` declares `module github.com/suTerminus/marginalia` and `go 1.26`.

- [ ] **Step 2: Write the failing test**

`cmd/version_test.go`:
```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	version = "1.2.3"
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Fatalf("version output %q missing version", out.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./cmd/ -run TestVersionCommand`
Expected: FAIL — `newRootCmd` undefined.

- [ ] **Step 4: Implement**

`main.go`:
```go
package main

import "github.com/suTerminus/marginalia/cmd"

func main() { cmd.Execute() }
```

`cmd/root.go`:
```go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "marginalia",
		Short:         "Hand a document to a human for block-anchored review",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// Execute runs the CLI and exits non-zero on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

`cmd/version.go`:
```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("marginalia %s (commit %s, built %s)\n", version, commit, date)
			return nil
		},
	}
}
```

- [ ] **Step 5: Run tests + build**

Run: `go test ./... && go build ./... && go run . version`
Expected: PASS; build succeeds; `marginalia dev (commit none, built unknown)`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum main.go cmd/
git commit -m "feat: scaffold cobra CLI with version command"
```

---

### Task 2: `internal/document` — block anchoring

**Files:**
- Create: `internal/document/block.go`, `internal/document/parse.go`, `internal/document/anchor.go`
- Test: `internal/document/anchor_test.go`, `internal/document/parse_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Block`, `Document`, `Parse`, `ParseBytes` (see Shared Interfaces).

- [ ] **Step 1: Write failing tests**

`internal/document/anchor_test.go`:
```go
package document

import "testing"

func TestSectionPathsAndOrdinals(t *testing.T) {
	src := []byte("intro para\n\n# First\n\npara under first\n\n## Nested\n\ndeep para\n")
	doc, err := ParseBytes("t.md", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ id, kind string }{
		{"0/1", "paragraph"},
		{"1/1", "heading"},
		{"1/2", "paragraph"},
		{"1.1/1", "heading"},
		{"1.1/2", "paragraph"},
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(doc.Blocks), len(want), doc.Blocks)
	}
	for i, w := range want {
		if doc.Blocks[i].ID != w.id || doc.Blocks[i].Kind != w.kind {
			t.Errorf("block %d = %s/%s, want %s/%s", i, doc.Blocks[i].ID, doc.Blocks[i].Kind, w.id, w.kind)
		}
	}
}

func TestQuoteAndHashStable(t *testing.T) {
	a, _ := ParseBytes("t.md", []byte("# Same text here\n"))
	b, _ := ParseBytes("other.md", []byte("#    Same   text here   \n"))
	if a.Blocks[0].Hash != b.Blocks[0].Hash {
		t.Errorf("hash not whitespace-stable: %s vs %s", a.Blocks[0].Hash, b.Blocks[0].Hash)
	}
	if len(a.Blocks[0].Hash) != 12 {
		t.Errorf("hash len = %d, want 12", len(a.Blocks[0].Hash))
	}
}

func TestQuoteTruncation(t *testing.T) {
	long := "word " // build > 90 runes
	for i := 0; i < 40; i++ {
		long += "word "
	}
	doc, _ := ParseBytes("t.md", []byte(long))
	q := doc.Blocks[0].Quote
	if []rune(q)[len([]rune(q))-1] != '…' {
		t.Errorf("expected ellipsis, got %q", q)
	}
	if len([]rune(q)) > 91 {
		t.Errorf("quote too long: %d runes", len([]rune(q)))
	}
}

func TestCodeBlockText(t *testing.T) {
	doc, _ := ParseBytes("t.md", []byte("```\nfmt.Println(\"hi\")\n```\n"))
	if doc.Blocks[0].Kind != "code" {
		t.Fatalf("kind = %s, want code", doc.Blocks[0].Kind)
	}
	if doc.Blocks[0].Hash == "" {
		t.Fatal("code block hash empty")
	}
}

func TestNoHeadings(t *testing.T) {
	doc, _ := ParseBytes("t.md", []byte("just one paragraph\n"))
	if doc.Blocks[0].ID != "0/1" {
		t.Errorf("id = %s, want 0/1", doc.Blocks[0].ID)
	}
}
```

`internal/document/parse_test.go`:
```go
package document

import "testing"

func TestParseTable(t *testing.T) {
	src := []byte("| a | b |\n|---|---|\n| 1 | 2 |\n")
	doc, err := ParseBytes("t.md", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != "table" {
		t.Fatalf("got %+v, want single table block", doc.Blocks)
	}
	if doc.Blocks[0].HTML == "" {
		t.Fatal("table HTML empty")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/document/`
Expected: FAIL — `ParseBytes` undefined.

- [ ] **Step 3: Implement `block.go`**

```go
package document

// Block is a commentable top-level element of a document.
type Block struct {
	ID        string `json:"id"`
	Section   string `json:"section"`
	Ordinal   int    `json:"ordinal"`
	Kind      string `json:"kind"`
	Level     int    `json:"level"`
	Quote     string `json:"quote"`
	Hash      string `json:"hash"`
	HTML      string `json:"html"`
	PlainText string `json:"text"`
}

// Document is a parsed source document.
type Document struct {
	Path   string  `json:"path"`
	Blocks []Block `json:"blocks"`
}
```

- [ ] **Step 4: Implement `anchor.go`**

```go
package document

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func quote(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

func headingLevel(n ast.Node) int {
	if h, ok := n.(*ast.Heading); ok {
		return h.Level
	}
	return 0
}

func kindOf(n ast.Node) string {
	switch n.Kind() {
	case ast.KindHeading:
		return "heading"
	case ast.KindParagraph:
		return "paragraph"
	case ast.KindList:
		return "list"
	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		return "code"
	case ast.KindBlockquote:
		return "blockquote"
	case ast.KindThematicBreak:
		return "thematic_break"
	case east.KindTable:
		return "table"
	}
	return strings.ToLower(n.Kind().String())
}

// nodeText extracts the plain text of a block node.
func nodeText(n ast.Node, source []byte) string {
	switch n.Kind() {
	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		var sb strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			sb.Write(lines.At(i).Value(source))
		}
		return sb.String()
	}
	var sb strings.Builder
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(source))
			if t.SoftLineBreak() || t.HardLineBreak() {
				sb.WriteByte(' ')
			}
		case *ast.String:
			sb.Write(t.Value)
		case *ast.AutoLink:
			sb.Write(t.URL(source))
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

// sectioner tracks heading counters to produce hierarchical section paths.
type sectioner struct {
	counters [7]int
	section  string
	ordinal  int
}

func newSectioner() *sectioner { return &sectioner{section: "0"} }

func (s *sectioner) next(n ast.Node) (section string, ordinal int) {
	if h, ok := n.(*ast.Heading); ok {
		lvl := h.Level
		if lvl < 1 {
			lvl = 1
		}
		if lvl > 6 {
			lvl = 6
		}
		s.counters[lvl]++
		for i := lvl + 1; i <= 6; i++ {
			s.counters[i] = 0
		}
		parts := make([]string, 0, lvl)
		for i := 1; i <= lvl; i++ {
			parts = append(parts, strconv.Itoa(s.counters[i]))
		}
		s.section = strings.Join(parts, ".")
		s.ordinal = 0
	}
	s.ordinal++
	return s.section, s.ordinal
}

```

- [ ] **Step 5: Implement `parse.go`**

```go
package document

import (
	"bytes"
	"fmt"
	"os"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Parse reads and parses a markdown file into a Document.
func Parse(path string) (*Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(path, src)
}

// ParseBytes parses markdown bytes into a Document.
func ParseBytes(path string, src []byte) (*Document, error) {
	root := md.Parser().Parse(text.NewReader(src))
	doc := &Document{Path: path}
	sec := newSectioner()
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		section, ordinal := sec.next(n)
		plain := normalize(nodeText(n, src))
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, src, n); err != nil {
			return nil, fmt.Errorf("render block %s/%d: %w", section, ordinal, err)
		}
		doc.Blocks = append(doc.Blocks, Block{
			ID:        fmt.Sprintf("%s/%d", section, ordinal),
			Section:   section,
			Ordinal:   ordinal,
			Kind:      kindOf(n),
			Level:     headingLevel(n),
			Quote:     quote(plain, 90),
			Hash:      hashText(plain),
			HTML:      buf.String(),
			PlainText: plain,
		})
	}
	return doc, nil
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/document/ -v`
Expected: PASS all.

- [ ] **Step 7: Commit**

```bash
git add internal/document/
git commit -m "feat: block anchoring — section paths, quotes, content hashes"
```

---

### Task 3: `internal/feedback` — append-only event store

**Files:**
- Create: `internal/feedback/event.go`, `internal/feedback/store.go`, `internal/feedback/resolve.go`
- Test: `internal/feedback/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Event`, type constants, `ValidType`, `Store`, `NewStore`, `Append`, `Load`, `Path`, `Resolve`.

- [ ] **Step 1: Write failing tests**

`internal/feedback/store_test.go`:
```go
package feedback

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestNewStorePath(t *testing.T) {
	s := NewStore("specs/foo.md")
	if s.Path() != "specs/foo.md.feedback.jsonl" {
		t.Fatalf("path = %s", s.Path())
	}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "d.md")
	s := NewStore(doc)
	in := Event{Doc: doc, Block: "1/2", Type: TypeComment, Text: "hi", Author: "b", Ts: "2026-07-03T10:00:00Z"}
	if err := s.Append(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "hi" || got[0].Block != "1/2" {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "none.md"))
	got, err := s.Load()
	if err != nil || got != nil {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestConcurrentAppend(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "c.md"))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Append(Event{Block: "1/1", Type: TypeComment, Text: "x"})
		}()
	}
	wg.Wait()
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("got %d events, want 50", len(got))
	}
}

func TestResolveLatestPerBlock(t *testing.T) {
	events := []Event{
		{Block: "1/1", Type: TypeComment, Text: "old", Ts: "2026-07-03T10:00:00Z"},
		{Block: "1/1", Type: TypeComment, Text: "new", Ts: "2026-07-03T11:00:00Z"},
		{Block: "2/1", Type: TypeApprove, Ts: "2026-07-03T10:30:00Z"},
		{Type: TypeReviewDone, Ts: "2026-07-03T12:00:00Z"},
	}
	got := Resolve(events)
	if got["1/1"].Text != "new" {
		t.Errorf("1/1 = %q, want new", got["1/1"].Text)
	}
	if _, ok := got[""]; ok {
		t.Error("review_done (empty block) must not appear in resolution")
	}
}

func TestValidType(t *testing.T) {
	if !ValidType("comment") || ValidType("bogus") {
		t.Fatal("ValidType wrong")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/feedback/`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement `event.go`**

```go
package feedback

// Event is one append-only feedback record.
type Event struct {
	Doc    string `json:"doc"`
	Block  string `json:"block"`
	Quote  string `json:"quote"`
	Hash   string `json:"hash"`
	Type   string `json:"type"`
	Text   string `json:"text"`
	Author string `json:"author"`
	Ts     string `json:"ts"`
}

// Feedback event types.
const (
	TypeComment     = "comment"
	TypeSuggestEdit = "suggest_edit"
	TypeQuestion    = "question"
	TypeApprove     = "approve"
	TypeReject      = "reject"
	TypeReviewDone  = "review_done"
)

// ValidType reports whether t is a known event type.
func ValidType(t string) bool {
	switch t {
	case TypeComment, TypeSuggestEdit, TypeQuestion, TypeApprove, TypeReject, TypeReviewDone:
		return true
	}
	return false
}
```

- [ ] **Step 4: Implement `store.go`**

```go
package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Store is an append-only JSONL feedback log beside a document.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a Store writing to "<docPath>.feedback.jsonl".
func NewStore(docPath string) *Store { return &Store{path: docPath + ".feedback.jsonl"} }

// Path returns the JSONL file path.
func (s *Store) Path() string { return s.path }

// Append writes one event as a JSON line. Never rewrites existing lines.
func (s *Store) Append(e Event) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Load reads all events in chronological (file) order.
func (s *Store) Load() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []Event
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("feedback: line %d: %w", i+1, err)
		}
		events = append(events, e)
	}
	return events, nil
}
```

- [ ] **Step 5: Implement `resolve.go`**

```go
package feedback

// Resolve returns the latest event per block (chronological by Ts).
// Events with an empty Block (e.g. review_done) are excluded.
func Resolve(events []Event) map[string]Event {
	latest := make(map[string]Event)
	for _, e := range events {
		if e.Block == "" {
			continue
		}
		if cur, ok := latest[e.Block]; !ok || e.Ts >= cur.Ts {
			latest[e.Block] = e
		}
	}
	return latest
}
```

- [ ] **Step 6: Run tests with race**

Run: `go test -race ./internal/feedback/ -v`
Expected: PASS, no race.

- [ ] **Step 7: Commit**

```bash
git add internal/feedback/
git commit -m "feat: append-only JSONL feedback store with resolution view"
```

---

### Task 4: `internal/web` — self-contained review page

**Files:**
- Create: `internal/web/web.go`, `internal/web/review.html.tmpl`
- Test: `internal/web/render_test.go`

**Interfaces:**
- Consumes: `document.Document`, `feedback.Event`.
- Produces: `web.Render(w, doc, events, author)`.

- [ ] **Step 1: Write failing test**

`internal/web/render_test.go`:
```go
package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

func TestRenderSelfContained(t *testing.T) {
	doc, _ := document.ParseBytes("d.md", []byte("# Title\n\nHello world.\n"))
	var buf bytes.Buffer
	if err := Render(&buf, doc, nil, "berkay"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`data-block="1/1"`, `data-block="1/2"`, "window.__MARGINALIA__", "berkay"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// self-contained + CSP-safe: no external resources, no clipboard
	for _, bad := range []string{"http://", "https://", "src=\"//", "cdn", "clipboard", "navigator.clipboard"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains forbidden token %q", bad)
		}
	}
}

func TestRenderHydratesEvents(t *testing.T) {
	doc, _ := document.ParseBytes("d.md", []byte("para\n"))
	ev := []feedback.Event{{Doc: "d.md", Block: "0/1", Type: "comment", Text: "note", Ts: "2026-07-03T10:00:00Z"}}
	var buf bytes.Buffer
	if err := Render(&buf, doc, ev, "a"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "note") {
		t.Error("existing event not embedded")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/web/`
Expected: FAIL — `Render` undefined.

- [ ] **Step 3: Implement `web.go`**

```go
package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

//go:embed review.html.tmpl
var files embed.FS

var tmpl = template.Must(template.New("review.html.tmpl").
	Funcs(template.FuncMap{"safe": func(s string) template.HTML { return template.HTML(s) }}).
	ParseFS(files, "review.html.tmpl"))

type payload struct {
	Doc    *document.Document `json:"doc"`
	Events []feedback.Event   `json:"events"`
	Author string             `json:"author"`
}

type viewData struct {
	Doc      *document.Document
	Author   string
	DataJSON template.JS
}

// Render writes the self-contained review page.
func Render(w io.Writer, doc *document.Document, events []feedback.Event, author string) error {
	if events == nil {
		events = []feedback.Event{}
	}
	raw, err := json.Marshal(payload{Doc: doc, Events: events, Author: author})
	if err != nil {
		return err
	}
	return tmpl.Execute(w, viewData{Doc: doc, Author: author, DataJSON: template.JS(raw)})
}
```

- [ ] **Step 4: Implement `review.html.tmpl`**

Write the full template below verbatim. It is a complete HTML document — no external requests, system fonts, ~68ch column, keyboard-accessible blocks, `prefers-reduced-motion`, no Clipboard API.

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Marginalia — {{.Doc.Path}}</title>
<style>
:root{--bg:#fbfbf9;--fg:#1a1a1a;--muted:#666;--rule:#e3e3dd;--accent:#3b5bdb;--marker:#e8b339;--card:#fff;--shadow:0 1px 3px rgba(0,0,0,.12)}
@media (prefers-color-scheme:dark){:root{--bg:#1a1a1a;--fg:#e8e8e6;--muted:#9a9a94;--rule:#333;--accent:#8aa0ff;--card:#242424;--shadow:0 1px 3px rgba(0,0,0,.5)}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font-family:Georgia,'Times New Roman',serif;line-height:1.6}
header{position:sticky;top:0;z-index:10;background:var(--bg);border-bottom:1px solid var(--rule);padding:.6rem 1rem;display:flex;align-items:center;gap:1rem;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:.85rem}
header .count{color:var(--muted)}
header button{margin-left:auto;font:inherit;padding:.4rem .9rem;border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:6px;cursor:pointer}
main{max-width:68ch;margin:2rem auto;padding:0 2.5rem}
h1,h2,h3,h4,h5,h6{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;line-height:1.25}
code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
pre{background:var(--card);padding:1rem;border-radius:6px;overflow:auto;font-size:.9em}
:not(pre)>code{background:var(--card);padding:.1em .35em;border-radius:4px;font-size:.9em}
table{border-collapse:collapse;width:100%}
th,td{border:1px solid var(--rule);padding:.4rem .6rem}
blockquote{margin:0;padding-left:1rem;border-left:3px solid var(--rule);color:var(--muted)}
.block{position:relative;border-radius:6px;padding:.15rem .5rem;margin:.2rem -.5rem;outline:none;transition:background .15s}
@media (prefers-reduced-motion:reduce){.block,.toast{transition:none}}
.block:hover{background:color-mix(in srgb,var(--accent) 7%,transparent)}
.block:focus-visible{box-shadow:0 0 0 2px var(--accent)}
.block[data-count]:not([data-count="0"])::before{content:attr(data-count);position:absolute;left:-2.4rem;top:.3rem;min-width:1.3rem;height:1.3rem;line-height:1.3rem;text-align:center;background:var(--marker);color:#000;border-radius:50%;font-size:.72rem;font-family:-apple-system,sans-serif}
.composer{margin:.4rem 0 .8rem;background:var(--card);border:1px solid var(--rule);border-radius:8px;padding:.8rem;box-shadow:var(--shadow);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:.9rem}
.composer .chips{display:flex;gap:.4rem;margin-bottom:.5rem;flex-wrap:wrap}
.composer .chip{padding:.25rem .7rem;border:1px solid var(--rule);border-radius:999px;background:transparent;color:var(--fg);cursor:pointer;font:inherit}
.composer .chip[aria-pressed="true"]{background:var(--accent);color:#fff;border-color:var(--accent)}
.composer textarea{width:100%;min-height:4rem;font:inherit;padding:.5rem;border:1px solid var(--rule);border-radius:6px;background:var(--bg);color:var(--fg);resize:vertical}
.composer .actions{display:flex;gap:.5rem;margin-top:.5rem}
.composer .actions button{font:inherit;padding:.35rem .8rem;border-radius:6px;cursor:pointer;border:1px solid var(--rule);background:transparent;color:var(--fg)}
.composer .actions .save{background:var(--accent);color:#fff;border-color:var(--accent)}
.existing{margin:.3rem 0 .6rem;font-family:-apple-system,sans-serif;font-size:.82rem}
.existing .item{padding:.4rem .6rem;border-left:2px solid var(--marker);margin:.3rem 0;background:var(--card);border-radius:0 6px 6px 0}
.existing .item .type{font-weight:600;text-transform:capitalize;color:var(--accent)}
.existing .item.stale{opacity:.65}
.toast{position:fixed;bottom:1rem;left:50%;transform:translateX(-50%);background:var(--fg);color:var(--bg);padding:.5rem 1rem;border-radius:6px;font-family:sans-serif;font-size:.85rem;opacity:0;transition:opacity .2s;pointer-events:none}
.toast.show{opacity:1}
</style>
</head>
<body>
<header>
  <strong>{{.Doc.Path}}</strong>
  <span class="count" id="count">0 comments</span>
  <button id="done" type="button">Done</button>
</header>
<main id="doc">
  {{range .Doc.Blocks}}
  <div class="block" data-block="{{.ID}}" data-hash="{{.Hash}}" data-quote="{{.Quote}}" tabindex="0" role="button" aria-label="Block {{.ID}}">{{safe .HTML}}</div>
  {{end}}
</main>
<div class="toast" id="toast" role="status" aria-live="polite"></div>
<script>
window.__MARGINALIA__ = {{.DataJSON}};
(function(){
  "use strict";
  var data = window.__MARGINALIA__;
  var author = data.author || "reviewer";
  var TYPES = [["comment","Comment"],["suggest_edit","Suggest edit"],["question","Question"],["approve","Approve"],["reject","Reject"]];
  var hashOf = {}; data.doc.blocks.forEach(function(b){ hashOf[b.id]=b.hash; });
  var blockOf = {}; data.doc.blocks.forEach(function(b){ blockOf[b.id]=b; });
  var byBlock = {};
  data.events.filter(function(e){return e.type!=="review_done";}).forEach(function(e){ (byBlock[e.block]=byBlock[e.block]||[]).push(e); });

  function count(){
    var n=0; Object.keys(byBlock).forEach(function(k){ n+=byBlock[k].length; });
    document.getElementById("count").textContent = n+" comment"+(n===1?"":"s");
  }
  function toast(m){ var t=document.getElementById("toast"); t.textContent=m; t.classList.add("show"); setTimeout(function(){t.classList.remove("show");},1800); }
  function marker(el){ el.dataset.count = (byBlock[el.dataset.block]||[]).length; }
  function renderExisting(el){
    var id=el.dataset.block, list=byBlock[id]||[];
    var box=el.nextElementSibling;
    if(box && box.classList.contains("existing")) box.remove();
    if(!list.length) return;
    box=document.createElement("div"); box.className="existing";
    list.forEach(function(e){
      var item=document.createElement("div"); item.className="item";
      if(e.hash && hashOf[id] && e.hash!==hashOf[id]) item.classList.add("stale");
      var t=document.createElement("span"); t.className="type"; t.textContent=e.type.replace("_"," ");
      var body=document.createElement("div"); body.textContent=e.text||"";
      item.appendChild(t); item.appendChild(body); box.appendChild(item);
    });
    el.after(box);
  }
  function closeComposer(){ var c=document.querySelector(".composer"); if(c) c.remove(); }
  function openComposer(el){
    closeComposer();
    var id=el.dataset.block, type="comment";
    var c=document.createElement("div"); c.className="composer";
    var chips=document.createElement("div"); chips.className="chips";
    var ta=document.createElement("textarea"); ta.placeholder="Your note…";
    TYPES.forEach(function(pair){
      var b=document.createElement("button"); b.type="button"; b.className="chip"; b.textContent=pair[1];
      b.setAttribute("aria-pressed", pair[0]===type?"true":"false");
      b.onclick=function(){
        type=pair[0];
        chips.querySelectorAll(".chip").forEach(function(x){x.setAttribute("aria-pressed","false");});
        b.setAttribute("aria-pressed","true");
        if(pair[0]==="suggest_edit" && !ta.value){ var blk=blockOf[id]; ta.value=blk?blk.text:""; }
      };
      chips.appendChild(b);
    });
    var actions=document.createElement("div"); actions.className="actions";
    var save=document.createElement("button"); save.className="save"; save.type="button"; save.textContent="Save";
    var cancel=document.createElement("button"); cancel.type="button"; cancel.textContent="Cancel"; cancel.onclick=closeComposer;
    save.onclick=function(){
      var text=ta.value.trim();
      if(!text && type!=="approve" && type!=="reject"){ ta.focus(); return; }
      var blk=blockOf[id]||{};
      var ev={doc:data.doc.path,block:id,quote:blk.quote||el.dataset.quote||"",hash:blk.hash||el.dataset.hash||"",type:type,text:text,author:author};
      fetch("/api/feedback",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(ev)})
        .then(function(res){ if(!res.ok) return res.text().then(function(t){throw new Error(t);}); return res.json(); })
        .then(function(saved){ (byBlock[id]=byBlock[id]||[]).push(saved); marker(el); renderExisting(el); count(); closeComposer(); toast("Saved"); })
        .catch(function(err){ toast("Save failed: "+err.message); });
    };
    actions.appendChild(save); actions.appendChild(cancel);
    c.appendChild(chips); c.appendChild(ta); c.appendChild(actions);
    el.after(c); ta.focus();
  }
  Array.prototype.forEach.call(document.querySelectorAll(".block"), function(el){
    marker(el); renderExisting(el);
    el.addEventListener("click", function(e){ if(e.target.closest(".composer,.existing")) return; openComposer(el); });
    el.addEventListener("keydown", function(e){ if(e.key==="Enter"){ e.preventDefault(); openComposer(el); } });
  });
  count();
  document.getElementById("done").onclick=function(){
    fetch("/api/feedback",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({doc:data.doc.path,type:"review_done",text:"",author:author})})
      .then(function(res){ if(!res.ok) throw new Error("failed"); toast("Review marked done — you can close this tab."); })
      .catch(function(err){ toast("Failed: "+err.message); });
  };
})();
</script>
</body>
</html>
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/web/ -v`
Expected: PASS. If the self-contained test flags a token, remove the external reference (there should be none).

- [ ] **Step 6: Commit**

```bash
git add internal/web/
git commit -m "feat: self-contained CSP-safe review page template"
```

---

### Task 5: `internal/server` — HTTP endpoints

**Files:**
- Create: `internal/server/server.go`, `internal/server/handlers.go`, `internal/server/open.go`
- Test: `internal/server/handlers_test.go`

**Interfaces:**
- Consumes: `document.Document`, `feedback.Store`/`Event`, `web.Render`.
- Produces: `server.Options`, `server.New`, `(*Server).Handler`, `(*Server).Run`.

- [ ] **Step 1: Write failing tests**

`internal/server/handlers_test.go`:
```go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

func newTestServer(t *testing.T) (*Server, *feedback.Store, string) {
	t.Helper()
	docPath := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(docPath, []byte("# Title\n\nHello.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(docPath)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(docPath)
	return New(Options{Doc: doc, Store: store, Author: "tester", Host: "127.0.0.1", Port: 0}), store, docPath
}

func TestIndexServesPage(t *testing.T) {
	s, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "window.__MARGINALIA__") {
		t.Fatalf("code=%d body missing page", rr.Code)
	}
}

func TestPostFeedbackAppendsToDisk(t *testing.T) {
	s, store, _ := newTestServer(t)
	body, _ := json.Marshal(feedback.Event{Block: "1/2", Type: "comment", Text: "note"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Load()
	if len(got) != 1 || got[0].Text != "note" || got[0].Author != "tester" || got[0].Ts == "" {
		t.Fatalf("disk state wrong: %+v", got)
	}
}

func TestPostFeedbackRejectsBadType(t *testing.T) {
	s, _, _ := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"type": "bogus"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rr.Code)
	}
}

func TestApiDocRestoresState(t *testing.T) {
	s, store, _ := newTestServer(t)
	_ = store.Append(feedback.Event{Block: "1/1", Type: "comment", Text: "prior", Ts: "2026-07-03T10:00:00Z"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/doc", nil))
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var payload struct {
		Doc    *document.Document `json:"doc"`
		Events []feedback.Event   `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Doc.Blocks) == 0 || len(payload.Events) != 1 || payload.Events[0].Text != "prior" {
		t.Fatalf("payload wrong: %+v", payload)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/`
Expected: FAIL — `New`/`Options` undefined.

- [ ] **Step 3: Implement `server.go`**

```go
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

// Options configures a review Server.
type Options struct {
	Doc    *document.Document
	Store  *feedback.Store
	Author string
	Host   string
	Port   int
	Open   bool
}

// Server serves the review page and feedback API.
type Server struct {
	opts Options
	mux  *http.ServeMux
}

// New builds a Server with routes registered.
func New(opts Options) *Server {
	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /api/doc", s.handleDoc)
	s.mux.HandleFunc("GET /api/feedback", s.handleGetFeedback)
	s.mux.HandleFunc("POST /api/feedback", s.handlePostFeedback)
	return s
}

// Handler exposes the mux for tests.
func (s *Server) Handler() http.Handler { return s.mux }

// Run starts the server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{Handler: s.mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("marginalia: serving %s at %s\n", s.opts.Doc.Path, url)
	fmt.Printf("marginalia: feedback → %s\n", s.opts.Store.Path())
	if s.opts.Open {
		_ = openBrowser(url)
	}
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Implement `handlers.go`**

```go
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/suTerminus/marginalia/internal/feedback"
	"github.com/suTerminus/marginalia/internal/web"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	events, err := s.opts.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.Render(w, s.opts.Doc, events, s.opts.Author); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDoc(w http.ResponseWriter, _ *http.Request) {
	events, err := s.opts.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"doc": s.opts.Doc, "events": events, "author": s.opts.Author})
}

func (s *Server) handleGetFeedback(w http.ResponseWriter, _ *http.Request) {
	events, err := s.opts.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handlePostFeedback(w http.ResponseWriter, r *http.Request) {
	var e feedback.Event
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if !feedback.ValidType(e.Type) {
		http.Error(w, "invalid event type", http.StatusBadRequest)
		return
	}
	if e.Doc == "" {
		e.Doc = s.opts.Doc.Path
	}
	if e.Author == "" {
		e.Author = s.opts.Author
	}
	if e.Ts == "" {
		e.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.opts.Store.Append(e); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}
```

- [ ] **Step 5: Implement `open.go`**

```go
package server

import (
	"fmt"
	"os/exec"
	"runtime"
)

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("--open not supported on %s", runtime.GOOS)
	}
}
```

- [ ] **Step 6: Run tests with race**

Run: `go test -race ./internal/server/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/server/
git commit -m "feat: review server — index, /api/doc, /api/feedback"
```

---

### Task 6: `cmd serve` + error routing + integration

**Files:**
- Create: `cmd/serve.go`, `cmd/errors.go`
- Modify: `cmd/root.go` (register serve)
- Test: `cmd/serve_test.go`, `cmd/errors_test.go`

**Interfaces:**
- Consumes: `document.Parse`, `feedback.NewStore`, `server.New/Run`, `cmd.version`.
- Produces: `marginalia serve <doc>` and advertise-on-error behavior.

- [ ] **Step 1: Write failing tests**

`cmd/errors_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestUnsupportedInputAdvertisesSkill(t *testing.T) {
	err := checkSupported("notes.toml")
	if err == nil {
		t.Fatal("expected error for .toml")
	}
	msg := err.Error()
	for _, want := range []string{".toml", "request-feature", "marginalia"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestSupportedExtensionsPass(t *testing.T) {
	if err := checkSupported("README.md"); err != nil {
		t.Fatalf("md should be supported: %v", err)
	}
	if err := checkSupported("doc.markdown"); err != nil {
		t.Fatalf("markdown should be supported: %v", err)
	}
}
```

`cmd/serve_test.go`:
```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildServerValidDoc(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(doc, []byte("# Hi\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := buildServer(doc, "127.0.0.1", 0, false, "tester")
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server for valid doc")
	}
}

func TestBuildServerUnsupportedExtension(t *testing.T) {
	_, err := buildServer("notes.toml", "127.0.0.1", 0, false, "tester")
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise-on-error, got %v", err)
	}
}

func TestBuildServerMissingFile(t *testing.T) {
	_, err := buildServer(filepath.Join(t.TempDir(), "nope.md"), "127.0.0.1", 0, false, "tester")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
```
> Note: end-to-end HTTP behavior is covered by `internal/server` tests. This task verifies the CLI wiring in `buildServer` — valid doc, advertise-on-error for unsupported input, and missing-file handling — without binding a port. The full serve round-trip is exercised manually in Step 6.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/`
Expected: FAIL — `checkSupported`, `buildServer` undefined.

- [ ] **Step 3: Implement `errors.go`**

```go
package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
)

var supportedExts = map[string]bool{".md": true, ".markdown": true}

func checkSupported(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if !supportedExts[ext] {
		return unsupportedInputError(ext)
	}
	return nil
}

func unsupportedInputError(ext string) error {
	label := ext
	if label == "" {
		label = "this file type"
	}
	return fmt.Errorf(
		"%s is not supported yet — agents: invoke the marginalia:request-feature skill to file it (marginalia %s)",
		label, version,
	)
}
```

- [ ] **Step 4: Implement `serve.go`**

```go
package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
	"github.com/suTerminus/marginalia/internal/server"
)

func buildServer(path, host string, port int, open bool, author string) (*server.Server, error) {
	if err := checkSupported(path); err != nil {
		return nil, err
	}
	doc, err := document.Parse(path)
	if err != nil {
		return nil, err
	}
	if author == "" {
		author = defaultAuthor()
	}
	return server.New(server.Options{
		Doc:    doc,
		Store:  feedback.NewStore(path),
		Author: author,
		Host:   host,
		Port:   port,
		Open:   open,
	}), nil
}

func defaultAuthor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "reviewer"
}

func newServeCmd() *cobra.Command {
	var (
		port   int
		host   string
		open   bool
		author string
	)
	cmd := &cobra.Command{
		Use:   "serve <doc>",
		Short: "Serve a document for block-anchored human review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := buildServer(args[0], host, port, open, author)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8787, "port to listen on")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host/interface to bind (set to your Tailscale IP for remote review)")
	cmd.Flags().BoolVar(&open, "open", false, "open the review page in a browser")
	cmd.Flags().StringVar(&author, "author", "", "review author (defaults to $USER)")
	return cmd
}
```

- [ ] **Step 5: Register serve in `root.go`**

Modify `cmd/root.go` — change `root.AddCommand(newVersionCmd())` to:
```go
	root.AddCommand(newServeCmd(), newVersionCmd())
```

- [ ] **Step 6: Run full suite + manual smoke**

```bash
go test -race ./...
go run . serve PRD.md --port 8799 &
sleep 1
curl -s localhost:8799/api/doc | head -c 120
curl -s -X POST localhost:8799/api/feedback -d '{"block":"1/1","type":"comment","text":"smoke"}'
cat PRD.md.feedback.jsonl
kill %1; rm -f PRD.md.feedback.jsonl
```
Expected: tests PASS; `/api/doc` returns JSON; POST returns 201; the JSONL line is on disk.

- [ ] **Step 7: Commit**

```bash
git add cmd/
git commit -m "feat: serve command with advertise-on-error routing"
```

---

## Phase B — CI/CD

### Task 7: Lint + test + coverage pipeline

**Files:**
- Create: `.golangci.yml`, `scripts/coverage.sh`, `.github/workflows/ci.yml`

**Interfaces:** none (infra). Validation via `golangci-lint`, `act`/schema, and a local coverage run.

- [ ] **Step 1: Verify tool schemas**

Use Context7/docs to confirm current **golangci-lint v2** config schema and **golangci/golangci-lint-action** version. Adjust the files below to match.

- [ ] **Step 2: Write `.golangci.yml`** (golangci-lint v2)

```yaml
version: "2"
linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - misspell
formatters:
  enable:
    - gofmt
    - gofumpt
```

- [ ] **Step 3: Write `scripts/coverage.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail
threshold="${1:-80}"
go tool cover -func=coverage.out > coverage.txt
total="$(grep -E '^total:' coverage.txt | awk '{print $3}' | tr -d '%')"
{
  echo "### Coverage: ${total}% (floor ${threshold}%)"
  echo ""
  echo '```'
  cat coverage.txt
  echo '```'
} >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"
awk -v t="$threshold" -v c="$total" 'BEGIN{ exit (c+0 < t+0) ? 1 : 0 }' || {
  echo "coverage ${total}% is below floor ${threshold}%" >&2
  exit 1
}
```
Then: `chmod +x scripts/coverage.sh`

- [ ] **Step 4: Write `.github/workflows/ci.yml`**

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
permissions:
  contents: read
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    env:
      CODECOV_TOKEN: ${{ secrets.CODECOV_TOKEN }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - name: Test
        run: go test -race -covermode=atomic -coverprofile=coverage.out ./...
      - name: Coverage gate + summary
        if: matrix.os == 'ubuntu-latest'
        run: ./scripts/coverage.sh 80
      - name: Upload to Codecov
        if: matrix.os == 'ubuntu-latest' && env.CODECOV_TOKEN != ''
        uses: codecov/codecov-action@v4
        with:
          files: coverage.out
          token: ${{ secrets.CODECOV_TOKEN }}
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - run: go build ./...
```

- [ ] **Step 5: Validate locally**

```bash
go test -covermode=atomic -coverprofile=coverage.out ./...
./scripts/coverage.sh 80
```
Expected: prints coverage table; exits 0 if ≥80% (raise test coverage first if below). Confirm YAML parses (e.g. `python3 -c "import yaml,sys;yaml.safe_load(open('.github/workflows/ci.yml'))"`).

- [ ] **Step 6: Commit**

```bash
git add .golangci.yml scripts/coverage.sh .github/workflows/ci.yml
git commit -m "ci: lint, race tests on linux+macos, coverage gate"
```

---

### Task 8: Release pipeline — release-please + GoReleaser

**Files:**
- Create: `release-please-config.json`, `.release-please-manifest.json`, `.goreleaser.yaml`, `.github/workflows/release.yml`

- [ ] **Step 1: Verify schemas** — confirm **release-please-action v4** inputs, **release-please config** schema (`release-type: go`), and **GoReleaser v2** config keys (`version: 2`, `archives.formats`) via Context7/docs.

- [ ] **Step 2: Write `release-please-config.json`**

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "packages": {
    ".": {
      "release-type": "go",
      "package-name": "marginalia",
      "include-component-in-tag": false
    }
  }
}
```

- [ ] **Step 3: Write `.release-please-manifest.json`**

```json
{ ".": "0.0.0" }
```

- [ ] **Step 4: Write `.goreleaser.yaml`**

```yaml
version: 2
project_name: marginalia
before:
  hooks:
    - go mod tidy
builds:
  - id: marginalia
    main: .
    binary: marginalia
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X github.com/suTerminus/marginalia/cmd.version={{.Version}}
      - -X github.com/suTerminus/marginalia/cmd.commit={{.Commit}}
      - -X github.com/suTerminus/marginalia/cmd.date={{.Date}}
archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
checksum:
  name_template: checksums.txt
changelog:
  disable: true
release:
  draft: false
```

- [ ] **Step 5: Write `.github/workflows/release.yml`**

```yaml
name: Release
on:
  push:
    branches: [main]
permissions:
  contents: write
  pull-requests: write
jobs:
  release-please:
    runs-on: ubuntu-latest
    outputs:
      release_created: ${{ steps.rp.outputs.release_created }}
      tag_name: ${{ steps.rp.outputs.tag_name }}
    steps:
      - uses: googleapis/release-please-action@v4
        id: rp
        with:
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json
  goreleaser:
    needs: release-please
    if: needs.release-please.outputs.release_created == 'true'
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 6: Validate**

```bash
# If goreleaser is available locally:
command -v goreleaser >/dev/null && goreleaser check || echo "skip goreleaser check (not installed)"
python3 -c "import json;json.load(open('release-please-config.json'));json.load(open('.release-please-manifest.json'))"
```
Expected: JSON valid; `goreleaser check` passes if installed.

- [ ] **Step 7: Commit**

```bash
git add release-please-config.json .release-please-manifest.json .goreleaser.yaml .github/workflows/release.yml
git commit -m "ci: release-please versioning + goreleaser artifact releases"
```

---

### Task 9: Security scanning + dependency automation

**Files:**
- Create: `.github/workflows/codeql.yml`, `.github/dependabot.yml`, `renovate.json`

- [ ] **Step 1: Write `.github/workflows/codeql.yml`**

```yaml
name: CodeQL
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
  schedule:
    - cron: '27 3 * * 1'
permissions:
  contents: read
  security-events: write
jobs:
  analyze:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: github/codeql-action/init@v3
        with:
          languages: go
      - uses: github/codeql-action/autobuild@v3
      - uses: github/codeql-action/analyze@v3
```

- [ ] **Step 2: Write `.github/dependabot.yml`** (Actions + security only; Renovate owns gomod)

```yaml
version: 2
updates:
  - package-ecosystem: github-actions
    directory: "/"
    schedule:
      interval: weekly
    labels:
      - dependencies
      - github-actions
```

- [ ] **Step 3: Write `renovate.json`** (gomod only)

```json
{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "extends": ["config:recommended", ":semanticCommits"],
  "enabledManagers": ["gomod"],
  "postUpdateOptions": ["gomodTidy"],
  "schedule": ["before 6am on monday"],
  "packageRules": [
    {
      "matchManagers": ["gomod"],
      "matchUpdateTypes": ["minor", "patch"],
      "groupName": "go modules (non-major)",
      "automerge": true
    }
  ]
}
```

- [ ] **Step 4: Validate**

```bash
python3 -c "import json;json.load(open('renovate.json'))"
python3 -c "import yaml;yaml.safe_load(open('.github/dependabot.yml'));yaml.safe_load(open('.github/workflows/codeql.yml'))"
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/codeql.yml .github/dependabot.yml renovate.json
git commit -m "ci: CodeQL scanning, Dependabot (actions), Renovate (gomod)"
```

> **Manual follow-up (note in PR):** install the Renovate GitHub App on the repo and enable CodeQL/Dependabot alerts in repo Settings → Security. These require repo-owner action outside CI.

---

## Phase C — Claude plugin + feedback scaffold

### Task 10: Plugin manifests + slash command

**Files:**
- Create: `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `commands/review-doc.md`

- [ ] **Step 1: Verify plugin schema** — confirm current Claude Code `plugin.json` and `marketplace.json` fields (name/version/description/author, marketplace `owner`/`plugins[].source`) via official docs or the claude-code-guide agent. Adjust below to match.

- [ ] **Step 2: Write `.claude-plugin/plugin.json`**

```json
{
  "name": "marginalia",
  "version": "0.1.0",
  "description": "Hand an agent-authored document to a human for block-anchored review; feedback returns as structured JSONL events.",
  "author": { "name": "suTerminus" },
  "homepage": "https://github.com/suTerminus/marginalia",
  "keywords": ["review", "markdown", "feedback", "annotation"]
}
```

- [ ] **Step 3: Write `.claude-plugin/marketplace.json`**

```json
{
  "name": "marginalia",
  "owner": { "name": "suTerminus", "url": "https://github.com/suTerminus" },
  "plugins": [
    {
      "name": "marginalia",
      "source": "./",
      "description": "Document review tool: render markdown, collect block-anchored comments, return structured feedback events."
    }
  ]
}
```

- [ ] **Step 4: Write `commands/review-doc.md`**

```markdown
---
description: Serve a markdown document for human review and consume the returned feedback events.
argument-hint: <path-to-doc>
---

Use the `review-doc` skill to run a Marginalia review for `$ARGUMENTS`.

Follow `skills/review-doc/SKILL.md` exactly: start the server, tell the human the
URL and what you need reviewed, watch `<doc>.feedback.jsonl` for the `review_done`
event, then address every event grouped by block, quoting `block` + `quote`.
```

- [ ] **Step 5: Validate**

```bash
python3 -c "import json;json.load(open('.claude-plugin/plugin.json'));json.load(open('.claude-plugin/marketplace.json'))"
```
Expected: valid JSON.

- [ ] **Step 6: Commit**

```bash
git add .claude-plugin/ commands/
git commit -m "feat: package repo as an installable Claude plugin + marketplace"
```

---

### Task 11: Skills — review-doc + request-feature

**Files:**
- Create: `skills/review-doc/SKILL.md`, `skills/request-feature/SKILL.md`

- [ ] **Step 1: Write `skills/review-doc/SKILL.md`**

```markdown
---
name: review-doc
description: Use when you need a human to review a document you produced (spec, plan, PRD, ADR, report) and you want their feedback back as structured data. Renders the file as a block-anchored review page, waits for the human, then reads their feedback events.
---

# Reviewing a document with Marginalia

## Preflight: ensure the binary exists

Run `marginalia version`. If it is not found, install it:

```bash
go install github.com/suTerminus/marginalia@latest
```

(or download a release binary from https://github.com/suTerminus/marginalia/releases).

## 1. Serve the document

```bash
marginalia serve <path> --open
```

This prints the URL and the feedback file path (`<path>.feedback.jsonl`). The
server writes every comment to that file the instant it is saved — there is no
export step.

## 2. Tell the human what you need

State the URL and exactly what you want reviewed ("I need your take on §3 and the
error-handling approach"). Then wait.

## 3. Watch for completion

Poll `<path>.feedback.jsonl` for a `review_done` event:

```bash
tail -f <path>.feedback.jsonl
```

Completion signals, in priority order: a `{"type":"review_done"}` line; the human
says they're done; or the file goes quiet after activity.

## 4. Address the feedback

Read the JSONL, group events by `block`, and address every one explicitly.
**Quote `block` + `quote`** so the human can verify you anchored correctly:

> §5.3/2 ("A registry-backed contract mirroring…") — you asked to simplify this.
> Done: …

Event types: `comment`, `suggest_edit` (the `text` is the proposed replacement),
`question`, `approve`, `reject`. If a `hash` no longer matches the current block
(the doc changed since the comment), flag the note as **stale** and re-confirm
with the human before acting.

## 5. Revise and, if needed, re-review

If you change the document, offer another `marginalia serve` pass.

---

*Hit a limitation (unsupported format, missing flag, awkward workflow)? Invoke the
`marginalia:request-feature` skill to file it.*
```

- [ ] **Step 2: Write `skills/request-feature/SKILL.md`**

```markdown
---
name: request-feature
description: Use when Marginalia hits a limitation — an unsupported input format, a missing flag, or a workflow gap. Files a deduplicated, structured GitHub issue that the maintainer can triage and an agent can later implement.
---

# Requesting a Marginalia feature

Marginalia's primary users are agents, so limitations you hit are the feature
pipeline. File them precisely.

## Guardrails (read first)

- **Quote structure, never content.** Describe the document shape ("a markdown
  file with nested tables"), never paste private text or secrets.
- **At most one issue per limitation per session.**
- Link the session's *purpose*, not a transcript.

## 1. Dedupe

```bash
gh issue list --repo suTerminus/marginalia --label agent-feedback --state all --search "<keywords>"
```

If an issue already covers the gap, add a comment with your new use case instead
of filing a duplicate.

## 2. File the issue

Use the `agent-feedback` issue form:

```bash
gh issue create --repo suTerminus/marginalia \
  --label agent-feedback --label <category> \
  --title "<concise limitation>" \
  --body "<what you attempted (exact invocation), expected, actual/error, workaround, marginalia version, requesting context>"
```

`<category>` is one of: `format-support`, `customization`, `skill-gap`, `bug`.

## 3. What makes an implementable issue

Include the exact `marginalia` invocation, the expected behavior, the actual
behavior/error, and a one-line acceptance criterion. The maintainer gates work
with the `approved-for-agent` label; well-formed issues can be implemented from
the issue alone.
```

- [ ] **Step 2b: Validate frontmatter**

```bash
head -4 skills/review-doc/SKILL.md skills/request-feature/SKILL.md
```
Expected: each starts with `---` / `name:` / `description:` / `---`.

- [ ] **Step 3: Commit**

```bash
git add skills/
git commit -m "docs: review-doc and request-feature agent skills"
```

---

### Task 12: Agent-feedback issue form + labels

**Files:**
- Create: `.github/ISSUE_TEMPLATE/agent-feedback.yml`, `.github/ISSUE_TEMPLATE/config.yml`, `scripts/setup-labels.sh`

- [ ] **Step 1: Write `.github/ISSUE_TEMPLATE/agent-feedback.yml`**

```yaml
name: Agent feedback
description: A limitation an agent hit while using Marginalia (auto-routed by the request-feature skill).
title: "[agent-feedback] "
labels: ["agent-feedback"]
body:
  - type: input
    id: invocation
    attributes:
      label: What was attempted (exact invocation)
      placeholder: marginalia serve notes.toml --open
    validations:
      required: true
  - type: textarea
    id: expected
    attributes:
      label: Expected behavior
    validations:
      required: true
  - type: textarea
    id: actual
    attributes:
      label: Actual behavior / error
    validations:
      required: true
  - type: input
    id: workaround
    attributes:
      label: Workaround used (if any)
  - type: dropdown
    id: category
    attributes:
      label: Category
      options: [format-support, customization, skill-gap, bug]
    validations:
      required: true
  - type: input
    id: version
    attributes:
      label: Marginalia version
      placeholder: v0.1.0
    validations:
      required: true
  - type: input
    id: context
    attributes:
      label: Requesting context (project/harness — structure, not content)
    validations:
      required: true
  - type: markdown
    attributes:
      value: "Do not paste private document content or secrets. Describe structure only."
```

- [ ] **Step 2: Write `.github/ISSUE_TEMPLATE/config.yml`**

```yaml
blank_issues_enabled: true
```

- [ ] **Step 3: Write `scripts/setup-labels.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail
repo="suTerminus/marginalia"
create(){ gh label create "$1" --repo "$repo" --color "$2" --description "$3" --force; }
create agent-feedback     "1d76db" "Filed by an agent hitting a limitation"
create approved-for-agent "0e8a16" "Approved for automated implementation"
create format-support     "5319e7" "New input format request"
create customization      "fbca04" "Configuration/customization request"
create skill-gap          "c2e0c6" "Skill/workflow gap"
create bug                "d73a4a" "Something is broken"
create dependencies       "0366d6" "Dependency updates"
```
Then: `chmod +x scripts/setup-labels.sh`

- [ ] **Step 4: Run the label setup + validate**

```bash
python3 -c "import yaml;yaml.safe_load(open('.github/ISSUE_TEMPLATE/agent-feedback.yml'))"
./scripts/setup-labels.sh
gh label list --repo suTerminus/marginalia | grep agent-feedback
```
Expected: labels created; form YAML valid.

- [ ] **Step 5: Commit**

```bash
git add .github/ISSUE_TEMPLATE/ scripts/setup-labels.sh
git commit -m "ci: agent-feedback issue form and label scaffold"
```

---

### Task 13: README + repo-wide verification

**Files:**
- Create: `README.md`
- Modify: `CLAUDE.md` (update Commands section — module now exists)

- [ ] **Step 1: Write `README.md`**

````markdown
# Marginalia

A document-review tool an agent hands to a human. It renders a markdown file as a
readable page, collects inline comments anchored to blocks, and writes them back
as append-only JSONL feedback events the agent consumes directly.

See `PRD.md` for the full product spec.

## Install

**Binary:**

```bash
go install github.com/suTerminus/marginalia@latest
```

Or download a release from the [releases page](https://github.com/suTerminus/marginalia/releases).

**Claude plugin:**

```
/plugin marketplace add suTerminus/marginalia
/plugin install marginalia
```

Then `/review-doc <path>` in any session.

## Quickstart

```bash
marginalia serve README.md --open
```

Open the URL, click any block to comment, hit **Done** when finished. Every
comment is written to `README.md.feedback.jsonl` the instant it is saved — there
is no export step.

## How it works

- Each block gets a stable ID (`section/ordinal`, e.g. `5.3/2`), a ~90-char
  quote, and a content hash — so feedback re-anchors on re-render and stale
  comments are flagged.
- Feedback events (`comment`, `suggest_edit`, `question`, `approve`, `reject`,
  `review_done`) are append-only JSONL beside the document.
- The page is fully self-contained (inline CSS/JS, system fonts, no external
  requests) and never touches the Clipboard API.

## Remote / phone review

Bind to your Tailscale interface and open the same URL from your phone:

```bash
marginalia serve doc.md --host 100.x.y.z
```

## Development

```bash
go test -race ./...      # tests
go build ./...           # build
marginalia serve X.md    # run
```
````

- [ ] **Step 2: Update `CLAUDE.md` Commands section**

Replace the "None exist yet…" line in the `## Commands` section with:
```markdown
- Build: `go build ./...`  ·  Run: `go run . serve <doc.md> --open`
- Test: `go test -race ./...`  ·  single: `go test -run TestName ./internal/document/`
- Coverage gate: `go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80`
- Lint: `golangci-lint run`
- Skills live in `skills/`; the repo is its own Claude plugin marketplace (`.claude-plugin/`).
```

- [ ] **Step 3: Full verification**

```bash
go build ./...
go test -race ./...
go vet ./...
gofmt -l .            # expect no output
go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80
```
Expected: all green; coverage ≥ 80%; `gofmt -l` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: README with install/usage and updated CLAUDE.md commands"
```

---

## Self-Review

**Spec coverage:**
- §3 layout → Tasks 1–13 create every listed path. ✔
- §4 anchoring algorithm → Task 2 (section counters, quote, hash, per-node HTML). ✔
- §5 feedback store (append-only, load, resolve, types incl. review_done) → Task 3. ✔
- §6 server (4 routes, restore-from-disk, --open, graceful shutdown, host/port) → Tasks 5–6. ✔
- §7 web page (68ch, system fonts, focusable blocks, chips, prefers-reduced-motion, no Clipboard, CSP-safe) → Task 4. ✔
- §8 CLI + advertise-on-error → Tasks 1, 6. ✔
- §9 testing (race, coverage floor, golangci) → Tasks 2–6, 7. ✔
- §10 CI/CD (ci matrix, codeql, release-please+goreleaser, dependabot, renovate) → Tasks 7–9. ✔
- §11 plugin (plugin.json, marketplace.json, skills, command, issue form, README) → Tasks 10–13. ✔

**Placeholder scan:** No `TBD`/`TODO`. The `cmd/serve_test.go` in Task 6 is deliberately a wiring smoke test (HTTP behavior is fully covered in `internal/server`); noted inline.

**Type consistency:** `Block`, `Document`, `Event`, `Store`, `Options`, `Render`, `New`, `Run`, `checkSupported`, `buildServer`, `version` — names match across Shared Interfaces and every task. GoReleaser ldflags target `cmd.version/commit/date`, matching Task 1's declarations.

**Known verification points (flagged in tasks, not gaps):** golangci-lint v2, GoReleaser v2, release-please v4, and Claude plugin/marketplace schemas must be confirmed against current docs during their tasks. Renovate app install + GitHub security-feature enablement are manual repo-owner steps noted in Task 9.
