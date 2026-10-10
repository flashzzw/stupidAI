package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"codex-netcap/internal/config"
)

func opencodeHistory(ctx context.Context, s *opencodeServer, id string) ([]opencodeMessage, error) {
	out := []opencodeMessage{}
	seen := map[string]bool{}
	cursor := ""
	for n := 0; n < 1000; n++ {
		path := "/session/" + url.PathEscape(id) + "/message?limit=100"
		if cursor != "" {
			path += "&before=" + url.QueryEscape(cursor)
		}
		var page []opencodeMessage
		if err := s.call(ctx, "GET", path, nil, &page); err != nil {
			return nil, err
		}
		next := ""
		for _, message := range page {
			if message.Info.ID == "" || message.Info.SessionID != id {
				return nil, fmt.Errorf("OpenCode 消息页身份不完整")
			}
			if next == "" || message.Info.ID < next {
				next = message.Info.ID
			}
			if !seen[message.Info.ID] {
				seen[message.Info.ID] = true
				out = append(out, message)
			}
		}
		if len(page) < 100 {
			sort.SliceStable(out, func(i, j int) bool {
				if out[i].Info.Time.Created == out[j].Info.Time.Created {
					return out[i].Info.ID < out[j].Info.ID
				}
				return out[i].Info.Time.Created < out[j].Info.Time.Created
			})
			return out, nil
		}
		if next == cursor || next == "" {
			return nil, fmt.Errorf("OpenCode 历史分页没有推进")
		}
		cursor = next
	}
	return nil, fmt.Errorf("OpenCode 历史分页不完整")
}

func opencodeTurns(messages []opencodeMessage, stopOnDeny bool) []Turn {
	turns := []Turn{}
	byID := map[string]int{}
	for _, message := range messages {
		if message.Info.Role == "user" {
			byID[message.Info.ID] = len(turns)
			turns = append(turns, Turn{ID: message.Info.ID, Status: "inProgress"})
		}
	}
	for _, message := range messages {
		if message.Info.Role != "assistant" || string(message.Info.Summary) == "true" || message.Info.Time.Completed == nil {
			continue
		}
		n, ok := byID[message.Info.ParentID]
		if !ok {
			continue
		}
		if message.Info.Error != nil {
			turns[n].Status = "failed"
			turns[n].Failure = opencodeFailure(message.Info.Error.Name, message.Info.Error.Data, *message.Info.Time.Completed)
			if message.Info.Error.Name == "MessageAbortedError" {
				turns[n].Status = "interrupted"
			}
		} else if message.Info.Finish != "" && message.Info.Finish != "tool-calls" && message.Info.Finish != "unknown" {
			turns[n].Status = "completed"
			turns[n].Failure = nil
		} else if stopOnDeny {
			// v1.18.32 stops on a rejected permission/question while retaining
			// finish=tool-calls. Other tool errors may continue the model loop.
			for _, part := range message.Parts {
				if part.Type == "tool" && part.State.Status == "error" && (part.State.Error == "The user rejected permission to use this specific tool call." || part.State.Error == "The user dismissed this question") {
					turns[n].Status = "interrupted"
				}
			}
		}
		if turns[n].Status != "inProgress" {
			turns[n].CompletedAt = time.UnixMilli(*message.Info.Time.Completed).UTC().Format(time.RFC3339Nano)
		}
	}
	return turns
}

