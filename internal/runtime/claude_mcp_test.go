package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeMCPInventoryControlsAdmissionAndPreservesInheritance(t *testing.T) {
	scope := Scope{NativeID: "original", MCPSelections: []MCPSelection{{AssetID: "asset", Server: "identity", Mode: "enabled", SourcePath: "/fixture/mcp.json"}}}
	for _, tc := range []struct {
		name, mode, status, source string
		available, live, want      bool
	}{
		{"connected", "enabled", "connected", "dynamic", true, true, true},
		{"untrusted_origin", "enabled", "connected", "plugin", true, true, false},
		{"failed", "enabled", "failed", "dynamic", true, true, false},
		{"oauth_required", "enabled", "needs-auth", "dynamic", true, true, false},
		{"absent_disabled", "disabled", "", "", true, true, true},
		{"inherited_collision", "disabled", "connected", "project", true, true, false},
		{"unknown", "disabled", "", "", false, true, false},
		{"offline_keeps_native_unknown", "enabled", "", "", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope.MCPSelections[0].Mode = tc.mode
			s := claudeSnapshot{Live: tc.live, Entries: []claudeEntry{{Kind: "initialized"}}, MCP: claudeMCPInventory{Available: tc.available, At: "2026-10-01T00:00:00Z", Servers: []claudeMCPStatus{{Name: "inherited", Status: "connected", Source: "project", Tools: []string{"extra"}}}}}
			if tc.status != "" {
				s.MCP.Servers = append(s.MCP.Servers, claudeMCPStatus{Name: "identity", Status: tc.status, Source: tc.source, Tools: []string{"whoami"}})
			}
			view := projectClaude(scope, s)
			if len(view.CapabilityEvidence) != 2 || view.CapabilityEvidence[0].Verified != tc.want || view.CapabilityEvidence[1].Mode != "inherit" {
				t.Fatalf("inventory not respected: %+v", view.CapabilityEvidence)
			}
			if tc.live && view.ExecutionClear != tc.want {
				t.Fatal("live MCP mismatch admitted input")
			}
			if tc.status == "needs-auth" && !strings.Contains(view.CapabilityEvidence[0].Detail, "OAuth") {
				t.Fatal("missing OAuth reason for blocked admission")
			}
		})
	}
}
func TestClaudeMCPConflictDiagnosisReportsObservedSourceWithoutGrantingInput(t *testing.T) {
	for _, tc := range []struct {
		name, mode, source, detail string
		servers                    []claudeMCPStatus
	}{
		{"missing", "enabled", "", "未出现在", nil},
		{"failed", "enabled", "dynamic", "连接失败", []claudeMCPStatus{{Name: "identity", Status: "failed", Source: "dynamic"}}},
		{"disable_collision", "disabled", "local", "无法确认本次禁用", []claudeMCPStatus{{Name: "identity", Status: "connected", Source: "local"}}},
		{"wrong_origin", "enabled", "project", "与本次动态选择不一致", []claudeMCPStatus{{Name: "identity", Status: "connected", Source: "project"}}},
		{"duplicate", "enabled", "unknown", "重复", []claudeMCPStatus{{Name: "identity", Status: "connected", Source: "dynamic"}, {Name: "identity", Status: "connected", Source: "user"}}},
		{"unknown_origin", "enabled", "unknown", "未知来源", []claudeMCPStatus{{Name: "identity", Status: "connected", Source: "private-value"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := Scope{NativeID: "original", MCPSelections: []MCPSelection{{AssetID: "asset", Server: "identity", Mode: tc.mode}}}
			s := claudeSnapshot{Live: true, Entries: []claudeEntry{{Kind: "initialized"}}, MCP: claudeMCPInventory{Available: true, Servers: tc.servers}}
			s.Config.ID = scope.NativeID
			v := projectClaude(scope, s)
			if v.ExecutionClear || v.CapabilityEvidence[0].Verified {
				t.Fatal("source diagnostic granted admission")
			}
			item := v.CapabilityEvidence[0]
			if item.Source != tc.source || !strings.Contains(item.Detail, tc.detail) {
				t.Fatalf("wrong conflict diagnosis: %+v", item)
			}
			data, _ := json.Marshal(item)
			if strings.Contains(string(data), "private-value") {
				t.Fatal("unrecognized native source escaped whitelist")
			}
		})
	}
}

func TestClaudeMCPUsageRequiresSuccessfulNativeToolResult(t *testing.T) {
	event := func(body string) claudeEntry {
		e := claudeEntry{Kind: "event"}
		e.Event.Message.Content = json.RawMessage(body)
		return e
	}
	entries := []claudeEntry{event(`[{"type":"tool_use","id":"denied","name":"mcp__identity__denied"},{"type":"tool_use","id":"ok","name":"mcp__identity__whoami"},{"type":"tool_use","id":"pending","name":"mcp__identity__not_completed"}]`), event(`[{"type":"tool_result","tool_use_id":"denied","is_error":true},{"type":"tool_result","tool_use_id":"ok","content":"value"}]`)}
	used := claudeMCPUsage(entries, []claudeMCPStatus{{Name: "identity"}})
	if len(used["identity"]) != 1 || used["identity"][0] != "whoami" {
		t.Fatalf("attempt/denial was counted as success: %+v", used)
	}
}

func TestClaudeMCPArrayResultCanBeObserved(t *testing.T) {
	// The native MCP result carries an array; Bash uses an object for task
	// metadata. Both travel through this same SDK snapshot boundary.
	var s claudeSnapshot
	err := json.Unmarshal([]byte(`{"live":true,"entries":[{"kind":"initialized"},{"kind":"input","operation":"turn","text":"call"},{"kind":"event","operation":"turn","event":{"type":"assistant","uuid":"call","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool","name":"mcp__identity__whoami","input":{}}]}}},{"kind":"event","operation":"turn","event":{"type":"user","uuid":"result","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool","content":[{"type":"text","text":"fixture result"}]}]},"tool_use_result":[{"type":"text","text":"fixture result"}]}},{"kind":"event","operation":"turn","event":{"type":"result","subtype":"success","queued_turn_count":0}}]}`), &s)
	if err != nil {
		t.Fatal(err)
	}
	var input claudeEvent
	if err := json.Unmarshal([]byte(`{"type":"user","uuid":"turn","message":{"role":"user","content":"call"}}`), &input); err != nil {
		t.Fatal(err)
	}
	s.History = []claudeEvent{input}
	view := projectClaude(Scope{NativeID: "original"}, s)
	used := claudeMCPUsage(s.Entries, []claudeMCPStatus{{Name: "identity"}})
	if view.Status != "idle" || !view.ExecutionClear || len(used["identity"]) != 1 || used["identity"][0] != "whoami" || len(view.Messages) != 3 || view.Messages[2].Text != "fixture result" {
		t.Fatalf("native MCP result lost: %+v", view)
	}
}
