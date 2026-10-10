package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// hooks/list reports directory/profile discovery. It accepts no thread ID and
// cannot prove that a saved thread loaded, trusted or ran these definitions.
func (c *Codex) ReadHookCatalog(ctx context.Context, scope Scope) (HookSourceCatalog, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || !filepath.IsAbs(scope.Cwd) || strings.ContainsRune(scope.Cwd, '\x00') {
		return HookSourceCatalog{}, fmt.Errorf("此执行类型或目录不支持 Codex hook 来源发现")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return HookSourceCatalog{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return HookSourceCatalog{}, fmt.Errorf("无法连接已绑定的 Codex 后台以查询 hook 来源")
	}
	var response struct {
		Data []struct {
			Cwd   string `json:"cwd"`
			Hooks []struct {
				Key        string `json:"key"`
				Event      string `json:"eventName"`
				Matcher    string `json:"matcher"`
				Type       string `json:"handlerType"`
				Timeout    *int64 `json:"timeoutSec"`
				SourcePath string `json:"sourcePath"`
				Source     string `json:"source"`
				PluginID   string `json:"pluginId"`
				Enabled    *bool  `json:"enabled"`
				Managed    *bool  `json:"isManaged"`
				Trust      string `json:"trustStatus"`
				Hash       string `json:"currentHash"`
				Async      *bool  `json:"async"`
			} `json:"hooks"`
			Warnings []string `json:"warnings"`
			Errors   []struct {
				Path string `json:"path"`
			} `json:"errors"`
		} `json:"data"`
	}
	if err = p.Call(ctx, "hooks/list", map[string]any{"cwds": []string{scope.Cwd}}, &response); err != nil {
		// RPC/parse diagnostics may quote commands or source contents.
		return HookSourceCatalog{}, fmt.Errorf("原生 hook 来源查询失败；未取得本次清单")
	}
	if len(response.Data) != 1 || response.Data[0].Cwd != scope.Cwd || response.Data[0].Hooks == nil || response.Data[0].Warnings == nil || response.Data[0].Errors == nil {
		return HookSourceCatalog{}, fmt.Errorf("原生 hook 清单缺失或目录不匹配")
	}
	entry := response.Data[0]
	if len(entry.Hooks)+len(entry.Warnings)+len(entry.Errors) > 2048 {
		return HookSourceCatalog{}, fmt.Errorf("原生 hook 清单超出读取上限，未返回截断结果")
	}
	out := HookSourceCatalog{Scope: "directory", NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.hooks/list", At: time.Now().UTC().Format(time.RFC3339Nano), Items: []HookSourceItem{}, WarningCount: len(entry.Warnings), Limitations: []string{
		"按保存的工作目录与执行配置发现来源；不是该会话的已加载或执行成功清单。",
		"项目未信任、受管规则和配置解析失败都可能使来源缺席；未发现不等于文件不存在。",
		"启用、信任与执行是不同状态；此入口不授予信任、修改选择或启动线程。",
		"路径、插件 ID 和内容摘要保留原生报告；不按名称推断资产或安装版本。",
		"完整策略未由此接口报告；命令、提示词、原始警告和错误正文不传出。",
	}}
	textOK := func(s string, max int, empty bool) bool {
		return (empty || s != "") && len(s) <= max && !strings.ContainsRune(s, '\x00')
	}
	pathOK := func(s string) bool { return textOK(s, 16384, false) && filepath.IsAbs(s) }
	seen := map[string]bool{}
	for _, h := range entry.Hooks {
		if !textOK(h.Key, 4096, false) || seen[h.Key] || !textOK(h.Event, 300, false) || !textOK(h.Matcher, 4000, true) || !textOK(h.Type, 300, false) || !textOK(h.Source, 300, false) || !pathOK(h.SourcePath) || !textOK(h.PluginID, 1200, true) || !textOK(h.Trust, 300, false) || !strings.HasPrefix(h.Hash, "sha256:") || !hookDefinitionHash.MatchString(strings.TrimPrefix(h.Hash, "sha256:")) || h.Enabled == nil || h.Managed == nil || h.Timeout == nil || *h.Timeout < 0 || *h.Timeout > 1<<53 || h.Type == "command" && h.Async == nil {
			return HookSourceCatalog{}, fmt.Errorf("原生 hook 元数据缺失、重复或无效，未返回部分清单")
		}
		seen[h.Key] = true
		seconds := float64(*h.Timeout)
		out.Items = append(out.Items, HookSourceItem{Key: h.Key, Event: h.Event, Matcher: h.Matcher, Type: h.Type, Source: h.Source, SourcePath: h.SourcePath, PluginID: h.PluginID, TrustStatus: h.Trust, Managed: h.Managed, Disabled: !*h.Enabled, Background: h.Async != nil && *h.Async, TimeoutSeconds: &seconds, DefinitionHash: h.Hash})
	}
	for _, e := range entry.Errors {
		path := ""
		if pathOK(e.Path) {
			path = e.Path
		}
		out.Errors = append(out.Errors, HookSourceError{Path: path, Detail: "原生报告此 hook 来源无效；源内容可能未被纳入发现结果"})
	}
	return out, nil
}
