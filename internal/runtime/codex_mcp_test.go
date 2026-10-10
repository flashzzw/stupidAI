package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex-netcap/internal/codex"
)

func mcpScope(t *testing.T) Scope {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcp_servers.identity]\ncommand='fixture'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Scope{Instance: Instance{ID: "fixture", Kind: "codex", Profile: filepath.Dir(path)}, NativeID: "native", Cwd: "/fixture", MCPSelections: []MCPSelection{{AssetID: "asset", Server: "identity", Mode: "enabled", SourcePath: path}}}
}

func mcpConfig(scope Scope, layerType string, file string, server map[string]any) map[string]any {
	servers := map[string]any{"identity": server}
	return map[string]any{"config": map[string]any{"mcp_servers": servers}, "layers": []any{map[string]any{"name": map[string]any{"type": layerType, "file": file}, "config": map[string]any{"mcp_servers": servers}}}}
}

func TestMCPOverridesRequireRegisteredUserLayerAndOnlyToggleSelectedServer(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "missing-layer", "other-layer", "other-source", "duplicate-layer", "remote", "unknown-server", "no-selection"} {
		t.Run(mode, func(t *testing.T) {
			scope := mcpScope(t)
			layer, path := "user", scope.MCPSelections[0].SourcePath
			server := map[string]any{"command": "fixture", "environment_id": "local"}
			switch mode {
			case "disabled":
				scope.MCPSelections[0].Mode = "disabled"
			case "other-layer":
				layer = "project"
			case "other-source":
				path = filepath.Join(t.TempDir(), "other.toml")
				_ = os.WriteFile(path, []byte("x"), 0600)
			case "remote":
				server = map[string]any{"url": "https://example.invalid/mcp"}
			case "unknown-server":
				scope.MCPSelections[0].Server = "missing"
			case "no-selection":
				scope.MCPSelections = nil
			}
			config := mcpConfig(scope, layer, path, server)
			if mode == "missing-layer" {
				delete(config, "layers")
			}
			if mode == "duplicate-layer" {
				config["layers"] = append(config["layers"].([]any), config["layers"].([]any)...)
			}
			f := &nativeFixture{call: func(method string, params map[string]any) (any, error) {
				if method != "config/read" || params["includeLayers"] != true || params["cwd"] != scope.Cwd {
					t.Fatalf("wrong source query: %s %+v", method, params)
				}
				return config, nil
			}}
			overrides := map[string]any{"unrelated": true}
			err := addCodexMCPOverrides(context.Background(), f, scope, overrides)
			if mode == "enabled" || mode == "disabled" {
				if err != nil || len(overrides) != 2 || overrides["mcp_servers.identity.enabled"] != (mode == "enabled") {
					t.Fatalf("override: %+v %v", overrides, err)
				}
			} else if mode == "no-selection" {
				if err != nil || len(f.methods) != 0 || len(overrides) != 1 {
					t.Fatal("reference-only path changed")
				}
			} else if err == nil {
				t.Fatal("unaccounted native config accepted")
			}
		})
	}
}

func TestMCPEvidenceNeedsCompleteThreadInventory(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "absent", "wrong-status", "disabled-tools", "error", "duplicate", "cycle", "page-error", "tools-error"} {
		t.Run(mode, func(t *testing.T) {
			scope := mcpScope(t)
			target := map[string]any{"name": "identity", "runtimeStatus": "connected", "pluginId": nil, "tools": map[string]any{"whoami": map[string]any{}}}
			switch mode {
			case "disabled":
				scope.MCPSelections[0].Mode = "disabled"
				target["runtimeStatus"] = "disabled"
				target["tools"] = map[string]any{}
			case "disabled-tools":
				scope.MCPSelections[0].Mode = "disabled"
				target["runtimeStatus"] = "disabled"
			case "wrong-status":
				target["runtimeStatus"] = "failed"
			case "tools-error":
				target["toolsError"] = "cannot load tools"
			}
			calls := 0
			f := &nativeFixture{call: func(method string, params map[string]any) (any, error) {
				calls++
				if method != "mcpServerStatus/list" || params["threadId"] != "native" {
					t.Fatalf("global inventory used: %s %+v", method, params)
				}
				if mode == "error" || (mode == "page-error" && calls > 1) {
					return nil, errors.New("unavailable")
				}
				if calls == 1 {
					return map[string]any{"data": []any{map[string]any{"name": "inherited", "runtimeStatus": "connected", "pluginId": nil, "tools": map[string]any{"other": map[string]any{}}}}, "nextCursor": "next"}, nil
				}
				if params["cursor"] != "next" {
					t.Fatal("lost pagination")
				}
				data := []any{target}
				if mode == "absent" {
					data = nil
				}
				if mode == "duplicate" {
					data = append(data, target)
				}
				next := ""
				if mode == "cycle" {
					next = "next"
				}
				return map[string]any{"data": data, "nextCursor": next}, nil
			}}
			turns := []codex.Turn{{Items: []codex.Item{{Type: "mcpToolCall", Server: "identity", Tool: "whoami", Status: "completed", PluginID: json.RawMessage(`null`), MCPResult: json.RawMessage(`{"content":[]}`)}}}}
			evidence, matched := codexMCPEvidence(context.Background(), f, scope, turns)
			want := mode == "enabled" || mode == "disabled"
			if matched != want || len(evidence) == 0 || evidence[0].Verified != want {
				t.Fatalf("incomplete inventory accepted: %+v %v", evidence, matched)
			}
			if want && (len(evidence) != 2 || evidence[1].Mode != "inherit" || evidence[1].Verified || !reflect.DeepEqual(evidence[0].UsedTools, []string{"whoami"})) {
				t.Fatalf("selection/history mixed: %+v", evidence)
			}
		})
	}
}

