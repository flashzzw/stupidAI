package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// PermissionRuleCatalog records the original query's native report. Rules can
// change during a turn; a sample neither freezes policy nor authorizes a tool.
type PermissionRuleCatalog struct {
	NativeID        string                    `json:"native_id"`
	Cwd             string                    `json:"cwd"`
	Source          string                    `json:"source"`
	QueryID         string                    `json:"query_id"`
	ConfigurationID string                    `json:"configuration_id"`
	At              string                    `json:"at"`
	SampleHash      string                    `json:"sample_hash"`
	Complete        bool                      `json:"complete"`
	ManagedOnly     bool                      `json:"managed_only"`
	ConflictCount   int                       `json:"conflict_count"`
	Items           []PermissionRuleItem      `json:"items"`
	Directories     []PermissionDirectory     `json:"directories"`
	Errors          []PermissionSettingsError `json:"errors"`
	Limitations     []string                  `json:"limitations"`
}

type PermissionRuleItem struct {
	Behavior    string `json:"behavior"`
	Source      string `json:"source"`
	Rule        string `json:"rule"`
	Editability string `json:"editability"`
	NotInEffect bool   `json:"not_in_effect"`
	Conflicting bool   `json:"conflicting"`
}

type PermissionDirectory struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

type PermissionSettingsError struct {
	File  string `json:"file"`
	Field string `json:"field"`
}

type PermissionCatalogReader interface {
	ReadPermissionCatalog(context.Context, Scope) (PermissionRuleCatalog, error)
}

func (a *Adapters) ReadPermissionCatalog(ctx context.Context, scope Scope) (PermissionRuleCatalog, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return PermissionRuleCatalog{}, err
	}
	reader, ok := r.(PermissionCatalogReader)
	if !ok {
		return PermissionRuleCatalog{}, fmt.Errorf("此后端尚无当前查询的权限规则读取入口")
	}
	return reader.ReadPermissionCatalog(ctx, scope)
}

type claudePermissionInventory struct {
	Version     int    `json:"version"`
	Available   bool   `json:"available"`
	At          string `json:"at"`
	Cwd         string `json:"cwd"`
	ManagedOnly *bool  `json:"managed_only"`
	Items       []struct {
		Behavior    string `json:"behavior"`
		Source      string `json:"source"`
		Rule        string `json:"rule"`
		Editability string `json:"editability"`
		NotInEffect *bool  `json:"not_in_effect"`
	} `json:"items"`
	Directories []PermissionDirectory     `json:"directories"`
	Errors      []PermissionSettingsError `json:"errors"`
}

