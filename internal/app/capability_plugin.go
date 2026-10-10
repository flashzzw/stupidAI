package app

import (
	"path"
	"strings"
)

type CapabilityPluginSource struct {
	Backend  string `json:"backend"`
	Layout   string `json:"layout"`
	Identity string `json:"identity"`
	Settings string `json:"settings"`
}

// Portable identity and fixed component paths remain canonical even when an
// older overlay supplies OpenAI-specific settings. Inline settings replace the
// overlay as a whole; an explicitly empty object is still a replacement.
func (i *assetInspector) pluginDefinition(backend string) (string, string, map[string]any, bool, bool) {
	definition, issue := resolvePluginManifest(i.tree, backend)
	portable := definition.Source.Layout == "portable"
	if issue != nil {
		i.issue(issue.Severity, issue.Code, issue.Path, issue.Detail)
		return "", "", nil, portable, false
	}
	manifest, settingsSource, settings := definition.Source.Identity, definition.Source.Settings, definition.Settings
	name, _ := definition.Identity["name"].(string)
	if strings.TrimSpace(name) == "" {
		i.issue("error", "invalid_plugin", manifest, "插件 manifest 缺少 name 字符串")
	}
	i.component("plugin", name, manifest, backend)
	if portable {
		if definition.Identity["$schema"] != agentPluginSchema {
			i.issue("warning", "native_schema_unverified", manifest, "缺少受支持的 Agent Plugins 1.0.0 plugin schema；固定 Codex 可能忽略根身份并改读旧格式，生产选配会拒绝")
		}
		if settingsSource == "plugin.json#extensions.com.openai" && i.tree.exists(".codex-plugin/plugin.json") {
			i.issue("warning", "replaced_overlay", ".codex-plugin/plugin.json", "根 manifest 的 extensions.com.openai 整体替代此 overlay；其中组件不加入当前声明清单")
		}
		if i.tree.exists(".mcp.json") {
			i.issue("warning", "replaced_component", ".mcp.json", "通用插件从根 mcp.json 发现 MCP；旧 .mcp.json 不加入当前声明清单")
		}
		i.issue("warning", "native_version_unverified", manifest, "已按官方通用包结构解析；当前 CLI 的实际发现、加载和权限仍需单独核对")
	}
	i.result.PluginSources = append(i.result.PluginSources, definition.Source)
	i.pluginPresentation(settingsSource, settings, backend)
	return manifest, settingsSource, settings, portable, true
}
func (i *assetInspector) pluginRelative(source, value string, allowRoot bool) (string, bool) {
	clean := path.Clean(value)
	if (!strings.HasPrefix(value, "./") && !(allowRoot && value == ".")) || (clean == "." && !allowRoot) || (clean != "." && !resourcePath(clean)) {
		i.issue("error", "invalid_component_path", source, "组件路径须以 ./ 开头并位于插件根目录内")
		return "", false
	}
	return clean, true
}
func (i *assetInspector) pluginPresentation(source string, settings map[string]any, backend string) {
	if presentation, ok := settings["interface"].(map[string]any); ok {
		for _, field := range []string{"composerIcon", "logo", "screenshots"} {
			if value, exists := presentation[field]; exists {
				paths, ok := stringList(value)
				if !ok {
					i.issue("error", "invalid_component_path", source, field+" 必须为路径字符串或数组")
					continue
				}
				for _, value := range paths {
					if p, ok := i.pluginRelative(source, value, false); ok {
						i.dependency(".", p)
					}
				}
			}
		}
	}
	if raw, exists := settings["apps"]; exists {
		i.pluginConfigValues(source, "apps", raw, backend, false)
	}
}
func (i *assetInspector) pluginConfigValues(source, kind string, value any, backend string, portableMCP bool) {
	switch v := value.(type) {
	case []any:
		for _, entry := range v {
			switch entry.(type) {
			case string, map[string]any:
				i.pluginConfigValues(source, kind, entry, backend, portableMCP)
			default:
				i.issue("error", "invalid_component_path", source, kind+" 数组成员必须为路径或配置对象")
			}
		}
	case string:
		p, ok := i.pluginRelative(source, v, false)
		if !ok {
			return
		}
		key := backend + "/" + kind + "/" + p
		if i.visited[key] {
			return
		}
		i.visited[key] = true
		if object, ok := i.jsonFile(p); ok {
			i.pluginConfigObject(p, kind, object, backend, portableMCP, false)
		}
	case map[string]any:
		i.pluginConfigObject(source, kind, v, backend, portableMCP, true)
	default:
		i.issue("error", "invalid_component_path", source, kind+" 必须是相对路径、配置对象或数组")
	}
}
func (i *assetInspector) pluginConfigObject(source, kind string, object map[string]any, backend string, portableMCP, inline bool) {
	switch kind {
	case "mcpServers":
		if inline {
			object = map[string]any{"mcpServers": object}
		}
		if portableMCP {
			if object["$schema"] != agentMCPSchema {
				i.issue("warning", "native_schema_unverified", source, "缺少受支持的 Agent Plugins 1.0.0 MCP schema；固定 Codex 不会加载此通用 MCP 清单，生产选配会拒绝")
			}
			if servers, ok := object["mcpServers"].(map[string]any); ok {
				for _, raw := range servers {
					server, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					transport, _ := server["type"].(string)
					command, _ := server["command"].(string)
					endpoint, _ := server["url"].(string)
					if transport == "stdio" {
						bare := command != "" && command != "." && command != ".." && !strings.ContainsAny(command, "/\\:\x00")
						contained := strings.HasPrefix(command, "./") && resourcePath(path.Clean(command))
						if !bare && !contained {
							i.issue("error", "invalid_portable_command", source, "通用 stdio MCP command 需要裸命令名或包内 ./ 路径；绝对路径和越界路径不被原生加载")
						}
					}
					if (transport == "stdio" && command == "") || ((transport == "streamable-http" || transport == "sse") && endpoint == "") || (transport != "stdio" && transport != "streamable-http" && transport != "sse") {
						i.issue("error", "invalid_mcp_transport", source, "通用 MCP 定义需要明确且与 command/url 一致的 type（stdio、streamable-http 或 sse）")
					}
				}
			}
		}
		i.mcp(source, object, backend)
	case "hooks":
		if _, exists := object["hooks"]; !exists && inline {
			object = map[string]any{"hooks": object}
		}
		i.hooks(source, object, backend)
	case "lspServers":
		for name, raw := range object {
			i.component("lsp", name, source, backend)
			if server, ok := raw.(map[string]any); ok {
				if command, ok := server["command"].(string); ok {
					i.commandPaths(source, command)
				}
			}
		}
		i.scanValues(source, object)
	case "apps":
		mappings, ok := object["apps"].(map[string]any)
		if !ok {
			i.issue("error", "invalid_plugin_apps", source, "注册 MCP 映射需要 apps 对象")
			return
		}
		for name := range mappings {
			i.component("registered_mcp", name, source, backend)
		}
		i.scanValues(source, object)
		if len(mappings) > 0 {
			i.issue("warning", "runtime_binding", source, "此处只保存注册 MCP 的标识映射；目标环境仍需独立连接及认证，包内不包含账号状态")
		}
	}
}
