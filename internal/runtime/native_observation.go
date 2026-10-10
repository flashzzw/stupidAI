package runtime

import (
	"context"
	"fmt"

	"codex-netcap/internal/codex"
)

// NativeExecutionObserver reads an existing executor without loading a thread
// or obtaining input authority. An Adapter's ordinary Observe may resume work
// and therefore is not a fallback for this optional capability.
type NativeExecutionObserver interface {
	ObserveNativeExecution(context.Context, Scope) (View, error)
}

func (a *Adapters) ObserveNativeExecution(ctx context.Context, scope Scope) (View, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return View{}, err
	}
	observer, ok := r.(NativeExecutionObserver)
	if !ok {
		return View{}, fmt.Errorf("该原生来源尚无只读执行观察接口；保留原历史，不启动替代执行器")
	}
	return observer.ObserveNativeExecution(ctx, scope)
}

func (c *Codex) ObserveNativeExecution(ctx context.Context, scope Scope) (View, error) {
	if scope.StateNamespace == "" || scope.NativeID == "" || scope.Cwd == "" || scope.Purpose != "" {
		return View{}, fmt.Errorf("只读原执行观察需要已保存的原生身份与状态目录")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return View{}, err
	}
	// External sources have no selected route or capability overrides. Never
	// turn this read into managed configuration validation or probe startup.
	scope.Route, scope.CredentialStamp, scope.MCPSelections = Route{}, "", nil
	scope.SkillSelections = nil
	scope.PluginSelections = nil
	scope.NoAutoResume, scope.ConfigurationBaseline = true, ""
	c.mu.Lock()
	defer c.mu.Unlock()
	// External threads can use paginated history. The managed legacy reader
	// is needed for its async questions but is not their full-history API.
	view, err := c.observeWithHistory(ctx, scope, func(ctx context.Context, p codex.Client, id string) ([]codex.Turn, error) {
		return codex.Turns(ctx, p, id, 0, true)
	}, "")
	view.TerminalCommand = ""
	return view, err
}
