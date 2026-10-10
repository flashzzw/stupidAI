package app

import (
	"codex-netcap/internal/runtime"
	"slices"
)

type SkillInputRequest struct {
	SnapshotID string               `json:"snapshot_id"`
	Items      []runtime.SkillInput `json:"items"`
}

func (a *Application) attachSkillInputEvidence(session Session, report *runtime.SkillContext) {
	snapshots := append([]*ExecutionSnapshot{session.ExecutionSnapshot}, session.ExecutionHistory...)
	for n := range report.Frames {
		frame := &report.Frames[n]
		frame.InputEvidence = nil
		var matches []Operation
		for _, op := range a.state.Operations {
			if op.SessionID == session.ID && op.Intent.Kind == "send" && op.Status != "not_sent" && len(op.Intent.SkillInputs) > 0 && op.Intent.Scope.NativeID == session.Scope.NativeID && op.Receipt.NativeID == session.Scope.NativeID && op.Receipt.NativeTurn == frame.TurnID {
				matches = append(matches, op)
			}
		}
		if len(matches) != 1 {
			continue
		}
		op := matches[0]
		var snapshot *ExecutionSnapshot
		for _, candidate := range snapshots {
			if candidate != nil && candidate.ID == op.ExecutionSnapshotID {
				if snapshot != nil {
					snapshot = nil
					break
				}
				snapshot = candidate
			}
		}
		if snapshot == nil || snapshot.SessionID != session.ID || snapshot.Cwd != op.Intent.Scope.Cwd || snapshot.StateNamespace != op.Intent.Scope.StateNamespace || digest(snapshot.Instance) != digest(op.Intent.Scope.Instance) || digest(snapshot.Route) != digest(op.Intent.Scope.Route) || digest(snapshot.SkillSelections) != digest(op.Intent.Scope.SkillSelections) || !runtime.EqualPluginSelections(snapshot.PluginSelections, op.Intent.Scope.PluginSelections) {
			continue
		}
		bound, err := runtime.ResolveCodexSkillInputs(op.Intent.Scope, op.Intent.SkillInputs)
		if err != nil {
			continue
		}
		evidence := &runtime.SkillInputEvidence{OperationID: op.ID, SnapshotID: snapshot.ID, Items: []runtime.SkillInputLoadEvidence{}}
		for _, skill := range bound {
			var refs []CapabilityReference
			for _, ref := range snapshot.CapabilityRefs {
				if ref.Asset.ID == skill.AssetID {
					refs = append(refs, ref)
				}
			}
			if len(refs) != 1 {
				evidence = nil
				break
			}
			item := runtime.SkillInputLoadEvidence{SkillInput: skill.SkillInput, AssetVersion: refs[0].Asset.Version, Path: skill.Path, ExpectedSHA256: skill.FileSHA256, Status: "not_observed", RecordIDs: []string{}}
			for _, load := range frame.Loads {
				if load.Name != skill.Name || load.Path != skill.Path {
					continue
				}
				if !slices.Contains(item.RecordIDs, load.ID) {
					item.RecordIDs = append(item.RecordIDs, load.ID)
				}
				if load.BodySHA256 == "" {
					if item.Status != "content_mismatch" {
						item.Status = "unverified_content"
					}
					continue
				}
				if load.BodySHA256 != skill.FileSHA256 {
					item.Status = "content_mismatch"
					continue
				}
				if item.Status == "not_observed" {
					item.Status = "loaded_exact"
				}
			}
			evidence.Items = append(evidence.Items, item)
		}
		frame.InputEvidence = evidence
	}
}
