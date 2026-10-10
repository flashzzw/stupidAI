package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestCodexPluginChangeStagesWithoutChangingCurrentVersion(t *testing.T) {
	c := codexPluginFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	r, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: "disabled"}}}
	preview, err := a.PreviewExecutionChange(r.SessionID, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Kind != "cli_plugin" || preview.Changes[0].After != "disabled" {
		t.Fatalf("wrong difference: %+v", preview)
	}
	request.PreviewHash = preview.PreviewHash
	if _, err = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: r.SessionID, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if digest(after.Snapshot) != digest(before.Snapshot) || after.PendingChange == nil || after.PendingChange.Snapshot.PluginSelections[0].Mode != "disabled" || after.PendingChange.Snapshot.PluginSelections[0].NativeSource != before.Snapshot.PluginSelections[0].NativeSource {
		t.Fatal("staging changed the current native binding")
	}
	if _, err = os.Stat(after.PendingChange.Snapshot.PluginSelections[0].CachePath); !os.IsNotExist(err) {
		t.Fatal("preview or staging prepared native cache")
	}
	if f.sends.Load() != 0 {
		t.Fatal("staging sent model input")
	}
}

func TestCodexPluginChangePersistsOriginalBindingAcrossLostReceiptAndRestart(t *testing.T) {
	c := codexPluginFixture(t)
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture(), observationStarted: make(chan struct{}, 1), observationRelease: make(chan struct{})}
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(sid)
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: "inherit"}}}
	preview, err := a.PreviewExecutionChange(sid, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(sid)
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
	f.blockObservations.Store(true)
	<-f.observationStarted
	var prep CommandReceipt
	waitTest(t, func() bool {
		prep, err = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: sid, ExecutionChange: &ref})
		return err == nil
	})
	if f.preparations.Load() != 0 {
		t.Fatal("configuration preparation raced a display observation")
	}
	f.blockObservations.Store(false)
	close(f.observationRelease)
	waitTest(t, func() bool {
		for _, o := range a.Snapshot().Operations {
			if o.ID == prep.OperationID {
				return o.Status == "succeeded"
			}
		}
		return false
	})
	f.durable = func(in runtime.Intent) {
		state, err := a.store.Load()
		if err != nil {
			t.Error(err)
			return
		}
		op := state.Operations[in.ID]
		if in.PreviousScope == nil || digest(*in.PreviousScope) != digest(state.Sessions[sid].Scope) || digest(op.Intent.PreviousScope) != digest(in.PreviousScope) || len(in.Scope.PluginSelections) != 0 {
			t.Error("apply intent did not persist both original binding and target inheritance")
		}
		s := state.Sessions[sid]
		if op.Status != "dispatching" || op.ExecutionSnapshotID != staged.PendingChange.Snapshot.ID || s.ExecutionSnapshot.ID != before.Snapshot.ID || !s.ExecutionHold || s.PendingExecutionChange.Status != "applying" {
			t.Errorf("native apply preceded durable candidate/intent: %+v", op)
		}
	}
	var applied CommandReceipt
	waitTest(t, func() bool {
		applied, err = a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: sid, ExecutionChange: &ref})
		return err == nil
	})
	waitTest(t, func() bool { return f.applications.Load() == 1 && a.Snapshot().Sessions[0].Status == "unknown" })
	if _, err := a.Apply(Command{ID: "overwrite", Kind: "discard_execution", SessionID: sid, ExecutionChange: &ref}); err == nil {
		t.Fatal("unknown native mutation discarded")
	}
	if _, err := a.Apply(Command{ID: "manual", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	read, _ := a.ReadExecution(sid)
	if !reflect.DeepEqual(read.Snapshot, before.Snapshot) || read.PendingChange == nil || !read.Hold {
		t.Fatal("crash guessed an effective version")
	}
	f.resolveApply.Store(true)
	waitTest(t, func() bool { r, _ := a.ReadExecution(sid); return r.PendingChange == nil })
	read, _ = a.ReadExecution(sid)
	s := a.Snapshot().Sessions[0]
	if !reflect.DeepEqual(read.Snapshot, staged.PendingChange.Snapshot) || len(read.History) != 1 || !reflect.DeepEqual(read.History[0], before.Snapshot) || s.ControlMode != "human_takeover" || !s.Paused || f.applications.Load() != 1 {
		t.Fatal("late reconciliation overwrote history, re-applied, or granted control")
	}
	if got, err := a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: sid, ExecutionChange: &ref}); err != nil || got != applied {
		t.Fatal("durable command retry changed operation")
	}
	for _, op := range a.Snapshot().Operations {
		if op.ID == created.OperationID && op.ExecutionSnapshotID != before.Snapshot.ID {
			t.Fatal("old operation changed version")
		}
	}
}

func TestCodexPluginChangeRejectsComponentCollisionBeforeStaging(t *testing.T) {
	c := codexPluginFixture(t)
	other := codexPluginFixture(t).CapabilityAssets[0]
	other.ID = "other-plugin"
	if err := os.WriteFile(filepath.Join(other.Source, ".codex-plugin/plugin.json"), []byte(`{"name":"other-plugin","version":"1.2.3"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = append(c.CapabilityAssets, other)
	f := newFixture()
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	_, err = a.PreviewExecutionChange(created.SessionID, ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "other-plugin", Mode: "enabled"}}})
	if err == nil {
		t.Fatal("conflicting plugin components accepted as candidate")
	}
	after, _ := a.ReadExecution(created.SessionID)
	if after.PendingChange != nil || digest(after.Snapshot) != digest(before.Snapshot) || f.sends.Load() != 0 {
		t.Fatal("rejected preview changed execution")
	}
}
