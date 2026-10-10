package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapabilityHookInspectionHTTPAdmission(t *testing.T) {
	h := testHandler(t)
	for _, tc := range []struct {
		name, body, origin string
		authorized         bool
		status             int
	}{
		{"unauthenticated", `{}`, "http://127.0.0.1:9000", false, 401},
		{"foreign-origin", `{}`, "https://other.invalid", true, 403},
		{"null", `null`, "", true, 400},
		{"array", `[]`, "", true, 400},
		{"executable-override", `{"server_executable":"/foreign"}`, "", true, 400},
		{"source-override", `{"source_path":"/foreign"}`, "", true, 400},
		{"trust-override", `{"trusted":true}`, "", true, 400},
		{"trailing-object", `{} {}`, "", true, 400},
		{"unregistered", `{"asset_id":"missing","revision":"v1","source_sha256":"none","instance_id":"missing"}`, "", true, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/capabilities/codex-hooks", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.authorized {
				r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCapabilityHookReviewRejectsBrowserNativeKeysAndInternalBindings(t *testing.T) {
	h := testHandler(t)
	for _, body := range []string{
		`{"command_id":"review","kind":"create","capability_selections":[{"asset_id":"a","mode":"enabled","hook_review":{"inspection_id":"x","trust":"trusted","native_key":"foreign"}}]}`,
		`{"command_id":"review","kind":"create","capability_selections":[{"asset_id":"a","mode":"enabled","hook_review":{"inspection_id":"x","trust":"trusted","definition_hash":"foreign"}}]}`,
		`{"command_id":"review","kind":"create","capability_selections":[{"asset_id":"a","mode":"enabled","hookBinding":{}}]}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/commands", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("browser attempted to replace server-owned review: status=%d", w.Code)
		}
	}
}
