package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func installedSettingsApplicationFixture(t *testing.T) (*Application, *applicationConfigurationFixture, string) {
	t.Helper()
	a, base, id, _, _ := pluginSettingsPreviewFixture(t, "valid")
	cfg := queryCopy(a.config)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f := &applicationConfigurationFixture{fixtureRuntime: base}
	a = openFixture(t, cfg, f)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	return a, f, id
}

func TestInstalledSettingsApplicationPreservesDurableBindingsAcrossLostReceipt(t *testing.T) {
	a, f, id := installedSettingsApplicationFixture(t)
	before := a.Snapshot().Sessions[0]
	request := pluginSettingsStageRequest(t, a, id)
	if _, err := a.StageSessionPluginSettings(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	pending := a.Snapshot().Sessions[0].PendingExecutionChange
	ref := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: pending.ID}
	if _, err := a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange.Baseline != "" })
	for _, op := range a.Snapshot().Operations {
		if op.Intent.Kind == "prepare_execution" && (op.Intent.Scope.ClaudeInstalledSettings != nil || op.Intent.ConfigurationTarget == nil ||
			digest(op.Intent.ConfigurationTarget.ClaudeInstalledSettings) != digest(pending.Snapshot.ClaudeInstalledPluginSettings.Binding)) {
			t.Fatal("preparation lost the old scope or exact saved target")
		}
	}
	f.durable = func(in runtime.Intent) {
		stored, err := a.store.Load()
		if err != nil {
			t.Error(err)
			return
		}
		op := stored.Operations[in.ID]
		if op.Status != "dispatching" || op.ExecutionSnapshotID != pending.Snapshot.ID ||
			in.PreviousScope == nil || !reflect.DeepEqual(*in.PreviousScope, before.Scope) ||
			digest(in.Scope.ClaudeInstalledSettings) != digest(pending.Snapshot.ClaudeInstalledPluginSettings.Binding) ||
			stored.Sessions[id].ExecutionSnapshot.ID != before.ExecutionSnapshot.ID {
			t.Error("installed settings were not persisted before native dispatch")
		}
	}
	command := Command{ID: "apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &ref}
	var receipt CommandReceipt
	var err error
	waitTest(t, func() bool { receipt, err = a.Apply(command); return err == nil })
	waitTest(t, func() bool { return f.applications.Load() == 1 && a.Snapshot().Sessions[0].Status == "unknown" })
	if s := a.Snapshot().Sessions[0]; !reflect.DeepEqual(s.Scope, before.Scope) || s.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID || s.PendingExecutionChange == nil || !s.Paused {
		t.Fatal("missing receipt promoted the candidate or discarded its original scope")
	}
	cfg := queryCopy(a.config)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.resolveApply.Store(true)
	a = openFixture(t, cfg, f)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange == nil })
	after := a.Snapshot().Sessions[0]
	if after.ExecutionSnapshot.ID != pending.Snapshot.ID || !reflect.DeepEqual(after.ExecutionHistory, []*ExecutionSnapshot{before.ExecutionSnapshot}) ||
		digest(after.Scope.ClaudeInstalledSettings) != digest(pending.Snapshot.ClaudeInstalledPluginSettings.Binding) || !after.Paused || f.applications.Load() != 1 {
		t.Fatal("late receipt lost version linkage, input pause, or replayed the application")
	}
	if err := verifyExecutionBinding(after); err != nil {
		t.Fatal(err)
	}
	if again, err := a.Apply(command); err != nil || again != receipt {
		t.Fatal("retry failed to reuse its original durable operation", err)
	}
}

func TestInstalledSettingsCandidateRejectsCorruptBindingBeforeDispatch(t *testing.T) {
	for _, scenario := range []string{"file-hash", "file-path", "version-id", "startup-version", "startup-files", "registry-files", "mode", "source-key", "extra-selection", "resource-drift", "ordinary-binding", "guard", "schema"} {
		t.Run(scenario, func(t *testing.T) {
			a, f, id := installedSettingsApplicationFixture(t)
			if _, err := a.StageSessionPluginSettings(context.Background(), id, pluginSettingsStageRequest(t, a, id)); err != nil {
				t.Fatal(err)
			}
			before := a.Snapshot()
			current := before.Sessions[0]
			candidate := current.PendingExecutionChange.Snapshot
			binding := candidate.ClaudeInstalledPluginSettings.Binding
			switch scenario {
			case "file-hash":
				binding.Artifact.File.SHA256 = strings.Repeat("a", 64)
			case "file-path":
				binding.Artifact.File.Path = filepath.Join(current.Scope.Cwd, "unrelated.json")
			case "version-id":
				candidate.ID = "another-version"
			case "startup-version":
				binding.StartupSourceVersion = strings.Repeat("a", 64)
			case "startup-files":
				binding.StartupFiles = nil
			case "registry-files":
				binding.SourceFiles[0].SHA256 = strings.Repeat("a", 64)
			case "mode":
				binding.Selections[0].Mode = "enabled"
			case "source-key":
				binding.Selections[0].NativeSource = "selected@another"
			case "extra-selection":
				binding.Selections = append(binding.Selections, binding.Selections[0])
			case "resource-drift":
				if err := os.WriteFile(filepath.Join(binding.Selections[0].SourcePath, "new-resource"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ordinary-binding":
				candidate.Route.Model = "another-model"
			case "guard":
				candidate.ClaudeLocalSettings.GuardVersion = 0
			case "schema":
				candidate.SchemaVersion = 1
			}
			a.mu.Lock()
			a.state.Sessions[id] = current
			a.mu.Unlock()
			ref := ExecutionChangeRequest{BaseSnapshotID: current.ExecutionSnapshot.ID, PendingChangeID: current.PendingExecutionChange.ID}
			if _, err := a.Apply(Command{ID: "reject", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref}); err == nil {
				t.Fatal("corrupt installed settings were admitted")
			}
			if f.preparations.Load() != 0 || f.applications.Load() != 0 || len(a.Snapshot().Operations) != len(before.Operations) {
				t.Fatal("rejected binding dispatched native work")
			}
		})
	}
}
