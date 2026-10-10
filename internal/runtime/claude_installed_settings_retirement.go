package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// The application must first durably abandon every reference to this version.
// Runtime then verifies ownership and the real consumers' exclusive file lock.
type ClaudeInstalledSettingsRetirer interface {
	RetireClaudeInstalledSettings(context.Context, Scope) error
}

func (a *Adapters) RetireClaudeInstalledSettings(ctx context.Context, scope Scope) error {
	adapter, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return err
	}
	retirer, ok := adapter.(ClaudeInstalledSettingsRetirer)
	if !ok {
		return fmt.Errorf("此后端不支持执行设置文件回收")
	}
	return retirer.RetireClaudeInstalledSettings(ctx, scope)
}

func (c *Claude) RetireClaudeInstalledSettings(ctx context.Context, scope Scope) error {
	binding := scope.ClaudeInstalledSettings
	if scope.Instance.Kind != "claude" || binding == nil || binding.Version != 1 {
		return fmt.Errorf("待回收的执行设置缺少原绑定")
	}
	id, err := uuid.Parse(binding.Artifact.ID)
	if err != nil || id.String() != binding.Artifact.ID {
		return fmt.Errorf("待回收的执行版本无效")
	}
	m, err := newClaudeSettingsFiles(c.stateDir)
	if err != nil {
		return err
	}
	lock, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	_, active := os.Lstat(filepath.Join(m.root, id.String()+".json"))
	_, released := os.Lstat(filepath.Join(m.root, "released", id.String()+".json"))
	if os.IsNotExist(active) && os.IsNotExist(released) {
		// A not-sent operation may have been refused before publication. Never
		// adopt or delete a same-named file without its durable ownership record.
		if _, err := os.Lstat(binding.Artifact.File.Path); os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("同名设置文件没有本次执行的归属记录，未删除")
	}
	record, err := m.load(id.String())
	if err != nil || record.Version != 2 || record.Scope != claudeSettingsScopeHash(scope) || record.Artifact != binding.Artifact || !equalClaudePluginSettings(record.Settings, ClaudeInstalledPluginSettings(binding)) {
		return fmt.Errorf("设置文件的记录与待回收版本不一致")
	}
	return m.removeRecord(ctx, record)
}
