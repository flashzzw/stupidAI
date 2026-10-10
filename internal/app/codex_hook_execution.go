package app

import (
	"codex-netcap/internal/runtime"
	"path/filepath"
)

func unresolvedCodexHooks(session Session) bool {
	if session.Scope.Instance.Kind != "codex" {
		return false
	}
	for _, hook := range session.View.HookExecutions {
		if hook.NativeRunID != "" && (hook.Status == "running" || hook.Status == "unknown") {
			return true
		}
	}
	return false
}

func retainCodexHookHistory(previous Session, view *runtime.View) {
	if previous.Scope.Instance.Kind != "codex" || previous.Scope.NativeID == "" || previous.Scope.NativeID != view.NativeID {
		return
	}
	runtime.RetainCodexHookExecutions(previous.View.HookExecutions, view)
}

func bindCodexHookOperations(session *Session, operations map[string]Operation) {
	if session.Scope.Instance.Kind != "codex" {
		return
	}
	for i := range session.View.HookExecutions {
		hook := &session.View.HookExecutions[i]
		if hook.NativeRunID == "" || hook.TurnID == "" {
			continue
		}
		matches := []string{}
		for id, op := range operations {
			if op.SessionID == session.ID && op.Receipt.NativeID == session.Scope.NativeID && op.Receipt.NativeTurn == hook.TurnID && createsInput(op.Intent) {
				matches = append(matches, id)
			}
		}
		if hook.Operation == "" && len(matches) == 1 {
			hook.Operation = matches[0]
		}
		if hook.AssetID != "" || hook.Source != "plugin" || len(matches) != 1 || hook.Operation != matches[0] {
			continue
		}
		op := operations[hook.Operation]
		var snapshot *ExecutionSnapshot
		for _, candidate := range append([]*ExecutionSnapshot{session.ExecutionSnapshot}, session.ExecutionHistory...) {
			if candidate != nil && candidate.ID == op.ExecutionSnapshotID {
				snapshot = candidate
			}
		}
		if snapshot == nil {
			continue
		}
		var matched *runtime.PluginSelection
		count := 0
		for n := range snapshot.PluginSelections {
			pack := &snapshot.PluginSelections[n]
			if pack.Hooks == nil {
				continue
			}
			for _, definition := range pack.Hooks.Inspection.Definitions {
				if definition.Event == hook.Event && hook.SourcePath == filepath.Join(pack.CachePath, filepath.FromSlash(definition.Path)) {
					matched = pack
					count++
					break
				}
			}
		}
		if count != 1 {
			continue
		}
		for _, ref := range snapshot.CapabilityRefs {
			if ref.Asset.ID == matched.AssetID && ref.Source.SHA256 == matched.SourceHash {
				hook.AssetID, hook.AssetVersion, hook.SourceHash, hook.ExecutionSnapshotID = ref.Asset.ID, ref.Asset.Version, ref.Source.SHA256, snapshot.ID
			}
		}
	}
}
