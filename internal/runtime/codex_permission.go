package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"codex-netcap/internal/codex"
)

type codexCreationPolicy struct {
	ConnectionID         string
	Evidence             PermissionEvidence
	DisabledPluginIDs    json.RawMessage
	Cwd, Model, Provider string
}

// A newly created thread may have no rollout for resume to read yet. Only the
// successful start reply on this very connection can witness its initial
// permission. First input, materialization or connection loss retires it.
func (c *Codex) freshCreationPolicy(scope Scope, p codexConnection, thread codex.Thread, readErr error) (codex.Started, bool) {
	var rpc *codex.RPCError
	if !errors.As(readErr, &rpc) || rpc.Code != -32600 || rpc.Message != "no rollout found for thread id "+scope.NativeID || thread.Status.Type != "idle" || thread.ID != scope.NativeID || thread.Cwd != scope.Cwd || thread.Model != scope.Route.Model || thread.Provider != scope.Route.Provider {
		return codex.Started{}, false
	}
	policy, ok := c.creationPolicies[scope.Instance.ID+"/"+scope.NativeID]
	if !ok || policy.ConnectionID != p.ConnectionID() || !policy.Evidence.Verified || policy.Evidence.ExpectedMode != scope.RecoveryPermission {
		return codex.Started{}, false
	}
	return codex.Started{Thread: thread, Model: thread.Model, Provider: thread.Provider, ApprovalPolicy: policy.Evidence.ApprovalPolicy, Sandbox: policy.Evidence.Sandbox, DisabledPluginIDs: policy.DisabledPluginIDs}, true
}

func codexRecoveryPolicy(scope Scope) (sandbox, approval string, err error) {
	switch scope.RecoveryPermission {
	case "read_only":
		sandbox = "read-only"
	case "workspace":
		sandbox = "workspace-write"
	default:
		return "", "", fmt.Errorf("缺少有效的原权限模式，不能恢复执行或发送输入")
	}
	approval = "on-request"
	if scope.Purpose == "probe" {
		approval = "never"
	}
	return sandbox, approval, nil
}

func verifyCodexRecoveryPermission(scope Scope, resumed codex.Started) (*PermissionEvidence, error) {
	evidence := &PermissionEvidence{Source: "codex.thread/resume", Coverage: "sandbox_mode_and_approval_policy", ExpectedMode: scope.RecoveryPermission, ApprovalPolicy: resumed.ApprovalPolicy, Sandbox: resumed.Sandbox, At: time.Now().UTC().Format(time.RFC3339Nano)}
	wantSandbox, wantApproval, err := codexRecoveryPolicy(scope)
	if err != nil {
		return evidence, err
	}
	var approval string
	var sandbox struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(resumed.ApprovalPolicy, &approval) != nil || json.Unmarshal(resumed.Sandbox, &sandbox) != nil {
		return evidence, fmt.Errorf("原生恢复回执缺少可核对的权限或审批策略，不能继续输入")
	}
	wantType := "readOnly"
	if wantSandbox == "workspace-write" {
		wantType = "workspaceWrite"
	}
	if sandbox.Type != wantType || approval != wantApproval {
		return evidence, fmt.Errorf("原生沙箱模式或审批策略与原权限不匹配；保留原会话，等待核对")
	}
	evidence.Verified = true
	return evidence, nil
}

