package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"codex-netcap/internal/codex"
)

func creationRollout(t *testing.T, scope Scope, id, source string) string {
	t.Helper()
	path := filepath.Join(scope.Instance.Profile, id+".jsonl")
	data, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": id, "session_id": id, "cwd": scope.Cwd, "model_provider": scope.Route.Provider, "thread_source": source, "cli_version": "0.158.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexColdCreationRecoveryUsesReadOnlySourceProof(t *testing.T) {
	for _, mode := range []string{"receipt", "discovery", "duplicate", "loaded_duplicate", "loaded_no_rollout", "unrelated_untagged", "profile_alias"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Profile, scope.Cwd = t.TempDir(), t.TempDir()
			id := scope.NativeID
			path := creationRollout(t, scope, id, "netcap-console:create-operation")
			otherPath := creationRollout(t, scope, "other", "netcap-console:create-operation")
			if mode == "unrelated_untagged" {
				otherPath = creationRollout(t, scope, "other", "")
			}
			if mode == "profile_alias" {
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(scope.Instance.Profile, alias); err != nil {
					t.Fatal(err)
				}
				scope.Instance.Profile = alias
			}
			if mode == "loaded_no_rollout" {
				path = ""
			}
			loaded, resumes := false, 0
			f.call = func(method string, params map[string]any) (any, error) {
				thread := idlePermissionThread(scope)
				thread.ID, thread.Path = id, path
				if !loaded {
					thread.Status.Type = "notLoaded"
				}
				switch method {
				case "thread/loaded/list":
					if strings.HasPrefix(mode, "loaded_") {
						return map[string]any{"data": []string{id}}, nil
					}
					return map[string]any{"data": []string{}}, nil
				case "thread/list":
					if _, tree := params["ancestorThreadId"]; tree {
						return emptyControl(method)
					}
					if !slices.Contains(params["sourceKinds"].([]string), "vscode") {
						return map[string]any{"data": []any{}}, nil
					}
					data := []codex.Thread{thread}
					if mode == "duplicate" || mode == "loaded_duplicate" || mode == "unrelated_untagged" {
						other := thread
						other.ID, other.Path = "other", otherPath
						data = append(data, other)
					}
					return map[string]any{"data": data}, nil
				case "thread/read":
					if strings.HasPrefix(mode, "loaded_") {
						thread.Source = "netcap-console:create-operation"
					}
					return map[string]any{"thread": thread}, nil
				case "thread/resume":
					resumes++
					loaded = true
					out := idlePermissionReply(scope)
					out.Thread = thread
					out.Thread.Status.Type = "idle"
					return out, nil
				}
				return emptyControl(method)
			}
			scope.NativeID = ""
			receipt := Receipt{}
			if mode == "receipt" {
				receipt.NativeID = id
			}
			result, err := c.Reconcile(context.Background(), Intent{ID: "create-operation", Kind: "create", Scope: scope, Permission: "workspace"}, receipt)
			if mode == "duplicate" || mode == "loaded_duplicate" {
				if err == nil || resumes != 0 {
					t.Fatalf("ambiguous creation resumed: %+v %v", result, err)
				}
			} else if err != nil || result.State != "finished" || result.Receipt.NativeID != id || resumes != 1 {
				t.Fatalf("original creation not recovered: resumes=%d result=%+v err=%v", resumes, result, err)
			}
		})
	}
}

func TestCodexColdCreationRejectsUnprovenSourceBeforeResume(t *testing.T) {
	for _, scenario := range []string{"conflicting_live_source", "foreign_header", "wrong_id", "wrong_session_id", "wrong_cwd", "wrong_provider", "wrong_version", "missing_source", "missing_path", "relative_path", "outside_profile", "escaping_symlink", "truncated", "wrong_entry", "duplicate_source", "oversized", "fifo", "directory"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Profile, scope.Cwd = t.TempDir(), t.TempDir()
			path := creationRollout(t, scope, scope.NativeID, "netcap-console:original")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			switch scenario {
			case "foreign_header":
				text = strings.ReplaceAll(text, "netcap-console:original", "netcap-console:foreign")
			case "wrong_id":
				text = strings.Replace(text, `"id":"native"`, `"id":"wrong"`, 1)
			case "wrong_session_id":
				text = strings.Replace(text, `"session_id":"native"`, `"session_id":"wrong"`, 1)
			case "wrong_cwd":
				text = strings.Replace(text, scope.Cwd, "/wrong", 1)
			case "wrong_provider":
				text = strings.Replace(text, `"model_provider":"local"`, `"model_provider":"other"`, 1)
			case "wrong_version":
				text = strings.Replace(text, "0.158.0", "0.1.0", 1)
			case "missing_source":
				text = strings.Replace(text, `"thread_source":"netcap-console:original"`, `"unrelated":"netcap-console:original"`, 1)
			case "truncated":
				text = strings.TrimSuffix(text, "\n")
			case "wrong_entry":
				text = strings.Replace(text, "session_meta", "response_item", 1)
			case "duplicate_source":
				text = strings.Replace(text, `"thread_source":`, `"thread_source":"foreign","thread_source":`, 1)
			case "oversized":
				text = strings.Repeat(" ", 4<<20) + text
			}
			if err = os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing_path":
				path = ""
			case "relative_path":
				path = "native.jsonl"
			case "outside_profile", "escaping_symlink":
				other := filepath.Join(t.TempDir(), "outside.jsonl")
				if err = os.WriteFile(other, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				if scenario == "outside_profile" {
					path = other
				} else {
					if err = os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err = os.Symlink(other, path); err != nil {
						t.Fatal(err)
					}
				}
			case "fifo":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err = syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				path = scope.Instance.Profile
			}
			resumes := 0
			f.call = func(method string, _ map[string]any) (any, error) {
				if method == "thread/read" {
					thread := idlePermissionThread(scope)
					thread.Path = path
					thread.Status.Type = "notLoaded"
					if scenario == "conflicting_live_source" {
						thread.Source = "foreign"
					}
					return map[string]any{"thread": thread}, nil
				}
				if method == "thread/resume" {
					resumes++
					return idlePermissionReply(scope), nil
				}
				return emptyControl(method)
			}
			result, _ := c.Reconcile(context.Background(), Intent{ID: "original", Kind: "create", Scope: scope, Permission: "workspace"}, Receipt{NativeID: scope.NativeID})
			if resumes != 0 || result.State != "unknown" {
				t.Fatalf("unproven source granted recovery: resumes=%d %+v", resumes, result)
			}
		})
	}
}

func TestCodexCreationReadRejectionKeepsSiblingRequests(t *testing.T) {
	c, f, scope := nativeHarness(t)
	key := scope.Instance.ID + "/sibling"
	c.pending[key] = Pending{ID: key, NativeID: "sibling", Status: "pending", ConnectionID: f.ConnectionID()}
	f.call = func(method string, _ map[string]any) (any, error) {
		return nil, &codex.RPCError{Code: -32600, Method: method, Message: "thread not found"}
	}
	if _, err := c.Reconcile(context.Background(), Intent{ID: "create", Kind: "create", Scope: scope}, Receipt{NativeID: scope.NativeID}); err == nil {
		t.Fatal("missing native thread accepted")
	}
	if f.closed || c.pending[key].Status != "pending" {
		t.Fatal("one rejected create read closed sibling approval connection")
	}
}