func (c *Claude) ReadPermissionCatalog(ctx context.Context, scope Scope) (PermissionRuleCatalog, error) {
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || scope.NativeID == "" || scope.StateNamespace == "" || !filepath.IsAbs(scope.Cwd) {
		return PermissionRuleCatalog{}, fmt.Errorf("此会话尚无原生权限规则读取入口")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return PermissionRuleCatalog{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Reading a report must use the existing query, including after source
	// drift. Starting a bridge or reopening a query would change the evidence.
	p := c.clients[scope.Instance.ID]
	if c.closing || p == nil {
		return PermissionRuleCatalog{}, fmt.Errorf("原生查询未开启；读取权限规则不会启动或恢复查询")
	}
	var result struct {
		claudeSnapshot
		QueryID         string                    `json:"query_id"`
		ConfigurationID string                    `json:"configuration_id"`
		Inventory       claudePermissionInventory `json:"inventory"`
	}
	if err := p.call(ctx, "permission_catalog", map[string]any{"native_id": scope.NativeID}, &result); err != nil {
		return PermissionRuleCatalog{}, fmt.Errorf("当前原生权限规则不可用；请确认原查询仍开启且没有正在改配")
	}
	if result.Connection == "" || result.Config.ID != scope.NativeID || result.Config.Source != scope.NativeID ||
		result.Config.Cwd != scope.Cwd || result.Config.Model != scope.Route.Model || result.Config.Endpoint != scope.Route.Endpoint ||
		result.Config.Executable != scope.Instance.Executable || result.Config.SettingsFile != scope.Instance.SettingsFile ||
		result.Config.Permission != scope.RecoveryPermission || !slices.Equal(result.Config.MCPSelections, scope.MCPSelections) ||
		!slices.Equal(result.Config.SkillSelections, scope.SkillSelections) || !EqualPluginSelections(result.Config.PluginSelections, scope.PluginSelections) ||
		result.Config.SettingsGuardVersion != scope.SettingsGuardVersion || !slices.Equal(result.Config.SettingsSources, scope.ClaudeSettingsSources) ||
		!claudeInstalledConfigMatches(scope, result.claudeSnapshot) {
		return PermissionRuleCatalog{}, fmt.Errorf("权限规则报告不属于当前保存的原生配置")
	}
	return claudePermissionCatalog(scope, result.QueryID, result.ConfigurationID, result.Inventory)
}

func claudePermissionCatalog(scope Scope, queryID, configurationID string, inventory claudePermissionInventory) (PermissionRuleCatalog, error) {
	fail := func() (PermissionRuleCatalog, error) {
		return PermissionRuleCatalog{}, fmt.Errorf("原生权限规则报告缺失或无效，未返回部分清单")
	}
	_, timeErr := time.Parse(time.RFC3339Nano, inventory.At)
	if inventory.Version != 1 || !inventory.Available || timeErr != nil || inventory.Cwd != scope.Cwd ||
		!instructionIdentifier.MatchString(queryID) || !claudeSettingsFileSHA.MatchString(configurationID) || inventory.ManagedOnly == nil ||
		inventory.Items == nil || len(inventory.Items) > 4096 || inventory.Directories == nil || len(inventory.Directories) > 256 ||
		inventory.Errors == nil || len(inventory.Errors) > 64 {
		return fail()
	}
	bytes := 0
	valid := func(value string, max int, empty, controls bool) bool {
		bytes += len(value)
		return (empty || value != "") && len(value) <= max && bytes <= 8<<20 && utf8.ValidString(value) &&
			(controls || !strings.ContainsFunc(value, func(r rune) bool { return r < 32 || r == 127 }))
	}
	out := PermissionRuleCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.list_permission_rules", QueryID: queryID,
		ConfigurationID: configurationID, At: inventory.At, ManagedOnly: *inventory.ManagedOnly, Items: []PermissionRuleItem{},
		Directories: append([]PermissionDirectory{}, inventory.Directories...), Errors: append([]PermissionSettingsError{}, inventory.Errors...),
		Limitations: []string{
			"这是当前原生查询报告的规则及来源；权限模式、沙箱、hooks、MCP 连接策略和逐次审批仍可能影响实际动作。",
			"热更新存在过渡期；同一规则可能同时带有两种忽略标记。清单和重复读取均不能保证下一次动作的最终权限。",
			"规则按原生保存的写法展示，不合并同名声明或推算最终允许范围；未知来源保留原值。",
			"额外工作目录的来源标签不能定位实际配置文件；当前 CLI 可将用户设置中的目录标为 localSettings。",
			"解析错误表示原生跳过了相应配置；此处只报告错误位置，不传出原始诊断正文。",
		}}
	type ruleKey struct{ source, behavior, rule string }
	states := map[ruleKey]int{}
	for _, item := range inventory.Items {
		if !slices.Contains([]string{"allow", "deny", "ask"}, item.Behavior) || !slices.Contains([]string{"persistent", "session", "readonly"}, item.Editability) ||
			item.NotInEffect == nil || !valid(item.Source, 300, false, false) || !valid(item.Rule, 1<<20, false, true) {
			return fail()
		}
		row := PermissionRuleItem{Behavior: item.Behavior, Source: item.Source, Rule: item.Rule, Editability: item.Editability, NotInEffect: *item.NotInEffect}
		out.Items = append(out.Items, row)
		flag := 1
		if row.NotInEffect {
			flag = 2
		}
		states[ruleKey{row.Source, row.Behavior, row.Rule}] |= flag
	}
	for _, directory := range out.Directories {
		if !valid(directory.Path, 8192, false, false) || !filepath.IsAbs(directory.Path) || !valid(directory.Source, 300, false, false) {
			return fail()
		}
	}
	for _, detail := range out.Errors {
		if !valid(detail.File, 8192, false, false) || !valid(detail.Field, 1024, true, false) {
			return fail()
		}
	}
	for _, state := range states {
		if state == 3 {
			out.ConflictCount++
		}
	}
	for i, row := range out.Items {
		out.Items[i].Conflicting = states[ruleKey{row.Source, row.Behavior, row.Rule}] == 3
	}
	// Native menu ordering is not a configuration change. Retain every row in
	// the report and sort copies only for the comparison digest.
	rows, directories, errors := []string{}, []string{}, []string{}
	for _, row := range out.Items {
		b, _ := json.Marshal(row)
		rows = append(rows, string(b))
	}
	for _, row := range out.Directories {
		b, _ := json.Marshal(row)
		directories = append(directories, string(b))
	}
	for _, row := range out.Errors {
		b, _ := json.Marshal(row)
		errors = append(errors, string(b))
	}
	slices.Sort(rows)
	slices.Sort(directories)
	slices.Sort(errors)
	canonical, err := json.Marshal(struct {
		Version     int      `json:"version"`
		Cwd         string   `json:"cwd"`
		ManagedOnly bool     `json:"managed_only"`
		Items       []string `json:"items"`
		Directories []string `json:"directories"`
		Errors      []string `json:"errors"`
	}{1, out.Cwd, out.ManagedOnly, rows, directories, errors})
	if err != nil {
		return fail()
	}
	out.SampleHash = fmt.Sprintf("%x", sha256.Sum256(canonical))
	return out, nil
}
