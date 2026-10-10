package app

import (
	"context"
	"testing"

	"codex-netcap/internal/runtime"
)

type hookCatalogFixture struct {
	*fixtureRuntime
	read func(runtime.Scope) (runtime.HookSourceCatalog, error)
}

func (f *hookCatalogFixture) ReadHookCatalog(_ context.Context, scope runtime.Scope) (runtime.HookSourceCatalog, error) {
	return f.read(scope)
}

func TestSessionHookCatalogPreservesBindingsAndRejectsStaleResults(t *testing.T) {
	for _, mode := range []string{"read-only", "wrong-id", "wrong-cwd", "wrong-source", "false-completeness", "changed-ownership"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := claudeSkillFixture(t)
			f := &hookCatalogFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before := a.Snapshot()
			f.read = func(scope runtime.Scope) (runtime.HookSourceCatalog, error) {
				out := runtime.HookSourceCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.native.hooks_menu", Items: []runtime.HookSourceItem{}, Limitations: []string{"not a complete scan"}}
				switch mode {
				case "wrong-id":
					out.NativeID = "another"
				case "wrong-cwd":
					out.Cwd = "/other"
				case "wrong-source":
					out.Source = "directory-scan"
				case "false-completeness":
					out.Complete = true
				case "changed-ownership":
					if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
						t.Fatal(err)
					}
				}
				return out, nil
			}
			_, err := a.ReadSessionHookCatalog(context.Background(), id)
			if (err == nil) != (mode == "read-only") {
				t.Fatalf("catalog validation: %v", err)
			}
			if mode == "read-only" && digest(before) != digest(a.Snapshot()) {
				t.Fatal("catalog wrote Application state")
			}
			if f.creates.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("catalog dispatched execution")
			}
		})
	}
}

func TestCodexHookCatalogReadsDirectoryWithoutChangingSession(t *testing.T) {
	for _, mode := range []string{"read-only", "wrong-id", "wrong-cwd", "wrong-source", "query-scope", "policy-overclaim", "false-completeness", "changed-ownership"} {
		t.Run(mode, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &hookCatalogFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before := a.Snapshot()
			f.read = func(scope runtime.Scope) (runtime.HookSourceCatalog, error) {
				out := runtime.HookSourceCatalog{Scope: "directory", NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.hooks/list", Items: []runtime.HookSourceItem{}, Limitations: []string{"directory discovery only"}}
				switch mode {
				case "wrong-id":
					out.NativeID = "other"
				case "wrong-cwd":
					out.Cwd = "/other"
				case "wrong-source":
					out.Source = "claude.native.hooks_menu"
				case "query-scope":
					out.Scope = "query"
				case "policy-overclaim":
					out.PolicyReported = true
				case "false-completeness":
					out.Complete = true
				case "changed-ownership":
					if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
						t.Fatal(err)
					}
				}
				return out, nil
			}
			out, err := a.ReadSessionHookCatalog(context.Background(), id)
			if (err == nil) != (mode == "read-only") {
				t.Fatalf("wrong catalog acceptance: %+v %v", out, err)
			}
			if mode == "read-only" && digest(before) != digest(a.Snapshot()) {
				t.Fatal("catalog changed Application state")
			}
			if f.creates.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("catalog executed work")
			}
		})
	}
}
