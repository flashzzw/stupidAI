package app

import "testing"

func TestClaudeSettingsGuardBindingAndLegacy(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, err := a.ReadExecution(id)
	if err != nil || before.Snapshot.ClaudeLocalSettings.GuardVersion != 1 || a.Snapshot().Sessions[0].Scope.SettingsGuardVersion != 1 {
		t.Fatalf("new execution guard not bound: %v", err)
	}
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
	s := state.Sessions[id]
	// A real pre-guard record has neither field. Retain its source versions.
	s.Scope.SettingsGuardVersion = 0
	s.Scope.ClaudeSettingsSources = nil
	s.ExecutionSnapshot.ClaudeLocalSettings.Version = 1
	s.ExecutionSnapshot.ClaudeLocalSettings.GuardVersion = 0
	state.Sessions[id] = s
	if err := db.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := openFixture(t, c, newFixture())
	read, err := legacy.ReadExecution(id)
	if err != nil || read.Status != "unchanged" || read.Snapshot.ClaudeLocalSettings.GuardVersion != 0 || legacy.Snapshot().Sessions[0].Scope.SettingsGuardVersion != 0 {
		t.Fatalf("legacy guard was invented or binding changed: %+v %v", read, err)
	}
}

func TestClaudeSettingsGuardMismatchRejectsInput(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	a.mu.Lock()
	s := a.state.Sessions[id]
	s.Scope.SettingsGuardVersion = 0
	a.state.Sessions[id] = s
	a.mu.Unlock()
	if _, err := a.Apply(Command{ID: "mismatch", Kind: "send", SessionID: id, Text: "MUST NOT SEND"}); err == nil {
		t.Fatal("unbound guard accepted")
	}
	if f.sends.Load() != 0 {
		t.Fatal("binding mismatch invoked provider")
	}
}
