package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

// ReadSessionSkillContext is a read-only historical sample; it cannot promote a
// capability selection, modify input ownership or certify method use.
func (a *Application) ReadSessionSkillContext(ctx context.Context, id string) (runtime.SkillContext, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "codex" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return runtime.SkillContext{}, fmt.Errorf("此会话尚无 Skill 上下文读取入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	reader, supported := a.runtime.(runtime.SkillContextReader)
	a.mu.Unlock()
	if err != nil {
		return runtime.SkillContext{}, err
	}
	if !supported {
		return runtime.SkillContext{}, fmt.Errorf("当前运行时尚未接通 Skill 上下文读取")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	scope := session.Scope
	scope.NoAutoResume = true
	out, err := reader.ReadSkillContext(check, scope)
	if err != nil {
		return runtime.SkillContext{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return runtime.SkillContext{}, fmt.Errorf("查询期间会话绑定或输入归属已变化，请重新读取 Skill 上下文")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return runtime.SkillContext{}, err
	}
	if out.NativeID != session.Scope.NativeID || out.Cwd != session.Scope.Cwd || out.Source != "codex.rollout.skill_context" || !out.Complete {
		return runtime.SkillContext{}, fmt.Errorf("Skill 上下文结果与查询来源不一致")
	}
	out = queryCopy(out)
	a.attachSkillInputEvidence(current, &out)
	return out, nil
}
