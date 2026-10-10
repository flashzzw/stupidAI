package app

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"codex-netcap/internal/runtime"
)

// A request patches only named assets. Omitted assets keep their pinned version;
// inherit releases the explicit binding and its source dependency. Library
// changes never rewrite a saved candidate or a previous execution snapshot.
func (a *Application) resourceExecutionCandidate(session Session, picks []CapabilitySelection) (*ExecutionSnapshot, []CapabilitySelectionChange, error) {
	before := session.ExecutionSnapshot
	candidate := queryCopy(before)
	seen := map[string]bool{}
	for _, pick := range picks {
		if seen[pick.AssetID] || !validID(pick.AssetID) || !slices.Contains([]string{"enabled", "disabled", "inherit"}, pick.Mode) {
			return nil, nil, fmt.Errorf("能力选择重复或模式无效")
		}
		seen[pick.AssetID] = true
		refIndex := slices.IndexFunc(candidate.CapabilityRefs, func(r CapabilityReference) bool { return r.Asset.ID == pick.AssetID })
		if session.Scope.Instance.Kind == "codex" {
			asset, _ := a.asset(pick.AssetID)
			if refIndex >= 0 {
				asset = candidate.CapabilityRefs[refIndex].Asset
			}
			if asset.Kind != "cli_plugin" && asset.Kind != "skill" && !(asset.Kind == "mcp" && asset.CodexMCP == "") {
				if asset.Kind != "mcp" || !slices.ContainsFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID }) || pick.Mode == "inherit" || pick.Version != "" {
					return nil, nil, fmt.Errorf("Codex profile 内的 MCP 映射只支持原已选服务启停；资源定义可新增、换版本或恢复继承")
				}
			}
		}
		selected := slices.ContainsFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID }) || slices.ContainsFunc(candidate.SkillSelections, func(s runtime.SkillSelection) bool { return s.AssetID == pick.AssetID }) || slices.ContainsFunc(candidate.PluginSelections, func(s runtime.PluginSelection) bool { return s.AssetID == pick.AssetID })
		if pick.Mode == "inherit" {
			if pick.Version != "" || pick.HookReview != nil {
				return nil, nil, fmt.Errorf("恢复继承不能指定资产版本")
			}
			if !selected {
				continue
			}
			candidate.MCPSelections = slices.DeleteFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID })
			candidate.SkillSelections = slices.DeleteFunc(candidate.SkillSelections, func(s runtime.SkillSelection) bool { return s.AssetID == pick.AssetID })
			candidate.PluginSelections = slices.DeleteFunc(candidate.PluginSelections, func(s runtime.PluginSelection) bool { return s.AssetID == pick.AssetID })
			candidate.CapabilityRefs = slices.DeleteFunc(candidate.CapabilityRefs, func(r CapabilityReference) bool { return r.Asset.ID == pick.AssetID })
			continue
		}
		// An explicit new version is admitted through the same source/format
		// checks as creation. Selecting the same mode alone cannot upgrade it.
		replace := !selected || pick.Version != "" && (refIndex < 0 || candidate.CapabilityRefs[refIndex].Asset.Version != pick.Version)
		if replace {
			fresh, err := a.captureExecution(session, nil, []CapabilitySelection{pick})
			if err != nil {
				return nil, nil, err
			}
			candidate.MCPSelections = slices.DeleteFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID })
			candidate.SkillSelections = slices.DeleteFunc(candidate.SkillSelections, func(s runtime.SkillSelection) bool { return s.AssetID == pick.AssetID })
			candidate.PluginSelections = slices.DeleteFunc(candidate.PluginSelections, func(s runtime.PluginSelection) bool { return s.AssetID == pick.AssetID })
			candidate.MCPSelections = append(candidate.MCPSelections, fresh.MCPSelections...)
			candidate.SkillSelections = append(candidate.SkillSelections, fresh.SkillSelections...)
			candidate.PluginSelections = append(candidate.PluginSelections, fresh.PluginSelections...)
			if refIndex < 0 {
				candidate.CapabilityRefs = append(candidate.CapabilityRefs, fresh.CapabilityRefs[0])
			} else {
				candidate.CapabilityRefs[refIndex] = fresh.CapabilityRefs[0]
			}
			continue
		}
		if refIndex < 0 {
			return nil, nil, fmt.Errorf("原能力缺少固定来源引用")
		}
		if pick.Mode == "enabled" && (slices.ContainsFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID && s.Mode != pick.Mode }) || slices.ContainsFunc(candidate.SkillSelections, func(s runtime.SkillSelection) bool { return s.AssetID == pick.AssetID && s.Mode != pick.Mode }) || slices.ContainsFunc(candidate.PluginSelections, func(s runtime.PluginSelection) bool { return s.AssetID == pick.AssetID && s.Mode != pick.Mode })) {
			asset, ok := a.asset(pick.AssetID)
			if !ok || asset.Disabled || digest(asset) != digest(candidate.CapabilityRefs[refIndex].Asset) {
				return nil, nil, fmt.Errorf("能力 %s 已停用、移除或登记版本变化；需明确选择可用版本", pick.AssetID)
			}
		}
		for i := range candidate.SkillSelections {
			if candidate.SkillSelections[i].AssetID == pick.AssetID {
				candidate.SkillSelections[i].Mode = pick.Mode
			}
		}
		for i := range candidate.MCPSelections {
			if candidate.MCPSelections[i].AssetID == pick.AssetID {
				candidate.MCPSelections[i].Mode = pick.Mode
			}
		}
		for i := range candidate.PluginSelections {
			if candidate.PluginSelections[i].AssetID == pick.AssetID {
				candidate.PluginSelections[i].Mode = pick.Mode
				if pick.HookReview != nil || pick.hookBinding != nil {
					if session.Scope.Instance.Kind != "codex" {
						return nil, nil, fmt.Errorf("hook 信任选择只适用于 Codex 插件")
					}
					if err := a.bindCodexHookReview(session.Scope, candidate.CapabilityRefs[refIndex], pick, &candidate.PluginSelections[i]); err != nil {
						return nil, nil, err
					}
				}
			}
		}
		if pick.HookReview != nil && !slices.ContainsFunc(candidate.PluginSelections, func(p runtime.PluginSelection) bool { return p.AssetID == pick.AssetID && p.Hooks != nil }) {
			return nil, nil, fmt.Errorf("所选资源没有可审核的 Codex hook")
		}
	}
	if len(candidate.CapabilityRefs) > 16 || len(candidate.MCPSelections) > 16 || len(candidate.SkillSelections) > 16 || len(candidate.PluginSelections) > 16 {
		return nil, nil, fmt.Errorf("引用、MCP 服务、Skill 和插件各不能超过 16 项")
	}
	pluginNames, pluginPaths := map[string]bool{}, map[string]bool{}
	for _, p := range candidate.PluginSelections {
		if pluginNames[p.Name] || pluginPaths[p.SourcePath] {
			return nil, nil, fmt.Errorf("所选插件共享原生名称或来源，不能独立改配")
		}
		pluginNames[p.Name], pluginPaths[p.SourcePath] = true, true
	}
	slices.SortFunc(candidate.PluginSelections, func(a, b runtime.PluginSelection) int { return strings.Compare(a.AssetID, b.AssetID) })
	if len(candidate.PluginSelections) == 0 {
		candidate.PluginSelections = nil
	}
	servers := map[string]bool{}
	for _, s := range candidate.MCPSelections {
		if servers[s.Server] {
			return nil, nil, fmt.Errorf("所选资产包含同名 MCP：%s", s.Server)
		}
		servers[s.Server] = true
	}
	if session.Scope.Instance.Kind == "codex" {
		for _, pack := range candidate.PluginSelections {
			for _, component := range pack.Components {
				if component.Kind != "mcp" {
					continue
				}
				if servers[component.NativeName] {
					return nil, nil, fmt.Errorf("所选插件与其他能力包含同名 MCP：%s", component.NativeName)
				}
				servers[component.NativeName] = true
			}
		}
	}
	keys := map[string]bool{}
	for _, skill := range candidate.SkillSelections {
		if keys[skill.NativeKey] {
			return nil, nil, fmt.Errorf("所选 Skill 共享原生键，不能独立改配")
		}
		keys[skill.NativeKey] = true
	}
	slices.SortFunc(candidate.SkillSelections, func(a, b runtime.SkillSelection) int { return strings.Compare(a.AssetID, b.AssetID) })
	if len(candidate.SkillSelections) == 0 {
		candidate.SkillSelections = nil
	}
	// Sort after replacement so the same candidate has one stable digest even
	// when its selections are reconstructed after a lost receipt or restart.
	slices.SortFunc(candidate.MCPSelections, func(a, b runtime.MCPSelection) int {
		return strings.Compare(a.AssetID+"/"+a.Server, b.AssetID+"/"+b.Server)
	})
	slices.SortFunc(candidate.CapabilityRefs, func(a, b CapabilityReference) int { return strings.Compare(a.Asset.ID, b.Asset.ID) })
	if len(candidate.MCPSelections) == 0 {
		candidate.MCPSelections = nil
	}
	return candidate, resourceSelectionChanges(before, candidate), nil
}

