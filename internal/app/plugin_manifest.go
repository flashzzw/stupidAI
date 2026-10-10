package app

const agentPluginSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
const agentMCPSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

// Identity and settings are resolved once by the same rules for library
// inspection and execution. An empty inline OpenAI object still replaces the
// legacy overlay; it is not a request to merge or fall back.
type pluginManifest struct {
	Source   CapabilityPluginSource
	Identity map[string]any
	Settings map[string]any
}

func resolvePluginManifest(tree capabilityResources, backend string) (pluginManifest, *CapabilityIssue) {
	files := tree.files()
	read := func(p string) (map[string]any, *CapabilityIssue) {
		body, exists := files[p]
		if !exists {
			return nil, &CapabilityIssue{"error", "missing_component", p, "声明的组件文件不存在"}
		}
		object, err := jsonObject(body)
		if err != nil {
			return nil, &CapabilityIssue{"error", "invalid_json", p, "组件 JSON 无效"}
		}
		return object, nil
	}
	legacy := ".claude-plugin/plugin.json"
	if backend == "codex" {
		legacy = ".codex-plugin/plugin.json"
	}
	out := pluginManifest{Source: CapabilityPluginSource{Backend: backend, Layout: "legacy", Identity: legacy, Settings: legacy}}
	if backend == "codex" && tree.exists("plugin.json") {
		out.Source.Layout, out.Source.Identity, out.Source.Settings = "portable", "plugin.json", "plugin.json（默认）"
	}
	var issue *CapabilityIssue
	out.Identity, issue = read(out.Source.Identity)
	if issue != nil {
		return out, issue
	}
	out.Settings = out.Identity
	if out.Source.Layout == "legacy" {
		return out, nil
	}
	out.Settings = map[string]any{}
	if raw, exists := out.Identity["extensions"]; exists {
		extensions, valid := raw.(map[string]any)
		if !valid {
			return out, &CapabilityIssue{"error", "invalid_plugin", out.Source.Identity, "extensions 必须是对象"}
		}
		if raw, exists := extensions["com.openai"]; exists {
			out.Settings, valid = raw.(map[string]any)
			if !valid {
				return out, &CapabilityIssue{"error", "invalid_plugin", out.Source.Identity, "extensions.com.openai 必须是对象"}
			}
			out.Source.Settings = "plugin.json#extensions.com.openai"
		}
	}
	if out.Source.Settings == "plugin.json（默认）" && tree.exists(legacy) {
		out.Source.Settings = legacy
		out.Settings, issue = read(legacy)
	}
	return out, issue
}
