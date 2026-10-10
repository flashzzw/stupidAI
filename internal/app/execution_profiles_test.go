package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex-netcap/internal/runtime"
)

func registrationFixture(t *testing.T, id string) ExecutionProfile {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = 'fixture'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return ExecutionProfile{Instance: runtime.Instance{ID: id, Name: "same display name", Kind: "codex", Executable: "/bin/true", Profile: dir, Socket: filepath.Join(dir, "control.sock"), PolicyRevision: "v1"}, Route: runtime.Route{ID: id + "-route", InstanceID: id, Provider: "fixture", Endpoint: "http://127.0.0.1:1", Model: "fixture", CredentialRef: "env:NETCAP_REGISTRATION_TEST", CredentialRevision: "v1", ConfigRevision: "v1"}}
}

func TestExecutionRegistrationPersistsAndDoesNotDispatch(t *testing.T) {
	t.Setenv("NETCAP_REGISTRATION_TEST", "fixture-key")
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	p := registrationFixture(t, "added")
	before := a.Workspace().Sequence
	preview, err := a.PreviewExecutionProfile(context.Background(), p)
	if err != nil || preview.Instance.ConfigError != "" || preview.ControlStatus != "unavailable" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if a.Workspace().Sequence != before || len(a.Workspace().Instances) != 1 {
		t.Fatal("preview persisted")
	}
	command := Command{ID: "register", Kind: "register_execution", ExecutionProfile: &p}
	first, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := a.Apply(command)
	if err != nil || retry != first || first.ExecutionProfileID != p.Instance.ID {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	command.ExecutionProfile = &ExecutionProfile{Instance: p.Instance, Route: p.Route}
	command.ExecutionProfile.Instance.Name = "changed"
	if _, err = a.Apply(command); err == nil {
		t.Fatal("same command accepted changed body")
	}
	if _, err = a.Apply(Command{ID: "overwrite", Kind: "register_execution", ExecutionProfile: &p}); err == nil {
		t.Fatal("instance ID overwritten")
	}
	if _, err = a.DiagnoseExecutionProfiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.creates.Load() != 0 || f.sends.Load() != 0 {
		t.Fatal("registration/diagnosis dispatched native input")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	if len(a.Workspace().Instances) != 2 || len(a.Workspace().Routes) != 2 {
		t.Fatal("registration lost on restart")
	}
	if _, err = a.Apply(Command{ID: "create-added", Kind: "create", ProjectID: "project", RouteID: p.Route.ID, Permission: "read_only"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.creates.Load() == 1 })
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	if a.Snapshot().Sessions[0].Scope.Instance.ID != "added" {
		t.Fatal("selection used wrong instance")
	}
}

func TestExecutionRegistrationAliasesDiagnosisAndConflicts(t *testing.T) {
	t.Setenv("NETCAP_REGISTRATION_TEST", "fixture-key")
	c := fixtureConfig(t)
	a := openFixture(t, c, newFixture())
	p := registrationFixture(t, "alias")
	p.Instance.Profile = filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(c.Instances[0].Profile, p.Instance.Profile); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "alias", Kind: "register_execution", ExecutionProfile: &p}); err != nil {
		t.Fatal(err)
	}
	for _, i := range a.Workspace().Instances {
		if len(i.SharedStateWith) != 1 {
			t.Fatalf("alias hidden: %+v", i)
		}
	}
	if err := os.Remove(p.Instance.Profile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), p.Instance.Profile); err != nil {
		t.Fatal(err)
	}
	statuses, err := a.DiagnoseExecutionProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range statuses {
		if len(i.SharedStateWith) != 0 {
			t.Fatal("stale alias retained after diagnosis")
		}
		if i.ID == "alias" && !strings.Contains(i.ConfigError, "默认原生配置") {
			t.Fatalf("missing reason: %+v", i)
		}
	}
	saved := map[string]ExecutionProfile{p.Instance.ID: p}
	merged, err := mergeExecutionProfiles(c, saved)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mergeExecutionProfiles(merged, saved); err != nil {
		t.Fatal("identical startup definition rejected")
	}
	merged.Instances[len(merged.Instances)-1].Profile = t.TempDir()
	if _, err = mergeExecutionProfiles(merged, saved); err == nil {
		t.Fatal("restart rebound saved identity")
	}
}

