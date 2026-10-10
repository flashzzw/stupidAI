package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func unsupportedSkillCatalog() error {
	return fmt.Errorf("此后端尚无当前查询的 Skill 来源清单入口")
}

func (c *Claude) ReadSkillCatalog(ctx context.Context, scope Scope) (SkillSourceCatalog, error) {
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || scope.NativeID == "" || scope.StateNamespace == "" || !filepath.IsAbs(scope.Cwd) {
		return SkillSourceCatalog{}, unsupportedSkillCatalog()
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return SkillSourceCatalog{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Do not use client(): opening a bridge is unnecessary for a catalog read,
	// and an idle/reaped query must not be replaced merely to fill this panel.
	p := c.clients[scope.Instance.ID]
	if c.closing || p == nil {
		return SkillSourceCatalog{}, fmt.Errorf("原生查询未开启；读取清单不会启动或恢复查询")
	}
	var result struct {
		claudeSnapshot
		Inventory claudeSkillInventory `json:"inventory"`
	}
	if err := p.call(ctx, "skill_catalog", map[string]any{"native_id": scope.NativeID}, &result); err != nil {
		return SkillSourceCatalog{}, fmt.Errorf("当前原生 Skill 清单不可用；请确认原查询仍开启且没有正在改配")
	}
	if result.Config.ID != scope.NativeID || result.Config.Cwd != scope.Cwd || result.Config.Model != scope.Route.Model || result.Config.Endpoint != scope.Route.Endpoint || result.Config.Executable != scope.Instance.Executable || result.Config.SettingsFile != scope.Instance.SettingsFile || !slices.Equal(result.Config.MCPSelections, scope.MCPSelections) || !slices.Equal(result.Config.SkillSelections, scope.SkillSelections) || !EqualPluginSelections(result.Config.PluginSelections, scope.PluginSelections) {
		return SkillSourceCatalog{}, fmt.Errorf("Skill 清单不属于当前保存的原生配置")
	}
	return claudeSkillCatalog(scope, result.Inventory)
}

func claudeSkillCatalog(scope Scope, inventory claudeSkillInventory) (SkillSourceCatalog, error) {
	out := SkillSourceCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.sdk.current_query", At: inventory.At, Items: []SkillSourceItem{}, Limitations: []string{
		"仅展示当前查询报告的命令与 Skill 描述，不是完整磁盘扫描；被隐藏的其他项可能不在清单中。",
		"SDK 未报告完整来源路径；快照绑定入口只来自已保存选择，不据显示名推断其他文件路径。",
		"命令或描述出现不代表正文已加载、方法已使用或 Gate 已通过；不能用全局白名单代替独立排除。",
	}}
	if !inventory.Available || inventory.Commands == nil || inventory.Descriptions == nil || len(inventory.Commands)+len(inventory.Descriptions) > 2048 {
		return out, fmt.Errorf("原生 Skill 清单不完整或超过读取上限")
	}
	valid := func(s string, max int) bool { return s != "" && len(s) <= max && !strings.ContainsRune(s, '\x00') }
	for _, c := range inventory.Commands {
		if !valid(c.Name, 300) || len(c.Description) > 4000 || len(c.Aliases) > 64 {
			return out, fmt.Errorf("原生命令元数据无效，未返回部分清单")
		}
		for _, alias := range c.Aliases {
			if !valid(alias, 300) {
				return out, fmt.Errorf("原生命令别名无效")
			}
		}
	}
	for _, d := range inventory.Descriptions {
		if !valid(d.Name, 300) || !valid(d.Source, 100) || len(d.PluginName) > 300 {
			return out, fmt.Errorf("原生 Skill 描述来源无效")
		}
	}
	appendItem := func(name, source, plugin string, described bool) {
		item := SkillSourceItem{Name: name, Source: source, PluginName: plugin, DescriptionListed: described, Aliases: []string{}, Control: "unavailable", Detail: "原生清单未报告可绑定来源，工作台不能确认独立排除此项。"}
		commands, descriptions := 0, 0
		for _, c := range inventory.Commands {
			if c.Name == name {
				commands++
				item.CommandAdvertised = true
				item.Builtin = item.Builtin || c.Builtin
				item.Description = c.Description
				item.Aliases = append(item.Aliases, c.Aliases...)
			}
		}
		for _, d := range inventory.Descriptions {
			if d.Name == name {
				descriptions++
			}
		}
		slices.Sort(item.Aliases)
		item.Aliases = slices.Compact(item.Aliases)
		item.Ambiguous = commands > 1 || descriptions > 1 || described && item.Builtin && source != "built-in"
		for _, c := range inventory.Commands {
			if c.Name == name {
				continue
			}
			for _, other := range append([]string{c.Name}, c.Aliases...) {
				if other == name || slices.Contains(item.Aliases, other) {
					item.Ambiguous = true
				}
			}
		}
		if !described && item.Builtin {
			item.Source = "builtin"
			item.Detail = "CLI 内置命令；未报告 Skill 描述，不能当作可独立选配的 Skill。"
		}
		if described && (source == "userSettings" || source == "projectSettings") {
			item.Control = "requires_binding"
			item.Detail = "需先登记 Skill 资产并唯一核对原生目录入口；仅凭显示名不能独立选配。"
		}
		if source == "policySettings" {
			item.Detail = "原生报告受管来源；工作台尚无单项选配入口，不能确认独立排除。"
		}
		if source == "plugin" {
			item.Detail = "来自 CLI 插件；此处没有已核对的单项资产绑定，工作台尚不能独立排除。"
		}
		if source == "built-in" {
			item.Detail = "原生内置 Skill；工作台尚无单项选配入口，不能确认独立排除。"
		}
		if item.Ambiguous {
			item.Control = "ambiguous"
			item.Detail = "名称、别名或描述来源存在重叠，不能确认独立选配；不会合并成一个已验证来源。"
		} else if described {
			matches := []SkillSelection{}
			for _, s := range scope.SkillSelections {
				if s.Name == name && s.Source == source && (s.Delivery != "local_plugin" || s.PluginName == plugin) {
					matches = append(matches, s)
				}
			}
			if len(matches) == 1 {
				s := matches[0]
				item.AssetID, item.BoundPath, item.ConfiguredMode = s.AssetID, s.DiscoveryPath, s.Mode
				item.Control, item.Detail = "asset_bound", "已关联保存的资产选择；实际生效以执行快照的单独核对结果为准。"
			} else if len(matches) > 1 {
				item.Ambiguous, item.Control, item.Detail = true, "ambiguous", "多个保存选择指向这一原生名称，不能唯一关联资产。"
			}
		}
		out.Items = append(out.Items, item)
	}
	for _, d := range inventory.Descriptions {
		appendItem(d.Name, d.Source, d.PluginName, true)
	}
	seen := map[string]bool{}
	for _, c := range inventory.Commands {
		if !seen[c.Name] && !slices.ContainsFunc(inventory.Descriptions, func(d claudeSkillDescription) bool { return d.Name == c.Name }) {
			appendItem(c.Name, "unknown", "", false)
		}
		seen[c.Name] = true
	}
	slices.SortFunc(out.Items, func(a, b SkillSourceItem) int {
		return strings.Compare(a.Name+"/"+a.Source+"/"+a.PluginName, b.Name+"/"+b.Source+"/"+b.PluginName)
	})
	return out, nil
}
