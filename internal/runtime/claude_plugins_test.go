package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudePluginEvidenceRequiresPackageAndEveryComponent(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "missing-inventory", "missing-arrays", "wrong-version", "wrong-path", "wrong-source", "missing-command", "extra-command", "extra-agent", "disabled-hook", "unknown-policy", "failed-mcp", "alias", "disabled-loaded"} {
		t.Run(mode, func(t *testing.T) {
			selection := PluginSelection{AssetID: "plugin", Name: "fixture", Mode: "enabled", Version: "1", SourcePath: "/plugin", Components: []PluginComponent{{Kind: "command", NativeName: "fixture:action", Count: 1}, {Kind: "agent", NativeName: "fixture:checker", Count: 1}, {Kind: "hook", NativeName: "PreToolUse", Count: 1}, {Kind: "mcp", NativeName: "plugin:fixture:identity", Count: 1}}}
			var in claudePluginInventory
			if err := json.Unmarshal([]byte(`{"available":true,"at":"2026-10-02T00:00:00Z","packages":[{"name":"fixture","version":"1","path":"/plugin","source":"fixture@inline"}],"skills":{"available":true,"commands":[{"name":"fixture:action","aliases":["action"]}],"descriptions":[{"name":"fixture:action","source":"plugin","plugin_name":"fixture"}]},"agents":[{"name":"fixture:checker"}],"hooks":{"available":true,"items":[{"event":"PreToolUse","plugin_name":"fixture@inline"}],"policy":{}},"mcp":{"available":true,"servers":[{"name":"plugin:fixture:identity","source":"plugin","status":"connected"}]}}`), &in); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled":
				selection.Mode = "disabled"
				in.Packages = in.Packages[:0]
				in.Skills.Commands = in.Skills.Commands[:0]
				in.Skills.Descriptions = in.Skills.Descriptions[:0]
				in.Agents = in.Agents[:0]
				in.Hooks.Items = in.Hooks.Items[:0]
				in.MCP.Servers = in.MCP.Servers[:0]
			case "missing-inventory":
				in.Available = false
			case "missing-arrays":
				in.Packages = nil
			case "wrong-version":
				in.Packages[0].Version = "2"
			case "wrong-path":
				in.Packages[0].Path = "/elsewhere"
			case "wrong-source":
				in.Packages[0].Source = "fixture@market"
			case "missing-command":
				in.Skills.Commands = in.Skills.Commands[:0]
			case "extra-command":
				in.Skills.Commands = append(in.Skills.Commands, claudeSkillCommand{Name: "fixture:extra"})
			case "extra-agent":
				in.Agents = append(in.Agents, in.Agents[0])
			case "disabled-hook":
				in.Hooks.Items[0].Disabled = true
			case "unknown-policy":
				in.Hooks.Policy.PolicyUnreadable = true
			case "failed-mcp":
				in.MCP.Servers[0].Status = "failed"
			case "alias":
				in.Skills.Commands = append(in.Skills.Commands, claudeSkillCommand{Name: "other", Aliases: []string{"action"}})
			case "disabled-loaded":
				selection.Mode = "disabled"
			}
			view := projectClaude(Scope{NativeID: "original", PluginSelections: []PluginSelection{selection}}, claudeSnapshot{Live: true, Entries: []claudeEntry{{Kind: "initialized"}}, Plugins: in})
			want := mode == "enabled" || mode == "disabled"
			if len(view.PluginEvidence) != 1 || view.PluginEvidence[0].Verified != want || view.ExecutionClear != want {
				t.Fatalf("incorrect admission: %+v", view)
			}
		})
	}
}
