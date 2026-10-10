package runtime

import (
	"slices"
	"strings"
)

type claudePluginPackage struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Version string `json:"version"`
}

type claudePluginInventory struct {
	Available bool                  `json:"available"`
	At        string                `json:"at"`
	Packages  []claudePluginPackage `json:"packages"`
	Skills    claudeSkillInventory  `json:"skills"`
	Agents    []struct {
		Name string `json:"name"`
	} `json:"agents"`
	Hooks claudeHookInventory `json:"hooks"`
	MCP   claudeMCPInventory  `json:"mcp"`
}

// Package metadata and every declared component must agree. This is loading
// evidence only; a registered hook or tool has not necessarily executed.
func claudePluginEvidence(scope Scope, inventory claudePluginInventory) ([]PluginEvidence, bool) {
	out := []PluginEvidence{}
	all := true
	for _, selection := range claudeExecutionPlugins(scope) {
		matched := claudePluginMatches(selection, inventory)
		detail := "CLI 插件来源、版本或组件尚未核对一致"
		if matched {
			detail = "原生包与组件清单匹配；不代表 hook 执行、工具调用或方法使用成功"
		}
		out = append(out, PluginEvidence{AssetID: selection.AssetID, Name: selection.Name, Mode: selection.Mode, Version: selection.Version, NativeSource: claudePluginSource(selection), Verified: matched, At: inventory.At, Detail: detail, Components: selection.Components})
		all = all && matched
	}
	return out, all
}

func claudePluginChangeVerified(target, previous []PluginSelection, inventory claudePluginInventory) bool {
	for _, p := range target {
		if !claudePluginMatches(p, inventory) {
			return false
		}
	}
	for _, p := range previous {
		if slices.ContainsFunc(target, func(t PluginSelection) bool { return t.Name == p.Name }) {
			continue
		}
		p.Mode = "disabled"
		if !claudePluginMatches(p, inventory) {
			return false
		}
	}
	return true
}

func claudePluginMatches(p PluginSelection, in claudePluginInventory) bool {
	if !in.Available || in.At == "" || in.Packages == nil || !in.Skills.Available || in.Skills.Commands == nil || in.Skills.Descriptions == nil || in.Agents == nil || !in.Hooks.Available || in.Hooks.Items == nil || in.Hooks.Policy == nil || !in.MCP.Available || in.MCP.Servers == nil {
		return false
	}
	packages := []claudePluginPackage{}
	for _, pack := range in.Packages {
		if pack.Name == p.Name {
			packages = append(packages, pack)
		}
	}
	names := map[string][]string{}
	valid := true
	for i, c := range in.Skills.Commands {
		if c.Name == "" {
			return false
		}
		aliases := append([]string{c.Name}, c.Aliases...)
		if !slices.ContainsFunc(aliases, func(n string) bool { return strings.HasPrefix(n, p.Name+":") }) {
			continue
		}
		names["command"] = append(names["command"], c.Name)
		valid = valid && !c.Builtin
		for j, other := range in.Skills.Commands {
			if i != j && slices.ContainsFunc(append([]string{other.Name}, other.Aliases...), func(n string) bool { return slices.Contains(aliases, n) }) {
				valid = false
			}
		}
	}
	for _, d := range in.Skills.Descriptions {
		if d.Name == "" || d.Source == "" {
			return false
		}
		if d.PluginName == p.Name || strings.HasPrefix(d.Name, p.Name+":") {
			names["description"] = append(names["description"], d.Name)
			valid = valid && d.Source == "plugin" && d.PluginName == p.Name
		}
	}
	for _, a := range in.Agents {
		if a.Name == "" {
			return false
		}
		if strings.HasPrefix(a.Name, p.Name+":") {
			names["agent"] = append(names["agent"], a.Name)
		}
	}
	for _, h := range in.Hooks.Items {
		if h.PluginName == p.Name || strings.HasPrefix(h.PluginName, p.Name+"@") {
			names["hook"] = append(names["hook"], h.Event)
			valid = valid && h.PluginName == claudePluginSource(p) && !h.Disabled
		}
	}
	for _, s := range in.MCP.Servers {
		if s.Name == "" {
			return false
		}
		if strings.HasPrefix(s.Name, "plugin:"+p.Name+":") {
			names["mcp"] = append(names["mcp"], s.Name)
			valid = valid && s.Source == "plugin" && s.Status == "connected"
		}
	}
	for _, component := range p.Components {
		if component.Kind != "mcp" {
			continue
		}
		aliases := 0
		for _, server := range in.MCP.Servers {
			if claudeMCPToolPrefix(server.Name) == claudeMCPToolPrefix(component.NativeName) {
				aliases++
			}
		}
		if p.Mode == "enabled" && aliases != 1 || p.Mode == "disabled" && aliases != 0 {
			return false
		}
	}
	if p.Mode == "disabled" {
		return len(packages) == 0 && len(names) == 0
	}
	if p.Mode != "enabled" || !valid || len(packages) != 1 || packages[0].Path != p.SourcePath || packages[0].Source != claudePluginSource(p) || packages[0].Version != p.Version {
		return false
	}
	if len(names["hook"]) > 0 && (in.Hooks.Policy.PolicyUnreadable || in.Hooks.Policy.SettingsErrorCount > 0) {
		return false
	}
	expected := map[string][]string{}
	for _, c := range p.Components {
		kind := c.Kind
		if kind == "skill" {
			kind = "command"
		}
		if c.Count < 1 || c.Count > 128 || !slices.Contains([]string{"command", "agent", "hook", "mcp"}, kind) {
			return false
		}
		for i := 0; i < c.Count; i++ {
			expected[kind] = append(expected[kind], c.NativeName)
			if kind == "command" {
				expected["description"] = append(expected["description"], c.NativeName)
			}
		}
	}
	for _, kind := range []string{"command", "description", "agent", "hook", "mcp"} {
		slices.Sort(expected[kind])
		slices.Sort(names[kind])
		if !slices.Equal(expected[kind], names[kind]) {
			return false
		}
	}
	return true
}
