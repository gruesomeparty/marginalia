package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "review.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func mustLoad(t *testing.T, body string) *Config {
	t.Helper()
	c, err := load(t, body)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestLoadConfig(t *testing.T) {
	c := mustLoad(t, `
title: Ingest spec review
instructions: |
  Focus on §3 — the retry policy.
actions:
  - type: blocker
    label: Blocker
    key: b
  - type: nit
  - type: legal
    label: Legal review
    requires_text: true
    fields:
      - name: severity
        options: [high, low]
        required: true
readonly:
  - "2"
require_verdict: true
`)
	if c.Title != "Ingest spec review" {
		t.Errorf("Title = %q", c.Title)
	}
	// A block scalar's trailing newline would render as an empty line.
	if c.Instructions != "Focus on §3 — the retry policy." {
		t.Errorf("Instructions = %q", c.Instructions)
	}
	if !c.RequireVerdict {
		t.Error("require_verdict lost")
	}
	// The built-ins come first, then the configured actions in order.
	var types []string
	for _, a := range c.Actions() {
		types = append(types, a.Type)
	}
	want := "comment,suggest_edit,question,approve,reject,blocker,nit,legal"
	if got := strings.Join(types, ","); got != want {
		t.Errorf("actions = %s, want %s", got, want)
	}
	// A label is derived when it isn't given, so a button always has words.
	nit, _ := c.Action("nit")
	if nit.Label != "Nit" {
		t.Errorf("derived label = %q", nit.Label)
	}
	legal, _ := c.Action("legal")
	if len(legal.Fields) != 1 || legal.Fields[0].Label != "Severity" {
		t.Errorf("field = %+v", legal.Fields)
	}
}

func TestBuiltinsOff(t *testing.T) {
	c := mustLoad(t, "builtins: false\nactions:\n  - type: blocker\n  - type: comment\n")
	var types []string
	for _, a := range c.Actions() {
		types = append(types, a.Type)
	}
	// With the built-ins off, `comment` is a word the agent may redefine.
	if got := strings.Join(types, ","); got != "blocker,comment" {
		t.Errorf("actions = %s", got)
	}
	if err := c.Validate("question", "hm", nil); err == nil {
		t.Error("a built-in that was turned off must be refused")
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"unknown key":          "titel: typo\n",
		"no actions at all":    "builtins: false\n",
		"empty type":           "actions:\n  - label: Blocker\n",
		"shouty type":          "actions:\n  - type: Blocker\n",
		"type already taken":   "actions:\n  - type: comment\n",
		"duplicate type":       "actions:\n  - type: nit\n  - type: nit\n",
		"duplicate key":        "actions:\n  - type: nit\n    key: n\n  - type: blocker\n    key: n\n",
		"multi-char key":       "actions:\n  - type: nit\n    key: nit\n",
		"reserved review_done": "actions:\n  - type: review_done\n",
		"bad field name":       "actions:\n  - type: nit\n    fields:\n      - name: Severity\n",
		"duplicate field":      "actions:\n  - type: nit\n    fields:\n      - name: sev\n      - name: sev\n",
		"empty option":         "actions:\n  - type: nit\n    fields:\n      - name: sev\n        options: [\"\"]\n",
		"empty readonly":       "readonly:\n  - \"  \"\n",
	}
	for name, body := range cases {
		if _, err := load(t, body); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected an error for a config that isn't there")
	}
}

// A read-only pattern covers the block it names and everything under it: a
// section, a list and its items, a diagram and its statements.
func TestLocked(t *testing.T) {
	c := mustLoad(t, "readonly:\n  - \"2\"\n  - \"1/3\"\n  - \"$.spec\"\n")
	locked := []string{"2", "2/1", "2.1", "2/4.2", "1/3", "1/3/client-->api", "$.spec", "$.spec.storage"}
	for _, id := range locked {
		if !c.Locked(id) {
			t.Errorf("%q should be read-only", id)
		}
	}
	for _, id := range []string{"1", "1/2", "21/1", "20", "$.spectrum", "3/1"} {
		if c.Locked(id) {
			t.Errorf("%q should be commentable", id)
		}
	}
}

func TestDefaultIsTheBuiltInReview(t *testing.T) {
	c := Default()
	if err := c.Validate("comment", "note", nil); err != nil {
		t.Errorf("the default review must accept a comment: %v", err)
	}
	if err := c.Validate("blocker", "note", nil); err == nil {
		t.Error("the default review has no blocker action")
	}
	if c.Locked("1/1") {
		t.Error("nothing is read-only by default")
	}
}

func TestValidate(t *testing.T) {
	c := mustLoad(t, `
actions:
  - type: blocker
  - type: legal
    requires_text: true
    fields:
      - name: severity
        options: [high, low]
        required: true
      - name: owner
`)
	ok := func(err error) bool { return err == nil }
	cases := []struct {
		name   string
		typ    string
		text   string
		fields map[string]string
		valid  bool
	}{
		{"a one-tap action needs nothing", "blocker", "", nil, true},
		{"an unknown type is refused", "bogus", "x", nil, false},
		{"a built-in still works", "comment", "note", nil, true},
		{"words are required when the action says so", "legal", "", map[string]string{"severity": "high"}, false},
		{"a required field is required", "legal", "look", nil, false},
		{"a choice must be one of the choices", "legal", "look", map[string]string{"severity": "urgent"}, false},
		{"a valid choice passes", "legal", "look", map[string]string{"severity": "low"}, true},
		{"free text field passes", "legal", "look", map[string]string{"severity": "low", "owner": "berkay"}, true},
		{"an undeclared field is refused", "legal", "look", map[string]string{"severity": "low", "sev": "x"}, false},
		{"fields on an action that has none", "blocker", "", map[string]string{"severity": "high"}, false},
	}
	for _, tc := range cases {
		if got := ok(c.Validate(tc.typ, tc.text, tc.fields)); got != tc.valid {
			t.Errorf("%s: Validate(%q, %q, %v) valid = %v, want %v", tc.name, tc.typ, tc.text, tc.fields, got, tc.valid)
		}
	}
}
