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
