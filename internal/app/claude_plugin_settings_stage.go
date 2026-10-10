package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"codex-netcap/internal/runtime"
)

type PluginSettingsStageRequest struct {
	CommandID      string `json:"command_id"`
	BaseSnapshotID string `json:"base_snapshot_id"`
	PluginSettingsPreviewRequest
}

// The observation records a proposal, not a loaded plugin or a reusable private
// settings file. The native preview has already released its temporary file.
type InstalledPluginSettingsCandidate struct {
	Binding         *runtime.ClaudeInstalledSettings `json:"binding,omitempty"`
	Preview         InstalledPluginSettingsPreview   `json:"preview"`
	Resource        SourceStamp                      `json:"declaration_resource"`
	StartupSettings runtime.ClaudeStartupSettings    `json:"startup_settings"`
}

const installedPluginSettingsBoundary = "保存仅登记候选，当前会话继续使用原配置。准备应用只读核对来源、原历史与空闲状态；明确应用时关闭自有查询，按原 ID 使用同目录的派生设置，核对实际安装来源及全部组件后晋升版本。候选启动后失败或丢回执保持未知，不自动回滚或重放。"

func checkApplicableExecutionCandidate(change *ExecutionChange) error {
	if change != nil && change.Snapshot != nil && (change.Snapshot.SchemaVersion != 1 || change.Snapshot.ClaudeInstalledPluginSettings != nil) {
		if !installedExecutionSnapshot(change.Snapshot) || change.Snapshot.ClaudeInstalledPluginSettings.Binding.Artifact.ID != change.Snapshot.ID {
			return fmt.Errorf("此候选格式尚无可执行的来源和文件绑定；请撤销后重新核对保存")
		}
	}
	return nil
}

type pluginSettingsStage struct {
	hash    string
	done    chan struct{}
	receipt CommandReceipt
	err     error
}

// This entry accepts only a selection and base IDs. Native proof always comes
// from a fresh server-side preview, outside the application mutex. Concurrent
// retries join that request; durable retries return the recorded receipt.
func (a *Application) StageSessionPluginSettings(ctx context.Context, id string, request PluginSettingsStageRequest) (receipt CommandReceipt, err error) {
	if !validID(request.CommandID) || request.SourceKey == "" || len(request.SourceKey) > 201 || len(request.SourceVersion) != 64 || request.Mode != "enabled" && request.Mode != "disabled" {
		return receipt, fmt.Errorf("保存插件设置需要 command_id、完整来源键、登记来源版本及 enabled/disabled 模式")
	}
	hash := digest(struct {
		Kind      string `json:"kind"`
		SessionID string `json:"session_id"`
		PluginSettingsStageRequest
	}{"stage_installed_plugin_settings", id, request})
	a.mu.Lock()
	session, saved, err := a.pluginSettingsStageSession(id, request, hash)
	if err != nil || saved != nil {
		a.mu.Unlock()
		if saved != nil {
			return *saved, nil
		}
		return receipt, err
	}
	if pending := a.pluginSettingsStages[request.CommandID]; pending != nil {
		a.mu.Unlock()
		if pending.hash != hash {
			return receipt, fmt.Errorf("command_id 已用于其他内容")
		}
		select {
		case <-pending.done:
			return queryCopy(pending.receipt), pending.err
		case <-ctx.Done():
			return receipt, ctx.Err()
		case <-a.ctx.Done():
			return receipt, fmt.Errorf("控制台正在关闭")
		}
	}
	if a.pluginSettingsStages == nil {
		a.pluginSettingsStages = map[string]*pluginSettingsStage{}
	}
	pending := &pluginSettingsStage{hash: hash, done: make(chan struct{})}
	a.pluginSettingsStages[request.CommandID] = pending
	a.wg.Add(1)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		pending.receipt, pending.err = queryCopy(receipt), err
		delete(a.pluginSettingsStages, request.CommandID)
		close(pending.done)
		a.mu.Unlock()
		a.wg.Done()
	}()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	defer cancel()
	return a.stagePluginSettings(ctx, session, request, hash)
}

// Called under a.mu, both before native observation and just before commit.
func (a *Application) pluginSettingsStageSession(id string, request PluginSettingsStageRequest, hash string) (Session, *CommandReceipt, error) {
	if a.closed {
		return Session{}, nil, fmt.Errorf("控制台正在关闭")
	}
	if a.fatal != nil {
		return Session{}, nil, a.fatal
	}
	if saved, ok := a.state.Commands[request.CommandID]; ok {
		if saved.Hash != hash {
			return Session{}, nil, fmt.Errorf("command_id 已用于其他内容")
		}
		return Session{}, &saved, nil
	}
	session, exists := a.state.Sessions[id]
	if !exists || session.Scope.Instance.Kind != "claude" {
		return Session{}, nil, fmt.Errorf("此会话尚无 Claude 插件设置保存入口")
	}
	// A new installed-plugin candidate never replaces an existing proposal.
	if err := checkChangeBase(session, ExecutionChangeRequest{BaseSnapshotID: request.BaseSnapshotID}); err != nil {
		return Session{}, nil, err
	}
	if err := verifyExecutionSources(session); err != nil {
		return Session{}, nil, err
	}
	if err := a.verifyRouteIdentity(session); err != nil {
		return Session{}, nil, err
	}
	return queryCopy(session), nil, nil
}

