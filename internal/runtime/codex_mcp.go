package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"codex-netcap/internal/codex"
)

func addCodexMCPOverrides(ctx context.Context, p codexConnection, scope Scope, overrides map[string]any) error {
	if len(scope.MCPSelections) == 0 {
		return nil
	}
	if scope.Instance.Kind != "codex" || scope.Purpose == "probe" {
		return fmt.Errorf("此执行类型不支持原生 MCP 选配")
	}
	var read struct {
		Config struct {
			Servers map[string]json.RawMessage `json:"mcp_servers"`
		} `json:"config"`
		Layers []struct {
			Name struct {
				Type string `json:"type"`
				File string `json:"file"`
			} `json:"name"`
			Config struct {
				Servers map[string]json.RawMessage `json:"mcp_servers"`
			} `json:"config"`
		} `json:"layers"`
	}
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": true}, &read); err != nil {
		return fmt.Errorf("无法核对原生 MCP 配置来源")
	}
	_, definitions, err := codexMCPDefinitionConfigs(ctx, p, scope, false)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, selection := range scope.MCPSelections {
		if !codexProviderKey.MatchString(selection.Server) || seen[selection.Server] || (selection.Mode != "enabled" && selection.Mode != "disabled") {
			return fmt.Errorf("原生 MCP 选择无效或重复")
		}
		seen[selection.Server] = true
		if definition, exists := definitions[selection.Server]; exists {
			if len(read.Config.Servers[selection.Server]) > 0 {
				return fmt.Errorf("共享 MCP %s 与继承配置同名，不能混合来源或凭据", selection.Server)
			}
			for _, layer := range read.Layers {
				if len(layer.Config.Servers[selection.Server]) > 0 {
					return fmt.Errorf("共享 MCP %s 与已有配置层同名，不能唯一绑定", selection.Server)
				}
			}
			for key, value := range definition {
				overrides["mcp_servers."+selection.Server+"."+key] = value
			}
			continue
		}
		var server struct {
			Command     string `json:"command"`
			URL         string `json:"url"`
			Environment string `json:"environment_id"`
		}
		if json.Unmarshal(read.Config.Servers[selection.Server], &server) != nil || server.Command == "" || server.URL != "" || (server.Environment != "" && server.Environment != "local") {
			return fmt.Errorf("MCP %s 不是可核对的本地 stdio 配置", selection.Server)
		}
		found := false
		for _, layer := range read.Layers {
			if len(layer.Config.Servers[selection.Server]) == 0 {
				continue
			}
			path, err := filepath.EvalSymlinks(layer.Name.File)
			if err != nil || layer.Name.Type != "user" || path != selection.SourcePath || found {
				return fmt.Errorf("MCP %s 有未纳入快照的配置层或来源冲突", selection.Server)
			}
			found = true
		}
		if !found {
			return fmt.Errorf("MCP %s 缺少已登记来源的原生依据", selection.Server)
		}
		overrides["mcp_servers."+selection.Server+".enabled"] = selection.Mode == "enabled"
	}
	return nil
}

type codexMCPStatus struct {
	Name       string                     `json:"name"`
	PluginID   json.RawMessage            `json:"pluginId"`
	Status     string                     `json:"runtimeStatus"`
	Tools      map[string]json.RawMessage `json:"tools"`
	ToolsError *string                    `json:"toolsError"`
}

// null explicitly reports a non-plugin server. Omission is an older/unknown
// provenance contract and must not stand in for that positive statement.
func codexMCPOrigin(raw json.RawMessage) (string, bool) {
	if strings.TrimSpace(string(raw)) == "null" {
		return "", true
	}
	var id string
	if json.Unmarshal(raw, &id) != nil || id == "" || len(id) > 1200 || strings.TrimSpace(id) != id || strings.ContainsRune(id, '\x00') {
		return "", false
	}
	return id, true
}

func codexMCPSource(id string, known bool) string {
	if !known {
		return "unknown"
	}
	if id != "" {
		return "plugin"
	}
	return "configuration"
}