func TestMCPMismatchBlocksNativeSendAndReply(t *testing.T) {
	for _, kind := range []string{"send", "respond"} {
		t.Run(kind, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			scope := mcpScope(t)
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					return mcpConfig(scope, "user", scope.MCPSelections[0].SourcePath, map[string]any{"command": "fixture"}), nil
				case "thread/read":
					return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": "idle"}, "turns": []any{}}}, nil
				case "mcpServerStatus/list":
					return map[string]any{"data": []any{map[string]any{"name": "identity", "runtimeStatus": "failed"}}}, nil
				case "turn/start":
					t.Fatal("sent before native selection matched")
				}
				return emptyControl(method)
			}
			_, err := c.Execute(context.Background(), Intent{Kind: kind, Scope: scope, Text: "work"})
			var notSent *NotSent
			if !errors.As(err, &notSent) || len(f.replies) > 0 {
				t.Fatalf("mismatch not blocked: %v", err)
			}
		})
	}
}

func TestMCPSingleCallApprovalUsesNativeActionAndExpires(t *testing.T) {
	for n, action := range []string{"accept", "decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			request := codex.Message{ID: json.RawMessage(`7`), Method: "mcpServer/elicitation/request", Params: json.RawMessage(`{"threadId":"native","turnId":"turn","mode":"form","message":"Allow identity.whoami?","_meta":{"codex_approval_kind":"mcp_tool_call","tool_params":{}},"requestedSchema":{"type":"object","properties":{}}}`)}
			f.requests["7"] = request
			f.events = []codex.Message{request}
			c.collect(scope.Instance, f)
			pending := c.pending["fixture/connection-1/7"]
			f.call = func(method string, _ map[string]any) (any, error) {
				if method != "thread/read" {
					return nil, fmt.Errorf("unexpected %s", method)
				}
				return map[string]any{"thread": map[string]any{"id": "native"}}, nil
			}
			in := Intent{Kind: "respond", Scope: scope, Pending: &pending, OptionID: fmt.Sprintf("decision-%d", n)}
			if _, err := c.Execute(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(f.replies[0])
			var reply map[string]any
			_ = json.Unmarshal(body, &reply)
			if reply["action"] != action || reply["decision"] != nil || len(pending.Options) != 3 {
				t.Fatalf("wrong native approval: %s", body)
			}
			if action == "accept" && !reflect.DeepEqual(reply["content"], map[string]any{}) {
				t.Fatal("accept omitted form content")
			}
			if _, err := c.Execute(context.Background(), in); err == nil || len(f.replies) != 1 {
				t.Fatal("expired request replied twice")
			}
		})
	}
	for _, schema := range []string{`{"type":"object","properties":{"secret":{"type":"string"}}}`, `{"type":"object","properties":null}`, `{"type":"object","properties":{},"required":["x"]}`} {
		request := codex.Message{Method: "mcpServer/elicitation/request", Params: json.RawMessage(`{"mode":"form","_meta":{"codex_approval_kind":"mcp_tool_call"},"requestedSchema":` + schema + `}`)}
		if pendingFromRequest(Instance{}, "", request).ResponseKind != "unsupported" {
			t.Fatal("arbitrary elicitation silently accepted")
		}
	}
}

func TestMCPEvidenceDoesNotCountRejectedOrFailedCallsAsSuccess(t *testing.T) {
	scope := mcpScope(t)
	f := &nativeFixture{call: func(string, map[string]any) (any, error) {
		return map[string]any{"data": []any{map[string]any{"name": "identity", "runtimeStatus": "connected", "pluginId": nil, "tools": map[string]any{}}}}, nil
	}}
	items := []codex.Item{}
	for _, result := range []string{`null`, `{"isError":true,"content":[]}`, `"invalid"`, ``} {
		items = append(items, codex.Item{Type: "mcpToolCall", Server: "identity", Tool: "whoami", Status: "completed", MCPResult: json.RawMessage(result)})
	}
	items = append(items, codex.Item{Type: "mcpToolCall", Server: "identity", Tool: "whoami", Status: "failed", MCPResult: json.RawMessage(`{}`)})
	evidence, ok := codexMCPEvidence(context.Background(), f, scope, []codex.Turn{{Items: items}})
	if !ok || len(evidence[0].UsedTools) > 0 {
		t.Fatal("unsuccessful native call counted as success")
	}
}
