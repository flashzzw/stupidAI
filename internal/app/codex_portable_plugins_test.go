package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexPortablePluginSelectionUsesCanonicalIdentityAndSelectedSettings(t *testing.T) {
	for _, mode := range []string{"root-only", "inline", "empty-inline", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			root := cfg.CapabilityAssets[0].Source
			identity := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-plugin","version":"1.2.3"}`
			overlay := `{"name":"ignored-name","version":"9.9.9","hooks":"./ignored-hooks.json"}`
			selected := "plugin.json（默认）"
			if mode == "inline" {
				identity = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-plugin","version":"1.2.3","extensions":{"com.openai":{"description":"Owned inline configuration"}}}`
				selected = "plugin.json#extensions.com.openai"
			}
			if mode == "empty-inline" {
				identity = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-plugin","version":"1.2.3","extensions":{"com.openai":{}}}`
				selected = "plugin.json#extensions.com.openai"
			}
			if mode == "fallback" {
				overlay = `{"name":"ignored-name","version":"9.9.9","description":"Owned fallback configuration"}`
				selected = ".codex-plugin/plugin.json"
			}
			files := map[string]string{
				"plugin.json":               identity,
				".codex-plugin/plugin.json": overlay,
				"mcp.json":                  `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"canonical":{"type":"stdio","command":"python3","args":["-c","print('fixture')"]}}}`,
				".mcp.json":                 `{"mcpServers":{"ignored":{"command":"missing-legacy-command"}}}`,
				"skills/method/SKILL.md":    "---\nname: method\ndescription: Canonical method\n---\nOwned method.\n",
				"ignored-hooks.json":        `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"missing-ignored-hook"}]}]}}`,
			}
			for name, body := range files {
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "root-only" {
				if err := os.Remove(filepath.Join(root, ".codex-plugin/plugin.json")); err != nil {
					t.Fatal(err)
				}
			}
			a := openFixture(t, cfg, newFixture())
			r, err := a.Apply(Command{ID: mode, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
			if err != nil {
				t.Fatal(err)
			}
			read, err := a.ReadExecution(r.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			pack := read.Snapshot.PluginSelections[0]
			if pack.Name != "fixture-plugin" || pack.Version != "1.2.3" || len(pack.Components) != 2 {
				t.Fatalf("canonical identity/components lost: %+v", pack)
			}
			data, _ := json.Marshal(pack)
			var saved struct {
				Source *struct{ Identity, Settings, Layout string } `json:"manifest_source"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Source == nil || saved.Source.Identity != "plugin.json" || saved.Source.Settings != selected || saved.Source.Layout != "portable" {
				t.Fatalf("wrong selected manifest: %+v", saved.Source)
			}
			for _, component := range pack.Components {
				if component.Kind == "mcp" && (component.Name != "canonical" || component.Path != "mcp.json") {
					t.Fatalf("ignored MCP selected: %+v", component)
				}
				if component.Kind == "skill" && component.NativeName != "fixture-plugin:method" {
					t.Fatalf("overlay identity leaked: %+v", component)
				}
			}
		})
	}
}

func TestCodexPortablePluginSelectionRejectsUnverifiedSchemaAndCommands(t *testing.T) {
	for _, mode := range []string{"missing-plugin-schema", "unknown-plugin-schema", "missing-mcp-schema", "unknown-mcp-schema", "absolute-command", "escape-command"} {
		t.Run(mode, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			root := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-plugin","version":"1.2.3"}`
			mcp := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"canonical":{"type":"stdio","command":"python3","args":["-c","print('owned')"]}}}`
			switch mode {
			case "missing-plugin-schema":
				root = `{"name":"fixture-plugin","version":"1.2.3"}`
			case "unknown-plugin-schema":
				root = strings.Replace(root, "schemas/1.0.0/", "schemas/9.0.0/", 1)
			case "missing-mcp-schema":
				mcp = strings.Replace(mcp, `"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",`, "", 1)
			case "unknown-mcp-schema":
				mcp = strings.Replace(mcp, "schemas/1.0.0/", "schemas/9.0.0/", 1)
			case "absolute-command":
				mcp = strings.Replace(mcp, `"python3"`, `"/usr/bin/python3"`, 1)
			case "escape-command":
				mcp = strings.Replace(mcp, `"python3"`, `"./../outside"`, 1)
			}
			asset := formatAsset(t, map[string][]byte{"plugin.json": []byte(root), "mcp.json": []byte(mcp)})
			if strings.Contains(mode, "schema") {
				diagnosis := formatDiagnosis(t, asset)
				found := false
				for _, issue := range diagnosis.Issues {
					found = found || issue.Code == "native_schema_unverified"
				}
				if !found || diagnosis.NativeStatus != "unverified" {
					t.Fatal("library hides the native schema limitation")
				}
			}
			cfg.CapabilityAssets[0].Source = asset.Source
			f := newFixture()
			a := openFixture(t, cfg, f)
			_, err := a.Apply(Command{ID: mode, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
			if err == nil || f.creates.Load() != 0 {
				t.Fatal("unverified portable input dispatched")
			}
		})
	}
}
