package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

func (a *Application) ReadSessionHookCatalog(ctx context.Context, id string) (runtime.HookSourceCatalog, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || (session.Scope.Instance.Kind != "claude" && session.Scope.Instance.Kind != "codex") || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return runtime.HookSourceCatalog{}, fmt.Errorf("此会话尚无原生 hook 来源清单入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	reader, supported := a.runtime.(runtime.HookCatalogReader)
	a.mu.Unlock()
	if err != nil {
		return runtime.HookSourceCatalog{}, err
	}
	if !supported {
		return runtime.HookSourceCatalog{}, fmt.Errorf("当前运行时尚未接通原生 hook 来源清单")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := reader.ReadHookCatalog(check, session.Scope)
	if err != nil {
		return runtime.HookSourceCatalog{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return runtime.HookSourceCatalog{}, fmt.Errorf("读取期间原生会话配置或输入归属已变化，请重新读取")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return runtime.HookSourceCatalog{}, err
	}
	expected := "claude.native.hooks_menu"
	if session.Scope.Instance.Kind == "codex" {
		expected = "codex.hooks/list"
	}
	if out.NativeID != session.Scope.NativeID || out.Cwd != session.Scope.Cwd || out.Source != expected || out.Complete {
		return runtime.HookSourceCatalog{}, fmt.Errorf("hook 清单来源或覆盖范围与当前查询契约不一致")
	}
	if session.Scope.Instance.Kind == "codex" && (out.Scope != "directory" || out.PolicyReported) {
		return runtime.HookSourceCatalog{}, fmt.Errorf("Codex hook 来源发现不能代替当前会话或完整策略核对")
	}
	return queryCopy(out), nil
}
