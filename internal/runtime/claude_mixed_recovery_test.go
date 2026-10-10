package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeRecoveryRejectsCandidateComponents(t *testing.T) {
	for _, mode := range []string{"retired", "skill-name", "skill-alias", "skill-description", "plugin-package", "plugin-alias", "plugin-hook", "plugin-mcp", "missing-live-inventory", "missing-durable-inventory", "durable-candidate-hook", "offline-retired"} {
		t.Run(mode, func(t *testing.T) {
			var s claudeSnapshot
			if err := json.Unmarshal([]byte(`{"live":true,"config":{"id":"original"},"entries":[{"kind":"initialized"}],"mcp":{"available":true,"servers":[]},"skills":{"available":true,"commands":[],"descriptions":[]},"plugins":{"available":true,"at":"2026-10-06T00:00:00Z","packages":[],"skills":{"available":true,"commands":[],"descriptions":[]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[]}}}`), &s); err != nil {
				t.Fatal(err)
			}
			oldSkill := SkillSelection{AssetID: "method", Name: "method:run", NativeKey: "method:run", Source: "plugin", Delivery: "local_plugin", PluginName: "method", Mode: "disabled"}
			oldPlugin := PluginSelection{AssetID: "pack", Name: "fixture", Mode: "disabled"}
			skill, plugin := oldSkill, oldPlugin
			skill.Mode, plugin.Mode = "enabled", "enabled"
			scope := Scope{NativeID: "original", SkillSelections: []SkillSelection{oldSkill}, PluginSelections: []PluginSelection{oldPlugin}}
			s.Configuration = &claudeConfigurationChange{Status: "recovered", SkillPrevious: scope.SkillSelections, PluginPrevious: scope.PluginSelections, SkillTarget: []SkillSelection{skill}, PluginTarget: []PluginSelection{plugin}, Recovery: &claudeConfigurationRecovery{Operation: "recovery", Status: "verified", At: "2026-10-06T00:00:00Z", HistoryDigest: strings.Repeat("a", 64), Inventory: s.MCP, Skills: s.Skills, Plugins: s.Plugins}}
			switch mode {
			case "skill-name":
				s.Skills.Commands = []claudeSkillCommand{{Name: "method:run"}}
			case "skill-alias":
				s.Skills.Commands = []claudeSkillCommand{{Name: "other", Aliases: []string{"method:run"}}}
			case "skill-description":
				_ = json.Unmarshal([]byte(`[{"name":"method:run","source":"plugin","plugin_name":"method"}]`), &s.Skills.Descriptions)
			case "plugin-package":
				s.Plugins.Packages = []claudePluginPackage{{Name: "fixture"}}
			case "plugin-alias":
				s.Plugins.Skills.Commands = []claudeSkillCommand{{Name: "other", Aliases: []string{"fixture:run"}}}
			case "plugin-hook":
				_ = json.Unmarshal([]byte(`[{"event":"SessionStart","plugin_name":"fixture@inline"}]`), &s.Plugins.Hooks.Items)
			case "plugin-mcp":
				s.Plugins.MCP.Servers = []claudeMCPStatus{{Name: "plugin:fixture:identity", Source: "plugin", Status: "connected"}}
			case "missing-live-inventory":
				s.Plugins.Available = false
			case "missing-durable-inventory":
				s.Configuration.Recovery.Plugins.Available = false
			case "durable-candidate-hook":
				_ = json.Unmarshal([]byte(`[{"event":"SessionStart","plugin_name":"fixture@inline"}]`), &s.Configuration.Recovery.Plugins.Hooks.Items)
			case "offline-retired":
				s.Live = false
			}
			v := projectClaude(scope, s)
			if v.ExecutionClear != (mode == "retired" || mode == "offline-retired") {
				t.Fatalf("candidate residue granted input: %+v", v)
			}
		})
	}
}

func TestClaudeRecoveryRetainsOriginalPluginVersion(t *testing.T) {
	for _, mode := range []string{"original", "live-version", "live-path", "durable-version", "durable-path"} {
		t.Run(mode, func(t *testing.T) {
			var s claudeSnapshot
			if err := json.Unmarshal([]byte(`{"live":true,"config":{"id":"original"},"entries":[{"kind":"initialized"}],"mcp":{"available":true,"servers":[]},"plugins":{"available":true,"at":"2026-10-06T00:00:00Z","packages":[{"name":"fixture","version":"1","path":"/original","source":"fixture@inline"}],"skills":{"available":true,"commands":[],"descriptions":[]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[]}}}`), &s); err != nil {
				t.Fatal(err)
			}
			old := PluginSelection{AssetID: "pack", Name: "fixture", Version: "1", SourcePath: "/original", Mode: "enabled"}
			target := old
			target.Version, target.SourcePath = "2", "/candidate"
			scope := Scope{NativeID: "original", PluginSelections: []PluginSelection{old}}
			s.Configuration = &claudeConfigurationChange{Status: "recovered", PluginPrevious: scope.PluginSelections, PluginTarget: []PluginSelection{target}, Recovery: &claudeConfigurationRecovery{Operation: "recovery", Status: "verified", At: "2026-10-06T00:00:00Z", HistoryDigest: strings.Repeat("a", 64), Inventory: s.MCP, Plugins: s.Plugins}}
			s.Configuration.Recovery.Plugins.Packages = append([]claudePluginPackage{}, s.Plugins.Packages...)
			switch mode {
			case "live-version":
				s.Plugins.Packages[0].Version = "2"
			case "live-path":
				s.Plugins.Packages[0].Path = "/candidate"
			case "durable-version":
				s.Configuration.Recovery.Plugins.Packages[0].Version = "2"
			case "durable-path":
				s.Configuration.Recovery.Plugins.Packages[0].Path = "/candidate"
			}
			v := projectClaude(scope, s)
			if v.ExecutionClear != (mode == "original") {
				t.Fatalf("wrong version accepted: %+v", v)
			}
		})
	}
}
