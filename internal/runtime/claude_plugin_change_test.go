package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeReleasedPluginCannotReturnAsInherited(t *testing.T) {
	for _, mode := range []string{"released", "package-returned", "alias-returned", "hook-returned", "mcp-returned", "inventory-missing", "applying"} {
		t.Run(mode, func(t *testing.T) {
			var snapshot claudeSnapshot
			if err := json.Unmarshal([]byte(`{"live":true,"config":{"id":"original"},"entries":[{"kind":"initialized"}],"mcp":{"available":true,"servers":[]},"plugins":{"available":true,"at":"now","packages":[],"skills":{"available":true,"commands":[],"descriptions":[]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[]}},"configuration":{"status":"verified","plugin_target":[],"plugin_previous":[{"asset_id":"plugin","name":"fixture","mode":"enabled"}]}}`), &snapshot); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "package-returned":
				snapshot.Plugins.Packages = []claudePluginPackage{{Name: "fixture"}}
			case "alias-returned":
				_ = json.Unmarshal([]byte(`[{"name":"other","aliases":["fixture:method"]}]`), &snapshot.Plugins.Skills.Commands)
			case "hook-returned":
				_ = json.Unmarshal([]byte(`[{"event":"PreToolUse","plugin_name":"fixture@inline"}]`), &snapshot.Plugins.Hooks.Items)
			case "mcp-returned":
				snapshot.Plugins.MCP.Servers = []claudeMCPStatus{{Name: "plugin:fixture:identity", Source: "plugin", Status: "connected"}}
			case "inventory-missing":
				snapshot.Plugins.Available = false
			case "applying":
				snapshot.Configuration.Status = "applying"
			}
			view := projectClaude(Scope{NativeID: "original"}, snapshot)
			if view.ExecutionClear != (mode == "released") {
				t.Fatalf("wrong retired plugin admission: %+v", view)
			}
		})
	}
}