func resourceSelectionChanges(before, after *ExecutionSnapshot) []CapabilitySelectionChange {
	key := func(s runtime.MCPSelection) string { return s.AssetID + "/" + s.Server }
	old, next := map[string]runtime.MCPSelection{}, map[string]runtime.MCPSelection{}
	versions := func(snapshot *ExecutionSnapshot) map[string]string {
		out := map[string]string{}
		for _, r := range snapshot.CapabilityRefs {
			out[r.Asset.ID] = r.Asset.Version
		}
		return out
	}
	bv, av := versions(before), versions(after)
	for _, s := range before.MCPSelections {
		old[key(s)] = s
	}
	for _, s := range after.MCPSelections {
		next[key(s)] = s
	}
	changes := []CapabilitySelectionChange{}
	for k, s := range old {
		t, exists := next[k]
		if exists && reflect.DeepEqual(s, t) && bv[s.AssetID] == av[s.AssetID] {
			continue
		}
		mode, version := "inherit", ""
		if exists {
			mode, version = t.Mode, av[t.AssetID]
		}
		changes = append(changes, CapabilitySelectionChange{AssetID: s.AssetID, Server: s.Server, Before: s.Mode, After: mode, BeforeVersion: bv[s.AssetID], AfterVersion: version})
	}
	for k, s := range next {
		if _, exists := old[k]; !exists {
			changes = append(changes, CapabilitySelectionChange{AssetID: s.AssetID, Server: s.Server, Before: "inherit", After: s.Mode, AfterVersion: av[s.AssetID]})
		}
	}
	changes = append(changes, claudeSkillChanges(before, after)...)
	return append(changes, claudePluginChanges(before, after)...)
}

