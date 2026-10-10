package app

import "codex-netcap/internal/runtime"

func codexUserHooks(snapshot *ExecutionSnapshot) *runtime.CodexUserHookSource {
	if snapshot == nil {
		return nil
	}
	return queryCopy(snapshot.CodexUserHooks)
}

func checkCodexUserHooks(snapshot *ExecutionSnapshot) []SourceCheck {
	if snapshot.CodexUserHooks == nil {
		return nil
	}
	saved := snapshot.CodexUserHooks
	item := SourceCheck{Name: "Codex 用户 hook 文件", Path: saved.File.Path, Status: "unchanged"}
	if err := runtime.VerifyCodexUserHooks(runtime.Scope{Instance: snapshot.Instance, Cwd: snapshot.Cwd, CodexUserHooks: saved}); err != nil {
		item.Status, item.Detail = "changed", err.Error()
	}
	return []SourceCheck{item}
}
