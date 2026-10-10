package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func addCodexPluginOverrides(ctx context.Context, p codexConnection, scope Scope, overrides map[string]any) error {
	if len(scope.PluginSelections) == 0 {
		return nil
	}
	if hasCodexHookSelections(scope) {
		if _, err := codexHookRequirements(ctx, p, scope); err != nil {
			return err
		}
	}
	if err := verifyCodexSkillAssetSources(ctx, p, scope); err != nil {
		return err
	}
	var read struct {
		Config struct {
			Servers map[string]json.RawMessage `json:"mcp_servers"`
			Plugins map[string]struct {
				Enabled *bool `json:"enabled"`
			} `json:"plugins"`
			Features map[string]bool `json:"features"`
			Hooks    struct {
				State map[string]json.RawMessage `json:"state"`
			} `json:"hooks"`
		} `json:"config"`
	}
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": true}, &read); err != nil {
		return fmt.Errorf("无法核对插件继承配置")
	}
	if !read.Config.Features["plugins"] {
		return fmt.Errorf("执行配置尚未启用 Codex 原生插件")
	}
	servers := map[string]bool{}
	for _, s := range scope.MCPSelections {
		servers[s.Server] = true
	}
	for _, pack := range scope.PluginSelections {
		if err := verifyCodexPluginCache(scope, pack); err != nil {
			return err
		}
		for id, plugin := range read.Config.Plugins {
			if id == pack.NativeSource || strings.HasPrefix(id, pack.Name+"@") && (plugin.Enabled == nil || *plugin.Enabled) {
				return fmt.Errorf("所选插件已有同名继承来源，不能单独核对该资源")
			}
		}
		for _, c := range pack.Components {
			if c.Kind != "mcp" {
				continue
			}
			if servers[c.NativeName] || len(read.Config.Servers[c.NativeName]) > 0 {
				return fmt.Errorf("插件 MCP 与其他配置存在同名服务，不能唯一绑定")
			}
			servers[c.NativeName] = true
		}
		overrides["plugins."+pack.NativeSource+".enabled"] = pack.Mode == "enabled"
	}
	if err := addCodexHookOverrides(scope, read.Config.Features, read.Config.Hooks.State, overrides); err != nil {
		return err
	}
	return nil
}

func codexPluginEvidence(scope Scope, rows []CapabilityEvidence) []PluginEvidence {
	out := []PluginEvidence{}
	for _, p := range scope.PluginSelections {
		verified := len(p.Components) > 0
		for _, component := range p.Components {
			if component.Kind != "mcp" {
				continue
			}
			matches := 0
			for _, row := range rows {
				if row.AssetID == p.AssetID && row.Server == component.NativeName && row.PluginID == p.NativeSource && row.Mode == p.Mode && row.Verified {
					matches++
				}
			}
			verified = verified && matches == 1
		}
		detail := "固定插件缓存与原生 MCP 组件尚未核对一致"
		if verified {
			detail = "固定插件缓存与原生 MCP 来源、清单匹配；工具调用仍受原生权限控制"
		}
		out = append(out, PluginEvidence{AssetID: p.AssetID, Name: p.Name, Mode: p.Mode, Version: p.Version, NativeSource: p.NativeSource, Verified: verified, At: time.Now().UTC().Format(time.RFC3339Nano), Detail: detail, Components: p.Components})
	}
	return out
}