func (c *Codex) resumeWithPermission(ctx context.Context, p codexConnection, scope Scope, thread codex.Thread) (codex.Thread, *PermissionEvidence, error) {
	var resumed codex.Started
	var err error
	usedCreationPolicy := false
	hookFingerprint, skillFingerprint := "", ""
	params := map[string]any{"threadId": scope.NativeID, "excludeTurns": true}
	if thread.Status.Type == "notLoaded" {
		sandbox, approval, permissionErr := codexRecoveryPolicy(scope)
		if permissionErr != nil {
			return thread, nil, permissionErr
		}
		overrides, routeErr := codexRouteConfig(ctx, p, scope)
		if routeErr != nil {
			return thread, nil, routeErr
		}
		params["model"], params["modelProvider"], params["config"] = scope.Route.Model, scope.Route.Provider, overrides
		params["sandbox"], params["approvalPolicy"] = sandbox, approval
		skillFingerprint, err = c.prepareDirectorySkills(ctx, p, scope)
		if err != nil {
			return thread, nil, err
		}
		hookFingerprint, err = c.prepareSelectedHooks(ctx, p, scope)
		if err != nil {
			return thread, nil, err
		}
	}
	if err = p.Call(ctx, "thread/resume", params, &resumed); err != nil {
		if fresh, ok := c.freshCreationPolicy(scope, p, thread, err); ok {
			resumed = fresh
			usedCreationPolicy = true
		} else {
			c.readFailed(scope.Instance, err)
			return thread, nil, err
		}
	} else {
		delete(c.creationPolicies, scope.Instance.ID+"/"+scope.NativeID)
	}
	if resumed.Thread.ID != scope.NativeID || resumed.Model != scope.Route.Model || resumed.Provider != scope.Route.Provider {
		return thread, nil, fmt.Errorf("恢复的 Codex 会话身份或实际模型不匹配")
	}
	if resumed.Thread.Status.Type != "idle" && resumed.Thread.Status.Type != "active" {
		return thread, nil, fmt.Errorf("原生恢复回执没有可核对的运行状态")
	}
	evidence, err := verifyCodexRecoveryPermission(scope, resumed)
	if err != nil {
		return thread, evidence, err
	}
	if usedCreationPolicy {
		initial := c.creationPolicies[scope.Instance.ID+"/"+scope.NativeID].Evidence
		evidence = &initial
	}
	if thread.Status.Type == "notLoaded" {
		if err = c.rememberDirectorySkills(ctx, p, scope, scope.NativeID, skillFingerprint); err != nil {
			return thread, nil, err
		}
		if err = c.rememberSelectedHooks(ctx, p, scope, scope.NativeID, hookFingerprint); err != nil {
			return thread, nil, err
		}
	}
	return resumed.Thread, evidence, nil
}

// Only an explicit asynchronous answer may disregard its own pending question.
// A native idle gap alone does not exclude a Goal, queue, child or terminal.
func codexIdleForPermission(view View, replyID string) bool {
	if view.NativeStatus != "idle" || !view.Evidence.Complete || len(view.Executions) != 0 || (view.Status != "idle" && view.Status != "waiting_input") {
		return false
	}
	for _, turn := range view.Turns {
		if turn.Status == "inProgress" {
			return false
		}
	}
	found := replyID == ""
	for _, pending := range view.Pending {
		if pending.Status == "resolved" || pending.Status == "expired" {
			continue
		}
		if replyID == "" || pending.ID != replyID || pending.Status != "pending" || pending.ResponseKind != "message_input" {
			return false
		}
		found = true
	}
	for _, capability := range view.CapabilityEvidence {
		// A previous source/version records usage, not a current selection.
		if capability.Mode != "inherit" && capability.Mode != "history" && !capability.Verified {
			return false
		}
	}
	for _, plugin := range view.PluginEvidence {
		if !plugin.Verified {
			return false
		}
	}
	return found
}

func (c *Codex) sampleLoadedPermission(ctx context.Context, p codexConnection, scope Scope, thread codex.Thread, before View, readTurns func(context.Context, codex.Client, string) ([]codex.Turn, error), replyID string) (View, error) {
	connectionID := p.ConnectionID()
	_, policy, err := c.resumeWithPermission(ctx, p, scope, thread)
	if err != nil {
		before.PermissionEvidence = policy
		return before, err
	}
	// Resume is not a pure read. Use fresh history and execution observations,
	// never the idle result that admitted it, and never resume a second time.
	scope.NoAutoResume = true
	after, err := c.observeWithHistory(ctx, scope, readTurns, replyID)
	after.PermissionEvidence = policy
	current := c.clients[scope.Instance.ID]
	if current == nil || current.ConnectionID() != connectionID || p.ConnectionID() != connectionID {
		policy.Verified = false
		return after, fmt.Errorf("权限采样期间原生连接已变化，等待重新核对")
	}
	if policy.Source == "codex.thread/start" {
		initial, ok := c.creationPolicies[scope.Instance.ID+"/"+scope.NativeID]
		if !ok || initial.ConnectionID != connectionID {
			policy.Verified = false
			return after, fmt.Errorf("创建回执已因原生活动或历史失效，等待重新核对")
		}
	}
	if err != nil || !codexIdleForPermission(after, replyID) {
		policy.Verified = false
	}
	return after, err
}
