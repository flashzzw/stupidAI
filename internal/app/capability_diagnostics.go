package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

type CapabilityIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Detail   string `json:"detail"`
}
type CapabilityComponent struct {
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	Backend           string `json:"backend,omitempty"`
	IndependentSwitch bool   `json:"independent_switch"`
}
type CapabilityDiagnosis struct {
	Format              string                   `json:"format"`
	Status              string                   `json:"status"`
	NativeStatus        string                   `json:"native_status"`
	Portable            bool                     `json:"portable"`
	Components          []CapabilityComponent    `json:"components"`
	Issues              []CapabilityIssue        `json:"issues"`
	RequiredEnvironment []string                 `json:"required_environment"`
	Boundary            string                   `json:"boundary"`
	PluginSources       []CapabilityPluginSource `json:"plugin_sources,omitempty"`
	Resources           []CapabilityResourceInfo `json:"resources,omitempty"`
}
type assetInspector struct {
	tree        capabilityResources
	files       map[string][]byte
	result      CapabilityDiagnosis
	seenMCP     map[string]string
	env         map[string]bool
	visited     map[string]bool
	scanBytes   int64
	scanEntries int
}

var markdownLink = regexp.MustCompile(`\]\(([^\s)]+)`)
var bundledMention = regexp.MustCompile("(?:`|[\"'\\s])((?:scripts|references|assets)/[A-Za-z0-9_./-]+)")
var pluginPath = regexp.MustCompile(`\$\{(?:CLAUDE_PLUGIN_ROOT|PLUGIN_ROOT)\}/([A-Za-z0-9_./-]+)`)
var relativeCommandPath = regexp.MustCompile(`(?:^|[\s"'])((?:\.{1,2}/|/)[A-Za-z0-9_./-]+)`)
var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
var credentialKey = regexp.MustCompile(`(?i)(api.?key|access.?token|refresh.?token|auth.?token|password|passwd|secret|credential|authorization|cookie)`)
var credentialAssignment = regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|password|passwd|secret|token|authorization)["']?\s*[:=]\s*["']([^"'\r\n]+)["']`)
var knownSecret = regexp.MustCompile(`(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[A-Z0-9]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

func credentialReference(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "env:") {
		return validEnvironmentName(strings.TrimPrefix(value, "env:"))
	}
	clean := envPlaceholder.ReplaceAllString(value, "")
	return clean != value && (clean == "" || clean == "Bearer " || clean == "Basic ")
}
func validEnvironmentName(s string) bool {
	return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(s)
}
func (i *assetInspector) issue(severity, code, p, detail string) {
	issue := CapabilityIssue{severity, code, p, detail}
	for _, old := range i.result.Issues {
		if old == issue {
			return
		}
	}
	i.result.Issues = append(i.result.Issues, issue)
	if severity == "error" {
		i.result.Status = "invalid"
		i.result.Portable = false
	}
	if code == "external_dependency" || code == "sensitive_content" || code == "native_state" {
		i.result.Portable = false
	}
}
func (i *assetInspector) component(kind, name, p, backend string) {
	c := CapabilityComponent{Kind: kind, Name: name, Path: p, Backend: backend}
	for _, old := range i.result.Components {
		if old == c {
			return
		}
	}
	i.result.Components = append(i.result.Components, c)
}
func (i *assetInspector) dependency(base, reference string) {
	reference = strings.Trim(reference, "`\"'")
	if reference == "" || strings.HasPrefix(reference, "#") {
		return
	}
	u, err := url.Parse(reference)
	if err == nil && (u.Scheme != "" || u.Host != "") {
		return
	}
	reference = strings.SplitN(reference, "#", 2)[0]
	reference, err = url.PathUnescape(reference)
	if err != nil {
		return
	}
	p := path.Clean(path.Join(base, reference))
	if !resourcePath(p) || strings.HasPrefix(reference, "/") {
		i.issue("warning", "external_dependency", reference, "依赖位于资源包外；请将所需文件纳入资源根或明确运行环境绑定")
		return
	}
	if !i.tree.exists(p) {
		i.issue("error", "missing_dependency", p, "声明或引用的资源文件不存在")
	}
}
func (i *assetInspector) document(p string, backend string) {
	body, ok := i.files[p]
	if !ok {
		i.issue("error", "missing_entry", p, "缺少 SKILL.md 入口")
		return
	}
	metadata, err := skillDocumentMetadata(body)
	if err != nil {
		i.issue("error", "invalid_frontmatter", p, err.Error())
		return
	}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	name, _ := metadata["name"].(string)
	description, _ := metadata["description"].(string)
	if backend == "codex" && (strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "") {
		i.issue("error", "missing_skill_metadata", p, "Codex Skill 需要 name 与 description 字符串")
	}
	if name == "" {
		name = path.Base(path.Dir(p))
		if name == "." {
			name = i.tree.Name
		}
	}
	i.component("skill", name, p, backend)
	for _, match := range markdownLink.FindAllStringSubmatch(normalized, -1) {
		i.dependency(path.Dir(p), match[1])
	}
	for _, match := range bundledMention.FindAllStringSubmatch(normalized, -1) {
		i.dependency(path.Dir(p), match[1])
	}
}

