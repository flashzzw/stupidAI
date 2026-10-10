package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeHookExecutionKeepsOutcomeAndUnfinishedOccupancy(t *testing.T) {
	for _, mode := range []string{"running", "success", "error", "cancelled", "disconnected", "orphan", "foreign", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			var s claudeSnapshot
			if err := json.Unmarshal([]byte(`{"config":{"id":"native"},"live":true,"entries":[{"kind":"initialized"},{"kind":"event","at":"2026-10-02T15:00:00Z","event":{"type":"system","subtype":"hook_started","session_id":"native","uuid":"start","hook_id":"hook","hook_event":"SessionStart","hook_name":"PRIVATE_COMMAND"}}]}`), &s); err != nil {
				t.Fatal(err)
			}
			if mode == "disconnected" {
				s.Live = false
			}
			if mode == "success" || mode == "error" || mode == "cancelled" || mode == "orphan" || mode == "duplicate" {
				outcome := mode
				if mode == "orphan" || mode == "duplicate" {
					outcome = "success"
				}
				body := `{"kind":"event","at":"2026-10-02T15:00:01Z","event":{"type":"system","subtype":"hook_response","session_id":"native","uuid":"end","hook_id":"hook","hook_event":"SessionStart","outcome":"` + outcome + `","exit_code":0,"output":"PRIVATE_OUTPUT"}}`
				var entry claudeEntry
				if err := json.Unmarshal([]byte(body), &entry); err != nil {
					t.Fatal(err)
				}
				if mode == "orphan" {
					s.Entries = s.Entries[:1]
				}
				s.Entries = append(s.Entries, entry)
				if mode == "duplicate" {
					s.Entries = append(s.Entries, entry)
				}
			}
			if mode == "foreign" {
				s.Entries[1].Event.SessionID = "other"
			}
			v := projectClaude(Scope{NativeID: "native"}, s)
			if mode == "foreign" {
				if len(v.HookExecutions) != 0 {
					t.Fatal("foreign hook attributed to this session")
				}
				return
			}
			if len(v.HookExecutions) != 1 {
				t.Fatalf("missing or duplicated hook: %+v", v)
			}
			h := v.HookExecutions[0]
			want := mode
			if mode == "disconnected" || mode == "orphan" {
				want = "unknown"
			}
			if mode == "duplicate" {
				want = "success"
			}
			if h.Status != want || h.Source != "unreported" {
				t.Fatalf("invented hook status or source: %+v", h)
			}
			if (mode == "running" || mode == "disconnected" || mode == "orphan") && v.ExecutionClear {
				t.Fatal("unfinished or incomplete hook released occupancy")
			}
			if (mode == "success" || mode == "error" || mode == "cancelled" || mode == "duplicate") && !v.ExecutionClear {
				t.Fatal("terminal hook kept occupancy")
			}
			data, _ := json.Marshal(v)
			if strings.Contains(string(data), "PRIVATE_") {
				t.Fatal("private hook diagnostics leaked into view")
			}
		})
	}
}
