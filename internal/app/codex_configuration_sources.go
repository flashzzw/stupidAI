package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

func (a *Application) captureCodexConfigurationSources(scope runtime.Scope) (*runtime.CodexConfigurationSources, error) {
	reader, ok := a.runtime.(runtime.CodexConfigurationSourceReader)
	if !ok {
		return nil, fmt.Errorf("当前运行时尚不能在创建前固定 Codex 配置来源")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	sources, err := reader.ReadCodexConfigurationSources(ctx, scope)
	if err != nil {
		return nil, err
	}
	if sources == nil {
		return nil, fmt.Errorf("运行时未返回 Codex 配置来源版本")
	}
	scope.CodexConfigurationSources = sources
	if err := runtime.VerifyCodexConfigurationFiles(scope); err != nil {
		return nil, err
	}
	return queryCopy(sources), nil
}

func codexConfigurationSources(snapshot *ExecutionSnapshot) *runtime.CodexConfigurationSources {
	if snapshot == nil {
		return nil
	}
	return queryCopy(snapshot.CodexConfigurationSources)
}

func checkCodexConfigurationSources(snapshot *ExecutionSnapshot) []SourceCheck {
	if snapshot.CodexConfigurationSources == nil {
		return nil
	}
	if err := runtime.VerifyCodexConfigurationFiles(runtime.Scope{Instance: snapshot.Instance, Cwd: snapshot.Cwd, CodexConfigurationSources: snapshot.CodexConfigurationSources}); err != nil {
		return []SourceCheck{{Name: "Codex 配置来源文件", Path: snapshot.Cwd, Status: "changed", Detail: err.Error()}}
	}
	checks := []SourceCheck{}
	for _, layer := range snapshot.CodexConfigurationSources.Layers {
		if layer.File != nil {
			checks = append(checks, SourceCheck{Name: "Codex 配置来源文件 · " + layer.Kind, Path: layer.File.Path, Status: "unchanged"})
		}
	}
	return checks
}
