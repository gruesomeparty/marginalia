package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/hosted"
)

// deploy/cloudflare is the deployment the repo ships, and these are the
// ways it can rot without anything else noticing: its image tag drifting
// from the release it sits in, the single-writer pin loosening, something
// account-specific creeping into the config, and its settings file naming a
// variable the binary never reads — which fails silently, as a setting that
// does nothing.

const templateDir = "deploy/cloudflare"

func TestTemplateImageIsTheRelease(t *testing.T) {
	var manifest map[string]string
	readJSON(t, ".release-please-manifest.json", &manifest)
	version := manifest["."]
	if version == "" {
		t.Fatal(".release-please-manifest.json has no version for the root package")
	}

	raw := readFile(t, filepath.Join(templateDir, "Dockerfile"))
	var from []string
	inBlock := false
	for _, line := range strings.Split(raw, "\n") {
		switch strings.TrimSpace(line) {
		case "# x-release-please-start-version":
			inBlock = true
			continue
		case "# x-release-please-end":
			inBlock = false
			continue
		}
		if strings.HasPrefix(line, "FROM ") {
			if !inBlock {
				t.Errorf("Dockerfile: %q is outside the x-release-please block, so a release would not move it", line)
			}
			from = append(from, strings.TrimSpace(strings.TrimPrefix(line, "FROM ")))
		}
	}
	want := "ghcr.io/gruesomeparty/marginalia:" + version
	if len(from) != 1 || from[0] != want {
		t.Errorf("Dockerfile builds FROM %v, want exactly %q — the image of the release this template ships in", from, want)
	}

	var config struct {
		Packages map[string]struct {
			ExtraFiles []string `json:"extra-files"`
		} `json:"packages"`
	}
	readJSON(t, "release-please-config.json", &config)
	listed := false
	for _, f := range config.Packages["."].ExtraFiles {
		listed = listed || f == templateDir+"/Dockerfile"
	}
	if !listed {
		t.Errorf("release-please-config.json does not list %s/Dockerfile in extra-files, so a release leaves its tag behind", templateDir)
	}
}

func TestTemplateStaysGenericAndSingleInstance(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal(stripJSONC(readFile(t, filepath.Join(templateDir, "wrangler.jsonc"))), &config); err != nil {
		t.Fatalf("wrangler.jsonc: %v", err)
	}
	// What belongs to one deployment comes from the command line or the
	// secrets file, never from the shared config.
	for _, key := range []string{"account_id", "route", "routes", "vars", "zone_id"} {
		if _, ok := config[key]; ok {
			t.Errorf("wrangler.jsonc sets %q: that is one deployment's value, and goes on the deploy command line", key)
		}
	}
	// A second hostname would be one Access does not sit in front of.
	for _, key := range []string{"workers_dev", "preview_urls"} {
		if v, ok := config[key].(bool); !ok || v {
			t.Errorf("wrangler.jsonc: %s = %v, want false", key, config[key])
		}
	}
	containers, _ := config["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("wrangler.jsonc declares %d containers, want 1", len(containers))
	}
	c, _ := containers[0].(map[string]any)
	// The Durable Object starts the container with an image and an instance
	// size, so the policy has to be the one that lets it.
	if p := c["scheduling_policy"]; p != "durable_object" {
		t.Errorf("wrangler.jsonc: scheduling_policy = %v, want durable_object — src/index.ts starts the container itself", p)
	}
	// Under that policy there is no max_instances: there is one container
	// because the Worker only ever names one Durable Object. The host is its
	// store's single writer, so a second name would be a second writer.
	worker := readFile(t, filepath.Join(templateDir, "src", "index.ts"))
	if n := strings.Count(worker, "getContainer("); n != 1 || !strings.Contains(worker, "getContainer(env.MARGINALIA)") {
		t.Errorf("src/index.ts: want exactly one getContainer(env.MARGINALIA), with no name — the singleton; found %d getContainer calls", n)
	}
	for _, other := range []string{"idFromName", "newUniqueId", "idFromString", "getRandom", "getContainer(env.MARGINALIA,"} {
		if strings.Contains(worker, other) {
			t.Errorf("src/index.ts uses %s: every Durable Object is a container, and the host must run once", other)
		}
	}
}

// Every MARGINALIA_* name in the settings file, set or commented out, is one
// the binary reads somewhere.
func TestTemplateSettingsAreReadByTheBinary(t *testing.T) {
	names := regexp.MustCompile(`MARGINALIA_[A-Z0-9_]*[A-Z0-9]`).FindAllString(readFile(t, filepath.Join(templateDir, "settings.example.env")), -1)
	if len(names) == 0 {
		t.Fatal("settings.example.env names no setting")
	}
	var source strings.Builder
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			source.WriteString(readFile(t, path))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		if !strings.Contains(source.String(), `"`+name+`"`) {
			t.Errorf("settings.example.env names %s, which no non-test Go file reads", name)
		}
	}
}

// stripJSONC removes // and /* */ comments outside strings, which is all
// the JSONC wrangler.jsonc uses.
func stripJSONC(src string) []byte {
	var out []byte
	inString, escaped := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inString:
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
			out = append(out, c)
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 3
		default:
			out = append(out, c)
		}
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(readFile(t, path)), v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// Every agent route the host serves lives under /mcp/, because that prefix
// is what the Worker refuses at the edge without a bearer, or with a wrong
// static token, before it forwards anything and so before the container
// wakes (issue #186). A route outside it would be one anyone could hammer
// to keep a billed container awake.
func TestTemplateRefusesAgentRoutesAtTheEdge(t *testing.T) {
	if !strings.HasPrefix(hosted.AgentWaitPath, "/mcp/") {
		t.Errorf("the agent wait %s is outside /mcp/, which the Worker does not guard", hosted.AgentWaitPath)
	}
	src := readFile(t, filepath.Join(templateDir, "src", "index.ts"))
	if !strings.Contains(src, `path !== "/mcp" && !path.startsWith("/mcp/")`) {
		t.Error("the Worker no longer treats /mcp/ as the agents' prefix")
	}
	refuse, forward := strings.Index(src, "await refuse("), strings.Index(src, "container.fetch(request)")
	if refuse < 0 || forward < 0 || refuse > forward {
		t.Error("the Worker must refuse before it forwards to the container")
	}
}
