package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (c *Codex) DiscoverSkills(ctx context.Context, scope Scope) (SkillInventory, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose == "probe" || !filepath.IsAbs(scope.Cwd) {
		return SkillInventory{}, fmt.Errorf("此执行类型或目录不支持 Skill 来源发现")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return SkillInventory{}, fmt.Errorf("无法连接已绑定的 Codex 后台以查询 Skill 来源")
	}
	return discoverCodexSkills(ctx, p, scope)
}

func discoverCodexSkills(ctx context.Context, p codexConnection, scope Scope) (SkillInventory, error) {
	var response struct {
		Data []struct {
			Cwd    string `json:"cwd"`
			Skills []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Path        string `json:"path"`
				Scope       string `json:"scope"`
				Enabled     *bool  `json:"enabled"`
				PluginID    string `json:"pluginId"`
			} `json:"skills"`
			Errors []struct {
				Path string `json:"path"`
			} `json:"errors"`
		} `json:"data"`
	}
	if err := p.Call(ctx, "skills/list", map[string]any{"cwds": []string{scope.Cwd}, "forceReload": true}, &response); err != nil {
		// Parser/RPC errors can quote source contents. Do not return them as UI
		// diagnostics or retain them in platform state.
		return SkillInventory{}, fmt.Errorf("原生 Skill 清单查询失败；未取得本次发现结果")
	}
	if len(response.Data) != 1 || response.Data[0].Cwd != scope.Cwd || response.Data[0].Skills == nil || response.Data[0].Errors == nil {
		return SkillInventory{}, fmt.Errorf("原生 Skill 清单缺失或目录不匹配")
	}
	entry := response.Data[0]
	if len(entry.Skills)+len(entry.Errors) > 2048 {
		return SkillInventory{}, fmt.Errorf("原生 Skill 清单超出 2048 项，未返回截断结果")
	}
	out := SkillInventory{Cwd: scope.Cwd, Source: "codex.skills/list", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, Skills: []DiscoveredSkill{}, Errors: []SkillScanError{}}
	seen := map[string]bool{}
	for _, s := range entry.Skills {
		path := filepath.Clean(s.Path)
		if strings.TrimSpace(s.Name) == "" || !filepath.IsAbs(s.Path) || strings.ContainsRune(s.Path, '\x00') || s.Enabled == nil || seen[path] {
			out.Errors = append(out.Errors, SkillScanError{Detail: "原生清单含缺失字段、无效路径或重复来源；该项未计入可发现清单"})
			continue
		}
		seen[path] = true
		switch s.Scope {
		case "user", "repo", "system", "admin":
		default:
			out.Errors = append(out.Errors, SkillScanError{Path: s.Path, Detail: "原生来源类别尚不受支持"})
		}
		out.Skills = append(out.Skills, DiscoveredSkill{Name: s.Name, Description: s.Description, Path: s.Path, Scope: s.Scope, Enabled: *s.Enabled, PluginID: s.PluginID})
	}
	for _, scan := range entry.Errors {
		out.Errors = append(out.Errors, SkillScanError{Path: scan.Path, Detail: "原生扫描报告此 Skill 来源无效；请检查源文件"})
	}
	out.Complete = len(out.Errors) == 0
	sort.Slice(out.Skills, func(i, j int) bool {
		if out.Skills[i].Name != out.Skills[j].Name {
			return out.Skills[i].Name < out.Skills[j].Name
		}
		return out.Skills[i].Path < out.Skills[j].Path
	})
	return out, nil
}
