package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type settingsUpgradeFixture struct {
	*projectHookFixture
	upgraded atomic.Bool
}

func (f *settingsUpgradeFixture) ReadCodexConfigurationSources(ctx context.Context, scope runtime.Scope) (*runtime.CodexConfigurationSources, error) {
	sources, err := f.fixtureRuntime.ReadCodexConfigurationSources(ctx, scope)
	if err == nil && f.upgraded.Load() {
		hash := strings.Repeat("a", 64)
		sources.Settings = &runtime.ConfigurationSettings{Version: 1, ReasoningEffort: runtime.ConfigurationEffort{State: "unset"}, EffortValueSHA256: hash, EffortSourcesSHA256: hash, OtherConfigurationSHA256: hash, OtherOriginsSHA256: hash, OtherLayersSHA256: hash}
	}
	return sources, err
}

func TestConfigurationSettingsUpgradePreservesLegacyHookCandidate(t *testing.T) {
	c, _ := executionFixture(t)
	path := filepath.Join(c.Instances[0].Profile, "hooks.json")
	writePluginMetadata(t, path, `{"hooks":{}}`)
	f := &settingsUpgradeFixture{projectHookFixture: newProjectHookFixture()}
	a := openFixture(t, c, f)
	id := createExecution(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, err := a.ReadExecution(id)
	if err != nil || before.Snapshot.CodexConfigurationSources.Settings != nil {
		t.Fatal("expected older source snapshot", err)
	}
	f.upgraded.Store(true)
	writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
	request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptCodexHookSources: true}
	preview, err := a.PreviewExecutionChange(id, request)
	if err != nil {
		t.Fatal("new comparison schema blocked existing hook adoption", err)
	}
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	staged, err := a.ReadExecution(id)
	if err != nil || digest(staged.Snapshot) != digest(before.Snapshot) || staged.PendingChange.Snapshot.CodexConfigurationSources.Settings != nil {
		t.Fatal("hook candidate fabricated settings evidence", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	restored, err := a.ReadExecution(id)
	if err != nil || digest(restored.PendingChange) != digest(staged.PendingChange) || digest(restored.Snapshot) != digest(before.Snapshot) {
		t.Fatal("reopen changed legacy candidate", err)
	}
}
