package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"codex-netcap/internal/codex"
)

func TestCodexSendAfterColdRecoveryPreservesRequestedPermission(t *testing.T) {
	c, f, scope := nativeHarness(t)
	loaded, actualSandbox, actualApproval := false, "", ""
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "thread/read":
			status := "notLoaded"
			if loaded {
				status = "idle"
			}
			return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "status": map[string]string{"type": status}, "turns": []any{}}}, nil
		case "thread/resume":
			loaded, actualSandbox, actualApproval = true, "read-only", "never"
			if value, ok := params["sandbox"].(string); ok {
				actualSandbox = value
			}
			if value, ok := params["approvalPolicy"].(string); ok {
				actualApproval = value
			}
			kind := "readOnly"
			if actualSandbox == "workspace-write" {
				kind = "workspaceWrite"
			}
			return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "status": map[string]string{"type": "idle"}}, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "approvalPolicy": actualApproval, "sandbox": map[string]any{"type": kind, "networkAccess": false}}, nil
		case "turn/start":
			return map[string]any{"turn": map[string]string{"id": "continued-original"}}, nil
		}
		return emptyControl(method)
	}
	receipt, err := c.Execute(context.Background(), Intent{ID: "continue-original", Kind: "send", Scope: scope, Permission: "workspace", Text: "continue"})
	if err != nil || receipt.NativeTurn != "continued-original" {
		t.Fatalf("original continuation: %+v %v", receipt, err)
	}
	if actualSandbox != "workspace-write" || actualApproval != "on-request" {
		t.Fatalf("continued with changed permission: sandbox=%s approval=%s", actualSandbox, actualApproval)
	}
}

func TestCodexRecoveryPermissionBoundaries(t *testing.T) {
	for _, scenario := range []string{"workspace", "read_only", "probe", "missing_mode", "missing_sandbox", "missing_approval", "wrong_sandbox", "wrong_approval", "structured_approval", "loaded_mismatch", "readonly_preview", "running"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.RecoveryPermission = "workspace"
			status, resumes := "notLoaded", 0
			if scenario == "read_only" {
				scope.RecoveryPermission = "read_only"
			}
			if scenario == "probe" {
				scope.Purpose, scope.RecoveryPermission = "probe", "read_only"
			}
			if scenario == "missing_mode" {
				scope.RecoveryPermission = ""
			}
			if scenario == "loaded_mismatch" {
				status = "idle"
			}
			if scenario == "readonly_preview" {
				scope.NoAutoResume = true
			}
			if scenario == "running" {
				status = "active"
			}
			f.call = func(method string, params map[string]any) (any, error) {
				thread := map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "status": map[string]string{"type": status}, "turns": []any{}}
				if method == "thread/read" {
					return map[string]any{"thread": thread}, nil
				}
				if method == "thread/resume" {
					resumes++
					if status == "notLoaded" {
						sandbox, approval := "workspace-write", "on-request"
						if scope.RecoveryPermission == "read_only" {
							sandbox = "read-only"
						}
						if scope.Purpose == "probe" {
							approval = "never"
						}
						if params["sandbox"] != sandbox || params["approvalPolicy"] != approval {
							t.Fatalf("recovery permission omitted: %+v", params)
						}
					} else if len(params) != 2 {
						t.Fatalf("loaded policy was overwritten: %+v", params)
					}
					status = "idle"
					thread["status"] = map[string]string{"type": status}
					kind, approval := "workspaceWrite", "on-request"
					if scope.RecoveryPermission == "read_only" {
						kind = "readOnly"
					}
					if scope.Purpose == "probe" {
						approval = "never"
					}
					out := map[string]any{"thread": thread, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "sandbox": map[string]any{"type": kind}, "approvalPolicy": approval}
					switch scenario {
					case "missing_sandbox":
						delete(out, "sandbox")
					case "missing_approval":
						delete(out, "approvalPolicy")
					case "wrong_sandbox", "loaded_mismatch":
						out["sandbox"] = map[string]string{"type": "readOnly"}
					case "wrong_approval":
						out["approvalPolicy"] = "never"
					case "structured_approval":
						out["approvalPolicy"] = map[string]bool{"reject": true}
					}
					return out, nil
				}
				if method == "turn/start" {
					t.Fatal("observation sent model input")
				}
				return emptyControl(method)
			}
			view, err := c.Observe(context.Background(), scope)
			ok := scenario == "workspace" || scenario == "read_only" || scenario == "probe" || scenario == "readonly_preview" || scenario == "running"
			if (err == nil) != ok {
				t.Fatalf("observation: %+v %v", view, err)
			}
			if scenario == "readonly_preview" || scenario == "running" || scenario == "missing_mode" {
				if resumes != 0 {
					t.Fatal("unapproved resume or active-thread mutation")
				}
				return
			}
			if view.PermissionEvidence == nil || view.PermissionEvidence.Verified != ok {
				t.Fatal("incorrect permission evidence")
			}
			if !ok {
				// A retry on the now-loaded thread must still reject the mismatch.
				if _, err = c.Observe(context.Background(), scope); err == nil {
					t.Fatal("retry silently accepted or repaired changed policy")
				}
				fresh := NewCodex()
				fresh.clients[scope.Instance.ID] = f
				if _, err = fresh.Observe(context.Background(), scope); err == nil {
					t.Fatal("controller restart erased permission mismatch")
				}
			}
		})
	}
}

