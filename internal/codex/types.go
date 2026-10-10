package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type Status struct {
	Type  string   `json:"type"`
	Flags []string `json:"activeFlags"`
}
type Thread struct {
	Path      string `json:"path"`
	Ephemeral bool   `json:"ephemeral"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Provider  string `json:"modelProvider"`
	Effort    string `json:"reasoningEffort"`
	Status    Status `json:"status"`
	UpdatedAt int64  `json:"updatedAt"`
	Parent    string `json:"parentThreadId"`
	Source    string `json:"threadSource"`
	Turns     []Turn `json:"turns,omitempty"`
}
type Item struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	ClientID  string          `json:"clientId"`
	Text      string          `json:"text"`
	Phase     string          `json:"phase"`
	Command   string          `json:"command"`
	ExitCode  *int            `json:"exitCode"`
	Output    string          `json:"aggregatedOutput"`
	Delivery  string          `json:"delivery,omitempty"`
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Status    string          `json:"status,omitempty"`
	MCPResult json.RawMessage `json:"result,omitempty"`
	MCPError  json.RawMessage `json:"error,omitempty"`
	PluginID  json.RawMessage `json:"pluginId,omitempty"`
	Questions []struct {
		Title   string   `json:"title"`
		Options []string `json:"options"`
	} `json:"questions,omitempty"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
}
type TurnError struct {
	Message string          `json:"message"`
	Info    json.RawMessage `json:"codexErrorInfo"`
}

type NativeGoal struct {
	ThreadID    string `json:"threadId"`
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	TokenBudget *int64 `json:"tokenBudget"`
	TokensUsed  int64  `json:"tokensUsed"`
}

func ReadGoal(ctx context.Context, c Client, id string) (*NativeGoal, error) {
	var out struct {
		Goal *NativeGoal `json:"goal"`
	}
	err := c.Call(ctx, "thread/goal/get", map[string]any{"threadId": id}, &out)
	return out.Goal, err
}

type Turn struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	StartedAt   int64      `json:"startedAt"`
	CompletedAt int64      `json:"completedAt"`
	Items       []Item     `json:"items"`
	ItemsView   string     `json:"itemsView"`
	Error       *TurnError `json:"error"`
}

func (t Turn) Answer() string {
	var answer string
	for _, i := range t.Items {
		if i.Type == "agentMessage" && (i.Phase == "final_answer" || i.Phase == "") {
			answer = i.Text
		}
	}
	return answer
}
func (t Turn) HasInput(id string) bool {
	for _, i := range t.Items {
		if i.Type == "userMessage" && i.ClientID == id {
			return true
		}
	}
	return false
}
func (t Turn) HumanInput(ours func(string) bool) bool {
	for _, i := range t.Items {
		if i.Type == "userMessage" && !ours(i.ClientID) {
			return true
		}
	}
	return false
}
func (t Turn) Tools() bool {
	for _, i := range t.Items {
		switch i.Type {
		case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "webSearch", "collabAgentToolCall", "imageGeneration", "requestUserInput":
			return true
		}
	}
	return false
}

type QueueItem struct {
	ID       string `json:"id"`
	ClientID string `json:"clientUserMessageId"`
}
type Started struct {
	DisabledPluginIDs json.RawMessage `json:"disabledPluginIds,omitempty"`
	Thread            Thread          `json:"thread"`
	Model             string          `json:"model"`
	Provider          string          `json:"modelProvider"`
	ApprovalPolicy    json.RawMessage `json:"approvalPolicy,omitempty"`
	Sandbox           json.RawMessage `json:"sandbox,omitempty"`
}

func Read(ctx context.Context, c Client, id string) (Thread, error) {
	var result struct {
		Thread Thread `json:"thread"`
	}
	err := c.Call(ctx, "thread/read", map[string]any{"threadId": id}, &result)
	return result.Thread, err
}
func Loaded(ctx context.Context, c Client) ([]string, error) {
	var out []string
	cursor := ""
	for page := 0; page < 1000; page++ {
		var result struct {
			Data []string `json:"data"`
			Next string   `json:"nextCursor"`
		}
		p := map[string]any{}
		if cursor != "" {
			p["cursor"] = cursor
		}
		if err := c.Call(ctx, "thread/loaded/list", p, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Data...)
		if result.Next == "" {
			return out, nil
		}
		if result.Next == cursor {
			return nil, fmt.Errorf("loaded cursor did not advance")
		}
		cursor = result.Next
	}
	return nil, fmt.Errorf("loaded pagination limit")
}
func Queue(ctx context.Context, c Client, id string) ([]QueueItem, error) {
	var out []QueueItem
	cursor := ""
	for page := 0; page < 1000; page++ {
		var result struct {
			Data []QueueItem `json:"data"`
			Next string      `json:"nextCursor"`
		}
		p := map[string]any{"threadId": id, "limit": 100}
		if cursor != "" {
			p["cursor"] = cursor
		}
		if err := c.Call(ctx, "thread/queue/list", p, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Data...)
		if result.Next == "" {
			return out, nil
		}
		if result.Next == cursor {
			return nil, fmt.Errorf("queue cursor did not advance")
		}
		cursor = result.Next
	}
	return nil, fmt.Errorf("queue pagination limit")
}
func Goal(ctx context.Context, c Client, id string) (string, error) {
	var result struct {
		Goal *struct {
			Status string `json:"status"`
		} `json:"goal"`
	}
	err := c.Call(ctx, "thread/goal/get", map[string]any{"threadId": id}, &result)
	if result.Goal == nil {
		return "", err
	}
	return result.Goal.Status, err
}

