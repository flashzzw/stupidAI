package app

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"codex-netcap/internal/runtime"
)

// Bind names and source paths, never command payloads or resolved credentials.
// The bridge reads this definition only when starting the owned native query.
func claudeMCPSelections(ref CapabilityReference, mode string) ([]runtime.MCPSelection, error) {
	if ref.Asset.Kind != "mcp" || ref.Asset.CodexMCP != "" || (mode != "enabled" && mode != "disabled") {
		return nil, fmt.Errorf("Claude 原生选择需要 MCP 定义与 enabled/disabled 模式")
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return nil, err
	}
	if stamp != ref.Source {
		return nil, fmt.Errorf("MCP 来源在登记启动快照期间发生变化")
	}
	entry := tree.Name
	source := stamp.Resolved
	if tree.Kind == "directory" {
		entry = ".mcp.json"
		if !tree.exists(entry) {
			entry = "mcp.json"
		}
		source = filepath.Join(source, entry)
	}
	definition, err := jsonObject(tree.files()[entry])
	if err != nil {
		return nil, fmt.Errorf("Claude MCP 定义需要有效 JSON 对象")
	}
	servers, ok := definition["mcpServers"].(map[string]any)
	if !ok || len(servers) == 0 || len(servers) > 16 {
		return nil, fmt.Errorf("Claude MCP 定义需要 1–16 个 mcpServers")
	}
	out := []runtime.MCPSelection{}
	for name, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok || !validID(name) {
			return nil, fmt.Errorf("MCP 服务名或定义无效")
		}
		if err := validateClaudeMCPDefinition(server); err != nil {
			return nil, err
		}
		out = append(out, runtime.MCPSelection{AssetID: ref.Asset.ID, Server: name, Mode: mode, SourcePath: source})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out, nil
}

func validateClaudeMCPDefinition(server map[string]any) error {
	transport := "stdio"
	if raw, exists := server["type"]; exists {
		var ok bool
		transport, ok = raw.(string)
		if !ok {
			return fmt.Errorf("MCP type 必须是 stdio、http 或 sse")
		}
	}
	fields, mapping := map[string]bool{"type": true}, ""
	switch transport {
	case "stdio":
		fields["command"], fields["args"], fields["env"], mapping = true, true, true, "env"
		if command, ok := server["command"].(string); !ok || strings.TrimSpace(command) == "" {
			return fmt.Errorf("stdio MCP 需要 command")
		}
		if raw, exists := server["args"]; exists {
			args, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("MCP args 必须是字符串数组")
			}
			for _, arg := range args {
				if _, ok := arg.(string); !ok {
					return fmt.Errorf("MCP args 必须是字符串数组")
				}
			}
		}
	case "http", "sse":
		fields["url"], fields["headers"], mapping = true, true, "headers"
		fields["headersHelper"] = true
		if raw, exists := server["headersHelper"]; exists {
			if helper, ok := raw.(string); !ok || strings.TrimSpace(helper) == "" || strings.ContainsRune(helper, '\x00') {
				return fmt.Errorf("MCP headersHelper 必须是非空且不含 NUL 的原生命令字符串")
			}
		}
		endpoint, _ := server["url"].(string)
		u, err := url.Parse(endpoint)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("远程 MCP 需要有效的 HTTP/HTTPS URL")
		}
	default:
		return fmt.Errorf("MCP type 必须是 stdio、http 或 sse；其他协议尚未接入")
	}
	for key := range server {
		if !fields[key] {
			return fmt.Errorf("MCP 包含当前协议尚不支持的配置字段")
		}
	}
	if raw, exists := server[mapping]; exists {
		values, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("MCP %s 必须是字符串映射", mapping)
		}
		for key, value := range values {
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("MCP %s 必须是字符串映射", mapping)
			}
			if mapping == "headers" && (key == "" || strings.ContainsAny(key, "\r\n:") || strings.ContainsAny(text, "\r\n")) {
				return fmt.Errorf("MCP headers 名称或值无效")
			}
		}
	}
	return nil
}