func TestExecutionRegistrationConcurrentAndPersistenceFailure(t *testing.T) {
	t.Setenv("NETCAP_REGISTRATION_TEST", "fixture-key")
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	p := registrationFixture(t, "one")
	command := Command{ID: "same", Kind: "register_execution", ExecutionProfile: &p}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Apply(command); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(a.Workspace().Instances) != 2 {
		t.Fatal("concurrent retry duplicated definition")
	}
	// Failed persistence must not publish an in-memory instance or route.
	store := &failingStore{Persistence: a.store}
	store.fail.Store(true)
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
	p = registrationFixture(t, "failed")
	if _, err := a.Apply(Command{ID: "failed", Kind: "register_execution", ExecutionProfile: &p}); err == nil {
		t.Fatal("disk failure hidden")
	}
	if len(a.Workspace().Instances) != 2 || len(a.Workspace().Routes) != 2 {
		t.Fatal("failed registration became selectable")
	}
}

func TestClaudeCustomAndDefaultSettingsRemainIndependentSources(t *testing.T) {
	for _, source := range []string{"custom", "default"} {
		t.Run(source, func(t *testing.T) {
			c, _ := executionFixture(t)
			i := &c.Instances[0]
			i.Kind = "claude"
			i.Bridge = filepath.Join(t.TempDir(), "bridge.js")
			i.SettingsFile = filepath.Join(t.TempDir(), "custom-name.json")
			for _, p := range []string{i.Bridge, i.SettingsFile, filepath.Join(i.Profile, "settings.json")} {
				if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f := newFixture()
			a := openFixture(t, c, f)
			sid := createExecution(t, a).SessionID
			before, _ := a.ReadExecution(sid)
			if before.Snapshot.SettingsSource == nil || before.Snapshot.SettingsSource.Path != i.SettingsFile || before.Snapshot.ProfileSource.Path != filepath.Join(i.Profile, "settings.json") {
				t.Fatal("custom settings replaced default source or state directory")
			}
			target := i.SettingsFile
			if source == "default" {
				target = filepath.Join(i.Profile, "settings.json")
			}
			if err := os.WriteFile(target, []byte("{\"env\":{\"FIXTURE\":\"changed\"}}"), 0600); err != nil {
				t.Fatal(err)
			}
			after, _ := a.ReadExecution(sid)
			if after.Status != "drifted" || digest(before.Snapshot) != digest(after.Snapshot) {
				t.Fatal("drift hidden or immutable snapshot rewritten")
			}
			if _, err := a.Apply(Command{ID: "drift", Kind: "send", SessionID: sid, Text: "must not dispatch"}); err == nil {
				t.Fatal("settings drift accepted input")
			}
			if f.sends.Load() != 0 {
				t.Fatal("native input on settings drift")
			}
		})
	}
}

func TestExecutionRegistrySchemaMigrationRetainsState(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := newState()
	state.Commands["old"] = CommandReceipt{ID: "old", Hash: "original", Status: "succeeded"}
	state.Sequence = 42
	if err = s.Save(state); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("DROP TABLE execution_profiles"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got.Sequence != 42 || got.Commands["old"] != state.Commands["old"] || len(got.ExecutionProfiles) != 0 {
		t.Fatalf("migration lost state: %+v %v", got, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "console.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=6"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if next, err := OpenStore(dir); err == nil {
		next.Close()
		t.Fatal("future schema accepted")
	}
}

func TestClaudeRegistrationRejectsIdentityOverridesWithoutNativeInput(t *testing.T) {
	for _, source := range []string{"default", "custom"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv("NETCAP_REGISTRATION_TEST", "fixture-key")
			c := fixtureConfig(t)
			f := newFixture()
			a := openFixture(t, c, f)
			p := registrationFixture(t, "conflict")
			p.Instance.Kind = "claude"
			p.Instance.Socket = ""
			p.Instance.Bridge = filepath.Join(t.TempDir(), "bridge.js")
			if err := os.WriteFile(p.Instance.Bridge, nil, 0600); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(p.Instance.Profile, "settings.json")
			if source == "custom" {
				file = filepath.Join(t.TempDir(), "custom.json")
				p.Instance.SettingsFile = file
			}
			if err := os.WriteFile(file, []byte(`{"env":{"ANTHROPIC_API_KEY":"secret-must-not-appear"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			preview, err := a.PreviewExecutionProfile(context.Background(), p)
			if err != nil || !strings.Contains(preview.Instance.ConfigError, "覆盖") || strings.Contains(preview.Instance.ConfigError, "secret-must-not-appear") {
				t.Fatalf("bad conflict diagnosis: %+v %v", preview, err)
			}
			if _, err = a.Apply(Command{ID: "register-conflict", Kind: "register_execution", ExecutionProfile: &p}); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Apply(Command{ID: "create-conflict", Kind: "create", ProjectID: "project", RouteID: p.Route.ID, Permission: "read_only"}); err == nil {
				t.Fatal("conflict permitted native creation")
			}
			if f.creates.Load() != 0 || f.sends.Load() != 0 {
				t.Fatal("identity conflict dispatched")
			}
		})
	}
}
