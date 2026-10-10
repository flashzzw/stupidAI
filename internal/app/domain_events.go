package app

import (
	"reflect"

	"codex-netcap/internal/events"
	"codex-netcap/internal/runtime"
)

func runtimeEnvelope(session Session, kind events.Kind, summary, at string) events.Envelope {
	return events.Envelope{Kind: kind, Source: "runtime/" + session.Scope.Instance.ID, SubjectType: "session", SubjectID: session.ID,
		SessionID: session.ID, InstanceID: session.Scope.Instance.ID, AgentKind: session.Scope.Instance.Kind, NativeSessionID: session.Scope.NativeID,
		OccurredAt: at, Summary: summary, NotificationSender: notificationSender(session.Scope.Instance.NotificationSender), Managed: true, Active: true}
}

func appendRuntimeEvent(state *State, event events.Envelope, delivery string) error {
	event, err := events.Identify(event, delivery)
	if err != nil {
		return err
	}
	return storeDomainEvent(state, event)
}

func collectManagedEvents(previous State, next *State, at string) error {
	for id, session := range next.Sessions {
		if session.ControlMode == "observed" {
			continue
		}
		old := previous.Sessions[id]
		if old.Status != session.Status || !reflect.DeepEqual(old.View.Messages, session.View.Messages) || !reflect.DeepEqual(old.View.Pending, session.View.Pending) {
			session.ActivityAt = at
			next.Sessions[id] = session
		}
		if session.Origin == "probe" {
			continue
		}
		oldMessages := map[string]bool{}
		for _, message := range old.View.Messages {
			if message.Role == "user" {
				oldMessages[message.TurnID+"/"+message.ID] = true
			}
		}
		for _, message := range session.View.Messages {
			if message.Role != "user" || oldMessages[message.TurnID+"/"+message.ID] {
				continue
			}
			for key, event := range next.DomainEvents {
				if event.SessionID == id && event.Managed {
					event.Active = false
					next.DomainEvents[key] = event
				}
			}
			event := runtimeEnvelope(session, events.Activity, "原生会话收到新输入", at)
			event.NativeTurnID, event.OperationID = message.TurnID, message.Operation
			if err := appendRuntimeEvent(next, event, "input/"+message.TurnID+"/"+message.ID); err != nil {
				return err
			}
		}
		pendingNow := map[string]runtime.Pending{}
		for _, pending := range session.View.Pending {
			if pending.Status == "pending" {
				pendingNow[pending.ID] = pending
			}
		}
		for key, event := range next.DomainEvents {
			if event.SessionID != id || !event.Managed || (event.Kind != events.PermissionRequired && event.Kind != events.InputRequired) {
				continue
			}
			found := false
			for _, pending := range pendingNow {
				if nativeRequestIdentity(pending.RequestID) == event.NativeRequestID && pending.ConnectionID == event.NativeConnectionID {
					found = true
				}
			}
			if !found {
				event.Active = false
				next.DomainEvents[key] = event
			}
		}
		for _, pending := range pendingNow {
			unchanged := false
			for _, p := range old.View.Pending {
				if p.ID == pending.ID && p.Status == "pending" {
					unchanged = true
				}
			}
			if unchanged {
				continue
			}
			kind := events.PermissionRequired
			if pending.ResponseKind == "user_input" || pending.ResponseKind == "async_input" || pending.ResponseKind == "message_input" {
				kind = events.InputRequired
			}
			event := runtimeEnvelope(session, kind, pending.Title, at)
			event.NativeTurnID, event.NativeRequestID, event.NativeConnectionID = pending.NativeTurn, nativeRequestIdentity(pending.RequestID), pending.ConnectionID
			for _, op := range next.Operations {
				if op.SessionID == id && createsInput(op.Intent) && op.Receipt.NativeTurn == pending.NativeTurn {
					event.OperationID = op.ID
				}
			}
			if err := appendRuntimeEvent(next, event, "request/"+pending.ID); err != nil {
				return err
			}
		}
	}
	for id, operation := range next.Operations {
		if previous.Operations[id].Status == operation.Status || !terminal(operation.Status) {
			continue
		}
		session, exists := next.Sessions[operation.SessionID]
		if !exists || session.ControlMode == "observed" || session.Origin == "probe" {
			continue
		}
		kind, summary := events.Kind(""), ""
		if operation.Intent.Kind == "create" && operation.Status == "succeeded" {
			kind, summary = events.SessionStarted, "原生会话已创建"
		}
		if createsInput(operation.Intent) {
			switch operation.Status {
			case "succeeded":
				kind, summary = events.TurnCompleted, "本轮执行已结束；任务验收独立记录"
			case "failed":
				kind, summary = events.RunFailed, "原生轮次失败，请查看原操作证据"
			case "interrupted":
				kind, summary = events.SessionStopped, "当前轮次已中止"
			}
		}
		if kind == "" {
			continue
		}
		event := runtimeEnvelope(session, kind, summary, at)
		event.NativeTurnID, event.OperationID = operation.Receipt.NativeTurn, operation.ID
		if err := appendRuntimeEvent(next, event, "operation/"+operation.ID); err != nil {
			return err
		}
	}
	return nil
}
