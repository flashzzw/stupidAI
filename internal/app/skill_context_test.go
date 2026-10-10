package app

import (
	"context"
	"testing"

	"codex-netcap/internal/runtime"
)

type contextFixture struct {
	*fixtureRuntime
	read func(runtime.Scope) (runtime.SkillContext, error)
}

func (f *contextFixture) ReadSkillContext(_ context.Context, s runtime.Scope) (runtime.SkillContext, error) {
	return f.read(s)
}

func TestSessionSkillContextReadUsesSavedBindingWithoutChangingExecution(t *testing.T) {
	c := fixtureConfig(t)
	f := &contextFixture{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
		t.Fatal(err)
	}
	before := a.Snapshot()
	f.read = func(scope runtime.Scope) (runtime.SkillContext, error) {
		if !scope.NoAutoResume || scope.NativeID != before.Sessions[0].Scope.NativeID || scope.StateNamespace != before.Sessions[0].Scope.StateNamespace {
			t.Fatal("context did not use original read-only binding")
		}
		return runtime.SkillContext{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.rollout.skill_context", Complete: true, Frames: []runtime.SkillContextFrame{{TurnID: "past"}}}, nil
	}
	out, err := a.ReadSessionSkillContext(context.Background(), id)
	if err != nil || len(out.Frames) != 1 {
		t.Fatal(err)
	}
	if digest(a.Snapshot()) != digest(before) || f.sends.Load() != 0 || f.creates.Load() != 1 {
		t.Fatal("context read changed input authority or execution")
	}
}

func TestSessionSkillContextReadRejectsReplacedBindingOrForeignResult(t *testing.T) {
	for _, mode := range []string{"registry-before", "registry-during", "generation", "native-id", "cwd", "source", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &contextFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.read = func(scope runtime.Scope) (runtime.SkillContext, error) {
				calls++
				a.mu.Lock()
				if mode == "registry-during" {
					a.config.Instances[0].PolicyRevision = "changed"
				}
				if mode == "generation" {
					s := a.state.Sessions[id]
					s.Generation++
					a.state.Sessions[id] = s
				}
				a.mu.Unlock()
				out := runtime.SkillContext{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.rollout.skill_context", Complete: true}
				switch mode {
				case "native-id":
					out.NativeID = "other"
				case "cwd":
					out.Cwd = "/other"
				case "source":
					out.Source = "guessed"
				case "incomplete":
					out.Complete = false
				}
				return out, nil
			}
			if mode == "registry-before" {
				a.mu.Lock()
				a.config.Instances[0].PolicyRevision = "changed"
				a.mu.Unlock()
			}
			out, err := a.ReadSessionSkillContext(context.Background(), id)
			if err == nil || out.Complete {
				t.Fatal("foreign or stale context accepted")
			}
			if mode == "registry-before" && calls != 0 {
				t.Fatal("read a replaced binding")
			}
		})
	}
}
