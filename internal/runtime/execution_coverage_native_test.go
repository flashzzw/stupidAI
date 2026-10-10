//go:build linux && nativecodex

package runtime_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeExecutionSourceCoverage(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0209-source-coverage-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: filepath.Join(root, "work")}}}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	checks, responses := map[string]bool{}, map[string]any{}
	write := func(path string, value any) {
		t.Helper()
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = a.Close()
		_ = f.Runtime.Close()
		write(filepath.Join(root, "result.json"), map[string]any{"checks": checks, "responses": responses, "processes": f.Records, "passed": !t.Failed(), "scope": "real native create then closed connection; read-only Handler file coverage without native refresh or model input"})
	})
	check := func(name string, passed bool) {
		t.Helper()
		checks[name] = passed
		if !passed {
			t.Fatal(name)
		}
	}
	created, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "work", RouteID: f.Route.ID, Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	s := waitNativeApplication(t, a, func(s app.Snapshot) bool {
		return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].View.ExecutionClear
	}).Sessions[0]
	check("real_identity_and_observation", s.ID == created.SessionID && s.Scope.NativeID != "" && s.View.Evidence.At != "")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := f.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	baseline := a.Snapshot().Sessions[0]
	handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
	export := func(phase string) app.ExecutionRead {
		t.Helper()
		rows := map[string]json.RawMessage{}
		for _, path := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + s.ID, "/api/sessions/" + s.ID + "/execution"} {
			req := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
			}
			rows[path] = append(json.RawMessage(nil), response.Body.Bytes()...)
		}
		responses[phase] = rows
		var read app.ExecutionRead
		if err := json.Unmarshal(rows["/api/sessions/"+s.ID+"/execution"], &read); err != nil {
			t.Fatal(err)
		}
		check(phase+"_same_snapshot", reflect.DeepEqual(read.Snapshot, baseline.ExecutionSnapshot))
		check(phase+"_observation_not_refreshed", reflect.DeepEqual(read.NativeEvidence, baseline.View.Evidence))
		check(phase+"_limited_coverage", read.SourceCoverage.Scope == "recorded_sources_only")
		check(phase+"_no_model_input", len(a.Snapshot().Sessions[0].View.Messages) == 0 && len(a.Snapshot().Operations) == 1)
		for _, source := range read.Sources {
			found := false
			for _, area := range read.SourceCoverage.Areas {
				if area.ID == source.Group {
					found = true
				}
			}
			check(phase+"_group_"+source.Group, found)
		}
		return read
	}
	initial := export("initial")
	check("initial_unchanged", initial.Status == "unchanged")
	hooks := filepath.Join(f.Instance.Profile, "hooks.json")
	write(hooks, map[string]any{"hooks": map[string]any{}})
	drift := export("drift")
	check("new_user_file_detected", drift.Status == "drifted")
	if err := os.Remove(hooks); err != nil {
		t.Fatal(err)
	}
	check("restored_unchanged", export("restored").Status == "unchanged")
	after, err := f.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	check("no_native_request_during_reads", string(before.Trace) == string(after.Trace))
	check("no_state_changes", reflect.DeepEqual(baseline, a.Snapshot().Sessions[0]))
}
