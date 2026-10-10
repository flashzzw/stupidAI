package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"codex-netcap/internal/events"
	"codex-netcap/internal/runtime"
)

type SessionRead struct {
	Sequence       uint64            `json:"sequence"`
	Session        Session           `json:"session"`
	Operations     []Operation       `json:"operations"`
	TotalMessages  int               `json:"total_messages"`
	PreviousCursor string            `json:"previous_cursor,omitempty"`
	Events         []events.Envelope `json:"events"`
}

type messageCursor struct{ Session, Turn, Message string }

func queryCopy[T any](value T) T {
	b, _ := json.Marshal(value)
	var result T
	_ = json.Unmarshal(b, &result)
	return result
}

// Workspace returns metadata for lists. Chat bodies, native request payloads
// and operation histories are fetched only for the selected session.
func (a *Application) Workspace() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := Snapshot{Sequence: a.state.Sequence, Sessions: []Session{}, Operations: []Operation{}, Projects: a.config.Projects, Routes: append([]runtime.Route{}, a.config.Routes...)}
	out.Instances = a.instances
	out.CapabilityAssets = a.capabilityAssets()
	out.RouteStatuses = a.routeStatuses()
	out.Projects = a.projects()
	for _, session := range a.state.Sessions {
		access := a.sessionAccess(session)
		session.Access = &access
		session.View = runtime.View{NativeID: session.Scope.NativeID, Status: session.Status}
		session.ExecutionSnapshot = nil
		session.PendingExecutionChange = nil
		session.ExecutionHistory = nil
		out.Sessions = append(out.Sessions, session)
	}
	sort.Slice(out.Sessions, func(i, j int) bool {
		left, right := out.Sessions[i].ActivityAt, out.Sessions[j].ActivityAt
		if left == "" {
			left = out.Sessions[i].UpdatedAt
		}
		if right == "" {
			right = out.Sessions[j].UpdatedAt
		}
		a, _ := time.Parse(time.RFC3339Nano, left)
		b, _ := time.Parse(time.RFC3339Nano, right)
		return a.After(b)
	})
	if a.fatal != nil {
		out.Error = a.fatal.Error()
	}
	return queryCopy(out)
}

// ReadSession pages by message identity rather than offset, so newer tokens or
// appended turns cannot shift the boundary of an older-page request.
func (a *Application) ReadSession(id, before string, limit int) (SessionRead, error) {
	if limit < 1 || limit > 200 {
		return SessionRead{}, fmt.Errorf("历史页大小必须在 1–200 之间")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.state.Sessions[id]
	if !ok {
		return SessionRead{}, fmt.Errorf("会话不存在")
	}
	access := a.sessionAccess(session)
	session.Access = &access
	messages := session.View.Messages
	end := len(messages)
	if before != "" {
		var cursor messageCursor
		if len(before) > 2000 {
			return SessionRead{}, fmt.Errorf("历史游标过长")
		}
		b, err := base64.RawURLEncoding.DecodeString(before)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.Session != id {
			return SessionRead{}, fmt.Errorf("历史游标不属于当前会话")
		}
		end = -1
		for n, message := range messages {
			if message.ID == cursor.Message && message.TurnID == cursor.Turn {
				end = n
				break
			}
		}
		if end < 0 {
			return SessionRead{}, fmt.Errorf("原生历史已变化，请重新加载最近消息")
		}
	}
	start := max(0, end-limit)
	out := SessionRead{Sequence: a.state.Sequence, Session: session, Operations: []Operation{}, TotalMessages: len(messages), Events: domainEvents(a.state, id)}
	if len(out.Events) > 100 {
		out.Events = out.Events[len(out.Events)-100:]
	}
	out.Session.View.Messages = append([]runtime.Message{}, messages[start:end]...)
	if start > 0 {
		message := messages[start]
		b, _ := json.Marshal(messageCursor{Session: id, Turn: message.TurnID, Message: message.ID})
		out.PreviousCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	for _, operation := range a.state.Operations {
		if operation.SessionID == id {
			out.Operations = append(out.Operations, operation)
		}
	}
	sort.Slice(out.Operations, func(i, j int) bool { return out.Operations[i].CreatedAt < out.Operations[j].CreatedAt })
	return queryCopy(out), nil
}

func (a *Application) Sequence() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Sequence
}
