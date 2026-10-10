//go:build linux && nativeclaude

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func nativeSkillFileAlias(t *testing.T, a *Application, c Config, sessions map[string]string, check func(string, bool), results map[string]any) {
	t.Helper()
	responses := map[string]any{}
	capture := func(phase string) {
		t.Helper()
		rows := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot()}
		for _, id := range sessions {
			detail, err := a.ReadSession(id, "", 100)
			if err != nil {
				t.Fatal(err)
			}
			rows["/api/sessions/"+id] = detail
		}
		responses[phase] = rows
	}
	find := func(id string) Session {
		for _, s := range a.Snapshot().Sessions {
			if s.ID == id {
				return s
			}
		}
		return Session{}
	}
	await := func(status string) {
		t.Helper()
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
			ready := true
			for _, id := range sessions {
				s := find(id)
				ready = ready && s.Status == status && len(s.View.SkillEvidence) == 1 && s.View.SkillEvidence[0].Verified == (status == "idle")
			}
			if ready {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("alias observation did not reach", status)
	}
	before := map[string]Session{}
	for label, id := range sessions {
		before[label] = find(id)
	}
	capture("before")
	source := before["a"].Scope.SkillSelections[0].SourcePath
	dir := filepath.Join(c.Projects[0].Cwd, ".claude/skills/other-key")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "SKILL.md")
	if err := os.Link(source, alias); err != nil {
		t.Fatal(err)
	}
	await("unknown")
	for label, id := range sessions {
		s := find(id)
		check(label+"_alias_withdraws_verified_selection", !s.View.SkillEvidence[0].Verified)
		check(label+"_alias_preserves_identity_and_snapshot", s.Scope.NativeID == before[label].Scope.NativeID && digest(s.ExecutionSnapshot) == digest(before[label].ExecutionSnapshot))
		_, err := a.Apply(Command{ID: "blocked-alias-" + label, Kind: "send", SessionID: id, Text: "MUST NOT REACH MODEL THROUGH ALIAS"})
		check(label+"_alias_blocks_application_input", err != nil)
	}
	capture("blocked")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	await("idle")
	for label, id := range sessions {
		s := find(id)
		check(label+"_repair_keeps_same_execution", s.Scope.NativeID == before[label].Scope.NativeID && digest(s.ExecutionSnapshot) == digest(before[label].ExecutionSnapshot) && s.Scope.SkillSelections[0].Mode == before[label].Scope.SkillSelections[0].Mode)
		check(label+"_no_model_input_or_result", len(s.View.Messages) == 0)
	}
	capture("repaired")
	results["file_alias_responses"] = responses
	data, err := json.MarshalIndent(map[string]any{"responses": responses, "sessions": sessions}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(c.StateDir), "file-alias-browser-responses.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
