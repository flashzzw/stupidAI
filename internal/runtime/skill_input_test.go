package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexSkillInputUsesBoundNativePathAlongsideText(t *testing.T) {
	c, f, scope, _, _ := mixedPolicyHarness(t)
	base := f.call
	starts := 0
	f.call = func(method string, params map[string]any) (any, error) {
		if method == "turn/start" {
			starts++
			b, _ := json.Marshal(params["input"])
			var inputs []map[string]any
			_ = json.Unmarshal(b, &inputs)
			if len(inputs) != 2 || inputs[0]["type"] != "text" || inputs[0]["text"] != "Use the method" || inputs[1]["type"] != "skill" || inputs[1]["name"] != "fixture-plugin:method" || inputs[1]["path"] != filepath.Join(scope.PluginSelections[0].CachePath, "skills/method/SKILL.md") {
				t.Errorf("Skill was not bound to selected version: %s", b)
			}
		}
		return base(method, params)
	}
	out, err := c.Execute(context.Background(), Intent{ID: "skill", Kind: "send", Scope: scope, Permission: "read_only", Text: "Use the method", SkillInputs: []SkillInput{{AssetID: "plugin", Name: "fixture-plugin:method"}}})
	if err != nil || out.NativeTurn != "sent" || starts != 1 {
		t.Fatalf("input failed: %+v %v", out, err)
	}
}

func TestCodexSkillInputRejectsUnavailableOrChangedBindings(t *testing.T) {
	for _, mode := range []string{"disabled", "unselected", "name", "duplicate", "limit", "wrong-kind", "wrong-backend", "cache-drift", "absolute-entry", "parent-entry", "missing-entry", "directory-entry", "duplicate-entry", "invalid-entry-hash"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope, _, _ := mixedPolicyHarness(t)
			calls := 0
			base := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "turn/start" {
					calls++
				}
				return base(method, params)
			}
			in := Intent{Kind: "send", ID: "reject", Scope: scope, Permission: "read_only", Text: "Must not send", SkillInputs: []SkillInput{{AssetID: "plugin", Name: "fixture-plugin:method"}}}
			pack := &in.Scope.PluginSelections[0]
			for n := range pack.Components {
				if pack.Components[n].Kind != "skill" {
					continue
				}
				switch mode {
				case "absolute-entry":
					pack.Components[n].Path = "/outside/SKILL.md"
				case "parent-entry":
					pack.Components[n].Path = "../outside/SKILL.md"
				case "missing-entry":
					pack.Components[n].Path = "missing/SKILL.md"
				}
			}
			for n := range pack.Files {
				if pack.Files[n].Path != "skills/method/SKILL.md" {
					continue
				}
				switch mode {
				case "directory-entry":
					pack.Files[n].Kind = "directory"
				case "duplicate-entry":
					pack.Files = append(pack.Files, pack.Files[n])
				case "invalid-entry-hash":
					pack.Files[n].SHA256 = "invalid"
				}
				break
			}
			switch mode {
			case "disabled":
				in.Scope.PluginSelections[0].Mode = "disabled"
			case "unselected":
				in.Scope.PluginSelections = nil
			case "name":
				in.SkillInputs[0].Name = "other:method"
			case "duplicate":
				in.SkillInputs = append(in.SkillInputs, in.SkillInputs[0])
			case "limit":
				for len(in.SkillInputs) < 17 {
					in.SkillInputs = append(in.SkillInputs, in.SkillInputs[0])
				}
			case "wrong-kind":
				in.Kind = "respond"
			case "wrong-backend":
				in.Scope.Instance.Kind = "claude"
			case "cache-drift":
				if err := os.WriteFile(filepath.Join(scope.PluginSelections[0].CachePath, "skills/method/SKILL.md"), []byte("Changed body"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Execute(context.Background(), in); err == nil || calls != 0 {
				t.Fatal("unverified native Skill input sent")
			}
		})
	}
}
