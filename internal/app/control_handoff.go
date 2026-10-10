package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"codex-netcap/internal/runtime"
)

// A handoff acknowledges immutable message identity and content, not the age
// of a poll. Partial history and later arrivals cannot erase that boundary.
func externalInputKey(message runtime.Message) string {
	b, _ := json.Marshal(struct {
		ID, Turn, Operation, Text string
	}{message.ID, message.TurnID, message.Operation, message.Text})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func externalInputs(view runtime.View, sessionID string, operations map[string]Operation) []string {
	keys := []string{}
	for _, message := range view.Messages {
		if message.Role != "user" {
			continue
		}
		if own, ok := operations[message.Operation]; ok && own.SessionID == sessionID && createsInput(own.Intent) {
			continue
		}
		key := externalInputKey(message)
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

func verifyHandoff(session Session, view runtime.View) error {
	if session.Scope.NativeID == "" || view.NativeID != session.Scope.NativeID {
		return fmt.Errorf("原生会话身份不匹配，保留原绑定并等待核对")
	}
	if !view.Evidence.Complete || !view.ExecutionClear || view.Status != "idle" || len(view.Executions) > 0 {
		return fmt.Errorf("原执行、历史或在途工具尚未确认空闲，自动化保持暂停")
	}
	for _, pending := range view.Pending {
		if pending.Status == "pending" || pending.Status == "submitted" || pending.Status == "unverified" {
			return fmt.Errorf("仍有未核对的原生请求，自动化保持暂停")
		}
	}
	return nil
}

// Called under the application lock after runtime recovery/observation, without
// sending model input. A later
// pause/takeover always wins, even when this check finishes successfully.
func (a *Application) finishHandoff(next State, operation Operation, view runtime.View, checkErr error) {
	session := next.Sessions[operation.SessionID]
	if a.closed || session.Generation != operation.Generation {
		checkErr = fmt.Errorf("核对期间输入归属已变化，本次恢复取消")
	}
	if checkErr == nil {
		retainCodexHookHistory(session, &view)
		checkErr = verifyHandoff(session, view)
	}
	if checkErr == nil {
		checkErr = a.verifyRouteBinding(session)
	}
	operation.Status = "not_sent"
	if checkErr != nil {
		invalidatePermissionEvidence(&session, view)
		operation.Error = checkErr.Error()
		operation.Evidence = append(operation.Evidence, runtime.Evidence{Source: "console.handoff", At: now(), Complete: true, Detail: operation.Error})
	} else {
		operation.Status = "succeeded"
		operation.Receipt.NativeID = session.Scope.NativeID
		operation.Evidence = append(operation.Evidence, view.Evidence)
		session.InputBaseline = externalInputs(view, session.ID, next.Operations)
		session.View = view
		session.Status, session.Connection = view.Status, "ready"
		session.ControlMode, session.Paused = "managed", false
	}
	session.UpdatedAt = now()
	next.Sessions[session.ID] = session
	next.Operations[operation.ID] = operation
	_ = a.commit(next, "operation.handoff", session.ID)
}
