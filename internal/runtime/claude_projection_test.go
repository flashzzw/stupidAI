package runtime

import (
	"encoding/json"
	"testing"
)

func TestClaudeResultDoesNotReleaseBackgroundScope(t *testing.T) {
	var snapshot claudeSnapshot
	err := json.Unmarshal([]byte(`{
	  "config":{"id":"native"},"live":true,
	  "entries":[
	    {"kind":"initialized"},
	    {"kind":"input","operation":"original","text":"work"},
	    {"kind":"event","operation":"original","event":{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"native-bash","task_type":"local_bash"}]}},
	    {"kind":"event","operation":"original","event":{"type":"result","subtype":"success","queued_turn_count":0,"terminal_reason":"completed"}}
	  ],
	  "history":[{"type":"user","uuid":"original","message":{"role":"user","content":"work"}}]
	}`), &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{NativeID: "native"}
	view := projectClaude(scope, snapshot)
	if view.Status != "running" || view.ExecutionClear || len(view.Executions) != 1 || view.NativeStatus != "sdk_live" {
		t.Fatalf("released background execution: %+v", view)
	}
	snapshot.Live = false
	view = projectClaude(scope, snapshot)
	if view.Status != "unknown" || view.Evidence.Complete || view.ExecutionClear || view.NativeStatus != "sdk_offline" {
		t.Fatalf("lost bridge retained unjustified certainty: %+v", view)
	}
	snapshot.Live = true
	snapshot.Entries = append(snapshot.Entries, claudeEntry{Kind: "event", Event: claudeEvent{Type: "system", Subtype: "background_tasks_changed"}})
	view = projectClaude(scope, snapshot)
	if !view.ExecutionClear || view.Status != "idle" {
		t.Fatalf("native completion did not clear occupancy: %+v", view)
	}
}
