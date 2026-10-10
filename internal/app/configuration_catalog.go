package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

func (a *Application) ReadSessionConfigurationCatalog(ctx context.Context, id string) (runtime.ConfigurationCatalog, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "codex" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return runtime.ConfigurationCatalog{}, fmt.Errorf("此会话尚无 Codex 配置来源读取入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	reader, supported := a.runtime.(runtime.ConfigurationCatalogReader)
	a.mu.Unlock()
	if err != nil {
		return runtime.ConfigurationCatalog{}, err
	}
	if !supported {
		return runtime.ConfigurationCatalog{}, fmt.Errorf("当前运行时尚未接通配置来源读取")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := reader.ReadConfigurationCatalog(check, session.Scope)
	if err != nil {
		return runtime.ConfigurationCatalog{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return runtime.ConfigurationCatalog{}, fmt.Errorf("读取期间会话配置或输入归属已变化，请重新读取")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return runtime.ConfigurationCatalog{}, err
	}
	if out.NativeID != session.Scope.NativeID || out.Cwd != session.Scope.Cwd || out.Source != "codex.config/read+configRequirements/read" || out.Scope != "disk_configuration" || out.Complete || out.At == "" {
		return runtime.ConfigurationCatalog{}, fmt.Errorf("配置来源响应与当前查询的身份或覆盖范围不符")
	}
	var saved *runtime.ConfigurationSettings
	if sources := session.Scope.CodexConfigurationSources; sources != nil {
		saved = sources.Settings
	}
	difference := runtime.CompareConfigurationSettings(saved, out.Settings)
	if session.ExecutionSnapshot != nil {
		difference.BaseSnapshotID = session.ExecutionSnapshot.ID
	}
	out.SettingsDifference = &difference
	return queryCopy(out), nil
}