func resourceCandidateSelections(before, after *ExecutionSnapshot) []CapabilitySelection {
	selections := []CapabilitySelection{}
	seen := map[string]bool{}
	for _, change := range resourceSelectionChanges(before, after) {
		if seen[change.AssetID] {
			continue
		}
		seen[change.AssetID] = true
		pick := CapabilitySelection{AssetID: change.AssetID, Mode: "inherit"}
		for _, s := range after.MCPSelections {
			if s.AssetID == pick.AssetID {
				pick.Mode = s.Mode
			}
		}
		for _, s := range after.SkillSelections {
			if s.AssetID == pick.AssetID {
				pick.Mode = s.Mode
			}
		}
		for _, s := range after.PluginSelections {
			if s.AssetID == pick.AssetID {
				pick.Mode = s.Mode
				if s.Hooks != nil {
					saved := queryCopy(s)
					pick.hookBinding = &saved
				}
			}
		}
		if pick.Mode != "inherit" {
			for _, ref := range after.CapabilityRefs {
				if ref.Asset.ID == pick.AssetID && (after.Instance.Kind == "claude" || ref.Asset.Kind == "cli_plugin" || ref.Asset.Kind == "skill" || ref.Asset.Kind == "mcp" && ref.Asset.CodexMCP == "") {
					pick.Version = ref.Asset.Version
				}
			}
		}
		selections = append(selections, pick)
	}
	return selections
}
