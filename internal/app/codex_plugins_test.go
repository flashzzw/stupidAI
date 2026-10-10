package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexPluginFixture(t *testing.T) Config {
	t.Helper()
	c, _ := executionFixture(t)
	root := t.TempDir()
	for path, body := range map[string]string{
		".codex-plugin/plugin.json": `{"name":"fixture-plugin","version":"1.2.3"}`,
		".mcp.json":                 `{"mcpServers":{"identity":{"command":"python3","args":["-c","print('fixture')"]}}}`,
	} {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "plugin", Name: "Fixture plugin", Kind: "cli_plugin", Version: "asset-v1", Source: root, Backends: []string{"codex"}}}
	return c
}

func TestCodexPluginCreationRejectsUnverifiedComponents(t *testing.T) {
	for _, kind := range []string{"hook", "skill-name-conflict", "portable-unverified-component", "invalid-version", "duplicate-mcp"} {
		t.Run(kind, func(t *testing.T) {
			c := codexPluginFixture(t)
			source := c.CapabilityAssets[0].Source
			path, body := "", ""
			switch kind {
			case "hook":
				path = "hooks/hooks.json"
				body = `{"hooks":{"SessionStart":[]}}`
			case "skill-name-conflict":
				p := filepath.Join(source, "skills/method/SKILL.md")
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("---\nname: method\ndescription: first method\n---\n"), 0600); err != nil {
					t.Fatal(err)
				}
				path = "skills/other/SKILL.md"
				body = "---\nname: method\ndescription: method\n---\nbody"
			case "portable-unverified-component":
				path = "plugin.json"
				body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-plugin","version":"1.2.3","extensions":{"com.openai":{"apps":"./apps.json"}}}`
			case "invalid-version":
				path = ".codex-plugin/plugin.json"
				body = `{"name":"fixture-plugin","version":"../other"}`
			case "duplicate-mcp":
				path = ".codex-plugin/plugin.json"
				body = `{"name":"fixture-plugin","version":"1.2.3","mcpServers":{"identity":{"command":"other"}}}`
			}
			path = filepath.Join(source, path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			f := newFixture()
			a := openFixture(t, c, f)
			_, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
			if err == nil || f.creates.Load() != 0 {
				t.Fatal("unverified plugin was dispatched")
			}
		})
	}
}

func TestCodexPluginCreationBindsPrivateNativeSource(t *testing.T) {
	c := codexPluginFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	var previous string
	for _, mode := range []string{"enabled", "disabled"} {
		r, err := a.Apply(Command{ID: "create-" + mode, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: mode}}})
		if err != nil {
			t.Fatal(err)
		}
		read, err := a.ReadExecution(r.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(read.Snapshot.PluginSelections) != 1 {
			t.Fatal("missing native plugin selection")
		}
		p := read.Snapshot.PluginSelections[0]
		b, _ := json.Marshal(p)
		var fields map[string]any
		_ = json.Unmarshal(b, &fields)
		id, _ := fields["native_source"].(string)
		cache, _ := fields["cache_path"].(string)
		if !strings.HasPrefix(id, "fixture-plugin@netcap-") || id == previous || cache == "" || !strings.HasPrefix(cache, filepath.Join(c.Instances[0].Profile, "plugins/cache")+string(filepath.Separator)) {
			t.Fatalf("not independently bound: %s", b)
		}
		if p.Mode != mode || p.SourcePath != c.CapabilityAssets[0].Source || p.Version != "1.2.3" || len(p.Components) != 1 || p.Components[0].Kind != "mcp" || p.Components[0].NativeName != "identity" || len(p.Files) != 3 {
			t.Fatalf("wrong source/component binding: %+v", p)
		}
		if read.Snapshot.CapabilityRefs[0].Asset.Version != "asset-v1" {
			t.Fatal("lost asset version")
		}
		if _, err := os.Stat(cache); !os.IsNotExist(err) {
			t.Fatal("snapshot creation should not materialize native cache")
		}
		previous = id
	}
	if f.sends.Load() != 0 {
		t.Fatal("selection sent model input")
	}
}

func TestCodexMixedPluginCreationKeepsSkillAndMCPInOneBinding(t *testing.T) {
	c := codexPluginFixture(t)
	p := filepath.Join(c.CapabilityAssets[0].Source, "skills/method/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\nname: method\ndescription: Owned method\n---\nUse the owned method.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newFixture()
	a := openFixture(t, c, f)
	r, err := a.Apply(Command{ID: "mixed", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	pack := read.Snapshot.PluginSelections[0]
	components := map[string]string{}
	for _, component := range pack.Components {
		components[component.Kind] = component.NativeName
	}
	if len(pack.Components) != 2 || components["skill"] != "fixture-plugin:method" || components["mcp"] != "identity" || pack.CachePath == "" || len(read.Snapshot.CapabilityRefs) != 1 {
		t.Fatalf("mixed package split or lost components: %+v", read.Snapshot)
	}
	if f.sends.Load() != 0 {
		t.Fatal("binding preparation sent model input")
	}
}

func TestCodexSkillOnlyPluginCreationBindsWithoutMCP(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			c := codexPluginFixture(t)
			root := c.CapabilityAssets[0].Source
			if err := os.Remove(filepath.Join(root, ".mcp.json")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "skills/method/SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("---\nname: method\ndescription: Owned method\n---\nOnly a method.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			a := openFixture(t, c, newFixture())
			r, err := a.Apply(Command{ID: "skill-only", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: mode}}})
			if err != nil {
				t.Fatal(err)
			}
			read, err := a.ReadExecution(r.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			pack := read.Snapshot.PluginSelections[0]
			if len(pack.Components) != 1 || pack.Components[0].Kind != "skill" || pack.Mode != mode || pack.NativeSource == "" || len(read.Snapshot.MCPSelections) != 0 {
				t.Fatal("Skill-only package gained MCP or lost its binding")
			}
		})
	}
}
