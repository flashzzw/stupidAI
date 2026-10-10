package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type HookSourceCatalog struct {
	Scope          string            `json:"scope,omitempty"`
	NativeID       string            `json:"native_id"`
	Cwd            string            `json:"cwd"`
	Source         string            `json:"source"`
	At             string            `json:"at"`
	Complete       bool              `json:"complete"`
	Items          []HookSourceItem  `json:"items"`
	Policy         HookSourcePolicy  `json:"policy"`
	PolicyReported bool              `json:"policy_reported"`
	WarningCount   int               `json:"warning_count,omitempty"`
	Errors         []HookSourceError `json:"errors,omitempty"`
	Limitations    []string          `json:"limitations"`
}

type HookSourceError struct {
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

type HookSourceItem struct {
	Key            string   `json:"key,omitempty"`
	SourcePath     string   `json:"source_path,omitempty"`
	PluginID       string   `json:"plugin_id,omitempty"`
	TrustStatus    string   `json:"trust_status,omitempty"`
	Managed        *bool    `json:"managed,omitempty"`
	Event          string   `json:"event"`
	Matcher        string   `json:"matcher"`
	Source         string   `json:"source"`
	PluginName     string   `json:"plugin_name"`
	Type           string   `json:"type"`
	Disabled       bool     `json:"disabled"`
	RunsOnce       bool     `json:"runs_once"`
	Background     bool     `json:"background"`
	Conditional    bool     `json:"conditional"`
	TimeoutSeconds *float64 `json:"timeout_seconds,omitempty"`
	DefinitionHash string   `json:"definition_hash"`
}

type HookSourcePolicy struct {
	DisabledByPolicy   bool `json:"disabled_by_policy"`
	ManagedOnly        bool `json:"managed_only"`
	PluginOnly         bool `json:"plugin_only"`
	AllDisabled        bool `json:"all_disabled"`
	PolicyHookCount    int  `json:"policy_hook_count"`
	PolicyUnreadable   bool `json:"policy_unreadable"`
	SafeMode           bool `json:"safe_mode"`
	BareMode           bool `json:"bare_mode"`
	SettingsErrorCount int  `json:"settings_error_count"`
}

type HookCatalogReader interface {
	ReadHookCatalog(context.Context, Scope) (HookSourceCatalog, error)
}

func (a *Adapters) ReadHookCatalog(ctx context.Context, scope Scope) (HookSourceCatalog, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return HookSourceCatalog{}, err
	}
	reader, ok := r.(HookCatalogReader)
	if !ok {
		return HookSourceCatalog{}, fmt.Errorf("此后端尚无原生 hook 来源清单入口")
	}
	return reader.ReadHookCatalog(ctx, scope)
}

type claudeHookInventory struct {
	Available bool              `json:"available"`
	At        string            `json:"at"`
	Items     []HookSourceItem  `json:"items"`
	Policy    *HookSourcePolicy `json:"policy"`
}

func (c *Claude) ReadHookCatalog(ctx context.Context, scope Scope) (HookSourceCatalog, error) {
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || scope.NativeID == "" || scope.StateNamespace == "" || !filepath.IsAbs(scope.Cwd) {
		return HookSourceCatalog{}, fmt.Errorf("此会话尚无原生 hook 来源清单入口")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return HookSourceCatalog{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A catalog reads an existing query; client() would start a new bridge.
	p := c.clients[scope.Instance.ID]
	if c.closing || p == nil {
		return HookSourceCatalog{}, fmt.Errorf("原生查询未开启；读取清单不会启动或恢复查询")
	}
	var result struct {
		claudeSnapshot
		Inventory claudeHookInventory `json:"inventory"`
	}
	if err := p.call(ctx, "hook_catalog", map[string]any{"native_id": scope.NativeID}, &result); err != nil {
		return HookSourceCatalog{}, fmt.Errorf("当前原生 hook 清单不可用；请确认原查询仍开启且没有正在改配")
	}
	if result.Connection == "" || result.Config.ID != scope.NativeID || result.Config.Source != scope.NativeID || result.Config.Cwd != scope.Cwd || result.Config.Model != scope.Route.Model || result.Config.Endpoint != scope.Route.Endpoint || result.Config.Executable != scope.Instance.Executable || result.Config.SettingsFile != scope.Instance.SettingsFile || !slices.Equal(result.Config.MCPSelections, scope.MCPSelections) || !slices.Equal(result.Config.SkillSelections, scope.SkillSelections) || !EqualPluginSelections(result.Config.PluginSelections, scope.PluginSelections) {
		return HookSourceCatalog{}, fmt.Errorf("hook 清单不属于当前保存的原生配置")
	}
	return claudeHookCatalog(scope, result.Inventory)
}

var hookDefinitionHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func claudeHookCatalog(scope Scope, inventory claudeHookInventory) (HookSourceCatalog, error) {
	out := HookSourceCatalog{Scope: "query", PolicyReported: true, NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.native.hooks_menu", At: inventory.At,
		Items: inventory.Items, Limitations: []string{
			"仅展示当前查询的原生 hook 菜单；受管策略可隐藏条目，配置解析错误也会使来源缺席。",
			"已登记不代表执行成功、权限授予或可独立选配；禁用状态只表示原生本次报告。",
			"命令、提示词、URL 和可编辑配置不随清单传出；定义摘要只用于识别变化。",
			"插件来源键保留原值；没有报告文件路径，不按插件名猜测资产绑定。",
		}}
	_, timeErr := time.Parse(time.RFC3339Nano, inventory.At)
	if !inventory.Available || inventory.Items == nil || len(inventory.Items) > 2048 || inventory.Policy == nil || timeErr != nil {
		return HookSourceCatalog{}, fmt.Errorf("原生 hook 清单不完整或超过读取上限")
	}
	if inventory.Policy.PolicyHookCount < 0 || inventory.Policy.SettingsErrorCount < 0 {
		return HookSourceCatalog{}, fmt.Errorf("原生 hook 策略元数据无效")
	}
	valid := func(value string, max int, empty bool) bool {
		return (empty || value != "") && len(value) <= max && !strings.ContainsRune(value, '\x00')
	}
	for _, h := range inventory.Items {
		if !valid(h.Event, 300, false) || !valid(h.Matcher, 4000, true) || !valid(h.Source, 300, false) || !valid(h.Type, 300, false) || !valid(h.PluginName, 1200, true) || !hookDefinitionHash.MatchString(h.DefinitionHash) || h.TimeoutSeconds != nil && *h.TimeoutSeconds <= 0 {
			return HookSourceCatalog{}, fmt.Errorf("原生 hook 元数据无效，未返回部分清单")
		}
	}
	out.Policy = *inventory.Policy
	return out, nil
}
