package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type healthRuntime struct{ *probeRuntime }

func (f *healthRuntime) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	r, err := f.probeRuntime.Execute(ctx, in)
	if err != nil || in.Kind != "send" || in.Scope.Purpose == "probe" {
		return r, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.views[r.NativeID]
	turn := runtime.Turn{ID: r.NativeTurn, Status: "completed", CompletedAt: now()}
	if kind, ok := strings.CutPrefix(in.Text, "fail:"); ok {
		turn.Status = "failed"
		turn.Failure = &runtime.Failure{Kind: kind, Source: "fixture.native.error"}
		if kind == "rate_limit" {
			turn.Failure.RetryAt = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
		}
	}
	v.Turns = append(v.Turns, turn)
	f.views[r.NativeID] = v
	return r, nil
}

func (f *healthRuntime) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	r, err := f.probeRuntime.Reconcile(ctx, in, receipt)
	for _, turn := range r.View.Turns {
		if turn.ID == receipt.NativeTurn && turn.Status == "failed" {
			r.Status = "failed"
		}
	}
	return r, err
}

func sendHealthFixture(t *testing.T, a *Application, session, id, body string) CommandReceipt {
	t.Helper()
	r, err := a.Apply(Command{ID: id, Kind: "send", SessionID: session, Text: body})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == r.OperationID {
				return terminal(op.Status)
			}
		}
		return false
	})
	return r
}

func TestRouteHealthUsesNativeOutcomesAndBlocksOnlyRelevantRoute(t *testing.T) {
	for _, tc := range []struct {
		kind, state string
		blocked     bool
	}{
		{"authentication", "attention", true}, {"quota", "attention", true},
		{"rate_limit", "backoff", true}, {"service", "backoff", true},
		{"network", "backoff", true}, {"tool", "unknown", false}, {"session", "unknown", false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			c := probeConfig(t)
			other := c.Routes[0]
			other.ID, other.Endpoint = "other", "http://127.0.0.1:9988/v1"
			c.Routes = append(c.Routes, other)
			f := &healthRuntime{&probeRuntime{fixtureRuntime: newFixture()}}
			a := openFixture(t, c, f)
			created := createFixture(t, a)
			sendHealthFixture(t, a, created.SessionID, "success", "Discuss the words rate_limit and unauthorized")
			status := a.Workspace().RouteStatuses
			if status[0].Health != "healthy" || status[0].Proof != "generation" || status[1].Health != "unknown" {
				t.Fatalf("model prose or other route contaminated health: %+v", status)
			}
			sendHealthFixture(t, a, created.SessionID, "failure", "fail:"+tc.kind)
			status = a.Workspace().RouteStatuses
			if status[0].Health != tc.state || status[0].ExpiresAt != "" || status[1].Health != "unknown" {
				t.Fatalf("failure hidden by old success: %+v", status)
			}
			_, err := a.Apply(Command{ID: "probe-blocked", Kind: "probe", RouteID: "route"})
			if (err != nil) != tc.blocked {
				t.Fatalf("probe admission: %v", err)
			}
			if tc.kind == "rate_limit" {
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				b := openFixture(t, c, f)
				if _, err := b.Apply(Command{ID: "restart-probe", Kind: "probe", RouteID: "route"}); err == nil {
					t.Fatal("restart discarded Retry-After")
				}
				if f.sends.Load() != 2 {
					t.Fatal("outer retry overlapped native operation")
				}
			}
		})
	}
}

func TestRouteHealthOldCompletionCannotHideNewFailure(t *testing.T) {
	c := probeConfig(t)
	f := &healthRuntime{&probeRuntime{fixtureRuntime: newFixture()}}
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	sendHealthFixture(t, a, created.SessionID, "success", "work")
	sendHealthFixture(t, a, created.SessionID, "failure", "fail:service")
	// Recovery can finish an earlier native request after a later request has
	// already failed. Feed that verified result through the same transaction.
	a.mu.Lock()
	next := clone(a.state)
	session := next.Sessions[created.SessionID]
	key, _ := healthKey(c.Instances[0], c.Routes[0])
	old := Operation{ID: "old-completion", CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
	a.recordRouteResult(&next, key, session, old, "generation", nil, "", "")
	err := a.commit(next, "fixture.late-result", session.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if a.Workspace().RouteStatuses[0].Health != "backoff" {
		t.Fatal("late older success hid newer failure")
	}
	// A later, real interactive success is sufficient to clear the restriction.
	sendHealthFixture(t, a, created.SessionID, "recovered", "work again")
	if a.Workspace().RouteStatuses[0].Health != "healthy" {
		t.Fatal("verified recovery did not refresh health")
	}
}

func TestRouteHealthBoundsConsecutiveRetriesAndRecoveryTTL(t *testing.T) {
	c := probeConfig(t)
	f := &healthRuntime{&probeRuntime{fixtureRuntime: newFixture()}}
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	for i := 0; i < 5; i++ {
		sendHealthFixture(t, a, created.SessionID, fmt.Sprintf("failure-%d", i), "fail:service")
	}
	if a.Workspace().RouteStatuses[0].Health != "attention" {
		t.Fatal("unbounded retry budget")
	}

	a.mu.Lock()
	next := clone(a.state)
	session := next.Sessions[created.SessionID]
	key, _ := healthKey(c.Instances[0], c.Routes[0])
	delete(next.RouteHealth, key)
	op := Operation{ID: "old", CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
	a.recordRouteResult(&next, key, session, op, "generation", nil, "", "")
	err := a.commit(next, "fixture.old-history", session.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if a.Workspace().RouteStatuses[0].Health != "stale" {
		t.Fatal("recovered old history obtained a fresh TTL")
	}
}

func TestRouteHealthLocalFailureCannotLiftAccountRestriction(t *testing.T) {
	c := probeConfig(t)
	f := &healthRuntime{&probeRuntime{fixtureRuntime: newFixture()}}
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	for _, kind := range []string{"authentication", "tool", "service"} {
		sendHealthFixture(t, a, created.SessionID, kind, "fail:"+kind)
		status := a.Workspace().RouteStatuses[0]
		if status.Health != "attention" || status.FailureKind != "authentication" {
			t.Fatalf("unresolved account restriction lifted: %+v", status)
		}
	}
}
