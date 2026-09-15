package review

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Presets are review framings that ship inside the binary.
//
// A configured review (#3) is the difference between "tell me what you think"
// and a vocabulary the agent can act on mechanically. But every agent writing
// that YAML from scratch means the same review — is this ADR sound, is this
// schema change safe — arrives with a different vocabulary each time, and the
// events downstream cannot be compared across two runs of the same workflow.
// A named framing makes the common cases consistent without taking away the
// file: a preset is a starting point, and `--config` still layers over it.
//
//go:embed presets/*.yaml
var presetFS embed.FS

const presetDir = "presets"

// PresetNames lists the framings that ship, in alphabetical order.
func PresetNames() []string {
	entries, err := fs.ReadDir(presetFS, presetDir)
	if err != nil {
		return nil // impossible: the directory is compiled in
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}

// PresetSource returns a preset's YAML as written, so an agent can read the
// vocabulary it is about to impose — and copy it to disk as a starting point
// rather than inventing one.
func PresetSource(name string) (string, error) {
	if !knownPreset(name) {
		return "", unknownPreset(name)
	}
	data, err := presetFS.ReadFile(path.Join(presetDir, name+".yaml"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Preset loads a shipped framing by name.
func Preset(name string) (*Config, error) {
	src, err := PresetSource(name)
	if err != nil {
		return nil, err
	}
	c := Default()
	if err := decodeInto(strings.NewReader(src), c); err != nil {
		return nil, fmt.Errorf("preset %q: %w", name, err)
	}
	if err := c.validate(); err != nil {
		// A broken preset is our bug, not the caller's, and it fails at the
		// first use rather than silently framing a review wrongly.
		return nil, fmt.Errorf("preset %q: %w", name, err)
	}
	return c, nil
}

func knownPreset(name string) bool {
	for _, n := range PresetNames() {
		if n == name {
			return true
		}
	}
	return false
}

func unknownPreset(name string) error {
	return fmt.Errorf("unknown review preset %q — available: %s", name, strings.Join(PresetNames(), ", "))
}

// Overlay decodes a config file onto this config, so a file can start from a
// preset and change only what it means to change. Absent keys are left alone,
// which is how yaml.v3 decodes into a populated struct — and is exactly the
// layering we want: naming `actions` replaces the vocabulary wholesale, while
// naming only `instructions` reframes a preset without touching its verbs.
func (c *Config) Overlay(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := decodeInto(f, c); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// decodeInto is the one place the decoder is configured: unknown keys are an
// error wherever a config comes from, because a misspelled option that
// silently does nothing leaves the agent thinking it framed a review it did
// not.
func decodeInto(r io.Reader, c *Config) error {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return err
	}
	return nil
}
