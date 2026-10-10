package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexHookReviewBindsServerInspectionAndRejectsStaleSelections(t *testing.T) {
	for _, scenario := range []string{"trusted", "untrusted", "missing", "forged", "invalid-trust", "expired", "source-drift", "root-mode-drift", "registry-drift", "instance-drift", "returned-copy-mutated"} {
		t.Run(scenario, func(t *testing.T) {
			a, f, request := inspectionFixture(t)
			inspection, err := a.InspectCapabilityHooks(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			review := &CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: "trusted"}
			switch scenario {
			case "untrusted":
				review.Trust = "untrusted"
			case "missing":
				review = nil
			case "forged":
				review.InspectionID = "browser-supplied"
			case "invalid-trust":
				review.Trust = "allow-all"
			case "expired":
				a.mu.Lock()
				r := a.hookInspections[review.InspectionID]
				r.Expires = time.Now().Add(-time.Second)
				a.hookInspections[review.InspectionID] = r
				a.mu.Unlock()
			case "source-drift":
				if err := os.WriteFile(filepath.Join(inspection.Source.Resolved, "hooks/hooks.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"false","timeout":1}]}]}}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "registry-drift":
				libraryChange(t, a, "changed", CapabilityChangeRequest{Action: "disable", AssetID: request.AssetID, BaseRevision: request.Revision})
			case "root-mode-drift":
				info, err := os.Stat(inspection.Source.Resolved)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(inspection.Source.Resolved, info.Mode().Perm()^0020); err != nil {
					t.Fatal(err)
				}
			case "instance-drift":
				a.mu.Lock()
				a.config.Instances[0].PolicyRevision = "new-policy"
				a.mu.Unlock()
			case "returned-copy-mutated":
				inspection.Inspection.Definitions[0].DefinitionHash = "foreign"
			}
			result, err := a.Apply(Command{ID: "create-hooks", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: request.AssetID, Mode: "enabled", HookReview: review}}})
			valid := scenario == "trusted" || scenario == "untrusted" || scenario == "returned-copy-mutated"
			if (err == nil) != valid {
				t.Fatalf("admission %s: %v", scenario, err)
			}
			if !valid {
				if f.creates.Load() != 0 {
					t.Fatal("invalid review dispatched native create")
				}
				return
			}
			s := a.Snapshot().Sessions[0]
			p := s.ExecutionSnapshot.PluginSelections[0]
			if s.ID != result.SessionID || p.Hooks == nil || p.Hooks.Trust != review.Trust || p.Hooks.Inspection.Definitions[0].DefinitionHash == "foreign" || p.SourceHash != inspection.Source.SHA256 {
				t.Fatal("review lost full resource binding or exposed mutable stored inspection")
			}
		})
	}
}

func TestCodexHookTrustChangeIsExplicitAndDoesNotMutateCurrentSnapshot(t *testing.T) {
	a, _, request := inspectionFixture(t)
	inspection, err := a.InspectCapabilityHooks(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: request.AssetID, Mode: "enabled", HookReview: &CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: "trusted"}}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Scope.NativeID != "" })
	before := a.Snapshot().Sessions[0].ExecutionSnapshot
	change := ExecutionChangeRequest{BaseSnapshotID: before.ID, Selections: []CapabilitySelection{{AssetID: request.AssetID, Mode: "enabled", HookReview: &CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: "untrusted"}}}}
	preview, err := a.PreviewExecutionChange(created.SessionID, change)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].BeforeHookTrust != "trusted" || preview.Changes[0].AfterHookTrust != "untrusted" {
		t.Fatalf("trust change hidden: %+v", preview.Changes)
	}
	change.PreviewHash = preview.PreviewHash
	if _, err = a.Apply(Command{ID: "save", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &change}); err != nil {
		t.Fatal(err)
	}
	s := a.Snapshot().Sessions[0]
	if s.ExecutionSnapshot.PluginSelections[0].Hooks.Trust != "trusted" || s.PendingExecutionChange.Snapshot.PluginSelections[0].Hooks.Trust != "untrusted" {
		t.Fatal("saving changed current trust")
	}
	// Receipts are not persisted as authority. Saved immutable candidates carry
	// definitions/version/trust and remain independent of the read cache.
	a.mu.Lock()
	a.hookInspections = nil
	a.mu.Unlock()
	if err := a.verifySavedExecutionChange(s); err != nil {
		t.Fatalf("saved review could not be revalidated without transient receipt: %v", err)
	}
	if s.PendingExecutionChange.Snapshot.PluginSelections[0].Hooks.Inspection.SourceHash != inspection.Source.SHA256 {
		t.Fatal("candidate did not retain fixed definition provenance")
	}
}
