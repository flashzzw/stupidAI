package app

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
)

func operationFailure(op Operation, view runtime.View) *runtime.Failure {
	for _, turn := range view.Turns {
		if op.Status == "failed" && view.Evidence.Complete && view.ExecutionClear && turn.ID == op.Receipt.NativeTurn && turn.Status == "failed" && turn.Failure != nil {
			return turn.Failure
		}
	}
	kind := "unknown"
	if op.Status == "interrupted" {
		kind = "cancelled"
	}
	return &runtime.Failure{Kind: kind, Source: "console.operation"}
}

func failureReason(kind string) string {
	switch kind {
	case "authentication":
		return "供应商认证失败，请处理凭据并更新 revision"
	case "permission":
		return "供应商权限受限，请处理账号权限"
	case "quota":
		return "供应商余额或配额受限，等待处理"
	case "rate_limit":
		return "供应商限流，等待下次允许时间"
	case "service":
		return "供应商服务暂时故障，等待有限退避"
	case "network":
		return "原生请求网络故障，等待有限退避"
	case "session":
		return "本次会话或输入不满足原生要求"
	case "tool":
		return "工具环境执行失败"
	case "assertion":
		return "探测断言未通过"
	case "cancelled":
		return "执行已取消"
	case "timeout":
		return "探测超过执行期限，原执行已结束；不能据此断言供应商故障"
	default:
		return "执行结果或控制连接尚未核对，不能断言供应商故障"
	}
}

// Old journals without completion times use the request's start as a
// conservative TTL anchor, never the time of recovery.
func resultTime(op Operation, view runtime.View, at time.Time) time.Time {
	started, _ := time.Parse(time.RFC3339Nano, op.CreatedAt)
	for _, turn := range view.Turns {
		if turn.ID != op.Receipt.NativeTurn {
			continue
		}
		completed, err := time.Parse(time.RFC3339Nano, turn.CompletedAt)
		if err == nil && !completed.Before(started.Add(-2*time.Second)) && !completed.After(at.Add(2*time.Second)) {
			return completed
		}
	}
	return started
}

// Recorded in the same transaction as the native outcome. Ordering by request
// start prevents an older, late completion from hiding a newer error.
func (a *Application) recordRouteResult(next *State, key string, session Session, op Operation, proof string, failure *runtime.Failure, detail, probeID string) {
	health := next.RouteHealth[key]
	started, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
	if err != nil {
		return
	}
	previous, _ := time.Parse(time.RFC3339Nano, health.LastResultStartedAt)
	if started.Before(previous) {
		return
	}
	at := time.Now().UTC()
	health.ID, health.RouteID = key, session.Scope.Route.ID
	health.LastOperationID, health.LastSessionID, health.LastProbeID = op.ID, session.ID, probeID
	health.LastResultStartedAt, health.LastResultAt = op.CreatedAt, at.Format(time.RFC3339Nano)
	if failure == nil {
		health.State, health.Proof, health.Error = "healthy", proof, ""
		health.FailureKind, health.NextAllowedAt, health.Failures = "", "", 0
		completed := resultTime(op, session.View, at)
		health.LastSuccessAt = completed.Format(time.RFC3339Nano)
		health.ExpiresAt = completed.Add(a.probeTTL()).Format(time.RFC3339Nano)
	} else {
		priorState, priorError, priorKind := health.State, health.Error, health.FailureKind
		health.ExpiresAt = ""
		// Local failures invalidate success, but cannot lift an unresolved
		// provider restriction without a subsequent verified success.
		if health.State != "attention" && health.State != "backoff" {
			health.State = "unknown"
		}
		health.FailureKind, health.Error = failure.Kind, failureReason(failure.Kind)
		if detail != "" && failure.Kind == "assertion" {
			health.Error = detail
		}
		switch failure.Kind {
		case "authentication", "permission", "quota":
			health.State, health.NextAllowedAt = "attention", ""
			health.Failures++
		case "rate_limit", "service", "network":
			health.Failures++
			if health.Failures >= 5 {
				health.State, health.NextAllowedAt = "attention", ""
				health.Error = "连续路由故障已达到 5 次，请核查配置与供应商后更新 revision"
			} else {
				health.State = "backoff"
				base := 5 * time.Second * time.Duration(1<<min(health.Failures-1, 6))
				if failure.Kind == "rate_limit" {
					base = max(base, 30*time.Second)
				}
				seed := sha256.Sum256([]byte(op.ID))
				nextAt := at.Add(base + base*time.Duration(seed[0])/1280)
				if reset, err := time.Parse(time.RFC3339Nano, failure.RetryAt); err == nil && reset.After(nextAt) {
					nextAt = reset
				}
				health.NextAllowedAt = nextAt.Format(time.RFC3339Nano)
			}
		default:
			if priorState == "attention" || priorState == "backoff" {
				health.Error, health.FailureKind = priorError, priorKind
			}
		}
		if priorState == "attention" && health.State == "backoff" {
			health.State, health.NextAllowedAt, health.Error, health.FailureKind = priorState, "", priorError, priorKind
		}
	}
	next.RouteHealth[key] = health
}

func successfulGeneration(op Operation, view runtime.View) bool {
	if op.Status != "succeeded" || !view.Evidence.Complete || !view.ExecutionClear {
		return false
	}
	completed := false
	for _, turn := range view.Turns {
		if turn.ID == op.Receipt.NativeTurn && turn.Status == "completed" {
			completed = true
		}
	}
	if !completed {
		return false
	}
	for _, message := range view.Messages {
		if message.TurnID == op.Receipt.NativeTurn && message.Role == "assistant" && message.Status == "completed" && strings.TrimSpace(message.Text) != "" {
			return true
		}
	}
	return false
}

func (a *Application) collectRouteHealth(previous State, next *State) {
	for id, op := range next.Operations {
		if previous.Operations[id].Status == op.Status || !createsInput(op.Intent) {
			continue
		}
		session := next.Sessions[op.SessionID]
		if session.Origin == "probe" || session.ControlMode == "observed" || session.RouteFingerprint == "" {
			continue
		}
		if a.requireCapability(session.Scope.Instance.ID, "route_evidence") != nil {
			continue
		}
		if !terminal(op.Status) && op.Status != "uncertain" {
			continue
		}
		key := digest([]string{routeIdentity(op.Intent.Scope.Route), session.RouteFingerprint})
		failure := operationFailure(op, session.View)
		if successfulGeneration(op, session.View) {
			failure = nil
		}
		a.recordRouteResult(next, key, session, op, "generation", failure, "", "")
	}
}

func (a *Application) admitRouteHealth(key string) error {
	health := a.state.RouteHealth[key]
	if health.State == "attention" {
		return fmt.Errorf("路由需要处理：%s", health.Error)
	}
	if deadline, err := time.Parse(time.RFC3339Nano, health.NextAllowedAt); err == nil && time.Now().Before(deadline) {
		return fmt.Errorf("路由退避至 %s：%s", health.NextAllowedAt, health.Error)
	}
	return nil
}
