package runtime

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// An installed override newly introduced by the failed candidate returns to
// inheritance. Its original components must match the saved native inventory;
// treating that package as a retired inline namespace would disable it instead.
func claudeRecoveredInheritedPlugins(scope Scope, change *claudeConfigurationChange, current claudePluginInventory) bool {
	if change.Recovery != nil && change.Recovery.OriginalQueryRetry != nil && !claudeOriginalQueryRecoveryMatches(change, change.Recovery.OriginalQueryRetry) {
		return false
	}
	if change.InstalledTarget == nil && change.InstalledPrevious == nil {
		return true
	}
	complete := func(in claudePluginInventory) bool {
		return in.Available && in.Packages != nil && in.Skills.Available && in.Skills.Commands != nil && in.Skills.Descriptions != nil && in.Agents != nil && in.Hooks.Available && in.Hooks.Items != nil && in.Hooks.Policy != nil && in.MCP.Available && in.MCP.Servers != nil
	}
	before := change.OriginalPlugins
	if before == nil {
		if change.Recovery == nil || !claudeOriginalQueryRecoveryMatches(change, change.Recovery.OriginalQueryRetry) || change.Status != "recovered" || change.Recovery.Status != "verified" {
			return false
		}
		before = &change.Recovery.Plugins
	}
	if !complete(*before) || !complete(current) {
		return false
	}
	plugins := append(claudeExecutionPlugins(scope), change.PluginTarget...)
	skills := append(slices.Clone(scope.SkillSelections), change.SkillTarget...)
	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Name] = true
	}
	for _, s := range skills {
		if s.Delivery != "" && s.PluginName != "" {
			names[s.PluginName] = true
		}
	}
	pluginName := func(name string) bool {
		for p := range names {
			if name == p || strings.HasPrefix(name, p+"@") {
				return true
			}
		}
		return false
	}
	commandName := func(name string) bool {
		for p := range names {
			if strings.HasPrefix(name, p+":") {
				return true
			}
		}
		return slices.ContainsFunc(skills, func(s SkillSelection) bool { return s.Name == name })
	}
	mcp := map[string]bool{}
	for _, s := range append(slices.Clone(scope.MCPSelections), change.Target...) {
		mcp[s.Server] = true
	}
	adopted := map[string]bool{}
	for _, after := range change.SettingsTarget {
		if slices.Contains(change.SettingsPrevious, after) {
			continue
		}
		kind := "projectSettings"
		if filepath.Base(after.Path) == "settings.local.json" {
			kind = "localSettings"
		}
		adopted[kind] = true
	}
	rows := func(in claudePluginInventory) map[string][]string {
		out := map[string][]string{}
		add := func(kind string, value any) {
			body, _ := json.Marshal(value)
			out[kind] = append(out[kind], string(body))
		}
		for _, p := range in.Packages {
			if !pluginName(p.Name) {
				add("packages", p)
			}
		}
		for _, c := range in.Skills.Commands {
			if !slices.ContainsFunc(append([]string{c.Name}, c.Aliases...), commandName) {
				add("commands", c)
			}
		}
		for _, d := range in.Skills.Descriptions {
			if !commandName(d.Name) && !pluginName(d.PluginName) {
				add("descriptions", d)
			}
		}
		for _, a := range in.Agents {
			if !commandName(a.Name) {
				add("agents", a)
			}
		}
		for _, h := range in.Hooks.Items {
			if !pluginName(h.PluginName) && !(h.PluginName == "" && adopted[h.Source]) {
				add("hooks", h)
			}
		}
		for _, s := range in.MCP.Servers {
			selected := mcp[s.Name]
			for p := range names {
				selected = selected || strings.HasPrefix(s.Name, "plugin:"+p+":")
			}
			if !selected {
				add("mcp", s)
			}
		}
		add("policy", in.Hooks.Policy)
		for _, values := range out {
			slices.Sort(values)
		}
		return out
	}
	return reflect.DeepEqual(rows(*before), rows(current))
}
