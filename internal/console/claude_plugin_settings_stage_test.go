package console

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPluginSettingsStageBoundary(t *testing.T) {
	h := testHandler(t)
	for _, tc := range []struct {
		name, token, origin, body string
		status                    int
	}{
		{"authorization", "", "", `{}`, 401},
		{"origin", strings.Repeat("a", 64), "https://foreign.test", `{}`, 403},
		{"null", strings.Repeat("a", 64), "", `null`, 400},
		{"path override", strings.Repeat("a", 64), "", `{"settings_file":"/foreign"}`, 400},
		{"client proof", strings.Repeat("a", 64), "", `{"validation":{"preservation":{"matches":true}}}`, 400},
		{"client snapshot", strings.Repeat("a", 64), "", `{"snapshot":{"schema_version":2}}`, 400},
		{"missing request", strings.Repeat("a", 64), "", `{}`, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:9000/api/sessions/one/plugins/settings-stage", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
