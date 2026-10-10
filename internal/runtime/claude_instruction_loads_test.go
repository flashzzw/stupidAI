package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func instructionLoadFixture() (Scope, claudeSnapshot) {
	scope := Scope{NativeID: "f94d7cef-6651-4002-957f-3b6e840b6961", Cwd: "/owned"}
	query := "f4c4e3d3-769d-48a4-bf35-93a7fd44d8d1"
	first, second := "011be0a1-a1f1-4563-b0a9-1dd5c8364e6d", "97d2d43a-beb0-41ae-8d9e-e086f361061c"
	prompt := "a4915e10-f0c5-432d-8e6b-52a99de39756"
	failed := false
	s := claudeSnapshot{Live: true, Active: second, InstructionObserver: &claudeInstructionObserver{Version: 1, RecordingFailed: &failed, CurrentQueryID: query, CurrentConfigurationID: strings.Repeat("c", 64)},
		Entries: []claudeEntry{{Kind: "initialized"}, {Kind: "input", QueryID: query, Operation: first}, {Kind: "input", QueryID: query, Operation: second},
			{Kind: "instruction_load", InstructionLoad: &claudeInstructionLoad{Version: 1, InstructionLoad: InstructionLoad{
				ID: "e10f2c47-a819-4c4d-b0d7-9b3bdcf3a9c0", NativeID: scope.NativeID, Cwd: scope.Cwd, NativeCwd: scope.Cwd, QueryID: query, ConfigurationID: strings.Repeat("c", 64),
				At: "2026-10-10T05:00:00Z", Path: "/owned/nested/CLAUDE.md", Source: "Project", Reason: "nested_traversal", PromptID: prompt, TriggerPath: "/owned/nested/data.txt"}}}}}
	event := claudeEvent{Type: "assistant", SessionID: scope.NativeID, UserMessageUUID: first}
	event.Message.Role = "assistant"
	event.Message.Content = json.RawMessage(`[{"type":"tool_use","id":"owned-read","name":"Read"}]`)
	s.Entries = append(s.Entries, claudeEntry{Kind: "event", QueryID: query, Event: event},
		claudeEntry{Kind: "instruction_prompt", InstructionPrompt: &claudeInstructionPrompt{Version: 1, NativeID: scope.NativeID, Cwd: scope.Cwd,
			QueryID: query, ConfigurationID: strings.Repeat("c", 64), At: "2026-10-10T05:00:00Z", PromptID: prompt, ToolUseID: "owned-read", Tool: "Read"}})
	s.Config.ID, s.Config.Cwd = scope.NativeID, scope.Cwd
	return scope, s
}

func TestClaudeInstructionLoadsKeepNativePromptAndQueryOwnership(t *testing.T) {
	scope, snapshot := instructionLoadFixture()
	view := projectClaude(scope, snapshot)
	history := view.InstructionLoads
	if history == nil || !history.Available || history.Complete || len(history.Items) != 1 || history.RecordingFailed || history.Truncated {
		t.Fatalf("invalid observation history: %+v", history)
	}
	load := history.Items[0]
	if !load.Current || load.Operation != snapshot.Entries[1].Operation || load.Operation == snapshot.Active || load.TriggerPath != "/owned/nested/data.txt" || load.ExecutionSnapshotID != "" {
		t.Fatal("late notification was attributed to a new operation or a file version")
	}
	if load.PromptID == load.Operation || load.PromptID != snapshot.Entries[3].InstructionLoad.PromptID || load.InputCorrelation != "native_tool_message" || history.Source != "claude.sdk.instructions_loaded" {
		t.Fatal("native prompt provenance lost")
	}
}

