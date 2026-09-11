package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// doc writes a document in a fresh directory and returns its path.
func doc(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The share-mode page has to work with no server and no network at all: a
// reviewer opens one file, comments, and hands the result back.
func TestExportIsSelfContained(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n\n![arch](https://example.com/arch.png)\n")
	out, page, err := buildExport([]string{src}, exportOptions{author: "tester"})
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	if out != src+".review.html" {
		t.Errorf("default output = %q", out)
	}
	html := string(page)
	if !strings.Contains(html, `"static":true`) {
		t.Error("the page does not know it has no server")
	}
	// An image would be fetched on load, from a host neither side chose.
	if strings.Contains(html, `<img`) {
		t.Error("an image survived into the offline page")
	}
	if !strings.Contains(html, `class="offline"`) || !strings.Contains(html, "example.com/arch.png") {
		t.Error("the reader should be told what is missing, and which image it was")
	}
	// Nothing may be fetched, and no comment may need a server to be saved.
	for _, forbidden := range []string{"http://", "https://example.com/arch.png\"", "<link", "@import", "url("} {
		if strings.Contains(html, forbidden) {
			t.Errorf("page contains %q", forbidden)
		}
	}
	if strings.Contains(html, "navigator.clipboard") {
		t.Error("the Clipboard API is blocked in hosted contexts and must never be depended on")
	}
	// The export path is a textarea plus a wrapped download, both present.
	for _, want := range []string{`id="sharejson"`, `id="download"`, "URL.createObjectURL", "localStorage"} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %s", want)
		}
	}
}

