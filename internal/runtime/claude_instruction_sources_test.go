package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeSkillCatalogInstructionMetadata(t *testing.T) {
	for _, test := range []struct {
		name, sample string
		available    bool
		count        int
	}{
		{"native", `{"available":true,"items":[{"path":"/parent/CLAUDE.md","source":"Project","tokens":18,"content":"PRIVATE_BODY"},{"path":"/profile/CLAUDE.md","source":"FutureSource","tokens":0}]}`, true, 2},
		{"empty", `{"available":true,"items":[]}`, true, 0},
		{"missing-list", `{"available":true}`, false, 0},
		{"unavailable", `{"available":false,"items":[{"path":"/x","source":"User","tokens":1}]}`, false, 0},
		{"relative-path", `{"available":true,"items":[{"path":"CLAUDE.md","source":"User","tokens":1}]}`, false, 0},
		{"missing-tokens", `{"available":true,"items":[{"path":"/x","source":"User"}]}`, false, 0},
		{"negative-tokens", `{"available":true,"items":[{"path":"/x","source":"User","tokens":-1}]}`, false, 0},
		{"unsafe-tokens", `{"available":true,"items":[{"path":"/x","source":"User","tokens":9007199254740992}]}`, false, 0},
		{"duplicate", `{"available":true,"items":[{"path":"/x","source":"User","tokens":1},{"path":"/x","source":"User","tokens":2}]}`, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var inventory claudeSkillInventory
			if err := json.Unmarshal([]byte(`{"available":true,"commands":[],"descriptions":[],"instructions":`+test.sample+`}`), &inventory); err != nil {
				t.Fatal(err)
			}
			out, err := claudeSkillCatalog(Scope{NativeID: "original", Cwd: "/project"}, inventory)
			if err != nil || out.Instructions == nil || out.Instructions.Available != test.available || len(out.Instructions.Items) != test.count || out.Complete {
				t.Fatalf("instruction projection: %+v %v", out, err)
			}
			if test.name == "native" && (out.Instructions.Items[0].Path != "/parent/CLAUDE.md" || out.Instructions.Items[1].Source != "FutureSource" || out.Instructions.Items[1].Tokens != 0) {
				t.Fatal("native source metadata changed")
			}
		})
	}
	out, err := claudeSkillCatalog(Scope{}, claudeSkillInventory{Available: true, Commands: []claudeSkillCommand{}, Descriptions: []claudeSkillDescription{}})
	if err != nil || out.Instructions != nil {
		t.Fatal("old bridge invented an empty instruction report")
	}
}