func TestClaudeInstructionLoadsJoinDistinctNativePromptThroughToolEvidence(t *testing.T) {
	// Minimal protocol fixture: prompt_id differs from SDK user_message_uuid.
	// Their only direct join here is PreToolUse.tool_use_id -> assistant tool ID.
	body := `{"config":{"id":"7ea9d6ff-693c-4e0e-901a-34ccb4684d82","cwd":"/owned"},"entries":[
	{"kind":"input","query_id":"13bee758-ede7-4a12-95d3-5f9a1b60ad93","operation":"3bb63555-5691-4526-a071-5aa3f9483332"},
	{"kind":"event","query_id":"13bee758-ede7-4a12-95d3-5f9a1b60ad93","event":{"type":"assistant","session_id":"7ea9d6ff-693c-4e0e-901a-34ccb4684d82","user_message_uuid":"3bb63555-5691-4526-a071-5aa3f9483332","parent_tool_use_id":null,"message":{"role":"assistant","content":[{"type":"tool_use","id":"tool_2"}]}}},
	{"kind":"instruction_prompt","link":{"version":1,"native_id":"7ea9d6ff-693c-4e0e-901a-34ccb4684d82","cwd":"/owned","query_id":"13bee758-ede7-4a12-95d3-5f9a1b60ad93","configuration_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","at":"2026-10-10T05:43:09Z","prompt_id":"c5159668-35eb-437d-8da5-ad28a05b2936","tool_use_id":"tool_2","tool":"Read"}},
	{"kind":"instruction_load","load":{"version":1,"id":"65cfd560-92c2-48b8-9031-7134d34574f1","native_id":"7ea9d6ff-693c-4e0e-901a-34ccb4684d82","cwd":"/owned","native_cwd":"/owned","query_id":"13bee758-ede7-4a12-95d3-5f9a1b60ad93","configuration_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","at":"2026-10-10T05:43:09Z","path":"/owned/feature/CLAUDE.md","source":"Project","reason":"nested_traversal","prompt_id":"c5159668-35eb-437d-8da5-ad28a05b2936"}}]}`
	var snapshot claudeSnapshot
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		t.Fatal(err)
	}
	history := claudeInstructionLoads(Scope{NativeID: snapshot.Config.ID, Cwd: snapshot.Config.Cwd}, snapshot)
	if history == nil || len(history.Items) != 1 || history.Items[0].Operation != "3bb63555-5691-4526-a071-5aa3f9483332" {
		t.Fatal("distinct native prompt lost its original input correlation")
	}
	if history.Items[0].PromptID == history.Items[0].Operation || history.Items[0].InputCorrelation != "native_tool_message" {
		t.Fatal("native prompt was overwritten by the input UUID")
	}
}

func TestClaudeInstructionLoadsRequireUnambiguousToolEvidence(t *testing.T) {
	for _, mode := range []string{"no-link", "missing-input-uuid", "unknown-input-uuid", "event-query", "input-query", "link-query", "link-configuration",
		"missing-native-id", "other-native-id", "replay", "child-event", "child-link", "wrong-event", "no-tool", "invalid-content", "link-cwd", "link-version", "link-time",
		"ambiguous-tool", "ambiguous-prompt", "matching-uuid-without-evidence"} {
		t.Run(mode, func(t *testing.T) {
			scope, snapshot := instructionLoadFixture()
			event, link := &snapshot.Entries[4].Event, snapshot.Entries[5].InstructionPrompt
			snapshot.Entries[4].Operation = snapshot.Entries[1].Operation
			otherQuery := "00e19b3c-4d65-4593-9f06-7b43824e2ce9"
			switch mode {
			case "no-link":
				snapshot.Entries[5].InstructionPrompt = nil
			case "missing-input-uuid":
				event.UserMessageUUID = ""
			case "unknown-input-uuid":
				event.UserMessageUUID = "44e19b3c-4d65-4593-9f06-7b43824e2ce9"
			case "event-query":
				snapshot.Entries[4].QueryID = otherQuery
			case "input-query":
				snapshot.Entries[1].QueryID = otherQuery
			case "link-query":
				link.QueryID = otherQuery
			case "link-configuration":
				link.ConfigurationID = strings.Repeat("e", 64)
			case "missing-native-id":
				event.SessionID = ""
			case "other-native-id":
				event.SessionID = "another"
			case "replay":
				event.IsReplay = true
			case "child-event":
				parent := "parent-tool"
				event.ParentToolUseID = &parent
			case "child-link":
				link.AgentID = "worker"
			case "wrong-event":
				event.Type = "user"
			case "no-tool":
				event.Message.Content = json.RawMessage(`[{"type":"text","text":"owned-read"}]`)
			case "invalid-content":
				event.Message.Content = json.RawMessage(`"owned-read"`)
			case "link-cwd":
				link.Cwd = "/other"
			case "link-version":
				link.Version = 2
			case "link-time":
				link.At = "yesterday"
			case "ambiguous-tool", "ambiguous-prompt":
				second := snapshot.Entries[4]
				second.Event.UserMessageUUID = snapshot.Entries[2].Operation
				if mode == "ambiguous-prompt" {
					second.Event.Message.Content = json.RawMessage(`[{"type":"tool_use","id":"second-read","name":"Read"}]`)
					other := *link
					other.ToolUseID = "second-read"
					snapshot.Entries = append(snapshot.Entries, claudeEntry{Kind: "instruction_prompt", InstructionPrompt: &other})
				}
				snapshot.Entries = append(snapshot.Entries, second)
			case "matching-uuid-without-evidence":
				snapshot.Entries[3].InstructionLoad.PromptID = snapshot.Entries[1].Operation
				snapshot.Entries[5].InstructionPrompt = nil
			}
			history := claudeInstructionLoads(scope, snapshot)
			if history == nil || len(history.Items) != 1 || !history.Available || history.Items[0].Operation != "" || history.Items[0].InputCorrelation != "" {
				t.Fatalf("unverified or ambiguous evidence assigned an input: %+v", history)
			}
		})
	}
}

