package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex-netcap/internal/runtime"
)

type skillFixture struct {
	*fixtureRuntime
	discover func(runtime.Scope) (runtime.SkillInventory, error)
}

func (f *skillFixture) DiscoverSkills(_ context.Context, scope runtime.Scope) (runtime.SkillInventory, error) {
	return f.discover(scope)
}

func TestSessionSkillDiscoveryPreservesSnapshotAndOwnership(t *testing.T) {
	c, source := executionFixture(t)
	f := &skillFixture{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	id := createExecution(t, a).SessionID
	if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
		t.Fatal(err)
	}
	before := a.Snapshot()
	f.discover = func(scope runtime.Scope) (runtime.SkillInventory, error) {
		if scope.Cwd != before.Sessions[0].Scope.Cwd || scope.Instance.Profile != before.Sessions[0].Scope.Instance.Profile {
			t.Fatal("wrong saved binding")
		}
		return runtime.SkillInventory{Cwd: scope.Cwd, Source: "codex.skills/list", Complete: true, Skills: []runtime.DiscoveredSkill{{Name: "shared", Path: filepath.Join(source, "SKILL.md"), Scope: "user", Enabled: true}}}, nil
	}
	// A current discovery is useful even when an asset's content has changed;
	// it must not erase drift or authorize use of that changed source.
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := a.ReadSessionSkills(context.Background(), id)
	if err != nil || !out.Complete || len(out.Skills) != 1 {
		t.Fatalf("discovery: %+v %v", out, err)
	}
	after := a.Snapshot()
	if digest(before) != digest(after) || f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("read changed snapshot, ownership or operations")
	}
	read, err := a.ReadExecution(id)
	if err != nil || read.Status != "drifted" {
		t.Fatal("discovery hid source drift")
	}
	if _, err := a.ReadSessionSkills(context.Background(), "missing"); err == nil {
		t.Fatal("unknown session queried")
	}
}

func TestSessionSkillDiscoveryRejectsChangedBindingAndMismatchedResult(t *testing.T) {
	for _, mode := range []string{"during-generation", "during-scope", "registry-before", "registry-during", "wrong-cwd", "wrong-source"} {
		t.Run(mode, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &skillFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			if _, err := a.Apply(Command{ID: "pause", Kind: "takeover", SessionID: id}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.discover = func(scope runtime.Scope) (runtime.SkillInventory, error) {
				calls++
				a.mu.Lock()
				session := a.state.Sessions[id]
				switch mode {
				case "during-generation":
					session.Generation++
				case "during-scope":
					session.Scope.Cwd = "/changed"
				case "registry-during":
					a.config.Instances[0].PolicyRevision = "changed"
				}
				a.state.Sessions[id] = session
				a.mu.Unlock()
				out := runtime.SkillInventory{Cwd: scope.Cwd, Source: "codex.skills/list", Complete: true}
				if mode == "wrong-cwd" {
					out.Cwd = "/other"
				}
				if mode == "wrong-source" {
					out.Source = "thread-loaded"
				}
				return out, nil
			}
			if mode == "registry-before" {
				a.mu.Lock()
				a.config.Instances[0].PolicyRevision = "changed"
				a.mu.Unlock()
			}
			out, err := a.ReadSessionSkills(context.Background(), id)
			if err == nil || out.Complete {
				t.Fatalf("stale/foreign discovery accepted: %+v %v", out, err)
			}
			if mode == "registry-before" && calls != 0 {
				t.Fatal("queried replaced instance")
			}
		})
	}
}
