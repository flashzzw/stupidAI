package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type accessRuntime struct {
	*fixtureRuntime
	fresh func(context.Context, runtime.Scope) (runtime.View, error)
	calls atomic.Int32
}

func (f *accessRuntime) ObserveFresh(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	f.calls.Add(1)
	if f.fresh != nil {
		return f.fresh(ctx, scope)
	}
	return f.fixtureRuntime.ObserveFresh(ctx, scope)
}

func TestSessionAccessDoesNotPromoteObservationEvents(t *testing.T) {
	c := observationConfig(t)
	f := &accessRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	r, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), ObservationDelivery{
		EventID: "source", OccurredAt: "2026-10-01T00:00:00Z",
		Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"external-native","origin":"console","control_mode":"managed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	sid := r.SessionIDs[0]
	before := a.Snapshot()
	for _, s := range []Session{before.Sessions[0], a.Workspace().Sessions[0]} {
		if s.Access == nil || s.Access.Source != "observation" || s.Access.PreviewAvailable || s.Access.Capabilities["live_input"].Status != "unsupported" {
			t.Fatalf("event claimed native access: %+v", s.Access)
		}
	}
	detail, err := a.ReadSession(sid, "", 10)
	if err != nil || detail.Session.Access.Source != "observation" {
		t.Fatalf("detail lost provenance: %+v %v", detail, err)
	}
	preview, err := a.PreviewSessionAccess(context.Background(), sid)
	if err != nil || preview.Error == "" || !preview.ReadOnly || preview.Access.Capabilities["live_observe"].Status != "unsupported" {
		t.Fatalf("observation preview: %+v %v", preview, err)
	}
	if f.calls.Load() != 0 || f.creates.Load() != 0 || f.sends.Load() != 0 || a.Sequence() != before.Sequence {
		t.Fatal("reading event provenance touched native runtime or persistent state")
	}
	for _, kind := range []string{"send", "resume", "takeover", "respond", "interrupt"} {
		if _, err := a.Apply(Command{ID: kind, Kind: kind, SessionID: sid, Text: "must not execute"}); err == nil {
			t.Fatalf("event preview granted %s", kind)
		}
	}
	detail.Session.Access.Capabilities["live_input"] = AccessCapability{Status: "supported"}
	again, _ := a.ReadSession(sid, "", 10)
	if again.Session.Access.Capabilities["live_input"].Status != "unsupported" {
		t.Fatal("caller mutated access authority")
	}
	state, err := a.store.Load()
	if err != nil || state.Sessions[sid].Access != nil {
		t.Fatal("query access projection was persisted")
	}
}

