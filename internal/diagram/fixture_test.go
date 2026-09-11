package diagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// Anchoring rests on how mermaid names the parts of its own SVG — L_<from>_<to>_<n>
// on edge paths and their labels, <svgid>-flowchart-<node>-<n> on node groups,
// <svgid>-<name> on clusters. None of that is documented API: it is internal to
// mermaid's renderer and has changed across its majors.
//
// So the SVG in testdata is not written by hand. It is the real output of the
// version named in fixtureSVG, for the diagram in testdata/flowchart.mmd, and
// this test drives it through the whole pipeline the server uses. When a
// mermaid upgrade renames or restructures anything, this fails and names the
// shape that lost its anchor — instead of the diagram still drawing, still
// looking right, and quietly not being clickable any more.
//
// To adopt a new version: render testdata/flowchart.mmd with it
//
//	mmdc -i testdata/flowchart.mmd -o testdata/flowchart-mermaid-cli-<version>.svg -b transparent
//
// then point fixtureSVG at the new file, run this test, and extend the matcher
// in anchor.go until it passes. Keep the old fixture beside it if you still
// support that version.
const (
	fixtureSVG    = "testdata/flowchart-mermaid-cli-11.17.0.svg"
	fixtureSource = "testdata/flowchart.mmd"
)

// The diagram in flowchart.mmd is chosen to draw one of everything the matcher
// claims to handle. Each entry is a shape mermaid emitted and the statement of
// the document it has to answer for.
func fixtureShapes() map[string]string {
	return map[string]string{
		// a plain edge, and the label-less path it draws
		"my-svg-L_client_api_0": "client-->api",
		"my-svg-L_api_queue_0":  "api-->queue",
		// a chain is one statement drawn as two paths; both point back at it
		"my-svg-L_queue_worker_0": "queue-->worker-->sink",
		"my-svg-L_worker_sink_0":  "queue-->worker-->sink",
		// a dotted edge with a label — the label sits on the arrow and takes
		// the pointer there, so it answers too
		"my-svg-L_worker_queue_0": "worker-->queue",
		// an edge inside a subgraph keeps the subgraph in its path
		"my-svg-L_charge_invoice_0": "payments/charge-->invoice",
		// an edge that crosses into one
		"my-svg-L_sink_charge_0": "sink-->charge",
		// a node the document declares on its own line
		"my-svg-flowchart-db-9": "db",
		// a subgraph's frame
		"my-svg-payments": "payments",
		// boxes no statement declares: each answers for the statement that
		// named it, which is the line that gave it its label
		"my-svg-flowchart-client-0":   "client-->api",
		"my-svg-flowchart-api-1":      "client-->api",
		"my-svg-flowchart-queue-3":    "api-->queue",
		"my-svg-flowchart-worker-5":   "queue-->worker-->sink",
		"my-svg-flowchart-sink-6":     "queue-->worker-->sink",
		"my-svg-flowchart-charge-10":  "payments/charge-->invoice",
		"my-svg-flowchart-invoice-11": "payments/charge-->invoice",
	}
}

// drawnFixture renders the fixture through the real path — a stub standing in
// for mmdc, then sanitize, theme and Stamp — and returns the SVG the page
// would receive.
func drawnFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(fixtureSVG)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "flowchart.mmd")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeMMDC(t, string(raw))
	rendered, failures := Attach(doc, &Renderer{Bin: bin, Cache: t.TempDir()})
	if rendered != 1 || len(failures) != 0 {
		t.Fatalf("rendered %d, failures %v", rendered, failures)
	}
	return doc.Blocks[0].SVG
}

func TestRealMermaidOutputIsAnchored(t *testing.T) {
	svg := drawnFixture(t)
	for id, want := range fixtureShapes() {
		got := anchorOf(t, svg, id)
		if got == want {
			continue
		}
		if got == "" {
			t.Errorf("%s carries no anchor — mermaid %s names this shape differently than the matcher expects; it draws but cannot be commented on", id, fixtureSVG)
			continue
		}
		t.Errorf("%s anchored to %q, want %q", id, got, want)
	}
}

// The other direction: every statement the document wrote must be reachable in
// the picture, or the reviewer can see something they cannot answer.
func TestEveryStatementIsReachableInTheDrawing(t *testing.T) {
	svg := drawnFixture(t)
	source, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "flowchart.mmd")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range doc.Blocks {
		if b.ID == doc.Blocks[0].ID {
			continue // the diagram itself is the page's own block
		}
		if !strings.Contains(svg, `data-anchor="`+b.ID+`"`) {
			t.Errorf("statement %q (%s) has no shape in the drawing", b.ID, b.Kind)
		}
	}
}

