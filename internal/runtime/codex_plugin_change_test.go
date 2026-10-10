package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexPluginApplyChangesLoadedThreadOnceAndChecksRetiredSources(t *testing.T) {
	for _, mode := range []string{"upgrade", "inherit", "lost-receipt", "retired-remains", "busy", "history-change", "unsubscribe-error", "unsubscribe-invalid", "busy-after-detach", "cache-drift", "identity-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			before := codexPluginScope(t)
			old := before.PluginSelections[0]
			next := codexPluginScope(t).PluginSelections[0]
			next.SourceHash = strings.Repeat("1", 64)
			next.NativeSource, next.CachePath, _ = CodexPluginLocation(before, next)
			after := before
			after.PluginSelections = []PluginSelection{next}
			if mode == "inherit" || mode == "retired-remains" {
				after.PluginSelections = nil
			}
			origin := old.NativeSource
			detached := false
			applying := false
			resumes, unsubscribes := 0, 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
				case "thread/start":
					return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request"}, nil
				case "thread/read":
					status := "idle"
					if applying && (mode == "busy" || mode == "busy-after-detach" && detached) {
						status = "active"
					}
					turns := []any{}
					if applying && mode == "history-change" {
						turns = append(turns, map[string]any{"id": "external", "status": "completed", "items": []any{}})
					}
					return map[string]any{"thread": map[string]any{"id": "created", "model": "fixture", "modelProvider": "fixture", "cwd": before.Cwd, "status": map[string]string{"type": status}, "turns": turns}}, nil
				case "thread/turns/list":
					if applying && mode == "history-change" {
						return map[string]any{"data": []any{map[string]any{"id": "external", "status": "completed", "items": []any{}}}}, nil
					}
					return map[string]any{"data": []any{}}, nil
				case "mcpServerStatus/list":
					rows := []any{map[string]any{"name": "unrelated", "pluginId": nil, "runtimeStatus": "connected", "tools": map[string]any{}}}
					if origin != "" {
						rows = append(rows, map[string]any{"name": "identity", "pluginId": origin, "runtimeStatus": "connected", "tools": map[string]any{}})
					}
					return map[string]any{"data": rows}, nil
				case "thread/unsubscribe":
					unsubscribes++
					if mode == "unsubscribe-error" {
						return nil, errors.New("lost unsubscribe receipt")
					}
					if mode == "unsubscribe-invalid" {
						return map[string]any{"status": "unexpected"}, nil
					}
					if mode == "cache-drift" {
						if err := os.WriteFile(filepath.Join(next.CachePath, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0600); err != nil {
							t.Fatal(err)
						}
					}
					detached = true
					return map[string]any{"status": "unsubscribed"}, nil
				case "thread/resume":
					resumes++
					if !detached {
						t.Fatal("applied to an attached native thread")
					}
					detached = false
					if mode != "retired-remains" {
						origin = ""
						if params["config"].(map[string]any)["plugins."+next.NativeSource+".enabled"] == true {
							origin = next.NativeSource
						}
					}
					if mode == "identity-mismatch" {
						return map[string]any{"thread": map[string]any{"id": "wrong"}}, nil
					}
					if mode == "lost-receipt" {
						return nil, errors.New("lost real response")
					}
					return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request"}, nil
				default:
					return emptyControl(method)
				}
			}
			if _, err := c.Execute(context.Background(), Intent{ID: "create", Kind: "create", Scope: before, Permission: "read_only"}); err != nil {
				t.Fatal(err)
			}
			before.NativeID = "created"
			after.NativeID = "created"
			prepared, err := c.Execute(context.Background(), Intent{Kind: "prepare_execution", Scope: before, Permission: "read_only"})
			if err != nil {
				t.Fatal(err)
			}
			if unsubscribes != 0 || resumes != 0 {
				t.Fatal("preparation changed a subscription")
			}
			intent := Intent{ID: "apply", Kind: "apply_execution", Scope: after, PreviousScope: &before, Permission: "read_only", ConfigurationBaseline: prepared.ConfigurationBaseline}
			applying = true
			receipt, err := c.Execute(context.Background(), intent)
			if mode == "busy" || mode == "history-change" || mode == "unsubscribe-error" || mode == "unsubscribe-invalid" || mode == "busy-after-detach" || mode == "cache-drift" {
				var notSent *NotSent
				if !errors.As(err, &notSent) || resumes != 0 {
					t.Fatalf("unverified boundary sent configuration: resumes=%d error=%v", resumes, err)
				}
				wantUnsubs := 1
				if mode == "busy" || mode == "history-change" {
					wantUnsubs = 0
				}
				if unsubscribes != wantUnsubs {
					t.Fatal("unexpected unsubscribe")
				}
				return
			}
			if mode == "identity-mismatch" {
				var notSent *NotSent
				if err == nil || errors.As(err, &notSent) || unsubscribes != 1 || resumes != 1 {
					t.Fatal("mismatched response lost unknown mutation state")
				}
				return
			}
			if mode == "lost-receipt" {
				var notSent *NotSent
				if err == nil || errors.As(err, &notSent) {
					t.Fatal("lost response treated as safe retry")
				}
				f.closed = false
				c.clients[before.Instance.ID] = f
			} else if err != nil {
				t.Fatal(err)
			}
			if unsubscribes != 1 || resumes != 1 {
				t.Fatal("change did not use one explicit detach/resume")
			}
			for i := 0; i < 2; i++ {
				resolution, err := c.Reconcile(context.Background(), intent, receipt)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "retired-remains" {
					if resolution.State == "finished" {
						t.Fatal("retired plugin incorrectly restored as inheritance")
					}
				} else if resolution.State != "finished" || resolution.Status != "succeeded" {
					t.Fatalf("unverified change: %+v", resolution)
				}
			}
			if unsubscribes != 1 || resumes != 1 {
				t.Fatal("reconciliation replayed native change")
			}
		})
	}
}