func (c *OpenCode) observe(ctx context.Context, scope Scope) (View, error) {
	v := View{NativeID: scope.NativeID, Status: "idle", Messages: []Message{}, Pending: []Pending{}, Executions: []Execution{}, Evidence: Evidence{Source: "opencode.rest+SSE", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, Detail: "原生消息、状态、权限/问题、子会话与自有 server PTY 完整核对"}}
	s, err := c.client(ctx, scope)
	if err != nil {
		return v, err
	}
	var native opencodeSession
	if err = s.call(ctx, "GET", "/session/"+url.PathEscape(scope.NativeID), nil, &native); err != nil {
		return v, err
	}
	if native.ID != scope.NativeID || native.Directory != scope.Cwd {
		return v, fmt.Errorf("OpenCode 原生会话身份/目录不匹配")
	}
	if native.Model.ID != "" && (native.Model.ID != scope.Route.Model || native.Model.Provider != scope.Route.Provider) {
		return v, fmt.Errorf("OpenCode 会话模型/供应商已变化")
	}
	messages, err := opencodeHistory(ctx, s, scope.NativeID)
	if err != nil {
		return v, err
	}
	status := map[string]opencodeStatus{}
	if err = s.call(ctx, "GET", "/session/status", nil, &status); err != nil {
		return v, err
	}
	active := status[scope.NativeID].Type
	if active == "busy" || active == "retry" {
		v.Status = "running"
	}
	var permissions, questions []opencodeRequest
	if err = s.call(ctx, "GET", "/permission", nil, &permissions); err != nil {
		return v, err
	}
	if err = s.call(ctx, "GET", "/question", nil, &questions); err != nil {
		return v, err
	}
	v.Turns = opencodeTurns(messages, s.stopOnDeny)
	turnState := map[string]string{}
	parents := map[string]string{}
	for _, turn := range v.Turns {
		turnState[turn.ID] = turn.Status
		if turn.Status == "inProgress" && v.Status == "idle" {
			v.Status = "unknown"
			v.Evidence.Complete = false
			v.Evidence.Detail = "原生消息未见终态，空 status map 不能证明已结束"
		}
	}
	s.mu.Lock()
	for _, message := range messages {
		turnID := message.Info.ID
		if message.Info.Role == "assistant" {
			turnID = message.Info.ParentID
			parents[message.Info.ID] = turnID
		}
		for _, part := range message.Parts {
			role, body := message.Info.Role, part.Text
			if part.Type == "tool" {
				if part.Tool == "bash" {
					var input struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(part.State.Input, &input) == nil {
						v.Tools = append(v.Tools, ToolResult{ID: part.ID, TurnID: turnID, Command: input.Command, Output: part.State.Output, ExitCode: part.State.Metadata.Exit, Complete: part.State.Status == "completed" && !part.State.Metadata.Truncated})
					}
				}
				role = "tool"
				body = part.Tool + "\n" + string(part.State.Input)
				if part.State.Output != "" {
					body = part.State.Output
				}
				if part.State.Error != "" {
					body = part.State.Error
				}
				if part.State.Status == "pending" || part.State.Status == "running" {
					v.Executions = append(v.Executions, Execution{ID: part.ID, Kind: "tool", Status: part.State.Status})
				}
			}
			if part.Type == "text" && turnState[turnID] == "inProgress" {
				if streamed, ok := s.parts[scope.NativeID][part.ID]; ok && strings.HasPrefix(streamed.Text, body) {
					body = streamed.Text
				}
			}
			if body != "" {
				v.Messages = append(v.Messages, Message{ID: part.ID, TurnID: turnID, Role: role, Text: body, Status: turnState[turnID], Operation: opencodeOperation(turnID)})
			}
		}
	}
	if !s.connected {
		v.Evidence.Detail += "；SSE 断开时以本次 REST 全量核对补齐"
	}
	v.Evidence.Detail += fmt.Sprintf("；已接收 %d 个 SSE 事件", s.events)
	s.mu.Unlock()
	for _, request := range append(permissions, questions...) {
		if request.SessionID != scope.NativeID {
			continue
		}
		id, _ := json.Marshal(request.ID)
		raw, _ := json.Marshal(request)
		p := Pending{ID: scope.Instance.ID + "/" + s.record.Epoch + "/" + request.ID, NativeID: scope.NativeID, NativeTurn: parents[request.Tool.MessageID], Kind: request.Permission,
			ConnectionID: s.record.Epoch, RequestID: id, Request: raw, Status: "pending", ResponseKind: "approval", Title: request.Permission + "\n" + strings.Join(request.Patterns, "\n"), Options: []Option{}, Questions: []Question{}}
		if len(request.Questions) > 0 {
			p.ResponseKind = "user_input"
			p.Kind = "question"
			p.Title = "需要你的回答"
			for n, q := range request.Questions {
				question := Question{ID: fmt.Sprintf("question-%d", n), Text: q.Text, Header: q.Header, Multiple: q.Multiple, AllowCustom: q.Custom, Options: []Option{}}
				for m, o := range q.Options {
					value, _ := json.Marshal(o.Label)
					question.Options = append(question.Options, Option{ID: fmt.Sprintf("option-%d", m), Label: o.Label, Description: o.Description, Value: value})
				}
				p.Questions = append(p.Questions, question)
			}
			v.Status = "waiting_input"
		} else {
			p.Options = []Option{{ID: "once", Label: "允许本次", Value: json.RawMessage(`"once"`)}, {ID: "always", Label: "允许并记住规则", Value: json.RawMessage(`"always"`)}, {ID: "reject", Label: "拒绝本次", Value: json.RawMessage(`"reject"`)}}
			details, _ := json.MarshalIndent(request, "", "  ")
			p.Details = string(details)
			v.Status = "waiting_approval"
		}
		v.Pending = append(v.Pending, p)
	}
	work, err := opencodeChildren(ctx, s, scope.NativeID, status)
	if err != nil {
		v.Evidence.Complete = false
		v.Evidence.Detail = "子会话核对不完整: " + err.Error()
	} else {
		v.Executions = append(v.Executions, work...)
		for _, execution := range work {
			if execution.Status == "unknown" {
				v.Evidence.Complete = false
				v.Evidence.Detail = "原生子会话没有完整终态证据"
				if v.Status == "idle" {
					v.Status = "unknown"
				}
			}
		}
	}
	var ptys []opencodePty
	if err = s.call(ctx, "GET", "/pty", nil, &ptys); err != nil {
		v.Evidence.Complete = false
		v.Evidence.Detail = "PTY 核对不完整"
	} else {
		// Native PTY metadata has no session ID. Retain a conservative server
		// reservation rather than inventing ownership for a particular session.
		for _, pty := range ptys {
			if pty.Status == "running" {
				v.Executions = append(v.Executions, Execution{ID: pty.ID, Kind: "server_pty", Status: "running"})
			}
		}
		retained, err := s.reconcilePTYs(ptys)
		if err != nil {
			v.Evidence.Complete = false
			v.Evidence.Detail = err.Error()
		} else if len(retained) > 0 {
			v.Executions = append(v.Executions, retained...)
			v.Evidence.Complete = false
			v.Evidence.Detail = "旧 server 的 PTY 进程组仍在运行；新 server 空列表不代表原执行已结束"
			if v.Status == "idle" {
				v.Status = "unknown"
			}
		}
	}
	if v.Status == "idle" && len(v.Executions) > 0 {
		v.Status = "running"
	}
	if !v.Evidence.Complete && v.Status == "idle" {
		v.Status = "unknown"
	}
	v.ExecutionClear = v.Evidence.Complete && v.Status == "idle" && len(v.Executions) == 0
	v.TerminalCommand = "OPENCODE_SERVER_PASSWORD=$(cat -- " + config.ShellQuote(s.root+"/server-token") + ") " + config.ShellQuote(scope.Instance.Executable) + " attach " + config.ShellQuote(s.record.URL) + " --session " + config.ShellQuote(scope.NativeID)
	return v, nil
}

