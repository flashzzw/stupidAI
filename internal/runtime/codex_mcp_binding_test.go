package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/mcpstdio"
)

func TestCodexMCPBoundLaunchIsStableAcrossCreationAndResume(t *testing.T) {
	scope := mcpDefinitionScope(t, `{"command":"python3","args":["scripts/server.py","${TOKEN}"],"env":{"SECRET":"${TOKEN}"}}`)
	scope.SessionID, scope.StateNamespace = "session-owned", "state-owned"
	scope.NativeID = ""
	scope.MCPSelections[0].Definition.Version = 2
	scope.MCPSelections[0].Definition.AssetVersion = "v2"
	scope.MCPSelections[0].Definition.SourceHash = strings.Repeat("b", 64)
	f := &mcpEnvironmentFixture{nativeFixture: &nativeFixture{call: emptyMCPConfig}, values: map[string]string{"TOKEN": "owned-sensitive-value"}}
	selections, configs, err := codexMCPDefinitionConfigs(context.Background(), f, scope, true)
	if err != nil {
		t.Fatal(err)
	}
	binding := selections[0].Definition.Binding
	if binding == nil || !binding.Stamp.Valid() || binding.AssetID != "shared" || binding.AssetVersion != "v2" || binding.Server != "identity" || !reflect.DeepEqual(binding.Environment, []string{"SECRET"}) {
		t.Fatal("expanded launch lacks its source identity")
	}
	args := configs["identity"]["args"].([]string)
	if len(args) != 6 || args[0] != codexMCPStdioCommand || args[2] != "--" || args[3] != "python3" || args[5] != "owned-sensitive-value" {
		t.Fatal("native launch did not use the bound stdio helper")
	}
	var delivered mcpstdio.Binding
	if json.Unmarshal([]byte(args[1]), &delivered) != nil || !reflect.DeepEqual(delivered, *binding) {
		t.Fatal("helper received different public launch metadata")
	}
	metadata, _ := json.Marshal(selections)
	if strings.Contains(string(metadata), "owned-sensitive-value") || strings.Contains(string(metadata), "python3") {
		t.Fatal("expanded process inputs were persisted")
	}
	scope.MCPSelections = selections
	scope.NativeID = "native-created-later"
	_, resumed, err := codexMCPDefinitionConfigs(context.Background(), f, scope, false)
	if err != nil || !reflect.DeepEqual(configs, resumed) {
		t.Fatalf("assigning a native ID changed the bound launch: %v", err)
	}
	for _, change := range []string{"session", "namespace", "asset-version", "tree", "digest", "missing-binding"} {
		t.Run(change, func(t *testing.T) {
			raw, _ := json.Marshal(scope)
			var changed Scope
			_ = json.Unmarshal(raw, &changed)
			switch change {
			case "session":
				changed.SessionID += "-other"
			case "namespace":
				changed.StateNamespace += "-other"
			case "asset-version":
				changed.MCPSelections[0].Definition.AssetVersion = "v3"
			case "tree":
				changed.MCPSelections[0].Definition.SourceHash = strings.Repeat("c", 64)
			case "digest":
				changed.MCPSelections[0].Definition.Binding.Digest = strings.Repeat("d", 64)
			case "missing-binding":
				changed.MCPSelections[0].Definition.Binding = nil
			}
			if _, _, err := codexMCPDefinitionConfigs(context.Background(), f, changed, false); err == nil {
				t.Fatal("a changed launch silently inherited the previous version")
			}
		})
	}
}

