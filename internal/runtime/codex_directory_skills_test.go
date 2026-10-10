package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexDirectorySkillSelectionRequiresOriginalSourceAndConnection(t *testing.T) {
	for _, scenario := range []string{"enabled", "disabled", "candidate-read", "unapplied-inherit", "missing-witness", "config-drift", "source-drift", "root-mode-drift", "missing-source", "wrong-name", "admin", "plugin", "alias", "duplicate-name", "incomplete", "inherited-alias"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			s := sharedSkillScope(t, "directory")
			source, err := skillAssetSource(s, s.PluginSelections[0])
			if err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(source.SourcePath, "SKILL.md")
			selected := SkillSelection{AssetID: "method", Name: "method", NativeKey: entry, Mode: "enabled", Source: "codex_directory", SourcePath: entry, DiscoveryPath: entry, Delivery: "native_directory", ResourceRoot: source.SourcePath, ResourceKind: "directory", ResourceHash: source.SourceHash, CodexDirectory: &CodexDirectorySkill{RootMode: source.RootMode, Files: source.Files}}
			if scenario == "disabled" {
				selected.Mode = "disabled"
			}
			s.SkillSelections, s.PluginSelections = []SkillSelection{selected}, nil
			retained := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.WriteFile(retained, []byte("owned retained"), 0600); err != nil {
				t.Fatal(err)
			}
			item := map[string]any{"name": "method", "description": "owned", "path": entry, "scope": "user", "enabled": true}
			items := []any{item}
			errors := []any{}
			configEntries := []any{map[string]any{"path": retained, "enabled": false}}
			switch scenario {
			case "missing-source":
				items = []any{}
			case "wrong-name":
				item["name"] = "changed"
			case "admin":
				item["scope"] = "admin"
			case "plugin":
				item["pluginId"] = "other@source"
			case "alias", "inherited-alias":
				alias := filepath.Join(t.TempDir(), "SKILL.md")
				if err := os.Symlink(entry, alias); err != nil {
					t.Fatal(err)
				}
				if scenario == "alias" {
					items = append(items, map[string]any{"name": "alias", "path": alias, "scope": "repo", "enabled": true})
				} else {
					configEntries = append(configEntries, map[string]any{"path": alias, "enabled": true})
				}
			case "duplicate-name":
				items = append(items, map[string]any{"name": "method", "path": retained, "scope": "repo", "enabled": true})
			case "incomplete":
				errors = append(errors, map[string]any{"path": retained})
			}
			rollout := filepath.Join(s.StateNamespace, "sessions", "skill.jsonl")
			starts, sends := 0, 0
			window := 1
			f.call = func(method string, params map[string]any) (any, error) {
				thread := map[string]any{"id": "created", "model": "fixture", "modelProvider": "fixture", "cwd": s.Cwd, "path": rollout, "status": map[string]any{"type": "idle"}, "turns": []any{}}
				switch method {
				case "skills/list":
					return map[string]any{"data": []any{map[string]any{"cwd": s.Cwd, "skills": items, "errors": errors}}}, nil
				case "config/read":
					return map[string]any{"config": map[string]any{"skills": map[string]any{"config": configEntries}, "model_context_window": window}}, nil
				case "thread/start", "thread/resume":
					if method == "thread/start" {
						starts++
						b, _ := json.Marshal(params["config"])
						var config map[string]json.RawMessage
						_ = json.Unmarshal(b, &config)
						var rows []struct {
							Path    string `json:"path"`
							Enabled bool   `json:"enabled"`
						}
						_ = json.Unmarshal(config["skills.config"], &rows)
						if len(rows) != 2 || rows[0].Path != retained || rows[0].Enabled || rows[1].Path != entry || rows[1].Enabled != (scenario != "disabled") {
							t.Fatal("selection dropped inherited config or lost exact entry")
						}
					}
					return map[string]any{"thread": thread, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request", "disabledPluginIds": []string{}}, nil
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
			admitted := scenario == "enabled" || scenario == "disabled" || scenario == "candidate-read" || scenario == "unapplied-inherit" || scenario == "missing-witness" || scenario == "config-drift" || scenario == "source-drift" || scenario == "root-mode-drift"
			if !admitted {
				if err == nil || starts != 0 {
					t.Fatalf("unverified source reached native create: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s.NativeID = "created"
			if err = os.MkdirAll(filepath.Dir(rollout), 0700); err != nil {
				t.Fatal(err)
			}
			writeMixedPolicy(t, rollout, s, []string{})
			switch scenario {
			case "missing-witness":
				c.directorySkillWitnesses = nil
			case "config-drift":
				window++
			case "source-drift":
				if err = os.WriteFile(entry, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "root-mode-drift":
				if err = os.Chmod(source.SourcePath, 0750); err != nil {
					t.Fatal(err)
				}
			case "unapplied-inherit":
				target := s
				target.SkillSelections = nil
				baseline, e := configurationBaseline(context.Background(), f, s.NativeID)
				if e != nil {
					t.Fatal(e)
				}
				result, e := c.Reconcile(context.Background(), Intent{Kind: "apply_execution", Scope: target, PreviousScope: &s, ConfigurationBaseline: baseline, Permission: "read_only"}, Receipt{NativeID: s.NativeID})
				if e != nil || result.State != "unknown" || starts != 1 || sends != 0 {
					t.Fatalf("empty candidate inferred inheritance without an applied configuration: %v %+v", e, result)
				}
			case "candidate-read":
				b, _ := json.Marshal(s)
				var candidate Scope
				_ = json.Unmarshal(b, &candidate)
				candidate.SkillSelections[0].Mode = "disabled"
				v, e := c.Observe(context.Background(), candidate)
				if e != nil || v.ExecutionClear {
					t.Fatal("candidate became current")
				}
			}
			ready := scenario == "enabled" || scenario == "disabled" || scenario == "candidate-read" || scenario == "unapplied-inherit"
			view, err := c.Observe(context.Background(), s)
			if err != nil || view.ExecutionClear != ready || len(view.SkillEvidence) != 1 || view.SkillEvidence[0].Verified != ready {
				t.Fatalf("wrong readiness: %v %+v", err, view.SkillEvidence)
			}
			_, err = c.Execute(context.Background(), Intent{ID: "input", Kind: "send", Scope: s, Permission: "read_only", Text: "owned"})
			if (err == nil) != ready || sends != map[bool]int{true: 1, false: 0}[ready] {
				t.Fatalf("input without current source/config: %v sends=%d", err, sends)
			}
			if !ready {
				receipt, e := c.Execute(context.Background(), Intent{ID: "prepare", Kind: "prepare_execution", Scope: s, Permission: "read_only"})
				canRetire := scenario == "source-drift" || scenario == "root-mode-drift"
				if (e == nil && receipt.ConfigurationBaseline != "") != canRetire || sends != 0 {
					t.Fatalf("read-only retirement confused changed files with unknown configuration: %v", e)
				}
			}
		})
	}
}