func opencodeChildren(ctx context.Context, s *opencodeServer, id string, status map[string]opencodeStatus) ([]Execution, error) {
	work := []Execution{}
	queue := []string{id}
	seen := map[string]bool{id: true}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		var children []opencodeSession
		if err := s.call(ctx, "GET", "/session/"+url.PathEscape(parent)+"/children", nil, &children); err != nil {
			return work, err
		}
		for _, child := range children {
			if seen[child.ID] || len(seen) >= 1000 {
				return work, fmt.Errorf("子会话树重复或不完整")
			}
			seen[child.ID] = true
			queue = append(queue, child.ID)
			history, err := opencodeHistory(ctx, s, child.ID)
			if err != nil {
				return work, err
			}
			running := status[child.ID].Type == "busy" || status[child.ID].Type == "retry"
			unknown := len(history) == 0
			for _, turn := range opencodeTurns(history, s.stopOnDeny) {
				if turn.Status == "inProgress" {
					unknown = true
				}
			}
			for _, message := range history {
				for _, part := range message.Parts {
					if part.Type == "tool" && (part.State.Status == "pending" || part.State.Status == "running") {
						running = true
					}
				}
			}
			if running {
				work = append(work, Execution{ID: child.ID, Kind: "child_session", Status: "running"})
			} else if unknown {
				work = append(work, Execution{ID: child.ID, Kind: "child_session", Status: "unknown"})
			}
		}
	}
	return work, nil
}
