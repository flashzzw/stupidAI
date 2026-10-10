package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"codex-netcap/internal/codex"
)

// Legacy console threads retain asynchronous question messages in rollout
// history even when the in-memory turns/list projection omits them.
func consoleTurns(ctx context.Context, p codex.Client, id string) ([]codex.Turn, error) {
	var out struct {
		Thread codex.Thread `json:"thread"`
	}
	err := p.Call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": true}, &out)
	if err != nil {
		var rpc *codex.RPCError
		if errors.As(err, &rpc) && rpc.Code == -32600 && (strings.Contains(rpc.Message, "no rollout found") || strings.Contains(rpc.Message, "is not materialized yet")) {
			return codex.Turns(ctx, p, id, 0, true)
		}
		return nil, err
	}
	for _, turn := range out.Thread.Turns {
		if turn.ItemsView != "" && turn.ItemsView != "full" {
			return nil, fmt.Errorf("原生会话历史不完整")
		}
	}
	slices.Reverse(out.Thread.Turns)
	return out.Thread.Turns, nil
}

func (c *Codex) recordAsync(scope Scope, turn codex.Turn, item codex.Item) {
	id := scope.Instance.ID + "/async/" + scope.NativeID + "/" + turn.ID + "/" + item.ID
	if _, exists := c.pending[id]; exists {
		return
	}
	request, _ := json.Marshal(item)
	requestID, _ := json.Marshal(item.ID)
	pending := Pending{ID: id, NativeID: scope.NativeID, NativeTurn: turn.ID, RequestID: requestID, Kind: "agentMessage.questions", Request: request,
		ResponseKind: "message_input", Title: "需要你的回答", Status: "pending", Options: []Option{}, Questions: []Question{}}
	for n, q := range item.Questions {
		question := Question{ID: fmt.Sprintf("question-%d", n), Text: q.Title, Options: []Option{}}
		for m, label := range q.Options {
			value, _ := json.Marshal(label)
			question.Options = append(question.Options, Option{ID: fmt.Sprintf("option-%d", m), Label: label, Value: value})
		}
		pending.Questions = append(pending.Questions, question)
	}
	c.pending[id] = pending
}

func (c *Codex) expireAsync(scope Scope, turns []codex.Turn) {
	for id, pending := range c.pending {
		if pending.NativeID != scope.NativeID || pending.ResponseKind != "message_input" || !strings.HasPrefix(id, scope.Instance.ID+"/") {
			continue
		}
		var itemID string
		_ = json.Unmarshal(pending.RequestID, &itemID)
		after := false
		for _, turn := range turns {
			for _, item := range turn.Items {
				if turn.ID == pending.NativeTurn && item.ID == itemID {
					after = true
					continue
				}
				if after && item.Type == "userMessage" {
					pending.Status = "expired"
					if pending.AnswerOperation != "" && item.ClientID == pending.AnswerOperation {
						pending.Status = "resolved"
					}
				}
			}
		}
		c.pending[id] = pending
	}
}

func asyncAnswer(in Intent) (string, error) {
	if in.Pending == nil || len(in.Answers) != len(in.Pending.Questions) {
		return "", fmt.Errorf("回答与原问题不匹配")
	}
	lines := []string{"对原生问题 " + in.Pending.ID + " 的回答："}
	for _, question := range in.Pending.Questions {
		answer, ok := in.Answers[question.ID]
		if !ok || len(answer) == 0 {
			return "", fmt.Errorf("缺少原问题的回答")
		}
		lines = append(lines, question.Text, strings.Join(answer, "\n"))
	}
	return strings.Join(lines, "\n"), nil
}

func (c *Codex) respondAsync(ctx context.Context, p codexConnection, in Intent) (Receipt, error) {
	receipt := Receipt{NativeID: in.Scope.NativeID}
	if _, _, err := codexRecoveryPolicy(in.Scope); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	view, err := c.observeWithHistory(ctx, in.Scope, consoleTurns, in.Pending.ID)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	live, ok := c.pending[in.Pending.ID]
	if !ok || live.Status != "pending" || live.NativeID != in.Scope.NativeID {
		return receipt, &NotSent{Reason: "原问题已被后续输入取代或无法核对"}
	}
	text, err := asyncAnswer(in)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	params := map[string]any{"threadId": in.Scope.NativeID, "clientUserMessageId": in.ID, "input": []map[string]any{{"type": "text", "text": text}}}
	active := ""
	for _, turn := range view.Turns {
		if turn.Status == "inProgress" {
			active = turn.ID
		}
	}
	if active != "" {
		if codexMixedPlugins(in.Scope.PluginSelections) && !view.pluginPolicy.matches(in.Scope.PluginSelections) {
			return receipt, &NotSent{Reason: "整包策略尚未核对，保留原问题"}
		}
		if active != live.NativeTurn {
			return receipt, &NotSent{Reason: "原问题不属于当前轮次"}
		}
		params["expectedTurnId"] = active
		var out struct {
			ID string `json:"turnId"`
		}
		err = p.Call(ctx, "turn/steer", params, &out)
		receipt.NativeTurn = out.ID
	} else {
		if !codexIdleForPermission(view, live.ID) || view.PermissionEvidence == nil || !view.PermissionEvidence.Verified {
			return receipt, &NotSent{Reason: "回答前原生权限、队列或后台执行尚未核对；保留原问题"}
		}
		var out struct {
			Turn codex.Turn `json:"turn"`
		}
		if err := c.addInputPluginPolicy(ctx, p, in.Scope, params); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
		delete(c.creationPolicies, in.Scope.Instance.ID+"/"+in.Scope.NativeID)
		err = p.Call(ctx, "turn/start", params, &out)
		receipt.NativeTurn = out.Turn.ID
	}
	if err == nil {
		live.Status = "submitted"
		live.AnswerOperation = in.ID
		c.pending[live.ID] = live
	}
	return receipt, err
}
