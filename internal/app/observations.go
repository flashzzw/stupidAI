package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex-netcap/internal/events"
	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type ObservationDelivery struct {
	EventID    string          `json:"event_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

type ObservationReceipt struct {
	ID         string   `json:"id"`
	Hash       string   `json:"hash"`
	EventIDs   []string `json:"event_ids"`
	SessionIDs []string `json:"session_ids"`
}

type ObserverUnauthorized struct{}

func (*ObserverUnauthorized) Error() string { return "观察来源或专用令牌无效" }

func notificationSender(value string) string {
	if value == "console" {
		return "console"
	}
	return "legacy"
}

func storeDomainEvent(state *State, event events.Envelope) error {
	if state.DomainEvents == nil {
		state.DomainEvents = map[string]events.Envelope{}
	}
	if previous, exists := state.DomainEvents[event.EventID]; exists {
		merged, err := events.Merge(previous, event)
		if err != nil {
			return err
		}
		event = merged
	}
	state.DomainEvents[event.EventID] = event
	return nil
}

func nativeRequestIdentity(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func associateObservedOperation(state State, session Session, observation events.Observation) string {
	for _, pending := range session.View.Pending {
		if observation.NativeConnectionID != "" && observation.NativeConnectionID == pending.ConnectionID && observation.NativeRequestID != "" && nativeRequestIdentity(pending.RequestID) == observation.NativeRequestID {
			observation.NativeTurnID = pending.NativeTurn
		}
	}
	if observation.NativeTurnID == "" {
		return ""
	}
	for _, operation := range state.Operations {
		if operation.SessionID == session.ID && createsInput(operation.Intent) && operation.Receipt.NativeTurn == observation.NativeTurnID {
			return operation.ID
		}
	}
	return ""
}

func (a *Application) IngestObservation(sourceID, credential string, delivery ObservationDelivery) (ObservationReceipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var source *ObserverConfig
	for _, candidate := range a.config.Observers {
		key, ok := strings.CutPrefix(candidate.TokenRef, "env:")
		expected := os.Getenv(key)
		if candidate.ID == sourceID && ok && len(expected) >= 32 && subtle.ConstantTimeCompare([]byte(expected), []byte(credential)) == 1 {
			copy := candidate
			source = &copy
		}
	}
	if source == nil {
		return ObservationReceipt{}, &ObserverUnauthorized{}
	}
	if a.closed || a.fatal != nil {
		return ObservationReceipt{}, fmt.Errorf("控制台不能持久接收事件")
	}
	if delivery.EventID == "" || len(delivery.EventID) > 200 || len(delivery.Payload) > 1<<20 {
		return ObservationReceipt{}, fmt.Errorf("事件投递身份/大小无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, delivery.OccurredAt); err != nil {
		return ObservationReceipt{}, fmt.Errorf("事件时间无效")
	}
	var instance runtime.Instance
	var project Project
	for _, candidate := range a.config.Instances {
		if candidate.ID == source.InstanceID {
			instance = candidate
		}
	}
	for _, candidate := range a.config.Projects {
		if candidate.ID == source.ProjectID {
			project = candidate
		}
	}
	if instance.ID == "" || project.ID == "" {
		return ObservationReceipt{}, fmt.Errorf("观察来源配置已失效")
	}
	b, _ := json.Marshal(delivery)
	sum := sha256.Sum256(b)
	receipt := ObservationReceipt{ID: source.ID + "/" + delivery.EventID, Hash: hex.EncodeToString(sum[:]), EventIDs: []string{}, SessionIDs: []string{}}
	if previous, exists := a.state.ObservationReceipts[receipt.ID]; exists {
		if previous.Hash != receipt.Hash {
			return receipt, fmt.Errorf("同一事件投递 ID 已用于不同内容")
		}
		return queryCopy(previous), nil
	}
	observations, err := events.Normalize(instance.Kind, source.Format, delivery.Payload)
	if err != nil {
		return receipt, err
	}
	next := clone(a.state)
	if next.ObservationReceipts == nil {
		next.ObservationReceipts = map[string]ObservationReceipt{}
	}
	for index, observation := range observations {
		cwd := project.Cwd
		if observation.Cwd != "" {
			resolved, err := filepath.EvalSymlinks(observation.Cwd)
			if err != nil || resolved != project.Cwd {
				return receipt, fmt.Errorf("观察事件工作目录不属于已配置项目")
			}
			cwd = resolved
		}
		var session Session
		for _, candidate := range next.Sessions {
			if candidate.Scope.Instance.ID == instance.ID && candidate.Scope.NativeID == observation.NativeSessionID {
				if session.ID != "" {
					return receipt, fmt.Errorf("原生会话关联不唯一")
				}
				session = candidate
			}
		}
		if session.ID == "" {
			id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("observed\x00"+instance.ID+"\x00"+observation.NativeSessionID)).String()
			session = Session{ID: id, Title: instance.Kind + " · " + observation.NativeSessionID, ProjectID: project.ID,
				Scope:  runtime.Scope{SessionID: id, Instance: instance, NativeID: observation.NativeSessionID, Cwd: cwd},
				Origin: source.Format + "/" + source.ID, ControlMode: "observed", Status: "unknown", Connection: "observed", Paused: true,
				View: runtime.View{NativeID: observation.NativeSessionID, Status: "unknown", Messages: []runtime.Message{}, Pending: []runtime.Pending{}, Executions: []runtime.Execution{}, Turns: []runtime.Turn{}}}
		}
		if session.ProjectID != project.ID || session.Scope.Cwd != cwd {
			return receipt, fmt.Errorf("观察来源与已有会话项目不匹配")
		}
		managed := session.ControlMode != "observed" && session.Origin != "native"
		sender := notificationSender(source.NotificationSender)
		if managed {
			sender = notificationSender(session.Scope.Instance.NotificationSender)
		}
		envelope, err := events.Identify(events.Envelope{Kind: observation.Kind, Source: source.Format + "/" + source.ID, SubjectType: "session", SubjectID: session.ID,
			SessionID: session.ID, InstanceID: instance.ID, AgentKind: instance.Kind, NativeSessionID: observation.NativeSessionID, NativeTurnID: observation.NativeTurnID, NativeRequestID: observation.NativeRequestID, NativeConnectionID: observation.NativeConnectionID,
			OperationID: associateObservedOperation(next, session, observation), OccurredAt: delivery.OccurredAt, Summary: observation.Summary, NotificationSender: sender, Active: true, Managed: managed}, fmt.Sprintf("%s/%d", delivery.EventID, index))
		if err != nil {
			return receipt, err
		}
		if !managed {
			at, _ := time.Parse(time.RFC3339Nano, envelope.OccurredAt)
			last, _ := time.Parse(time.RFC3339Nano, session.ActivityAt)
			previous, exists := next.DomainEvents[envelope.EventID]
			if at.Before(last) || (exists && !previous.Active) {
				envelope.Active = false
			} else if session.Origin == "native" {
				// A registered history source has no live execution projection.
				// Keep hook activity in the timeline without upgrading its native
				// status, requests or control evidence.
				session.ActivityAt, session.UpdatedAt = envelope.OccurredAt, now()
			} else {
				projectObservation(&next, &session, envelope)
			}
		} else {
			// Even without native correlation, an event that predates newer
			// input cannot revive a cancelled notification. Runtime evidence
			// still wins when storeDomainEvent merges an identified action.
			at, _ := time.Parse(time.RFC3339Nano, envelope.OccurredAt)
			for _, previous := range next.DomainEvents {
				if previous.SessionID == session.ID && previous.Managed && previous.Kind == events.Activity {
					activityAt, _ := time.Parse(time.RFC3339Nano, previous.OccurredAt)
					if at.Before(activityAt) {
						envelope.Active = false
					}
				}
			}
		}
		if err := storeDomainEvent(&next, envelope); err != nil {
			return receipt, err
		}
		next.Sessions[session.ID] = session
		receipt.EventIDs = append(receipt.EventIDs, envelope.EventID)
		receipt.SessionIDs = append(receipt.SessionIDs, session.ID)
	}
	next.ObservationReceipts[receipt.ID] = receipt
	if err := a.commit(next, "observation.received", ""); err != nil {
		return ObservationReceipt{}, err
	}
	return queryCopy(receipt), nil
}

func projectObservation(state *State, session *Session, event events.Envelope) {
	at, _ := time.Parse(time.RFC3339Nano, event.OccurredAt)
	last, _ := time.Parse(time.RFC3339Nano, session.ActivityAt)
	// Preserve old source evidence in the ledger without allowing a late event
	// to resurrect a cleared prompt or replace newer observed activity.
	if at.Before(last) {
		return
	}
	session.ActivityAt, session.UpdatedAt = event.OccurredAt, now()
	session.View.Evidence = runtime.Evidence{Source: event.Source, At: event.OccurredAt, Complete: false, Detail: "仅观察事件；不证明控制权、路由或完整原生执行状态"}
	clear := event.Kind == events.Activity || event.Kind == events.TurnCompleted || event.Kind == events.SessionStopped || event.Kind == events.InteractionResolved
	if clear {
		for n := range session.View.Pending {
			pending := &session.View.Pending[n]
			if event.NativeRequestID == "" || nativeRequestIdentity(pending.RequestID) == event.NativeRequestID {
				pending.Status = "expired"
			}
		}
		for id, previous := range state.DomainEvents {
			if previous.SessionID == session.ID && !previous.Managed && (event.NativeRequestID == "" || previous.NativeRequestID == event.NativeRequestID) {
				previous.Active = false
				state.DomainEvents[id] = previous
			}
		}
	}
	switch event.Kind {
	case events.PermissionRequired, events.InputRequired:
		session.Status = "waiting_approval"
		if event.Kind == events.InputRequired {
			session.Status = "waiting_input"
		}
		found := false
		for _, pending := range session.View.Pending {
			if pending.ID == event.EventID {
				found = true
			}
		}
		if !found {
			id, _ := json.Marshal(event.NativeRequestID)
			session.View.Pending = append(session.View.Pending, runtime.Pending{ID: event.EventID, NativeID: event.NativeSessionID, NativeTurn: event.NativeTurnID, Kind: string(event.Kind), RequestID: id, ResponseKind: "unsupported", Title: event.Summary, Status: "pending", Options: []runtime.Option{}, Questions: []runtime.Question{}})
		}
	case events.Activity:
		session.Status = "running"
	case events.TurnCompleted, events.SessionStopped, events.SessionStarted:
		session.Status = "idle"
	case events.RunFailed, events.InteractionResolved:
		session.Status = "unknown"
	}
	session.View.Status = session.Status
	if _, exists := state.DomainEvents[event.EventID]; !exists {
		session.View.Messages = append(session.View.Messages, runtime.Message{ID: event.EventID, Role: "event", Text: event.Summary, Status: string(event.Kind)})
	}
}

func (a *Application) DomainEvents(sessionID string) []events.Envelope {
	a.mu.Lock()
	defer a.mu.Unlock()
	return queryCopy(domainEvents(a.state, sessionID))
}

func domainEvents(state State, sessionID string) []events.Envelope {
	out := []events.Envelope{}
	for _, event := range state.DomainEvents {
		if sessionID == "" || event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, out[i].OccurredAt)
		right, _ := time.Parse(time.RFC3339Nano, out[j].OccurredAt)
		if left.Equal(right) {
			return out[i].EventID < out[j].EventID
		}
		return left.Before(right)
	})
	return out
}
