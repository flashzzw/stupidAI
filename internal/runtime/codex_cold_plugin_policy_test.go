package runtime

import (
	"context"
	"fmt"
	"testing"
)

func TestCodexColdPluginPolicyDispatchBoundary(t *testing.T) {
	for _, mode := range []string{"ready", "already-loaded", "changed-history", "lost-reply", "wrong-id", "wrong-permission", "missing-policy", "changed-policy", "missing-inventory", "null-inventory", "controlled-plugin-present"} {
		t.Run(mode, func(t *testing.T) {
			c, f, original, _, ids := mixedPolicyHarness(t)
			original.RecoveryPermission = "read_only"
			target := original
			target.PluginSelections = append([]PluginSelection(nil), original.PluginSelections...)
			target.PluginSelections[0].Mode = "disabled"
			baseline, err := configurationBaseline(context.Background(), f, original.NativeID)
			if err != nil {
				t.Fatal(err)
			}
			base := f.call
			resumes := 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "thread/read":
					value, err := base(method, params)
					thread := value.(map[string]any)["thread"].(map[string]any)
					status := "notLoaded"
					if mode == "already-loaded" {
						status = "idle"
					}
					thread["status"] = map[string]any{"type": status}
					if mode == "changed-history" {
						thread["turns"] = []any{map[string]any{"id": "external", "status": "completed", "items": []any{}}}
					}
					return value, err
				case "thread/loaded/list":
					return map[string]any{"data": []string{}}, nil
				case "thread/resume":
					resumes++
					if params["config"].(map[string]any)["plugins."+original.PluginSelections[0].NativeSource+".enabled"] != false {
						t.Fatal("controlled plugin not excluded")
					}
					if mode == "lost-reply" {
						return nil, fmt.Errorf("owned lost resume reply")
					}
					value, err := base(method, params)
					out := value.(map[string]any)
					switch mode {
					case "wrong-id":
						out["thread"].(map[string]any)["id"] = "another"
					case "wrong-permission":
						out["sandbox"] = map[string]any{"type": "dangerFullAccess"}
					case "missing-policy":
						delete(out, "disabledPluginIds")
					case "changed-policy":
						out["disabledPluginIds"] = []string{"foreign@owned"}
					}
					return value, err
				case "mcpServerStatus/list":
					if mode == "null-inventory" {
						return map[string]any{"data": nil}, nil
					}
					if mode == "missing-inventory" {
						return map[string]any{}, nil
					}
					if mode == "controlled-plugin-present" {
						return base(method, params)
					}
					return map[string]any{"data": []any{}}, nil
				}
				return base(method, params)
			}
			in := Intent{Kind: "apply_execution", Scope: target, PreviousScope: &original, ConfigurationBaseline: baseline, Permission: "read_only"}
			sent, err := c.loadColdPluginPolicyBoundary(context.Background(), f, in, original, *ids)
			wantSent := mode != "already-loaded" && mode != "changed-history"
			if sent != wantSent || resumes != map[bool]int{true: 1, false: 0}[wantSent] || (err == nil) != (mode == "ready") {
				t.Fatalf("cold boundary: sent=%t resumes=%d error=%v", sent, resumes, err)
			}
		})
	}
}
