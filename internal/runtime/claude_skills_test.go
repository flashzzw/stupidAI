package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeSkillInventoryKeepsDiscoverySeparateFromMethodUse(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "missing-inventory", "missing-arrays", "malformed-entry", "command-only", "description-only", "duplicate", "wrong-source", "builtin"} {
		t.Run(mode, func(t *testing.T) {
			scope := Scope{NativeID: "original", SkillSelections: []SkillSelection{{AssetID: "shared", Name: "display", NativeKey: "key", Mode: "enabled", Source: "userSettings"}}}
			body := `{"available":true,"at":"2026-10-02T00:00:00Z","commands":[{"name":"display","builtin":false}],"descriptions":[{"name":"display","source":"userSettings"}]}`
			var inventory claudeSkillInventory
			if err := json.Unmarshal([]byte(body), &inventory); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled":
				scope.SkillSelections[0].Mode = "disabled"
				inventory.Commands = inventory.Commands[:0]
				inventory.Descriptions = inventory.Descriptions[:0]
			case "missing-inventory":
				inventory.Available = false
			case "missing-arrays":
				scope.SkillSelections[0].Mode = "disabled"
				inventory.Commands = nil
				inventory.Descriptions = nil
			case "command-only":
				inventory.Descriptions = inventory.Descriptions[:0]
			case "malformed-entry":
				scope.SkillSelections[0].Mode = "disabled"
				inventory.Commands[0].Name = ""
				inventory.Descriptions = inventory.Descriptions[:0]
			case "description-only":
				inventory.Commands = inventory.Commands[:0]
			case "duplicate":
				inventory.Commands = append(inventory.Commands, inventory.Commands[0])
			case "wrong-source":
				inventory.Descriptions[0].Source = "plugin"
			case "builtin":
				inventory.Commands[0].Builtin = true
			}
			v := projectClaude(scope, claudeSnapshot{Live: true, Entries: []claudeEntry{{Kind: "initialized"}}, Skills: inventory})
			want := mode == "enabled" || mode == "disabled"
			if len(v.SkillEvidence) != 1 || v.SkillEvidence[0].Verified != want || v.ExecutionClear != want {
				t.Fatalf("wrong admission: %+v", v)
			}
			encoded, _ := json.Marshal(v.SkillEvidence[0])
			var item map[string]any
			_ = json.Unmarshal(encoded, &item)
			for _, key := range []string{"loaded", "used", "gate_passed"} {
				if _, ok := item[key]; ok {
					t.Fatal("discovery promoted to method evidence")
				}
			}
		})
	}
}

func TestClaudeSkillResourceInventoryRequiresOriginAndUniqueAliases(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "wrong-plugin", "missing-plugin", "alias", "extra-command", "extra-description", "disabled-extra", "disabled-alias"} {
		t.Run(mode, func(t *testing.T) {
			s := SkillSelection{AssetID: "resource", Name: "package:display", NativeKey: "package:method", Mode: "enabled", Source: "plugin", Delivery: "local_plugin", PluginName: "package"}
			var inventory claudeSkillInventory
			if err := json.Unmarshal([]byte(`{"available":true,"commands":[{"name":"package:display","aliases":["display","package:method"]}],"descriptions":[{"name":"package:display","source":"plugin","plugin_name":"package"}]}`), &inventory); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled":
				s.Mode = "disabled"
				inventory.Commands = inventory.Commands[:0]
				inventory.Descriptions = inventory.Descriptions[:0]
			case "wrong-plugin":
				inventory.Descriptions[0].PluginName = "other"
			case "missing-plugin":
				inventory.Descriptions[0].PluginName = ""
			case "alias":
				other := inventory.Commands[0]
				other.Name = "inherited"
				other.Aliases = []string{"display"}
				inventory.Commands = append(inventory.Commands, other)
			case "extra-command":
				other := inventory.Commands[0]
				other.Name = "package:extra"
				other.Aliases = nil
				inventory.Commands = append(inventory.Commands, other)
			case "extra-description":
				other := inventory.Descriptions[0]
				other.Name = "extra"
				inventory.Descriptions = append(inventory.Descriptions, other)
			case "disabled-alias":
				s.Mode = "disabled"
				inventory.Commands[0].Name = "other"
				inventory.Descriptions = inventory.Descriptions[:0]
			case "disabled-extra":
				s.Mode = "disabled"
				inventory.Commands[0].Name = "package:extra"
				inventory.Descriptions = inventory.Descriptions[:0]
			}
			v := projectClaude(Scope{NativeID: "original", SkillSelections: []SkillSelection{s}}, claudeSnapshot{Live: true, Entries: []claudeEntry{{Kind: "initialized"}}, Skills: inventory})
			want := mode == "enabled" || mode == "disabled"
			if v.ExecutionClear != want || v.SkillEvidence[0].Verified != want {
				t.Fatalf("unverified resource admitted: %+v", v)
			}
		})
	}
}
