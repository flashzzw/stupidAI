package app

import "codex-netcap/internal/runtime"

func claudeSkillChanges(before, after *ExecutionSnapshot) []CapabilitySelectionChange {
	old, next := map[string]runtime.SkillSelection{}, map[string]runtime.SkillSelection{}
	refs := func(snapshot *ExecutionSnapshot) map[string]CapabilityReference {
		result := map[string]CapabilityReference{}
		for _, ref := range snapshot.CapabilityRefs {
			result[ref.Asset.ID] = ref
		}
		return result
	}
	br, ar := refs(before), refs(after)
	for _, s := range before.SkillSelections {
		old[s.AssetID] = s
	}
	for _, s := range after.SkillSelections {
		next[s.AssetID] = s
	}
	changes := []CapabilitySelectionChange{}
	for id, s := range old {
		t, exists := next[id]
		if exists && digest(s) == digest(t) && digest(br[id]) == digest(ar[id]) {
			continue
		}
		change := CapabilitySelectionChange{Kind: "skill", AssetID: id, Name: br[id].Asset.Name, Before: s.Mode, After: "inherit", BeforeVersion: br[id].Asset.Version}
		if exists {
			change.After, change.AfterVersion, change.Name = t.Mode, ar[id].Asset.Version, ar[id].Asset.Name
		}
		changes = append(changes, change)
	}
	for id, s := range next {
		if _, exists := old[id]; !exists {
			changes = append(changes, CapabilitySelectionChange{Kind: "skill", AssetID: id, Name: ar[id].Asset.Name, Before: "inherit", After: s.Mode, AfterVersion: ar[id].Asset.Version})
		}
	}
	return changes
}
