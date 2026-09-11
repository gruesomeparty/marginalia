package diagram

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeMMDC writes a stand-in for mermaid-cli: it reads -i/-o like the real
// one, writes svg to the output file and records each run. Tests must not
// depend on a Node toolchain being installed, and the real renderer starts a
// browser.
func fakeMMDC(t *testing.T, svg string) (bin, runs string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	dir := t.TempDir()
	runs = filepath.Join(dir, "runs")
	body := filepath.Join(dir, "body.svg")
	if err := os.WriteFile(body, []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "mmdc")
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2;;
    *) shift;;
  esac
done
echo run >> ` + runs + `
cat ` + body + ` > "$out"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, runs
}

func failingMMDC(t *testing.T, message string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	bin := filepath.Join(t.TempDir(), "mmdc")
	script := "#!/bin/sh\necho '" + message + "' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func countRuns(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

func TestUnavailableRendererIsHarmless(t *testing.T) {
	var r *Renderer
	if r.Available() {
		t.Fatal("nil renderer reports available")
	}
	if _, err := (&Renderer{}).SVG("graph TD"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestFindPrefersExplicitThenEnv(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("MARGINALIA_MMDC", "")
	t.Setenv("MARGINALIA_MMDC_ARGS", "")

	bin, _ := fakeMMDC(t, "<svg></svg>")
	if got := Find(bin); got.Bin != bin {
		t.Fatalf("explicit path: got %q want %q", got.Bin, bin)
	}
	t.Setenv("MARGINALIA_MMDC", bin)
	t.Setenv("MARGINALIA_MMDC_ARGS", "-p pconf.json")
	r := Find("")
	if r.Bin != bin {
		t.Fatalf("MARGINALIA_MMDC: got %q want %q", r.Bin, bin)
	}
	if strings.Join(r.Args, " ") != "-p pconf.json" {
		t.Fatalf("args: got %v", r.Args)
	}
	if r.Cache == "" || !strings.Contains(r.Cache, "marginalia") {
		t.Fatalf("cache dir: got %q", r.Cache)
	}
	if !r.Available() {
		t.Fatal("renderer with a binary reports unavailable")
	}
}

func TestFindMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MARGINALIA_MMDC", "")
	if r := Find(filepath.Join(t.TempDir(), "nope")); r.Available() {
		t.Fatalf("found a renderer that is not there: %q", r.Bin)
	}
}

// The rendered SVG lands in a page that promises to run no code and make no
// requests, in share mode under a strict CSP. Whatever mermaid emits, that is
// what has to come out of here.
func TestSVGSanitizes(t *testing.T) {
	raw := `<?xml version="1.0"?>
<!DOCTYPE svg>
<svg xmlns="http://www.w3.org/2000/svg">
<style>@import url(https://fonts.example/x.css); .node{fill:red}</style>
<script>alert(1)</script>
<script src="https://evil.example/x.js"/>
<g onclick="steal()" ONMOUSEOVER='x()'><image href="https://evil.example/p.png"/></g>
<use xlink:href="#arrow"/><image href="data:image/png;base64,AA"/>
</svg>`
	bin, _ := fakeMMDC(t, raw)
	r := &Renderer{Bin: bin, Cache: t.TempDir()}
	svg, err := r.SVG("graph TD\n A-->B")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("prolog survived: %q", svg[:min(60, len(svg))])
	}
	for _, bad := range []string{"script", "onclick", "ONMOUSEOVER", "@import", "evil.example"} {
		if strings.Contains(svg, bad) {
			t.Errorf("sanitize kept %q:\n%s", bad, svg)
		}
	}
	if !strings.Contains(svg, `xlink:href="#arrow"`) {
		t.Error("dropped a fragment reference")
	}
	if !strings.Contains(svg, "data:image/png") {
		t.Error("dropped an inlined image")
	}
	// The stylesheet survives the import being cut out of it — and comes back
	// speaking the page's variables, since rendering themes it too.
	if !strings.Contains(svg, ".node{fill:var(--mg-node-fill)}") {
		t.Errorf("lost the stylesheet with the import:\n%s", svg)
	}
}

func TestSVGCachesBySource(t *testing.T) {
	bin, runs := fakeMMDC(t, "<svg>drawn</svg>")
	r := &Renderer{Bin: bin, Cache: t.TempDir()}
	for i := 0; i < 3; i++ {
		if _, err := r.SVG("graph TD\n A-->B"); err != nil {
			t.Fatal(err)
		}
	}
	if got := countRuns(t, runs); got != 1 {
		t.Fatalf("same source rendered %d times, want 1", got)
	}
	// A fresh renderer sharing the cache directory still hits it: that is
	// what keeps --watch cheap across re-parses.
	if _, err := (&Renderer{Bin: bin, Cache: r.Cache}).SVG("graph TD\n A-->B"); err != nil {
		t.Fatal(err)
	}
	if got := countRuns(t, runs); got != 1 {
		t.Fatalf("cache missed across renderers: %d runs", got)
	}
	if _, err := r.SVG("graph TD\n A-->C"); err != nil {
		t.Fatal(err)
	}
	if got := countRuns(t, runs); got != 2 {
		t.Fatalf("changed source rendered %d times, want 2", got)
	}
}

func TestSVGWithoutCacheStillRenders(t *testing.T) {
	bin, runs := fakeMMDC(t, "<svg>drawn</svg>")
	r := &Renderer{Bin: bin}
	if _, err := r.SVG("graph TD"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SVG("graph TD"); err != nil {
		t.Fatal(err)
	}
	if got := countRuns(t, runs); got != 2 {
		t.Fatalf("want 2 runs without a cache, got %d", got)
	}
}

func TestSVGFailureCarriesStderr(t *testing.T) {
	r := &Renderer{Bin: failingMMDC(t, "Parse error on line 2"), Cache: t.TempDir()}
	_, err := r.SVG("graph TD\n ???")
	if err == nil {
		t.Fatal("a failing renderer returned no error")
	}
	if !strings.Contains(err.Error(), "Parse error on line 2") {
		t.Fatalf("error hides the reason: %v", err)
	}
	if !strings.Contains(err.Error(), "mmdc") {
		t.Fatalf("error does not name the tool: %v", err)
	}
}
