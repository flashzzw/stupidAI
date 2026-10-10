package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type bindingRuntime struct {
	*fixtureRuntime
	inspections  atomic.Int32
	observations atomic.Int32
	histories    atomic.Int32
	inspect      func(runtime.DiscoveryRequest, string) (runtime.NativeSession, error)
}

func (f *bindingRuntime) Discover(context.Context, runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
	return runtime.DiscoveryPage{}, fmt.Errorf("not used")
}
func (f *bindingRuntime) InspectNative(_ context.Context, r runtime.DiscoveryRequest, id string) (runtime.NativeSession, error) {
	f.inspections.Add(1)
	if f.inspect != nil {
		return f.inspect(r, id)
	}
	return runtime.NativeSession{NativeID: id, Cwd: r.Cwd, Title: "original", Source: "cli", NativeStatus: "active"}, nil
}
func (f *bindingRuntime) Observe(context.Context, runtime.Scope) (runtime.View, error) {
	f.observations.Add(1)
	return runtime.View{}, fmt.Errorf("native registration must not auto-load or control")
}
func (f *bindingRuntime) ObserveFresh(ctx context.Context, s runtime.Scope) (runtime.View, error) {
	return f.Observe(ctx, s)
}
func (f *bindingRuntime) ReadNativeHistory(_ context.Context, r runtime.DiscoveryRequest, id string) (runtime.NativeHistorySnapshot, error) {
	f.histories.Add(1)
	return runtime.NativeHistorySnapshot{Session: runtime.NativeSession{NativeID: id, Cwd: r.Cwd}, Entries: []runtime.NativeHistoryEntry{{ID: "original-message", Kind: "user", Data: json.RawMessage(`{"content":"original history"}`)}}}, nil
}
func bindingRequest(t *testing.T, a *Application, instance string) NativeBindingRequest {
	t.Helper()
	p, err := a.PreviewNative(context.Background(), NativePreviewRequest{ProjectID: "project", InstanceID: instance, NativeID: discoveredID})
	if err != nil || len(p.BindingToken) != 64 {
		t.Fatalf("preview binding: %+v %v", p, err)
	}
	return NativeBindingRequest{CommandID: "bind", ProjectID: "project", InstanceID: instance, NativeID: discoveredID, PreviewToken: p.BindingToken}
}

