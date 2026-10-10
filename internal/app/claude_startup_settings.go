package app

import (
	"codex-netcap/internal/runtime"
	"context"
	"fmt"
)

func (a *Application) ReadSessionClaudeStartupSettings(ctx context.Context, id string) (runtime.ClaudeStartupSettings, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "claude" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return runtime.ClaudeStartupSettings{}, fmt.Errorf("此会话尚无 Claude 设置来源读取入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	a.mu.Unlock()
	if err != nil {
		return runtime.ClaudeStartupSettings{}, err
	}
	out, err := runtime.ReadClaudeStartupSettings(ctx, session.Scope)
	if err != nil {
		return runtime.ClaudeStartupSettings{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return runtime.ClaudeStartupSettings{}, fmt.Errorf("读取期间会话绑定或输入归属变化，请重新读取设置来源")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return runtime.ClaudeStartupSettings{}, err
	}
	out.NativeID = session.Scope.NativeID
	if session.ExecutionSnapshot != nil {
		out.SnapshotID = session.ExecutionSnapshot.ID
	}
	return out, nil
}
