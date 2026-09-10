package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

func TestRenderSelfContained(t *testing.T) {
	doc, _ := document.ParseBytes("d.md", []byte("# Title\n\nHello world.\n"))
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc, Author: "berkay"}); err != nil {
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
	if err := Render(&buf, Page{Doc: doc, Events: ev, Author: "a"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "note") {
		t.Error("existing event not embedded")
	}
}

func TestRenderTreeDocument(t *testing.T) {
	doc, err := document.ParseBytes("api.proto", []byte("message M {\n  string a = 1;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc, Author: "berkay"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`<main id="doc" class="tree">`,
		`id="fold"`,
		`data-block="M"`,
		`data-kids="1"`,
		`data-block="M/a"`,
		`data-level="1"`,
		`data-parent="M"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q", want)
		}
	}
}

func TestRenderMarkdownHasNoTreeChrome(t *testing.T) {
	doc, _ := document.ParseBytes("d.md", []byte("# Title\n"))
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc, Author: "berkay"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `id="fold"`) {
		t.Error("prose documents should not get the fold control")
	}
}

func TestRenderNestedNav(t *testing.T) {
	doc, _ := document.ParseBytes("spec.md", []byte("# Spec\n"))
	var buf bytes.Buffer
	err := Render(&buf, Page{
		Doc:    doc,
		Author: "berkay",
		Title:  "Handover",
		Nested: true,
		Docs: []NavDoc{
			{Label: "spec.md", Rel: "spec.md", Current: true},
			{Label: "docs/plan.md", Rel: "docs/plan.md", Count: 2},
			{Label: "docs/deep/api.proto", Rel: "docs/deep/api.proto", Done: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// A discovered set mirrors its folders, so the reviewer can see the shape.
	for _, want := range []string{
		`<span class="dir">docs/</span>`,
		`<span class="dir">deep/</span>`,
		`href="/d/docs/deep/api.proto"`,
		`>api.proto</a>`,
		`<span class="n">2</span>`,
		"3 documents · 1 done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nested nav missing %q", want)
		}
	}
	if strings.Contains(out, `>docs/plan.md</a>`) {
		t.Error("a nested entry should show its file name, not its whole path")
	}
}

// With an index the curated list is the tree: its order, its labels, no
// directory grouping the author didn't ask for.
func TestRenderFlatNavKeepsIndexOrderAndLabels(t *testing.T) {
	doc, _ := document.ParseBytes("api.proto", []byte("message M { string a = 1; }\n"))
	var buf bytes.Buffer
	err := Render(&buf, Page{
		Doc:   doc,
		Title: "API contract review",
		Docs: []NavDoc{
			{Label: "Order service contract", Rel: "api/orders.proto", Current: true},
			{Label: "Rollout plan", Rel: "docs/plan.md"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, `class="dir"`) {
		t.Error("a curated set should not be regrouped by directory")
	}
	first, second := strings.Index(out, "Order service contract"), strings.Index(out, "Rollout plan")
	if first < 0 || second < 0 || first > second {
		t.Errorf("labels missing or reordered: %d, %d", first, second)
	}
}

func TestRenderSingleDocumentHasNoSetChrome(t *testing.T) {
	doc, _ := document.ParseBytes("spec.md", []byte("# Spec\n"))
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc, Docs: []NavDoc{{Label: "spec.md", Rel: "spec.md", Current: true}}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// The set controls are markup, not script: the page's JS is one template
	// and stays inert when there is no set to navigate.
	for _, bad := range []string{`class="nav"`, `id="finish"`, "Done with this file"} {
		if strings.Contains(out, bad) {
			t.Errorf("single-document review should not show %q", bad)
		}
	}
	if !strings.Contains(out, `<button id="done" type="button">Done</button>`) {
		t.Error("single-document review should keep the plain Done button")
	}
	if !strings.Contains(out, "<strong>spec.md</strong>") {
		t.Error("a single document titles itself")
	}
}
