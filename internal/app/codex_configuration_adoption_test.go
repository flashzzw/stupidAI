package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

// This fixture supplies source evidence only; actual effort application and
// recovery are covered by the fixed-native Application tests.
type configurationAdoptionFixture struct{ *projectHookFixture }

func (f *configurationAdoptionFixture) ReadCodexConfigurationSources(ctx context.Context, scope runtime.Scope) (*runtime.CodexConfigurationSources, error) {
	s, e := f.fixtureRuntime.ReadCodexConfigurationSources(ctx, scope)
	if e != nil {
		return nil, e
	}
	body, e := os.ReadFile(filepath.Join(scope.Instance.Profile, "config.toml"))
	if e != nil {
		return nil, e
	}
	effort := "low"
	if strings.Contains(string(body), "high") {
		effort = "high"
	}
	hash := strings.Repeat("a", 64)
	s.Settings = &runtime.ConfigurationSettings{Version: 1, ReasoningEffort: runtime.ConfigurationEffort{State: "set", Value: effort}, EffortValueSHA256: digest(effort), EffortSourcesSHA256: digest(effort), OtherConfigurationSHA256: hash, OtherOriginsSHA256: hash, OtherLayersSHA256: hash}
	return s, nil
}
func (f *configurationAdoptionFixture) ReadCodexHookSourceProof(ctx context.Context, scope, target runtime.Scope, prior *runtime.CodexHookSourceProof) (*runtime.CodexHookSourceProof, error) {
	p, e := f.fixtureRuntime.ReadCodexHookSourceProof(ctx, scope, target, prior)
	if e == nil && prior == nil {
		p.ConfigurationTargetHash = digest(target.CodexConfigurationSources)
	}
	return p, e
}

func TestCodexConfigurationCandidatePersistenceAndBinding(t *testing.T) {
	for _, mode := range []string{"roundtrip", "diff-tamper", "proof-tamper", "source-drift", "capability-reference"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _ := executionFixture(t)
			path := filepath.Join(cfg.Instances[0].Profile, "config.toml")
			writePluginMetadata(t, path, "model_reasoning_effort='low'\n")
			if mode == "capability-reference" {
				cfg.CapabilityAssets = append(cfg.CapabilityAssets, CapabilityAsset{ID: "profile-reference", Name: "Shared profile asset", Kind: "mcp", Version: "1", Source: path, Backends: []string{"codex"}})
			}
			f := &configurationAdoptionFixture{newProjectHookFixture()}
			a := openFixture(t, cfg, f)
			cmd := Command{ID: "create", Kind: "create", ProjectID: cfg.Projects[0].ID, RouteID: cfg.Routes[0].ID, Permission: "read_only"}
			if mode == "capability-reference" {
				cmd.CapabilityRefs = []string{"profile-reference"}
			}
			r, e := a.Apply(cmd)
			if e != nil {
				t.Fatal(e)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before := a.Snapshot().Sessions[0]
			writePluginMetadata(t, path, "model_reasoning_effort='high'\n")
			req := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, AdoptCodexConfigurationSources: true}
			preview, e := a.PreviewExecutionChange(r.SessionID, req)
			if mode == "capability-reference" {
				if e == nil {
					t.Fatal("configuration adoption upgraded an independent capability reference")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			req.PreviewHash = preview.PreviewHash
			if _, e = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: r.SessionID, ExecutionChange: &req}); e != nil {
				t.Fatal(e)
			}
			staged := a.Snapshot().Sessions[0]
			if staged.Fingerprint != before.Fingerprint || staged.RouteFingerprint != before.RouteFingerprint || digest(staged.ExecutionSnapshot) != digest(before.ExecutionSnapshot) {
				t.Fatal("saving adopted current configuration")
			}
			if e := a.Close(); e != nil {
				t.Fatal(e)
			}
			a = openFixture(t, cfg, f)
			restored := a.Snapshot().Sessions[0]
			if digest(restored.PendingExecutionChange) != digest(staged.PendingExecutionChange) || digest(restored.ExecutionSnapshot) != digest(before.ExecutionSnapshot) {
				t.Fatal("restart changed candidate or original")
			}
			if e := a.verifySavedExecutionChange(restored); e != nil {
				t.Fatal("saved candidate cannot be revalidated", e)
			}
			switch mode {
			case "diff-tamper":
				restored.PendingExecutionChange.CodexConfigurationChange.Settings.After.Value = "low"
				if a.verifySavedExecutionChange(restored) == nil {
					t.Fatal("invented difference accepted")
				}
			case "source-drift":
				writePluginMetadata(t, path, "# changed after save\nmodel_reasoning_effort='high'\n")
				if a.verifySavedExecutionChange(restored) == nil {
					t.Fatal("candidate file drift accepted")
				}
			case "proof-tamper":
				restored.ExecutionHold = true
				restored.PendingExecutionChange.OperationID = "owned"
				target := executionChangeScope(restored.Scope, restored.PendingExecutionChange.Snapshot)
				op := Operation{ID: "owned", ExecutionSnapshotID: restored.ExecutionSnapshot.ID, Intent: runtime.Intent{Kind: "prepare_execution", Scope: restored.Scope, ConfigurationTarget: &target, Permission: restored.Permission, HookSourceProof: queryCopy(restored.PendingExecutionChange.HookSourceProof)}}
				if e := verifyExecutionChangeIntent(restored, op); e != nil {
					t.Fatal(e)
				}
				op.Intent.HookSourceProof.ConfigurationTargetHash = strings.Repeat("b", 64)
				if verifyExecutionChangeIntent(restored, op) == nil {
					t.Fatal("operation replaced candidate proof")
				}
			}
		})
	}
}
