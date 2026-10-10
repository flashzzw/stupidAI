package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	s, err := app.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(app.Config{}, s, runtime.NewCodex())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return Handler(a, "127.0.0.1:9000", strings.Repeat("a", 64), fstest.MapFS{"index.html": {Data: []byte("console")}})
}

func TestLocalAuthorizationAndOrigin(t *testing.T) {
	h := testHandler(t)
	for _, tc := range []struct {
		name, method, path, host, origin, token, body string
		status                                        int
	}{
		{"recover needs token", "POST", "/api/commands", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{"command_id":"recover","kind":"recover_execution","session_id":"one","execution_recovery":{}}`, 401},
		{"recover foreign origin", "POST", "/api/commands", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{"command_id":"recover","kind":"recover_execution","session_id":"one","execution_recovery":{}}`, 403},
		{"recover source override", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"recover","kind":"recover_execution","session_id":"one","execution_recovery":{"cwd":"foreign"}}`, 400},
		{"recover invalid mode", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"recover","kind":"recover_execution","session_id":"one","execution_recovery":{"original_query_retry":"true"}}`, 400},
		{"recover missing session", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"recover","kind":"recover_execution","session_id":"one","execution_recovery":{}}`, 409},
		{"no token", "GET", "/api/snapshot", "127.0.0.1:9000", "", "", "", 401},
		{"recovery check needs token", "POST", "/api/sessions/one/execution/recovery-check", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"recovery check foreign origin", "POST", "/api/sessions/one/execution/recovery-check", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"recovery check rejects source override", "POST", "/api/sessions/one/execution/recovery-check", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"recovery check rejects null", "POST", "/api/sessions/one/execution/recovery-check", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"recovery check missing session", "POST", "/api/sessions/one/execution/recovery-check", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"skill context needs token", "POST", "/api/sessions/one/skills/context", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"skill context foreign origin", "POST", "/api/sessions/one/skills/context", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"skill context rejects path override", "POST", "/api/sessions/one/skills/context", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"skill context rejects null", "POST", "/api/sessions/one/skills/context", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"skill context missing session", "POST", "/api/sessions/one/skills/context", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"skill discovery needs token", "POST", "/api/sessions/one/skills/discover", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"skill discovery foreign origin", "POST", "/api/sessions/one/skills/discover", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"skill discovery rejects path override", "POST", "/api/sessions/one/skills/discover", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"skill discovery rejects null", "POST", "/api/sessions/one/skills/discover", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"skill discovery missing session", "POST", "/api/sessions/one/skills/discover", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"skill catalog needs token", "POST", "/api/sessions/one/skills/catalog", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"skill catalog foreign origin", "POST", "/api/sessions/one/skills/catalog", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"skill catalog rejects path override", "POST", "/api/sessions/one/skills/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"skill catalog rejects null", "POST", "/api/sessions/one/skills/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"skill catalog missing session", "POST", "/api/sessions/one/skills/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"hook catalog needs token", "POST", "/api/sessions/one/hooks/catalog", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"hook catalog foreign origin", "POST", "/api/sessions/one/hooks/catalog", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"hook catalog rejects path override", "POST", "/api/sessions/one/hooks/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"hook catalog rejects null", "POST", "/api/sessions/one/hooks/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"hook catalog missing session", "POST", "/api/sessions/one/hooks/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"permission catalog needs token", "POST", "/api/sessions/one/permissions/catalog", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"permission catalog foreign origin", "POST", "/api/sessions/one/permissions/catalog", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"permission catalog rejects override", "POST", "/api/sessions/one/permissions/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"permission catalog rejects null", "POST", "/api/sessions/one/permissions/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"permission catalog missing session", "POST", "/api/sessions/one/permissions/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"installed plugins need token", "POST", "/api/sessions/one/plugins/installed", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"installed plugins foreign origin", "POST", "/api/sessions/one/plugins/installed", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"installed plugins reject path override", "POST", "/api/sessions/one/plugins/installed", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"profile":"/foreign"}`, 400},
		{"installed plugins reject null", "POST", "/api/sessions/one/plugins/installed", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"installed plugins missing session", "POST", "/api/sessions/one/plugins/installed", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"startup settings need token", "POST", "/api/sessions/one/plugins/startup-settings", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"startup settings foreign origin", "POST", "/api/sessions/one/plugins/startup-settings", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"startup settings reject path override", "POST", "/api/sessions/one/plugins/startup-settings", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/foreign"}`, 400},
		{"startup settings reject null", "POST", "/api/sessions/one/plugins/startup-settings", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"startup settings missing session", "POST", "/api/sessions/one/plugins/startup-settings", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"plugin settings preview needs token", "POST", "/api/sessions/one/plugins/settings-preview", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"plugin settings preview foreign origin", "POST", "/api/sessions/one/plugins/settings-preview", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"plugin settings preview rejects path override", "POST", "/api/sessions/one/plugins/settings-preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"settings_file":"/foreign"}`, 400},
		{"plugin settings preview rejects null", "POST", "/api/sessions/one/plugins/settings-preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"plugin settings preview requires source and mode", "POST", "/api/sessions/one/plugins/settings-preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"workspace needs token", "GET", "/api/workspace", "127.0.0.1:9000", "", "", "", 401},
		{"workspace metadata", "GET", "/api/workspace", "127.0.0.1:9000", "", strings.Repeat("a", 64), "", 200},
		{"history needs token", "GET", "/api/sessions/one", "127.0.0.1:9000", "", "", "", 401},
		{"access preview needs token", "POST", "/api/sessions/one/access/preview", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"access preview foreign origin", "POST", "/api/sessions/one/access/preview", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"access preview cannot import native id", "POST", "/api/sessions/one/access/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"native_id":"external"}`, 400},
		{"access preview rejects null", "POST", "/api/sessions/one/access/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"access preview rejects supplied environment", "POST", "/api/sessions/one/access/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"executor":{"uid":"0"}}`, 400},
		{"native preview rejects supplied binding", "POST", "/api/native-sessions/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"context":{"binding":{"profile":"/foreign"}}}`, 400},
		{"bound observe rejects supplied environment", "POST", "/api/sessions/one/native/observe", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"executor":{"source":"codex.unix_peer"}}`, 400},
		{"native binding needs token", "POST", "/api/native-sessions/bind", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"native binding foreign origin", "POST", "/api/native-sessions/bind", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"native binding rejects route", "POST", "/api/native-sessions/bind", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"route_id":"other"}`, 400},
		{"native binding rejects null", "POST", "/api/native-sessions/bind", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"bound history rejects native override", "POST", "/api/sessions/one/native/history", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"native_id":"external"}`, 400},
		{"bound observe needs token", "POST", "/api/sessions/one/native/observe", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"bound observe foreign origin", "POST", "/api/sessions/one/native/observe", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"bound observe rejects identity override", "POST", "/api/sessions/one/native/observe", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"native_id":"foreign"}`, 400},
		{"bound observe rejects control", "POST", "/api/sessions/one/native/observe", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"resume":true}`, 400},
		{"bound observe rejects null", "POST", "/api/sessions/one/native/observe", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"bound history rejects null", "POST", "/api/sessions/one/native/history", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"native history needs token", "POST", "/api/native-sessions/history", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"native history foreign origin", "POST", "/api/native-sessions/history", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"native history rejects cwd", "POST", "/api/native-sessions/history", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/private"}`, 400},
		{"native history rejects route", "POST", "/api/native-sessions/history", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"route_id":"other"}`, 400},
		{"native history rejects null", "POST", "/api/native-sessions/history", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"native history rejects path id", "POST", "/api/native-sessions/history", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"native_id":"../../state"}`, 409},
		{"native discovery needs token", "POST", "/api/native-sessions/discover", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"native preview foreign origin", "POST", "/api/native-sessions/preview", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"native discovery rejects cwd", "POST", "/api/native-sessions/discover", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/private"}`, 400},
		{"native preview rejects route", "POST", "/api/native-sessions/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"route_id":"other"}`, 400},
		{"native discovery rejects null", "POST", "/api/native-sessions/discover", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"native preview rejects path id", "POST", "/api/native-sessions/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"native_id":"../../state"}`, 409},
		{"access preview missing session", "POST", "/api/sessions/one/access/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"execution sources need token", "GET", "/api/sessions/one/execution", "127.0.0.1:9000", "", "", "", 401},
		{"configuration catalog needs token", "POST", "/api/sessions/one/configuration/catalog", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"configuration catalog foreign origin", "POST", "/api/sessions/one/configuration/catalog", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"configuration catalog no overrides", "POST", "/api/sessions/one/configuration/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/other"}`, 400},
		{"configuration catalog rejects null", "POST", "/api/sessions/one/configuration/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `null`, 400},
		{"configuration catalog missing session", "POST", "/api/sessions/one/configuration/catalog", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{}`, 409},
		{"capabilities need token", "GET", "/api/capabilities", "127.0.0.1:9000", "", "", ``, 401},
		{"capability export needs token", "GET", "/api/capabilities/export", "127.0.0.1:9000", "", "", ``, 401},
		{"capability preview foreign origin", "POST", "/api/capabilities/preview", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"capability preview unknown fields", "POST", "/api/capabilities/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"exec":"anything"}`, 400},
		{"registration preview needs token", "POST", "/api/execution-profiles/preview", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"registration foreign origin", "POST", "/api/execution-profiles/preview", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"registration unknown fields", "POST", "/api/execution-profiles/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"cwd":"/tmp"}`, 400},
		{"diagnosis unknown fields", "POST", "/api/execution-profiles/diagnose", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"exec":"anything"}`, 400},
		{"execution preview needs token", "POST", "/api/sessions/one/execution/preview", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{}`, 401},
		{"preview foreign origin", "POST", "/api/sessions/one/execution/preview", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"preview rejects route switching", "POST", "/api/sessions/one/execution/preview", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"route_id":"other"}`, 400},
		{"invalid history limit", "GET", "/api/sessions/one?limit=all", "127.0.0.1:9000", "", strings.Repeat("a", 64), "", 400},
		{"valid read", "GET", "/api/snapshot", "127.0.0.1:9000", "", strings.Repeat("a", 64), "", 200},
		{"host rebinding", "GET", "/api/snapshot", "evil.test", "", strings.Repeat("a", 64), "", 403},
		{"foreign origin", "POST", "/api/commands", "127.0.0.1:9000", "https://evil.test", strings.Repeat("a", 64), `{}`, 403},
		{"login", "POST", "/api/login", "127.0.0.1:9000", "http://127.0.0.1:9000", "", `{"token":"` + strings.Repeat("a", 64) + `"}`, 200},
		{"arbitrary command", "POST", "/api/commands", "127.0.0.1:9000", "http://127.0.0.1:9000", strings.Repeat("a", 64), `{"command_id":"attempt","kind":"exec","text":"date"}`, 409},
		{"unknown fields", "POST", "/api/commands", "127.0.0.1:9000", "http://127.0.0.1:9000", strings.Repeat("a", 64), `{"command_id":"attempt","kind":"create","cwd":"/tmp"}`, 400},
		{"skill input rejects native path", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"skill","kind":"send","skill_input":{"snapshot_id":"one","items":[{"asset_id":"pack","name":"pack:method","path":"/foreign"}]}}`, 400},
		{"skill input rejects inline body", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"skill","kind":"send","skill_input":{"snapshot_id":"one","items":[{"asset_id":"pack","name":"pack:method","body":"unbound"}]}}`, 400},
		{"skill input rejects malformed items", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"skill","kind":"send","skill_input":{"snapshot_id":"one","items":{}}}`, 400},
		{"skill input requires saved session", "POST", "/api/commands", "127.0.0.1:9000", "", strings.Repeat("a", 64), `{"command_id":"skill","kind":"send","session_id":"missing","skill_input":{"snapshot_id":"one","items":[{"asset_id":"pack","name":"pack:method"}]}}`, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.name == "login" {
				cookies := w.Result().Cookies()
				if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
					t.Fatal("unsafe auth cookie")
				}
			}
		})
	}
}

