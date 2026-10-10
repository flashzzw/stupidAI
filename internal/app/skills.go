package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

// ReadSessionSkills uses the saved binding only. It does not persist discovery
// as execution evidence, rewrite a snapshot, or change input ownership.
func (a *Application) ReadSessionSkills(ctx context.Context, id string) (SkillDiscovery, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "codex" {
		a.mu.Unlock()
		return SkillDiscovery{}, fmt.Errorf("此会话尚无 Skill 来源发现入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	discoverer, supported := a.runtime.(runtime.SkillDiscoverer)
	a.mu.Unlock()
	if err != nil {
		return SkillDiscovery{}, err
	}
	if !supported {
		return SkillDiscovery{}, fmt.Errorf("当前运行时尚未接通 Skill 来源发现")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := discoverer.DiscoverSkills(check, session.Scope)
	if err != nil {
		return SkillDiscovery{}, err
	}
	if out.Cwd != session.Scope.Cwd || out.Source != "codex.skills/list" {
		return SkillDiscovery{}, fmt.Errorf("Skill 发现结果与查询来源不一致")
	}
	if len(out.Skills)+len(out.Errors) > 2048 {
		return SkillDiscovery{}, fmt.Errorf("Skill 来源清单超出核对范围")
	}
	// Identity sampling can touch external files. Keep it outside the state
	// lock and check the saved binding again afterwards. This is not admission.
	result := explainSkillDiscovery(session, out)
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return SkillDiscovery{}, fmt.Errorf("查询期间会话绑定或输入归属已变化，请重新查询 Skill 来源")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return SkillDiscovery{}, err
	}
	return queryCopy(result), nil
}

func (a *Application) verifyCapabilityCatalogBinding(session Session) error {
	if err := verifyExecutionBinding(session); err != nil {
		return err
	}
	if err := runtime.VerifyStateNamespace(session.Scope); err != nil {
		return err
	}
	instance, ok := configuredInstance(a.config, session.Scope.Instance.ID)
	if !ok || digest(instance) != digest(session.Scope.Instance) {
		return fmt.Errorf("能力来源查询所绑定的执行配置已变化")
	}
	// Discovery and catalogs describe current native sources, even after
	// content drift; neither authorizes input under the old snapshot.
	return nil
}
