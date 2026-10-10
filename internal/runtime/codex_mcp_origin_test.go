package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex-netcap/internal/codex"
)

func codexMCPOriginHarness(t *testing.T, rows []any, items []codex.Item, inventoryError ...error) (*Codex, Scope) {
	t.Helper()
	c, f, _ := nativeHarness(t)
	scope := mcpScope(t)
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "thread/read":
			turns := []codex.Turn{}
			if params["includeTurns"] == true {
				turns = []codex.Turn{{ID: "previous", Status: "completed", Items: items}}
			}
			return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "status": map[string]string{"type": "idle"}, "turns": turns}}, nil
		case "thread/turns/list":
			return map[string]any{"data": []codex.Turn{{ID: "previous", Status: "completed", Items: items}}}, nil
		case "mcpServerStatus/list":
			if len(inventoryError) > 0 {
				return nil, inventoryError[0]
			}
			return map[string]any{"data": rows, "nextCursor": nil}, nil
		default:
			return emptyControl(method)
		}
	}
	return c, scope
}

func TestCodexMCPOriginRequiresReportedProvenance(t *testing.T) {
	for _, mode := range []string{"configuration", "absent", "empty", "invalid-type", "nul", "too-long"} {
		t.Run(mode, func(t *testing.T) {
			row := map[string]any{"name": "identity", "runtimeStatus": "connected", "tools": map[string]any{}, "pluginId": nil}
			switch mode {
			case "absent":
				delete(row, "pluginId")
			case "empty":
				row["pluginId"] = ""
			case "invalid-type":
				row["pluginId"] = map[string]any{}
			case "nul":
				row["pluginId"] = "a\x00@b"
			case "too-long":
				row["pluginId"] = strings.Repeat("a", 1201)
			}
			c, scope := codexMCPOriginHarness(t, []any{row}, nil)
			v, err := c.Observe(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			want := mode == "configuration"
			if v.ExecutionClear != want || len(v.CapabilityEvidence) != 1 || v.CapabilityEvidence[0].Verified != want {
				t.Fatalf("source presence did not gate binding: %+v", v.CapabilityEvidence)
			}
			if want && v.CapabilityEvidence[0].Source != "configuration" {
				t.Fatal("null plugin ID misclassified as a particular config layer")
			}
			if !want && v.CapabilityEvidence[0].Source != "unknown" {
				t.Fatal("missing or invalid source was reclassified")
			}
		})
	}
}

func TestCodexMCPOriginRejectsPluginAsSelectedUserDefinition(t *testing.T) {
	c, scope := codexMCPOriginHarness(t, []any{map[string]any{"name": "identity", "runtimeStatus": "connected", "pluginId": "fixture@owned", "tools": map[string]any{"whoami": map[string]any{}}}}, nil)
	v, err := c.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if v.ExecutionClear || len(v.CapabilityEvidence) != 1 || v.CapabilityEvidence[0].Verified {
		t.Fatal("plugin server credited to selected user MCP definition")
	}
	b, _ := json.Marshal(v.CapabilityEvidence)
	var rows []map[string]any
	_ = json.Unmarshal(b, &rows)
	if rows[0]["plugin_id"] != "fixture@owned" || rows[0]["source"] != "plugin" {
		t.Fatal("full native plugin identity was discarded")
	}
}

func TestCodexMCPOriginKeepsHistoricalSourcesSeparate(t *testing.T) {
	var items []codex.Item
	if err := json.Unmarshal([]byte(`[
	{"type":"mcpToolCall","server":"identity","tool":"old_plugin_tool","pluginId":"fixture@old","status":"completed","result":{"content":[]}},
	{"type":"mcpToolCall","server":"identity","tool":"user_tool","pluginId":null,"status":"completed","result":{"content":[]}},
	{"type":"mcpToolCall","server":"identity","tool":"legacy_tool","status":"completed","result":{"content":[]}},
	{"type":"mcpToolCall","server":"plugin_server","tool":"current_plugin_tool","pluginId":"fixture@owned","status":"completed","result":{"content":[]}},
	{"type":"mcpToolCall","server":"plugin_server","tool":"other_plugin_tool","pluginId":"other@market","status":"completed","result":{"content":[]}}
	]`), &items); err != nil {
		t.Fatal(err)
	}
	rows := []any{map[string]any{"name": "identity", "runtimeStatus": "connected", "pluginId": nil, "tools": map[string]any{}}, map[string]any{"name": "plugin_server", "runtimeStatus": "connected", "pluginId": "fixture@owned", "tools": map[string]any{}}}
	c, scope := codexMCPOriginHarness(t, rows, items)
	v, err := c.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !v.ExecutionClear || len(v.CapabilityEvidence) != 5 {
		t.Fatalf("historical origins lost: %+v", v.CapabilityEvidence)
	}
	for _, e := range v.CapabilityEvidence {
		if len(e.UsedTools) != 1 {
			t.Fatalf("origin usage mixed: %+v", e)
		}
		switch e.UsedTools[0] {
		case "user_tool":
			if e.AssetID != "asset" || !e.Verified || e.Source != "configuration" {
				t.Fatal("known configuration usage lost")
			}
		case "current_plugin_tool":
			if e.Mode != "inherit" || e.PluginID != "fixture@owned" || e.AssetID != "" {
				t.Fatal("current plugin identity lost")
			}
		case "legacy_tool":
			if e.Mode != "history" || e.Source != "unknown" || e.AssetID != "" {
				t.Fatal("legacy origin guessed")
			}
		case "old_plugin_tool":
			if e.Mode != "history" || e.PluginID != "fixture@old" || e.AssetID != "" {
				t.Fatal("old plugin usage credited to user asset")
			}
		case "other_plugin_tool":
			if e.Mode != "history" || e.PluginID != "other@market" || e.AssetID != "" {
				t.Fatal("same server name collapsed two plugins")
			}
		default:
			t.Fatal("unexpected usage")
		}
	}
}

func TestCodexMCPOriginKeepsHistoryAfterLastSelectionReleased(t *testing.T) {
	var items []codex.Item
	if err := json.Unmarshal([]byte(`[{"type":"mcpToolCall","server":"identity","tool":"old_plugin_tool","pluginId":"fixture@old","status":"completed","result":{"content":[]}}]`), &items); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{nil, errors.New("inventory unavailable")} {
		c, scope := codexMCPOriginHarness(t, nil, items, err)
		scope.MCPSelections = nil
		v, err := c.Observe(context.Background(), scope)
		if err != nil {
			t.Fatal(err)
		}
		if !v.ExecutionClear || len(v.CapabilityEvidence) != 1 {
			t.Fatalf("released selection lost native history: %+v", v.CapabilityEvidence)
		}
		row := v.CapabilityEvidence[0]
		if row.Mode != "history" || row.PluginID != "fixture@old" || row.AssetID != "" || row.Verified || len(row.UsedTools) != 1 || row.UsedTools[0] != "old_plugin_tool" {
			t.Fatalf("history recategorized as current selection: %+v", row)
		}
	}

}