func TestSSEBeginsWithSnapshotAndHasIndependentLifetime(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/events", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Result().Body)
	if w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(string(body), "event: snapshot") || !w.Flushed {
		t.Fatalf("missing stream synchronization: %s", body)
	}
}

func TestObservationCredentialHasNoConsoleAuthority(t *testing.T) {
	t.Setenv("NETCAP_HTTP_OBSERVER_TOKEN", strings.Repeat("b", 64))
	store, err := app.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := app.Config{Instances: []runtime.Instance{{ID: "codex", Kind: "codex"}}, Projects: []app.Project{{ID: "project", Cwd: t.TempDir()}}, Observers: []app.ObserverConfig{{ID: "hooks", InstanceID: "codex", ProjectID: "project", Format: "hook", TokenRef: "env:NETCAP_HTTP_OBSERVER_TOKEN"}}}
	a, err := app.Open(c, store, runtime.NewCodex())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := Handler(a, "127.0.0.1:9000", strings.Repeat("a", 64), fstest.MapFS{})
	body := `{"event_id":"first","occurred_at":"2026-09-29T00:00:00Z","payload":{"hook_event_name":"SessionStart","session_id":"observed"}}`
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"POST", "/api/observations/hooks", strings.Repeat("b", 64), body, 202},
		{"POST", "/api/observations/hooks", strings.Repeat("a", 64), body, 401},
		{"POST", "/api/observations/other", strings.Repeat("b", 64), body, 401},
		{"GET", "/api/workspace", strings.Repeat("b", 64), "", 401},
		{"GET", "/api/domain-events", strings.Repeat("b", 64), "", 401},
		{"POST", "/api/commands", strings.Repeat("b", 64), `{"command_id":"take","kind":"resume"}`, 401},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s returned %d: %s", tc.path, w.Code, w.Body.String())
		}
	}
	serialized, _ := json.Marshal(a.Snapshot())
	if strings.Contains(string(serialized), strings.Repeat("b", 64)) || len(a.Snapshot().Sessions) != 1 || a.Snapshot().Sessions[0].ControlMode != "observed" {
		t.Fatal("observation exposed credential or gained authority")
	}
	sid := a.Snapshot().Sessions[0].ID
	for _, path := range []string{"/api/workspace", "/api/sessions/" + sid, "/api/sessions/" + sid + "/access/preview"} {
		method, body := "GET", ""
		if strings.HasSuffix(path, "/preview") {
			method, body = "POST", "{}"
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:9000"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"source":"observation"`) || !strings.Contains(w.Body.String(), `"preview_available":false`) || !strings.Contains(w.Body.String(), `"basis":"recorded_context"`) || strings.Contains(w.Body.String(), `"executor":`) {
			t.Fatalf("HTTP source projection missing: %s %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestCapabilityPackageHTTPAuthorizationAndBodyLimits(t *testing.T) {
	h := testHandler(t)
	paths := []string{"/api/capabilities/package/export/preview", "/api/capabilities/package/export", "/api/capabilities/package/import/preview", "/api/capabilities/package/import"}
	for _, path := range paths {
		for _, mode := range []string{"unauthenticated", "foreign_origin", "unknown_fields"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				r := httptest.NewRequest("POST", "http://127.0.0.1:9000"+path, strings.NewReader(`{"unknown":true}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "http://127.0.0.1:9000")
				want := 401
				if mode != "unauthenticated" {
					r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
					want = 400
				}
				if mode == "foreign_origin" {
					r.Header.Set("Origin", "https://evil.test")
					want = 403
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("status %d expected %d", w.Code, want)
				}
			})
		}
	}
	for _, path := range append(paths, "/api/commands") {
		t.Run(path+"/body_limit", func(t *testing.T) {
			body := `{}`
			if path == "/api/capabilities/package/import" {
				body = `{"command_id":"test","kind":"import_capability_package"}`
			}
			r := httptest.NewRequest("POST", "http://127.0.0.1:9000"+path, strings.NewReader(body+strings.Repeat(" ", 1<<20)))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 400
			if strings.Contains(path, "/package/import") {
				want = 409
			}
			if w.Code != want {
				t.Fatalf("body limit status %d expected %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
}
