package runtime

import (
	"encoding/json"
	"os"
	"testing"
)

func TestClaudePluginMCPUsageKeepsSelectedSourceAndSuccessfulHistory(t *testing.T) {
	views := map[string]View{}
	for _, mode := range []string{"success", "error", "pending", "offline", "normalized-collision"} {
		t.Run(mode, func(t *testing.T) {
			scope := Scope{NativeID: "original", PluginSelections: []PluginSelection{{AssetID: "plugin", Name: "fixture", Mode: "enabled", Version: "1", SourcePath: "/plugin", Components: []PluginComponent{{Kind: "mcp", NativeName: "plugin:fixture:identity", Count: 1}}}}}
			var s claudeSnapshot
			if err := json.Unmarshal([]byte(`{"live":true,"entries":[{"kind":"initialized"},{"kind":"event","operation":"turn","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call","name":"mcp__plugin_fixture_identity__whoami"}]}}}],"plugins":{"available":true,"at":"2026-10-03T00:00:00Z","packages":[{"name":"fixture","version":"1","path":"/plugin","source":"fixture@inline"}],"skills":{"available":true,"commands":[],"descriptions":[]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[{"name":"plugin:fixture:identity","status":"connected","source":"plugin","tools":["whoami"]}]}}}`), &s); err != nil {
				t.Fatal(err)
			}
			if mode != "pending" {
				result := `[{"type":"tool_result","tool_use_id":"call","is_error":false,"content":[{"type":"text","text":"identity"}]}]`
				if mode == "error" {
					result = `[{"type":"tool_result","tool_use_id":"call","is_error":true}]`
				}
				s.Entries = append(s.Entries, claudeEntry{Kind: "event", Operation: "turn", Event: claudeEvent{Type: "user"}})
				s.Entries[len(s.Entries)-1].Event.Message.Content = json.RawMessage(result)
			}
			if mode == "offline" {
				s.Live = false
				s.Plugins = claudePluginInventory{}
			}
			if mode == "normalized-collision" {
				s.Plugins.MCP.Servers = append(s.Plugins.MCP.Servers, claudeMCPStatus{Name: "plugin_fixture_identity", Status: "connected", Source: "project", Tools: []string{"whoami"}})
			}
			view := projectClaude(scope, s)
			views[mode] = view
			if len(view.CapabilityEvidence) == 0 {
				t.Fatal("plugin MCP usage is absent from the public view")
			}
			item := view.CapabilityEvidence[0]
			if item.AssetID != "plugin" || item.Server != "plugin:fixture:identity" || item.Mode != "enabled" {
				t.Fatalf("plugin binding classified as inherited: %+v", item)
			}
			if mode == "normalized-collision" && (item.Verified || view.ExecutionClear) {
				t.Fatal("namespace collision admitted input")
			}
			wantUsage := mode == "success" || mode == "offline"
			if (len(item.UsedTools) == 1 && item.UsedTools[0] == "whoami") != wantUsage {
				t.Fatalf("wrong usage attribution: %+v", item)
			}
			if mode == "offline" && item.Verified {
				t.Fatal("historical use became current connection proof")
			}
			if mode == "success" && (!item.Verified || item.Source != "plugin") {
				t.Fatal("selected plugin source not verified")
			}
		})
	}
	if path := os.Getenv("NETCAP_PLUGIN_USAGE_EVIDENCE"); path != "" {
		data, err := json.MarshalIndent(views, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
