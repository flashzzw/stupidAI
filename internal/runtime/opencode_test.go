package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fake is only the native HTTP boundary. Assertions use the adapter's
// public observation/reconciliation contract, including native union shapes.
func openCodeFixture(t *testing.T, history *string) (*OpenCode, Scope, *opencodeServer) {
	t.Helper()
	profile := t.TempDir()
	t.Setenv("NETCAP_TEST_OPENCODE_KEY", "fixture")
	if err := os.WriteFile(filepath.Join(profile, "opencode.json"), []byte(`{"provider":{"fixture":{"options":{}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	scope := Scope{Instance: Instance{ID: "test", Kind: "opencode", Profile: profile}, Route: Route{Provider: "fixture", Model: "fixture-model", CredentialRef: "env:NETCAP_TEST_OPENCODE_KEY"}, Cwd: profile, NativeID: "ses_test"}
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/config":
			_, _ = w.Write([]byte(`{"provider":{"fixture":{"options":{"baseURL":"","apiKey":"fixture"}}}}`))
		case "/session/ses_test":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses_test", "directory": profile})
		case "/session/ses_test/message":
			_, _ = w.Write([]byte(*history))
		case "/session/status", "/mcp":
			_, _ = w.Write([]byte(`{}`))
		case "/permission", "/question", "/pty", "/session/ses_test/children":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected native request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(native.Close)
	_, digest, err := opencodeConfig(scope)
	if err != nil {
		t.Fatal(err)
	}
	s := &opencodeServer{record: opencodeServerRecord{PID: os.Getpid(), Birth: processBirth(os.Getpid()), URL: native.URL, Digest: digest}, root: t.TempDir(), cwd: profile, http: native.Client(), parts: map[string]map[string]opencodePart{}, seen: map[string]bool{}}
	c := NewOpenCode(t.TempDir())
	c.servers[opencodeKey(scope)] = s
	return c, scope, s
}

func TestOpenCodeUserSummaryObjectAndAssistantSummaryFlag(t *testing.T) {
	history := `[
	 {"info":{"id":"msg_user","sessionID":"ses_test","role":"user","summary":{"diffs":[]}},"parts":[{"id":"prt_user","type":"text","text":"input"}]},
	 {"info":{"id":"msg_summary","sessionID":"ses_test","role":"assistant","parentID":"msg_user","summary":true,"finish":"stop","time":{"completed":1}},"parts":[]}
	]`
	c, scope, _ := openCodeFixture(t, &history)
	v, err := c.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if v.ExecutionClear || v.Status != "unknown" || len(v.Messages) != 1 {
		t.Fatalf("compaction summary cannot finish a turn: %+v", v)
	}
	history = strings.Replace(history, `"summary":true`, `"summary":false`, 1)
	v, err = c.Observe(context.Background(), scope)
	if err != nil || !v.ExecutionClear || v.Turns[0].Status != "completed" {
		t.Fatalf("normal assistant terminal should finish turn: %+v, %v", v, err)
	}
}

func TestOpenCodeSSETextRejectsDuplicateAndLateEvents(t *testing.T) {
	history := `[{"info":{"id":"msg_user","sessionID":"ses_test","role":"user"},"parts":[]},
	{"info":{"id":"msg_assistant","sessionID":"ses_test","role":"assistant","parentID":"msg_user"},"parts":[{"id":"prt_text","type":"text","text":"hel"}]}]`
	c, scope, s := openCodeFixture(t, &history)
	s.event([]byte(`{"id":"evt_001","type":"message.part.updated","properties":{"sessionID":"ses_test","part":{"id":"prt_text","type":"text","text":"hel"}}}`))
	delta := []byte(`{"id":"evt_003","type":"message.part.delta","properties":{"sessionID":"ses_test","partID":"prt_text","field":"text","delta":"lo"}}`)
	s.event(delta)
	s.event(delta)
	// Simulate bounded dedupe eviction, then replay an old delta and snapshot.
	s.seen = map[string]bool{}
	s.event(delta)
	s.event([]byte(`{"id":"evt_002","type":"message.part.updated","properties":{"sessionID":"ses_test","part":{"id":"prt_text","type":"text","text":"bad"}}}`))
	v, err := c.Observe(context.Background(), scope)
	if err != nil || len(v.Messages) != 1 || v.Messages[0].Text != "hello" {
		t.Fatalf("duplicate/late event changed streamed output: %+v, %v", v, err)
	}
	if v.ExecutionClear || v.Evidence.Complete {
		t.Fatal("SSE text alone released unfinished native turn")
	}
}

func TestOpenCodeMissingNativeInputIsUnknownAfterLostReceipt(t *testing.T) {
	history := `[]`
	c, scope, _ := openCodeFixture(t, &history)
	r, err := c.Reconcile(context.Background(), Intent{ID: "3b1150dd-302a-4bfd-b9e0-24ff52abaefa", CreatedAt: "2026-09-28T19:37:14.827132782Z", Scope: scope, Kind: "send", Text: "original"}, Receipt{})
	if err != nil || r.State != "unknown" {
		t.Fatalf("missing native input is not proof of not-sent: %+v %v", r, err)
	}
	history = `[{"info":{"id":"msg_user","sessionID":"ses_other","role":"user"}}]`
	if _, err = c.Observe(context.Background(), scope); err == nil {
		t.Fatal("wrong-session history accepted")
	}
}

func TestOpenCodeHistoryPaginationMustAdvanceAndIncludeOlderMessages(t *testing.T) {
	calls := 0
	repeat := false
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("limit") != "100" {
			t.Error("missing explicit page bound")
		}
		start, end := 3, 102
		if cursor := r.URL.Query().Get("before"); cursor != "" && !repeat {
			if cursor != "msg_003" {
				t.Errorf("wrong older-message cursor: %s", cursor)
			}
			start, end = 1, 2
		}
		page := []any{}
		for n := start; n <= end; n++ {
			page = append(page, map[string]any{"info": map[string]any{"id": fmt.Sprintf("msg_%03d", n), "sessionID": "ses_test", "role": "user", "time": map[string]int{"created": n}}, "parts": []any{}})
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer native.Close()
	s := &opencodeServer{record: opencodeServerRecord{URL: native.URL}, http: native.Client()}
	messages, err := opencodeHistory(context.Background(), s, "ses_test")
	if err != nil || calls != 2 || len(messages) != 102 || messages[0].Info.ID != "msg_001" || messages[101].Info.ID != "msg_102" {
		t.Fatalf("incomplete/out-of-order paged history: length=%d calls=%d err=%v", len(messages), calls, err)
	}
	repeat = true
	if _, err := opencodeHistory(context.Background(), s, "ses_test"); err == nil {
		t.Fatal("non-advancing page accepted as complete history")
	}
}
