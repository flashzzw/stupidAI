package app

import (
	"context"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

type permissionCatalogFixture struct {
	*fixtureRuntime
	read func(runtime.Scope) (runtime.PermissionRuleCatalog, error)
}

func (f *permissionCatalogFixture) ReadPermissionCatalog(_ context.Context, scope runtime.Scope) (runtime.PermissionRuleCatalog, error) {
	return f.read(scope)
}

func TestSessionPermissionCatalogPreservesStateAndRejectsStaleBindings(t *testing.T) {
	for _, mode := range []string{"read-only", "wrong-id", "wrong-cwd", "wrong-source", "false-completeness", "missing-query", "missing-configuration", "missing-digest", "changed-ownership", "changed-profile"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := claudeSkillFixture(t)
			f := &permissionCatalogFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before := a.Snapshot()
			f.read = func(scope runtime.Scope) (runtime.PermissionRuleCatalog, error) {
				out := runtime.PermissionRuleCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.list_permission_rules", QueryID: "query", ConfigurationID: strings.Repeat("a", 64), SampleHash: strings.Repeat("b", 64)}
				switch mode {
				case "wrong-id":
					out.NativeID = "another"
				case "wrong-cwd":
					out.Cwd = "/other"
				case "wrong-source":
					out.Source = "disk.settings"
				case "false-completeness":
					out.Complete = true
				case "missing-query":
					out.QueryID = ""
				case "missing-configuration":
					out.ConfigurationID = ""
				case "missing-digest":
					out.SampleHash = ""
				case "changed-ownership":
					if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
						t.Fatal(err)
					}
				case "changed-profile":
					a.mu.Lock()
					a.config.Instances[0].Executable = "/changed"
					a.mu.Unlock()
				}
				return out, nil
			}
			_, err := a.ReadSessionPermissionCatalog(context.Background(), id)
			if (err == nil) != (mode == "read-only") {
				t.Fatalf("permission report validation: %v", err)
			}
			if mode == "read-only" && digest(before) != digest(a.Snapshot()) {
				t.Fatal("permission read wrote Application state")
			}
			if f.creates.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("permission read dispatched work")
			}
		})
	}
}
