//go:build linux && nativecodex

package runtime_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexNativePortableRootPlugin(t *testing.T) { codexNativePluginSkillFlow(t, "portable-root") }
func TestCodexNativePortableInlinePlugin(t *testing.T) {
	codexNativePluginSkillFlow(t, "portable-inline")
}
func TestCodexNativePortableFallbackPlugin(t *testing.T) {
	codexNativePluginSkillFlow(t, "portable-fallback")
}

func nativePortableSettings(layout string) string {
	switch layout {
	case "inline":
		return "plugin.json#extensions.com.openai"
	case "fallback":
		return ".codex-plugin/plugin.json"
	default:
		return "plugin.json（默认）"
	}
}

func writeNativePortablePlugin(t *testing.T, root, layout, version string, mcp []byte) {
	t.Helper()
	write := func(name string, body any) {
		t.Helper()
		var data []byte
		switch value := body.(type) {
		case string:
			data = []byte(value)
		default:
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	identity := map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "mixed", "version": version + ".0.0"}
	overlay := map[string]any{"name": "ignored-identity", "version": "9.9.9", "skills": "./legacy-skills"}
	switch layout {
	case "inline":
		identity["extensions"] = map[string]any{"com.openai": map[string]any{}}
		overlay["hooks"] = "./ignored-hooks.json"
		overlay["mcpServers"] = "./ignored-mcp.json"
	case "fallback":
		overlay["mcpServers"] = "./selected-mcp.json"
	}
	write("plugin.json", identity)
	if layout == "root" {
		if err := os.Remove(filepath.Join(root, ".codex-plugin/plugin.json")); err != nil {
			t.Fatal(err)
		}
	} else {
		write(".codex-plugin/plugin.json", overlay)
	}
	var config struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcp, &config); err != nil {
		t.Fatal(err)
	}
	for _, server := range config.Servers {
		server["type"] = "stdio"
		if layout == "root" {
			args := server["args"].([]any)
			write("mcp-server.py", "#!/usr/bin/env python3\n"+args[2].(string))
			if err := os.Chmod(filepath.Join(root, "mcp-server.py"), 0700); err != nil {
				t.Fatal(err)
			}
			server["command"], server["args"] = "./mcp-server.py", []any{args[3]}
		}
	}
	write("mcp.json", map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": config.Servers})
	if layout == "fallback" {
		for _, server := range config.Servers {
			write("selected-mcp.json", map[string]any{"mcpServers": map[string]any{"selected_service_v" + version: server}})
		}
	}
	ignored := map[string]any{"mcpServers": map[string]any{"ignored_mcp": map[string]any{"command": "netcap-owned-ignored-command-must-not-run"}}}
	write(".mcp.json", ignored)
	write("ignored-mcp.json", ignored)
	write("ignored-hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"netcap-owned-ignored-hook-must-not-run"}]}]}}`)
	write("legacy-skills/ignored/SKILL.md", "---\nname: ignored\ndescription: PORTABLE_IGNORED_SKILL\n---\nIGNORED_SKILL_BODY\n")
}
