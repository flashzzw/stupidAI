package app

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"codex-netcap/internal/runtime"
)

func codexPluginSelection(scope runtime.Scope, ref CapabilityReference, mode string) (runtime.PluginSelection, error) {
	var out runtime.PluginSelection
	if scope.Instance.Kind != "codex" || ref.Asset.Kind != "cli_plugin" || !slices.Contains([]string{"enabled", "disabled"}, mode) {
		return out, fmt.Errorf("Codex 插件需要有效的整包选择")
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return out, err
	}
	if stamp != ref.Source || tree.Kind != "directory" {
		return out, fmt.Errorf("插件需要已核对的完整资源目录")
	}
	definition, issue := resolvePluginManifest(tree, "codex")
	if issue != nil {
		return out, fmt.Errorf("Codex 插件 manifest 无效：%s", issue.Detail)
	}
	portable := definition.Source.Layout == "portable"
	name, _ := definition.Identity["name"].(string)
	version, _ := definition.Identity["version"].(string)
	if portable {
		if definition.Identity["$schema"] != agentPluginSchema {
			return out, fmt.Errorf("通用插件需要声明受支持的 Agent Plugins 1.0.0 plugin schema；否则固定 Codex 可能改读旧格式")
		}
		if tree.exists("mcp.json") {
			mcp, err := jsonObject(tree.files()["mcp.json"])
			if err != nil || mcp["$schema"] != agentMCPSchema {
				return out, fmt.Errorf("通用 mcp.json 需要声明受支持的 Agent Plugins 1.0.0 MCP schema；否则原生不会加载这些服务")
			}
		}
		for key := range definition.Identity {
			if !slices.Contains([]string{"$schema", "name", "version", "description", "author", "homepage", "repository", "license", "keywords", "extensions"}, key) {
				return out, fmt.Errorf("通用插件字段 %s 的组件影响尚未核对", key)
			}
		}
	}
	for key := range definition.Settings {
		if !slices.Contains([]string{"name", "version", "description", "author", "homepage", "repository", "license", "keywords", "mcpServers", "skills"}, key) && !(key == "hooks" && !portable) {
			return out, fmt.Errorf("插件字段 %s 的组件影响尚未核对", key)
		}
	}
	if raw, exists := definition.Settings["skills"]; exists && !portable {
		roots, valid := stringList(raw)
		if !valid || len(roots) == 0 {
			return out, fmt.Errorf("Codex 插件 skills 需要至少一个包内目录")
		}
		for _, root := range roots {
			dir := path.Clean(root)
			found := dir == "." && strings.TrimSpace(root) != ""
			for _, entry := range tree.Entries {
				found = found || entry.Path == dir && entry.Kind == "directory"
			}
			if !found || dir != "." && !resourcePath(dir) {
				return out, fmt.Errorf("Codex 插件 skills 需要存在的包内目录，单文件声明尚未核对")
			}
		}
	}
	for _, p := range []string{"commands", "agents", "settings.json", ".lsp.json", "apps.json"} {
		if tree.exists(p) {
			return out, fmt.Errorf("Codex 插件组件 %s 尚不能完整核对", p)
		}
	}
	diagnosis := diagnoseResources(ref.Asset, tree)
	if diagnosis.Status == "invalid" {
		return out, fmt.Errorf("插件结构或依赖无效，请核对能力库诊断")
	}
	for _, issue := range diagnosis.Issues {
		if issue.Code == "duplicate_mcp" {
			return out, fmt.Errorf("插件有重复 MCP 定义")
		}
	}
	out = runtime.PluginSelection{AssetID: ref.Asset.ID, Name: name, Version: version, Mode: mode, SourcePath: stamp.Resolved, SourceHash: stamp.SHA256, RootMode: tree.Mode, Files: []runtime.PluginFile{}, Components: []runtime.PluginComponent{}}
	if tree.exists("hooks") || definition.Settings["hooks"] != nil || slices.ContainsFunc(diagnosis.Components, func(c CapabilityComponent) bool { return c.Backend == "codex" && c.Kind == "hook" }) {
		pkg, err := capabilityHookPackage(ref.Asset, tree, stamp)
		if err != nil {
			return out, err
		}
		out.Hooks = &runtime.PluginHookSelection{Expected: pkg.Expected}
	}
	if portable {
		out.ManifestSource = &runtime.PluginManifestSource{Identity: definition.Source.Identity, Settings: definition.Source.Settings, Layout: definition.Source.Layout}
	}
	for _, file := range tree.Entries {
		out.Files = append(out.Files, runtime.PluginFile{Path: file.Path, Kind: file.Kind, Mode: file.Mode, SHA256: file.SHA256})
	}
	seen := map[string]bool{}
	for _, c := range diagnosis.Components {
		if c.Backend != "codex" || c.Kind == "plugin" {
			continue
		}
		if !slices.Contains([]string{"mcp", "skill", "hook"}, c.Kind) || !validID(c.Name) {
			return out, fmt.Errorf("Codex 插件组件 %s 尚不能完整核对", c.Kind)
		}
		componentKey := c.Kind + "/" + c.Name
		if c.Kind == "hook" {
			componentKey += "/" + c.Path
		}
		if seen[componentKey] {
			return out, fmt.Errorf("Codex 插件存在同名组件，不能唯一绑定来源")
		}
		seen[componentKey] = true
		component := runtime.PluginComponent{Kind: c.Kind, Name: c.Name, Path: c.Path, NativeName: c.Name, Count: 1}
		if c.Kind == "skill" {
			if !resourcePath(c.Path) || path.Base(c.Path) != "SKILL.md" {
				return out, fmt.Errorf("Codex 插件 Skill 需要已核对的包内 SKILL.md 入口")
			}
			component.NativeName = name + ":" + c.Name
		}
		if c.Kind == "hook" {
			if out.Hooks == nil {
				return out, fmt.Errorf("hook 组件没有完整声明")
			}
			component.NativeName = codexHookEvents[c.Name]
			for _, expected := range out.Hooks.Expected {
				if expected.Path == c.Path && expected.Event == component.NativeName {
					component.Count = expected.Count
				}
			}
		}
		out.Components = append(out.Components, component)
	}
	if len(out.Components) == 0 || len(out.Components) > 128 {
		return out, fmt.Errorf("当前需要 1 至 128 项可核对的 Skill、MCP 或 hook 组件")
	}
	out.NativeSource, out.CachePath, err = runtime.CodexPluginLocation(scope, out)
	return out, err
}

func codexPluginSnapshots(before, after *ExecutionSnapshot) bool {
	resources := func(s *ExecutionSnapshot) bool {
		return s != nil && (len(s.PluginSelections)+len(s.SkillSelections) > 0 || slices.ContainsFunc(s.MCPSelections, func(p runtime.MCPSelection) bool { return p.Definition != nil }))
	}
	return before.Instance.Kind == "codex" && (resources(before) || resources(after))
}

func (a *Application) codexPluginChange(session Session, picks []CapabilitySelection) bool {
	if session.Scope.Instance.Kind != "codex" {
		return false
	}
	if codexPluginSnapshots(session.ExecutionSnapshot, nil) {
		return true
	}
	for _, pick := range picks {
		if asset, ok := a.asset(pick.AssetID); ok && (asset.Kind == "cli_plugin" || asset.Kind == "skill" || asset.Kind == "mcp" && asset.CodexMCP == "") {
			return true
		}
	}
	return false
}
