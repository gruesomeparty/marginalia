package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// goreleaser validates a publisher's `token:` against this exact regex and
// nothing else — copied verbatim from internal/tmpl/tmpl.go in v2.18.2, the
// version .github/workflows/release.yml pins. It means the field takes
// `{{ .Env.NAME }}` and only that: no `index .Env "NAME"`, no surrounding
// text, no default.
//
// This test exists because `goreleaser check` does NOT catch a violation — it
// validated the broken config happily — and the real check runs in the publish
// pipe, after the archives are built and the GitHub release is already out.
// v0.1.0 shipped with no Homebrew cask for exactly that reason: the failure
// arrives at the last step of a release that has otherwise already happened,
// and cannot be fixed without cutting another version.
var goreleaserEnvOnly = regexp.MustCompile(`^{{\s*\.Env\.[^.\s}]+\s*}}$`)

func TestGoreleaserTokensAreSingleEnvVars(t *testing.T) {
	raw, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}

	var found int
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "token:") {
			continue
		}
		found++
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "token:"))
		value = strings.Trim(value, `'"`)
		if !goreleaserEnvOnly.MatchString(value) {
			t.Errorf(".goreleaser.yaml:%d: token %q is not {{ .Env.NAME }};\n"+
				"goreleaser rejects it when it publishes, which is after the release is live", i+1, value)
		}
	}

	if found == 0 {
		t.Fatal("no token: field found in .goreleaser.yaml — if the publisher was removed, remove this test with it")
	}
}
