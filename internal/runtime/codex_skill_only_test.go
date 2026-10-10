package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodexSkillOnlyPluginRequiresCachePolicyAndCompleteInventory(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled-policy", "cache-drift", "unexpected-mcp", "unloaded", "missing-inventory"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			s := codexMixedScope(t)
			pack := &s.PluginSelections[0]
			if err := os.Remove(filepath.Join(pack.SourcePath, ".mcp.json")); err != nil {
				t.Fatal(err)
			}
			pack.Files = slices.DeleteFunc(pack.Files, func(file PluginFile) bool { return file.Path == ".mcp.json" })
			pack.Components = slices.DeleteFunc(pack.Components, func(component PluginComponent) bool { return component.Kind == "mcp" })
			c, f, s, path, policy := pluginPolicyHarnessWithScope(t, c, f, s)
			base := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "mcpServerStatus/list" {
					if mode == "missing-inventory" {
						return nil, errors.New("inventory unavailable")
					}
					if mode == "unexpected-mcp" {
						return map[string]any{"data": []any{map[string]any{"name": "undeclared", "pluginId": s.PluginSelections[0].NativeSource, "runtimeStatus": "connected", "tools": map[string]any{}}}}, nil
					}
				}
				out, err := base(method, params)
				if method == "thread/read" && mode == "unloaded" {
					out.(map[string]any)["thread"].(map[string]any)["status"] = map[string]any{"type": "notLoaded"}
				}
				return out, err
			}
			if mode == "disabled-policy" {
				*policy = append(*policy, pack.NativeSource)
				writeMixedPolicy(t, path, s, *policy)
			}
			if mode == "cache-drift" {
				if err := os.WriteFile(filepath.Join(pack.CachePath, "skills/method/SKILL.md"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s.NoAutoResume = true
			view, err := c.Observe(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			want := mode == "enabled"
			if len(view.PluginEvidence) != 1 || view.PluginEvidence[0].Verified != want || view.ExecutionClear != want {
				t.Fatalf("Skill-only selection readiness: %+v", view)
			}
			if want {
				if len(view.CapabilityEvidence) != 0 || strings.Contains(view.PluginEvidence[0].Detail, "MCP") {
					t.Fatal("invented MCP evidence for Skill-only package")
				}
				s.NoAutoResume = false
				receipt, err := c.Execute(context.Background(), Intent{ID: "skill-only", Kind: "send", Scope: s, Permission: "read_only", Text: "Use method", SkillInputs: []SkillInput{{AssetID: pack.AssetID, Name: pack.Name + ":method"}}})
				if err != nil || receipt.NativeTurn != "sent" {
					t.Fatal("Skill-only input rejected", err)
				}
			}
		})
	}
}
