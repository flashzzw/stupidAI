package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type fixtureRuntime struct {
	mu             sync.Mutex
	views          map[string]runtime.View
	sends          atomic.Int32
	creates        atomic.Int32
	before         func(runtime.Intent)
	block          chan struct{}
	unknown        bool
	observeBlock   chan struct{}
	observeStarted chan struct{}
	resolve        func(context.Context, runtime.Intent, runtime.Receipt) (runtime.Resolution, error)
}

func newFixture() *fixtureRuntime { return &fixtureRuntime{views: map[string]runtime.View{}} }

// The base application fixture has no native project hook sources. Tests of
// source discovery use an explicit reader or the real Codex adapter.
func (f *fixtureRuntime) ReadCodexProjectHookSources(_ context.Context, scope runtime.Scope) (*runtime.CodexProjectHookSources, error) {
	return &runtime.CodexProjectHookSources{Version: 1, Source: "codex.config/read+hooks/list", Cwd: scope.Cwd, Profile: scope.Instance.Profile, Files: []runtime.CodexProjectHookFile{}}, nil
}

// Fake establishment evidence is confined to application state tests. Native
// source/proof validation is exercised with the actual adapter elsewhere.
func (f *fixtureRuntime) ReadCodexHookSourceProof(_ context.Context, scope, target runtime.Scope, prior *runtime.CodexHookSourceProof) (*runtime.CodexHookSourceProof, error) {
	if prior != nil {
		return queryCopy(prior), nil
	}
	return &runtime.CodexHookSourceProof{Version: 1, ScopeHash: digest(scope), Fingerprint: strings.Repeat("1", 64), RequirementsHash: strings.Repeat("2", 64), At: "2026-10-06T00:00:00Z"}, nil
}
func (f *fixtureRuntime) Describe(_ context.Context, instance runtime.Instance) (runtime.Description, error) {
	return runtime.Description{Kind: instance.Kind, Version: "fixture", Compatibility: "supported", Capabilities: map[string]string{
		"create": "supported", "send": "supported", "approval": "supported", "question": "supported", "interrupt": "supported", "resume": "supported", "recovery": "supported", "history": "supported",
	}}, nil
}
func (f *fixtureRuntime) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if f.before != nil {
		f.before(in)
	}
	if in.Kind == "create" {
		f.creates.Add(1)
		id := "native-" + in.ID
		f.mu.Lock()
		f.views[id] = runtime.View{NativeID: id, Status: "idle", Messages: []runtime.Message{}, Pending: []runtime.Pending{}}
		f.mu.Unlock()
		return runtime.Receipt{NativeID: id}, nil
	}
	f.sends.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return runtime.Receipt{NativeID: in.Scope.NativeID}, ctx.Err()
		}
	}
	if f.unknown {
		return runtime.Receipt{NativeID: in.Scope.NativeID}, errors.New("fixture lost receipt")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.views[in.Scope.NativeID]
	v.Messages = append(v.Messages, runtime.Message{ID: in.ID + "-user", TurnID: in.ID, Role: "user", Text: in.Text, Status: "completed", Operation: in.ID},
		runtime.Message{ID: in.ID + "-answer", TurnID: in.ID, Role: "assistant", Text: "reply " + in.Text, Status: "completed"})
	f.views[in.Scope.NativeID] = v
	return runtime.Receipt{NativeID: in.Scope.NativeID, NativeTurn: in.ID}, nil
}
func (f *fixtureRuntime) Observe(_ context.Context, scope runtime.Scope) (runtime.View, error) {
	f.mu.Lock()
	v := f.views[scope.NativeID]
	v.Messages = append([]runtime.Message{}, v.Messages...)
	f.mu.Unlock()
	if f.observeStarted != nil {
		select {
		case f.observeStarted <- struct{}{}:
		default:
		}
		<-f.observeBlock
	}
	return v, nil
}
func (f *fixtureRuntime) ObserveFresh(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	return f.Observe(ctx, scope)
}
func (f *fixtureRuntime) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if f.resolve != nil {
		return f.resolve(ctx, in, receipt)
	}
	v, _ := f.Observe(ctx, in.Scope)
	if f.unknown {
		return runtime.Resolution{State: "unknown", View: v}, nil
	}
	return runtime.Resolution{State: "finished", Status: "succeeded", Receipt: receipt, View: v, Evidence: runtime.Evidence{Complete: true, Source: "fixture completed execution"}}, nil
}
func (f *fixtureRuntime) Close() error { return nil }

func fixtureConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("model = 'fixture'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Version: 1, StateDir: filepath.Join(root, "state"), Projects: []Project{{ID: "project", Cwd: root}},
		Instances: []runtime.Instance{{ID: "instance", Kind: "codex", Profile: root}},
		Routes:    []runtime.Route{{ID: "route", InstanceID: "instance", Model: "fixture"}}}
}
func openFixture(t *testing.T, c Config, f runtime.Adapter) *Application {
	t.Helper()
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(c, store, f)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
func waitTest(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("application did not reach expected observable state")
}
func createFixture(t *testing.T, a *Application) CommandReceipt {
	t.Helper()
	r, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	return r
}

func TestDurableIntentAndIdempotentConversation(t *testing.T) {
	c := fixtureConfig(t)
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture()
	f.before = func(in runtime.Intent) {
		s, err := store.Load()
		if err != nil {
			t.Error(err)
			return
		}
		if op, ok := s.Operations[in.ID]; !ok || op.Status != "dispatching" {
			t.Errorf("runtime called before durable intent: %+v", op)
		}
	}
	a, err := Open(c, store, f)
	if err != nil {
		t.Fatal(err)
	}
	r := createFixture(t, a)
	for _, text := range []string{"first", "second"} {
		cmd := Command{ID: text, Kind: "send", SessionID: r.SessionID, Text: text}
		first, err := a.Apply(cmd)
		if err != nil {
			t.Fatal(err)
		}
		second, err := a.Apply(cmd)
		if err != nil || first != second {
			t.Fatalf("duplicate receipt: %v", err)
		}
		waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	}
	if f.sends.Load() != 2 {
		t.Fatal("duplicate runtime sends")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.before = nil
	a = openFixture(t, c, f)
	s := a.Snapshot().Sessions[0]
	if s.Scope.NativeID == "" || len(s.View.Messages) != 4 {
		t.Fatalf("history/identity lost: %+v", s)
	}
	if _, err = a.Apply(Command{ID: "second", Kind: "send", SessionID: r.SessionID, Text: "second"}); err != nil {
		t.Fatal(err)
	}
	if f.sends.Load() != 2 {
		t.Fatal("restart repeated command")
	}
	if _, err = a.Apply(Command{ID: "second", Kind: "send", SessionID: r.SessionID, Text: "changed"}); err == nil {
		t.Fatal("conflicting id accepted")
	}
}

type failingStore struct {
	Persistence
	fail atomic.Bool
}

func (s *failingStore) Save(v State) error {
	if s.fail.Load() {
		return errors.New("fixture disk failure")
	}
	return s.Persistence.Save(v)
}

func TestPersistenceFailurePreventsRuntimeWrite(t *testing.T) {
	c := fixtureConfig(t)
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s := &failingStore{Persistence: store}
	f := newFixture()
	a, err := Open(c, s, f)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s.fail.Store(true)
	_, err = a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
	if err == nil || f.creates.Load() != 0 || len(a.Snapshot().Sessions) != 0 {
		t.Fatal("failed commit dispatched or became visible")
	}
}

func TestSlowSendPauseAndUnknownRemainOccupied(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	f.block = make(chan struct{})
	f.unknown = true
	a := openFixture(t, c, f)
	r := createFixture(t, a)
	if _, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: r.SessionID, Text: "one side effect"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 1 })
	start := time.Now()
	if _, err := a.Apply(Command{ID: "pause", Kind: "pause", SessionID: r.SessionID}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 500*time.Millisecond || !a.Snapshot().Sessions[0].Paused {
		t.Fatal("pause blocked behind runtime IO")
	}
	close(f.block)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "unknown" })
	for _, kind := range []string{"send", "resume"} {
		if _, err := a.Apply(Command{ID: "retry-" + kind, Kind: kind, SessionID: r.SessionID, Text: "retry"}); err == nil {
			t.Fatalf("%s cleared unknown", kind)
		}
	}
	if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: r.SessionID}); err != nil {
		t.Fatal(err)
	}
	if !a.Snapshot().Sessions[0].Paused || f.sends.Load() != 1 {
		t.Fatal("late receipt or takeover restarted execution")
	}
}

func TestStoreRejectsSecondWriterAndLegacyDirectory(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if second, err := OpenStore(dir); err == nil {
		second.Close()
		t.Fatal("second writer admitted")
	}
	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if second, err := OpenStore(legacy); err == nil {
		second.Close()
		t.Fatal("legacy directory admitted")
	}
}

func TestReadOnlyObservationDoesNotRejectIdleSend(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	f.observeStarted = make(chan struct{}, 1)
	f.observeBlock = make(chan struct{})
	a := openFixture(t, c, f)
	r := createFixture(t, a)
	select {
	case <-f.observeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("observation did not start")
	}
	_, err := a.Apply(Command{ID: "send-during-read", Kind: "send", SessionID: r.SessionID, Text: "hello"})
	close(f.observeBlock)
	if err != nil {
		t.Fatalf("idle send rejected because of read-only observation: %v", err)
	}
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].Status == "idle" && len(a.Snapshot().Sessions[0].View.Messages) == 2
	})
	if f.sends.Load() != 1 {
		t.Fatal("read/write overlap duplicated native execution")
	}
}
