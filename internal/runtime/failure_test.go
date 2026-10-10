package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex-netcap/internal/codex"
)

func TestCodexProjectsOnlyStructuredNativeFailure(t *testing.T) {
	for _, tc := range []struct{ info, kind string }{
		{`"unauthorized"`, "authentication"}, {`"usageLimitExceeded"`, "quota"},
		{`"rateLimitExceeded"`, "rate_limit"}, {`"sandboxError"`, "tool"},
		{`"contextWindowExceeded"`, "session"}, {`{"httpConnectionFailed":{"httpStatusCode":503}}`, "service"},
		{`{"responseStreamDisconnected":{"httpStatusCode":null}}`, "network"},
		{`"other"`, "unknown"}, {`{"unknown":{"httpStatusCode":401}}`, "unknown"},
	} {
		t.Run(tc.info, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			f.call = func(method string, _ map[string]any) (any, error) {
				switch method {
				case "thread/read":
					return map[string]any{"thread": codex.Thread{ID: "native", Turns: []codex.Turn{{ID: "turn", Status: "failed", CompletedAt: 1790640000, Error: &codex.TurnError{Message: "arbitrary text: rate_limit, unauthorized, secret", Info: json.RawMessage(tc.info)}}}}}, nil
				}
				return emptyControl(method)
			}
			v, err := c.Observe(context.Background(), scope)
			if err != nil || len(v.Turns) != 1 || v.Turns[0].Failure == nil || v.Turns[0].Failure.Kind != tc.kind {
				t.Fatalf("native error projection: %+v %v", v, err)
			}
		})
	}
}

func TestOpenCodeProviderFailureUsesMessageEvidenceAndRetryAfter(t *testing.T) {
	for _, tc := range []struct{ name, data, kind, retry string }{
		{"APIError", `{"statusCode":429,"isRetryable":true,"responseHeaders":{"Retry-After":"120"}}`, "rate_limit", "2026-09-29T00:02:00Z"},
		{"APIError", `{"statusCode":503,"isRetryable":true,"responseHeaders":{"retry-after":"Tue, 29 Sep 2026 01:00:00 GMT"}}`, "service", "2026-09-29T01:00:00Z"},
		{"APIError", `{"statusCode":401,"message":"rate_limit"}`, "authentication", ""},
		{"APIError", `{"statusCode":402}`, "quota", ""},
		{"APIError", `{"statusCode":400}`, "session", ""},
		{"APIError", `{"isRetryable":true}`, "network", ""},
		{"UnknownError", `{"message":"429 unauthorized secret","statusCode":401}`, "unknown", ""},
		{"ProviderAuthError", `{}`, "authentication", ""},
	} {
		t.Run(tc.kind+tc.retry, func(t *testing.T) {
			at, _ := time.Parse(time.RFC3339, "2026-09-29T00:00:00Z")
			body, _ := json.Marshal([]any{
				map[string]any{"info": map[string]any{"id": "user", "role": "user"}},
				map[string]any{"info": map[string]any{"id": "assistant", "role": "assistant", "parentID": "user", "time": map[string]int64{"completed": at.UnixMilli()}, "error": map[string]any{"name": tc.name, "data": json.RawMessage(tc.data)}}},
			})
			var messages []opencodeMessage
			if err := json.Unmarshal(body, &messages); err != nil {
				t.Fatal(err)
			}
			turns := opencodeTurns(messages, false)
			if len(turns) != 1 || turns[0].Status != "failed" || turns[0].Failure == nil || turns[0].Failure.Kind != tc.kind || turns[0].Failure.RetryAt != tc.retry {
				t.Fatalf("wrong terminal provider evidence: %+v", turns)
			}
		})
	}
}

func TestClaudeRetryDoesNotBecomeTerminalProviderFailure(t *testing.T) {
	for _, tc := range []struct{ name, events, state, kind string }{
		{"retry", `{"type":"system","subtype":"api_retry","error":"rate_limit","attempt":1,"retry_delay_ms":2000}`, "inProgress", ""},
		{"rate-limit", `{"type":"assistant","error":"rate_limit","message":{"content":[]}}`, "failed", "rate_limit"},
		{"auth", `{"type":"assistant","error":"authentication_failed","message":{"content":[]}}`, "failed", "authentication"},
		{"subagent", `{"type":"assistant","parent_tool_use_id":"child","error":"billing_error","message":{"content":[]}}`, "failed", ""},
		{"model-prose", `{"type":"assistant","message":{"content":[{"type":"text","text":"billing_error rate_limit"}]}}`, "failed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var snapshot claudeSnapshot
			body := `{"config":{"id":"native"},"live":true,"entries":[{"kind":"initialized"},{"kind":"input","operation":"op","text":"work"},{"kind":"event","operation":"op","event":` + tc.events + `}],"history":[{"type":"user","uuid":"op","message":{"content":"work"}}]}`
			if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
				t.Fatal(err)
			}
			if tc.state == "failed" {
				zero := 0
				snapshot.Entries = append(snapshot.Entries, claudeEntry{Kind: "event", Operation: "op", At: "2026-09-29T00:00:00Z", Event: claudeEvent{Type: "result", Subtype: "error_during_execution", Queued: &zero}})
			}
			v := projectClaude(Scope{NativeID: "native"}, snapshot)
			if v.Turns[0].Status != tc.state {
				t.Fatalf("retry released native execution: %+v", v)
			}
			failure := v.Turns[0].Failure
			if tc.kind == "" && failure != nil || tc.kind != "" && (failure == nil || failure.Kind != tc.kind) {
				t.Fatalf("wrong provider evidence: %+v", failure)
			}
		})
	}
}
