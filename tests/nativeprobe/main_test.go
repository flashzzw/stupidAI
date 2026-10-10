package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

func fixtureManifest(t *testing.T) (manifest, string) {
	t.Helper()
	root, err := os.MkdirTemp("", "netcap-native-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NETCAP_NATIVE_PROBE_CODEX_A", "fixture-A")
	m := manifest{Root: root, Cases: []probeCase{{Label: "A", Instance: runtime.Instance{ID: "fixture", Kind: "codex", Profile: profile, Executable: "/fixture/codex", ServerExecutable: "/fixture/server"}, Route: runtime.Route{ID: "route", InstanceID: "fixture", Model: "gpt-6-astra", Provider: "fixture", Endpoint: "http://127.0.0.1:12345/v1", CredentialRef: "env:NETCAP_NATIVE_PROBE_CODEX_A", CredentialRevision: "1", ConfigRevision: "1"}}}}
	return m, filepath.Join(root, "fixture.json")
}

func TestManifestAcceptsOnlyOwnedLocalSyntheticCases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*manifest)
		valid  bool
	}{
		{"valid", func(*manifest) {}, true},
		{"external-endpoint", func(m *manifest) { m.Cases[0].Route.Endpoint = "https://example.com/v1" }, false},
		{"userinfo", func(m *manifest) { m.Cases[0].Route.Endpoint = "http://token@127.0.0.1:12345/v1" }, false},
		{"real-credential-ref", func(m *manifest) { m.Cases[0].Route.CredentialRef = "env:REAL_KEY" }, false},
		{"outside-profile", func(m *manifest) { m.Cases[0].Instance.Profile = t.TempDir() }, false},
		{"escaping-instance", func(m *manifest) { m.Cases[0].Instance.ID = "../../other"; m.Cases[0].Route.InstanceID = "../../other" }, false},
		{"duplicate", func(m *manifest) { m.Cases = append(m.Cases, m.Cases[0]) }, false},
		{"unknown-scenario", func(m *manifest) { m.Cases[0].Scenario = "pretend-success" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, path := fixtureManifest(t)
			tc.mutate(&m)
			b, _ := json.Marshal(m)
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := loadManifest(path)
			if (err == nil) != tc.valid {
				t.Fatalf("manifest validation: %v", err)
			}
		})
	}
}

func TestFailureChecksNeedCompleteCorrelatedNativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*runtime.Resolution)
		valid  bool
	}{
		{"valid", func(*runtime.Resolution) {}, true},
		{"unknown", func(r *runtime.Resolution) { r.Status = "uncertain" }, false},
		{"wrong-turn", func(r *runtime.Resolution) { r.View.Turns[0].ID = "another" }, false},
		{"incomplete", func(r *runtime.Resolution) { r.Evidence.Complete = false }, false},
		{"occupied", func(r *runtime.Resolution) { r.View.ExecutionClear = false }, false},
		{"unclassified", func(r *runtime.Resolution) { r.View.Turns[0].Failure.Kind = "unknown" }, false},
		{"no-source", func(r *runtime.Resolution) { r.View.Turns[0].Failure.Source = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runtime.Resolution{State: "finished", Status: "failed", Evidence: runtime.Evidence{Complete: true}, View: runtime.View{Evidence: runtime.Evidence{Complete: true}, ExecutionClear: true, Turns: []runtime.Turn{{ID: "turn", Status: "failed", Failure: &runtime.Failure{Kind: "authentication", Source: "fixture.native.error"}}}}}
			tc.mutate(&r)
			_, err := verifyResult(probeCase{Scenario: "authentication"}, "utc-date", r, runtime.Receipt{NativeTurn: "turn"}, time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("failure evidence acceptance: %v", err)
			}
		})
	}
}

func TestToolFailureCannotMaskProviderFaultOrMissingCommand(t *testing.T) {
	r := runtime.Resolution{State: "finished", Status: "interrupted", Evidence: runtime.Evidence{Complete: true}, View: runtime.View{Evidence: runtime.Evidence{Complete: true}, ExecutionClear: true, Turns: []runtime.Turn{{ID: "turn", Status: "interrupted"}}, Tools: []runtime.ToolResult{{ID: "tool", TurnID: "turn", Command: "exit 7"}}}}
	check := func() error {
		_, err := verifyResult(probeCase{Scenario: "tool_failure"}, "utc-date", r, runtime.Receipt{NativeTurn: "turn"}, time.Now())
		return err
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	r.View.Turns[0].Failure = &runtime.Failure{Kind: "service", Source: "fixture.native.error"}
	if check() == nil {
		t.Fatal("provider fault accepted as intended tool failure")
	}
	r.View.Turns[0].Failure = nil
	r.View.Tools = nil
	if check() == nil {
		t.Fatal("missing tool evidence accepted")
	}
}
