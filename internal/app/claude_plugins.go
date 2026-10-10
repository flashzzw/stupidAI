package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"codex-netcap/internal/runtime"
)

func claudePluginSelection(scope runtime.Scope, ref CapabilityReference, mode string) (runtime.PluginSelection, error) {
	if scope.Instance.Kind != "claude" {
		return runtime.PluginSelection{}, fmt.Errorf("CLI 插件选择需要 Claude 执行")
	}
	out, err := claudePluginResource(ref, mode)
	if err != nil {
		return out, err
	}
	// Inline delivery can shadow an installed source with the same name.
	if err := rejectInstalledPluginName(scope.Instance.Profile, out.Name); err != nil {
		return out, err
	}
	return out, nil
}

func claudePluginResource(ref CapabilityReference, mode string) (runtime.PluginSelection, error) {
	var out runtime.PluginSelection
	if ref.Asset.Kind != "cli_plugin" || !slices.Contains([]string{"enabled", "disabled"}, mode) {
		return out, fmt.Errorf("CLI 插件选择需要本地插件与 enabled/disabled 模式")
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return out, err
	}
	if stamp != ref.Source || tree.Kind != "directory" {
		return out, fmt.Errorf("插件资源需要已核对的完整目录")
	}
	manifest, err := jsonObject(tree.files()[".claude-plugin/plugin.json"])
	if err != nil {
		return out, fmt.Errorf("Claude 插件需要有效的 .claude-plugin/plugin.json")
	}
	name, _ := manifest["name"].(string)
	version, _ := manifest["version"].(string)
	if !validID(name) || strings.HasPrefix(name, "netcap-skill-") || len(name) > 100 || len(version) > 100 {
		return out, fmt.Errorf("插件原生名称或声明版本无效")
	}
	for key := range manifest {
		if !slices.Contains([]string{"name", "version", "description", "author", "homepage", "repository", "license", "keywords", "skills", "commands", "agents", "hooks", "mcpServers"}, key) {
			return out, fmt.Errorf("插件字段 %s 的运行影响尚不能完整核对", key)
		}
	}
	for _, p := range []string{".lsp.json", "settings.json", "output-styles", "outputStyles"} {
		if tree.exists(p) {
			return out, fmt.Errorf("插件包含尚不能核对的自动组件：%s", p)
		}
	}
	diagnosis := diagnoseResources(ref.Asset, tree)
	if diagnosis.Status == "invalid" {
		return out, fmt.Errorf("插件结构或依赖无效，请先核对能力库诊断")
	}
	for _, issue := range diagnosis.Issues {
		if issue.Code == "duplicate_mcp" {
			return out, fmt.Errorf("插件包含重复 MCP 定义，不能确认其来源")
		}
	}
	out = runtime.PluginSelection{AssetID: ref.Asset.ID, Name: name, Mode: mode, Version: version, SourcePath: stamp.Resolved, SourceHash: stamp.SHA256, RootMode: tree.Mode, Files: []runtime.PluginFile{}, Components: []runtime.PluginComponent{}}
	for _, entry := range tree.Entries {
		out.Files = append(out.Files, runtime.PluginFile{Path: entry.Path, Kind: entry.Kind, Mode: entry.Mode, SHA256: entry.SHA256})
	}
	for _, c := range diagnosis.Components {
		if c.Backend != "claude" || c.Kind == "plugin" {
			continue
		}
		component := runtime.PluginComponent{Kind: c.Kind, Name: c.Name, Path: c.Path, NativeName: name + ":" + c.Name, Count: 1}
		switch c.Kind {
		case "skill", "command":
		case "agent":
			metadata, e := skillDocumentMetadata(tree.files()[c.Path])
			if e != nil {
				return out, e
			}
			if n, ok := metadata["name"].(string); ok && n != "" {
				component.NativeName = name + ":" + n
			}
		case "mcp":
			if !validID(c.Name) {
				return out, fmt.Errorf("插件 MCP 名称尚不能唯一映射")
			}
			component.NativeName = "plugin:" + name + ":" + c.Name
		case "hook":
			component.NativeName = c.Name
			object, e := jsonObject(tree.files()[c.Path])
			if e != nil {
				return out, e
			}
			hooks, ok := object["hooks"].(map[string]any)
			if !ok {
				return out, fmt.Errorf("插件 hook 定义无效")
			}
			// A manifest embeds the same hooks object as hooks/hooks.json.
			if nested, ok := hooks["hooks"].(map[string]any); ok {
				hooks = nested
			}
			rows, ok := hooks[c.Name].([]any)
			if !ok {
				return out, fmt.Errorf("插件 hook 事件需要匹配项数组")
			}
			component.Count = 0
			for _, row := range rows {
				m, ok := row.(map[string]any)
				if !ok {
					return out, fmt.Errorf("插件 hook 匹配项无效")
				}
				entries, ok := m["hooks"].([]any)
				if !ok {
					return out, fmt.Errorf("插件 hook 缺少执行项")
				}
				component.Count += len(entries)
			}
		default:
			return out, fmt.Errorf("插件组件 %s 尚不能核对，未加载此包", c.Kind)
		}
		if component.Count < 1 || component.Count > 128 {
			return out, fmt.Errorf("插件组件数量无效或超过上限")
		}
		if slices.ContainsFunc(out.Components, func(old runtime.PluginComponent) bool {
			return old.Kind == component.Kind && old.NativeName == component.NativeName
		}) {
			return out, fmt.Errorf("插件含重复组件标识，不能唯一核对")
		}
		out.Components = append(out.Components, component)
	}
	if len(out.Components) > 128 {
		return out, fmt.Errorf("插件组件超过核对上限")
	}
	return out, nil
}

func rejectInstalledPluginName(profile, name string) error {
	if _, err := os.Lstat(filepath.Join(profile, "plugins")); os.IsNotExist(err) {
		return nil
	}
	stamp, err := stampSource(filepath.Join(profile, "plugins", "installed_plugins.json"), true)
	if err != nil {
		return err
	}
	if stamp.Kind == "absent" {
		return nil
	}
	tree, _, err := readCapabilityResources(CapabilityAsset{Source: stamp.Resolved})
	if err != nil {
		return err
	}
	object, err := jsonObject(tree.files()[tree.Name])
	if err != nil {
		return fmt.Errorf("原生插件登记表不可读取，无法核对同名来源")
	}
	plugins, ok := object["plugins"].(map[string]any)
	if !ok {
		return fmt.Errorf("原生插件登记表缺少来源列表")
	}
	for key := range plugins {
		if strings.SplitN(key, "@", 2)[0] == name {
			return fmt.Errorf("插件 %s 已有同名原生安装来源；当前不能保证独立启停此资产", name)
		}
	}
	return nil
}
