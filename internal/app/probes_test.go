package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type probeRuntime struct {
	*fixtureRuntime
	badOutput bool
}

type rejectedCreateRuntime struct{ *probeRuntime }

func (f *rejectedCreateRuntime) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind == "create" && in.Scope.Purpose != "probe" {
		return runtime.Receipt{}, &runtime.NotSent{Reason: "fixture route credential mismatch before native create"}
	}
	return f.probeRuntime.Execute(ctx, in)
}

func TestRejectedNativeCreateDoesNotKeepAutomaticCapacity(t *testing.T) {
	c := probeConfig(t)
	f := &rejectedCreateRuntime{&probeRuntime{fixtureRuntime: newFixture()}}
	a := openFixture(t, c, f)
	r, err := a.Apply(Command{ID: "rejected", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == r.OperationID {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	if _, err := a.Apply(Command{ID: "probe-after-rejected-create", Kind: "probe", RouteID: "route"}); err != nil {
		t.Fatalf("confirmed no native create still reserves capacity: %v", err)
	}
}

type timeoutProbeRuntime struct {
	*probeRuntime
	stops    atomic.Int32
	released atomic.Bool
}

func (f *timeoutProbeRuntime) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind == "interrupt" {
		f.stops.Add(1)
		return runtime.Receipt{NativeID: in.Scope.NativeID, NativeTurn: in.NativeTurn}, nil
	}
	r, err := f.probeRuntime.Execute(ctx, in)
	if err == nil && in.Kind == "send" {
		f.mu.Lock()
		v := f.views[r.NativeID]
		v.Turns[len(v.Turns)-1].Status = "inProgress"
		v.Turns[len(v.Turns)-1].CompletedAt = ""
		v.Status, v.ExecutionClear = "running", false
		f.views[r.NativeID] = v
		f.mu.Unlock()
	}
	return r, err
}

func (f *timeoutProbeRuntime) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	v, err := f.Observe(ctx, in.Scope)
	r := runtime.Resolution{State: "running", Receipt: receipt, View: v, Evidence: runtime.Evidence{Complete: true, Source: "fixture.native.execution"}}
	if f.released.Load() {
		f.mu.Lock()
		v = f.views[in.Scope.NativeID]
		v.Status, v.ExecutionClear = "idle", true
		for n := range v.Turns {
			v.Turns[n].Status, v.Turns[n].CompletedAt = "interrupted", now()
		}
		f.views[in.Scope.NativeID] = v
		f.mu.Unlock()
		r.View, r.State, r.Status = v, "finished", "interrupted"
		if in.Kind == "interrupt" {
			r.Status = "succeeded"
		}
	}
	return r, err
}

func TestProbeTimeoutWaitsForNativeScopeAndDoesNotRepeatStop(t *testing.T) {
	c := probeConfig(t)
	c.Automation.ProbeTimeoutSeconds = 1
	other := c.Routes[0]
	other.ID = "other"
	c.Routes = append(c.Routes, other)
	f := &timeoutProbeRuntime{probeRuntime: &probeRuntime{fixtureRuntime: newFixture()}}
	a := openFixture(t, c, f)
	if _, err := a.Apply(Command{ID: "probe", Kind: "probe", RouteID: "route"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.stops.Load() == 1 })
	if _, err := a.Apply(Command{ID: "overlap", Kind: "probe", RouteID: "other"}); err == nil {
		t.Fatal("timeout released active native scope")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, f)
	if _, err := b.Apply(Command{ID: "restart-overlap", Kind: "probe", RouteID: "other"}); err == nil {
		t.Fatal("restart released timed-out native execution")
	}
	f.released.Store(true)
	waitTest(t, func() bool { p := b.Workspace().RouteStatuses[0].Probe; return p != nil && p.Status == "failed" })
	status := b.Workspace().RouteStatuses[0]
	if status.Health != "unknown" || status.FailureKind != "timeout" || f.stops.Load() != 1 || f.sends.Load() != 1 {
		t.Fatalf("timeout retried or invented provider failure: %+v", status)
	}
	if _, err := b.Apply(Command{ID: "after-release", Kind: "probe", RouteID: "other"}); err != nil {
		t.Fatalf("verified scope did not release capacity: %v", err)
	}
}

func (f *probeRuntime) Describe(ctx context.Context, instance runtime.Instance) (runtime.Description, error) {
	d, err := f.fixtureRuntime.Describe(ctx, instance)
	d.Capabilities["probe"] = "supported"
	d.Capabilities["route_evidence"] = "supported"
	return d, err
}
func (f *probeRuntime) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	receipt, err := f.fixtureRuntime.Execute(ctx, in)
	if err != nil {
		return receipt, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	view := f.views[receipt.NativeID]
	view.ExecutionClear = true
	view.Evidence = runtime.Evidence{Complete: true, Source: "fixture native result"}
	if in.Kind == "send" && in.Scope.Purpose == "probe" {
		timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		output := "__NETCAP_UTC__0 " + timestamp + "\n"
		zero := 0
		if f.badOutput {
			output = "__NETCAP_UTC__1 \n"
		}
		view.Turns = append(view.Turns, runtime.Turn{ID: receipt.NativeTurn, Status: "completed", CompletedAt: now()})
		if in.Text == runtime.ProbePrompt("text") {
			view.Messages[len(view.Messages)-1].Text = "NETCAP_GENERATION_OK"
		} else {
			view.Messages[len(view.Messages)-1].Text = timestamp
			view.Tools = append(view.Tools, runtime.ToolResult{ID: "command-" + in.ID, TurnID: receipt.NativeTurn, Command: runtime.DateCommand, Output: output, ExitCode: &zero, Complete: true})
		}
	}
	f.views[receipt.NativeID] = view
	return receipt, nil
}

func probeConfig(t *testing.T) Config {
	c := fixtureConfig(t)
	c.Instances[0].ServerExecutable = "/fixture/server"
	c.Routes[0].CredentialRef = "env:NETCAP_PROBE_TEST_KEY"
	c.Routes[0].ConfigRevision = "1"
	c.Routes[0].CredentialRevision = "1"
	t.Setenv("NETCAP_PROBE_TEST_KEY", "fixture-credential")
	return c
}

func TestProbeOperationCoalescingTTLAndRestart(t *testing.T) {
	c := probeConfig(t)
	f := &probeRuntime{fixtureRuntime: newFixture()}
	block := make(chan struct{})
	f.block = block
	a := openFixture(t, c, f)
	first, err := a.Apply(Command{ID: "probe-first", Kind: "probe", RouteID: "route"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 1 })
	second, err := a.Apply(Command{ID: "probe-second", Kind: "probe", RouteID: "route"})
	if err != nil || second.SessionID != first.SessionID || second.Status != "coalesced" {
		t.Fatalf("probe duplicated: %+v %v", second, err)
	}
	if _, err := a.Apply(Command{ID: "inject", Kind: "send", SessionID: first.SessionID, Text: "other work"}); err == nil {
		t.Fatal("interactive input injected into probe")
	}
	close(block)
	waitTest(t, func() bool { return a.Workspace().RouteStatuses[0].Health == "healthy" })
	status := a.Workspace().RouteStatuses[0]
	if status.Proof != "utc-date" || status.Probe == nil || status.Probe.Status != "succeeded" || status.Probe.SendOperationID == "" {
		t.Fatalf("missing probe evidence: %+v", status)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, f)
	cached, err := b.Apply(Command{ID: "probe-after-restart", Kind: "probe", RouteID: "route"})
	if err != nil || cached.Status != "cached" || f.sends.Load() != 1 || f.creates.Load() != 1 {
		t.Fatalf("fresh health replayed execution: %+v %v", cached, err)
	}
}

func TestProbeUnknownAndHumanOccupancyRetainCapacity(t *testing.T) {
	c := probeConfig(t)
	c.Routes = append(c.Routes, runtime.Route{ID: "other", InstanceID: "instance", Model: "fixture", CredentialRef: "env:NETCAP_PROBE_TEST_KEY", ConfigRevision: "1", CredentialRevision: "1"})
	f := &probeRuntime{fixtureRuntime: newFixture()}
	f.unknown = true
	a := openFixture(t, c, f)
	if _, err := a.Apply(Command{ID: "unknown-probe", Kind: "probe", RouteID: "route"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		return a.Workspace().RouteStatuses[0].Probe != nil && a.Workspace().RouteStatuses[0].Probe.Status == "unknown"
	})
	if _, err := a.Apply(Command{ID: "overlap", Kind: "probe", RouteID: "other"}); err == nil {
		t.Fatal("unknown probe released capacity")
	}
	if f.sends.Load() != 1 {
		t.Fatal("unknown input was retried")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, f)
	if _, err := b.Apply(Command{ID: "overlap-restart", Kind: "probe", RouteID: "other"}); err == nil {
		t.Fatal("restart released unknown capacity")
	}
	b.Close()

	c = probeConfig(t)
	f = &probeRuntime{fixtureRuntime: newFixture()}
	a = openFixture(t, c, f)
	manual := createFixture(t, a)
	if _, err := a.Apply(Command{ID: "human", Kind: "takeover", SessionID: manual.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "probe-human", Kind: "probe", RouteID: "route"}); err == nil || !strings.Contains(err.Error(), "人工接管") {
		t.Fatalf("human ownership ignored: %v", err)
	}
}

func TestProbeAssertionFailureIsNotMaskedByModelCompletion(t *testing.T) {
	c := probeConfig(t)
	f := &probeRuntime{fixtureRuntime: newFixture(), badOutput: true}
	a := openFixture(t, c, f)
	if _, err := a.Apply(Command{ID: "bad-command", Kind: "probe", RouteID: "route"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { p := a.Workspace().RouteStatuses[0].Probe; return p != nil && p.Status == "failed" })
	status := a.Workspace().RouteStatuses[0]
	if status.Health != "unknown" || !strings.Contains(status.Reason, "退出码") {
		t.Fatalf("bad tool result became healthy: %+v", status)
	}
}

func TestProbeTextProofTTLExpiryAndDateUpgrade(t *testing.T) {
	c := probeConfig(t)
	f := &probeRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	if _, err := a.Apply(Command{ID: "text-first", Kind: "probe", RouteID: "route", ProbeMode: "text"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Workspace().RouteStatuses[0].Health == "healthy" })
	if a.Workspace().RouteStatuses[0].Proof != "text" {
		t.Fatal("text probe claimed tool evidence")
	}
	if cached, err := a.Apply(Command{ID: "text-cache", Kind: "probe", RouteID: "route", ProbeMode: "text"}); err != nil || cached.Status != "cached" {
		t.Fatalf("text proof not reused: %+v %v", cached, err)
	}
	if upgrade, err := a.Apply(Command{ID: "date-upgrade", Kind: "probe", RouteID: "route"}); err != nil || upgrade.Status == "cached" {
		t.Fatalf("text proof substituted for tools: %+v %v", upgrade, err)
	}
	waitTest(t, func() bool { return a.Workspace().RouteStatuses[0].Proof == "utc-date" })
	status := a.Workspace().RouteStatuses[0]
	success, _ := time.Parse(time.RFC3339Nano, status.LastSuccessAt)
	expires, _ := time.Parse(time.RFC3339Nano, status.ExpiresAt)
	if expires.Sub(success) != 120*time.Second {
		t.Fatal("default health TTL is not 120 seconds")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Resume with expired persisted evidence, avoiding a dependency on the
	// scheduler fitting both cache assertions inside a one-second TTL window.
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for key, health := range state.RouteHealth {
		health.LastSuccessAt = success.Add(-time.Hour).Format(time.RFC3339Nano)
		health.ExpiresAt = expires.Add(-time.Hour).Format(time.RFC3339Nano)
		state.RouteHealth[key] = health
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	a = openFixture(t, c, f)
	if a.Workspace().RouteStatuses[0].Health != "stale" {
		t.Fatal("expired persisted health was refreshed by restart")
	}
	if fresh, err := a.Apply(Command{ID: "after-expiry", Kind: "probe", RouteID: "route"}); err != nil || fresh.Status == "cached" {
		t.Fatalf("expired proof reused: %+v %v", fresh, err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 3 })
}

func TestProbeCompletedSessionTakeoverStillBlocksFreshProbe(t *testing.T) {
	c := probeConfig(t)
	c.Automation.ProbeTTLSeconds = 1
	f := &probeRuntime{fixtureRuntime: newFixture()}
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f.observeBlock, f.observeStarted = release, make(chan struct{}, 1)
	a := openFixture(t, c, f)
	r, err := a.Apply(Command{ID: "probe", Kind: "probe", RouteID: "route"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		select {
		case <-f.observeStarted:
			return true
		default:
			return false
		}
	})
	f.mu.Lock()
	var completed time.Time
	for _, view := range f.views {
		for _, turn := range view.Turns {
			completed, _ = time.Parse(time.RFC3339Nano, turn.CompletedAt)
		}
	}
	f.mu.Unlock()
	if completed.IsZero() {
		t.Fatal("fixture did not complete native input before observation")
	}
	// Delay the first observation across the configured TTL, as can happen
	// when a control connection stalls after the native model has finished.
	time.Sleep(time.Until(completed.Add(1100 * time.Millisecond)))
	releaseOnce.Do(func() { close(release) })
	waitTest(t, func() bool {
		probe := a.Workspace().RouteStatuses[0].Probe
		return probe != nil && probe.Status == "succeeded"
	})
	if a.Workspace().RouteStatuses[0].Health != "stale" {
		t.Fatal("late observation reset the native success TTL")
	}
	if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: r.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "fresh", Kind: "probe", RouteID: "route"}); err == nil || !strings.Contains(err.Error(), "人工接管") {
		t.Fatalf("completed probe hid human occupancy: %v", err)
	}
	if f.sends.Load() != 1 {
		t.Fatal("probe wrote alongside human owner")
	}
}

type probeFailureStore struct {
	Persistence
	stage string
}

func (s *probeFailureStore) Save(state State) error {
	if len(state.Probes) > 0 {
		if s.stage == "create" {
			return errors.New("fixture probe transaction failure")
		}
		for _, probe := range state.Probes {
			if probe.SendOperationID != "" {
				return errors.New("fixture probe send transaction failure")
			}
		}
	}
	return s.Persistence.Save(state)
}

func TestProbeTransactionFailurePreventsNativeDispatch(t *testing.T) {
	for _, stage := range []string{"create", "send"} {
		t.Run(stage, func(t *testing.T) {
			c := probeConfig(t)
			store, err := OpenStore(c.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			f := &probeRuntime{fixtureRuntime: newFixture()}
			a, err := Open(c, &probeFailureStore{Persistence: store, stage: stage}, f)
			if err != nil {
				store.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			_, err = a.Apply(Command{ID: "probe", Kind: "probe", RouteID: "route"})
			if stage == "create" && err == nil {
				t.Fatal("create commit succeeded unexpectedly")
			}
			waitTest(t, func() bool { return a.Snapshot().Error != "" })
			if f.sends.Load() != 0 || stage == "create" && f.creates.Load() != 0 {
				t.Fatal("native mutation preceded durable probe state")
			}
		})
	}
}

func TestProbePauseBeforeFirstInputAndCapacityBoundaries(t *testing.T) {
	t.Run("pause-before-input", func(t *testing.T) {
		c := probeConfig(t)
		f := &probeRuntime{fixtureRuntime: newFixture()}
		started, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		f.before = func(in runtime.Intent) {
			if in.Kind == "create" {
				close(started)
				<-release
			}
		}
		a := openFixture(t, c, f)
		r, err := a.Apply(Command{ID: "probe", Kind: "probe", RouteID: "route"})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		if _, err := a.Apply(Command{ID: "pause", Kind: "pause", SessionID: r.SessionID}); err != nil {
			t.Fatal(err)
		}
		// Let the native create receipt arrive only after pause is durable.
		release <- struct{}{}
		waitTest(t, func() bool { p := a.Workspace().RouteStatuses[0].Probe; return p != nil && p.Status == "cancelled" })
		if f.sends.Load() != 0 {
			t.Fatal("pause lost priority to automatic first input")
		}
	})
	t.Run("two-routes", func(t *testing.T) {
		c := probeConfig(t)
		c.Automation.GlobalLimit = 2
		for _, id := range []string{"second", "third"} {
			route := c.Routes[0]
			route.ID = id
			c.Routes = append(c.Routes, route)
		}
		f := &probeRuntime{fixtureRuntime: newFixture()}
		f.block = make(chan struct{})
		defer close(f.block)
		a := openFixture(t, c, f)
		for _, route := range []string{"route", "second"} {
			if _, err := a.Apply(Command{ID: route, Kind: "probe", RouteID: route}); err != nil {
				t.Fatal(err)
			}
		}
		waitTest(t, func() bool { return f.sends.Load() == 2 })
		if _, err := a.Apply(Command{ID: "third", Kind: "probe", RouteID: "third"}); err == nil {
			t.Fatal("global probe capacity exceeded")
		}
		if _, err := a.Apply(Command{ID: "mode-change", Kind: "probe", RouteID: "route", ProbeMode: "text"}); err == nil {
			t.Fatal("mode change replaced an active native probe")
		}
	})
}
