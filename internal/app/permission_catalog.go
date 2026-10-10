package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

func (a *Application) ReadSessionPermissionCatalog(ctx context.Context, id string) (runtime.PermissionRuleCatalog, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "claude" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return runtime.PermissionRuleCatalog{}, fmt.Errorf("此会话尚无原生权限规则读取入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	reader, supported := a.runtime.(runtime.PermissionCatalogReader)
	a.mu.Unlock()
	if err != nil {
		return runtime.PermissionRuleCatalog{}, err
	}
	if !supported {
		return runtime.PermissionRuleCatalog{}, fmt.Errorf("当前运行时尚未接通原生权限规则读取")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	scope := session.Scope
	scope.RecoveryPermission = session.Permission
	out, err := reader.ReadPermissionCatalog(check, scope)
	if err != nil {
		return runtime.PermissionRuleCatalog{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return runtime.PermissionRuleCatalog{}, fmt.Errorf("读取期间原生会话配置或输入归属已变化，请重新读取")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return runtime.PermissionRuleCatalog{}, err
	}
	if out.NativeID != session.Scope.NativeID || out.Cwd != session.Scope.Cwd || out.Source != "claude.list_permission_rules" ||
		out.QueryID == "" || out.ConfigurationID == "" || out.SampleHash == "" || out.Complete {
		return runtime.PermissionRuleCatalog{}, fmt.Errorf("权限规则来源或覆盖范围与当前查询契约不一致")
	}
	return queryCopy(out), nil
}
