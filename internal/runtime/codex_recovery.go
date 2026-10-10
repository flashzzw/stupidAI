package runtime

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/codex"
)

func (c *Codex) Reconcile(ctx context.Context, in Intent, receipt Receipt) (Resolution, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "codex.reconcile", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "没有可验证的原生关联；未发送与没有查到是不同结论"}}
	p, err := c.client(ctx, in.Scope.Instance)
	if err != nil {
		return result, err
	}
	if in.Kind == "recover_execution" {
		return c.reconcileConfigurationRecovery(ctx, p, in, receipt)
	}
	if in.Kind == "apply_execution" {
		return c.reconcileExecution(ctx, p, in, receipt)
	}
	if in.Scope.NativeID != "" && receipt.NativeID != "" && in.Scope.NativeID != receipt.NativeID {
		result.Evidence.Detail = "候选引用不属于原操作绑定的会话"
		return result, nil
	}
	nativeID := receipt.NativeID
	if nativeID == "" {
		nativeID = in.Scope.NativeID
	}
	if in.Kind == "create" {
		if nativeID == "" {
			nativeID, err = findCreated(ctx, p, in)
			if err != nil {
				return result, err
			}
		}
		if nativeID == "" {
			return result, nil
		}
		thread, e := codex.Read(ctx, p, nativeID)
		if e != nil {
			c.readFailed(in.Scope.Instance, e)
			return result, e
		}
		if thread.ID != nativeID {
			return result, fmt.Errorf("创建候选读取返回了不同原生 ID")
		}
		matched, matchErr := createdMatches(thread, in)
		if matchErr != nil {
			return result, matchErr
		}
		if !matched {
			result.Evidence.Detail = "候选原生会话的来源、工作目录或路由不匹配"
			return result, nil
		}
	}
	if nativeID == "" {
		return result, nil
	}
	scope := in.Scope
	scope.NativeID = nativeID
	scope.RecoveryPermission = in.Permission
	view, err := c.observe(ctx, scope)
	if err != nil {
		return result, err
	}
	result.View = view
	result.Evidence = view.Evidence
	result.Receipt.NativeID = nativeID
	if in.Kind == "create" {
		if view.Evidence.Complete {
			result.Evidence.Detail = "原生创建来源、ID、目录与路由已核对；" + view.Evidence.Detail
			result.State = "finished"
			result.Status = "succeeded"
		}
		return result, nil
	}
	if in.Kind == "respond" && in.Pending != nil {
		if in.Pending.ResponseKind == "message_input" {
			text, e := asyncAnswer(in)
			if e != nil {
				return result, e
			}
			for _, message := range view.Messages {
				if message.Operation == in.ID && message.Text == text {
					result.Receipt.NativeTurn = message.TurnID
					result.State = "running"
					if message.Status != "inProgress" && view.Evidence.Complete && view.ExecutionClear {
						result.State = "finished"
						result.Status = "succeeded"
					}
					return result, nil
				}
			}
			return result, nil
		}
		for _, pending := range view.Pending {
			if pending.ID == in.Pending.ID && pending.Status == "resolved" {
				result.State = "finished"
				result.Status = "succeeded"
				result.Evidence.Complete = true
				result.Evidence.Detail = "原生 serverRequest/resolved 确认该请求已清除"
				return result, nil
			}
		}
		if view.ExecutionClear && view.Evidence.Complete {
			result.State = "finished"
			result.Status = "succeeded"
			result.Evidence.Detail = "原轮次和相关执行已结束，请求无法再接受回复；不推断具体审批选择"
			return result, nil
		}
		return result, nil
	}
	turnID := receipt.NativeTurn
	if in.Kind == "interrupt" {
		turnID = in.NativeTurn
	} else {
		matched := ""
		for _, message := range view.Messages {
			if message.Role == "user" && message.Operation == in.ID && message.Text == in.Text && (turnID == "" || message.TurnID == turnID) {
				matched = message.TurnID
				break
			}
		}
		if matched == "" {
			result.Evidence.Detail = "历史没有匹配原操作 ID 与输入；保留 Unknown"
			return result, nil
		}
		turnID = matched
		result.Receipt.NativeTurn = turnID
	}
	for _, turn := range view.Turns {
		if turn.ID != turnID {
			continue
		}
		if turn.Status == "inProgress" {
			result.State = "running"
			return result, nil
		}
		if !view.Evidence.Complete {
			return result, nil
		}
		if !view.ExecutionClear {
			result.State = "running"
			result.Evidence.Detail = "原轮次结束，但仍有队列、后台进程或子任务占用"
			return result, nil
		}
		switch turn.Status {
		case "completed":
			result.Status = "succeeded"
		case "failed":
			result.Status = "failed"
		case "interrupted":
			result.Status = "interrupted"
		default:
			return result, nil
		}
		if in.Kind == "interrupt" {
			result.Status = "succeeded"
		}
		result.State = "finished"
		return result, nil
	}
	return result, nil
}

func findCreated(ctx context.Context, p codex.Client, in Intent) (string, error) {
	ids, err := codex.Loaded(ctx, p)
	if err != nil {
		return "", err
	}
	match := ""
	seen := map[string]bool{}
	check := func(thread codex.Thread) error {
		if thread.ID == "" {
			return fmt.Errorf("创建候选缺少原生 ID")
		}
		if seen[thread.ID] {
			return nil
		}
		seen[thread.ID] = true
		matched, err := createdMatches(thread, in)
		if err != nil {
			return err
		}
		if matched {
			if match != "" && match != thread.ID {
				return fmt.Errorf("多个原生会话匹配创建意图，需人工核对")
			}
			match = thread.ID
		}
		return nil
	}
	for _, id := range ids {
		thread, e := codex.Read(ctx, p, id)
		if e != nil {
			return "", e
		}
		if thread.ID != id {
			return "", fmt.Errorf("创建候选读取返回了不同原生 ID")
		}
		if e = check(thread); e != nil {
			return "", e
		}
	}
	cursor := ""
	for page := 0; page < 1000; page++ {
		var list struct {
			Data []codex.Thread `json:"data"`
			Next string         `json:"nextCursor"`
		}
		params := map[string]any{"cwd": in.Scope.Cwd, "modelProviders": []string{in.Scope.Route.Provider}, "sourceKinds": []string{"appServer", "vscode", "cli", "exec", "unknown"}, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err = p.Call(ctx, "thread/list", params, &list); err != nil {
			return "", err
		}
		for _, thread := range list.Data {
			if err = check(thread); err != nil {
				return "", err
			}
		}
		if list.Next == "" {
			return match, nil
		}
		if list.Next == cursor {
			return "", fmt.Errorf("会话历史分页未推进")
		}
		cursor = list.Next
	}
	return "", fmt.Errorf("会话查找不完整")
}