func TestClaudeInstructionLoadsAllowRepeatedEvidenceForOneOriginalInput(t *testing.T) {
	scope, snapshot := instructionLoadFixture()
	snapshot.Entries = append(snapshot.Entries, snapshot.Entries[4], snapshot.Entries[5])
	history := claudeInstructionLoads(scope, snapshot)
	if history.Items[0].Operation != snapshot.Entries[1].Operation {
		t.Fatal("repeated evidence for the same input was treated as ambiguous ownership")
	}
}

func TestClaudeInstructionLoadsPreserveHistoryAndUnknownMetadata(t *testing.T) {
	for _, mode := range []string{"old-query", "old-configuration", "unknown-prompt", "subagent", "unknown-source", "offline", "old-bridge"} {
		t.Run(mode, func(t *testing.T) {
			scope, snapshot := instructionLoadFixture()
			load := snapshot.Entries[3].InstructionLoad
			switch mode {
			case "old-query":
				load.QueryID = "f883b923-980a-4770-bac5-875e580ebf4a"
				for _, index := range []int{1, 2, 4} {
					snapshot.Entries[index].QueryID = load.QueryID
				}
				snapshot.Entries[5].InstructionPrompt.QueryID = load.QueryID
			case "old-configuration":
				load.ConfigurationID = strings.Repeat("e", 64)
				snapshot.Entries[5].InstructionPrompt.ConfigurationID = load.ConfigurationID
			case "unknown-prompt":
				load.PromptID = "b16ba101-c08e-4d2b-b965-059dc61f3086"
			case "subagent":
				load.AgentID, load.AgentType = "native-worker", "reviewer"
			case "unknown-source":
				load.Source, load.Reason = "FutureSource", "future_load"
			case "offline":
				snapshot.Live = false
			case "old-bridge":
				snapshot.InstructionObserver = nil
			}
			history := claudeInstructionLoads(scope, snapshot)
			if history == nil || !history.Available || history.Complete || len(history.Items) != 1 || history.RecordingFailed {
				t.Fatalf("recorded source lost: %+v", history)
			}
			got := history.Items[0]
			if (mode == "old-query" || mode == "old-configuration" || mode == "offline" || mode == "old-bridge") && got.Current {
				t.Fatal("historical observation promoted to current query")
			}
			if (mode == "unknown-prompt" || mode == "subagent") && got.Operation != "" {
				t.Fatal("unverified native prompt assigned a main operation")
			}
			if mode != "unknown-prompt" && mode != "subagent" && got.Operation != snapshot.Entries[1].Operation {
				t.Fatal("historical notification lost its verified original input")
			}
			if mode == "unknown-source" && (got.Source != "FutureSource" || got.Reason != "future_load") {
				t.Fatal("unknown native metadata reclassified")
			}
		})
	}
}

