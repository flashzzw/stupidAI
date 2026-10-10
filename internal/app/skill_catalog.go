package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

func (a *Application) ReadSessionSkillCatalog(ctx context.Context, id string) (SessionSkillCatalog, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "claude" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return SessionSkillCatalog{}, fmt.Errorf("此会话尚无原生 Skill 来源清单入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	reader, supported := a.runtime.(runtime.SkillCatalogReader)
	a.mu.Unlock()
	if err != nil {
		return SessionSkillCatalog{}, err
	}
	if !supported {
		return SessionSkillCatalog{}, fmt.Errorf("当前运行时尚未接通原生 Skill 来源清单")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := reader.ReadSkillCatalog(check, session.Scope)
	if err != nil {
		return SessionSkillCatalog{}, err
	}
	if out.NativeID != session.Scope.NativeID || out.Cwd != session.Scope.Cwd || out.Source != "claude.sdk.current_query" || out.Complete {
		return SessionSkillCatalog{}, fmt.Errorf("Skill 清单来源或覆盖范围与当前查询契约不一致")
	}
	directories := discoverClaudeSkillDirectories(check, session)
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return SessionSkillCatalog{}, fmt.Errorf("读取期间原生会话配置或输入归属已变化，请重新读取")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return SessionSkillCatalog{}, err
	}
	return SessionSkillCatalog{SkillSourceCatalog: queryCopy(out), Directories: directories}, nil
}
