package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"codex-netcap/internal/runtime"
)

type CodexHookSourceChange struct {
	BeforeOrigin string              `json:"before_origin,omitempty"`
	AfterOrigin  string              `json:"after_origin,omitempty"`
	Source       string              `json:"source"`
	Before       *runtime.FileSource `json:"before,omitempty"`
	After        *runtime.FileSource `json:"after,omitempty"`
	Detail       string              `json:"detail"`
}

func codexHookSourceAdoption(before, after *ExecutionSnapshot) bool {
	return before != nil && after != nil && before.Instance.Kind == "codex" && (digest(before.CodexUserHooks) != digest(after.CodexUserHooks) || digest(before.CodexProjectHooks) != digest(after.CodexProjectHooks))
}

func (a *Application) captureCodexHookSourceAdoption(session Session, candidate *ExecutionSnapshot) ([]CodexHookSourceChange, error) {
	original := session.ExecutionSnapshot
	if session.Scope.Instance.Kind != "codex" || original.CodexUserHooks == nil || original.CodexProjectHooks == nil || original.CodexUserHooks.Version != 1 || original.CodexProjectHooks.Version != 1 {
		return nil, fmt.Errorf("原执行尚未记录完整的用户/项目 hook 文件版本，不能直接采纳")
	}
	user, err := runtime.ReadCodexUserHooks(session.Scope)
	if err != nil {
		return nil, err
	}
	project, err := a.captureCodexProjectHooks(session.Scope)
	if err != nil {
		return nil, err
	}
	changes := []CodexHookSourceChange{}
	if digest(user) != digest(original.CodexUserHooks) {
		changes = append(changes, CodexHookSourceChange{Source: "user", Before: queryCopy(&original.CodexUserHooks.File), After: queryCopy(&user.File), Detail: "用户 hook 文件版本变化；保留原生信任配置，不自动信任新 hash"})
	}
	oldFiles := map[string]runtime.CodexProjectHookFile{}
	newFiles := map[string]runtime.CodexProjectHookFile{}
	paths := []string{}
	for _, f := range original.CodexProjectHooks.Files {
		oldFiles[f.File.Path] = f
		paths = append(paths, f.File.Path)
	}
	for _, f := range project.Files {
		newFiles[f.File.Path] = f
		if _, exists := oldFiles[f.File.Path]; !exists {
			paths = append(paths, f.File.Path)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		old, had := oldFiles[path]
		next, has := newFiles[path]
		if had && has && digest(old) == digest(next) {
			continue
		}
		diff := CodexHookSourceChange{Source: "project", Detail: "项目 hook 文件或来源关系变化"}
		if had {
			diff.Before = queryCopy(&old.File)
			diff.BeforeOrigin = codexProjectHookOrigin(old)
		}
		if has {
			diff.After = queryCopy(&next.File)
			diff.AfterOrigin = codexProjectHookOrigin(next)
		}
		if !had {
			diff.Detail = "新增项目来源"
		} else if !has {
			diff.Detail = "移除项目来源"
		} else if old.File == next.File {
			diff.Detail = "配置层版本、启用状态或原生发现关系变化"
		}
		changes = append(changes, diff)
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("用户/项目 hook 来源版本未变化，无需采纳")
	}
	candidate.CodexUserHooks = user
	candidate.CodexProjectHooks = project
	if original.CodexConfigurationSources != nil {
		sources, err := a.captureCodexConfigurationSources(session.Scope)
		if err != nil {
			return nil, err
		}
		if original.CodexConfigurationSources.Settings == nil {
			// Hook adoption keeps the original comparison coverage. A newer
			// reader must not silently upgrade a saved candidate's schema.
			sources.Settings = nil
		}
		candidate.CodexConfigurationSources = sources
		if !runtime.CodexHookConfigurationTransition(session.Scope, executionChangeScope(session.Scope, candidate)) {
			return nil, fmt.Errorf("hook 采纳不能同时采纳配置文件或合并来源变化；仅允许空项目配置层的增减")
		}
	}
	return changes, nil
}

func codexProjectHookOrigin(file runtime.CodexProjectHookFile) string {
	layer := "未声明"
	if file.Declared {
		layer = "已声明"
		if file.LayerDisabled {
			layer += "、未启用"
		}
		layer += "（" + file.LayerVersion + "）"
	}
	found := "未发现"
	if file.Discovered {
		found = "已发现"
	}
	return "配置层：" + layer + "；原生清单：" + found
}

func (a *Application) captureCodexHookSourceProof(session Session, candidate *ExecutionSnapshot) (*runtime.CodexHookSourceProof, error) {
	reader, ok := a.runtime.(runtime.CodexHookSourceProofReader)
	if !ok {
		return nil, fmt.Errorf("运行时尚不能保存 hook 采纳的原配置依据")
	}
	var prior *runtime.CodexHookSourceProof
	if session.PendingExecutionChange != nil && session.PendingExecutionChange.adoptsCodexSources() {
		prior = session.PendingExecutionChange.HookSourceProof
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	proof, err := reader.ReadCodexHookSourceProof(ctx, session.Scope, executionChangeScope(session.Scope, candidate), prior)
	if err != nil {
		return nil, err
	}
	if err := runtime.ValidateCodexHookSourceProof(session.Scope, proof); err != nil {
		return nil, err
	}
	return queryCopy(proof), nil
}
