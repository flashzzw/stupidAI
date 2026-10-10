package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/events"
	"codex-netcap/internal/runtime"
)

type observerSpy struct {
	*fixtureRuntime
	observations atomic.Int32
}

func (s *observerSpy) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	s.observations.Add(1)
	return s.fixtureRuntime.Observe(ctx, scope)
}

func observationConfig(t *testing.T) Config {
	c := fixtureConfig(t)
	c.Observers = []ObserverConfig{{ID: "local-hooks", InstanceID: "instance", ProjectID: "project", Format: "hook", TokenRef: "env:NETCAP_OBSERVATION_TEST_TOKEN"}}
	t.Setenv("NETCAP_OBSERVATION_TEST_TOKEN", strings.Repeat("b", 64))
	return c
}

func TestObservationsAreDurableReadOnlyAndOrdered(t *testing.T) {
	c := observationConfig(t)
	f := &observerSpy{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	delivery := ObservationDelivery{EventID: "request-one", OccurredAt: "2026-09-29T00:00:01Z", Payload: json.RawMessage(`{"hook_event_name":"PermissionRequest","session_id":"native-one","request_id":"request","connection_id":"connection","message":"same text","control_mode":"managed","operation_id":"invented"}`)}
	if _, err := a.IngestObservation("local-hooks", "wrong", delivery); err == nil {
		t.Fatal("observation admitted without source credential")
	}
	r, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), delivery)
	if err != nil {
		t.Fatal(err)
	}
	seq := a.Sequence()
	if duplicate, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), delivery); err != nil || duplicate.ID != r.ID || a.Sequence() != seq {
		t.Fatal("observation retry changed state")
	}
	sid := r.SessionIDs[0]
	session := a.Snapshot().Sessions[0]
	if session.ControlMode != "observed" || session.Connection != "observed" || !session.Paused || session.Scope.Route.ID != "" || session.View.ExecutionClear {
		t.Fatalf("observation invented control/route/evidence: %+v", session)
	}
	for _, kind := range []string{"send", "respond", "interrupt", "pause", "resume", "takeover", "reconcile", "associate"} {
		if _, err := a.Apply(Command{ID: kind, Kind: kind, SessionID: sid, Text: "must not execute", PendingID: session.View.Pending[0].ID}); err == nil {
			t.Fatalf("observation upgraded through %s", kind)
		}
	}
	delivery.EventID, delivery.OccurredAt, delivery.Payload = "activity", "2026-09-29T00:00:03Z", json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"native-one"}`)
	if _, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), delivery); err != nil {
		t.Fatal(err)
	}
	delivery.EventID, delivery.OccurredAt, delivery.Payload = "late-prompt", "2026-09-29T00:00:02Z", json.RawMessage(`{"hook_event_name":"PermissionRequest","session_id":"native-one","message":"late"}`)
	if _, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), delivery); err != nil {
		t.Fatal(err)
	}
	if current := a.Snapshot().Sessions[0]; current.Status != "running" || current.View.Pending[0].Status != "expired" {
		t.Fatal("late observation revived stale prompt")
	}
	for _, event := range a.DomainEvents(sid) {
		if event.Kind == events.PermissionRequired && event.Active {
			t.Fatal("stale permission event remains eligible for notification")
		}
	}
	time.Sleep(650 * time.Millisecond)
	if f.observations.Load() != 0 || f.sends.Load() != 0 || f.creates.Load() != 0 {
		t.Fatal("read-only observations accessed native runtime")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, newFixture())
	if len(b.DomainEvents(sid)) != 3 || b.Snapshot().Sessions[0].ControlMode != "observed" {
		t.Fatal("observations lost on restart")
	}
}

func TestObservedManagedSessionKeepsRuntimeAuthority(t *testing.T) {
	c := observationConfig(t)
	c.Instances[0].NotificationSender = "console"
	f := newFixture()
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	sent, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: created.SessionID, Text: "original"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before := a.Snapshot().Sessions[0]
	var turn string
	for _, op := range a.Snapshot().Operations {
		if op.ID == sent.OperationID {
			turn = op.Receipt.NativeTurn
		}
	}
	delivery := ObservationDelivery{EventID: "runtime-overlap", OccurredAt: now(), Payload: json.RawMessage(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"turn_id":%q,"message":"hook result"}`, before.Scope.NativeID, turn))}
	r, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), delivery)
	if err != nil {
		t.Fatal(err)
	}
	after := a.Snapshot().Sessions[0]
	if after.ControlMode != before.ControlMode || after.Generation != before.Generation || after.View.Messages[1].Text != before.View.Messages[1].Text {
		t.Fatal("hook observation changed managed runtime state")
	}
	var event events.Envelope
	for _, candidate := range a.DomainEvents(created.SessionID) {
		if candidate.Kind == events.TurnCompleted {
			event = candidate
		}
	}
	if !event.Managed || event.OperationID != sent.OperationID || event.NotificationSender != "console" || r.SessionIDs[0] != created.SessionID || len(event.Sources) != 2 {
		t.Fatalf("native turn was not associated with original operation: %+v", event)
	}
	delivery.EventID = "wrong-project"
	delivery.Payload = json.RawMessage(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"cwd":"/"}`, before.Scope.NativeID))
	if _, err := a.IngestObservation("local-hooks", strings.Repeat("b", 64), delivery); err == nil {
		t.Fatal("observer wrote outside configured project")
	}
}
