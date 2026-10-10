package app

import "codex-netcap/internal/runtime"

func claudePluginChanges(before, after *ExecutionSnapshot) []CapabilitySelectionChange {
	old, next := map[string]runtime.PluginSelection{}, map[string]runtime.PluginSelection{}
	versions := func(snapshot *ExecutionSnapshot) map[string]string {
		result := map[string]string{}
		for _, ref := range snapshot.CapabilityRefs {
			result[ref.Asset.ID] = ref.Asset.Version
		}
		return result
	}
	bv, av := versions(before), versions(after)
	for _, s := range before.PluginSelections {
		old[s.AssetID] = s
	}
	for _, s := range after.PluginSelections {
		next[s.AssetID] = s
	}
	changes := []CapabilitySelectionChange{}
	kindAndName := func(p runtime.PluginSelection) (string, string) {
		if p.SkillAsset != nil && len(p.Components) == 1 {
			return "skill", p.Components[0].Name
		}
		return "cli_plugin", p.Name
	}
	for id, s := range old {
		t, exists := next[id]
		if exists && digest(s) == digest(t) && bv[id] == av[id] {
			continue
		}
		kind, name := kindAndName(s)
		change := CapabilitySelectionChange{Kind: kind, AssetID: id, Name: name, Before: s.Mode, After: "inherit", BeforeVersion: bv[id]}
		if s.Hooks != nil {
			change.BeforeHookTrust = s.Hooks.Trust
		}
		if exists {
			change.After, change.AfterVersion = t.Mode, av[id]
			change.Kind, change.Name = kindAndName(t)
			if t.Hooks != nil {
				change.AfterHookTrust = t.Hooks.Trust
			}
		}
		changes = append(changes, change)
	}
	for id, s := range next {
		if _, exists := old[id]; !exists {
			kind, name := kindAndName(s)
			change := CapabilitySelectionChange{Kind: kind, AssetID: id, Name: name, Before: "inherit", After: s.Mode, AfterVersion: av[id]}
			if s.Hooks != nil {
				change.AfterHookTrust = s.Hooks.Trust
			}
			changes = append(changes, change)
		}
	}
	return changes
}
