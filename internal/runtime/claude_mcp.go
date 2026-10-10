package runtime

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

type claudeMCPStatus struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Source string   `json:"source"`
	Tools  []string `json:"tools"`
}
type claudeMCPInventory struct {
	Available bool              `json:"available"`
	At        string            `json:"at"`
	Servers   []claudeMCPStatus `json:"servers"`
}

func claudeMCPSource(source string) string {
	switch source {
	case "", "user", "project", "local", "dynamic", "managed", "enterprise", "plugin", "claudeai", "agent", "sdk":
		return source
	default:
		return "unknown"
	}
}

func claudeMCPSourceLabel(source string) string {
	if source == "" || source == "unknown" {
		return "未知来源"
	}
	return source
}

func claudeMCPEvidence(scope Scope, s claudeSnapshot) ([]CapabilityEvidence, bool) {
	inventory := s.MCP
	if len(claudeExecutionPlugins(scope)) > 0 {
		inventory = s.Plugins.MCP
	}
	type boundSelection struct {
		MCPSelection
		source          string
		packageVerified bool
	}
	selections := []boundSelection{}
	for _, selection := range scope.MCPSelections {
		selections = append(selections, boundSelection{selection, "dynamic", true})
	}
	for _, plugin := range claudeExecutionPlugins(scope) {
		for _, component := range plugin.Components {
			if component.Kind == "mcp" {
				selections = append(selections, boundSelection{MCPSelection{AssetID: plugin.AssetID, Server: component.NativeName, Mode: plugin.Mode}, "plugin", claudePluginMatches(plugin, s.Plugins)})
			}
		}
	}
	selected := map[string]bool{}
	counts := map[string]int{}
	statuses := map[string]claudeMCPStatus{}
	for _, server := range inventory.Servers {
		counts[server.Name]++
		statuses[server.Name] = server
	}
	// Successful native history remains evidence of past usage while offline;
	// it does not imply that the server is currently connected or verified.
	usageServers := append([]claudeMCPStatus{}, inventory.Servers...)
	for _, selection := range selections {
		if _, exists := statuses[selection.Server]; !exists {
			usageServers = append(usageServers, claudeMCPStatus{Name: selection.Server})
		}
	}
	used := claudeMCPUsage(s.Entries, usageServers)
	result := []CapabilityEvidence{}
	matched := inventory.Available
	for _, selection := range selections {
		server, exists := statuses[selection.Server]
		selected[selection.Server] = true
		item := CapabilityEvidence{AssetID: selection.AssetID, Server: selection.Server, Mode: selection.Mode, RuntimeStatus: server.Status, Source: claudeMCPSource(server.Source), Tools: append([]string{}, server.Tools...), UsedTools: used[selection.Server], At: inventory.At, Detail: "原生 MCP 清单尚未核对；新的输入会在原生查询启动后重新检查"}
		if counts[selection.Server] > 1 {
			item.Source = "unknown"
		}
		if inventory.Available {
			if selection.Mode == "enabled" {
				item.Verified = exists && counts[selection.Server] == 1 && server.Status == "connected" && server.Source == selection.source
			}
			if selection.Mode == "disabled" {
				item.Verified = !exists || counts[selection.Server] == 1 && server.Status == "disabled" && len(server.Tools) == 0
				if !exists {
					item.RuntimeStatus = "disabled"
				}
			}
			item.Detail = "当前原生 MCP 与选择不一致，可能存在连接失败或同名继承来源"
			if item.Verified {
				item.Detail = "已核对本会话原生 MCP 状态与工具清单；启用项每次调用仍需原生权限判断"
			} else if counts[selection.Server] > 1 {
				item.Detail = "同名 MCP 在原生清单中重复，无法唯一核对来源"
			} else if !exists {
				item.Detail = "所选 MCP 未出现在原生清单；请核对原生来源和 MCP 允许/拒绝策略"
			} else if selection.Mode == "disabled" {
				item.Detail = "MCP 仍从 " + claudeMCPSourceLabel(item.Source) + " 来源加载，无法确认本次禁用"
			} else if server.Status == "needs-auth" {
				item.Detail = "此 Claude 执行配置需要原生 OAuth 认证；登录与令牌刷新由原生凭据存储管理，工具调用仍需单独审批"
			} else if server.Source != selection.source {
				item.Detail = "MCP 来自 " + claudeMCPSourceLabel(item.Source) + "，与本次动态选择不一致"
				if selection.source == "plugin" {
					item.Detail = "MCP 来自 " + claudeMCPSourceLabel(item.Source) + "，与所选插件来源不一致"
				}
			} else if server.Status == "failed" {
				item.Detail = "MCP 连接失败，请核对服务及认证配置"
			}
		}
		if !selection.packageVerified {
			item.Verified = false
			item.Detail = "插件整包来源或组件尚未核对；历史成功调用不代表当前加载已验证"
		}
		aliases := 0
		for _, other := range inventory.Servers {
			if claudeMCPToolPrefix(other.Name) == claudeMCPToolPrefix(selection.Server) {
				aliases++
			}
		}
		if aliases > 1 || selection.Mode == "disabled" && selection.source == "plugin" && aliases > 0 {
			item.Verified = false
			item.Source = "unknown"
			if counts[selection.Server] <= 1 {
				item.Detail = "原生 MCP 工具命名空间与其他来源冲突，无法唯一核对"
			}
		}
		matched = matched && item.Verified
		result = append(result, item)
	}
	names := []string{}
	for name := range statuses {
		if !selected[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		server := statuses[name]
		source := claudeMCPSource(server.Source)
		if counts[name] > 1 {
			source = "unknown"
		}
		result = append(result, CapabilityEvidence{Server: name, Mode: "inherit", RuntimeStatus: server.Status, Source: source, Tools: append([]string{}, server.Tools...), UsedTools: used[name], At: inventory.At, Detail: "沿用原生 " + claudeMCPSourceLabel(source) + " 来源；不因其他资产的选择而移除"})
	}
	return result, matched
}
func claudeMCPToolPrefix(server string) string {
	return "mcp__" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, server) + "__"
}

func claudeMCPUsage(entries []claudeEntry, servers []claudeMCPStatus) map[string][]string {
	type call struct{ server, tool string }
	calls := map[string]call{}
	used := map[string][]string{}
	for _, entry := range entries {
		if entry.Kind != "event" || entry.Event.ParentToolUseID != nil {
			continue
		}
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error"`
		}
		if json.Unmarshal(entry.Event.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_use" && block.ID != "" {
				matches := []call{}
				for _, server := range servers {
					prefix := claudeMCPToolPrefix(server.Name)
					if strings.HasPrefix(block.Name, prefix) {
						matches = append(matches, call{server.Name, strings.TrimPrefix(block.Name, prefix)})
					}
				}
				if len(matches) == 1 {
					calls[block.ID] = matches[0]
				} else {
					delete(calls, block.ID)
				}
			}
			if block.Type == "tool_result" && !block.IsError {
				if c, ok := calls[block.ToolUseID]; ok && !slices.Contains(used[c.server], c.tool) {
					used[c.server] = append(used[c.server], c.tool)
				}
			}
		}
	}
	for server := range used {
		sort.Strings(used[server])
	}
	return used
}