// Turns never treats a truncated history as evidence that an operation was not
// sent. Callers keep unresolved operations blocked when a full scan fails.
func Turns(ctx context.Context, c Client, id string, after int64, all bool) ([]Turn, error) {
	var out []Turn
	cursor := ""
	for page := 0; page < 1000; page++ {
		var result struct {
			Data []Turn `json:"data"`
			Next string `json:"nextCursor"`
		}
		p := map[string]any{"threadId": id, "limit": 50, "sortDirection": "desc", "itemsView": "full"}
		if cursor != "" {
			p["cursor"] = cursor
		}
		if err := c.Call(ctx, "thread/turns/list", p, &result); err != nil {
			var rpc *RPCError
			if errors.As(err, &rpc) && rpc.Code == -32601 {
				return legacyTurns(ctx, c, id)
			}
			if errors.As(err, &rpc) && rpc.Code == -32600 && strings.Contains(rpc.Message, "is not materialized yet") {
				t, readErr := Read(ctx, c, id)
				if readErr == nil && t.Status.Type == "idle" {
					// A freshly created idle thread has no rollout until its first
					// submission. This is not evidence to resend an unknown operation.
					return nil, nil
				}
			}
			return nil, err
		}
		for _, t := range result.Data {
			if t.ItemsView != "" && t.ItemsView != "full" {
				items, err := Items(ctx, c, id, t.ID)
				if err != nil {
					return nil, err
				}
				t.Items = items
			}
			out = append(out, t)
		}
		if result.Next == "" || !all {
			return out, nil
		}
		if after > 0 && len(result.Data) > 0 {
			last := result.Data[len(result.Data)-1]
			if last.StartedAt > 0 && last.StartedAt < after {
				return out, nil
			}
		}
		if result.Next == cursor {
			return nil, fmt.Errorf("turn cursor did not advance")
		}
		cursor = result.Next
	}
	return nil, fmt.Errorf("turn pagination limit")
}
func Items(ctx context.Context, c Client, id, turn string) ([]Item, error) {
	var out []Item
	cursor := ""
	for page := 0; page < 1000; page++ {
		var result struct {
			Data []struct {
				Item Item `json:"item"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		p := map[string]any{"threadId": id, "turnId": turn, "limit": 100}
		if cursor != "" {
			p["cursor"] = cursor
		}
		if err := c.Call(ctx, "thread/items/list", p, &result); err != nil {
			var rpc *RPCError
			if errors.As(err, &rpc) && rpc.Code == -32601 {
				turns, e := legacyTurns(ctx, c, id)
				if e != nil {
					return nil, e
				}
				for _, t := range turns {
					if t.ID == turn {
						return t.Items, nil
					}
				}
				return nil, fmt.Errorf("legacy history does not contain turn %s", turn)
			}
			return nil, err
		}
		for _, entry := range result.Data {
			out = append(out, entry.Item)
		}
		if result.Next == "" {
			return out, nil
		}
		if result.Next == cursor {
			return nil, fmt.Errorf("item cursor did not advance")
		}
		cursor = result.Next
	}
	return nil, fmt.Errorf("item pagination limit")
}

func legacyTurns(ctx context.Context, c Client, id string) ([]Turn, error) {
	var result struct {
		Thread Thread `json:"thread"`
	}
	if err := c.Call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": true}, &result); err != nil {
		var rpc *RPCError
		if errors.As(err, &rpc) && rpc.Code == -32600 && strings.Contains(rpc.Message, "no rollout found") {
			t, e := Read(ctx, c, id)
			if e == nil && t.Status.Type == "idle" {
				return nil, nil
			}
		}
		return nil, err
	}
	turns := result.Thread.Turns
	// The legacy thread/read contract is chronological; callers consume the
	// newest turn first, just as with thread/turns/list(sortDirection=desc).
	slices.Reverse(turns)
	return turns, nil
}
