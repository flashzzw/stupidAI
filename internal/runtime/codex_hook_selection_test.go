package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexHookSelectionEstablishesOnlyOwnVerifiedConfiguration(t *testing.T) {
	for _, scenario := range []string{"trusted", "untrusted", "candidate-read", "missing-witness", "config-drift", "numeric-config-drift", "cache-drift", "changed-definition", "inherited-own-key", "feature-disabled", "managed-only", "managed-untrusted", "managed-disabled", "requirements-null", "requirements-missing", "requirements-invalid", "requirements-field-missing", "requirements-error", "requirements-drift", "requirements-number-drift", "requirements-during-create"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			s := codexPluginScope(t)
			s.Cwd = t.TempDir()
			s.StateNamespace = s.Instance.Profile
			pkg := hookInspectionPackage(t)
			binary, _ := hookInspectionBinary(t, "valid")
			s.Instance.ServerExecutable = binary
			inspection, err := c.InspectPluginHooks(context.Background(), s.Instance, pkg)
			if err != nil {
				t.Fatal(err)
			}
			pack := PluginSelection{AssetID: pkg.AssetID, Name: pkg.Name, Version: pkg.Version, SourcePath: pkg.SourcePath, SourceHash: pkg.SourceHash, RootMode: pkg.RootMode, Files: pkg.Files, Mode: "enabled", Components: []PluginComponent{{Kind: "hook", Name: "SessionStart", NativeName: "sessionStart", Path: "hooks/hooks.json", Count: 1}}, Hooks: &PluginHookSelection{Trust: "trusted", Inspection: inspection, Expected: pkg.Expected}}
			if scenario == "untrusted" || scenario == "managed-untrusted" {
				pack.Hooks.Trust = "untrusted"
			}
			if scenario == "managed-disabled" {
				pack.Mode = "disabled"
			}
			pack.NativeSource, pack.CachePath, err = CodexPluginLocation(s, pack)
			if err != nil {
				t.Fatal(err)
			}
			s.PluginSelections = []PluginSelection{pack}
			key := pack.NativeSource + strings.TrimPrefix(inspection.Definitions[0].Key, inspection.NativeSource)
			state := map[string]any{"retained@owned:hook": map[string]any{"enabled": false, "trusted_hash": "sha256:" + strings.Repeat("c", 64)}}
			features := map[string]bool{"plugins": true, "hooks": scenario != "feature-disabled"}
			window := json.Number("9007199254740992")
			if scenario == "inherited-own-key" {
				state[key] = map[string]any{"enabled": true}
			}
			if scenario == "changed-definition" {
				s.PluginSelections[0].Hooks.Inspection.Definitions[0].DefinitionHash = "sha256:" + strings.Repeat("d", 64)
			}
			path := filepath.Join(s.StateNamespace, "sessions", "hook.jsonl")
			starts, sends := 0, 0
			requirements := map[string]any{"allowManagedHooksOnly": strings.HasPrefix(scenario, "managed-"), "policy_version": json.Number("9007199254740992")}
			disabledIDs := []string{}
			if pack.Mode == "disabled" {
				disabledIDs = append(disabledIDs, pack.NativeSource)
			}
			f.call = func(method string, params map[string]any) (any, error) {
				thread := map[string]any{"id": "created", "model": "fixture", "modelProvider": "fixture", "cwd": s.Cwd, "path": path, "status": map[string]any{"type": "idle"}, "turns": []any{}}
				switch method {
				case "configRequirements/read":
					switch scenario {
					case "requirements-null":
						return map[string]any{"requirements": nil}, nil
					case "requirements-missing":
						return map[string]any{}, nil
					case "requirements-invalid":
						return map[string]any{"requirements": map[string]any{"allowManagedHooksOnly": "false"}}, nil
					case "requirements-field-missing":
						return map[string]any{"requirements": map[string]any{}}, nil
					case "requirements-error":
						return nil, fmt.Errorf("PRIVATE_POLICY_BODY")
					}
					return map[string]any{"requirements": requirements}, nil
				case "config/read":
					return map[string]any{"config": map[string]any{"features": features, "hooks": map[string]any{"state": state}, "model_context_window": window}}, nil
				case "thread/start", "thread/resume":
					if method == "thread/start" {
						starts++
						body, _ := json.Marshal(params["config"])
						var overrides map[string]json.RawMessage
						_ = json.Unmarshal(body, &overrides)
						var got map[string]map[string]any
						_ = json.Unmarshal(overrides["hooks.state"], &got)
						if got["retained@owned:hook"]["enabled"] != false || got[key]["enabled"] != true || (got[key]["trusted_hash"] != nil) != (pack.Hooks.Trust == "trusted") {
							t.Fatal("hook override dropped inheritance or confused trust")
						}
						if scenario == "requirements-during-create" {
							requirements["allowManagedHooksOnly"] = true
						}
					}
					return map[string]any{"thread": thread, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request", "disabledPluginIds": disabledIDs}, nil
				case "thread/read":
					return map[string]any{"thread": thread}, nil
				case "thread/turns/list", "mcpServerStatus/list":
					return map[string]any{"data": []any{}}, nil
				case "turn/start":
					sends++
					return map[string]any{"turn": map[string]any{"id": "sent"}}, nil
				default:
					return emptyControl(method)
				}
			}
			_, err = c.Execute(context.Background(), Intent{Kind: "create", Scope: s, Permission: "read_only"})
			if scenario == "requirements-during-create" {
				if err == nil || starts != 1 || len(c.hookWitnesses) != 0 {
					t.Fatal("changed native requirements established a hook witness", err)
				}
				return
			}
			rejected := scenario == "changed-definition" || scenario == "inherited-own-key" || scenario == "feature-disabled" || scenario == "managed-only" || scenario == "managed-untrusted" || scenario == "requirements-missing" || scenario == "requirements-invalid" || scenario == "requirements-field-missing" || scenario == "requirements-error"
			if rejected {
				if err == nil || starts != 0 {
					t.Fatalf("unverified definitions/config reached native create: %v", err)
				}
				if strings.Contains(err.Error(), "PRIVATE_POLICY_BODY") {
					t.Fatal("native policy error leaked source body")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s.NativeID = "created"
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			writeMixedPolicy(t, path, s, disabledIDs)
			switch scenario {
			case "candidate-read":
				body, _ := json.Marshal(s)
				var candidate Scope
				_ = json.Unmarshal(body, &candidate)
				candidate.PluginSelections[0].Hooks.Trust = "untrusted"
				view, e := c.Observe(context.Background(), candidate)
				if e != nil || view.ExecutionClear {
					t.Fatal("unapplied candidate was treated as current")
				}
			case "missing-witness":
				c.hookWitnesses = nil
			case "config-drift":
				state["new@source:key"] = map[string]any{"enabled": true}
			case "numeric-config-drift":
				window = json.Number("9007199254740993")
			case "requirements-drift":
				requirements["allowManagedHooksOnly"] = true
			case "requirements-number-drift":
				requirements["policy_version"] = json.Number("9007199254740993")
			case "cache-drift":
				if err := os.WriteFile(filepath.Join(pack.CachePath, "hooks/hooks.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			view, err := c.Observe(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			ready := scenario == "trusted" || scenario == "untrusted" || scenario == "candidate-read" || scenario == "managed-disabled" || scenario == "requirements-null"
			if view.ExecutionClear != ready || len(view.PluginEvidence) != 1 || view.PluginEvidence[0].Verified != ready {
				t.Fatalf("wrong hook readiness: %+v", view)
			}
			if scenario == "requirements-drift" {
				for n := 0; n < 2; n++ {
					if !strings.Contains(view.PluginEvidence[0].Detail, "仅允许受管 hook") {
						t.Fatal("managed restriction was replaced by a generic missing-witness error", view.PluginEvidence[0].Detail)
					}
					view, err = c.Observe(context.Background(), s)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err = c.Execute(context.Background(), Intent{ID: "input", Kind: "send", Scope: s, Permission: "read_only", Text: "owned"})
			if (err == nil) != ready || sends != map[bool]int{true: 1, false: 0}[ready] {
				t.Fatalf("input admitted without current hook witness: %v sends=%d", err, sends)
			}
		})
	}
}
