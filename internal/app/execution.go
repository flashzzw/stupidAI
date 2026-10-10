package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

// CapabilityAsset registers an external source. A reference records a dependency;
// it does not install, enable, authorize, or claim native loading of that asset.
type CapabilityAsset struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Version    string   `json:"version"`
	Source     string   `json:"source"`
	Backends   []string `json:"backends"`
	Disabled   bool     `json:"disabled,omitempty"`
	CodexMCP   string   `json:"codex_mcp,omitempty"`
	CodexSkill string   `json:"codex_skill,omitempty"`
	Management string   `json:"management,omitempty"`
}

type SourceStamp struct {
	Path     string `json:"path"`
	Resolved string `json:"resolved_path"`
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type CapabilityReference struct {
	Asset  CapabilityAsset `json:"asset"`
	Source SourceStamp     `json:"source"`
}

type ExecutionSnapshot struct {
	ID                            string                             `json:"id"`
	SchemaVersion                 int                                `json:"schema_version"`
	SessionID                     string                             `json:"session_id"`
	CreatedAt                     string                             `json:"created_at"`
	Instance                      runtime.Instance                   `json:"instance"`
	CLIVersion                    string                             `json:"cli_version"`
	Route                         runtime.Route                      `json:"route"`
	Cwd                           string                             `json:"cwd"`
	StateNamespace                string                             `json:"state_namespace"`
	Permission                    string                             `json:"permission"`
	ProfileSource                 SourceStamp                        `json:"profile_source"`
	SettingsSource                *SourceStamp                       `json:"settings_source,omitempty"`
	ClaudeLocalSettings           *ClaudeLocalSettingsSnapshot       `json:"claude_local_settings,omitempty"`
	CodexUserHooks                *runtime.CodexUserHookSource       `json:"codex_user_hooks,omitempty"`
	CodexProjectHooks             *runtime.CodexProjectHookSources   `json:"codex_project_hooks,omitempty"`
	CodexConfigurationSources     *runtime.CodexConfigurationSources `json:"codex_configuration_sources,omitempty"`
	CapabilityRefs                []CapabilityReference              `json:"capability_refs"`
	Coverage                      string                             `json:"coverage"`
	MCPSelections                 []runtime.MCPSelection             `json:"mcp_selections,omitempty"`
	SkillSelections               []runtime.SkillSelection           `json:"skill_selections,omitempty"`
	PluginSelections              []runtime.PluginSelection          `json:"plugin_selections,omitempty"`
	ClaudeInstalledPluginSettings *InstalledPluginSettingsCandidate  `json:"claude_installed_plugin_settings,omitempty"`
	Revision                      int                                `json:"revision,omitempty"`
	PreviousID                    string                             `json:"previous_snapshot_id,omitempty"`
	RestoredFrom                  string                             `json:"restored_from_snapshot_id,omitempty"`
}

type SourceCheck struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type ExecutionRead struct {
	SourceCoverage     ExecutionSourceCoverage      `json:"source_coverage"`
	Snapshot           *ExecutionSnapshot           `json:"snapshot"`
	CheckedAt          string                       `json:"checked_at"`
	Status             string                       `json:"status"`
	Sources            []SourceCheck                `json:"sources"`
	BindingError       string                       `json:"binding_error,omitempty"`
	NativeEvidence     runtime.Evidence             `json:"native_evidence"`
	CapabilityEvidence []runtime.CapabilityEvidence `json:"capability_evidence,omitempty"`
	HookExecutions     []runtime.HookExecution      `json:"hook_executions,omitempty"`
	PluginEvidence     []runtime.PluginEvidence     `json:"plugin_evidence,omitempty"`
	SkillEvidence      []runtime.SkillEvidence      `json:"skill_evidence,omitempty"`
	PendingChange      *ExecutionChange             `json:"pending_change,omitempty"`
	Hold               bool                         `json:"hold"`
	History            []*ExecutionSnapshot         `json:"history,omitempty"`
	MCPLogins          []MCPLoginPlan               `json:"mcp_logins,omitempty"`
}

func validateAsset(asset CapabilityAsset) error {
	if asset.Management != "" && asset.Management != "external" && asset.Management != "managed" {
		return fmt.Errorf("能力管理方式仅支持 external/managed")
	}
	if !validID(asset.ID) || asset.Name == "" || len(asset.Name) > 200 || asset.Version == "" || len(asset.Version) > 200 || asset.Source == "" {
		return fmt.Errorf("能力资产需要有效的 id、name、version 与 source")
	}
	if asset.Kind != "skill" && asset.Kind != "mcp" && asset.Kind != "cli_plugin" {
		return fmt.Errorf("能力资产 kind 仅支持 skill、mcp、cli_plugin")
	}
	if len(asset.Backends) == 0 || len(asset.Backends) > 2 {
		return fmt.Errorf("能力资产需声明适用的 Codex/Claude")
	}
	seen := map[string]bool{}
	for _, backend := range asset.Backends {
		if (backend != "codex" && backend != "claude") || seen[backend] {
			return fmt.Errorf("能力资产 backends 无效或重复")
		}
		seen[backend] = true
	}
	if asset.CodexMCP != "" && (asset.Kind != "mcp" || !validID(asset.CodexMCP) || len(asset.Backends) != 1 || asset.Backends[0] != "codex") {
		return fmt.Errorf("codex_mcp 映射仅支持 Codex 专属 MCP，且必须是有效的原生服务名")
	}
	if asset.CodexSkill != "" && (asset.Kind != "skill" || !slices.Contains(asset.Backends, "codex") || !filepath.IsAbs(asset.CodexSkill) || filepath.Clean(asset.CodexSkill) != asset.CodexSkill || filepath.Base(asset.CodexSkill) != "SKILL.md" || strings.ContainsRune(asset.CodexSkill, '\x00') || asset.Management == "managed") {
		return fmt.Errorf("codex_skill 需要 Codex 外部 Skill 的绝对 SKILL.md 原生入口；不能用导入副本代替原生目录控制")
	}
	return nil
}

// Hash complete registered sources without storing their contents or secrets.
// Directory walks are bounded and refuse nested links/special files rather than
// presenting a partial tree digest as a complete resource version.
func stampSource(path string, optional bool) (SourceStamp, error) {
	s := SourceStamp{Path: path, Optional: optional}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		if optional {
			if _, missing := os.Lstat(path); os.IsNotExist(missing) {
				parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
				if parentErr == nil {
					s.Kind, s.Resolved = "absent", filepath.Join(parent, filepath.Base(path))
					return s, nil
				}
			}
		}
		return s, fmt.Errorf("来源无法读取: %s", path)
	}
	s.Resolved = actual
	info, err := os.Stat(actual)
	if err != nil {
		return s, fmt.Errorf("来源无法读取: %s", path)
	}
	s.Kind = "file"
	if info.IsDir() {
		s.Kind = "directory"
	}
	h := sha256.New()
	var bytes int64
	entries := 0
	err = filepath.WalkDir(actual, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 2048 {
			return fmt.Errorf("来源条目超过 2048")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("来源包含符号链接或特殊文件，请登记实际资源目录")
		}
		rel, err := filepath.Rel(actual, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%q %s\n", filepath.ToSlash(rel), info.Mode().String())
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		// Re-check the opened file; a path may have changed since WalkDir.
		opened, err := f.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			f.Close()
			return fmt.Errorf("读取期间来源已变化")
		}
		fileHash := sha256.New()
		n, err := io.Copy(fileHash, io.LimitReader(f, (32<<20)-bytes+1))
		f.Close()
		bytes += n
		if err != nil || bytes > 32<<20 {
			return fmt.Errorf("来源读取失败或总大小超过 32 MiB")
		}
		fmt.Fprintf(h, "%x\n", fileHash.Sum(nil))
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	s.SHA256 = hex.EncodeToString(h.Sum(nil))
	return s, nil
}

func (a *Application) captureExecution(session Session, ids []string, selections []CapabilitySelection) (*ExecutionSnapshot, error) {
	if session.Origin != "console" || (session.Scope.Instance.Kind != "codex" && session.Scope.Instance.Kind != "claude") {
		if len(ids) > 0 || len(selections) > 0 {
			return nil, fmt.Errorf("能力引用当前仅支持普通 Codex/Claude 会话")
		}
		return nil, nil
	}
	if len(ids) > 16 {
		return nil, fmt.Errorf("单次执行最多记录 16 项能力引用")
	}
	if len(selections) > 16 {
		return nil, fmt.Errorf("单次执行最多选配 16 项能力资产")
	}
	// A native selection also records its immutable source reference, without
	// changing the meaning of older reference-only requests.
	ids = append([]string{}, ids...)
	for _, selection := range selections {
		asset, _ := a.asset(selection.AssetID)
		if selection.HookReview != nil && (session.Scope.Instance.Kind != "codex" || asset.Kind != "cli_plugin") {
			return nil, fmt.Errorf("hook 信任选择只适用于 Codex 插件")
		}
		if selection.Version != "" {
			asset, ok := a.asset(selection.AssetID)
			if !ok || asset.Version != selection.Version {
				return nil, fmt.Errorf("能力 %s 的登记版本已变化，请重新选择", selection.AssetID)
			}
		}
		if !slices.Contains(ids, selection.AssetID) {
			ids = append(ids, selection.AssetID)
		}
	}
	if len(ids) > 16 {
		return nil, fmt.Errorf("引用和选配合计不能超过 16 项")
	}
	filename := "config.toml"
	optional := session.Scope.Instance.Kind == "claude"
	if optional {
		filename = "settings.json"
	}
	profile, err := stampSource(filepath.Join(session.Scope.Instance.Profile, filename), optional)
	if err != nil {
		return nil, err
	}
	snapshot := &ExecutionSnapshot{ID: uuid.NewString(), SchemaVersion: 1, SessionID: session.ID, CreatedAt: now(),
		Instance: session.Scope.Instance, Route: session.Scope.Route, Cwd: session.Scope.Cwd,
		StateNamespace: session.Scope.StateNamespace, Permission: session.Permission, ProfileSource: profile,
		CapabilityRefs: []CapabilityReference{}, Coverage: "recorded_sources_only"}
	if session.Scope.Instance.Kind == "codex" {
		snapshot.CodexConfigurationSources, err = a.captureCodexConfigurationSources(session.Scope)
		if err != nil {
			return nil, err
		}
		snapshot.CodexUserHooks, err = runtime.ReadCodexUserHooks(session.Scope)
		if err != nil {
			return nil, err
		}
		snapshot.CodexProjectHooks, err = a.captureCodexProjectHooks(session.Scope)
		if err != nil {
			return nil, err
		}
	}
	if session.Scope.Instance.Kind == "claude" {
		local, err := captureClaudeLocalSettings(session.Scope)
		if err != nil {
			return nil, err
		}
		snapshot.ClaudeLocalSettings = local
	}
	if session.Scope.Instance.SettingsFile != "" {
		source, err := stampSource(session.Scope.Instance.SettingsFile, false)
		if err != nil {
			return nil, err
		}
		snapshot.SettingsSource = &source
	}
	for _, instance := range a.instances {
		if instance.ID == session.Scope.Instance.ID {
			snapshot.CLIVersion = instance.Description.Version
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		asset, ok := a.asset(id)
		if !ok || seen[id] || asset.Disabled || !slices.Contains(asset.Backends, session.Scope.Instance.Kind) {
			return nil, fmt.Errorf("能力引用 %s 未登记、已停用、重复或不适用", id)
		}
		if err := validateAsset(asset); err != nil {
			return nil, err
		}
		seen[id] = true
		source, err := stampSource(asset.Source, false)
		if err != nil {
			return nil, err
		}
		if management(asset) == "managed" {
			entry, ok := a.state.CapabilityLibrary[id]
			if !ok || len(entry.Versions) == 0 || entry.Versions[len(entry.Versions)-1].Source == nil || *entry.Versions[len(entry.Versions)-1].Source != source {
				return nil, fmt.Errorf("平台管理的资产 %s 固定版本已变化或无法验证", id)
			}
		}
		snapshot.CapabilityRefs = append(snapshot.CapabilityRefs, CapabilityReference{Asset: asset, Source: source})
	}
	servers, selectedIDs := map[string]bool{}, map[string]bool{}
	skillKeys := map[string]bool{}
	for _, selection := range selections {
		asset, _ := a.asset(selection.AssetID)
		if asset.Kind == "cli_plugin" {
			if selectedIDs[selection.AssetID] {
				return nil, fmt.Errorf("插件选择重复")
			}
			selectedIDs[selection.AssetID] = true
			var ref CapabilityReference
			for _, r := range snapshot.CapabilityRefs {
				if r.Asset.ID == selection.AssetID {
					ref = r
				}
			}
			selectPlugin := claudePluginSelection
			if session.Scope.Instance.Kind == "codex" {
				selectPlugin = codexPluginSelection
			}
			binding, err := selectPlugin(session.Scope, ref, selection.Mode)
			if err != nil {
				return nil, err
			}
			if session.Scope.Instance.Kind == "codex" {
				if err = a.bindCodexHookReview(session.Scope, ref, selection, &binding); err != nil {
					return nil, err
				}
			}
			if slices.ContainsFunc(snapshot.PluginSelections, func(p runtime.PluginSelection) bool {
				return p.Name == binding.Name || p.SourcePath == binding.SourcePath
			}) {
				return nil, fmt.Errorf("所选插件共享原生名称或来源，不能独立启停")
			}
			snapshot.PluginSelections = append(snapshot.PluginSelections, binding)
			continue
		}
		if asset.Kind == "skill" {
			if selectedIDs[selection.AssetID] {
				return nil, fmt.Errorf("Skill 选择重复")
			}
			selectedIDs[selection.AssetID] = true
			var ref CapabilityReference
			for _, candidate := range snapshot.CapabilityRefs {
				if candidate.Asset.ID == selection.AssetID {
					ref = candidate
				}
			}
			if session.Scope.Instance.Kind == "codex" {
				if ref.Asset.CodexSkill != "" {
					binding, err := codexDirectorySkillSelection(session.Scope, ref, selection.Mode)
					if err != nil {
						return nil, err
					}
					if skillKeys[binding.NativeKey] {
						return nil, fmt.Errorf("所选 Skill 共享原生入口")
					}
					skillKeys[binding.NativeKey] = true
					snapshot.SkillSelections = append(snapshot.SkillSelections, binding)
					continue
				}
				binding, err := codexSkillAssetSelection(session.Scope, ref, selection.Mode)
				if err != nil {
					return nil, err
				}
				snapshot.PluginSelections = append(snapshot.PluginSelections, binding)
				continue
			}
			binding, err := claudeSkillSelection(session.Scope, ref, selection.Mode, a.config.StateDir)
			if err != nil {
				return nil, err
			}
			if skillKeys[binding.NativeKey] {
				return nil, fmt.Errorf("所选 Skill 共享原生目录键，不能分别启停")
			}
			skillKeys[binding.NativeKey] = true
			snapshot.SkillSelections = append(snapshot.SkillSelections, binding)
			continue
		}
		if session.Scope.Instance.Kind == "claude" {
			if selectedIDs[selection.AssetID] {
				return nil, fmt.Errorf("MCP 选择重复")
			}
			selectedIDs[selection.AssetID] = true
			var ref CapabilityReference
			for _, candidate := range snapshot.CapabilityRefs {
				if candidate.Asset.ID == selection.AssetID {
					ref = candidate
				}
			}
			bindings, err := claudeMCPSelections(ref, selection.Mode)
			if err != nil {
				return nil, err
			}
			for _, binding := range bindings {
				if servers[binding.Server] {
					return nil, fmt.Errorf("所选资产包含同名 MCP：%s", binding.Server)
				}
				servers[binding.Server] = true
				snapshot.MCPSelections = append(snapshot.MCPSelections, binding)
			}
			if len(snapshot.MCPSelections) > 16 {
				return nil, fmt.Errorf("单次执行最多选择 16 个 MCP 服务")
			}
			continue
		}
		if asset.CodexMCP == "" || servers[asset.CodexMCP] || selectedIDs[selection.AssetID] || (selection.Mode != "enabled" && selection.Mode != "disabled") {
			return nil, fmt.Errorf("原生 MCP 选择必须有唯一的已登记映射和 enabled/disabled 模式")
		}
		var source SourceStamp
		for _, ref := range snapshot.CapabilityRefs {
			if ref.Asset.ID == selection.AssetID {
				source = ref.Source
			}
		}
		if source.Kind != "file" || source.Resolved != profile.Resolved {
			return nil, fmt.Errorf("原生 MCP 选配首片要求来源是当前 Codex profile 的 config.toml")
		}
		servers[asset.CodexMCP], selectedIDs[selection.AssetID] = true, true
		snapshot.MCPSelections = append(snapshot.MCPSelections, runtime.MCPSelection{AssetID: selection.AssetID, Server: asset.CodexMCP, Mode: selection.Mode, SourcePath: source.Resolved})
	}
	return queryCopy(snapshot), nil
}

func (a *Application) asset(id string) (CapabilityAsset, bool) {
	if entry, ok := a.state.CapabilityLibrary[id]; ok {
		if entry.Removed {
			return CapabilityAsset{}, false
		}
		return latestCapability(entry), true
	}
	for _, asset := range a.config.CapabilityAssets {
		if asset.ID == id {
			return asset, true
		}
	}
	return CapabilityAsset{}, false
}

func checkExecutionSources(snapshot *ExecutionSnapshot) []SourceCheck {
	checks := []SourceCheck{}
	if snapshot == nil {
		return checks
	}
	check := func(group, name string, saved SourceStamp) {
		current, err := stampSource(saved.Path, saved.Optional)
		item := SourceCheck{Group: group, Name: name, Path: saved.Path, Status: "unchanged"}
		if err != nil {
			item.Status, item.Detail = "unavailable", err.Error()
		} else if current != saved {
			item.Status, item.Detail = "changed", "来源内容、路径或文件权限已变化"
		}
		checks = append(checks, item)
	}
	check("profile", "CLI 配置文件", snapshot.ProfileSource)
	if snapshot.SettingsSource != nil {
		check("settings", "Claude 自定义 settings", *snapshot.SettingsSource)
	}
	for _, ref := range snapshot.CapabilityRefs {
		check("capability_refs", ref.Asset.Name, ref.Source)
	}
	appendGroup := func(group string, items []SourceCheck) {
		for _, item := range items {
			item.Group = group
			checks = append(checks, item)
		}
	}
	appendGroup("claude_local_settings", checkClaudeLocalSettings(snapshot))
	appendGroup("claude_installed_settings", checkClaudeInstalledSettings(snapshot))
	appendGroup("codex_user_hooks", checkCodexUserHooks(snapshot))
	appendGroup("codex_project_hooks", checkCodexProjectHooks(snapshot))
	appendGroup("codex_configuration_sources", checkCodexConfigurationSources(snapshot))
	return checks
}

func verifyExecutionBinding(session Session) error {
	if digest(session.Scope.ClaudeInstalledSettings) != digest(claudeInstalledSettings(session.ExecutionSnapshot)) {
		return fmt.Errorf("Claude 已安装插件设置与原快照绑定不一致")
	}
	if digest(session.Scope.CodexConfigurationSources) != digest(codexConfigurationSources(session.ExecutionSnapshot)) {
		return fmt.Errorf("Codex 配置来源版本与原快照绑定不一致")
	}
	if digest(session.Scope.CodexProjectHooks) != digest(codexProjectHooks(session.ExecutionSnapshot)) {
		return fmt.Errorf("Codex 项目 hook 文件版本与原快照绑定不一致")
	}
	if digest(session.Scope.CodexUserHooks) != digest(codexUserHooks(session.ExecutionSnapshot)) {
		return fmt.Errorf("Codex 用户 hook 文件版本与原快照绑定不一致")
	}
	if !slices.Equal(session.Scope.ClaudeSettingsSources, claudeNativeSettingsSources(session.ExecutionSnapshot)) {
		return fmt.Errorf("Claude 本地设置文件版本与原生绑定不一致")
	}
	if session.Scope.SettingsGuardVersion != claudeSettingsGuardVersion(session.ExecutionSnapshot) {
		return fmt.Errorf("Claude 本地设置阻止机制与原快照绑定不一致")
	}
	s := session.ExecutionSnapshot
	if s == nil { // Historical records must not acquire invented startup evidence.
		return nil
	}
	validSchema := s.SchemaVersion == 1 && s.ClaudeInstalledPluginSettings == nil || installedExecutionSnapshot(s) && s.ClaudeInstalledPluginSettings.Binding.Artifact.ID == s.ID
	if s.SessionID != session.ID || s.ID == "" || !validSchema || digest(s.Instance) != digest(session.Scope.Instance) || s.Route != session.Scope.Route || s.Cwd != session.Scope.Cwd || s.Permission != session.Permission || s.StateNamespace != session.Scope.StateNamespace || digest(s.MCPSelections) != digest(session.Scope.MCPSelections) || digest(s.SkillSelections) != digest(session.Scope.SkillSelections) || !runtime.EqualPluginSelections(s.PluginSelections, session.Scope.PluginSelections) {
		return fmt.Errorf("执行快照与原会话绑定不一致")
	}
	return nil
}

func verifyExecutionSources(session Session) error {
	if err := verifyExecutionBinding(session); err != nil {
		return err
	}
	for _, source := range checkExecutionSources(session.ExecutionSnapshot) {
		if source.Status != "unchanged" {
			return fmt.Errorf("执行快照来源待核对（%s）：%s", source.Name, source.Detail)
		}
	}
	return nil
}

// ReadExecution does file I/O after releasing the state lock. It checks the
// copied snapshot only and never rewrites a historical snapshot or starts a CLI.
func (a *Application) ReadExecution(id string) (ExecutionRead, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	session = queryCopy(session)
	logins := []MCPLoginPlan{}
	for _, receipt := range a.state.Commands {
		if receipt.SessionID == id && receipt.MCPLogin != nil {
			logins = append(logins, *receipt.MCPLogin)
		}
	}
	instance, hasInstance := configuredInstance(a.config, session.Scope.Instance.ID)
	route, hasRoute := configuredRoute(a.config, session.Scope.Route.ID)
	a.mu.Unlock()
	if !ok {
		return ExecutionRead{}, fmt.Errorf("会话不存在")
	}
	out := ExecutionRead{Snapshot: session.ExecutionSnapshot, CheckedAt: now(), Status: "unrecorded", Sources: []SourceCheck{}, NativeEvidence: session.View.Evidence}
	out.SourceCoverage = executionSourceCoverage(out.Snapshot)
	out.MCPLogins = logins
	out.CapabilityEvidence = session.View.CapabilityEvidence
	out.SkillEvidence = session.View.SkillEvidence
	out.PluginEvidence = session.View.PluginEvidence
	out.HookExecutions = session.View.HookExecutions
	out.PendingChange = session.PendingExecutionChange
	out.Hold, out.History = session.ExecutionHold, session.ExecutionHistory
	if out.Snapshot == nil {
		return out, nil
	}
	out.Status = "unchanged"
	out.Sources = checkExecutionSources(out.Snapshot)
	for _, source := range out.Sources {
		if source.Status != "unchanged" {
			out.Status = "drifted"
		}
	}
	if err := verifyExecutionBinding(session); err != nil {
		out.BindingError = err.Error()
	} else if !hasInstance || !hasRoute || digest(instance) != digest(session.Scope.Instance) || route != session.Scope.Route {
		out.BindingError = "当前登记的执行配置与原绑定不同"
	} else if err := runtime.VerifyStateNamespace(session.Scope); err != nil {
		out.BindingError = err.Error()
	} else if err := runtime.VerifyCredentialStamp(session.Scope); err != nil {
		out.BindingError = err.Error()
	}
	if out.BindingError != "" {
		out.Status = "drifted"
	}
	return out, nil
}
