package app

import (
	"testing"
)

func TestCodexConfigurationSnapshotPersistenceAndLegacy(t *testing.T) {
	for _, mode := range []string{"new", "legacy", "scope-mismatch", "invalid-version"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := executionFixture(t)
			f := newFixture()
			a := openFixture(t, c, f)
			sid := createExecution(t, a).SessionID
			before, err := a.ReadExecution(sid)
			if err != nil || before.Snapshot.CodexConfigurationSources == nil || coverageArea(t, before, "codex_configuration_sources").Status != "recorded" {
				t.Fatal("new source version missing", err)
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
			session := state.Sessions[sid]
			switch mode {
			case "legacy":
				session.ExecutionSnapshot.CodexConfigurationSources = nil
				session.Scope.CodexConfigurationSources = nil
			case "scope-mismatch":
				session.Scope.CodexConfigurationSources = nil
			case "invalid-version":
				session.Scope.CodexConfigurationSources.Version = 99
				session.ExecutionSnapshot.CodexConfigurationSources.Version = 99
			}
			state.Sessions[sid] = session
			if err := db.Save(state); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			a = openFixture(t, c, f)
			after, err := a.ReadExecution(sid)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "new":
				if digest(after.Snapshot) != digest(before.Snapshot) {
					t.Fatal("reopen changed source version")
				}
			case "legacy":
				if after.Snapshot.CodexConfigurationSources != nil || coverageArea(t, after, "codex_configuration_sources").Status != "unrecorded" || after.Status != "unchanged" {
					t.Fatal("legacy source invented")
				}
			default:
				if after.Status != "drifted" {
					t.Fatal("unbound source version accepted")
				}
			}
		})
	}
}