func TestNativeBindingPreservesIdentityWithoutGrantingControl(t *testing.T) {
	f := &bindingRuntime{fixtureRuntime: newFixture()}
	c := fixtureConfig(t)
	a := openFixture(t, c, f)
	request := bindingRequest(t, a, "instance")
	if len(a.Workspace().Sessions) != 0 {
		t.Fatal("preview registered a session")
	}
	receipt, err := a.BindNative(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadSession(receipt.SessionID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	s := read.Session
	if s.Scope.NativeID != discoveredID || s.Scope.StateNamespace != c.Instances[0].Profile || s.Scope.Instance.ID != "instance" || s.ProjectID != "project" || s.NativeBinding == nil {
		t.Fatalf("lost identity: %+v", s)
	}
	if s.Origin != "native" || s.ControlMode != "native_readonly" || !s.Paused || s.Status != "unknown" || s.Scope.Route.ID != "" || s.ExecutionSnapshot != nil || len(read.Operations) != 0 {
		t.Fatal("registration invented execution/route/control")
	}
	if s.Access.Source != "native" || s.Access.PreviewAvailable || s.Access.Capabilities["live_input"].Status != "unverified" {
		t.Fatalf("source projection: %+v", s.Access)
	}
	for _, kind := range []string{"send", "respond", "interrupt", "resume", "pause", "takeover", "associate", "reconcile", "stage_execution", "prepare_execution", "apply_execution"} {
		if _, err := a.Apply(Command{ID: "try-" + kind, Kind: kind, SessionID: s.ID, Text: "do not send"}); err == nil {
			t.Fatalf("read-only source accepted %s", kind)
		}
	}
	preview, err := a.PreviewSessionAccess(context.Background(), s.ID)
	if err != nil || preview.Error == "" {
		t.Fatal("native source acquired an ordinary control preview")
	}
	history, err := a.ReadBoundNativeHistory(context.Background(), s.ID, "")
	if err != nil || len(history.Entries) != 1 || !strings.Contains(history.Entries[0].RawJSON, "original history") {
		t.Fatalf("source-bound history: %+v %v", history, err)
	}
	for _, kind := range []string{"archive", "unarchive"} {
		if _, err := a.Apply(Command{ID: kind, Kind: kind, SessionID: s.ID}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(350 * time.Millisecond)
	if f.creates.Load() != 0 || f.sends.Load() != 0 || f.observations.Load() != 0 || f.histories.Load() != 2 {
		t.Fatal("registration or polling touched original executor")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, f)
	again, err := b.BindNative(context.Background(), request)
	if err != nil || again != receipt {
		t.Fatalf("restart lost command receipt: %+v %v", again, err)
	}
	read, _ = b.ReadSession(s.ID, "", 50)
	if read.Session.Scope.NativeID != discoveredID || read.Session.Scope.Route.ID != "" || read.Session.ControlMode != "native_readonly" || read.Session.NativeBinding.Configuration != s.NativeBinding.Configuration {
		t.Fatal("restart rebound or upgraded source")
	}
	time.Sleep(350 * time.Millisecond)
	if f.observations.Load() != 0 {
		t.Fatal("restart resumed the original execution")
	}
}

func TestNativeBindingCoalescesConcurrentRequests(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), &bindingRuntime{fixtureRuntime: newFixture()})
	request := bindingRequest(t, a, "instance")
	type result struct {
		receipt CommandReceipt
		err     error
	}
	results := make(chan result, 2)
	for _, id := range []string{"first", "second"} {
		go func(id string) {
			r := request
			r.CommandID = id
			receipt, err := a.BindNative(context.Background(), r)
			results <- result{receipt, err}
		}(id)
	}
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || one.receipt.SessionID != two.receipt.SessionID || len(a.Workspace().Sessions) != 1 {
		t.Fatalf("concurrent duplicates: %+v %+v", one, two)
	}
	r := request
	r.CommandID = "first"
	r.NativeID = "018f0000-0000-7000-8000-000000000002"
	if _, err := a.BindNative(context.Background(), r); err == nil {
		t.Fatal("command id accepted changed contents")
	}
}

func TestNativeBindingRejectsStaleOrForeignPreview(t *testing.T) {
	for _, scenario := range []string{"metadata-drift", "config-drift", "wrong-project", "wrong-id", "empty-token", "unknown-instance", "closed", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &bindingRuntime{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			r := bindingRequest(t, a, "instance")
			ctx := context.Background()
			switch scenario {
			case "metadata-drift":
				f.inspect = func(r runtime.DiscoveryRequest, id string) (runtime.NativeSession, error) {
					return runtime.NativeSession{NativeID: id, Cwd: r.Cwd, Title: "changed"}, nil
				}
			case "config-drift":
				if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("model='other'\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-project":
				r.ProjectID = "other"
			case "wrong-id":
				r.NativeID = "../../state"
			case "empty-token":
				r.PreviewToken = ""
			case "unknown-instance":
				r.InstanceID = "other"
			case "closed":
				a.Close()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := a.BindNative(ctx, r); err == nil {
				t.Fatal("invalid binding accepted")
			}
			if len(a.Workspace().Sessions) != 0 || f.creates.Load() != 0 || f.sends.Load() != 0 {
				t.Fatal("invalid binding created state or execution")
			}
		})
	}
}

func TestNativeBindingSeparatesNamespacesAndPreservesExistingRecords(t *testing.T) {
	c := fixtureConfig(t)
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "config.toml"), []byte("model='other'\n"), 0600)
	c.Instances = append(c.Instances, runtime.Instance{ID: "other", Kind: "codex", Profile: other})
	a := openFixture(t, c, &bindingRuntime{fixtureRuntime: newFixture()})
	one, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	second := bindingRequest(t, a, "other")
	second.CommandID = "bind-other"
	two, err := a.BindNative(context.Background(), second)
	if err != nil || one.SessionID == two.SessionID || len(a.Workspace().Sessions) != 2 {
		t.Fatalf("namespace collision: %+v %v", two, err)
	}
	a.mu.Lock()
	next := clone(a.state)
	s := next.Sessions[one.SessionID]
	s.Origin = "console"
	s.ControlMode = "human_takeover"
	s.Generation = 7
	s.NativeBinding = nil
	next.Sessions[s.ID] = s
	err = a.commit(next, "test.existing", s.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	r := bindingRequest(t, a, "instance")
	r.CommandID = "existing"
	result, err := a.BindNative(context.Background(), r)
	read, _ := a.ReadSession(s.ID, "", 50)
	if err != nil || result.SessionID != s.ID || result.Status != "coalesced" || read.Session.Generation != 7 || read.Session.ControlMode != "human_takeover" || read.Session.NativeBinding != nil {
		t.Fatal("registration altered an existing controlled record")
	}
}

func TestBoundNativeHistoryRefusesConfigurationRebinding(t *testing.T) {
	c := fixtureConfig(t)
	f := &bindingRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("model='changed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadBoundNativeHistory(context.Background(), r.SessionID, ""); err == nil || f.histories.Load() != 0 {
		t.Fatal("saved source followed changed configuration")
	}
	read, _ := a.ReadSession(r.SessionID, "", 50)
	if read.Session.Scope.NativeID != discoveredID || read.Session.NativeBinding == nil {
		t.Fatal("drift erased original binding")
	}
}

func TestNativeBindingObservationDoesNotBecomeManagedEvidence(t *testing.T) {
	c := observationConfig(t)
	a := openFixture(t, c, &bindingRuntime{fixtureRuntime: newFixture()})
	r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"message":"external stop"}`, discoveredID))
	_, err = a.IngestObservation("local-hooks", observationToken(), ObservationDelivery{EventID: "native-binding-event", OccurredAt: "2026-10-01T12:00:00Z", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range a.DomainEvents(r.SessionID) {
		if event.Managed {
			t.Fatal("native metadata binding promoted hook to managed execution evidence")
		}
	}
	read, _ := a.ReadSession(r.SessionID, "", 50)
	if read.Session.ControlMode != "native_readonly" || read.Session.Status != "unknown" || read.Session.View.Evidence.Complete || read.Session.View.Evidence.Source != "console.native_metadata_binding" {
		t.Fatal("event replaced native control or status evidence")
	}
}

func TestNativeBindingClaudeSourceHasNoSelectedRoute(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f := &bindingRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	read, _ := a.ReadSession(r.SessionID, "", 50)
	history, err := a.ReadBoundNativeHistory(context.Background(), r.SessionID, "")
	if err != nil || history.Coverage != "claude_main_transcript_entries" || read.Session.Scope.Instance.Kind != "claude" || read.Session.Scope.Route.ID != "" || read.Session.Access.Source != "native" || f.creates.Load() != 0 || f.sends.Load() != 0 {
		t.Fatalf("Claude source binding: %+v %v", history, err)
	}
}

func TestNativeBindingRejectsAmbiguousExistingIdentity(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), &bindingRuntime{fixtureRuntime: newFixture()})
	r, err := a.BindNative(context.Background(), bindingRequest(t, a, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	next := clone(a.state)
	duplicate := next.Sessions[r.SessionID]
	duplicate.ID = "duplicate"
	duplicate.Scope.SessionID = duplicate.ID
	next.Sessions[duplicate.ID] = duplicate
	err = a.commit(next, "test.duplicate", duplicate.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	request := bindingRequest(t, a, "instance")
	request.CommandID = "ambiguous"
	if _, err := a.BindNative(context.Background(), request); err == nil {
		t.Fatal("binding arbitrarily selected one of multiple original identity matches")
	}
}
