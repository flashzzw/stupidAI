package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCodexRecoveryUnloadedRequiresCompleteList(t *testing.T) {
	for _, mode := range []string{"unloaded", "loaded", "missing", "null", "invalid", "loaded-later-page", "repeated-page"} {
		t.Run(mode, func(t *testing.T) {
			_, f, _ := nativeHarness(t)
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "thread/read" {
					return map[string]any{"thread": map[string]any{"id": "original", "status": map[string]string{"type": "notLoaded"}}}, nil
				}
				if method == "thread/loaded/list" {
					switch mode {
					case "missing":
						return map[string]any{}, nil
					case "null":
						return map[string]any{"data": nil}, nil
					case "invalid":
						return map[string]any{"data": 42}, nil
					case "loaded":
						return map[string]any{"data": []string{"original"}}, nil
					case "loaded-later-page":
						if params["cursor"] == "next" {
							return map[string]any{"data": []string{"original"}}, nil
						}
						return map[string]any{"data": []string{"other"}, "nextCursor": "next"}, nil
					case "repeated-page":
						return map[string]any{"data": []string{}, "nextCursor": "next"}, nil
					}
					return map[string]any{"data": []string{}}, nil
				}
				return nil, fmt.Errorf("unexpected %s", method)
			}
			err := codexRecoveryUnloaded(context.Background(), f, "original")
			if (err == nil) != (mode == "unloaded") {
				t.Fatalf("unloaded evidence accepted=%t: %v", err == nil, err)
			}
		})
	}
}

func TestCodexRecoveryCheckNeverWritesAndRejectsDrift(t *testing.T) {
	for _, mode := range []string{"ready", "loaded", "queue", "history", "config-error", "read-race"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			scope := codexPluginScope(t)
			scope.NativeID = "original"
			scope.RecoveryPermission = "read_only"
			if err := prepareCodexPluginCaches(scope); err != nil {
				t.Fatal(err)
			}
			candidate := scope
			candidate.PluginSelections = append([]PluginSelection{}, scope.PluginSelections...)
			candidate.PluginSelections[0].Mode = "disabled"
			failed := Intent{ID: "apply", Kind: "apply_execution", Scope: candidate, PreviousScope: &scope, ConfigurationBaseline: fmt.Sprintf("%x", sha256.Sum256([]byte("[]"))), Permission: "read_only"}
			reads := 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					if mode == "config-error" {
						return nil, fmt.Errorf("fixture config unavailable")
					}
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
				case "configRequirements/read":
					return map[string]any{"requirements": nil}, nil
				case "thread/read":
					reads++
					status := "notLoaded"
					if mode == "loaded" || mode == "read-race" && reads > 3 {
						status = "idle"
					}
					turns := []any{}
					if mode == "history" {
						turns = append(turns, map[string]any{"id": "external", "status": "completed", "items": []any{}})
					}
					return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "model": "fixture", "modelProvider": "fixture", "status": map[string]string{"type": status}, "turns": turns}}, nil
				case "thread/loaded/list":
					return map[string]any{"data": []string{}}, nil
				case "thread/turns/list":
					return map[string]any{"data": []any{}}, nil
				case "thread/queue/list":
					if mode == "queue" {
						return map[string]any{"data": []any{map[string]any{"id": "queued"}}}, nil
					}
					return map[string]any{"data": []any{}}, nil
				default:
					return emptyControl(method)
				}
			}
			report, err := c.ReadConfigurationRecovery(context.Background(), failed)
			if err != nil {
				t.Fatal(err)
			}
			all := true
			for _, check := range report.Checks {
				all = all && check.Status == "passed"
			}
			if all != (mode == "ready") {
				t.Fatalf("check outcome %+v", report)
			}
			if err := ValidateConfigurationRecoveryCheck(failed, report); err != nil {
				t.Fatal(err)
			}
			for _, method := range f.methods {
				if method == "thread/resume" || method == "thread/settings/update" || method == "thread/unsubscribe" {
					t.Fatalf("read-only check sent %s", method)
				}
			}
		})
	}
}