func (a *Application) stagePluginSettings(ctx context.Context, session Session, request PluginSettingsStageRequest, hash string) (CommandReceipt, error) {
	var receipt CommandReceipt
	before := readInstalledPluginCatalog(ctx, session)
	entry, err := selectedInstalledPlugin(before, request.PluginSettingsPreviewRequest, session.Scope.Cwd)
	if err != nil {
		return receipt, err
	}
	resource, err := stampSource(entry.DeclaredPath, false)
	if err != nil {
		return receipt, err
	}
	preview, err := a.PreviewSessionPluginSettings(ctx, session.ID, request.PluginSettingsPreviewRequest)
	if err != nil {
		return receipt, err
	}
	v := preview.Validation
	if !preview.SourcesStable || len(preview.Problems) != 0 || preview.Cleanup.State != "released" || len(preview.Cleanup.Issues) != 0 ||
		v == nil || !v.SchemaValid || !v.ProcessStopped || !v.FileHolderObserved || v.Preservation == nil || !v.Preservation.Matches ||
		len(v.PluginSettings) == 0 || slices.ContainsFunc(v.PluginSettings, func(p runtime.ClaudePluginSettingObservation) bool { return !p.Matches }) {
		return receipt, fmt.Errorf("本次原生设置、目标值、来源保留或临时文件回收未核对通过，未保存待应用方案")
	}
	startup, err := runtime.ReadClaudeStartupSettings(ctx, session.Scope)
	if err != nil {
		return receipt, err
	}
	if preview.StartupSourceVersion == "" || startup.Fingerprint() != preview.StartupSourceVersion {
		return receipt, fmt.Errorf("原生核对后启动设置来源变化，未保存待应用方案；请重新核对")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, saved, err := a.pluginSettingsStageSession(session.ID, request, hash)
	if err != nil {
		return receipt, err
	}
	if saved != nil {
		return *saved, nil
	}
	if current.Generation != session.Generation || current.ControlMode != session.ControlMode || digest(current.Scope) != digest(session.Scope) {
		return receipt, fmt.Errorf("保存期间会话绑定或输入归属变化，未保存待应用方案")
	}
	after := readInstalledPluginCatalog(ctx, current)
	checked, err := selectedInstalledPlugin(after, request.PluginSettingsPreviewRequest, current.Scope.Cwd)
	if err != nil {
		return receipt, err
	}
	actual, err := stampSource(entry.DeclaredPath, false)
	if err != nil || actual != resource || checked != entry || preview.Entry != entry {
		return receipt, fmt.Errorf("保存期间插件声明或资源目录变化，未保存待应用方案")
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	candidate := queryCopy(current.ExecutionSnapshot)
	// Older readers also reject a format they cannot rebuild, rather than
	// silently dropping this selection and reporting a successful application.
	candidate.SchemaVersion = 2
	candidate.RestoredFrom = ""
	binding, previous, err := installedSettingsCandidate(current, entry, resource, preview, startup, request.Mode)
	if err != nil {
		return receipt, err
	}
	candidate.ClaudeInstalledPluginSettings = &InstalledPluginSettingsCandidate{Binding: binding, Preview: preview, Resource: resource, StartupSettings: startup}
	change := &ExecutionChange{Snapshot: candidate, InstalledPluginChange: true, Boundary: installedPluginSettingsBoundary,
		Changes: []CapabilitySelectionChange{{Kind: "installed_cli_plugin", Name: entry.Name, SourceKey: entry.SourceKey, Before: previous, After: request.Mode, AfterVersion: entry.RegisteredVersion}}}
	receipt = CommandReceipt{ID: request.CommandID, Hash: hash, SessionID: session.ID}
	return a.persistExecutionChange(clone(a.state), current, receipt, change)
}

func installedSettingsCandidate(session Session, entry InstalledPluginEntry, resource SourceStamp, preview InstalledPluginSettingsPreview, startup runtime.ClaudeStartupSettings, mode string) (*runtime.ClaudeInstalledSettings, string, error) {
	ref := CapabilityReference{Asset: CapabilityAsset{Name: entry.Name, Kind: "cli_plugin", Version: entry.ManifestVersion, Source: entry.DeclaredPath, Backends: []string{"claude"}}, Source: resource}
	selection, err := claudePluginResource(ref, mode)
	if err != nil {
		return nil, "", err
	}
	selection.NativeSource = entry.SourceKey
	if selection.Name != entry.Name || selection.Version != entry.ManifestVersion || selection.SourcePath != entry.ResolvedPath {
		return nil, "", fmt.Errorf("插件资源版本与本地安装声明不一致")
	}
	binding := &runtime.ClaudeInstalledSettings{Version: 1, SourceFiles: preview.SourceFiles, StartupFiles: startup.Files, StartupSourceVersion: startup.Fingerprint(), Selections: []runtime.PluginSelection{}}
	if session.Scope.ClaudeInstalledSettings != nil {
		binding.Selections = queryCopy(session.Scope.ClaudeInstalledSettings.Selections)
	}
	previous, found := "unrecorded", false
	for i, p := range binding.Selections {
		if p.NativeSource == entry.SourceKey {
			previous, found = p.Mode, true
			binding.Selections[i] = selection
		}
	}
	if !found {
		binding.Selections = append(binding.Selections, selection)
	}
	if previous == mode {
		return nil, previous, fmt.Errorf("插件设置与当前已保存的选择相同")
	}
	slices.SortFunc(binding.Selections, func(a, b runtime.PluginSelection) int { return strings.Compare(a.NativeSource, b.NativeSource) })
	return binding, previous, nil
}
