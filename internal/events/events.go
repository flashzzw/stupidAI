// Package events owns domain event identity and native observation formats.
// It never dispatches runtime commands or grants control from an observation.
package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Kind string

const (
	PermissionRequired  Kind = "permission_required"
	InputRequired       Kind = "input_required"
	RunFailed           Kind = "run_failed"
	TurnCompleted       Kind = "session.turn_completed"
	SessionStarted      Kind = "session.started"
	SessionStopped      Kind = "session.stopped"
	Activity            Kind = "session.activity"
	InteractionResolved Kind = "interaction.resolved"
	TaskSucceeded       Kind = "task.succeeded"
	GoalCompleted       Kind = "goal.completed"
	RouteUnavailable    Kind = "route.unavailable"
	RouteRecovered      Kind = "route.recovered"
)

func (k Kind) Subject() string {
	switch k {
	case PermissionRequired, InputRequired, RunFailed, TurnCompleted, SessionStarted, SessionStopped, Activity, InteractionResolved:
		return "session"
	case TaskSucceeded:
		return "task"
	case GoalCompleted:
		return "goal"
	case RouteUnavailable, RouteRecovered:
		return "route"
	default:
		return ""
	}
}

type Envelope struct {
	EventID            string   `json:"event_id"`
	Kind               Kind     `json:"kind"`
	Source             string   `json:"source"`
	Sources            []string `json:"sources"`
	SubjectType        string   `json:"subject_type"`
	SubjectID          string   `json:"subject_id"`
	OccurredAt         string   `json:"occurred_at"`
	SessionID          string   `json:"session_id,omitempty"`
	OperationID        string   `json:"operation_id,omitempty"`
	AttemptID          string   `json:"attempt_id,omitempty"`
	InstanceID         string   `json:"instance_id,omitempty"`
	AgentKind          string   `json:"agent_kind,omitempty"`
	NativeSessionID    string   `json:"native_session_id,omitempty"`
	NativeTurnID       string   `json:"native_turn_id,omitempty"`
	NativeRequestID    string   `json:"native_request_id,omitempty"`
	NativeConnectionID string   `json:"native_connection_id,omitempty"`
	Summary            string   `json:"summary"`
	NotificationSender string   `json:"notification_sender"`
	Active             bool     `json:"active"`
	Managed            bool     `json:"managed"`
}

// Identify correlates only explicit native request/turn identities. Without
// one, the source delivery ID remains distinct even if the text is identical.
func Identify(e Envelope, deliveryID string) (Envelope, error) {
	if e.Kind.Subject() == "" || e.SubjectType != e.Kind.Subject() || e.SubjectID == "" || e.Source == "" || deliveryID == "" {
		return e, fmt.Errorf("事件种类、来源或主体身份无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
		return e, fmt.Errorf("事件时间无效")
	}
	if e.NotificationSender != "legacy" && e.NotificationSender != "console" {
		return e, fmt.Errorf("事件通知归属无效")
	}
	if e.SubjectType == "session" && (e.SessionID != e.SubjectID || e.InstanceID == "") {
		return e, fmt.Errorf("会话事件缺少实例/原生身份")
	}
	if e.SubjectType == "route" && e.SessionID != "" {
		return e, fmt.Errorf("路由主体不能伪造会话身份")
	}
	key := []string{string(e.Kind), e.SubjectType, e.SubjectID, e.Source, deliveryID}
	if e.SubjectType == "session" {
		ref := ""
		if e.NativeRequestID != "" && e.NativeConnectionID != "" {
			ref = "request/" + e.NativeConnectionID + "/" + e.NativeRequestID
		} else if e.NativeTurnID != "" && e.Kind != PermissionRequired && e.Kind != InputRequired {
			ref = "turn/" + e.NativeTurnID
		}
		if ref != "" && e.NativeSessionID != "" {
			key = []string{string(e.Kind), e.SubjectType, e.SubjectID, e.InstanceID, e.NativeSessionID, ref}
		}
	}
	b, _ := json.Marshal(key)
	sum := sha256.Sum256(b)
	e.EventID = hex.EncodeToString(sum[:])
	e.Sources = []string{e.Source}
	return e, nil
}

func Merge(old, next Envelope) (Envelope, error) {
	if old.EventID != next.EventID || old.Kind != next.Kind || old.SubjectID != next.SubjectID || old.InstanceID != next.InstanceID || old.NativeSessionID != next.NativeSessionID {
		return old, fmt.Errorf("事件身份冲突")
	}
	sources := append([]string{}, old.Sources...)
	for _, source := range next.Sources {
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}
	// Runtime evidence owns managed state; hook/OTEL observations only add
	// provenance and cannot overwrite its result or notification ownership.
	if strings.HasPrefix(next.Source, "runtime/") {
		old = next
	}
	old.Sources = sources
	return old, nil
}