func skillDocumentMetadata(body []byte) (map[string]any, error) {
	metadata := map[string]any{}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	if strings.HasPrefix(normalized, "---\n") {
		end := strings.Index(normalized[4:], "\n---")
		if end < 0 {
			return nil, fmt.Errorf("YAML 元数据缺少结束标记")
		}
		decoder := yaml.NewDecoder(strings.NewReader(normalized[4 : 4+end]))
		if err := decoder.Decode(&metadata); err != nil || metadata == nil {
			return nil, fmt.Errorf("YAML 元数据无效，检查字段类型和重复键")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, fmt.Errorf("入口必须只有一份 YAML 元数据")
		}
	}
	return metadata, nil
}
func jsonObject(data []byte) (map[string]any, error) {
	if err := uniqueJSONKeys(data); err != nil {
		return nil, err
	}
	var out map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&out); err != nil || out == nil {
		return nil, fmt.Errorf("需要 JSON 对象")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("只能包含一份 JSON 对象")
	}
	return out, nil
}
func (i *assetInspector) jsonFile(p string) (map[string]any, bool) {
	raw, ok := i.files[p]
	if !ok {
		i.issue("error", "missing_component", p, "声明的组件文件不存在")
		return nil, false
	}
	value, err := jsonObject(raw)
	if err != nil {
		i.issue("error", "invalid_json", p, "组件 JSON 无效")
		return nil, false
	}
	return value, true
}
func (i *assetInspector) scanValues(p string, value any) {
	i.scanObjectValues(p, value, true)
}
func (i *assetInspector) scanObjectValues(p string, value any, collectEnvironment bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if text, ok := child.(string); ok && credentialKey.MatchString(key) && !credentialReference(text) {
				i.issue("error", "sensitive_content", p, "发现凭据字段的字面值；改用环境变量引用后再打包")
			}
			i.scanObjectValues(p, child, collectEnvironment)
		}
	case []any:
		for _, child := range v {
			i.scanObjectValues(p, child, collectEnvironment)
		}
	case string:
		if !collectEnvironment {
			return
		}
		if strings.HasPrefix(v, "env:") && validEnvironmentName(strings.TrimPrefix(v, "env:")) {
			i.env[strings.TrimPrefix(v, "env:")] = true
		}
		for _, match := range envPlaceholder.FindAllStringSubmatch(v, -1) {
			if match[1] != "CLAUDE_PLUGIN_ROOT" && match[1] != "PLUGIN_ROOT" && match[1] != "CLAUDE_PLUGIN_DATA" && match[1] != "PLUGIN_DATA" {
				i.env[match[1]] = true
			}
		}
	}
}
func (i *assetInspector) commandPaths(p, text string) {
	for _, match := range pluginPath.FindAllStringSubmatch(text, -1) {
		i.dependency(".", match[1])
	}
	for _, match := range relativeCommandPath.FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(match[1], "/usr/bin/") || strings.HasPrefix(match[1], "/bin/") {
			continue
		}
		i.dependency(".", match[1])
	}
	if strings.Contains(text, "${CLAUDE_PLUGIN_DATA}") || strings.Contains(text, "${PLUGIN_DATA}") {
		i.issue("warning", "runtime_binding", p, "由目标 CLI 提供插件持久数据目录；只保留变量引用，运行环境数据不会随资源包导出")
	}
}
func (i *assetInspector) mcp(p string, object map[string]any, backend string) {
	servers, ok := object["mcpServers"].(map[string]any)
	if !ok {
		servers, ok = object["mcp_servers"].(map[string]any)
	}
	if !ok {
		i.issue("error", "invalid_mcp", p, "需要 mcpServers 对象，完整 CLI 配置应保留为外部来源")
		return
	}
	for name, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			i.issue("error", "invalid_mcp", p, "MCP 服务定义必须是对象")
			continue
		}
		if prior, exists := i.seenMCP[backend+"/"+name]; exists && prior != p {
			i.issue("warning", "duplicate_mcp", p, "MCP 名称 "+name+" 在 "+prior+" 也有定义，原生合并顺序尚待核对")
		}
		i.seenMCP[backend+"/"+name] = p
		command, _ := server["command"].(string)
		endpoint, _ := server["url"].(string)
		if (command == "") == (endpoint == "") {
			i.issue("error", "invalid_mcp", p, "MCP 服务必须明确声明 command 或 url 其中之一")
		}
		if endpoint != "" {
			u, err := url.Parse(endpoint)
			if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
				i.issue("error", "invalid_mcp_url", p, "MCP URL 无效")
			} else if u.User != nil || u.RawQuery != "" {
				i.issue("error", "sensitive_content", p, "MCP URL 包含账号信息或查询参数，不能直接作为无凭据资源导出")
			}
		}
		if command != "" {
			i.commandPaths(p, command)
			if strings.HasPrefix(command, "/") && !strings.HasPrefix(command, "/usr/bin/") && !strings.HasPrefix(command, "/bin/") {
				i.issue("warning", "external_dependency", p, "MCP 命令绑定包外绝对路径，无法随资源移动")
			}
		}
		if raw, exists := server["headersHelper"]; exists && backend == "claude" {
			if helper, ok := raw.(string); !ok || strings.TrimSpace(helper) == "" || strings.ContainsRune(helper, '\x00') {
				i.issue("error", "invalid_mcp_headers_helper", p, "动态认证需要非空且不含 NUL 的 headersHelper 命令")
			} else {
				// This shell command is relative to the execution project, not the
				// resource tree; do not claim its dependencies were packaged.
				i.issue("warning", "runtime_binding", p, "headersHelper 在原生连接时执行，使用所选项目 cwd 与配置环境；相对路径不会随资源包自动重定位")
			}
		}
		if args, ok := server["args"].([]any); ok {
			for _, arg := range args {
				if text, ok := arg.(string); ok {
					i.commandPaths(p, text)
					if strings.HasPrefix(text, "scripts/") {
						i.dependency(".", text)
					}
				}
			}
		}
		i.component("mcp", name, p, backend)
	}
	i.scanValues(p, object)
}
func (i *assetInspector) hooks(p string, object map[string]any, backend string) {
	hooks, ok := object["hooks"].(map[string]any)
	if !ok {
		i.issue("error", "invalid_hooks", p, "hooks 需要事件映射对象")
		return
	}
	for event, value := range hooks {
		i.component("hook", event, p, backend)
		encoded, _ := json.Marshal(value)
		i.commandPaths(p, string(encoded))
	}
	i.scanValues(p, object)
}
func stringList(value any) ([]string, bool) {
	switch v := value.(type) {
	case string:
		return []string{v}, true
	case []any:
		out := []string{}
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
func (i *assetInspector) plugin(backend string) {
	manifest, settingsSource, object, portable, ok := i.pluginDefinition(backend)
	if !ok {
		return
	}
	for _, kind := range []string{"skills", "commands", "agents"} {
		if portable && kind != "skills" {
			continue
		}
		defaults := []string{kind}
		if raw, exists := object[kind]; exists && !portable {
			custom, ok := stringList(raw)
			if !ok {
				i.issue("error", "invalid_component_path", manifest, kind+" 必须是相对路径或路径数组")
				continue
			}
			if kind == "skills" && backend == "claude" {
				defaults = append(defaults, custom...)
			} else {
				defaults = custom
			}
		} else if kind == "skills" && !portable && !i.tree.exists("skills") {
			if _, exists := i.files["SKILL.md"]; exists {
				defaults = append(defaults, ".")
			}
		}
		for _, dir := range defaults {
			dir = path.Clean(dir)
			if dir != "." && !resourcePath(dir) {
				i.issue("error", "invalid_component_path", manifest, "组件路径越过插件根目录")
				continue
			}
			if !i.tree.exists(dir) {
				if _, declared := object[kind]; declared && !portable {
					i.issue("error", "missing_component", dir, "声明的插件组件路径不存在")
				}
				continue
			}
			for p := range i.files {
				within := p == dir || dir == "." || strings.HasPrefix(p, dir+"/")
				if !within {
					continue
				}
				if kind == "skills" && path.Base(p) == "SKILL.md" {
					i.document(p, backend)
				}
				if (kind == "commands" || kind == "agents") && strings.HasSuffix(p, ".md") {
					i.component(strings.TrimSuffix(kind, "s"), strings.TrimSuffix(path.Base(p), ".md"), p, backend)
				}
			}
		}
	}
	for _, kind := range []string{"mcpServers", "hooks", "lspServers"} {
		if portable && kind == "lspServers" {
			continue
		}
		defaultPath := map[string]string{"mcpServers": ".mcp.json", "hooks": "hooks/hooks.json", "lspServers": ".lsp.json"}[kind]
		raw, explicit := object[kind]
		if portable && kind == "mcpServers" {
			defaultPath = "mcp.json"
			explicit = false
		}
		replaceDefault := backend == "codex" && kind == "hooks" && explicit
		if !replaceDefault && i.tree.exists(defaultPath) {
			i.pluginConfigValues(settingsSource, kind, "./"+defaultPath, backend, portable && kind == "mcpServers")
		}
		if explicit {
			i.pluginConfigValues(settingsSource, kind, raw, backend, false)
		}
	}
	i.issue("warning", "bundled_components", manifest, "组件属于插件整体；此处没有独立启停授权，原生加载与有效权限仍需验证")
}
func diagnoseResources(asset CapabilityAsset, tree capabilityResources) CapabilityDiagnosis {
	i := assetInspector{tree: tree, files: tree.files(), result: CapabilityDiagnosis{Format: asset.Kind, Status: "static_checked", NativeStatus: "unverified", Portable: true, Components: []CapabilityComponent{}, Issues: []CapabilityIssue{}, RequiredEnvironment: []string{}, Boundary: "静态检查入口、声明组件和显式相对路径；不运行脚本、不验证所有动态依赖，不代表已原生加载或取得权限"}, seenMCP: map[string]string{}, env: map[string]bool{}, visited: map[string]bool{}}
	if capabilityStateName(tree.Name) {
		i.issue("warning", "native_state", tree.Name, "资源根名称属于原生配置、账号或会话状态")
	}
	for _, entry := range tree.Entries {
		if entry.Kind == "file" {
			i.scanResource(entry.Path, entry.Data, 0)
		} else if capabilityStateName(path.Base(entry.Path)) {
			i.issue("warning", "native_state", entry.Path, "来源包含原生配置、账号或会话状态，不能纳入能力资源包")
		}
	}
	switch asset.Kind {
	case "skill":
		entry := "SKILL.md"
		if tree.Kind == "file" {
			entry = tree.Name
			if path.Base(entry) != "SKILL.md" {
				i.issue("error", "missing_entry", entry, "Skill 文件入口需要命名为 SKILL.md")
			}
		}
		for _, backend := range asset.Backends {
			i.document(entry, backend)
		}
	case "mcp":
		if asset.CodexMCP != "" {
			i.result.Format = "codex_profile_reference"
			i.component("mcp", asset.CodexMCP, tree.Name, "codex")
			i.issue("warning", "external_dependency", tree.Name, "该映射依赖原 Codex profile；资源包不复制原生配置")
		} else {
			entry := tree.Name
			if tree.Kind == "directory" {
				entry = ".mcp.json"
				if !tree.exists(entry) {
					entry = "mcp.json"
				}
			}
			if object, ok := i.jsonFile(entry); ok {
				for _, backend := range asset.Backends {
					i.mcp(entry, object, backend)
				}
			}
		}
	case "cli_plugin":
		for _, backend := range asset.Backends {
			i.plugin(backend)
		}
	}
	for name := range i.env {
		i.result.RequiredEnvironment = append(i.result.RequiredEnvironment, name)
	}
	sort.Strings(i.result.RequiredEnvironment)
	sort.Slice(i.result.Resources, func(a, b int) bool { return i.result.Resources[a].Path < i.result.Resources[b].Path })
	sort.Slice(i.result.PluginSources, func(a, b int) bool { return i.result.PluginSources[a].Backend < i.result.PluginSources[b].Backend })
	sort.Slice(i.result.Components, func(a, b int) bool {
		x, y := i.result.Components[a], i.result.Components[b]
		return x.Backend+x.Kind+x.Path+x.Name < y.Backend+y.Kind+y.Path+y.Name
	})
	sort.Slice(i.result.Issues, func(a, b int) bool {
		x, y := i.result.Issues[a], i.result.Issues[b]
		return x.Path+x.Code+x.Detail+x.Severity < y.Path+y.Code+y.Detail+y.Severity
	})
	return i.result
}