// The fixture is raw mermaid output, so it also proves the two rewrites that
// run before anchoring still do their job on the real thing.
func TestRealMermaidOutputIsSanitizedAndThemed(t *testing.T) {
	svg := drawnFixture(t)
	if !strings.HasPrefix(svg, "<svg") {
		t.Error("the XML prolog survived")
	}
	for _, bad := range []string{"<script", " onclick", "@import"} {
		if strings.Contains(svg, bad) {
			t.Errorf("sanitize left %q in real output", bad)
		}
	}
	// The rules that decide what a flowchart looks like, as this version of
	// mermaid writes them, each now reading the page's variables.
	for _, want := range []string{
		`.node rect,#my-svg .node circle,#my-svg .node ellipse,#my-svg .node polygon,#my-svg .node path{fill:var(--mg-node-fill);stroke:var(--mg-node-stroke);`,
		`.flowchart-link{stroke:var(--mg-line);fill:none;}`,
		`.marker{fill:var(--mg-line);stroke:var(--mg-line);}`,
		`.cluster rect{fill:var(--mg-cluster-fill);stroke:var(--mg-node-stroke);`,
		`.edgeLabel{background-color:var(--mg-label-bg);`,
		`.label text,#my-svg span{fill:var(--mg-text);color:var(--mg-text);}`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the palette rewrite missed a rule this version writes:\n  %s", want)
		}
	}
	// And the tolerance, on a real example: mermaid ships rules for shapes a
	// flowchart never draws (icons, images). We have no variable for those, so
	// they keep mermaid's own colours rather than being half-recoloured — and
	// they paint nothing here.
	if !strings.Contains(svg, `.icon-shape .icon{fill:#9370DB;`) {
		t.Error("a rule we have no role for was rewritten anyway")
	}
}

// A hit target per anchored edge, so every arrow in the real drawing is
// clickable rather than two pixels wide.
func TestRealMermaidEdgesGetHitTargets(t *testing.T) {
	svg := drawnFixture(t)
	edges := 0
	for id := range fixtureShapes() {
		if strings.Contains(id, "-L_") {
			edges++
		}
	}
	if got := strings.Count(svg, `class="mg-hit"`); got != edges {
		t.Errorf("%d hit targets for %d drawn edges", got, edges)
	}
}

// The alarm has to be real: a fixture whose ids no longer match must leave
// shapes unanchored rather than passing quietly. This is the same output with
// mermaid's names changed the way a major version might change them, and it
// proves the tests above would notice.
func TestAnchoringNoticesRenamedShapes(t *testing.T) {
	raw, err := os.ReadFile(fixtureSVG)
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.NewReplacer("L_", "E_", "-flowchart-", "-node-").Replace(string(raw))
	source, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "flowchart.mmd")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeMMDC(t, renamed)
	if _, failures := Attach(doc, &Renderer{Bin: bin, Cache: t.TempDir()}); len(failures) != 0 {
		t.Fatal(failures)
	}
	svg := doc.Blocks[0].SVG
	// Every edge and every box is now named in a way the matcher does not
	// know, so none of them answers for anything.
	for _, id := range []string{"my-svg-E_client_api_0", "my-svg-E_worker_queue_0", "my-svg-node-db-9", "my-svg-node-client-0"} {
		if !anchorOfMissing(svg, id) {
			t.Errorf("%s was anchored despite its name changing", id)
		}
	}
	// One shape survives a rename of the naming schemes, and it is the one
	// mermaid names after the diagram's own word rather than by a scheme: the
	// subgraph's cluster. Anything more than that would mean this fixture
	// cannot tell a mermaid change from a working day.
	if got := strings.Count(svg, "data-anchor="); got != 1 {
		t.Errorf("%d anchors survived a full rename, want 1 (the subgraph cluster)", got)
	}
	if got := anchorOf(t, svg, "my-svg-payments"); got != "payments" {
		t.Errorf("the cluster anchored to %q", got)
	}
}

// anchorOfMissing reports whether no tag in svg carries id with an anchor.
func anchorOfMissing(svg, id string) bool {
	for _, tag := range strings.Split(svg, "<") {
		if strings.Contains(tag, `id="`+id+`"`) && strings.Contains(tag, "data-anchor=") {
			return false
		}
	}
	return true
}
