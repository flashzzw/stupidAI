package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func coverageArea(t *testing.T, read ExecutionRead, id string) ExecutionSourceArea {
	t.Helper()
	for _, area := range read.SourceCoverage.Areas {
		if area.ID == id {
			return area
		}
	}
	t.Fatalf("missing coverage area %s", id)
	return ExecutionSourceArea{}
}

// Four independent bindings share one asset. This is a state/API test, not a
// claim of four concurrently running native CLIs or merged configuration.
func TestExecutionSourceCoverageFourBindings(t *testing.T) {
	c, shared := executionFixture(t)
	c.Instances, c.Routes, c.Projects = nil, nil, nil
	for _, id := range []string{"codex-a", "codex-b", "claude-a", "claude-b"} {
		one := fixtureConfig(t)
		instance := one.Instances[0]
		instance.ID, instance.Kind = id, strings.Split(id, "-")[0]
		if instance.Kind == "claude" {
			instance.Bridge = filepath.Join(instance.Profile, "bridge.js")
			writePluginMetadata(t, instance.Bridge, "")
		}
		c.Instances = append(c.Instances, instance)
		c.Routes = append(c.Routes, runtime.Route{ID: id, InstanceID: id, Model: "fixture"})
		c.Projects = append(c.Projects, Project{ID: id, Cwd: one.Projects[0].Cwd})
	}
	f := newFixture()
	a := openFixture(t, c, f)
	ids := map[string]string{}
	for _, instance := range c.Instances {
		r, err := a.Apply(Command{ID: instance.ID, Kind: "create", ProjectID: instance.ID, RouteID: instance.ID, Permission: "read_only", CapabilityRefs: []string{"shared"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[instance.ID] = r.SessionID
	}
	waitTest(t, func() bool {
		for _, session := range a.Snapshot().Sessions {
			if session.Status != "idle" {
				return false
			}
		}
		return len(a.Snapshot().Sessions) == 4
	})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// No runtime or persistence is supplied: reads must use the saved state and
	// files only, and cannot refresh native evidence or change durable state.
	reader := &Application{config: c, state: state}
	for id, session := range reader.state.Sessions {
		session.View.Evidence = runtime.Evidence{Source: "saved-observation", At: "2020-01-01T00:00:00Z", Complete: true}
		session.ExecutionHold = true
		reader.state.Sessions[id] = session
	}
	before := digest(reader.state)
	readAll := func(drifted map[string]bool) {
		t.Helper()
		for binding, sid := range ids {
			r, err := reader.ReadExecution(sid)
			if err != nil || (r.Status == "drifted") != drifted[binding] {
				t.Fatalf("%s: %s %v", binding, r.Status, err)
			}
			if r.SourceCoverage.Scope != "recorded_sources_only" || r.NativeEvidence.At != "2020-01-01T00:00:00Z" || !r.Hold {
				t.Fatal("read promoted coverage, refreshed observation or released hold")
			}
			for _, group := range []string{"profile", "capability_refs"} {
				if coverageArea(t, r, group).Status != "recorded" {
					t.Fatal("missing recorded source", group)
				}
			}
			for _, group := range []string{"inherited_configuration", "external_dependencies", "dynamic_capabilities", "effective_permissions"} {
				if coverageArea(t, r, group).Status != "not_covered" {
					t.Fatal("unobserved config promoted", group)
				}
			}
			for _, source := range r.Sources {
				coverageArea(t, r, source.Group)
			}
			if strings.HasPrefix(binding, "codex") {
				if coverageArea(t, r, "codex_project_hooks").Status != "recorded" || len(r.Snapshot.CodexProjectHooks.Files) != 0 {
					t.Fatal("empty recorded collection confused with missing")
				}
			} else if coverageArea(t, r, "claude_local_settings").Status != "recorded" || coverageArea(t, r, "settings").Status != "not_selected" {
				t.Fatal("Claude coverage incorrect")
			}
		}
	}
	readAll(nil)
	hooks := filepath.Join(c.Instances[0].Profile, "hooks.json")
	writePluginMetadata(t, hooks, `{"hooks":{}}`)
	readAll(map[string]bool{"codex-a": true})
	if err := os.Remove(hooks); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, filepath.Join(shared, "tool.py"), "PRIVATE_CHANGED_RESOURCE")
	readAll(map[string]bool{"codex-a": true, "codex-b": true, "claude-a": true, "claude-b": true})
	if digest(reader.state) != before || f.sends.Load() != 0 || f.creates.Load() != 4 {
		t.Fatal("source check mutated state or dispatched")
	}
	read, _ := reader.ReadExecution(ids["codex-a"])
	read.SourceCoverage.Areas[0].Status = "caller-mutation"
	read.Sources[0].Status = "caller-mutation"
	again, _ := reader.ReadExecution(ids["codex-a"])
	if again.Sources[0].Status == "caller-mutation" || coverageArea(t, again, "profile").Status != "recorded" {
		t.Fatal("response aliases state")
	}
}

func TestExecutionSourceCoverageLegacyAndInvalidRecords(t *testing.T) {
	for _, backend := range []string{"codex", "claude"} {
		t.Run(backend, func(t *testing.T) {
			c, _ := executionFixture(t)
			c.Instances[0].Kind = backend
			if backend == "claude" {
				c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
				writePluginMetadata(t, c.Instances[0].Bridge, "")
			}
			a := openFixture(t, c, newFixture())
			sid := createExecution(t, a).SessionID
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			session := queryCopy(a.state.Sessions[sid])
			reader := &Application{config: c, state: State{Sessions: map[string]Session{sid: session}}}
			if backend == "codex" {
				session.ExecutionSnapshot.CodexProjectHooks.Version = 99
			} else {
				session.ExecutionSnapshot.ClaudeLocalSettings.Version = 99
			}
			reader.state.Sessions[sid] = session
			r, err := reader.ReadExecution(sid)
			if err != nil || r.Status != "drifted" {
				t.Fatal("invalid record presented as checked", err)
			}
			session.ExecutionSnapshot.CodexUserHooks, session.Scope.CodexUserHooks = nil, nil
			session.ExecutionSnapshot.CodexProjectHooks, session.Scope.CodexProjectHooks = nil, nil
			session.ExecutionSnapshot.ClaudeLocalSettings, session.Scope.ClaudeSettingsSources, session.Scope.SettingsGuardVersion = nil, nil, 0
			session.ExecutionSnapshot.Coverage = "unknown-historical-label"
			reader.state.Sessions[sid] = session
			r, err = reader.ReadExecution(sid)
			if err != nil || r.Status != "unchanged" || r.SourceCoverage.Scope != "recorded_sources_only" {
				t.Fatal("legacy file check incorrectly promoted or refused", err)
			}
			group := "codex_project_hooks"
			if backend == "claude" {
				group = "claude_local_settings"
			}
			if coverageArea(t, r, group).Status != "unrecorded" {
				t.Fatal("legacy source invented")
			}
			session.ExecutionSnapshot = nil
			reader.state.Sessions[sid] = session
			r, err = reader.ReadExecution(sid)
			if err != nil || r.Status != "unrecorded" || r.SourceCoverage.Scope != "unrecorded" || len(r.SourceCoverage.Areas) != 0 {
				t.Fatal("unrecorded snapshot promoted", err)
			}
		})
	}
}
