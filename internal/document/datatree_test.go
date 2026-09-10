package document

import (
	"strings"
	"testing"
)

const jsonSrc = `{
  "apiVersion": "v1",
  "spec": {
    "storage": {
      "paths": ["/var/a", "/var/b", "/var/c"]
    },
    "replicas": 3,
    "debug": false,
    "quota": null,
    "odd key": "kept"
  }
}`

func TestJSONNodePaths(t *testing.T) {
	doc, err := ParseBytes("payload.json", []byte(jsonSrc))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if doc.Format != FormatJSON || !doc.IsTree() {
		t.Fatalf("format = %q", doc.Format)
	}
	// The acceptance criterion of issue #2: the node's JSONPath is its block.
	for _, want := range []string{
		"$",
		"$.apiVersion",
		"$.spec",
		"$.spec.storage",
		"$.spec.storage.paths",
		"$.spec.storage.paths[0]",
		"$.spec.storage.paths[2]",
		"$.spec.replicas",
		"$.spec.debug",
		"$.spec.quota",
		`$.spec["odd key"]`,
	} {
		if blockByID(doc, want) == nil {
			t.Errorf("no block anchored at %s", want)
		}
	}
}

// Key order is the author's. A decoder that sorts keys turns a review into a
// scavenger hunt.
func TestJSONPreservesKeyOrder(t *testing.T) {
	doc, err := ParseBytes("p.json", []byte(`{"zebra":1,"apple":2,"mango":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range doc.Blocks[1:] {
		got = append(got, b.ID)
	}
	want := []string{"$.zebra", "$.apple", "$.mango"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("key order = %v, want %v", got, want)
	}
}

func TestJSONScalarRendering(t *testing.T) {
	doc, err := ParseBytes("p.json", []byte(jsonSrc))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ id, kind, quote string }{
		{"$", kindObject, "{2 keys}"},
		{"$.apiVersion", kindString, `apiVersion: "v1"`},
		{"$.spec.replicas", kindNumber, "replicas: 3"},
		{"$.spec.debug", kindBool, "debug: false"},
		{"$.spec.quota", kindNull, "quota: null"},
		{"$.spec.storage.paths", kindArray, "paths: [3 items]"},
		{"$.spec.storage.paths[1]", kindString, `[1]: "/var/b"`},
	}
	for _, c := range cases {
		b := blockByID(doc, c.id)
		if b == nil {
			t.Errorf("%s missing", c.id)
			continue
		}
		if b.Kind != c.kind {
			t.Errorf("%s kind = %q, want %q", c.id, b.Kind, c.kind)
		}
		if b.Quote != c.quote {
			t.Errorf("%s quote = %q, want %q", c.id, b.Quote, c.quote)
		}
	}
	// A string and a number that look alike must not read alike: quoting is
	// how a reviewer spots `"8080"` where `8080` was meant.
	quoted, err := ParseBytes("p.json", []byte(`{"port":"8080","tls":"true"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := blockByID(quoted, "$.port").Quote; got != `port: "8080"` {
		t.Errorf("string quote = %q", got)
	}
}

// Changing one node must not move any other node's anchor: that is what makes
// the hash a stale-check rather than a noise generator.
func TestJSONHashIsPerNode(t *testing.T) {
	before, err := ParseBytes("p.json", []byte(jsonSrc))
	if err != nil {
		t.Fatal(err)
	}
	after, err := ParseBytes("p.json", []byte(strings.Replace(jsonSrc, `"replicas": 3`, `"replicas": 5`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if blockByID(before, "$.apiVersion").Hash != blockByID(after, "$.apiVersion").Hash {
		t.Error("an unrelated edit moved a sibling's anchor")
	}
	if blockByID(before, "$.spec.replicas").Hash == blockByID(after, "$.spec.replicas").Hash {
		t.Error("the edited node's hash should go stale")
	}
}

func TestJSONStructure(t *testing.T) {
	doc, err := ParseBytes("p.json", []byte(jsonSrc))
	if err != nil {
		t.Fatal(err)
	}
	spec := blockByID(doc, "$.spec")
	if spec.Parent != "$" || spec.Level != 1 || !spec.HasChildren {
		t.Errorf("spec block = %+v", spec)
	}
	leaf := blockByID(doc, "$.spec.storage.paths[2]")
	if leaf.Parent != "$.spec.storage.paths" || leaf.Level != 4 || leaf.HasChildren {
		t.Errorf("leaf block = %+v", leaf)
	}
}

func TestJSONErrors(t *testing.T) {
	if _, err := ParseBytes("p.json", []byte(`{"a":`)); err == nil {
		t.Error("expected an error for truncated JSON")
	}
	if _, err := ParseBytes("p.json", []byte(`{"a":1} {"b":2}`)); err == nil {
		t.Error("expected an error for trailing data after the top-level value")
	}
	if _, err := ParseBytes("p.json", []byte(`[1,`)); err == nil {
		t.Error("expected an error for a truncated array")
	}
}

func TestJSONEmptyContainers(t *testing.T) {
	doc, err := ParseBytes("p.json", []byte(`{"a":{},"b":[],"":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := blockByID(doc, "$.a").Quote; got != "a: {}" {
		t.Errorf("empty object quote = %q", got)
	}
	if got := blockByID(doc, "$.b").Quote; got != "b: []" {
		t.Errorf("empty array quote = %q", got)
	}
	// An empty key is a key, not a sequence element.
	if blockByID(doc, `$[""]`) == nil {
		t.Errorf("empty key lost: %+v", doc.Blocks)
	}
}

const yamlSrc = `# Deployment for the ingest service.
apiVersion: apps/v1
spec:
  # Two is the minimum that survives a node drain.
  replicas: 2   # raise with traffic
  storage:
    paths:
      - /var/a
      - /var/b
  debug: false
  quota: ~
  when: 2026-07-03
`

func TestYAMLNodePathsAndComments(t *testing.T) {
	doc, err := ParseBytes("k8s.yaml", []byte(yamlSrc))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if doc.Format != FormatYAML {
		t.Fatalf("format = %q", doc.Format)
	}
	for _, want := range []string{"$", "$.apiVersion", "$.spec.replicas", "$.spec.storage.paths[1]", "$.spec.quota"} {
		if blockByID(doc, want) == nil {
			t.Errorf("no block anchored at %s", want)
		}
	}
	replicas := blockByID(doc, "$.spec.replicas")
	if !strings.Contains(replicas.Quote, "Two is the minimum") {
		t.Errorf("head comment not carried into the anchor: %q", replicas.Quote)
	}
	if !strings.Contains(replicas.PlainText, "raise with traffic") {
		t.Errorf("line comment not carried into the anchor: %q", replicas.PlainText)
	}
	if !strings.Contains(replicas.HTML, `class="cmt"`) || strings.Contains(replicas.HTML, "#") {
		t.Errorf("comment markup = %q", replicas.HTML)
	}
	if got := blockByID(doc, "$.spec.quota").Kind; got != kindNull {
		t.Errorf("`~` kind = %q, want null", got)
	}
	if got := blockByID(doc, "$.spec.debug").Kind; got != kindBool {
		t.Errorf("debug kind = %q, want bool", got)
	}
	if got := blockByID(doc, "$.spec.when").Kind; got != "timestamp" {
		t.Errorf("date kind = %q, want the resolved tag", got)
	}
}

func TestYAMLMultiDocument(t *testing.T) {
	doc, err := ParseBytes("k8s.yaml", []byte("kind: Service\n---\nkind: Deployment\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$doc[0]", "$doc[0].kind", "$doc[1]", "$doc[1].kind"} {
		if blockByID(doc, want) == nil {
			t.Errorf("no block anchored at %s: %+v", want, doc.Blocks)
		}
	}
	if p := blockByID(doc, "$doc[1].kind").Parent; p != "$doc[1]" {
		t.Errorf("second document's node parented to %q", p)
	}
}

func TestYAMLAnchorsAndAliases(t *testing.T) {
	doc, err := ParseBytes("k.yaml", []byte("base: &b\n  cpu: 1\nprod: *b\n"))
	if err != nil {
		t.Fatal(err)
	}
	alias := blockByID(doc, "$.prod")
	if alias == nil || alias.Kind != "alias" || !strings.Contains(alias.Quote, "*b") {
		t.Errorf("alias block = %+v", alias)
	}
}

func TestYAMLErrors(t *testing.T) {
	if _, err := ParseBytes("k.yaml", []byte("a: [1,\n")); err == nil {
		t.Error("expected an error for malformed YAML")
	}
	if _, err := ParseBytes("k.yaml", []byte("# nothing but a comment\n")); err == nil {
		t.Error("expected an error for a YAML file with no documents")
	}
}

const tomlSrc = `title = "config"
enabled = true
ratio = 0.75
when = 2026-07-03T10:00:00Z

[server]
host = "127.0.0.1"
ports = [8080, 8081]

[[products]]
name = "hammer"
sku = 738594937

[[products]]
name = "nail"
sku = 284758393
`

func TestTOMLNodePathsAndOrder(t *testing.T) {
	doc, err := ParseBytes("config.toml", []byte(tomlSrc))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if doc.Format != FormatTOML {
		t.Fatalf("format = %q", doc.Format)
	}
	want := []string{
		"$", "$.title", "$.enabled", "$.ratio", "$.when",
		"$.server", "$.server.host", "$.server.ports", "$.server.ports[0]", "$.server.ports[1]",
		"$.products", "$.products[0]", "$.products[0].name", "$.products[0].sku",
		"$.products[1]", "$.products[1].name", "$.products[1].sku",
	}
	var got []string
	for _, b := range doc.Blocks {
		got = append(got, b.ID)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if k := blockByID(doc, "$.enabled").Kind; k != kindBool {
		t.Errorf("enabled kind = %q", k)
	}
	if q := blockByID(doc, "$.ratio").Quote; q != "ratio: 0.75" {
		t.Errorf("ratio quote = %q", q)
	}
	if k := blockByID(doc, "$.when").Kind; k != "datetime" {
		t.Errorf("datetime kind = %q", k)
	}
	if q := blockByID(doc, "$.products[1].sku").Quote; q != "sku: 284758393" {
		t.Errorf("sku quote = %q", q)
	}
}

func TestTOMLErrors(t *testing.T) {
	if _, err := ParseBytes("c.toml", []byte("a = \n")); err == nil {
		t.Error("expected an error for malformed TOML")
	}
}

func TestTOMLLocalDateAndUnorderedKeys(t *testing.T) {
	doc, err := ParseBytes("c.toml", []byte("day = 2026-07-03\n[t]\nb = 1\na = 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if b := blockByID(doc, "$.day"); b == nil || b.Quote == "day: " {
		t.Errorf("local date not rendered: %+v", b)
	}
	// Source order, not alphabetical.
	if doc.Blocks[len(doc.Blocks)-2].ID != "$.t.b" {
		t.Errorf("table key order lost: %+v", doc.Blocks)
	}
}

func TestDataHTMLEscapesContent(t *testing.T) {
	doc, err := ParseBytes("p.json", []byte(`{"<script>":"</script>"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range doc.Blocks {
		if strings.Contains(b.HTML, "<script>") || strings.Contains(b.HTML, "</script>") {
			t.Errorf("unescaped content in %q", b.HTML)
		}
	}
}
