package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These are reduced captures from real CLI runs. Only timestamps in text and
// the owned rollout path are rebased; no commands or native results are made up.
// Control-plane responses below are fixtures, not live lifecycle evidence.
func nativeDateCapture(t *testing.T, name, captured, now, profile string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "native-date", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.NewReplacer(captured, now, "@PROFILE@", filepath.ToSlash(profile)).Replace(string(b)))
}

func checkDateReplay(t *testing.T, view View, turn string, at time.Time, complete bool) {
	t.Helper()
	proof, err := VerifyProbe(view, turn, "utc-date", at)
	if complete {
		if err != nil || proof != at.Format(time.RFC3339) {
			t.Fatalf("captured native date not verified: proof=%q err=%v view=%+v", proof, err, view)
		}
		if len(view.Tools) != 1 || view.Tools[0].TurnID != turn || view.Tools[0].Command != DateCommand {
			t.Fatalf("native call identity/command lost: %+v", view.Tools)
		}
	} else if err == nil {
		t.Fatal("final timestamp without complete native tool output was accepted")
	}
}

func TestCapturedNativeDateCodex(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("tool-result=%t", complete), func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			profile := t.TempDir()
			scope.Instance.Profile, scope.Cwd, scope.Purpose = profile, profile, "probe"
			at := time.Now().UTC().Truncate(time.Second)
			var thread struct {
				Thread map[string]any `json:"thread"`
			}
			if err := json.Unmarshal(nativeDateCapture(t, "codex-thread.json", "2026-09-29T08:31:54Z", at.Format(time.RFC3339), profile), &thread); err != nil {
				t.Fatal(err)
			}
			scope.NativeID = thread.Thread["id"].(string)
			turn := thread.Thread["turns"].([]any)[0].(map[string]any)["id"].(string)
			rollout := nativeDateCapture(t, "codex-rollout.jsonl", "2026-09-29T08:31:54Z", at.Format(time.RFC3339), profile)
			if !complete {
				lines := strings.Split(strings.TrimSpace(string(rollout)), "\n")
				rollout = []byte(strings.Join(lines[:len(lines)-1], "\n") + "\n")
			}
			if err := os.WriteFile(filepath.Join(profile, "rollout.jsonl"), rollout, 0600); err != nil {
				t.Fatal(err)
			}
			f.call = func(method string, _ map[string]any) (any, error) {
				if method == "thread/read" {
					return thread, nil
				}
				return emptyControl(method)
			}
			view, err := c.Observe(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			checkDateReplay(t, view, turn, at, complete)
		})
	}
}

func TestCapturedNativeDateClaude(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("tool-result=%t", complete), func(t *testing.T) {
			at := time.Now().UTC().Truncate(time.Second)
			var snapshot claudeSnapshot
			if err := json.Unmarshal(nativeDateCapture(t, "claude-journal.json", "2026-09-29T08:31:56Z", at.Format(time.RFC3339), ""), &snapshot.Entries); err != nil {
				t.Fatal(err)
			}
			snapshot.Live = true
			turn := snapshot.Entries[1].Operation
			if !complete {
				entries := snapshot.Entries
				snapshot.Entries = nil
				for _, entry := range entries {
					if entry.Event.Type == "user" && entry.Event.UUID != turn {
						continue
					}
					snapshot.Entries = append(snapshot.Entries, entry)
				}
			}
			view := projectClaude(Scope{}, snapshot)
			checkDateReplay(t, view, turn, at, complete)
		})
	}
}

type captureTransport func(*http.Request) (*http.Response, error)

func (f captureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCapturedNativeDateOpenCode(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("tool-result=%t", complete), func(t *testing.T) {
			at := time.Now().UTC().Truncate(time.Second)
			profile := t.TempDir()
			t.Setenv("NETCAP_TEST_OPENCODE_KEY", "fixture")
			if err := os.WriteFile(filepath.Join(profile, "opencode.json"), []byte(`{"provider":{"fixture":{"options":{}}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			var messages []opencodeMessage
			history := nativeDateCapture(t, "opencode-history.json", "2026-09-29T08:31:59Z", at.Format(time.RFC3339), profile)
			if err := json.Unmarshal(history, &messages); err != nil {
				t.Fatal(err)
			}
			scope := Scope{Instance: Instance{ID: "capture", Kind: "opencode", Profile: profile}, Route: Route{Provider: "fixture", Model: "fixture-model", CredentialRef: "env:NETCAP_TEST_OPENCODE_KEY"}, NativeID: messages[0].Info.SessionID, Cwd: profile}
			if !complete {
				// Keep the native call and final answer but remove the tool result.
				var raw []map[string]any
				if err := json.Unmarshal(history, &raw); err != nil {
					t.Fatal(err)
				}
				for _, message := range raw {
					for _, part := range message["parts"].([]any) {
						p := part.(map[string]any)
						if p["type"] == "tool" {
							state := p["state"].(map[string]any)
							state["status"] = "running"
							delete(state, "output")
							delete(state, "metadata")
						}
					}
				}
				history, _ = json.Marshal(raw)
			}
			transport := captureTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					return nil, fmt.Errorf("unexpected native write: %s", r.Method)
				}
				body := ""
				switch r.URL.Path {
				case "/config":
					body = `{"provider":{"fixture":{"options":{"baseURL":"","apiKey":"fixture"}}}}`
				case "/session/" + scope.NativeID:
					b, _ := json.Marshal(map[string]string{"id": scope.NativeID, "directory": profile})
					body = string(b)
				case "/session/" + scope.NativeID + "/message":
					body = string(history)
				case "/session/status", "/mcp":
					body = `{}`
				case "/permission", "/question", "/pty", "/session/" + scope.NativeID + "/children":
					body = `[]`
				default:
					return nil, fmt.Errorf("unexpected native request: %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			_, digest, err := opencodeConfig(scope)
			if err != nil {
				t.Fatal(err)
			}
			c := NewOpenCode(t.TempDir())
			c.servers[opencodeKey(scope)] = &opencodeServer{record: opencodeServerRecord{PID: os.Getpid(), Birth: processBirth(os.Getpid()), URL: "http://capture.invalid", Digest: digest}, root: t.TempDir(), cwd: profile, http: &http.Client{Transport: transport}, parts: map[string]map[string]opencodePart{}, seen: map[string]bool{}}
			view, err := c.Observe(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			checkDateReplay(t, view, messages[0].Info.ID, at, complete)
		})
	}
}
