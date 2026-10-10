package app

import (
	"codex-netcap/internal/runtime"
	"context"
	"fmt"
	"path/filepath"
)

type PluginSettingsPreviewRequest struct {
	SourceKey     string `json:"source_key"`
	Mode          string `json:"mode"`
	SourceVersion string `json:"source_version"`
}

type InstalledPluginSettingsPreview struct {
	runtime.ClaudePluginSettingsPreview
	Entry         InstalledPluginEntry `json:"entry"`
	SourceVersion string               `json:"source_version"`
	SourceFiles   []runtime.FileSource `json:"source_files"`
}

func (a *Application) PreviewSessionPluginSettings(ctx context.Context, id string, request PluginSettingsPreviewRequest) (InstalledPluginSettingsPreview, error) {
	var out InstalledPluginSettingsPreview
	if request.SourceKey == "" || len(request.SourceKey) > 201 || len(request.SourceVersion) != 64 || request.Mode != "enabled" && request.Mode != "disabled" {
		return out, fmt.Errorf("插件设置预览需要完整来源键、登记来源版本及 enabled/disabled 模式")
	}
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "claude" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return out, fmt.Errorf("此会话尚无 Claude 插件设置预览入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	a.mu.Unlock()
	if err != nil {
		return out, err
	}
	before := readInstalledPluginCatalog(ctx, session)
	entry, err := selectedInstalledPlugin(before, request, session.Scope.Cwd)
	if err != nil {
		return out, err
	}
	out.Entry, out.SourceVersion, out.SourceFiles = entry, before.SourceVersion, before.SourceFiles
	out.ClaudePluginSettingsPreview = runtime.PreviewClaudePluginSettings(ctx, a.config.StateDir, session.Scope,
		runtime.ClaudePluginSetting{SourceKey: request.SourceKey, Enabled: request.Mode == "enabled"})
	if session.ExecutionSnapshot != nil {
		out.SnapshotID = session.ExecutionSnapshot.ID
	}
	after := readInstalledPluginCatalog(ctx, session)
	if len(after.Errors) != 0 || before.SourceVersion != after.SourceVersion {
		out.SourcesStable = false
		out.Problems = append(out.Problems, "预览期间插件登记或声明来源变化，请重新读取安装登记")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return InstalledPluginSettingsPreview{}, fmt.Errorf("预览期间会话绑定或输入归属变化，请重新读取；临时文件已按使用状态核查回收")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return InstalledPluginSettingsPreview{}, err
	}
	return out, nil
}

func selectedInstalledPlugin(catalog InstalledPluginCatalog, request PluginSettingsPreviewRequest, cwd string) (InstalledPluginEntry, error) {
	if len(catalog.Errors) != 0 || catalog.SourceVersion != request.SourceVersion {
		return InstalledPluginEntry{}, fmt.Errorf("插件登记或声明来源变化或读取不完整，请重新读取安装登记")
	}
	var entries []InstalledPluginEntry
	for _, entry := range catalog.Entries {
		if entry.SourceKey == request.SourceKey {
			entries = append(entries, entry)
		}
	}
	if len(entries) != 1 || entries[0].Status != "local_declaration" ||
		entries[0].Scope != "user" && filepath.Clean(entries[0].ProjectPath) != cwd {
		return InstalledPluginEntry{}, fmt.Errorf("此来源没有唯一、与当前项目匹配的本地声明，暂不能核对其设置")
	}
	return entries[0], nil
}
