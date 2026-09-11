package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// seed writes a document, one event per given suggestion, and returns the path.
func seed(t *testing.T, body string, events ...feedback.Event) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	hashOf := map[string]string{}
	for _, b := range doc.Blocks {
		hashOf[b.ID] = b.Hash
	}
	store := feedback.NewStore(path)
	for _, e := range events {
		if e.Hash == "auto" {
			e.Hash = hashOf[e.Block]
		}
		if err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// The acceptance criterion of issue #6: the unchanged block's suggestion is
// applicable, the edited block's is not, and the document is never written.
func TestSuggestionsReportsWhatIsSafeToApply(t *testing.T) {
	body := "# Spec\n\nCapped at 500.\n\nRetries: five.\n"
	path := seed(t, body,
		feedback.Event{Block: "1/2", Quote: "Capped at 500.", Hash: "auto", Type: feedback.TypeSuggestEdit, Text: "Capped at 5000.", Ts: "2026-07-03T10:00:00Z"},
		feedback.Event{Block: "1/3", Quote: "Retries: five.", Hash: "auto", Type: feedback.TypeSuggestEdit, Text: "Retries: three.", Ts: "2026-07-03T10:01:00Z"},
	)
	// The author revises the first block after the review.
	if err := os.WriteFile(path, []byte(strings.Replace(body, "Capped at 500.", "Capped at 2000.", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, "suggestions", path, "--json")
	if err != nil {
		t.Fatalf("suggestions: %v\n%s", err, out)
	}
	var reports []struct {
		Doc        string `json:"doc"`
		Applicable []struct {
			Block       string `json:"block"`
			Hash        string `json:"hash"`
			Current     string `json:"current"`
			Replacement string `json:"replacement"`
		} `json:"applicable"`
		NeedsConfirmation []struct {
			Block  string `json:"block"`
			Reason string `json:"reason"`
		} `json:"needs_confirmation"`
	}
	if err := json.Unmarshal([]byte(out), &reports); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(reports) != 1 || len(reports[0].Applicable) != 1 || len(reports[0].NeedsConfirmation) != 1 {
		t.Fatalf("report = %+v", reports)
	}
	got := reports[0].Applicable[0]
	if got.Block != "1/3" || got.Replacement != "Retries: three." || got.Current != "Retries: five." || got.Hash == "" {
		t.Errorf("applicable = %+v", got)
	}
	if stale := reports[0].NeedsConfirmation[0]; stale.Block != "1/2" || stale.Reason != feedback.ReasonStale {
		t.Errorf("needs confirmation = %+v", stale)
	}

	// Never mutate the source document.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("suggestions must not write to the document")
	}
}

func TestSuggestionsTextOutputAndEmptyCase(t *testing.T) {
	path := seed(t, "# Spec\n\nBody.\n",
		feedback.Event{Block: "1/2", Quote: "Body.", Hash: "auto", Type: feedback.TypeSuggestEdit, Text: "Better body.", Ts: "2026-07-03T10:00:00Z"},
	)
	out, err := runCmd(t, "suggestions", path)
	if err != nil {
		t.Fatalf("suggestions: %v\n%s", err, out)
	}
	for _, want := range []string{"1 applicable", "applicable", "current: Body.", "replace: Better body."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	quiet := seed(t, "# Spec\n\nBody.\n")
	out, err = runCmd(t, "suggestions", quiet)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if !strings.Contains(out, "no suggestions") {
		t.Errorf("a document with no feedback should say so: %q", out)
	}
}

func TestSuggestionsRequiresAPath(t *testing.T) {
	out, err := runCmd(t, "suggestions")
	if err == nil {
		t.Fatal("expected an error with no arguments")
	}
	if !strings.Contains(err.Error(), "marginalia suggestions spec.md") {
		t.Errorf("error should say what to type: %v (%s)", err, out)
	}
	if strings.Contains(err.Error(), "request-feature") {
		t.Errorf("a usage mistake should not advertise the feature tracker: %v", err)
	}
}