type permissionConnection struct {
	*nativeFixture
	identity string
}

func (f *permissionConnection) ConnectionID() string { return f.identity }

func TestCodexFreshCreationPolicyIsBoundAndConsumed(t *testing.T) {
	for _, scenario := range []string{"first_input", "new_connection", "wrong_metadata", "different_mode", "unrelated_error", "invalid_start", "external_activity", "external_history"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.RecoveryPermission = "workspace"
			c.clients[scope.Instance.ID] = &permissionConnection{nativeFixture: f, identity: "original-connection"}
			changed, externalActive := false, false
			f.call = func(method string, params map[string]any) (any, error) {
				thread := map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "status": map[string]string{"type": "idle"}, "turns": []any{}}
				switch method {
				case "thread/start":
					kind := "workspaceWrite"
					if scenario == "invalid_start" {
						kind = "readOnly"
					}
					return map[string]any{"thread": thread, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "approvalPolicy": "on-request", "sandbox": map[string]string{"type": kind}}, nil
				case "thread/read":
					if externalActive {
						thread["status"] = map[string]string{"type": "active"}
					}
					if changed && scenario == "external_history" {
						thread["turns"] = []any{map[string]any{"id": "external", "status": "completed", "items": []any{}}}
					}
					if changed && scenario == "wrong_metadata" {
						thread["model"] = "changed"
					}
					return map[string]any{"thread": thread}, nil
				case "thread/resume":
					message := "no rollout found for thread id " + scope.NativeID
					if changed && scenario == "unrelated_error" {
						message = "other missing history"
					}
					return nil, &codex.RPCError{Code: -32600, Message: message}
				case "turn/start":
					return map[string]any{"turn": map[string]string{"id": "first"}}, nil
				}
				return emptyControl(method)
			}
			_, err := c.Execute(context.Background(), Intent{Kind: "create", Scope: scope, Permission: "workspace"})
			if scenario == "invalid_start" {
				if err == nil || len(c.creationPolicies) != 0 {
					t.Fatal("unverified start became initial permission evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			view, err := c.Observe(context.Background(), scope)
			if err != nil || view.PermissionEvidence == nil || view.PermissionEvidence.Source != "codex.thread/start" {
				t.Fatalf("fresh start policy lost: %+v %v", view, err)
			}
			changed = true
			if scenario == "external_activity" {
				externalActive = true
				if _, err := c.Observe(context.Background(), scope); err != nil {
					t.Fatal(err)
				}
				externalActive = false
			}
			if scenario == "new_connection" {
				c.clients[scope.Instance.ID] = &permissionConnection{nativeFixture: f, identity: "replacement-connection"}
			}
			if scenario == "different_mode" {
				scope.RecoveryPermission = "read_only"
			}
			if scenario == "first_input" {
				if _, err := c.Execute(context.Background(), Intent{Kind: "send", Scope: scope, Permission: "workspace", Text: "first"}); err != nil {
					t.Fatal(err)
				}
				if len(c.creationPolicies) != 0 {
					t.Fatal("first input retained creation witness")
				}
			}
			if _, err = c.Observe(context.Background(), scope); err == nil {
				t.Fatal("old start evidence substituted for new proof")
			}
		})
	}
}

func TestCodexMissingOriginalPermissionCannotGrantHandoffOrInput(t *testing.T) {
	c, f, scope := nativeHarness(t)
	f.call = func(method string, _ map[string]any) (any, error) { return nil, fmt.Errorf("unexpected %s", method) }
	if _, err := c.ObserveFresh(context.Background(), scope); err == nil {
		t.Fatal("missing original permission granted handoff")
	}
	if _, err := c.Execute(context.Background(), Intent{Kind: "send", Scope: scope, Text: "no permission"}); err == nil {
		t.Fatal("missing permission sent input")
	}
	if len(f.methods) != 0 {
		t.Fatal("missing permission reached native RPC")
	}
	data, _ := json.Marshal(Scope{RecoveryPermission: "workspace"})
	var decoded Scope
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.RecoveryPermission != "" {
		t.Fatal("transient policy duplicated persisted Session.Permission")
	}
}
