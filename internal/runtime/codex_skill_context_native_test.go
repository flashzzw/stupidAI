//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeSkillContextRead(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-skill-context-view-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks := map[string]bool{}
	reports := map[string]runtime.SkillContext{}
	var readCalls []map[string]any
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "reports": reports, "read_calls": readCalls, "processes": f.Records, "passed": !t.Failed(), "scope": "production read-only Runtime/Application/Handler; inherited fixture Skill, two interrupted unavailable local provider turns; no successful model/tool/hook execution"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(b))
	})
	source := filepath.Join(root, "codex/plugins/cache/netcap-context/fixture/1.0.0")
	skill := filepath.Join(source, "skills/method/SKILL.md")
	write(filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"fixture","version":"1.0.0"}`)
	write(skill, "---\nname: method\ndescription: W0208_CONTEXT_DESCRIPTION\n---\nPRIVATE_NATIVE_SKILL_BODY\n")
	config := filepath.Join(root, "codex/config.toml")
	b, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	write(config, strings.Replace(string(b), "plugins=false", "plugins=true\nplugin_sharing=false", 1)+"\n[plugins.\"fixture@netcap-context\"]\nenabled=true\n")
	beforeConfig, _ := os.ReadFile(config)
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ids := []string{}
	native := []string{}
	for index, label := range []string{"Skill context history", "No prepared context"} {
		r, e := a.Apply(app.Command{ID: "create-" + strconv.Itoa(index), Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: label})
		if e != nil {
			t.Fatal(e)
		}
		snap := waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, ss := range s.Sessions {
				if ss.ID == r.SessionID {
					return ss.Status == "idle"
				}
			}
			return false
		})
		ids = append(ids, r.SessionID)
		for _, ss := range snap.Sessions {
			if ss.ID == r.SessionID {
				native = append(native, ss.Scope.NativeID)
			}
		}
		if _, e = a.Apply(app.Command{ID: "takeover-" + strconv.Itoa(index), Kind: "takeover", SessionID: r.SessionID}); e != nil {
			t.Fatal(e)
		}
	}
	initial, err := a.ReadSessionSkillContext(context.Background(), ids[0])
	check("unmaterialized_context_is_not_claimed", err != nil && !initial.Complete)
	for i, label := range []string{"loaded", "disabled"} {
		if i == 1 {
			if err = f.Call(context.Background(), "thread/unsubscribe", map[string]any{"threadId": native[0]}, nil); err != nil {
				t.Fatal(err)
			}
			if err = f.Call(context.Background(), "thread/resume", map[string]any{"threadId": native[0], "cwd": cfg.Projects[0].Cwd, "model": f.Route.Model, "modelProvider": f.Route.Provider, "sandbox": "read-only", "approvalPolicy": "on-request", "config": map[string]any{"plugins.fixture@netcap-context.enabled": false}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		var started struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err = f.Call(context.Background(), "turn/start", map[string]any{"threadId": native[0], "input": []any{map[string]any{"type": "text", "text": "Owned Skill context read; provider intentionally unavailable"}, map[string]any{"type": "skill", "name": "fixture:method", "path": skill}}}, &started); err != nil {
			t.Fatal(err)
		}
		attempted := false
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			checkpoint, e := f.Checkpoint()
			if e != nil {
				t.Fatal(e)
			}
			var records []struct {
				Events []struct {
					Method string `json:"method"`
					Params struct {
						ThreadID string          `json:"threadId"`
						TurnID   string          `json:"turnId"`
						Error    json.RawMessage `json:"error"`
					} `json:"params"`
				} `json:"events"`
			}
			_ = json.Unmarshal(checkpoint.Trace, &records)
			for _, record := range records {
				for _, event := range record.Events {
					if event.Method == "error" && event.Params.ThreadID == native[0] && event.Params.TurnID == started.Turn.ID && strings.Contains(string(event.Params.Error), "responseStreamDisconnected") {
						attempted = true
					}
				}
			}
			if attempted {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		check(label+"_unavailable_provider_observed", attempted)
		if err = f.Call(context.Background(), "turn/interrupt", map[string]any{"threadId": native[0], "turnId": started.Turn.ID}, nil); err != nil {
			t.Fatal(err)
		}
		terminal := false
		deadline = time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var out struct {
				Thread struct {
					Turns []struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					} `json:"turns"`
				} `json:"thread"`
			}
			if err = f.Call(context.Background(), "thread/read", map[string]any{"threadId": native[0], "includeTurns": true}, &out); err != nil {
				t.Fatal(err)
			}
			for _, turn := range out.Thread.Turns {
				if turn.ID == started.Turn.ID && turn.Status == "interrupted" {
					terminal = true
				}
			}
			if terminal {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		check(label+"_turn_interrupted", terminal)
		report, e := a.ReadSessionSkillContext(context.Background(), ids[0])
		if e != nil {
			t.Fatal(e)
		}
		reports[label] = report
		check(label+"_original_identity", report.NativeID == native[0] && report.Cwd == cfg.Projects[0].Cwd)
		check(label+"_frame_count", len(report.Frames) == i+1)
		frame := report.Frames[len(report.Frames)-1]
		exposed := false
		for _, item := range frame.Descriptions {
			if item.Name == "fixture:method" && item.Path == skill {
				exposed = true
			}
		}
		check(label+"_description_matches_turn", exposed == (i == 0))
		check(label+"_body_load_matches_turn", len(frame.Loads) == 1-i)
		encoded, _ := json.Marshal(report)
		check(label+"_body_not_exposed", !strings.Contains(string(encoded), "PRIVATE_NATIVE_SKILL_BODY"))
	}
	check("old_body_load_retained", len(reports["disabled"].Frames[0].Loads) == 1)
	checkpoint, err := f.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	snapshotBefore := a.Snapshot()
	report, err := a.ReadSessionSkillContext(runtime.NativeTraceContext(context.Background(), "read-skill-context"), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	snapshotAfter := a.Snapshot()
	// Live observations may update views/sequence during a read. Application
	// unit tests check the full no-write contract with a controlled observer;
	// here preserve the binding and ownership while native polling stays active.
	bindings := func(s app.Snapshot) string {
		out := map[string]any{}
		for _, session := range s.Sessions {
			out[session.ID] = []any{session.Scope, session.Generation, session.ControlMode, session.Paused, session.Permission, session.ExecutionSnapshot, session.ExecutionHistory}
		}
		b, _ := json.Marshal(out)
		return string(b)
	}
	check("read_preserves_binding_and_control", bindings(snapshotBefore) == bindings(snapshotAfter))
	next, err := f.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	var previous, current []struct {
		Calls []map[string]any `json:"calls"`
	}
	_ = json.Unmarshal(checkpoint.Trace, &previous)
	_ = json.Unmarshal(next.Trace, &current)
	onlyReads := true
	for i, p := range current {
		for _, call := range p.Calls[len(previous[i].Calls):] {
			if call["trace_context"] != "read-skill-context" {
				continue
			}
			readCalls = append(readCalls, call)
			params, _ := call["params"].(map[string]any)
			onlyReads = onlyReads && call["method"] == "thread/read" && params["includeTurns"] == false && params["threadId"] == native[0]
		}
	}
	check("read_calls_metadata_only", onlyReads && len(readCalls) > 0)
	token := strings.Repeat("s", 64)
	handler := console.Handler(a, "127.0.0.1:9467", token, os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids[0], "/api/sessions/" + ids[1], "/api/sessions/" + ids[0] + "/skills/context"} {
		method, body := "GET", ""
		if strings.HasSuffix(path, "/skills/context") {
			method = "POST"
			body = "{}"
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:9467"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("handler %s: %d %s", path, w.Code, w.Body.String())
		}
		responses[path] = append([]byte(nil), w.Body.Bytes()...)
	}
	encoded, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(encoded))
	beforeClose := report
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	store, err = app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := a.ReadSessionSkillContext(context.Background(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(beforeClose.Frames)
	b2, _ := json.Marshal(restored.Frames)
	check("restart_reads_original_history_without_loading", restored.NativeID == beforeClose.NativeID && string(b1) == string(b2))
	afterConfig, _ := os.ReadFile(config)
	check("profile_configuration_unchanged", string(beforeConfig) == string(afterConfig))
}
