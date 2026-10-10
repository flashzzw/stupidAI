package app

import (
	"context"
	"testing"

	"codex-netcap/internal/runtime"
)

type catalogFixture struct {
	*fixtureRuntime
	read func(runtime.Scope) (runtime.SkillSourceCatalog, error)
}

func (f *catalogFixture) ReadSkillCatalog(_ context.Context, scope runtime.Scope) (runtime.SkillSourceCatalog, error) {
	return f.read(scope)
}

func TestSessionSkillCatalogPreservesBindingsAndRejectsStaleResults(t *testing.T) {
	for _, mode := range []string{"read-only", "wrong-id", "wrong-cwd", "wrong-source", "false-completeness", "changed-ownership"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := claudeSkillFixture(t)
			f := &catalogFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before := a.Snapshot()
			f.read = func(scope runtime.Scope) (runtime.SkillSourceCatalog, error) {
				out := runtime.SkillSourceCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.sdk.current_query", Items: []runtime.SkillSourceItem{}, Limitations: []string{"not a complete scan"}}
				out.Instructions = &runtime.InstructionSourceSample{Available: true, Items: []runtime.InstructionSourceItem{{Path: "/parent/CLAUDE.md", Source: "Project", Tokens: 18}}}
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
			out, err := a.ReadSessionSkillCatalog(context.Background(), id)
			if (err == nil) != (mode == "read-only") {
				t.Fatalf("catalog validation: %v", err)
			}
			if mode == "read-only" && digest(before) != digest(a.Snapshot()) {
				t.Fatal("catalog wrote Application state")
			}
			if mode == "read-only" && (out.Instructions == nil || !out.Instructions.Available || out.Instructions.Items[0].Path != "/parent/CLAUDE.md") {
				t.Fatal("read-only instruction sources lost in Application response")
			}
			if f.creates.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("catalog dispatched execution")
			}
		})
	}
}
