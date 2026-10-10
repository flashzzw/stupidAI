package app

import (
	"context"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type incompatibleRuntime struct{ *observerSpy }

func (f *incompatibleRuntime) Describe(_ context.Context, instance runtime.Instance) (runtime.Description, error) {
	return runtime.Description{Kind: instance.Kind, Version: "future-version", Compatibility: "unverified", Capabilities: map[string]string{"create": "unverified", "send": "unverified"}}, nil
}

func TestUnverifiedInstancePreservesHistoryAndDeniesNativeWork(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	spy := &incompatibleRuntime{&observerSpy{fixtureRuntime: f}}
	b := openFixture(t, c, spy)
	read, err := b.ReadSession(created.SessionID, "", 50)
	if err != nil || read.Session.Connection != "incompatible" || read.Session.Status != "unknown" {
		t.Fatalf("version change was not exposed: %+v %v", read, err)
	}
	for _, cmd := range []Command{
		{ID: "unsupported-create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"},
		{ID: "unsupported-send", Kind: "send", SessionID: created.SessionID, Text: "must not send"},
		{ID: "unsupported-resume", Kind: "resume", SessionID: created.SessionID},
	} {
		if _, err := b.Apply(cmd); err == nil {
			t.Fatalf("unverified capability admitted %s", cmd.Kind)
		}
	}
	if _, err := b.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if spy.observations.Load() != 0 || f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("unverified CLI received native work")
	}
	if b.Workspace().Instances[0].Description.Version != "future-version" || b.Snapshot().Sessions[0].Scope.NativeID != read.Session.Scope.NativeID {
		t.Fatal("missing compatibility/native identity")
	}
}
