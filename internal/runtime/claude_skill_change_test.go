package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeReleasedSkillRemainsCheckedAfterConfiguration(t *testing.T) {
	for _, mode := range []string{"released", "name-returned", "alias-returned", "description-returned", "inventory-missing", "uncommitted"} {
		t.Run(mode, func(t *testing.T) {
			var snapshot claudeSnapshot
			err := json.Unmarshal([]byte(`{"live":true,"config":{"id":"original"},"entries":[{"kind":"initialized"}],"mcp":{"available":true,"servers":[]},"skills":{"available":true,"commands":[],"descriptions":[]},"configuration":{"status":"verified","skill_target":[],"skill_previous":[{"asset_id":"old","name":"package:method","native_key":"package:method","source":"plugin","delivery":"local_plugin","plugin_name":"package","mode":"enabled"}]}}`), &snapshot)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "name-returned":
				_ = json.Unmarshal([]byte(`{"available":true,"commands":[{"name":"package:method","aliases":[]}],"descriptions":[]}`), &snapshot.Skills)
			case "alias-returned":
				_ = json.Unmarshal([]byte(`{"available":true,"commands":[{"name":"other","aliases":["package:method"]}],"descriptions":[]}`), &snapshot.Skills)
			case "description-returned":
				_ = json.Unmarshal([]byte(`{"available":true,"commands":[],"descriptions":[{"name":"other","source":"plugin","plugin_name":"package"}]}`), &snapshot.Skills)
			case "inventory-missing":
				snapshot.Skills.Available = false
			case "uncommitted":
				snapshot.Configuration.Status = "applying"
			}
			view := projectClaude(Scope{NativeID: "original"}, snapshot)
			if view.ExecutionClear != (mode == "released") {
				t.Fatalf("wrong released Skill admission: %+v", view)
			}
		})
	}
}