type codexMCPUsageSource struct {
	Server, PluginID string
	Known            bool
}

// Read the selected thread, not a global inventory. Missing pages, duplicate
// servers, or absent requested entries cannot count as a successful exclusion.
func codexMCPEvidence(ctx context.Context, p codexConnection, scope Scope, turns []codex.Turn) ([]CapabilityEvidence, bool) {
	used := map[codexMCPUsageSource][]string{}
	for _, turn := range turns {
		for _, item := range turn.Items {
			pluginID, known := codexMCPOrigin(item.PluginID)
			origin := codexMCPUsageSource{Server: item.Server, PluginID: pluginID, Known: known}
			var result *struct {
				IsError bool `json:"isError"`
			}
			if item.Type == "mcpToolCall" && item.Status == "completed" && item.Server != "" && item.Tool != "" && json.Unmarshal(item.MCPResult, &result) == nil && result != nil && !result.IsError && (len(item.MCPError) == 0 || string(item.MCPError) == "null") && !slices.Contains(used[origin], item.Tool) {
				used[origin] = append(used[origin], item.Tool)
			}
		}
	}
	explicit := len(scope.MCPSelections)+len(scope.PluginSelections) > 0
	if !explicit && len(used) == 0 {
		return nil, true
	}
	statuses, complete := codexMCPStatuses(ctx, p, scope.NativeID)
	selections := append([]MCPSelection{}, scope.MCPSelections...)
	pluginSources := map[string]string{}
	pluginInvalid := map[string]bool{}
	for _, pack := range scope.PluginSelections {
		pluginInvalid[pack.AssetID] = verifyCodexPluginCache(scope, pack) != nil
		expected := map[string]bool{}
		for _, component := range pack.Components {
			if component.Kind != "mcp" {
				continue
			}
			expected[component.NativeName] = true
			if slices.ContainsFunc(selections, func(s MCPSelection) bool { return s.Server == component.NativeName }) {
				pluginInvalid[pack.AssetID] = true
				continue
			}
			selections = append(selections, MCPSelection{AssetID: pack.AssetID, Server: component.NativeName, Mode: pack.Mode})
			pluginSources[component.NativeName] = pack.NativeSource
		}
		for name, status := range statuses {
			id, known := codexMCPOrigin(status.PluginID)
			if known && id == pack.NativeSource && !expected[name] {
				pluginInvalid[pack.AssetID] = true
			}
		}
	}
	evidence := []CapabilityEvidence{}
	represented := map[codexMCPUsageSource]bool{}
	// Missing current inventory must not erase historical usage or introduce
	// a new input gate after the last explicit selection has been released.
	matched := complete || !explicit
	selected := map[string]bool{}
	for _, selection := range selections {
		status, exists := statuses[selection.Server]
		expectedPlugin := pluginSources[selection.Server]
		pluginID, sourceKnown := codexMCPOrigin(status.PluginID)
		if complete && !exists && expectedPlugin != "" && selection.Mode == "disabled" {
			pluginID, sourceKnown, status.Status = expectedPlugin, true, "disabled"
		}
		item := CapabilityEvidence{AssetID: selection.AssetID, Server: selection.Server, Mode: selection.Mode, RuntimeStatus: status.Status,
			PluginID: pluginID, Source: codexMCPSource(pluginID, sourceKnown),
			Tools: []string{}, At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "原生会话 MCP 清单尚未完整核对"}
		for tool := range status.Tools {
			item.Tools = append(item.Tools, tool)
		}
		sort.Strings(item.Tools)
		if complete && (exists || expectedPlugin != "" && selection.Mode == "disabled") {
			item.Verified = !pluginInvalid[selection.AssetID] && sourceKnown && pluginID == expectedPlugin && status.ToolsError == nil && ((selection.Mode == "enabled" && status.Status == "connected") || (selection.Mode == "disabled" && status.Status == "disabled" && len(status.Tools) == 0))
			item.Detail = "本次会话的原生 MCP 状态与选择不一致"
			if item.Verified {
				item.Detail = "已核对本次会话的原生 MCP 状态与工具清单；调用仍受原生权限控制"
			}
		}
		if pluginID != "" && expectedPlugin == "" {
			item.Detail = "原生 MCP 属于 CLI 插件，与所选配置文件来源不一致"
		}
		if expectedPlugin != "" && !item.Verified {
			item.Detail = "固定插件缓存、完整来源或 MCP 组件清单未核对一致"
		}
		if !sourceKnown {
			item.Detail = "原生未报告有效的插件来源字段，不能确认与所选配置文件一致"
		}
		if !item.Verified {
			matched = false
		} else {
			origin := codexMCPUsageSource{Server: selection.Server, PluginID: expectedPlugin, Known: true}
			item.UsedTools = used[origin]
			represented[origin] = true
		}
		evidence = append(evidence, item)
		selected[selection.Server] = true
	}
	for _, invalid := range pluginInvalid {
		if invalid {
			matched = false
		}
	}
	// Report inherited servers separately; explicit picks do not disable them.
	names := []string{}
	for name := range statuses {
		if !selected[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		status := statuses[name]
		pluginID, sourceKnown := codexMCPOrigin(status.PluginID)
		item := CapabilityEvidence{Server: name, Mode: "inherit", RuntimeStatus: status.Status, Tools: []string{}, At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "沿用原生配置，未纳入本次显式选配核对"}
		item.PluginID = pluginID
		item.Source = codexMCPSource(pluginID, sourceKnown)
		if sourceKnown {
			origin := codexMCPUsageSource{Server: name, PluginID: pluginID, Known: true}
			item.UsedTools = used[origin]
			represented[origin] = true
		}
		for tool := range status.Tools {
			item.Tools = append(item.Tools, tool)
		}
		sort.Strings(item.Tools)
		evidence = append(evidence, item)
	}
	// A previous plugin or an old record without provenance must remain visible
	// without transferring its successful calls to today's same-name asset.
	historical := []codexMCPUsageSource{}
	for origin := range used {
		if !represented[origin] {
			historical = append(historical, origin)
		}
	}
	sort.Slice(historical, func(i, j int) bool {
		a, b := historical[i], historical[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		if a.PluginID != b.PluginID {
			return a.PluginID < b.PluginID
		}
		return !a.Known && b.Known
	})
	for _, origin := range historical {
		tools := used[origin]
		sort.Strings(tools)
		detail := "历史成功调用按原生插件来源单独保留，不表示本次加载，也不自动归入当前资产"
		if !origin.Known {
			detail = "历史成功调用缺少有效插件来源字段，未按同名服务推断资产归属"
		}
		evidence = append(evidence, CapabilityEvidence{Server: origin.Server, PluginID: origin.PluginID, Source: codexMCPSource(origin.PluginID, origin.Known), Mode: "history", RuntimeStatus: "unreported", Tools: []string{}, UsedTools: tools, At: time.Now().UTC().Format(time.RFC3339Nano), Detail: detail})
	}
	return evidence, matched
}

func codexMCPStatuses(ctx context.Context, p codexConnection, nativeID string) (map[string]codexMCPStatus, bool) {
	statuses := map[string]codexMCPStatus{}
	cursor := ""
	seen := map[string]bool{}
	complete := false
	for page := 0; page < 100; page++ {
		var result struct {
			Data []codexMCPStatus `json:"data"`
			Next string           `json:"nextCursor"`
		}
		params := map[string]any{"threadId": nativeID, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if p.Call(ctx, "mcpServerStatus/list", params, &result) != nil || result.Data == nil {
			break
		}
		duplicate := false
		for _, status := range result.Data {
			if status.Name == "" {
				duplicate = true
				break
			}
			if _, ok := statuses[status.Name]; ok {
				duplicate = true
				break
			}
			statuses[status.Name] = status
		}
		if duplicate {
			break
		}
		if result.Next == "" {
			complete = true
			break
		}
		if seen[result.Next] {
			break
		}
		seen[result.Next] = true
		cursor = result.Next
	}
	return statuses, complete
}
