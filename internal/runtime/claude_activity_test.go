package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeNativeInterruptMessageIsNotHumanTakeover(t *testing.T) {
	var s claudeSnapshot
	if err := json.Unmarshal([]byte(`{
	 "config":{"id":"native"},"live":true,
	 "entries":[{"kind":"initialized"},{"kind":"input","operation":"original","text":"work"},
	  {"kind":"event","operation":"original","event":{"type":"user","uuid":"original","isReplay":true,"message":{"role":"user","content":"work"}}},
	  {"kind":"event","operation":"original","event":{"type":"user","uuid":"native-note","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}},
	  {"kind":"event","operation":"original","event":{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","queued_turn_count":0}}],
	 "history":[{"type":"user","uuid":"original","message":{"role":"user","content":"work"}},
	  {"type":"user","uuid":"native-note","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}]
	}`), &s); err != nil {
		t.Fatal(err)
	}
	v := projectClaude(Scope{NativeID: "native"}, s)
	if !v.ExecutionClear || v.Turns[0].Status != "interrupted" || v.Messages[1].Role != "tool" {
		t.Fatalf("native generated interruption mistaken for human input: %+v", v)
	}
	// The same text authored outside this SDK process has no matching live
	// event provenance. Text equality must not grant it controller ownership.
	s.History[1].UUID = "external-user"
	v = projectClaude(Scope{NativeID: "native"}, s)
	if v.ExecutionClear || v.Status != "unknown" {
		t.Fatal("external input acquired ownership by matching native text")
	}
}
