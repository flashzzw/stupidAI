package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCodexHookDeactivationCandidateIsOnlyARestriction(t *testing.T) {
	for _, scenario := range []string{"disable", "retire", "reduce-trust", "unchanged", "enable", "upgrade", "raise-trust", "other-plugin", "other-skill", "other-mcp", "identity", "new-plugin", "duplicate", "another-enabled-hook"} {
		t.Run(scenario, func(t *testing.T) {
			before := Scope{Instance: Instance{Kind: "codex"}, NativeID: "owned", PluginSelections: []PluginSelection{{AssetID: "hook", Mode: "enabled", Version: "v1", Hooks: &PluginHookSelection{Trust: "trusted"}}, {AssetID: "retained", Mode: "enabled"}}}
			data, _ := json.Marshal(before)
			var target Scope
			_ = json.Unmarshal(data, &target)
			target.PluginSelections[0].Mode = "disabled"
			switch scenario {
			case "retire":
				target.PluginSelections = target.PluginSelections[1:]
			case "reduce-trust":
				target.PluginSelections[0].Hooks.Trust = "untrusted"
			case "unchanged":
				target.PluginSelections[0].Mode = "enabled"
			case "enable":
				before.PluginSelections[0].Mode = "disabled"
				target.PluginSelections[0].Mode = "enabled"
			case "upgrade":
				target.PluginSelections[0].Version = "v2"
			case "raise-trust":
				before.PluginSelections[0].Hooks.Trust = "untrusted"
			case "other-plugin":
				target.PluginSelections[1].Mode = "disabled"
			case "other-skill":
				target.SkillSelections = []SkillSelection{{AssetID: "foreign"}}
			case "other-mcp":
				target.MCPSelections = []MCPSelection{{AssetID: "foreign"}}
			case "identity":
				target.NativeID = "foreign"
			case "new-plugin":
				target.PluginSelections = append(target.PluginSelections, PluginSelection{AssetID: "new"})
			case "duplicate":
				target.PluginSelections = append(target.PluginSelections, target.PluginSelections[0])
			case "another-enabled-hook":
				before.PluginSelections[1].Hooks = &PluginHookSelection{Trust: "trusted"}
				target.PluginSelections[1].Hooks = &PluginHookSelection{Trust: "trusted"}
			}
			want := slices.Contains([]string{"disable", "retire", "reduce-trust"}, scenario)
			if CodexHookDeactivation(before, target) != want {
				t.Fatal("wrong deactivation classification", scenario)
			}
		})
	}
}

