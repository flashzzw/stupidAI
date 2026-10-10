package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlRejectsBrowserAndMalformedCommands(t *testing.T) {
	in := make(chan request)
	h := handler(in)
	cases := []struct {
		method, path, body, origin string
		status                     int
	}{{"POST", "/v1/command", `{"action":"pause"}`, "https://untrusted.example", 403}, {"GET", "/v1/command", "", "", 405}, {"POST", "/v1/command", `{"action":"pause","shell":"rm"}`, "", 400}, {"POST", "/v1/command", `{"action":"pause"} {}`, "", 400}, {"GET", "/missing", "", "", 404}}
	for _, tt := range cases {
		r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("%s %s: %d", tt.method, tt.path, w.Code)
		}
	}
}
func TestControlSerializesCommandThroughOwner(t *testing.T) {
	in := make(chan request)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := <-in
		if r.command.Action != "pause" {
			t.Error("wrong action")
		}
		r.reply <- response{body: []byte(`{"running":false}`)}
	}()
	w := httptest.NewRecorder()
	handler(in).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/command", strings.NewReader(`{"action":"pause"}`)))
	<-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), "false") {
		t.Fatal(w.Body.String())
	}
}
