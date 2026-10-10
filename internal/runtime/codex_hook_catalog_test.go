package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCodexHookCatalogPreservesSourceAndTrustWithoutExecuting(t *testing.T) {
	c, f, scope := nativeHarness(t)
	scope.Instance.Kind = "codex"
	reader, ok := any(c).(HookCatalogReader)
	if !ok {
		t.Fatal("Codex has no hook catalog reader")
	}
	f.call = func(method string, params map[string]any) (any, error) {
		if method != "hooks/list" || !reflect.DeepEqual(params, map[string]any{"cwds": []string{scope.Cwd}}) {
			t.Fatalf("unexpected call: %s %+v", method, params)
		}
		return map[string]any{"data": []any{map[string]any{"cwd": scope.Cwd, "warnings": []string{"PRIVATE_WARNING"}, "errors": []any{map[string]any{"path": "/broken/hooks.json", "message": "PRIVATE_ERROR"}}, "hooks": []any{map[string]any{
			"key": "fixture@market:hook", "eventName": "preToolUse", "matcher": "Read", "handlerType": "command", "command": "PRIVATE_COMMAND", "timeoutSec": 60, "sourcePath": "/cache/fixture/1/hooks/hooks.json", "source": "plugin", "pluginId": "fixture@market", "enabled": true, "isManaged": false, "trustStatus": "untrusted", "currentHash": "sha256:" + strings.Repeat("a", 64), "async": true,
		}}}}}, nil
	}
	out, err := reader.ReadHookCatalog(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var view map[string]any
	_ = json.Unmarshal(b, &view)
	if out.Source != "codex.hooks/list" || out.Complete || out.NativeID != scope.NativeID || len(out.Items) != 1 || view["scope"] != "directory" || view["policy_reported"] != false {
		t.Fatalf("wrong discovery scope: %s", b)
	}
	item := view["items"].([]any)[0].(map[string]any)
	if item["plugin_id"] != "fixture@market" || item["trust_status"] != "untrusted" || item["source_path"] != "/cache/fixture/1/hooks/hooks.json" || item["managed"] != false || !out.Items[0].Background || out.Items[0].Disabled {
		t.Fatalf("wrong source state: %s", b)
	}
	if strings.Contains(string(b), "PRIVATE_") || len(f.methods) != 1 || len(f.replies) != 0 {
		t.Fatal("catalog exposed bodies or executed work")
	}
}

func TestCodexHookCatalogRejectsIncompleteOrMismatchedMetadata(t *testing.T) {
	for _, mode := range []string{"empty", "unknown-source-trust", "disabled", "missing-hooks", "missing-warnings", "missing-errors", "wrong-cwd", "duplicate-cwd", "missing-enabled", "missing-managed", "missing-timeout", "missing-async", "invalid-hash", "relative-path", "duplicate-key", "rpc-error", "wrong-backend", "probe", "query-purpose"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind = "codex"
			hook := map[string]any{"key": "source:key", "eventName": "preToolUse", "matcher": "Read", "handlerType": "command", "command": "PRIVATE_COMMAND", "timeoutSec": 0, "sourcePath": "/fixture/hooks.json", "source": "user", "pluginId": nil, "enabled": true, "isManaged": false, "trustStatus": "untrusted", "currentHash": "sha256:" + strings.Repeat("b", 64), "async": false}
			entry := map[string]any{"cwd": scope.Cwd, "hooks": []any{hook}, "warnings": []string{}, "errors": []any{}}
			switch mode {
			case "empty":
				entry["hooks"] = []any{}
			case "unknown-source-trust":
				hook["source"], hook["trustStatus"] = "future-source", "future-trust"
			case "disabled":
				hook["enabled"] = false
			case "missing-hooks":
				delete(entry, "hooks")
			case "missing-warnings":
				delete(entry, "warnings")
			case "missing-errors":
				delete(entry, "errors")
			case "wrong-cwd":
				entry["cwd"] = "/other"
			case "missing-enabled":
				delete(hook, "enabled")
			case "missing-managed":
				delete(hook, "isManaged")
			case "missing-timeout":
				delete(hook, "timeoutSec")
			case "missing-async":
				delete(hook, "async")
			case "invalid-hash":
				hook["currentHash"] = "sha256:PRIVATE_BAD_HASH"
			case "relative-path":
				hook["sourcePath"] = "relative/hooks.json"
			case "duplicate-key":
				entry["hooks"] = []any{hook, hook}
			case "wrong-backend":
				scope.Instance.Kind = "claude"
			case "probe":
				scope.Purpose = "probe"
			case "query-purpose":
				scope.Purpose = "future-query"
			}
			f.call = func(method string, params map[string]any) (any, error) {
				if method != "hooks/list" || !reflect.DeepEqual(params, map[string]any{"cwds": []string{scope.Cwd}}) {
					t.Fatal("unexpected mutation or scope")
				}
				if mode == "rpc-error" {
					return nil, errors.New("PRIVATE_ERROR")
				}
				data := []any{entry}
				if mode == "duplicate-cwd" {
					data = append(data, entry)
				}
				return map[string]any{"data": data}, nil
			}
			out, err := c.ReadHookCatalog(context.Background(), scope)
			valid := mode == "empty" || mode == "unknown-source-trust" || mode == "disabled"
			if (err == nil) != valid {
				t.Fatalf("unexpected catalog acceptance: %+v %v", out, err)
			}
			if mode == "unknown-source-trust" && (out.Items[0].Source != "future-source" || out.Items[0].TrustStatus != "future-trust") {
				t.Fatal("unknown native category was guessed")
			}
			if mode == "disabled" && !out.Items[0].Disabled {
				t.Fatal("disabled hook reported enabled")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "PRIVATE_") || err != nil && strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("private native fields leaked")
			}
			if len(f.replies) != 0 || len(f.methods) > 1 {
				t.Fatal("catalog executed work")
			}
		})
	}
}
