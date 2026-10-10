package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestSessionHistoryPagesAndWorkspaceMetadata(t *testing.T) {
	c := fixtureConfig(t)
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state := newState()
	for _, id := range []string{"one", "two"} {
		session := Session{ID: id, ProjectID: "project", Status: "idle"}
		for n := 1; n <= 7; n++ {
			session.View.Messages = append(session.View.Messages, runtime.Message{ID: fmt.Sprint(n), TurnID: "turn", Role: "assistant", Text: fmt.Sprintf("private history %s %d", id, n)})
		}
		state.Sessions[id] = session
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	a := openFixture(t, c, newFixture())
	workspace, _ := json.Marshal(a.Workspace())
	if strings.Contains(string(workspace), "private history") {
		t.Fatal("workspace list loaded chat bodies")
	}
	latest, err := a.ReadSession("one", "", 3)
	if err != nil || latest.TotalMessages != 7 || latest.Session.View.Messages[0].ID != "5" {
		t.Fatalf("latest page: %+v %v", latest, err)
	}
	older, err := a.ReadSession("one", latest.PreviousCursor, 3)
	if err != nil || older.Session.View.Messages[0].ID != "2" || older.Session.View.Messages[2].ID != "4" {
		t.Fatalf("older page: %+v %v", older, err)
	}
	first, err := a.ReadSession("one", older.PreviousCursor, 3)
	if err != nil || len(first.Session.View.Messages) != 1 || first.PreviousCursor != "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	if _, err := a.ReadSession("two", latest.PreviousCursor, 3); err == nil {
		t.Fatal("cross-session cursor accepted")
	}
	if _, err := a.ReadSession("one", "", 201); err == nil {
		t.Fatal("unbounded history accepted")
	}
	latest.Session.View.Messages[0].Text = "caller mutation"
	unchanged, _ := a.ReadSession("one", "", 3)
	if unchanged.Session.View.Messages[0].Text == "caller mutation" {
		t.Fatal("query caller mutated stored history")
	}
}

func TestArchivePreservesInflightExecutionAndSurvivesRestart(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	f.block = make(chan struct{})
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	if _, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: created.SessionID, Text: "continue original turn"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 1 })
	before := a.Snapshot().Sessions[0]
	if _, err := a.Apply(Command{ID: "archive", Kind: "archive", SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}
	after := a.Snapshot().Sessions[0]
	if !after.Archived || after.Generation != before.Generation || after.Paused != before.Paused || after.Status != before.Status {
		t.Fatal("archive changed native execution/ownership")
	}
	close(f.block)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	if !a.Snapshot().Sessions[0].Archived || f.sends.Load() != 1 {
		t.Fatal("late result unarchived or replayed execution")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, newFixture())
	if !b.Workspace().Sessions[0].Archived {
		t.Fatal("archive lost on restart")
	}
	if _, err := b.Apply(Command{ID: "show", Kind: "unarchive", SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}
	if b.Workspace().Sessions[0].Archived {
		t.Fatal("unarchive did not restore visibility")
	}
}
