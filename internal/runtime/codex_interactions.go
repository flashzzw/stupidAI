package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"codex-netcap/internal/codex"
)

func pendingFromRequest(instance Instance, connection string, event codex.Message) Pending {
	var data struct {
		ThreadID  string            `json:"threadId"`
		TurnID    string            `json:"turnId"`
		Command   string            `json:"command"`
		Reason    string            `json:"reason"`
		Decisions []json.RawMessage `json:"availableDecisions"`
		Blocking  bool              `json:"isBlocking"`
		Questions []struct {
			ID      string `json:"id"`
			Header  string `json:"header"`
			Text    string `json:"question"`
			Secret  bool   `json:"isSecret"`
			Options []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(event.Params, &data)
	p := Pending{ID: instance.ID + "/" + connection + "/" + string(event.ID), NativeID: data.ThreadID, NativeTurn: data.TurnID,
		ConnectionID: connection, RequestID: event.ID, Kind: event.Method, Request: event.Params, Status: "pending", ResponseKind: "unsupported", Title: data.Reason,
		Options: []Option{}, Questions: []Question{}}
	switch event.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		p.ResponseKind = "approval"
		if data.Command != "" {
			p.Title = data.Command
		}
		if p.Title == "" {
			p.Title = "原生文件变更审批"
		}
		if len(data.Decisions) == 0 {
			data.Decisions = []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"acceptForSession"`), json.RawMessage(`"decline"`), json.RawMessage(`"cancel"`)}
		}
		labels := map[string]string{"accept": "允许本次", "acceptForSession": "允许此会话", "decline": "拒绝本次", "cancel": "拒绝并中止"}
		for n, value := range data.Decisions {
			label := string(value)
			description := ""
			var key string
			if json.Unmarshal(value, &key) == nil {
				label = labels[key]
				if label == "" {
					label = key
				}
			} else {
				var rule map[string]json.RawMessage
				if json.Unmarshal(value, &rule) == nil {
					if _, ok := rule["acceptWithExecpolicyAmendment"]; ok {
						label = "允许并添加命令规则"
						description = "原生端将保存此次提供的命令规则；后续匹配命令可免于再次审批。"
					} else if _, ok := rule["applyNetworkPolicyAmendment"]; ok {
						label = "应用网络访问规则"
						description = "按原生端提供的主机与访问策略更新网络规则。"
					}
				}
			}
			p.Options = append(p.Options, Option{ID: fmt.Sprintf("decision-%d", n), Label: label, Description: description, Value: value})
		}
	case "item/tool/requestUserInput":
		p.ResponseKind = "user_input"
		if !data.Blocking {
			p.ResponseKind = "async_input"
		}
		p.Title = "需要你的回答"
		for _, q := range data.Questions {
			question := Question{ID: q.ID, Header: q.Header, Text: q.Text, Secret: q.Secret, Options: []Option{}}
			for n, choice := range q.Options {
				value, _ := json.Marshal(choice.Label)
				question.Options = append(question.Options, Option{ID: fmt.Sprintf("option-%d", n), Label: choice.Label, Description: choice.Description, Value: value})
			}
			p.Questions = append(p.Questions, question)
		}
	case "mcpServer/elicitation/request":
		// Only the native single-call approval shape is supported. Arbitrary
		// elicitation forms, URLs and persistent grants need their own UI.
		var request struct {
			Mode    string `json:"mode"`
			Message string `json:"message"`
			Meta    struct {
				Kind       string          `json:"codex_approval_kind"`
				ToolParams json.RawMessage `json:"tool_params"`
			} `json:"_meta"`
			Schema map[string]json.RawMessage `json:"requestedSchema"`
		}
		if json.Unmarshal(event.Params, &request) != nil {
			break
		}
		var kind string
		var properties map[string]json.RawMessage
		if request.Mode != "form" || request.Meta.Kind != "mcp_tool_call" || len(request.Schema) != 2 ||
			json.Unmarshal(request.Schema["type"], &kind) != nil || kind != "object" ||
			json.Unmarshal(request.Schema["properties"], &properties) != nil || properties == nil || len(properties) != 0 {
			break
		}
		p.ResponseKind, p.Title = "approval", request.Message
		if len(request.Meta.ToolParams) > 0 {
			p.Details = string(request.Meta.ToolParams)
		}
		p.Options = []Option{
			{ID: "decision-0", Label: "允许本次", Value: json.RawMessage(`{"action":"accept","content":{}}`)},
			{ID: "decision-1", Label: "拒绝本次", Value: json.RawMessage(`{"action":"decline"}`)},
			{ID: "decision-2", Label: "取消此次审批", Value: json.RawMessage(`{"action":"cancel"}`)},
		}
	}
	return p
}

func sortPending(p []Pending) { sort.Slice(p, func(i, j int) bool { return p[i].ID < p[j].ID }) }

func (c *Codex) respond(ctx context.Context, p codexConnection, in Intent) (Receipt, error) {
	receipt := Receipt{NativeID: in.Scope.NativeID}
	if in.Pending == nil {
		return receipt, &NotSent{Reason: "缺少原生请求引用"}
	}
	if in.Pending.ResponseKind == "message_input" {
		return c.respondAsync(ctx, p, in)
	}
	request := in.Pending
	// A read barrier lets the reader consume native resolved notifications before
	// testing the connection-local request set. It is not a new permission request.
	if _, err := codex.Read(ctx, p, in.Scope.NativeID); err != nil {
		return receipt, &NotSent{Reason: "回复前原生核对失败: " + err.Error()}
	}
	c.collect(in.Scope.Instance, p)
	live, exists := c.pending[request.ID]
	_, valid := p.PendingRequest(request.RequestID)
	if !exists || !valid || live.Status != "pending" || request.ConnectionID != p.ConnectionID() || live.NativeID != in.Scope.NativeID || live.NativeTurn != request.NativeTurn {
		return receipt, &NotSent{Reason: "原请求已处理、过期或无法在当前连接确认"}
	}
	var result any
	switch live.ResponseKind {
	case "approval":
		var choice json.RawMessage
		for _, option := range live.Options {
			if option.ID == in.OptionID {
				choice = option.Value
			}
		}
		if len(choice) == 0 {
			return receipt, &NotSent{Reason: "所选选项不属于原请求"}
		}
		result = map[string]any{"decision": choice}
		if live.Kind == "mcpServer/elicitation/request" {
			result = choice
		}
	case "user_input", "async_input":
		answers := map[string]any{}
		for _, question := range live.Questions {
			values, ok := in.Answers[question.ID]
			if !ok || len(values) == 0 {
				return receipt, &NotSent{Reason: "缺少原问题的回答"}
			}
			answers[question.ID] = map[string]any{"answers": values}
		}
		if len(answers) != len(in.Answers) {
			return receipt, &NotSent{Reason: "回答包含未知问题 ID"}
		}
		result = map[string]any{"answers": answers}
	default:
		return receipt, &NotSent{Reason: "此原生交互尚不支持网页回复，请转人工"}
	}
	if err := p.Respond(ctx, request.RequestID, result); err != nil {
		if err == codex.ErrRequestExpired || err == context.Canceled || err == context.DeadlineExceeded {
			return receipt, &NotSent{Reason: err.Error()}
		}
		return receipt, err
	}
	live.Status = "submitted"
	c.pending[live.ID] = live
	return receipt, nil
}

func verifyOwnedTurn(ctx context.Context, p codexConnection, in Intent) error {
	turns, err := codex.Turns(ctx, p, in.Scope.NativeID, 0, true)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if turn.ID != in.NativeTurn {
			continue
		}
		owned := false
		for _, item := range turn.Items {
			if item.Type != "userMessage" {
				continue
			}
			if !slices.Contains(in.OwnedInputs, item.ClientID) {
				return fmt.Errorf("该轮次含人工输入，不能自动中断")
			}
			owned = true
		}
		if !owned {
			return fmt.Errorf("没有该轮次的自有输入证据")
		}
		if turn.Status != "inProgress" {
			return fmt.Errorf("目标轮次已结束；仍需核对后台执行")
		}
		return nil
	}
	return fmt.Errorf("未找到目标轮次，不能推断可中断")
}

// executions only queries the selected session tree. Unknown or unsupported
// queries remain incomplete evidence; they are never interpreted as no work.
func executions(ctx context.Context, p codex.Client, threadID string, unloaded ...bool) ([]Execution, error) {
	out, err := threadExecutions(ctx, p, threadID, unloaded...)
	if err != nil {
		return out, err
	}
	cursor := ""
	for page := 0; page < 1000; page++ {
		var children struct {
			Data []codex.Thread `json:"data"`
			Next string         `json:"nextCursor"`
		}
		params := map[string]any{"ancestorThreadId": threadID, "limit": 100, "sourceKinds": []string{"subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther"}}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err = p.Call(ctx, "thread/list", params, &children); err != nil {
			return out, err
		}
		for _, child := range children.Data {
			if child.Status.Type == "active" {
				out = append(out, Execution{ID: child.ID, Kind: "child_session", Status: "running"})
				continue
			}
			turns, e := codex.Turns(ctx, p, child.ID, 0, true)
			if e != nil {
				return out, e
			}
			if len(turns) == 0 || turns[0].Status == "inProgress" {
				out = append(out, Execution{ID: child.ID, Kind: "child_session", Status: "unknown"})
			}
			// Idle is a gap between turns, not proof that a child's queue,
			// native Goal or background terminals have stopped.
			work, e := threadExecutions(ctx, p, child.ID)
			if e != nil {
				return out, e
			}
			for _, execution := range work {
				execution.ID = child.ID + "/" + execution.ID
				execution.Kind = "child_" + execution.Kind
				out = append(out, execution)
			}
		}
		if children.Next == "" {
			break
		}
		if children.Next == cursor || page == 999 {
			return out, fmt.Errorf("子会话分页不完整")
		}
		cursor = children.Next
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].Kind+out[i].ID, out[j].Kind+out[j].ID) < 0 })
	return out, nil
}

func threadExecutions(ctx context.Context, p codex.Client, threadID string, unloaded ...bool) ([]Execution, error) {
	out := []Execution{}
	queue, err := codex.Queue(ctx, p, threadID)
	if err != nil {
		return out, err
	}
	for _, q := range queue {
		out = append(out, Execution{ID: q.ID, Kind: "queue", Status: "queued"})
	}
	goal, err := codex.ReadGoal(ctx, p, threadID)
	if err != nil {
		return out, err
	}
	if goal != nil && goal.Status == "active" {
		out = append(out, Execution{ID: threadID, Kind: "native_goal", Status: "running"})
	}
	cursor := ""
	for page := 0; page < 1000; page++ {
		var terminals struct {
			Data []struct {
				ID string `json:"processId"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		params := map[string]any{"threadId": threadID, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err = p.Call(ctx, "thread/backgroundTerminals/list", params, &terminals); err != nil {
			// Only a previously verified idle configuration hold with identical
			// history may interpret the absent unloaded runtime handle. A generic
			// RPC failure or another thread's absence remains incomplete evidence.
			var rpc *codex.RPCError
			if len(unloaded) > 0 && unloaded[0] && page == 0 && errors.As(err, &rpc) && rpc.Code == -32600 && rpc.Message == "thread not found: "+threadID {
				thread, readErr := codex.Read(ctx, p, threadID)
				if readErr == nil && thread.ID == threadID && thread.Status.Type == "notLoaded" {
					return out, nil
				}
			}
			return out, err
		}
		for _, terminal := range terminals.Data {
			out = append(out, Execution{ID: terminal.ID, Kind: "terminal", Status: "running"})
		}
		if terminals.Next == "" {
			break
		}
		if terminals.Next == cursor || page == 999 {
			return out, fmt.Errorf("后台终端分页不完整")
		}
		cursor = terminals.Next
	}
	return out, nil
}