// Feedback already on disk travels with the page, so a shared review shows
// the thread so far rather than starting blank.
func TestExportCarriesExistingFeedback(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n")
	if err := feedback.NewStore(src).Append(feedback.Event{
		Block: "1/2", Quote: "Capped at 500.", Type: feedback.TypeReject,
		Text: "too low", Author: "berkay", Ts: "2026-07-03T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	_, page, err := buildExport([]string{src}, exportOptions{author: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "too low") {
		t.Error("prior feedback did not travel with the page")
	}
}

// A configured review frames a shared page too, and an unknown theme is a
// feature request rather than a silent default.
func TestExportHonoursReviewAndTheme(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n")
	cfg := filepath.Join(filepath.Dir(src), "review.yaml")
	if err := os.WriteFile(cfg, []byte("instructions: Only §1.\nactions:\n  - type: blocker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, page, err := buildExport([]string{src}, exportOptions{config: cfg, theme: "catppuccin-mocha"})
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if !strings.Contains(html, "Only §1.") || !strings.Contains(html, `"type":"blocker"`) {
		t.Error("the review's framing did not reach the shared page")
	}
	if !strings.Contains(html, `data-palette="catppuccin" data-mode="dark"`) {
		t.Error("the theme did not reach the shared page")
	}
	if _, _, err := buildExport([]string{src}, exportOptions{theme: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "request-feature") {
		t.Errorf("unknown theme should advertise: %v", err)
	}
}

// Export is one document at a time; asking for a set is a feature request.
func TestExportRefusesASet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# T\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := buildExport([]string{dir}, exportOptions{})
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise-on-error, got %v", err)
	}
	if _, _, err := buildExport([]string{filepath.Join(dir, "gone.md")}, exportOptions{}); err == nil {
		t.Error("a missing document must fail")
	}
}

// The round trip: what the page exports is what import merges, and importing
// it twice changes nothing — because someone will.
func TestImportRoundTrip(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n")
	shared := filepath.Join(filepath.Dir(src), "review.json")
	body, _ := json.Marshal(sharedReview{Doc: src, Events: []feedback.Event{
		{Block: "1/2", Quote: "Capped at 500.", Type: feedback.TypeComment, Text: "too low", Author: "them", Ts: "2026-07-03T10:00:00Z"},
		{Block: "", Type: feedback.TypeReviewDone, Author: "them", Ts: "2026-07-03T10:05:00Z"},
	}})
	if err := os.WriteFile(shared, body, 0o644); err != nil {
		t.Fatal(err)
	}
	added, skipped, target, err := importReview(shared, "")
	if err != nil {
		t.Fatalf("importReview: %v", err)
	}
	if added != 2 || skipped != 0 || target != src+".feedback.jsonl" {
		t.Fatalf("added=%d skipped=%d target=%q", added, skipped, target)
	}
	events, err := feedback.NewStore(src).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Text != "too low" || events[0].Doc != src {
		t.Fatalf("log = %+v", events)
	}
	// Again: nothing added, nothing rewritten.
	added, skipped, _, err = importReview(shared, "")
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || skipped != 2 {
		t.Errorf("second import: added=%d skipped=%d", added, skipped)
	}
	if again, _ := feedback.NewStore(src).Load(); len(again) != 2 {
		t.Errorf("log grew on re-import: %+v", again)
	}
}

// A hand-edited bare array is accepted; a file that says nothing about which
// document it reviews is not, unless --doc says.
func TestImportShapes(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n")
	dir := filepath.Dir(src)
	bare := filepath.Join(dir, "bare.json")
	if err := os.WriteFile(bare, []byte(`[{"block":"1/2","type":"comment","text":"hand written"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := importReview(bare, ""); err == nil {
		t.Error("a file with no document and no --doc must fail")
	}
	added, _, _, err := importReview(bare, src)
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	// A document that moved, and files that are not reviews at all.
	if _, _, _, err := importReview(bare, filepath.Join(dir, "gone.md")); err == nil {
		t.Error("importing onto a document that is not there must fail")
	}
	junk := filepath.Join(dir, "junk.json")
	if err := os.WriteFile(junk, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := importReview(junk, src); err == nil {
		t.Error("junk must fail")
	}
	if _, _, _, err := importReview(filepath.Join(dir, "missing.json"), src); err == nil {
		t.Error("a missing file must fail")
	}
}

// An event with nothing to anchor to is refused before anything is written:
// a partial import would be worse than none.
func TestImportRefusesUnanchoredEvents(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n")
	bad := filepath.Join(filepath.Dir(src), "bad.json")
	for _, body := range []string{
		`{"doc":"x","events":[{"block":"1/2","type":"comment","text":"ok"},{"type":"comment","text":"nowhere"}]}`,
		`{"doc":"x","events":[{"block":"1/2","text":"no type"}]}`,
	} {
		if err := os.WriteFile(bad, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := importReview(bad, src); err == nil {
			t.Errorf("expected a refusal for %s", body)
		}
		if events, _ := feedback.NewStore(src).Load(); len(events) != 0 {
			t.Fatalf("a refused import must write nothing: %+v", events)
		}
	}
}

func TestRequirePaths(t *testing.T) {
	for verb, want := range map[string]string{
		"serve":  "marginalia serve spec.md",
		"export": "marginalia export spec.md",
		"import": "marginalia import review.json",
	} {
		err := requirePaths(verb)(nil, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", verb, err)
		}
		// A usage mistake is not a feature request.
		if err != nil && strings.Contains(err.Error(), "request-feature") {
			t.Errorf("%s should not advertise: %v", verb, err)
		}
		if err := requirePaths(verb)(nil, []string{"x"}); err != nil {
			t.Errorf("%s with a path: %v", verb, err)
		}
	}
}

// run executes the CLI the way a user does, capturing what it printed.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out strings.Builder
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&out)
	err := executeRoot(root)
	return out.String(), err
}

// The whole share-mode loop from the command line: export a page, pretend a
// reviewer handed back what it exports, and import it.
func TestExportImportThroughTheCLI(t *testing.T) {
	src := doc(t, "spec.md", "# Spec\n\nCapped at 500.\n")
	dir := filepath.Dir(src)
	page := filepath.Join(dir, "share", "review.html")
	out, err := run(t, "export", src, "-o", page)
	if err != nil {
		t.Fatalf("export: %v (%s)", err, out)
	}
	if !strings.Contains(out, "wrote "+page) || !strings.Contains(out, "marginalia import") {
		t.Errorf("export said: %q", out)
	}
	// The output directory is created rather than being the caller's problem.
	if _, err := os.Stat(page); err != nil {
		t.Fatalf("page not written: %v", err)
	}

	reply := filepath.Join(dir, "reply.json")
	body, _ := json.Marshal(sharedReview{Doc: src, Events: []feedback.Event{
		{Block: "1/2", Type: feedback.TypeSuggestEdit, Text: "Capped at 2000.", Author: "them", Ts: "2026-07-03T11:00:00Z"},
	}})
	if err := os.WriteFile(reply, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, "import", reply); err != nil {
		t.Fatalf("import: %v (%s)", err, out)
	}
	if !strings.Contains(out, "1 added, 0 already there") {
		t.Errorf("import said: %q", out)
	}
	if out, err = run(t, "import", reply); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if !strings.Contains(out, "already merged") {
		t.Errorf("second import said: %q", out)
	}
	events, err := feedback.NewStore(src).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Text != "Capped at 2000." {
		t.Fatalf("log = %+v", events)
	}
	// And the document itself was never touched.
	if body, _ := os.ReadFile(src); string(body) != "# Spec\n\nCapped at 500.\n" {
		t.Errorf("the source document changed: %q", body)
	}
}

// Both new commands say what to type when invoked bare, and neither pretends
// that is a missing feature.
func TestExportImportUsageErrors(t *testing.T) {
	for _, verb := range []string{"export", "import"} {
		out, err := run(t, verb)
		if err == nil {
			t.Fatalf("%s with no argument should fail (%s)", verb, out)
		}
		if strings.Contains(err.Error(), "request-feature") {
			t.Errorf("%s usage error should not advertise: %v", verb, err)
		}
		if !strings.Contains(err.Error(), "marginalia "+verb) {
			t.Errorf("%s error should show the shape: %v", verb, err)
		}
	}
}

// isolateCache keeps rendered diagrams out of the real user cache directory:
// the renderer caches by source content, so tests would otherwise both dirty
// the developer's cache and answer each other's renders.
func isolateCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("MARGINALIA_MMDC", "")
	t.Setenv("MARGINALIA_MMDC_ARGS", "")
}

// stubMMDC writes a stand-in for mermaid-cli: the real one starts a headless
// browser, which no test may depend on.
func stubMMDC(t *testing.T, svg string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	dir := t.TempDir()
	body := filepath.Join(dir, "body.svg")
	if err := os.WriteFile(body, []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "mmdc")
	script := "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) out=\"$2\"; shift 2;;\n    *) shift;;\n  esac\ndone\ncat " + body + " > \"$out\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

const diagramDoc = "# Flow\n\n```mermaid\nflowchart LR\n  client --> api\n```\n"

// A shared page carries its pictures as SVG. That is the whole reason the
// renderer runs here and not in the reviewer's browser: an SVG needs no
// script and makes no request, so the file still opens under a strict CSP.
func TestExportCarriesTheDrawnDiagram(t *testing.T) {
	isolateCache(t)
	svg := `<svg id="my-svg" xmlns="http://www.w3.org/2000/svg"><path id="my-svg-L_client_api_0" data-id="L_client_api_0"/></svg>`
	src := doc(t, "flow.md", diagramDoc)
	_, page, err := buildExport([]string{src}, exportOptions{author: "tester", mmdc: stubMMDC(t, svg)})
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	html := string(page)
	if !strings.Contains(html, `<figure class="diagram">`) {
		t.Fatal("the shared page shows no picture")
	}
	if !strings.Contains(html, `data-anchor="1/2/client-->api"`) {
		t.Error("the shared picture is not anchored, so its shapes cannot be commented on")
	}
	for _, bad := range []string{`src="http`, `href="http`, "url(http", "@import", "<script src"} {
		if strings.Contains(html, bad) {
			t.Errorf("the shared page fetches something: %s", bad)
		}
	}
}

// --diagrams=off shares the source instead, on a machine that has a renderer.
func TestExportDiagramsOff(t *testing.T) {
	isolateCache(t)
	src := doc(t, "flow.md", diagramDoc)
	_, page, err := buildExport([]string{src}, exportOptions{
		author: "tester", diagrams: "off", mmdc: stubMMDC(t, "<svg/>"),
	})
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	html := string(page)
	if strings.Contains(html, `<figure class="diagram">`) {
		t.Error("--diagrams=off still drew a picture")
	}
	if !strings.Contains(html, `data-block="1/2/client--&gt;api"`) {
		t.Error("the anchored source is gone")
	}
}

// A failed render is a loud error here, not a silent fallback: the file is
// about to be handed to someone who cannot re-run the command.
func TestExportReportsAFailedDiagram(t *testing.T) {
	isolateCache(t)
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	bin := filepath.Join(t.TempDir(), "mmdc")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'Parse error' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := doc(t, "broken.md", "# Flow\n\n```mermaid\nflowchart LR\n  ???\n```\n")
	_, _, err := buildExport([]string{src}, exportOptions{author: "tester", mmdc: bin})
	if err == nil {
		t.Fatal("a failed render exported anyway")
	}
	for _, want := range []string{"Parse error", "--diagrams=off"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestDiagramModes(t *testing.T) {
	isolateCache(t)
	bin := stubMMDC(t, "<svg/>")
	if r, err := diagrams("auto", bin); err != nil || !r.Available() {
		t.Errorf("auto with a renderer: %v, available=%v", err, r.Available())
	}
	if r, err := diagrams("", bin); err != nil || !r.Available() {
		t.Errorf("default with a renderer: %v, available=%v", err, r.Available())
	}
	if r, err := diagrams("off", bin); err != nil || r.Available() {
		t.Errorf("off: %v, available=%v", err, r.Available())
	}
	_, err := diagrams("maybe", bin)
	if err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	// An unknown flag value is where the tool advertises the way to ask for
	// better behaviour.
	if !strings.Contains(err.Error(), "auto, off") || !strings.Contains(err.Error(), "request-feature") {
		t.Errorf("unhelpful error: %v", err)
	}
}
