package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

func (a *Application) captureCodexProjectHooks(scope runtime.Scope) (*runtime.CodexProjectHookSources, error) {
	reader, ok := a.runtime.(runtime.CodexProjectHookReader)
	if !ok {
		return nil, fmt.Errorf("当前运行时尚不能在创建前核对项目 hook 来源")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	sources, err := reader.ReadCodexProjectHookSources(ctx, scope)
	if err != nil {
		return nil, err
	}
	if sources == nil {
		return nil, fmt.Errorf("运行时未返回项目 hook 来源版本")
	}
	scope.CodexProjectHooks = sources
	if err := runtime.VerifyCodexProjectHookFiles(scope); err != nil {
		return nil, err
	}
	return queryCopy(sources), nil
}

func codexProjectHooks(snapshot *ExecutionSnapshot) *runtime.CodexProjectHookSources {
	if snapshot == nil {
		return nil
	}
	return queryCopy(snapshot.CodexProjectHooks)
}

func checkCodexProjectHooks(snapshot *ExecutionSnapshot) []SourceCheck {
	if snapshot.CodexProjectHooks == nil {
		return nil
	}
	scope := runtime.Scope{Instance: snapshot.Instance, Cwd: snapshot.Cwd, CodexProjectHooks: snapshot.CodexProjectHooks}
	if err := runtime.VerifyCodexProjectHookFiles(scope); err != nil {
		return []SourceCheck{{Name: "Codex 项目 hook 文件", Path: snapshot.Cwd, Status: "changed", Detail: err.Error()}}
	}
	checks := []SourceCheck{}
	for _, file := range snapshot.CodexProjectHooks.Files {
		checks = append(checks, SourceCheck{Name: "Codex 项目 hook 文件", Path: file.File.Path, Status: "unchanged"})
	}
	return checks
}
