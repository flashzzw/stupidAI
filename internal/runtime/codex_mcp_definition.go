package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"codex-netcap/internal/mcpstdio"
)

// Read-only preparation fixes the selected daemon's explicit environment
// references before the Application persists its operation and snapshot.
type CodexMCPDefinitionReader interface {
	ReadCodexMCPDefinitions(context.Context, Scope) ([]MCPSelection, error)
}

type codexMCPEnvironmentSource interface {
	MCPEnvironment(context.Context, Instance, []string) (map[string]string, error)
}

func (a *Adapters) ReadCodexMCPDefinitions(ctx context.Context, scope Scope) ([]MCPSelection, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return nil, err
	}
	reader, ok := r.(CodexMCPDefinitionReader)
	if !ok {
		return nil, fmt.Errorf("运行时尚不能固定 Codex MCP 定义与环境引用")
	}
	return reader.ReadCodexMCPDefinitions(ctx, scope)
}

func (c *Codex) ReadCodexMCPDefinitions(ctx context.Context, scope Scope) ([]MCPSelection, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" {
		return nil, fmt.Errorf("MCP 定义版本需要普通 Codex 执行")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return nil, err
	}
	if err = verifyCodexConfigurationSources(ctx, p, scope); err != nil {
		return nil, err
	}
	selections, _, err := codexMCPDefinitionConfigs(ctx, p, scope, true)
	if err != nil {
		return nil, err
	}
	scope.MCPSelections = selections
	// Apply the same namespace, source-conflict and version checks as startup,
	// but only build the overrides here. No native query is created or resumed.
	if err = addCodexMCPOverrides(ctx, p, scope, map[string]any{}); err != nil {
		return nil, err
	}
	return selections, nil
}

var mcpEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var mcpEnvironmentReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