func TestSessionAccessPreviewKeepsIdentityAndControl(t *testing.T) {
	for _, scenario := range []string{"idle", "inflight", "approval", "incomplete", "wrong-id", "disconnected", "notLoaded", "sdk_offline"} {
		t.Run(scenario, func(t *testing.T) {
			f := &accessRuntime{fixtureRuntime: newFixture()}
			a := openFixture(t, fixtureConfig(t), f)
			sid := createFixture(t, a).SessionID
			if _, err := a.Apply(Command{ID: "manual", Kind: "takeover", SessionID: sid}); err != nil {
				t.Fatal(err)
			}
			before := a.Snapshot()
			s := before.Sessions[0]
			f.fresh = func(_ context.Context, scope runtime.Scope) (runtime.View, error) {
				if !scope.NoAutoResume || scope.NativeID != s.Scope.NativeID || scope.Instance.ID != s.Scope.Instance.ID || scope.StateNamespace != s.Scope.StateNamespace {
					t.Error("preview changed native identity or allowed automatic resume")
				}
				view := runtime.View{NativeID: scope.NativeID, Status: "idle", ExecutionClear: true,
					Evidence: runtime.Evidence{Complete: true, Source: "controlled fixture", At: "2026-10-01T00:00:00Z"}}
				switch scenario {
				case "inflight":
					view.Executions = []runtime.Execution{{ID: "tool", Status: "running"}}
				case "approval":
					view.Pending = []runtime.Pending{{ID: "approval", Status: "pending"}}
				case "incomplete":
					view.Evidence.Complete = false
				case "wrong-id":
					view.NativeID = "different-native"
				case "disconnected":
					return view, errors.New("native read unavailable")
				case "notLoaded", "sdk_offline":
					view.NativeStatus = scenario
				}
				return view, nil
			}
			preview, err := a.PreviewSessionAccess(context.Background(), sid)
			if err != nil || !preview.ReadOnly || preview.SessionID != sid || preview.Generation != s.Generation {
				t.Fatalf("preview: %+v %v", preview, err)
			}
			want := "unavailable"
			if scenario == "idle" {
				want = "conditional"
			}
			resumeWant := want
			if scenario == "notLoaded" || scenario == "sdk_offline" {
				resumeWant = "conditional"
				if preview.Access.Capabilities["live_observe"].Status != "unavailable" {
					t.Fatal("offline history was labeled as live observation")
				}
			}
			if preview.Access.Capabilities["live_input"].Status != want || preview.Access.Capabilities["resume"].Status != resumeWant {
				t.Fatalf("preview invented input availability: %+v", preview)
			}
			if (scenario == "wrong-id" || scenario == "incomplete" || scenario == "disconnected") && (preview.Error == "" || preview.Evidence.Complete) {
				t.Fatal("invalid evidence accepted")
			}
			after := a.Snapshot()
			if f.calls.Load() != 1 || f.creates.Load() != 1 || f.sends.Load() != 0 || len(after.Operations) != len(before.Operations) ||
				after.Sessions[0].Generation != s.Generation || !after.Sessions[0].Paused || after.Sessions[0].ControlMode != "human_takeover" || after.Sessions[0].Scope.NativeID != s.Scope.NativeID {
				t.Fatal("preview changed control, identity or native input count")
			}
		})
	}
}

func TestSessionAccessPreviewLosesRaceToTakeover(t *testing.T) {
	f := &accessRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, fixtureConfig(t), f)
	sid := createFixture(t, a).SessionID
	started, release := make(chan struct{}), make(chan struct{})
	f.fresh = func(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return runtime.View{}, ctx.Err()
		}
		return runtime.View{NativeID: scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true}}, nil
	}
	done := make(chan SessionAccessPreview, 1)
	go func() { result, _ := a.PreviewSessionAccess(context.Background(), sid); done <- result }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("preview did not start")
	}
	if _, err := a.Apply(Command{ID: "manual", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := <-done
	if result.Error == "" || result.Evidence.Complete || result.Access.Capabilities["resume"].Status != "unavailable" || !a.Snapshot().Sessions[0].Paused || f.sends.Load() != 0 {
		t.Fatalf("stale preview survived newer input ownership: %+v", result)
	}
}

func TestSessionAccessPreviewRejectsChangedConfigurationBeforeNativeRead(t *testing.T) {
	c := fixtureConfig(t)
	f := &accessRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	sid := createFixture(t, a).SessionID
	if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("model = 'changed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.PreviewSessionAccess(context.Background(), sid)
	if err != nil || result.Error == "" || result.Evidence.Complete || result.Access.Capabilities["resume"].Status != "unavailable" || f.calls.Load() != 0 {
		t.Fatalf("changed binding reached native runtime: %+v %v", result, err)
	}
}

func TestSessionAccessDoesNotInventLegacyOrProbeControl(t *testing.T) {
	c := fixtureConfig(t)
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state := newState()
	for _, origin := range []string{"legacy-import", "probe"} {
		state.Sessions[origin] = Session{ID: origin, Origin: origin, Scope: runtime.Scope{Instance: c.Instances[0]}, Paused: true}
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	f := &accessRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	for _, s := range a.Workspace().Sessions {
		if s.Access.PreviewAvailable {
			t.Fatal("missing native identity granted preview")
		}
		if s.Origin == "probe" && s.Access.Capabilities["live_input"].Status != "unsupported" || s.Origin != "probe" && s.Access.Source != "unknown" {
			t.Fatalf("invented provenance: %+v", s.Access)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("listing records called the native runtime")
	}
}
