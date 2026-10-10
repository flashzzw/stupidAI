package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex-netcap/internal/runtime"
)

func changeFixture(t *testing.T) (Config, *fixtureRuntime, *Application, string) {
	t.Helper()
	c := fixtureConfig(t)
	c.CapabilityAssets = []CapabilityAsset{{ID: "identity", Name: "Identity", Kind: "mcp", Version: "v1", Source: filepath.Join(c.Instances[0].Profile, "config.toml"), Backends: []string{"codex"}, CodexMCP: "identity"}}
	f := newFixture()
	a := openFixture(t, c, f)
	r, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "identity", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	return c, f, a, r.SessionID
}

func changeRequest(t *testing.T, a *Application, sid string) ExecutionChangeRequest {
	t.Helper()
	read, err := a.ReadExecution(sid)
	if err != nil {
		t.Fatal(err)
	}
	r := ExecutionChangeRequest{BaseSnapshotID: read.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "identity", Mode: "disabled"}}}
	if read.PendingChange != nil {
		r.PendingChangeID = read.PendingChange.ID
	}
	preview, err := a.PreviewExecutionChange(sid, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	return r
}

func TestExecutionChangeSurvivesRestartWithoutChangingEffectiveExecution(t *testing.T) {
	c, f, a, sid := changeFixture(t)
	before, _ := a.ReadExecution(sid)
	request := changeRequest(t, a, sid)
	if got, _ := a.ReadExecution(sid); got.PendingChange != nil {
		t.Fatal("preview persisted a change")
	}
	cmd := Command{ID: "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &request}
	receipt, err := a.Apply(cmd)
	if err != nil || receipt.Status != "pending" || receipt.OperationID != "" {
		t.Fatalf("stage: %+v %v", receipt, err)
	}
	if again, err := a.Apply(cmd); err != nil || again != receipt {
		t.Fatal("lost receipt retry is not idempotent")
	}
	after, _ := a.ReadExecution(sid)
	plan := after.PendingChange
	if !reflect.DeepEqual(before.Snapshot, after.Snapshot) || plan == nil || plan.Snapshot.Revision != 2 || plan.Snapshot.PreviousID != before.Snapshot.ID || plan.Snapshot.ID == before.Snapshot.ID || plan.Snapshot.MCPSelections[0].Mode != "disabled" {
		t.Fatalf("candidate overwrote active snapshot or lacks linkage: %+v", after)
	}
	if f.sends.Load() != 0 || f.creates.Load() != 1 || len(a.Snapshot().Operations) != 1 {
		t.Fatal("staging performed native work")
	}
	plan.Snapshot.MCPSelections[0].Mode = "caller mutation"
	durable, _ := a.ReadExecution(sid)
	if durable.PendingChange.Snapshot.MCPSelections[0].Mode != "disabled" {
		t.Fatal("query caller mutated durable proposal")
	}
	if a.Workspace().Sessions[0].PendingExecutionChange != nil {
		t.Fatal("workspace metadata expanded to full pending snapshot")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	restarted, _ := a.ReadExecution(sid)
	if !reflect.DeepEqual(restarted.PendingChange, durable.PendingChange) || !reflect.DeepEqual(restarted.Snapshot, before.Snapshot) {
		t.Fatal("restart changed pending or active snapshot")
	}
	if _, err := a.Apply(Command{ID: "next-turn", Kind: "send", SessionID: sid, Text: "continue original configuration"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	for _, op := range a.Snapshot().Operations {
		if op.ExecutionSnapshotID != before.Snapshot.ID || op.Intent.Scope.MCPSelections[0].Mode != "enabled" {
			t.Fatal("ordinary next turn used pending configuration")
		}
	}
}

func TestExecutionChangeDoesNotInvalidateInflightReceipt(t *testing.T) {
	_, f, a, sid := changeFixture(t)
	f.block = make(chan struct{})
	defer func() {
		if f.block != nil {
			close(f.block)
		}
	}()
	_, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: sid, Text: "in progress"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 1 })
	before := a.Snapshot().Sessions[0]
	r := changeRequest(t, a, sid)
	if _, err := a.Apply(Command{ID: "stage-running", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	after := a.Snapshot().Sessions[0]
	if after.Generation != before.Generation || after.Status != before.Status || !reflect.DeepEqual(before.Scope, after.Scope) {
		t.Fatal("staging changed active operation ownership")
	}
	close(f.block)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	f.block = nil
	for _, op := range a.Snapshot().Operations {
		if op.Status != "succeeded" || op.ExecutionSnapshotID != before.ExecutionSnapshot.ID {
			t.Fatalf("inflight receipt lost: %+v", op)
		}
	}
}

func TestExecutionChangeRejectsStalePreviewAndUnsafeBindings(t *testing.T) {
	for _, test := range []string{"base", "preview", "source", "route", "extra-command-field", "unsupported-asset", "duplicate", "mode", "unchanged", "missing-preview"} {
		t.Run(test, func(t *testing.T) {
			_, f, a, sid := changeFixture(t)
			r := changeRequest(t, a, sid)
			cmd := Command{ID: "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}
			switch test {
			case "base":
				r.BaseSnapshotID = "old"
			case "preview":
				r.PreviewHash = "old"
			case "source":
				if err := os.WriteFile(a.config.CapabilityAssets[0].Source, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "route":
				cmd.RouteID = "other-provider"
			case "extra-command-field":
				cmd.NativeID = "other-thread"
			case "unsupported-asset":
				r.Selections[0].AssetID = "new"
			case "duplicate":
				r.Selections = append(r.Selections, r.Selections[0])
			case "mode":
				r.Selections[0].Mode = "inherit"
			case "unchanged":
				r.Selections[0].Mode = "enabled"
			case "missing-preview":
				r.PreviewHash = ""
			}
			if _, err := a.Apply(cmd); err == nil {
				t.Fatal("unsafe or stale change accepted")
			}
			read, _ := a.ReadExecution(sid)
			if read.PendingChange != nil || f.sends.Load() != 0 || f.creates.Load() != 1 {
				t.Fatal("rejected change mutated execution")
			}
		})
	}
}

func TestExecutionChangePendingCASAndDiscard(t *testing.T) {
	_, _, a, sid := changeFixture(t)
	r := changeRequest(t, a, sid)
	if _, err := a.Apply(Command{ID: "first", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "stale-tab", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err == nil {
		t.Fatal("stale tab overwrote plan")
	}
	updated := changeRequest(t, a, sid)
	if _, err := a.Apply(Command{ID: "replace", Kind: "stage_execution", SessionID: sid, ExecutionChange: &updated}); err != nil {
		t.Fatal(err)
	}
	discard := ExecutionChangeRequest{BaseSnapshotID: r.BaseSnapshotID, PendingChangeID: updated.PendingChangeID}
	if _, err := a.Apply(Command{ID: "stale-discard", Kind: "discard_execution", SessionID: sid, ExecutionChange: &discard}); err == nil {
		t.Fatal("stale discard erased replacement")
	}
	read, _ := a.ReadExecution(sid)
	discard.PendingChangeID = read.PendingChange.ID
	cmd := Command{ID: "discard", Kind: "discard_execution", SessionID: sid, ExecutionChange: &discard}
	receipt, err := a.Apply(cmd)
	if err != nil || receipt.Status != "discarded" {
		t.Fatal(err)
	}
	if got, err := a.Apply(cmd); err != nil || got != receipt {
		t.Fatal("discard retry not idempotent")
	}
	read, _ = a.ReadExecution(sid)
	if read.PendingChange != nil || read.Snapshot.ID != r.BaseSnapshotID {
		t.Fatal("discard affected active version")
	}
}

func TestExecutionChangeUnknownAndApprovalRemainUnchanged(t *testing.T) {
	for _, status := range []string{"unknown", "waiting_approval", "waiting_input"} {
		t.Run(status, func(t *testing.T) {
			_, f, a, sid := changeFixture(t)
			nativeID := a.Snapshot().Sessions[0].Scope.NativeID
			f.mu.Lock()
			f.views[nativeID] = runtime.View{NativeID: nativeID, Status: status, Pending: []runtime.Pending{{ID: "request", Status: "pending"}}}
			f.mu.Unlock()
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == status })
			before := a.Snapshot().Sessions[0]
			r := changeRequest(t, a, sid)
			if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
				t.Fatal(err)
			}
			after := a.Snapshot().Sessions[0]
			read, _ := a.ReadExecution(sid)
			ref := ExecutionChangeRequest{BaseSnapshotID: read.Snapshot.ID, PendingChangeID: read.PendingChange.ID}
			for _, kind := range []string{"prepare_execution", "apply_execution"} {
				if _, err := a.Apply(Command{ID: "deny-" + kind, Kind: kind, SessionID: sid, ExecutionChange: &ref}); err == nil {
					t.Fatal("non-idle execution accepted " + kind)
				}
			}
			if before.Status != after.Status || before.Generation != after.Generation || !reflect.DeepEqual(before.View.Pending, after.View.Pending) || !reflect.DeepEqual(before.Scope, after.Scope) || f.sends.Load() != 0 {
				t.Fatal("staging cleared unknown or pending approval")
			}
		})
	}
}

func TestExecutionChangeEnableRequiresUnchangedActiveRegistry(t *testing.T) {
	c := fixtureConfig(t)
	c.CapabilityAssets = []CapabilityAsset{{ID: "identity", Name: "Identity", Kind: "mcp", Version: "v1", Source: filepath.Join(c.Instances[0].Profile, "config.toml"), Backends: []string{"codex"}, CodexMCP: "identity"}}
	a := openFixture(t, c, newFixture())
	receipt, err := a.Apply(Command{ID: "disabled", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "identity", Mode: "disabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	read, _ := a.ReadExecution(receipt.SessionID)
	r := ExecutionChangeRequest{BaseSnapshotID: read.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "identity", Mode: "enabled"}}}
	preview, err := a.PreviewExecutionChange(receipt.SessionID, r)
	if err != nil || len(preview.Changes) != 1 || preview.Changes[0].Before != "disabled" || preview.Changes[0].After != "enabled" {
		t.Fatalf("enable preview: %+v %v", preview, err)
	}
	r.PreviewHash = preview.PreviewHash
	for _, state := range []string{"disabled", "revised", "removed"} {
		a.mu.Lock()
		a.config.CapabilityAssets = append([]CapabilityAsset{}, c.CapabilityAssets...)
		switch state {
		case "disabled":
			a.config.CapabilityAssets[0].Disabled = true
		case "revised":
			a.config.CapabilityAssets[0].Version = "v2"
		case "removed":
			a.config.CapabilityAssets = nil
		}
		a.mu.Unlock()
		if _, err := a.Apply(Command{ID: state, Kind: "stage_execution", SessionID: receipt.SessionID, ExecutionChange: &r}); err == nil {
			t.Fatalf("%s registry enabled old asset", state)
		}
	}
	a.mu.Lock()
	a.config.CapabilityAssets = c.CapabilityAssets
	a.mu.Unlock()
	if _, err := a.Apply(Command{ID: "enable", Kind: "stage_execution", SessionID: receipt.SessionID, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	read, _ = a.ReadExecution(receipt.SessionID)
	if read.Snapshot.MCPSelections[0].Mode != "disabled" || read.PendingChange.Snapshot.MCPSelections[0].Mode != "enabled" {
		t.Fatal("enabling pending version affected original selection")
	}
}
