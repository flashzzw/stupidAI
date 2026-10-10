package app

import (
	"path/filepath"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestCodexHookSourceAdoptionCandidate(t *testing.T) {
	for _, source := range []string{"user", "project"} {
		t.Run(source, func(t *testing.T) {
			c, _ := executionFixture(t)
			path := filepath.Join(c.Instances[0].Profile, "hooks.json")
			if source == "project" {
				path = filepath.Join(c.Projects[0].Cwd, ".codex/hooks.json")
			}
			writePluginMetadata(t, path, `{"hooks":{}}`)
			f := newProjectHookFixture()
			a := openFixture(t, c, f)
			id := createExecution(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before, _ := a.ReadExecution(id)
			writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
			request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptCodexHookSources: true}
			preview, err := a.PreviewExecutionChange(id, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.CodexHookSourceChanges) != 1 || preview.CodexHookSourceChanges[0].Source != source {
				t.Fatal("source diff missing")
			}
			request.PreviewHash = preview.PreviewHash
			if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
				t.Fatal(err)
			}
			staged, _ := a.ReadExecution(id)
			if runtime.ValidateCodexHookSourceProof(a.Snapshot().Sessions[0].Scope, staged.PendingChange.HookSourceProof) != nil {
				t.Fatal("original proof missing")
			}
			if digest(staged.Snapshot) != digest(before.Snapshot) || !staged.PendingChange.AdoptCodexHookSources {
				t.Fatal("staging changed current version")
			}
			if _, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: id, Text: "must not send"}); err == nil {
				t.Fatal("candidate enabled ordinary input")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a = openFixture(t, c, f)
			restored, _ := a.ReadExecution(id)
			if digest(restored.PendingChange) != digest(staged.PendingChange) || digest(restored.Snapshot) != digest(before.Snapshot) {
				t.Fatal("candidate changed on reopen")
			}
			writePluginMetadata(t, path, `{"hooks":{"PreToolUse":[]}}`)
			ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
			if _, err := a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref}); err == nil {
				t.Fatal("candidate changed after save")
			}
			if _, err := a.Apply(Command{ID: "discard", Kind: "discard_execution", SessionID: id, ExecutionChange: &ref}); err != nil {
				t.Fatal(err)
			}
			discarded, _ := a.ReadExecution(id)
			if discarded.PendingChange != nil || digest(discarded.Snapshot) != digest(before.Snapshot) {
				t.Fatal("discard rewrote old snapshot")
			}
		})
	}
}

func TestCodexHookSourceAdoptionRejectsOtherSourceDrift(t *testing.T) {
	c, _ := executionFixture(t)
	path := filepath.Join(c.Instances[0].Profile, "hooks.json")
	writePluginMetadata(t, path, `{"hooks":{}}`)
	a := openFixture(t, c, newProjectHookFixture())
	id := createExecution(t, a).SessionID
	before, _ := a.ReadExecution(id)
	writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
	writePluginMetadata(t, filepath.Join(c.Instances[0].Profile, "config.toml"), "changed unrelated configuration")
	if _, err := a.PreviewExecutionChange(id, ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptCodexHookSources: true}); err == nil {
		t.Fatal("hook adoption masked profile drift")
	}
}

func TestCodexHookSourceAdoptionWithPluginCandidate(t *testing.T) {
	c := codexPluginFixture(t)
	path := filepath.Join(c.Instances[0].Profile, "hooks.json")
	writePluginMetadata(t, path, `{"hooks":{}}`)
	a := openFixture(t, c, newProjectHookFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(id)
	writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
	request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptCodexHookSources: true, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}}
	preview, err := a.PreviewExecutionChange(id, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.CodexHookSourceChanges) != 1 || len(preview.Changes) == 0 {
		t.Fatal("mixed candidate omitted an explicit change")
	}
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	read, _ := a.ReadExecution(id)
	if read.PendingChange == nil || len(read.PendingChange.Snapshot.PluginSelections) != 1 || digest(read.Snapshot) != digest(before.Snapshot) {
		t.Fatal("mixed candidate replaced active snapshot")
	}
}

func TestCodexHookSourceProofIntentBinding(t *testing.T) {
	c, _ := executionFixture(t)
	path := filepath.Join(c.Instances[0].Profile, "hooks.json")
	writePluginMetadata(t, path, `{"hooks":{}}`)
	a := openFixture(t, c, newProjectHookFixture())
	id := createExecution(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	original, _ := a.ReadExecution(id)
	writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
	request := ExecutionChangeRequest{BaseSnapshotID: original.Snapshot.ID, AdoptCodexHookSources: true}
	preview, err := a.PreviewExecutionChange(id, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	session := a.Snapshot().Sessions[0]
	session.ExecutionHold = true
	session.PendingExecutionChange.OperationID = "owned-prepare"
	target := executionChangeScope(session.Scope, session.PendingExecutionChange.Snapshot)
	op := Operation{ID: "owned-prepare", ExecutionSnapshotID: session.ExecutionSnapshot.ID, Intent: runtime.Intent{Kind: "prepare_execution", Scope: session.Scope, Permission: session.Permission, ConfigurationTarget: &target, HookSourceProof: queryCopy(session.PendingExecutionChange.HookSourceProof)}}
	if err := verifyExecutionChangeIntent(session, op); err != nil {
		t.Fatal("valid saved proof rejected", err)
	}
	for _, scenario := range []string{"removed", "identity", "fingerprint", "policy", "time"} {
		t.Run(scenario, func(t *testing.T) {
			tampered := queryCopy(op)
			switch scenario {
			case "removed":
				tampered.Intent.HookSourceProof = nil
			case "identity":
				tampered.Intent.HookSourceProof.ScopeHash = "other"
			case "fingerprint":
				tampered.Intent.HookSourceProof.Fingerprint = "other"
			case "policy":
				tampered.Intent.HookSourceProof.RequirementsHash = "other"
			case "time":
				tampered.Intent.HookSourceProof.At = "2026-10-06T01:00:00Z"
			}
			if err := verifyExecutionChangeIntent(session, tampered); err == nil {
				t.Fatal("operation replaced saved proof")
			}
		})
	}
	session.PendingExecutionChange.HookSourceProof = nil
	op.Intent.HookSourceProof = nil
	if err := verifyExecutionChangeIntent(session, op); err != nil {
		t.Fatal("legacy candidate rejected before runtime can check live connection", err)
	}
}
