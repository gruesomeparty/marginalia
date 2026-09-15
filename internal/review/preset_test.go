package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every shipped framing must load and validate. A broken preset is our bug,
// and it should fail here rather than in front of a reviewer.
func TestEveryPresetLoads(t *testing.T) {
	names := PresetNames()
	if len(names) < 4 {
		t.Fatalf("expected the shipped framings, got %v", names)
	}
	for _, name := range names {
		cfg, err := Preset(name)
		if err != nil {
			t.Errorf("preset %q: %v", name, err)
			continue
		}
		if cfg.Title == "" || cfg.Instructions == "" {
			t.Errorf("preset %q has no framing: title=%q instructions=%q", name, cfg.Title, cfg.Instructions)
		}
		if len(cfg.Custom) == 0 {
			t.Errorf("preset %q adds no vocabulary — it would be the default review under another name", name)
		}
		// The point of a preset is a vocabulary an agent can rely on, so the
		// words it writes to the log must be the ones the config declared.
		for _, a := range cfg.Custom {
			if a.Label == "" {
				t.Errorf("preset %q: action %q has no label", name, a.Type)
			}
		}
	}
}

func TestPresetSourceIsTheYAMLAsWritten(t *testing.T) {
	src, err := PresetSource("adr")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "title: Architecture decision review") {
		t.Errorf("source is not the file: %q", src[:min(80, len(src))])
	}
	// What `review show` prints must be loadable as a config file, or the
	// "copy it to disk as a starting point" promise is a lie.
	path := filepath.Join(t.TempDir(), "copied.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("a printed preset does not load as a config: %v", err)
	}
}

func TestUnknownPresetNamesTheOnesThatExist(t *testing.T) {
	_, err := Preset("architecture")
	if err == nil {
		t.Fatal("an unknown preset was accepted")
	}
	for _, want := range []string{"architecture", "adr", "security"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if _, err := PresetSource("nope"); err == nil {
		t.Error("PresetSource accepted an unknown name")
	}
}

// A file layered over a preset changes what it names and leaves the rest —
// which is what makes a preset a starting point rather than a cage.
func TestOverlayChangesOnlyWhatItNames(t *testing.T) {
	cfg, err := Preset("adr")
	if err != nil {
		t.Fatal(err)
	}
	actions, title := len(cfg.Actions()), cfg.Title
	path := filepath.Join(t.TempDir(), "over.yaml")
	if err := os.WriteFile(path, []byte("instructions: Only §3, please.\nrequire_verdict: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Overlay(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Instructions != "Only §3, please." {
		t.Errorf("instructions not overridden: %q", cfg.Instructions)
	}
	if !cfg.RequireVerdict {
		t.Error("require_verdict not overridden")
	}
	if cfg.Title != title {
		t.Errorf("title changed to %q — the overlay did not name it", cfg.Title)
	}
	if got := len(cfg.Actions()); got != actions {
		t.Errorf("vocabulary changed from %d to %d actions — the overlay did not name it", actions, got)
	}
	// Naming `actions` does replace the vocabulary wholesale.
	replace := filepath.Join(t.TempDir(), "actions.yaml")
	if err := os.WriteFile(replace, []byte("actions:\n  - type: nit\n    key: n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Overlay(replace); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Custom) != 1 || cfg.Custom[0].Type != "nit" {
		t.Errorf("naming actions did not replace them: %+v", cfg.Custom)
	}
}

// An overlay that cannot mean what it says is rejected exactly as a config
// file is: validation runs over the merged result, not the file alone.
func TestOverlayValidatesTheMergedConfig(t *testing.T) {
	cfg, err := Preset("adr")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clash.yaml")
	// `b` is already the ADR preset's blocker key.
	if err := os.WriteFile(path, []byte("actions:\n  - type: blocker\n    key: b\n  - type: bikeshed\n    key: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Overlay(path); err == nil {
		t.Fatal("a duplicate key survived the overlay")
	}
}
