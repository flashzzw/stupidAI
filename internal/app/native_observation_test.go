package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

type nativeObservationRuntime struct {
	*bindingRuntime
	read func(context.Context, runtime.Scope) (runtime.View, error)
}

func (f *nativeObservationRuntime) ObserveNativeExecution(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	return f.read(ctx, scope)
}

func TestBoundNativeObservationKeepsIdentityAndDoesNotGrantControl(t *testing.T) {
	for _, scenario := range []string{"running", "idle", "queued", "not_loaded", "unknown", "incomplete", "disconnected", "foreign_id", "config_before", "config_during", "generation_during", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &nativeObservationRuntime{bindingRuntime: &bindingRuntime{fixtureRuntime: newFixture()}}
			a := openFixture(t, c, f)
			r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
			if err != nil {
				t.Fatal(err)
			}
			before := a.Snapshot()
			calls := 0
			f.read = func(ctx context.Context, s runtime.Scope) (runtime.View, error) {
				calls++
				if !s.NoAutoResume || s.NativeID != discoveredID || s.Instance.ID != "instance" || s.Cwd != c.Projects[0].Cwd || s.StateNamespace != c.Instances[0].Profile || s.Route.ID != "" {
					t.Fatal("observation changed binding or allowed resume")
				}
				v := runtime.View{NativeID: s.NativeID, NativeStatus: "active", Status: "running", TerminalCommand: "must never be exposed", Evidence: runtime.Evidence{Complete: true, Source: "fixture", At: now()}, Tools: []runtime.ToolResult{{ID: "original", Output: "original output"}}}
				switch scenario {
				case "idle":
					v.NativeStatus, v.Status, v.ExecutionClear = "idle", "idle", true
				case "queued":
					v.NativeStatus, v.Status = "idle", "running"
					v.Executions = []runtime.Execution{{ID: "q", Kind: "queue", Status: "queued"}}
				case "not_loaded":
					v.NativeStatus, v.Status = "notLoaded", "idle"
				case "unknown":
					v.NativeStatus, v.Status = "unknown", "idle"
				case "incomplete":
					v.Evidence.Complete = false
				case "disconnected":
					return v, fmt.Errorf("owned daemon disconnected")
				case "foreign_id":
					v.NativeID = "foreign"
				case "config_during":
					if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("changed"), 0600); err != nil {
						t.Fatal(err)
					}
				case "generation_during":
					a.mu.Lock()
					s := a.state.Sessions[r.SessionID]
					s.Generation++
					a.state.Sessions[s.ID] = s
					a.mu.Unlock()
				}
				return v, ctx.Err()
			}
			if scenario == "config_before" {
				if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			out, err := a.ObserveBoundNative(ctx, r.SessionID)
			if scenario == "config_before" || scenario == "config_during" || scenario == "generation_during" || scenario == "cancelled" {
				if err == nil {
					t.Fatal("changed observation accepted")
				}
				if scenario == "config_before" && calls != 0 {
					t.Fatal("changed binding reached native runtime")
				}
				return
			}
			if err != nil || !out.ReadOnly || out.SessionID != r.SessionID || out.View.TerminalCommand != "" {
				t.Fatalf("observation: %+v %v", out, err)
			}
			observed := scenario == "running" || scenario == "idle" || scenario == "queued"
			if (out.Status == "observed") != observed {
				t.Fatalf("wrong availability: %+v", out)
			}
			if !observed && out.Error == "" {
				t.Fatal("unavailable execution lacks reason")
			}
			if scenario == "not_loaded" && !strings.Contains(out.Error, "未加载") {
				t.Fatal("unloaded executor lost its specific diagnosis")
			}
			if scenario == "foreign_id" && len(out.View.Tools) > 0 {
				t.Fatal("foreign execution data exposed")
			}
			if digest(before) != digest(a.Snapshot()) {
				t.Fatal("observation mutated saved session or operation state")
			}
			if _, err := a.Apply(Command{ID: "still-no-input", Kind: "send", SessionID: r.SessionID, Text: "forbidden"}); err == nil {
				t.Fatal("observation granted input authority")
			}
		})
	}
}

func TestBoundNativeObservationRejectsUnboundAndUnsupportedSources(t *testing.T) {
	f := &bindingRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, fixtureConfig(t), f)
	if _, err := a.ObserveBoundNative(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown source accepted")
	}
	r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ObserveBoundNative(context.Background(), r.SessionID); err == nil {
		t.Fatal("ordinary adapter substituted for read observer")
	}
	if f.observations.Load() != 0 {
		t.Fatal("unsupported observer launched ordinary observe")
	}
}

func TestClaudeRosterSampleNeverBecomesExecutionOrInputEvidence(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f := &nativeObservationRuntime{bindingRuntime: &bindingRuntime{fixtureRuntime: newFixture()}}
	f.read = func(_ context.Context, s runtime.Scope) (runtime.View, error) {
		return runtime.View{NativeID: s.NativeID, NativeStatus: "roster_only", Status: "idle", ExecutionClear: true, TerminalCommand: "must-not-expose", Evidence: runtime.Evidence{Complete: true, Source: "claude.agents_json"}, AgentRoster: &runtime.AgentRosterStatus{Kind: "background", State: "done", Status: "idle", JobID: "12345678"}}, nil
	}
	a := openFixture(t, c, f)
	r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	before := digest(a.Snapshot())
	out, err := a.ObserveBoundNative(context.Background(), r.SessionID)
	if err != nil || out.Status != "sampled" || out.Error != "" || out.View.Status != "unknown" || out.View.Evidence.Complete || out.View.ExecutionClear || out.View.TerminalCommand != "" {
		t.Fatalf("sample granted execution evidence: %+v %v", out, err)
	}
	if before != digest(a.Snapshot()) {
		t.Fatal("sample mutated saved state")
	}
	for _, kind := range []string{"send", "resume", "reply", "interrupt"} {
		if _, err := a.Apply(Command{ID: "forbidden-" + kind, Kind: kind, SessionID: r.SessionID}); err == nil {
			t.Fatalf("sample granted %s", kind)
		}
	}
	if f.creates.Load() != 0 || f.sends.Load() != 0 || f.observations.Load() != 0 {
		t.Fatal("roster sampling dispatched ordinary execution")
	}
}