func TestCodexHookDeactivationPreparationStillRequiresOwnedIdleConfiguration(t *testing.T) {
	for _, scenario := range []string{"valid", "retire", "no-witness", "other-connection", "config-drift", "cache-drift", "active-goal", "active-thread", "pending-approval", "running-hook", "unknown-hook", "partial-history", "no-target", "wrong-target", "policy-missing", "input-cannot-use-target", "json-cannot-grant-removal"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			scope := codexPluginScope(t)
			scope.Cwd = t.TempDir()
			scope.StateNamespace = scope.Instance.Profile
			pkg := hookInspectionPackage(t)
			binary, _ := hookInspectionBinary(t, "valid")
			scope.Instance.ServerExecutable = binary
			inspection, err := c.InspectPluginHooks(context.Background(), scope.Instance, pkg)
			if err != nil {
				t.Fatal(err)
			}
			pack := PluginSelection{AssetID: pkg.AssetID, Name: pkg.Name, Version: pkg.Version, SourcePath: pkg.SourcePath, SourceHash: pkg.SourceHash, RootMode: pkg.RootMode, Files: pkg.Files, Mode: "enabled", Components: []PluginComponent{{Kind: "hook", Name: "SessionStart", NativeName: "sessionStart", Path: "hooks/hooks.json", Count: 1}}, Hooks: &PluginHookSelection{Trust: "trusted", Inspection: inspection, Expected: pkg.Expected}}
			pack.NativeSource, pack.CachePath, err = CodexPluginLocation(scope, pack)
			if err != nil {
				t.Fatal(err)
			}
			scope.PluginSelections = []PluginSelection{pack}
			rollout := filepath.Join(scope.StateNamespace, "sessions", "owned.jsonl")
			managed, checking := false, false
			window := 1
			f.call = func(method string, params map[string]any) (any, error) {
				status := "idle"
				if checking && scenario == "active-thread" {
					status = "active"
				}
				thread := map[string]any{"id": "owned", "model": "fixture", "modelProvider": "fixture", "cwd": scope.Cwd, "path": rollout, "status": map[string]any{"type": status}, "turns": []any{}}
				switch method {
				case "configRequirements/read":
					if checking && scenario == "policy-missing" {
						return map[string]any{}, nil
					}
					return map[string]any{"requirements": map[string]any{"allowManagedHooksOnly": managed}}, nil
				case "config/read":
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true, "hooks": true}, "model_context_window": window}}, nil
				case "thread/start":
					return map[string]any{"thread": thread, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request", "disabledPluginIds": []string{}}, nil
				case "thread/read":
					if checking && scenario == "partial-history" && params["includeTurns"] == true {
						thread["turns"] = nil
					}
					return map[string]any{"thread": thread}, nil
				case "thread/turns/list", "mcpServerStatus/list":
					return map[string]any{"data": []any{}}, nil
				case "thread/goal/get":
					if checking && scenario == "active-goal" {
						return map[string]any{"goal": map[string]string{"status": "active"}}, nil
					}
				}
				return emptyControl(method)
			}
			if _, err = c.Execute(context.Background(), Intent{Kind: "create", Scope: scope, Permission: "read_only"}); err != nil {
				t.Fatal(err)
			}
			scope.NativeID = "owned"
			if err = os.MkdirAll(filepath.Dir(rollout), 0700); err != nil {
				t.Fatal(err)
			}
			writeMixedPolicy(t, rollout, scope, []string{})
			managed = true
			// A normal observation must refuse current use without erasing the
			// same connection's historical configuration establishment.
			view, err := c.Observe(context.Background(), scope)
			if err != nil || view.ExecutionClear {
				t.Fatal("managed restriction did not withdraw input readiness", err)
			}
			data, _ := json.Marshal(scope)
			var target Scope
			_ = json.Unmarshal(data, &target)
			target.PluginSelections[0].Mode = "disabled"
			in := Intent{Kind: "prepare_execution", Scope: scope, Permission: "read_only", ConfigurationTarget: &target}
			key := scope.Instance.ID + "/" + scope.NativeID
			switch scenario {
			case "retire":
				target.PluginSelections = nil
			case "no-witness":
				delete(c.hookWitnesses, key)
			case "other-connection":
				c.clients[scope.Instance.ID] = &permissionConnection{nativeFixture: f, identity: "replacement"}
			case "config-drift":
				window++
			case "cache-drift":
				if err = os.WriteFile(filepath.Join(pack.CachePath, "hooks/hooks.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "pending-approval":
				c.pending[key+"/request"] = Pending{ID: key + "/request", NativeID: scope.NativeID, Status: "pending", ResponseKind: "approval"}
			case "running-hook", "unknown-hook":
				status := "running"
				if scenario == "unknown-hook" {
					status = "unknown"
				}
				c.hooks[key] = map[string]HookExecution{"hook": {ID: "hook", Status: status}}
			case "no-target":
				in.ConfigurationTarget = nil
			case "wrong-target":
				target.Route.Model = "foreign"
			case "input-cannot-use-target":
				in.Kind = "send"
				in.Text = "must not send"
			case "json-cannot-grant-removal":
				var forged Scope
				_ = json.Unmarshal([]byte(`{"hookDeactivation":true,"hook_deactivation":true}`), &forged)
				if forged.hookDeactivation {
					t.Fatal("JSON granted internal removal state")
				}
				in.ConfigurationTarget = nil
			}
			checking = true
			f.methods = nil
			r, err := c.Execute(context.Background(), in)
			want := scenario == "valid" || scenario == "retire"
			if (err == nil && r.ConfigurationBaseline != "") != want {
				t.Fatalf("preparation %s: %+v %v", scenario, r, err)
			}
			for _, method := range f.methods {
				if slices.Contains([]string{"thread/start", "thread/resume", "thread/unsubscribe", "thread/settings/update", "turn/start"}, method) {
					t.Fatal("preparation wrote native state", method)
				}
			}
			if slices.Contains([]string{"no-witness", "other-connection", "config-drift", "cache-drift", "active-goal", "active-thread", "pending-approval", "running-hook", "unknown-hook", "partial-history", "wrong-target", "policy-missing"}, scenario) {
				f.methods = nil
				_, err = c.Execute(context.Background(), Intent{Kind: "apply_execution", Scope: target, PreviousScope: &scope, Permission: "read_only", ConfigurationBaseline: "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"})
				if err == nil {
					t.Fatal("unsafe deactivation applied", scenario)
				}
				for _, method := range f.methods {
					if slices.Contains([]string{"thread/start", "thread/resume", "thread/unsubscribe", "thread/settings/update", "turn/start"}, method) {
						t.Fatal("unsafe apply wrote native state", scenario, method)
					}
				}
			}
		})
	}
}
