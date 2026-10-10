package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
)

type ProbeRecord struct {
	ID                string `json:"id"`
	RouteID           string `json:"route_id"`
	HealthKey         string `json:"health_key"`
	Mode              string `json:"mode"`
	SessionID         string `json:"session_id"`
	CreateOperationID string `json:"create_operation_id"`
	SendOperationID   string `json:"send_operation_id,omitempty"`
	StopOperationID   string `json:"stop_operation_id,omitempty"`
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	Result            string `json:"result,omitempty"`
	StartedAt         string `json:"started_at"`
	FinishedAt        string `json:"finished_at,omitempty"`
	DeadlineAt        string `json:"deadline_at,omitempty"`
	TimedOut          bool   `json:"timed_out,omitempty"`
}

type RouteHealth struct {
	ID                  string `json:"id"`
	RouteID             string `json:"route_id"`
	State               string `json:"state"`
	Proof               string `json:"proof"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	ExpiresAt           string `json:"expires_at,omitempty"`
	LastProbeID         string `json:"last_probe_id,omitempty"`
	Error               string `json:"error,omitempty"`
	FailureKind         string `json:"failure_kind,omitempty"`
	NextAllowedAt       string `json:"next_allowed_at,omitempty"`
	Failures            int    `json:"consecutive_failures,omitempty"`
	LastOperationID     string `json:"last_operation_id,omitempty"`
	LastSessionID       string `json:"last_session_id,omitempty"`
	LastResultStartedAt string `json:"last_result_started_at,omitempty"`
	LastResultAt        string `json:"last_result_at,omitempty"`
}

func probeTerminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}
func healthKey(instance runtime.Instance, route runtime.Route) (string, error) {
	fingerprint, err := routeFingerprint(instance, route)
	if err != nil {
		return "", err
	}
	return digest([]string{routeIdentity(route), fingerprint}), nil
}
func (a *Application) probeTTL() time.Duration {
	seconds := a.config.Automation.ProbeTTLSeconds
	if seconds == 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func (a *Application) probeTimeout() time.Duration {
	seconds := a.config.Automation.ProbeTimeoutSeconds
	if seconds == 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func (a *Application) admitProbe(routeID, sessionID string) error {
	globalLimit, routeLimit := a.config.Automation.GlobalLimit, a.config.Automation.RouteLimit
	if globalLimit == 0 {
		globalLimit = 1
	}
	if routeLimit == 0 {
		routeLimit = 1
	}
	global, route := 0, 0
	activeProbes := map[string]bool{}
	for _, probe := range a.state.Probes {
		if probe.SessionID == sessionID || probeTerminal(probe.Status) {
			continue
		}
		global++
		activeProbes[probe.SessionID] = true
		if probe.RouteID == routeID {
			route++
		}
	}
	for _, session := range a.state.Sessions {
		if session.ID == sessionID || session.ControlMode == "observed" || session.Status == "closed" {
			continue
		}
		if session.Scope.Route.ID == routeID && session.ControlMode == "human_takeover" {
			return fmt.Errorf("路由有人工接管会话，探测须等待人工释放")
		}
		if activeProbes[session.ID] {
			continue
		}
		occupied := a.busy[session.ID] || a.occupied(session.ID) || session.Status != "idle" || !session.View.ExecutionClear || !session.View.Evidence.Complete
		if occupied {
			global++
			if session.Scope.Route.ID == routeID {
				route++
			}
		}
	}
	if route >= routeLimit {
		return fmt.Errorf("路由执行容量已被在途、待处理或未知执行占用")
	}
	if global >= globalLimit {
		return fmt.Errorf("全局自动执行容量已被在途、待处理或未知执行占用")
	}
	return nil
}

func validateProbeConfig(instance runtime.Instance, route runtime.Route, mode string) error {
	if mode != "utc-date" && mode != "text" {
		return fmt.Errorf("探测模式必须为 utc-date 或 text")
	}
	key, ok := strings.CutPrefix(route.CredentialRef, "env:")
	if !ok || key == "" || os.Getenv(key) == "" {
		return fmt.Errorf("隔离探测需要已设置的 env: 凭据引用")
	}
	if instance.Kind == "codex" && instance.ServerExecutable == "" {
		return fmt.Errorf("Codex 探测需要配置已验证的 server_executable")
	}
	return nil
}

// tickProbes runs under the application lock. Each next native command uses
// the same durable command/operation transaction as interactive input.
func (a *Application) tickProbes() {
	ids := []string{}
	for id := range a.state.Probes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		probe := a.state.Probes[id]
		if probeTerminal(probe.Status) {
			continue
		}
		session := a.state.Sessions[probe.SessionID]
		create := a.state.Operations[probe.CreateOperationID]
		deadline, deadlineErr := time.Parse(time.RFC3339Nano, probe.DeadlineAt)
		if !probe.TimedOut && deadlineErr == nil && time.Now().After(deadline) {
			sent := a.state.Operations[probe.SendOperationID]
			if probe.SendOperationID == "" || !terminal(sent.Status) || resultTime(sent, session.View, time.Now()).After(deadline) {
				probe.TimedOut = true
				probe.Reason = "探测超时，停止续发并核对原执行"
				next := clone(a.state)
				next.Probes[id] = probe
				op := create
				if probe.SendOperationID != "" {
					op = sent
				}
				a.recordRouteResult(&next, probe.HealthKey, session, op, probe.Mode, &runtime.Failure{Kind: "unknown", Source: "console.probe.timeout"}, "", probe.ID)
				if a.commit(next, "probe.timeout", session.ID) != nil {
					return
				}
				continue
			}
		}
		old := probe
		if !terminal(create.Status) {
			probe.Status = "starting"
			if create.Status == "uncertain" {
				probe.Status = "unknown"
				probe.Reason = "创建结果未知，先核对原执行"
			}
		} else if create.Status != "succeeded" {
			probe.Status = "failed"
			probe.Reason = "原生探测创建未完成：" + create.Error
		} else if probe.SendOperationID == "" {
			if session.Paused || session.ControlMode != "managed" || probe.TimedOut {
				probe.Status = "cancelled"
				probe.Reason = "探测自动化已暂停或转人工"
			} else if err := a.admitRouteHealth(probe.HealthKey); err != nil {
				probe.Status = "waiting"
				probe.Reason = err.Error()
			} else if err := a.admitProbe(probe.RouteID, session.ID); err != nil {
				probe.Status = "waiting"
				probe.Reason = err.Error()
			} else {
				_, err := a.applyCommand(Command{ID: "probe-send-" + id, Kind: "send", SessionID: session.ID, Text: runtime.ProbePrompt(probe.Mode)}, true)
				if err == nil {
					continue
				}
				probe.Status = "failed"
				probe.Reason = err.Error()
			}
		} else {
			sent := a.state.Operations[probe.SendOperationID]
			stop, stopping := a.state.Operations[probe.StopOperationID]
			if stopping && !terminal(stop.Status) {
				probe.Status, probe.Reason = "stopping", "等待中止结果与原执行范围核对"
				if stop.Status == "uncertain" {
					probe.Status = "unknown"
				}
			} else if !terminal(sent.Status) {
				probe.Status = "running"
				probe.Reason = "等待原生轮次结束"
				if sent.Status == "uncertain" {
					probe.Status = "unknown"
					probe.Reason = "发送结果未知，先核对原操作"
				}
				if probe.TimedOut {
					probe.Reason = "探测已超时，保留占用直到原执行结束"
					if !stopping && sent.Receipt.NativeTurn != "" && !a.busy[session.ID] && session.ControlMode == "managed" {
						if _, err := a.applyCommand(Command{ID: "probe-stop-" + id, Kind: "interrupt", SessionID: session.ID}, true); err == nil {
							continue
						}
					}
				}
			} else if probe.TimedOut {
				probe.Status, probe.Reason = "failed", "探测超时，原执行已核对结束"
			} else if sent.Status != "succeeded" {
				probe.Status = "failed"
				probe.Reason = "原生探测未成功结束"
				if sent.Error != "" {
					probe.Reason = sent.Error
				}
			} else {
				started, _ := time.Parse(time.RFC3339Nano, sent.CreatedAt)
				result, err := runtime.VerifyProbe(session.View, sent.Receipt.NativeTurn, probe.Mode, started)
				probe.Status, probe.Result, probe.Reason = "succeeded", result, "完整原生轮次和探测断言已验证"
				if err != nil {
					probe.Status = "failed"
					probe.Reason = err.Error()
				}
			}
		}
		if old == probe {
			continue
		}
		next := clone(a.state)
		if probe.Status == "unknown" {
			op := create
			if probe.SendOperationID != "" {
				op = next.Operations[probe.SendOperationID]
			}
			a.recordRouteResult(&next, probe.HealthKey, session, op, probe.Mode, &runtime.Failure{Kind: "unknown", Source: "console.operation"}, "", probe.ID)
		}
		if probeTerminal(probe.Status) {
			probe.FinishedAt = now()
			op := create
			if probe.SendOperationID != "" {
				op = next.Operations[probe.SendOperationID]
			}
			failure := &runtime.Failure{Kind: "assertion", Source: "console.probe"}
			if op.Status != "succeeded" {
				failure = operationFailure(op, session.View)
			}
			if probe.Status == "cancelled" {
				failure = &runtime.Failure{Kind: "cancelled", Source: "console.probe"}
			}
			if probe.TimedOut {
				failure = &runtime.Failure{Kind: "timeout", Source: "console.probe"}
				probe.Reason = "探测超时，原执行已核对结束"
			}
			if probe.Status == "succeeded" {
				failure = nil
			}
			a.recordRouteResult(&next, probe.HealthKey, session, op, probe.Mode, failure, probe.Reason, probe.ID)
		}
		next.Probes[id] = probe
		if a.commit(next, "probe.updated", session.ID) != nil {
			return
		}
	}
}