type codexMCPDefinition struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func readCodexMCPDefinition(selection MCPSelection) (codexMCPDefinition, []string, error) {
	var out codexMCPDefinition
	if selection.Definition == nil || !slices.Contains([]int{1, 2}, selection.Definition.Version) || !hookDefinitionHash.MatchString(selection.Definition.SHA256) ||
		!codexProviderKey.MatchString(selection.Server) || !filepath.IsAbs(selection.SourcePath) || filepath.Clean(selection.SourcePath) != selection.SourcePath {
		return out, nil, fmt.Errorf("Codex MCP 定义来源或版本无效")
	}
	if selection.Definition.Version == 1 {
		if selection.Definition.AssetVersion != "" || selection.Definition.SourceHash != "" || selection.Definition.Binding != nil {
			return out, nil, fmt.Errorf("旧 MCP 定义不能附带未核对的启动绑定")
		}
	} else if !hookDefinitionHash.MatchString(selection.Definition.SourceHash) || !(mcpstdio.Stamp{Version: 1, Digest: selection.Definition.SHA256, AssetID: selection.AssetID, AssetVersion: selection.Definition.AssetVersion, Server: selection.Server}).Valid() {
		return out, nil, fmt.Errorf("MCP 资源版本或来源摘要无效")
	}
	file, body, err := readSourceFile(selection.SourcePath, false, "Codex MCP 定义")
	if err != nil || file.ResolvedPath != selection.SourcePath {
		return out, nil, fmt.Errorf("Codex MCP 定义文件无法核对")
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != selection.Definition.SHA256 {
		return out, nil, fmt.Errorf("Codex MCP 定义内容已变化，保留原执行版本")
	}
	var root struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(body, &root) != nil || len(root.Servers) == 0 || len(root.Servers) > 16 {
		return out, nil, fmt.Errorf("MCP 定义需要 1–16 个服务")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(root.Servers[selection.Server], &fields) != nil || fields == nil || json.Unmarshal(root.Servers[selection.Server], &out) != nil {
		return out, nil, fmt.Errorf("所选 MCP 服务定义无效或不存在")
	}
	if raw, exists := fields["type"]; exists {
		var transport string
		if json.Unmarshal(raw, &transport) != nil || transport != "stdio" {
			return out, nil, fmt.Errorf("Codex 共享 MCP 定义目前需要 stdio；远程传输尚未核对")
		}
	}
	for key := range fields {
		if !slices.Contains([]string{"type", "command", "args", "env"}, key) {
			return out, nil, fmt.Errorf("Codex MCP 定义包含尚未支持的字段")
		}
		if (key == "args" || key == "env") && strings.TrimSpace(string(fields[key])) == "null" {
			return out, nil, fmt.Errorf("MCP args 与 env 不能为 null")
		}
	}
	if strings.TrimSpace(out.Command) == "" || len(out.Args) > 256 || len(out.Env) > 128 {
		return out, nil, fmt.Errorf("MCP command、args 或 env 无效")
	}
	values := append([]string{out.Command}, out.Args...)
	for key, value := range out.Env {
		if !mcpEnvironmentName.MatchString(key) {
			return out, nil, fmt.Errorf("MCP 环境变量名称无效")
		}
		values = append(values, value)
	}
	names := map[string]bool{}
	for _, value := range values {
		if len(value) > 32768 || strings.ContainsRune(value, '\x00') {
			return out, nil, fmt.Errorf("MCP 字符串过长或包含 NUL")
		}
		for _, match := range mcpEnvironmentReference.FindAllStringSubmatch(value, -1) {
			if strings.Contains(match[2], "${") {
				return out, nil, fmt.Errorf("MCP 环境引用不支持嵌套默认值")
			}
			names[match[1]] = true
		}
		if strings.Contains(mcpEnvironmentReference.ReplaceAllString(value, ""), "${") {
			return out, nil, fmt.Errorf("MCP 环境引用格式无效")
		}
	}
	if len(names) > 64 {
		return out, nil, fmt.Errorf("单个 MCP 定义不能引用超过 64 个环境变量")
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return out, keys, nil
}

// Validate returned metadata against the caller's source without needing any
// credential values. Missing or duplicate environment stamps remain invalid.
func ValidateCodexMCPDefinitions(scope Scope) error {
	for _, selection := range scope.MCPSelections {
		if selection.Definition == nil {
			continue
		}
		if scope.Instance.Kind != "codex" || scope.Purpose != "" {
			return fmt.Errorf("MCP 定义版本不属于普通 Codex 执行")
		}
		definition, names, err := readCodexMCPDefinition(selection)
		if err != nil {
			return err
		}
		if selection.Definition.Version == 2 {
			if err := validateCodexMCPLaunch(scope, selection, definition); err != nil {
				return err
			}
		}
		if selection.Mode == "disabled" && selection.Definition.Environment != nil && len(selection.Definition.Environment) == 0 {
			continue
		}
		if selection.Definition.Environment == nil || len(names) != len(selection.Definition.Environment) {
			return fmt.Errorf("MCP 环境引用版本缺失或多余")
		}
		for i, binding := range selection.Definition.Environment {
			if binding.Name != names[i] || !hookDefinitionHash.MatchString(binding.SHA256) {
				return fmt.Errorf("MCP 环境引用版本无效")
			}
		}
	}
	return nil
}

// The native thread ID is intentionally absent: creation captures this before
// that ID exists, and resumption must retain the same launch identity. Other
// selected resources are independent and do not rebind an unchanged MCP.
func codexMCPLaunchContext(scope Scope, selection MCPSelection) string {
	definition := selection.Definition
	body, _ := json.Marshal([]any{scope.SessionID, scope.StateNamespace, scope.Instance.ID, scope.Instance.Profile, scope.Instance.Socket,
		selection.SourcePath, definition.SourceHash, definition.SHA256, definition.Environment})
	sum := sha256.Sum256(append([]byte("netcap-codex-mcp-scope-v1\x00"), body...))
	return hex.EncodeToString(sum[:])
}

func validateCodexMCPLaunch(scope Scope, selection MCPSelection, definition codexMCPDefinition) error {
	if scope.SessionID == "" || scope.StateNamespace == "" {
		return fmt.Errorf("MCP 启动版本缺少执行与状态归属")
	}
	binding := selection.Definition.Binding
	if binding == nil && selection.Mode == "disabled" {
		return nil
	}
	if binding == nil || !binding.Valid() || binding.AssetID != selection.AssetID || binding.AssetVersion != selection.Definition.AssetVersion || binding.Server != selection.Server ||
		binding.Cwd != scope.Cwd || binding.Context != codexMCPLaunchContext(scope, selection) {
		return fmt.Errorf("MCP 启动版本与所选资源或执行归属不一致")
	}
	names := make([]string, 0, len(definition.Env))
	for name := range definition.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	if !slices.Equal(names, binding.Environment) {
		return fmt.Errorf("MCP 启动环境名称与定义不一致")
	}
	return nil
}

func mcpEnvironmentBindings(names []string, values map[string]string) []MCPEnvironmentBinding {
	bindings := make([]MCPEnvironmentBinding, 0, len(names))
	for _, name := range names {
		value, present := values[name]
		sum := sha256.Sum256([]byte(fmt.Sprintf("netcap-codex-mcp-environment-v1\x00%s\x00%t\x00%s", name, present, value)))
		bindings = append(bindings, MCPEnvironmentBinding{Name: name, Present: present, SHA256: hex.EncodeToString(sum[:])})
	}
	return bindings
}

func codexMCPDefinitionConfigs(ctx context.Context, p codexConnection, scope Scope, capture bool) ([]MCPSelection, map[string]map[string]any, error) {
	if !capture {
		if err := ValidateCodexMCPDefinitions(scope); err != nil {
			return nil, nil, err
		}
	}
	selections := append([]MCPSelection{}, scope.MCPSelections...)
	configs := map[string]map[string]any{}
	for index, selection := range selections {
		if selection.Definition == nil {
			continue
		}
		definition, names, err := readCodexMCPDefinition(selection)
		if err != nil {
			return nil, nil, err
		}
		if selection.Mode == "disabled" {
			binding := *selection.Definition
			if capture {
				binding.Environment = []MCPEnvironmentBinding{}
				binding.Binding = nil
			}
			args := make([]string, 0, len(definition.Args))
			for _, value := range definition.Args {
				args = append(args, codexMCPResourcePath(selection.SourcePath, value))
			}
			// The native disabled entry still needs a transport shape. Keep its
			// literal command/args without resolving or handing off credentials.
			configs[selection.Server] = map[string]any{"enabled": false, "command": codexMCPResourcePath(selection.SourcePath, definition.Command),
				"args": args, "env": map[string]string{}, "cwd": scope.Cwd, "environment_id": "local"}
			selections[index].Definition = &binding
			continue
		}
		for _, name := range names {
			if !slices.Contains(scope.Instance.EnvAllow, name) {
				return nil, nil, fmt.Errorf("MCP 环境引用 %s 未列入该执行配置的 env_allow", name)
			}
		}
		values := map[string]string{}
		if len(names) > 0 {
			reader, ok := p.(codexMCPEnvironmentSource)
			if !ok {
				return nil, nil, fmt.Errorf("当前连接无法核对 MCP 所需的后台环境引用")
			}
			values, err = reader.MCPEnvironment(ctx, scope.Instance, names)
			if err != nil {
				return nil, nil, err
			}
		}
		binding := *selection.Definition
		binding.Environment = mcpEnvironmentBindings(names, values)
		config, err := expandCodexMCPDefinition(scope.Cwd, selection.SourcePath, definition, values)
		if err != nil {
			return nil, nil, err
		}
		config["enabled"] = selection.Mode == "enabled"
		selections[index].Definition = &binding
		if binding.Version == 2 {
			if scope.SessionID == "" || scope.StateNamespace == "" {
				return nil, nil, fmt.Errorf("MCP 启动版本缺少执行与状态归属")
			}
			launch, err := mcpstdio.Bind(mcpstdio.Stamp{AssetID: selection.AssetID, AssetVersion: binding.AssetVersion, Server: selection.Server},
				codexMCPLaunchContext(scope, selections[index]), scope.Cwd, config["command"].(string), config["args"].([]string), config["env"].(map[string]string))
			if err != nil {
				return nil, nil, fmt.Errorf("MCP 实际启动参数无法绑定")
			}
			binding.Binding = &launch
			command, err := os.Executable()
			if err != nil {
				return nil, nil, fmt.Errorf("无法定位 MCP stdio 交付程序")
			}
			body, err := json.Marshal(launch)
			if err != nil || len(body) > 65536 {
				return nil, nil, fmt.Errorf("MCP 启动绑定超出交付范围")
			}
			config["args"] = append([]string{codexMCPStdioCommand, string(body), "--", config["command"].(string)}, config["args"].([]string)...)
			config["command"] = command
		}
		if !capture && !reflect.DeepEqual(binding, *selection.Definition) {
			return nil, nil, fmt.Errorf("Codex MCP 后台环境或实际启动版本已变化；不能静默采用新的凭据或参数")
		}
		configs[selection.Server] = config
	}
	return selections, configs, nil
}

func codexMCPResourcePath(source, value string) string {
	if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "scripts/") {
		return filepath.Join(filepath.Dir(source), value)
	}
	return value
}

func expandCodexMCPDefinition(cwd, path string, definition codexMCPDefinition, values map[string]string) (map[string]any, error) {
	var missing string
	expand := func(value string) string {
		return mcpEnvironmentReference.ReplaceAllStringFunc(value, func(reference string) string {
			match := mcpEnvironmentReference.FindStringSubmatch(reference)
			if actual, ok := values[match[1]]; ok {
				return actual
			}
			if strings.Contains(reference, ":-") {
				return match[2]
			}
			missing = match[1]
			return ""
		})
	}
	command := codexMCPResourcePath(path, expand(definition.Command))
	args := make([]string, 0, len(definition.Args))
	for _, value := range definition.Args {
		args = append(args, codexMCPResourcePath(path, expand(value)))
	}
	env := map[string]string{}
	for name, value := range definition.Env {
		env[name] = expand(value)
	}
	if missing != "" {
		return nil, fmt.Errorf("指定 Codex 后台缺少 MCP 环境引用 %s", missing)
	}
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("MCP command 展开后为空")
	}
	all := append([]string{command}, args...)
	for _, value := range env {
		all = append(all, value)
	}
	for _, value := range all {
		if len(value) > 32768 || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("MCP 参数展开后过长或包含 NUL")
		}
	}
	return map[string]any{"command": command, "args": args, "env": env, "cwd": cwd, "environment_id": "local"}, nil
}