func TestClaudeInstructionLoadsFlagMalformedRowsWithoutInventingEmptyCoverage(t *testing.T) {
	for _, mode := range []string{"version", "wrong-session", "wrong-cwd", "relative-path", "bad-time", "bad-prompt", "bad-query", "bad-config", "empty-reason", "nul-source", "bad-parent", "bad-trigger", "bad-glob", "missing-load", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			scope, snapshot := instructionLoadFixture()
			bad := *snapshot.Entries[3].InstructionLoad
			bad.ID = "06c180ee-7865-4c47-a33c-ee6e49ae47a6"
			switch mode {
			case "version":
				bad.Version = 2
			case "wrong-session":
				bad.NativeID = "another"
			case "wrong-cwd":
				bad.Cwd = "/other"
			case "relative-path":
				bad.Path = "CLAUDE.md"
			case "bad-time":
				bad.At = "yesterday"
			case "bad-prompt":
				bad.PromptID = "unknown"
			case "bad-query":
				bad.QueryID = "unknown"
			case "bad-config":
				bad.ConfigurationID = "not-a-hash"
			case "empty-reason":
				bad.Reason = ""
			case "nul-source":
				bad.Source = "Project\x00"
			case "bad-parent":
				bad.ParentPath = "CLAUDE.md"
			case "bad-trigger":
				bad.TriggerPath = "data.txt"
			case "bad-glob":
				bad.Globs = []string{""}
			case "duplicate":
				bad.ID = snapshot.Entries[3].InstructionLoad.ID
			}
			snapshot.Entries = append(snapshot.Entries, claudeEntry{Kind: "instruction_load", InstructionLoad: &bad})
			if mode == "missing-load" {
				snapshot.Entries[len(snapshot.Entries)-1].InstructionLoad = nil
			}
			history := claudeInstructionLoads(scope, snapshot)
			if !history.Available || !history.RecordingFailed || history.Complete || len(history.Items) != 1 {
				t.Fatalf("malformed record handling: %+v", history)
			}
		})
	}
}

func TestClaudeInstructionLoadsKeepLegacyUnavailableAndExplicitEmptySeparate(t *testing.T) {
	if claudeInstructionLoads(Scope{}, claudeSnapshot{}) != nil {
		t.Fatal("old bridge invented an empty history")
	}
	scope, snapshot := instructionLoadFixture()
	snapshot.Entries = nil
	history := claudeInstructionLoads(scope, snapshot)
	if !history.Available || len(history.Items) != 0 || history.Complete {
		t.Fatal("explicit observer report was lost")
	}
	snapshot.InstructionObserver.RecordingFailed = nil
	history = claudeInstructionLoads(scope, snapshot)
	if history.Available || !history.RecordingFailed || history.CurrentQueryID != "" {
		t.Fatal("missing observer evidence became a verified empty history")
	}
}

func TestClaudeInstructionLoadsBoundProjectionAndIgnoreDerivedWireClaims(t *testing.T) {
	scope, snapshot := instructionLoadFixture()
	original := *snapshot.Entries[3].InstructionLoad
	snapshot.Entries = append(snapshot.Entries[:3], snapshot.Entries[4:]...)
	for i := range 130 {
		load := original
		load.ID = fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)
		load.Operation, load.ExecutionSnapshotID, load.InputCorrelation = "forged", "forged-snapshot", "forged-correlation"
		snapshot.Entries = append(snapshot.Entries, claudeEntry{Kind: "instruction_load", InstructionLoad: &load})
	}
	history := claudeInstructionLoads(scope, snapshot)
	if !history.Truncated || len(history.Items) != 128 || history.Items[0].ID != "00000002-1111-4111-8111-111111111111" || len(snapshot.Entries) != 135 {
		t.Fatal("bounded view deleted or reordered the original journal")
	}
	encoded, _ := json.Marshal(history)
	if strings.Contains(string(encoded), "forged") {
		t.Fatal("derived wire claims were trusted")
	}
}