func TestCodexRecoveryDispatchAndOperationProof(t *testing.T) {
	for _, mode := range []string{"success", "loaded", "race-before-resume", "history-drift", "lost-reply", "wrong-reply", "wrong-operation", "closed-connection", "acknowledged-restart", "acknowledged-missing", "acknowledged-hash", "acknowledged-version", "acknowledged-time", "acknowledged-future", "acknowledged-before-intent", "acknowledged-native-id", "acknowledged-fingerprint", "acknowledged-config-drift", "acknowledged-history", "acknowledged-queue", "acknowledged-loaded", "acknowledged-read-race", "acknowledged-late-drift"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			scope := codexPluginScope(t)
			scope.NativeID = "original"
			scope.RecoveryPermission = "read_only"
			if err := prepareCodexPluginCaches(scope); err != nil {
				t.Fatal(err)
			}
			candidate := scope
			candidate.PluginSelections = append([]PluginSelection{}, scope.PluginSelections...)
			candidate.PluginSelections[0].Mode = "disabled"
			in := Intent{ID: "recover", Kind: "recover_execution", Scope: scope, ConfigurationTarget: &candidate, ConfigurationRecoveryOf: "apply", ConfigurationBaseline: fmt.Sprintf("%x", sha256.Sum256([]byte("[]"))), Permission: "read_only"}
			loaded := mode == "loaded"
			cold, coldReads := false, 0
			lists, resumes := 0, 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					if cold && (mode == "acknowledged-config-drift" || mode == "acknowledged-late-drift" && lists >= 3) {
						return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}, "changed": true}}, nil
					}
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
				case "configRequirements/read":
					return map[string]any{"requirements": nil}, nil
				case "thread/read":
					if cold {
						coldReads++
						if mode == "acknowledged-read-race" && coldReads > 4 {
							loaded = true
						}
					}
					status := "notLoaded"
					if loaded {
						status = "idle"
					}
					turns := []any{}
					if mode == "history-drift" && lists >= 3 || cold && mode == "acknowledged-history" {
						turns = append(turns, map[string]any{"id": "external", "status": "completed", "items": []any{}})
					}
					return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "model": "fixture", "modelProvider": "fixture", "status": map[string]string{"type": status}, "turns": turns}}, nil
				case "thread/loaded/list":
					lists++
					ids := []string{}
					if loaded || mode == "race-before-resume" && lists >= 4 {
						ids = append(ids, scope.NativeID)
					}
					return map[string]any{"data": ids}, nil
				case "thread/turns/list":
					return map[string]any{"data": []any{}}, nil
				case "thread/queue/list":
					if cold && mode == "acknowledged-queue" {
						return map[string]any{"data": []any{map[string]any{"id": "queued"}}}, nil
					}
					return map[string]any{"data": []any{}}, nil
				case "mcpServerStatus/list":
					return map[string]any{"data": []any{map[string]any{"name": "identity", "pluginId": scope.PluginSelections[0].NativeSource, "runtimeStatus": "connected", "tools": map[string]any{}}}}, nil
				case "thread/resume":
					resumes++
					loaded = true
					if mode == "lost-reply" {
						return nil, fmt.Errorf("lost native reply")
					}
					id := scope.NativeID
					if mode == "wrong-reply" {
						id = "other"
					}
					return map[string]any{"thread": map[string]any{"id": id, "status": map[string]string{"type": "idle"}}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request"}, nil
				default:
					return emptyControl(method)
				}
			}
			receipt, err := c.Execute(context.Background(), in)
			unsent := mode == "loaded" || mode == "race-before-resume" || mode == "history-drift"
			if unsent {
				if err == nil || resumes != 0 {
					t.Fatalf("unsafe recovery dispatched: %d %v", resumes, err)
				}
				return
			}
			if resumes != 1 {
				t.Fatalf("recovery dispatches=%d: %v", resumes, err)
			}
			if mode == "lost-reply" {
				c.clients[scope.Instance.ID] = f
			}
			if mode == "wrong-operation" {
				in.ConfigurationRecoveryOf = "different-apply"
			}
			if mode == "closed-connection" {
				_ = c.Close()
				c.clients[scope.Instance.ID] = f
			}
			if strings.HasPrefix(mode, "acknowledged-") {
				if err != nil || receipt.ConfigurationRecovery == nil {
					t.Fatalf("fully checked recovery missing acknowledgement: %v", err)
				}
				// Exercise the persisted wire shape rather than copying pointers.
				body, err := json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(body, &receipt); err != nil {
					t.Fatal(err)
				}
				_ = c.Close()
				c = NewCodex()
				c.clients[scope.Instance.ID] = f
				cold, loaded = true, mode == "acknowledged-loaded"
				lists = 0
				switch mode {
				case "acknowledged-missing":
					receipt.ConfigurationRecovery = nil
				case "acknowledged-hash":
					receipt.ConfigurationRecovery.IntentHash = strings.Repeat("a", 64)
				case "acknowledged-version":
					receipt.ConfigurationRecovery.Version = 2
				case "acknowledged-time":
					receipt.ConfigurationRecovery.At = "invalid"
				case "acknowledged-future":
					receipt.ConfigurationRecovery.At = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
				case "acknowledged-before-intent":
					in.CreatedAt = time.Now().Format(time.RFC3339Nano)
					receipt.ConfigurationRecovery.IntentHash = codexRecoveryHash(in)
					receipt.ConfigurationRecovery.At = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
				case "acknowledged-native-id":
					receipt.NativeID = "other"
				case "acknowledged-fingerprint":
					receipt.ConfigurationRecovery.Fingerprint = strings.Repeat("f", 64)
				}
			}
			result, e := c.Reconcile(context.Background(), in, receipt)
			if e != nil && mode != "wrong-operation" && !strings.HasPrefix(mode, "acknowledged-") {
				t.Fatal(e)
			}
			if (result.State == "finished" && result.Status == "succeeded") != (mode == "success" || mode == "acknowledged-restart") {
				t.Fatalf("wrong recovery result %+v; execute %v", result, err)
			}
			if mode == "acknowledged-restart" && (e != nil || result.View.NativeStatus != "notLoaded" || result.View.ExecutionClear || result.View.PluginEvidence[0].Verified) {
				t.Fatal("historical acknowledgement invented current loading or input authority")
			}
			if resumes != 1 {
				t.Fatal("reconcile replayed configuration")
			}
		})
	}
}
