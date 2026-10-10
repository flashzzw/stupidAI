package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func skillInputFixture(t *testing.T, modes ...string) (*Application, *contextFixture, string) {
	t.Helper()
	cfg := codexPluginFixture(t)
	p := filepath.Join(cfg.CapabilityAssets[0].Source, "skills/method/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\nname: method\ndescription: Owned method\n---\nOWNED_SKILL_METHOD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := &contextFixture{fixtureRuntime: newFixture()}
	a := openFixture(t, cfg, f)
	mode := "enabled"
	if len(modes) > 0 {
		mode = modes[0]
	}
	r, err := a.Apply(Command{ID: "create-skill-input", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: mode}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	return a, f, r.SessionID
}

func TestSkillContextCorrelatesExplicitRequestAndChecksBodyDigest(t *testing.T) {
	for _, mode := range []string{"exact", "content-mismatch", "no-load", "other-source", "no-receipt", "ambiguous", "missing-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			a, f, id := skillInputFixture(t)
			execution, err := a.ReadExecution(id)
			if err != nil {
				t.Fatal(err)
			}
			input := runtime.SkillInput{AssetID: "plugin", Name: "fixture-plugin:method"}
			r, err := a.Apply(Command{ID: "load-method", Kind: "send", SessionID: id, Text: "Use method", SkillInput: &SkillInputRequest{SnapshotID: execution.Snapshot.ID, Items: []runtime.SkillInput{input}}})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == r.OperationID {
						return op.Status == "succeeded"
					}
				}
				return false
			})
			if _, err = a.Apply(Command{ID: "takeover-for-read", Kind: "takeover", SessionID: id}); err != nil {
				t.Fatal(err)
			}
			scope := a.Snapshot().Sessions[0].Scope
			bound, err := runtime.ResolveCodexSkillInputs(scope, []runtime.SkillInput{input})
			if err != nil {
				t.Fatal(err)
			}
			load := runtime.SkillBodyLoad{ID: "native-load", Name: input.Name, Path: bound[0].Path, BodySHA256: bound[0].FileSHA256, ContentSHA256: strings.Repeat("1", 64)}
			if mode == "content-mismatch" {
				load.BodySHA256 = strings.Repeat("0", 64)
			}
			if mode == "other-source" {
				load.Path = "/different/SKILL.md"
			}
			loads := []runtime.SkillBodyLoad{load}
			if mode == "no-load" {
				loads = nil
			}
			shared := runtime.SkillContext{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.rollout.skill_context", Complete: true, Frames: []runtime.SkillContextFrame{{TurnID: r.OperationID, Loads: loads}}}
			f.read = func(s runtime.Scope) (runtime.SkillContext, error) { return shared, nil }
			a.mu.Lock()
			op := a.state.Operations[r.OperationID]
			if mode == "no-receipt" {
				op.Receipt.NativeTurn = ""
				a.state.Operations[r.OperationID] = op
			}
			if mode == "ambiguous" {
				op.ID = "duplicate"
				a.state.Operations[op.ID] = op
			}
			if mode == "missing-snapshot" {
				op.ExecutionSnapshotID = "missing"
				a.state.Operations[r.OperationID] = op
			}
			a.mu.Unlock()
			before := a.Snapshot()
			out, err := a.ReadSessionSkillContext(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			stable := func(s Snapshot) string {
				slices.SortFunc(s.Operations, func(a, b Operation) int { return strings.Compare(a.ID, b.ID) })
				return digest(s)
			}
			if stable(a.Snapshot()) != stable(before) {
				t.Fatal("body evidence changed execution state")
			}
			if shared.Frames[0].InputEvidence != nil {
				t.Fatal("request correlation mutated reader-owned context")
			}
			got := out.Frames[0].InputEvidence
			if mode == "no-receipt" || mode == "ambiguous" || mode == "missing-snapshot" {
				if got != nil {
					t.Fatal("invented request correlation")
				}
				return
			}
			if got == nil || got.OperationID != r.OperationID || got.SnapshotID != execution.Snapshot.ID || len(got.Items) != 1 || got.Items[0].AssetVersion != "asset-v1" {
				t.Fatalf("missing original request/version: %+v", got)
			}
			status := "not_observed"
			if mode == "exact" {
				status = "loaded_exact"
			}
			if mode == "content-mismatch" {
				status = "content_mismatch"
			}
			if got.Items[0].Status != status {
				t.Fatalf("incorrect body result: %+v", got)
			}
		})
	}
}

func TestSkillInputIsSavedWithOriginalSnapshotBeforeDispatch(t *testing.T) {
	a, f, id := skillInputFixture(t)
	read, err := a.ReadExecution(id)
	if err != nil {
		t.Fatal(err)
	}
	input := runtime.SkillInput{AssetID: "plugin", Name: "fixture-plugin:method"}
	f.before = func(in runtime.Intent) {
		if in.Kind != "send" {
			return
		}
		if len(in.SkillInputs) != 1 || in.SkillInputs[0] != input {
			t.Error("explicit Skill lost before native dispatch")
		}
		for _, op := range a.Snapshot().Operations {
			if op.ID == in.ID && op.ExecutionSnapshotID != read.Snapshot.ID {
				t.Error("Skill request moved to a different snapshot")
			}
		}
	}
	cmd := Command{ID: "skill-input", Kind: "send", SessionID: id, Text: "Use this method", SkillInput: &SkillInputRequest{SnapshotID: read.Snapshot.ID, Items: []runtime.SkillInput{input}}}
	r, err := a.Apply(cmd)
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 1 })
	for _, op := range a.Snapshot().Operations {
		if op.ID == r.OperationID && (len(op.Intent.SkillInputs) != 1 || op.Intent.SkillInputs[0] != input) {
			t.Fatal("Skill request not persisted")
		}
	}
	again, err := a.Apply(cmd)
	if err != nil || again.OperationID != r.OperationID || f.sends.Load() != 1 {
		t.Fatal("duplicate receipt replayed Skill input", err)
	}
}

func TestSkillInputRejectsStaleOrUnselectedRequestBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"stale", "empty", "duplicate", "unknown-asset", "unknown-skill", "disabled", "wrong-kind", "management-kind"} {
		t.Run(mode, func(t *testing.T) {
			selection := "enabled"
			if mode == "disabled" {
				selection = "disabled"
			}
			a, f, id := skillInputFixture(t, selection)
			read, err := a.ReadExecution(id)
			if err != nil {
				t.Fatal(err)
			}
			request := &SkillInputRequest{SnapshotID: read.Snapshot.ID, Items: []runtime.SkillInput{{AssetID: "plugin", Name: "fixture-plugin:method"}}}
			cmd := Command{ID: "rejected", Kind: "send", SessionID: id, Text: "Must not dispatch", SkillInput: request}
			switch mode {
			case "stale":
				request.SnapshotID = "old-version"
			case "empty":
				request.Items = nil
			case "duplicate":
				request.Items = append(request.Items, request.Items[0])
			case "unknown-asset":
				request.Items[0].AssetID = "other"
			case "unknown-skill":
				request.Items[0].Name = "other:method"
			case "wrong-kind":
				cmd.Kind = "interrupt"
			case "management-kind":
				cmd.Kind = "manage_capability"
			}
			if _, err = a.Apply(cmd); err == nil || f.sends.Load() != 0 {
				t.Fatal("unverified Skill request dispatched")
			}
		})
	}
}
