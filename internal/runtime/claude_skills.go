package runtime

import (
	"slices"
	"strings"
)

type claudeSkillInventory struct {
	Available    bool                     `json:"available"`
	At           string                   `json:"at"`
	Commands     []claudeSkillCommand     `json:"commands"`
	Descriptions []claudeSkillDescription `json:"descriptions"`
}

type claudeSkillCommand struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Builtin     bool     `json:"builtin"`
	Aliases     []string `json:"aliases"`
}

type claudeSkillDescription struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	PluginName string `json:"plugin_name"`
}

func claudeSkillEvidence(scope Scope, inventory claudeSkillInventory) ([]SkillEvidence, bool) {
	result := []SkillEvidence{}
	available := inventory.Available && inventory.Commands != nil && inventory.Descriptions != nil
	for _, command := range inventory.Commands {
		available = available && command.Name != ""
	}
	for _, description := range inventory.Descriptions {
		available = available && description.Name != "" && description.Source != ""
	}
	matched := available
	for _, s := range scope.SkillSelections {
		commands, descriptions := 0, 0
		builtin, sourceMatch := false, false
		identityMatch, aliasesUnique := true, true
		for _, c := range inventory.Commands {
			names := append([]string{c.Name}, c.Aliases...)
			belongs := slices.Contains(names, s.Name)
			if s.Delivery == "local_plugin" {
				belongs = slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, s.PluginName+":") })
			}
			if belongs {
				commands++
				builtin = builtin || c.Builtin
				identityMatch = identityMatch && c.Name == s.Name
				for _, other := range inventory.Commands {
					if other.Name == c.Name {
						continue
					}
					for _, name := range append([]string{other.Name}, other.Aliases...) {
						aliasesUnique = aliasesUnique && !slices.Contains(names, name)
					}
				}
			}
		}
		for _, d := range inventory.Descriptions {
			if d.Name == s.Name || s.Delivery == "local_plugin" && (d.PluginName == s.PluginName || strings.HasPrefix(d.Name, s.PluginName+":")) {
				descriptions++
				sourceMatch = d.Name == s.Name && d.Source == s.Source && (s.Delivery != "local_plugin" || d.PluginName == s.PluginName)
			}
		}
		item := SkillEvidence{AssetID: s.AssetID, Name: s.Name, NativeKey: s.NativeKey, Mode: s.Mode, Source: s.Source, CommandAdvertised: commands > 0, DescriptionListed: descriptions > 0, At: inventory.At, Detail: "Skill 原生命令与描述清单尚未核对；不代表正文已加载或方法已使用"}
		item.Verified = available && identityMatch && aliasesUnique && ((s.Mode == "enabled" && commands == 1 && !builtin && descriptions == 1 && sourceMatch) || (s.Mode == "disabled" && commands == 0 && descriptions == 0))
		if item.Verified {
			item.Detail = "原生命令与描述清单和本次选择一致；正文加载、方法使用与 Gate 仍需独立证据"
		}
		result = append(result, item)
		matched = matched && item.Verified
	}
	return result, matched
}

func claudeSkillChangeVerified(target, previous []SkillSelection, inventory claudeSkillInventory) bool {
	if len(target) == 0 && len(previous) == 0 {
		return true
	}
	if _, matches := claudeSkillEvidence(Scope{SkillSelections: target}, inventory); !matches {
		return false
	}
	for _, old := range previous {
		if old.Delivery != "local_plugin" || slices.ContainsFunc(target, func(s SkillSelection) bool { return s.PluginName == old.PluginName }) {
			continue
		}
		old.Mode = "disabled"
		if _, matches := claudeSkillEvidence(Scope{SkillSelections: []SkillSelection{old}}, inventory); !matches {
			return false
		}
	}
	return true
}
