package app

import (
	"context"
	"testing"

	"codex-netcap/internal/runtime"
)

type configurationCatalogFixture struct {
	*fixtureRuntime
	read func(runtime.Scope) (runtime.ConfigurationCatalog, error)
}

func (f *configurationCatalogFixture) ReadConfigurationCatalog(_ context.Context, scope runtime.Scope) (runtime.ConfigurationCatalog, error) {
	return f.read(scope)
}

func TestConfigurationCatalogReadOnlyAndBinding(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-id", "wrong-cwd", "wrong-source", "wrong-scope", "false-completeness", "missing-time", "takeover", "registry-changed"} {
		t.Run(mode, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &configurationCatalogFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			before := a.Snapshot()
			f.read = func(scope runtime.Scope) (runtime.ConfigurationCatalog, error) {
				out := runtime.ConfigurationCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.config/read+configRequirements/read", Scope: "disk_configuration", At: "2026-10-06T00:00:00Z"}
				// The Application must compute against its saved snapshot, not
				// forward an adapter's comparison to another execution.
				out.SettingsDifference = &runtime.ConfigurationSettingsDifference{BaseSnapshotID: "wrong", Status: "changed", EffortChanged: true}
				switch mode {
				case "wrong-id":
					out.NativeID = "other"
				case "wrong-cwd":
					out.Cwd = "/other"
				case "wrong-source":
					out.Source = "thread"
				case "wrong-scope":
					out.Scope = "thread_configuration"
				case "false-completeness":
					out.Complete = true
				case "missing-time":
					out.At = ""
				case "takeover":
					if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
						t.Fatal(err)
					}
				case "registry-changed":
					a.mu.Lock()
					a.config.Instances[0].PolicyRevision = "changed"
					a.mu.Unlock()
				}
				return out, nil
			}
			out, err := a.ReadSessionConfigurationCatalog(context.Background(), id)
			if (err == nil) != (mode == "valid") {
				t.Fatal("incorrect acceptance", err)
			}
			if mode == "valid" && digest(before) != digest(a.Snapshot()) {
				t.Fatal("catalog changed state")
			}
			if mode == "valid" && (out.SettingsDifference == nil || out.SettingsDifference.Status != "unrecorded" || out.SettingsDifference.BaseSnapshotID != before.Sessions[0].ExecutionSnapshot.ID || out.SettingsDifference.EffortChanged || out.SettingsDifference.Before != nil) {
				t.Fatal("legacy settings fabricated or comparison not bound to saved snapshot")
			}
			if f.creates.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("catalog dispatched")
			}
		})
	}
}
