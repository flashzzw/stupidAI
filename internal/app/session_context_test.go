package app

import (
	"context"
	"encoding/json"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestSessionContextSeparatesSavedBindingFromRegistry(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	sid := createFixture(t, a).SessionID
	a.mu.Lock()
	session := a.state.Sessions[sid]
	session.Scope.Instance.Name = "saved name"
	session.View.Executor = &runtime.ExecutorEvidence{Source: "controlled-peer", PID: 123, UID: "1000", OS: "linux", WSLDistro: "Ubuntu-fixture", At: "2026-10-01T00:00:00Z"}
	a.state.Sessions[sid] = session
	original := session.Scope.Instance.Profile
	a.config.Instances[0].Profile = "/different/current/profile"
	a.mu.Unlock()
	workspace := a.Workspace()
	detail, err := a.ReadSession(sid, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []Session{workspace.Sessions[0], detail.Session} {
		ctx := view.Access.Context
		if ctx.Binding.Profile != original || ctx.Binding.Name != "saved name" || ctx.RegistryStatus != "changed" || ctx.Executor == nil || ctx.Executor.PID != 123 {
			t.Fatalf("binding projection: %+v", ctx)
		}
		if ctx.Binding.Permission != session.Permission {
			t.Fatal("permission projection changed saved choice")
		}
	}
	detail.Session.Access.Context.Binding.Profile = "caller mutation"
	again, _ := a.ReadSession(sid, "", 20)
	if again.Session.Access.Context.Binding.Profile != original {
		t.Fatal("caller changed saved binding")
	}
	state, err := a.store.Load()
	if err != nil || state.Sessions[sid].Access != nil {
		t.Fatal("display context persisted as authority")
	}
}

func TestSessionContextDoesNotPromoteEventsOrDefinitionMatches(t *testing.T) {
	c := fixtureConfig(t)
	a := openFixture(t, c, newFixture())
	for _, scenario := range []string{"same", "renamed", "missing", "event", "event_console_origin", "legacy", "probe"} {
		t.Run(scenario, func(t *testing.T) {
			s := Session{Origin: "console", Permission: "workspace", Scope: runtime.Scope{Instance: c.Instances[0]}, View: runtime.View{Executor: &runtime.ExecutorEvidence{Source: "untrusted-event", UID: "0"}}}
			switch scenario {
			case "renamed":
				s.Scope.Instance.Name = "older name"
			case "missing":
				s.Scope.Instance.ID = "missing"
			case "event":
				s.Origin = "hooks"
				s.ControlMode = "observed"
			case "event_console_origin":
				s.ControlMode = "observed"
			case "legacy":
				s.Origin = ""
			case "probe":
				s.Origin = "probe"
			}
			ctx := a.sessionContext(s)
			want := "matching"
			if scenario == "missing" {
				want = "missing"
			}
			if scenario == "probe" {
				want = "isolated"
			}
			if ctx.RegistryStatus != want {
				t.Fatalf("registry context: %+v", ctx)
			}
			if scenario == "event" || scenario == "event_console_origin" || scenario == "legacy" {
				if ctx.Executor != nil || ctx.Binding.Permission != "unverified" || ctx.Basis != "recorded_context" {
					t.Fatal("event or legacy data claimed executor identity")
				}
			}
		})
	}
}

func TestAccessPreviewShowsNewBackendSampleWithoutPersistingIt(t *testing.T) {
	f := &accessRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, fixtureConfig(t), f)
	sid := createFixture(t, a).SessionID
	f.fresh = func(_ context.Context, scope runtime.Scope) (runtime.View, error) {
		return runtime.View{NativeID: scope.NativeID, NativeStatus: "idle", Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true}, Executor: &runtime.ExecutorEvidence{PID: 456, UID: "1001", Source: "controlled-peer", At: "2026-10-01T01:00:00Z"}}, nil
	}
	preview, err := a.PreviewSessionAccess(context.Background(), sid)
	if err != nil || preview.Error != "" || preview.Access.Context.Executor == nil || preview.Access.Context.Executor.PID != 456 {
		t.Fatalf("preview backend sample missing: %+v %v", preview, err)
	}
	state, err := a.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Sessions[sid].View.Executor != nil {
		t.Fatal("readonly preview persisted backend evidence")
	}
	data, _ := json.Marshal(preview)
	if !json.Valid(data) {
		t.Fatal("invalid context response")
	}
}
