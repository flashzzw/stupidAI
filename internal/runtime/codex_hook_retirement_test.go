package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodexHookRetirementCannotBeInferredFromDisabledPolicy(t *testing.T) {
	for _, scenario := range []string{"missing-witness", "acknowledged", "old-selection", "other-connection", "config-drift", "config-read-error", "not-loaded", "pending-approval", "running-hook", "unknown-hook", "policy-missing", "history-change"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			before := codexPluginScope(t)
			before.NativeID = "owned"
			before.Cwd = t.TempDir()
			before.StateNamespace = before.Instance.Profile
			before.PluginSelections[0].Hooks = &PluginHookSelection{Trust: "trusted"}
			before.PluginSelections[0].Components = []PluginComponent{{Kind: "hook", Name: "UserPromptSubmit"}}
			after := before
			after.PluginSelections = nil
			path := filepath.Join(before.StateNamespace, "sessions", "owned.jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			// The durable policy write can complete before detach/resume. A positive
			// exclusion therefore cannot attest that the new hook configuration exists.
			writeMixedPolicy(t, path, before, []string{before.PluginSelections[0].NativeSource})
			checking := false
			f.call = func(method string, params map[string]any) (any, error) {
				status, window := "idle", 1
				turns := []any{}
				if checking {
					if scenario == "not-loaded" {
						status = "notLoaded"
					}
					if scenario == "config-drift" {
						window++
					}
					if scenario == "history-change" {
						turns = append(turns, map[string]any{"id": "unexpected", "status": "completed", "items": []any{}})
					}
				}
				switch method {
				case "config/read":
					if checking && scenario == "config-read-error" {
						return nil, fmt.Errorf("PRIVATE_CONFIG_ERROR")
					}
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true, "hooks": true}, "model_context_window": window}}, nil
				case "configRequirements/read":
					return map[string]any{"requirements": nil}, nil
				case "thread/read":
					return map[string]any{"thread": map[string]any{"id": before.NativeID, "cwd": before.Cwd, "model": before.Route.Model, "modelProvider": before.Route.Provider, "path": path, "status": map[string]string{"type": status}, "turns": turns}}, nil
				case "thread/turns/list":
					return map[string]any{"data": turns}, nil
				case "mcpServerStatus/list":
					return map[string]any{"data": []any{}}, nil
				default:
					return emptyControl(method)
				}
			}
			baseline, err := configurationBaseline(context.Background(), f, before.NativeID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "missing-witness" {
				witnessScope := after
				if scenario == "old-selection" {
					witnessScope = before
				}
				fingerprint, err := codexConfigurationFingerprint(context.Background(), f, witnessScope)
				if err != nil {
					t.Fatal(err)
				}
				if err = c.rememberSelectedHooks(context.Background(), f, witnessScope, before.NativeID, fingerprint); err != nil {
					t.Fatal(err)
				}
			}
			key := before.Instance.ID + "/" + before.NativeID
			switch scenario {
			case "other-connection":
				c.clients[before.Instance.ID] = &permissionConnection{nativeFixture: f, identity: "replacement"}
			case "pending-approval":
				c.pending[key+"/request"] = Pending{ID: key + "/request", NativeID: before.NativeID, Status: "pending", ResponseKind: "approval"}
			case "running-hook", "unknown-hook":
				c.hooks[key] = map[string]HookExecution{"hook": {ID: "hook", Status: strings.TrimSuffix(scenario, "-hook")}}
			case "policy-missing":
				writeMixedPolicy(t, path, before, nil)
			}
			checking = true
			in := Intent{ID: "retirement", Kind: "apply_execution", Scope: after, PreviousScope: &before, Permission: "read_only", ConfigurationBaseline: baseline}
			for range 2 {
				r, err := c.Reconcile(context.Background(), in, Receipt{NativeID: before.NativeID})
				if (r.State == "finished" && r.Status == "succeeded" && err == nil) != (scenario == "acknowledged") {
					t.Fatalf("retirement %s: state=%s detail=%s error=%v", scenario, r.State, r.Evidence.Detail, err)
				}
				if scenario == "missing-witness" && !strings.Contains(r.Evidence.Detail, "hook 撤去") {
					t.Fatal("missing retirement evidence has no specific reason", r.Evidence.Detail)
				}
			}
			for _, method := range f.methods {
				if slices.Contains([]string{"thread/start", "thread/resume", "thread/unsubscribe", "thread/settings/update", "turn/start"}, method) {
					t.Fatal("read-only retirement reconciliation wrote native state", method)
				}
			}
		})
	}
}
