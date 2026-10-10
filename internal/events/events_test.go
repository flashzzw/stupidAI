package events

import (
	"encoding/json"
	"testing"
)

func TestNativeIdentityCorrelationAndUncorrelatedSources(t *testing.T) {
	base := Envelope{Kind: TurnCompleted, Source: "runtime/one", SubjectType: "session", SubjectID: "session", SessionID: "session", InstanceID: "instance", NativeSessionID: "native", NativeTurnID: "turn", OccurredAt: "2026-09-29T00:00:00Z", NotificationSender: "console", Managed: true}
	runtime, err := Identify(base, "operation")
	if err != nil {
		t.Fatal(err)
	}
	base.Source = "hook/one"
	hook, _ := Identify(base, "delivery")
	if hook.EventID != runtime.EventID {
		t.Fatal("same explicit native turn did not correlate")
	}
	merged, err := Merge(hook, runtime)
	if err != nil || len(merged.Sources) != 2 || merged.Source != "runtime/one" {
		t.Fatalf("lost runtime authority/source evidence: %+v %v", merged, err)
	}
	base.NativeTurnID = ""
	first, _ := Identify(base, "one")
	second, _ := Identify(base, "two")
	if first.EventID == second.EventID {
		t.Fatal("equal text without native identity merged separate deliveries")
	}
	base.Kind, base.NativeRequestID, base.NativeConnectionID = PermissionRequired, "1", "connection-one"
	first, _ = Identify(base, "one")
	base.NativeConnectionID = "connection-two"
	second, _ = Identify(base, "two")
	if first.EventID == second.EventID {
		t.Fatal("reused request ID merged across connections")
	}
	base.Kind, base.SubjectType, base.SubjectID = RouteUnavailable, "route", "route-a"
	if _, err := Identify(base, "route"); err == nil {
		t.Fatal("route event accepted fabricated session identity")
	}
}

func TestNativeObservationKindsDoNotClaimTaskCompletion(t *testing.T) {
	for _, tc := range []struct {
		agent, format, raw string
		kind               Kind
	}{
		{"claude", "hook", `{"hook_event_name":"PermissionRequest","session_id":"claude-one","tool_input":{"command":"date -u"}}`, PermissionRequired},
		{"codex", "hook", `{"hook_event_name":"Stop","session_id":"codex-one","last_assistant_message":"finished"}`, TurnCompleted},
		{"opencode", "opencode", `{"type":"question.asked","properties":{"id":"que_one","sessionID":"ses_one","questions":[{"question":"Choose one"}]}}`, InputRequired},
		{"opencode", "opencode", `{"type":"session.idle","properties":{"sessionID":"ses_one"}}`, TurnCompleted},
		{"opencode", "opencode", `{"type":"session.error","properties":{"sessionID":"ses_one","error":{"name":"MessageAbortedError"}}}`, SessionStopped},
	} {
		t.Run(tc.agent+"/"+string(tc.kind), func(t *testing.T) {
			items, err := Normalize(tc.agent, tc.format, json.RawMessage(tc.raw))
			if err != nil || len(items) != 1 || items[0].Kind != tc.kind || items[0].Kind.Subject() != "session" {
				t.Fatalf("incorrect observation: %+v %v", items, err)
			}
		})
	}
	items, err := Normalize("claude", "hook", json.RawMessage(`{"hook_event_name":"Stop","hook_source":"opencode-plugin","session_id":"same-native"}`))
	if err != nil || len(items) != 0 {
		t.Fatal("replayed OpenCode hook became a second event source")
	}
}

func TestOTELFailureRequiresNativeSessionAndExcludesModelDiscovery(t *testing.T) {
	attribute := func(key string, value any) any {
		return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
	}
	record := func(path, session string) any {
		return map[string]any{"attributes": []any{attribute("event.name", "codex.api_request"), attribute("conversation.id", session), attribute("endpoint", path), attribute("error.message", "same failure text")}}
	}
	raw, _ := json.Marshal(map[string]any{"resourceLogs": []any{map[string]any{"resource": map[string]any{"attributes": []any{attribute("cwd", "/workspace")}}, "scopeLogs": []any{map[string]any{"logRecords": []any{record("https://fixture.invalid/v1/models", "one"), record("/v1/responses", "one"), record("/v1/responses", "two"), record("/v1/responses", "")}}}}}})
	items, err := Normalize("codex", "otel", raw)
	if err != nil || len(items) != 2 || items[0].NativeSessionID == items[1].NativeSessionID || items[0].Cwd != "/workspace" || items[0].Kind != RunFailed {
		t.Fatalf("invalid OTEL scope: %+v %v", items, err)
	}
}
