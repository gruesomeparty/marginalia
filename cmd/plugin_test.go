package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The plugin manifest names a command the binary has to actually have.
//
// It is a JSON file nothing compiles, so a rename of the `mcp` subcommand
// would leave it pointing at nothing and the only symptom would be an agent
// quietly reporting "no MCP server, using the CLI" — which is exactly how this
// shipped missing in the first place.
func TestPluginManifestNamesRealCommands(t *testing.T) {
	var manifest struct {
		Version    string `json:"version"`
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	raw, err := os.ReadFile(filepath.Join("..", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version == "" {
		t.Error("the plugin needs a version")
	}
	srv, ok := manifest.MCPServers["marginalia"]
	if !ok {
		t.Fatal("the plugin must declare the MCP server, or installing it gives an agent the skills and none of the tools")
	}
	if srv.Command != "marginalia" {
		t.Errorf("command = %q, want the binary's own name", srv.Command)
	}
	if len(srv.Args) == 0 {
		t.Fatal("no args: the server is started by a subcommand")
	}
	// The named subcommand has to exist on the root command.
	want := srv.Args[0]
	for _, c := range newRootCmd().Commands() {
		if c.Name() == want {
			return
		}
	}
	t.Fatalf("the manifest starts `marginalia %s`, which is not a subcommand", want)
}