func TestCodexMCPActualBindingControlsLoadingAndHistoricalAttribution(t *testing.T) {
	stamp := mcpstdio.Stamp{Version: 1, Digest: strings.Repeat("a", 64), AssetID: "shared", AssetVersion: "v2", Server: "identity"}
	old := stamp
	old.Digest, old.AssetVersion = strings.Repeat("b", 64), "v1"
	for _, mode := range []string{"current", "old-process", "missing", "invalid", "wrong-server", "wrong-asset", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			scope := mcpScope(t)
			scope.MCPSelections[0].AssetID = "shared"
			scope.MCPSelections[0].Definition = &MCPDefinition{Version: 2, AssetVersion: "v2", Binding: &mcpstdio.Binding{Stamp: stamp}}
			actual := stamp
			status := "connected"
			tools := map[string]any{"whoami": map[string]any{}}
			switch mode {
			case "old-process":
				actual = old
			case "invalid":
				actual.Digest = "invalid"
			case "wrong-server":
				actual.Server = "other"
			case "wrong-asset":
				actual.AssetID = "other"
			case "disabled":
				scope.MCPSelections[0].Mode, status, tools = "disabled", "disabled", map[string]any{}
			}
			row := map[string]any{"name": "identity", "runtimeStatus": status, "pluginId": nil, "tools": tools}
			if mode != "missing" && mode != "disabled" {
				row["serverCapabilities"] = map[string]any{"experimental": map[string]any{mcpstdio.MetadataKey: actual}}
			}
			f := &nativeFixture{call: func(method string, params map[string]any) (any, error) {
				if method != "mcpServerStatus/list" || params["threadId"] != scope.NativeID {
					t.Fatal("unexpected native operation")
				}
				return map[string]any{"data": []any{row}, "nextCursor": ""}, nil
			}}
			call := func(tool string, binding *mcpstdio.Stamp) codex.Item {
				result := map[string]any{"content": []any{}}
				if binding != nil {
					result["_meta"] = map[string]any{mcpstdio.MetadataKey: binding}
				}
				body, _ := json.Marshal(result)
				return codex.Item{Type: "mcpToolCall", Server: "identity", Tool: tool, Status: "completed", PluginID: json.RawMessage(`null`), MCPResult: body}
			}
			turns := []codex.Turn{{Items: []codex.Item{call("old-call", &old), call("new-call", &stamp), call("legacy-call", nil)}}}
			evidence, matched := codexMCPEvidence(context.Background(), f, scope, turns)
			want := mode == "current" || mode == "disabled"
			if matched != want || len(evidence) < 3 || evidence[0].Verified != want {
				t.Fatalf("same-name inventory was mistaken for a loaded version: %+v", evidence)
			}
			if want && !reflect.DeepEqual(evidence[0].UsedTools, []string{"new-call"}) || !want && len(evidence[0].UsedTools) != 0 {
				t.Fatal("old or unmarked calls were attributed to the current selection")
			}
			oldFound, legacyFound := false, false
			for _, row := range evidence[1:] {
				if row.Mode != "history" {
					t.Fatal("previous calls were reported as current loading")
				}
				if reflect.DeepEqual(row.UsedTools, []string{"old-call"}) && row.Binding != nil && *row.Binding == old {
					oldFound = true
				}
				if reflect.DeepEqual(row.UsedTools, []string{"legacy-call"}) && row.Binding == nil {
					legacyFound = true
				}
			}
			if !oldFound || !legacyFound {
				t.Fatal("historical versions were lost or merged")
			}
		})
	}
}

func TestCodexMCPReleaseRequiresAbsenceEvenWithNoRemainingSelections(t *testing.T) {
	for _, mode := range []string{"absent", "stale-process", "incomplete", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			original := mcpScope(t)
			original.MCPSelections[0].Definition = &MCPDefinition{Version: 2}
			target := original
			target.MCPSelections = nil
			f := &nativeFixture{call: func(method string, params map[string]any) (any, error) {
				if method != "mcpServerStatus/list" || params["threadId"] != target.NativeID {
					t.Fatal("release used a global or mutating check")
				}
				data := []any{map[string]any{"name": "keep", "pluginId": nil, "runtimeStatus": "connected", "tools": map[string]any{}}}
				switch mode {
				case "stale-process":
					data = append(data, map[string]any{"name": "identity", "pluginId": nil, "runtimeStatus": "connected", "tools": map[string]any{}})
				case "incomplete":
					return map[string]any{}, nil
				case "duplicate":
					data = append(data, data[0])
				}
				return map[string]any{"data": data, "nextCursor": ""}, nil
			}}
			if got := codexRetiredPluginsAbsent(context.Background(), f, Intent{Scope: target, PreviousScope: &original}); got != (mode == "absent") {
				t.Fatal("empty target was treated as proof that the shared MCP was removed")
			}
		})
	}
}

func TestCodexHistoricalMCPVersionsPermitFreshPermissionCheckAndOneNewInput(t *testing.T) {
	c, f, scope := nativeHarness(t)
	old := mcpstdio.Stamp{Version: 1, Digest: strings.Repeat("a", 64), AssetID: "shared", AssetVersion: "v1", Server: "identity"}
	result, _ := json.Marshal(map[string]any{"content": []any{}, "_meta": map[string]any{mcpstdio.MetadataKey: old}})
	turns := []codex.Turn{{ID: "old-turn", Status: "completed", Items: []codex.Item{{Type: "mcpToolCall", Server: "identity", Tool: "whoami", PluginID: json.RawMessage(`null`), Status: "completed", MCPResult: result}}}}
	resumes, inputs := 0, 0
	f.call = func(method string, params map[string]any) (any, error) {
		thread := map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "status": map[string]string{"type": "idle"}, "turns": turns}
		switch method {
		case "thread/read":
			return map[string]any{"thread": thread}, nil
		case "thread/turns/list":
			return map[string]any{"data": turns}, nil
		case "mcpServerStatus/list":
			return map[string]any{"data": []any{}, "nextCursor": nil}, nil
		case "thread/resume":
			resumes++
			if len(params) != 2 {
				t.Fatal("permission sampling overwrote the loaded configuration")
			}
			return map[string]any{"thread": thread, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "approvalPolicy": "on-request", "sandbox": map[string]any{"type": "readOnly"}}, nil
		case "turn/start":
			inputs++
			if params["clientUserMessageId"] != "new-input" {
				t.Fatal("historical input was replayed")
			}
			return map[string]any{"turn": map[string]any{"id": "new-turn"}}, nil
		}
		return emptyControl(method)
	}
	receipt, err := c.Execute(context.Background(), Intent{ID: "new-input", Kind: "send", Scope: scope, Permission: "read_only", Text: "new work"})
	if err != nil || receipt.NativeTurn != "new-turn" || resumes != 1 || inputs != 1 {
		t.Fatalf("historical usage blocked current permission verification: resumes=%d inputs=%d err=%v", resumes, inputs, err)
	}
}
