package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeRecoveryRestoresInstalledInheritance(t *testing.T) {
	const original = `{"available":true,"at":"2026-10-10T00:00:00Z","packages":[{"name":"selected","path":"/market/selected","source":"selected@owned","version":"1"}],"skills":{"available":true,"commands":[{"name":"selected:method","aliases":[],"builtin":false}],"descriptions":[{"name":"selected:method","source":"plugin","plugin_name":"selected"}]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[]}}`
	for _, scenario := range []string{"restored", "missing-inherited-package", "cache-substitution", "missing-skill", "extra-agent", "policy-drift", "missing-original-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			var before, current claudePluginInventory
			if json.Unmarshal([]byte(original), &before) != nil || json.Unmarshal([]byte(original), &current) != nil {
				t.Fatal("invalid fixture inventory")
			}
			change := &claudeConfigurationChange{InstalledTarget: &ClaudeInstalledSettings{Selections: []PluginSelection{{Name: "selected", NativeSource: "selected@owned", Mode: "disabled"}}}, OriginalPlugins: &before}
			switch scenario {
			case "missing-inherited-package":
				current.Packages = nil
			case "cache-substitution":
				current.Packages[0].Path = "/profile/cache/selected"
			case "missing-skill":
				current.Skills.Commands = []claudeSkillCommand{}
			case "extra-agent":
				_ = json.Unmarshal([]byte(`[{"name":"unexpected"}]`), &current.Agents)
			case "policy-drift":
				current.Hooks.Policy.ManagedOnly = true
			case "missing-original-receipt":
				change.OriginalPlugins = nil
			}
			if claudeRecoveredInheritedPlugins(Scope{}, change, current) != (scenario == "restored") {
				t.Fatal("recovery did not preserve the originally inherited installation")
			}
		})
	}
}
