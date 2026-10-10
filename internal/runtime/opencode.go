package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type OpenCode struct {
	mu       sync.Mutex
	stateDir string
	servers  map[string]*opencodeServer
}

func NewOpenCode(stateDir string) *OpenCode {
	return &OpenCode{stateDir: stateDir, servers: map[string]*opencodeServer{}}
}

func (c *OpenCode) Describe(ctx context.Context, i Instance) (Description, error) {
	b, err := exec.CommandContext(ctx, i.Executable, "--version").Output()
	return describeVersion("opencode", strings.TrimSpace(string(b)), err), err
}

func (c *OpenCode) client(ctx context.Context, scope Scope) (*opencodeServer, error) {
	cfg, digest, err := opencodeConfig(scope)
	if err != nil {
		return nil, err
	}
	key := opencodeKey(scope)
	if s := c.servers[key]; s != nil {
		if s.record.Digest != digest {
			return nil, fmt.Errorf("OpenCode 配置/凭据快照已变化，不能沿用旧会话")
		}
		if s.alive() {
			return s, s.verifyConfig(ctx, scope)
		}
		s.stop()
		delete(c.servers, key)
	}
	s, err := startOpenCode(ctx, scope, filepath.Join(c.stateDir, "runtimes", scope.Instance.ID, key), cfg, digest)
	if err != nil {
		return nil, err
	}
	if err = s.verifyConfig(ctx, scope); err != nil {
		s.stop()
		return nil, err
	}
	c.servers[key] = s
	return s, nil
}

func opencodeMessageID(in Intent) (string, error) {
	stamp, err := time.Parse(time.RFC3339Nano, in.CreatedAt)
	if err != nil {
		return "", fmt.Errorf("OpenCode 输入缺少持久化创建时间")
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return "", err
	}
	// The timestamp prefix follows the target version's ordering convention.
	// Its given-ID API accepts a longer suffix, preserving the complete input
	// UUID so observed messages can be attributed without a second mapping DB.
	prefix := (uint64(stamp.UnixMilli())*0x1000 + 1) & 0xffffffffffff
	return fmt.Sprintf("msg_%012x_%s", prefix, strings.ReplaceAll(id.String(), "-", "")), nil
}

func opencodeOperation(messageID string) string {
	pieces := strings.Split(messageID, "_")
	if len(pieces) != 3 || pieces[0] != "msg" || len(pieces[1]) != 12 || len(pieces[2]) != 32 {
		return ""
	}
	id, err := uuid.Parse(pieces[2])
	if err != nil {
		return ""
	}
	return id.String()
}

func opencodeReadOnlyRules(permission json.RawMessage) []map[string]string {
	rules := []map[string]string{{"permission": "*", "pattern": "*", "action": "ask"}}
	for _, name := range []string{"read", "glob", "grep", "list"} {
		rules = append(rules, map[string]string{"permission": name, "pattern": "*", "action": "allow"})
	}
	// Session hardening must not turn a profile or project deny into an ask.
	var scalar string
	if json.Unmarshal(permission, &scalar) == nil && scalar == "deny" {
		return []map[string]string{{"permission": "*", "pattern": "*", "action": "deny"}}
	}
	var entries map[string]json.RawMessage
	if json.Unmarshal(permission, &entries) == nil {
		for name, value := range entries {
			if json.Unmarshal(value, &scalar) == nil && scalar == "deny" {
				rules = append(rules, map[string]string{"permission": name, "pattern": "*", "action": "deny"})
				continue
			}
			var patterns map[string]string
			if json.Unmarshal(value, &patterns) == nil {
				for pattern, action := range patterns {
					if action == "deny" {
						rules = append(rules, map[string]string{"permission": name, "pattern": pattern, "action": "deny"})
					}
				}
			}
		}
	}
	return rules
}

func (c *OpenCode) Execute(ctx context.Context, in Intent) (Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.client(ctx, in.Scope)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	r := Receipt{NativeID: in.Scope.NativeID}
	path := "/session/" + url.PathEscape(in.Scope.NativeID)
	switch in.Kind {
	case "create":
		body := map[string]any{"title": "Netcap " + in.ID, "metadata": map[string]string{"netcapOperation": in.ID}, "model": map[string]string{"id": in.Scope.Route.Model, "providerID": in.Scope.Route.Provider}}
		if in.Permission == "read_only" {
			var cfg struct {
				Permission json.RawMessage `json:"permission"`
			}
			if err = s.call(ctx, "GET", "/config", nil, &cfg); err != nil {
				return r, &NotSent{Reason: err.Error()}
			}
			body["permission"] = opencodeReadOnlyRules(cfg.Permission)
		}
		var native opencodeSession
		if err = s.call(ctx, "POST", "/session", body, &native); err != nil {
			return r, err
		}
		if native.ID == "" || native.Directory != in.Scope.Cwd || native.Metadata.Operation != in.ID {
			return r, fmt.Errorf("OpenCode 创建回执缺少原操作关联")
		}
		r.NativeID = native.ID
	case "send":
		view, e := c.observe(ctx, in.Scope)
		if e != nil {
			return r, &NotSent{Reason: e.Error()}
		}
		if !view.ExecutionClear {
			return r, &NotSent{Reason: "OpenCode 原执行、请求或后台范围尚未确认空闲"}
		}
		id, e := opencodeMessageID(in)
		if e != nil {
			return r, &NotSent{Reason: e.Error()}
		}
		err = s.call(ctx, "POST", path+"/prompt_async", map[string]any{"messageID": id, "model": map[string]string{"providerID": in.Scope.Route.Provider, "modelID": in.Scope.Route.Model}, "parts": []map[string]string{{"type": "text", "text": in.Text}}}, nil)
		r.NativeTurn = id
	case "respond":
		if in.Pending == nil {
			return r, &NotSent{Reason: "缺少原生待处理请求"}
		}
		view, e := c.observe(ctx, in.Scope)
		if e != nil {
			return r, &NotSent{Reason: e.Error()}
		}
		var pending *Pending
		for _, p := range view.Pending {
			if p.ID == in.Pending.ID && p.ConnectionID == in.Pending.ConnectionID {
				copy := p
				pending = &copy
			}
		}
		if pending == nil {
			return r, &NotSent{Reason: "原请求已处理、过期或 server 身份已变化"}
		}
		var id string
		_ = json.Unmarshal(pending.RequestID, &id)
		var body any
		if pending.ResponseKind == "user_input" {
			answers := [][]string{}
			if len(in.Answers) != len(pending.Questions) {
				return r, &NotSent{Reason: "回答与原问题不匹配"}
			}
			for _, q := range pending.Questions {
				values, ok := in.Answers[q.ID]
				if !ok || len(values) == 0 {
					return r, &NotSent{Reason: "缺少原问题的回答"}
				}
				if !q.Multiple && len(values) != 1 {
					return r, &NotSent{Reason: "原问题只允许单选"}
				}
				seen := map[string]bool{}
				for _, value := range values {
					if strings.TrimSpace(value) == "" || len(value) > 20000 || seen[value] {
						return r, &NotSent{Reason: "回答为空、过长或重复"}
					}
					seen[value] = true
					if q.AllowCustom != nil && !*q.AllowCustom {
						valid := false
						for _, option := range q.Options {
							if option.Label == value {
								valid = true
							}
						}
						if !valid {
							return r, &NotSent{Reason: "原问题不允许自定义答案"}
						}
					}
				}
				answers = append(answers, values)
			}
			path = "/question/" + url.PathEscape(id) + "/reply"
			body = map[string]any{"answers": answers}
		} else {
			decision := ""
			for _, option := range pending.Options {
				if option.ID == in.OptionID {
					_ = json.Unmarshal(option.Value, &decision)
				}
			}
			if decision == "" {
				return r, &NotSent{Reason: "选项不属于原请求"}
			}
			path = "/permission/" + url.PathEscape(id) + "/reply"
			body = map[string]string{"reply": decision}
		}
		var accepted bool
		err = s.call(ctx, "POST", path, body, &accepted)
		var response *opencodeHTTPError
		if errors.As(err, &response) && response.Status == 404 {
			return r, &NotSent{Reason: "原生请求已被处理或清除"}
		}
		if err == nil && !accepted {
			return r, fmt.Errorf("OpenCode 未确认接受回复")
		}
		r.NativeTurn = pending.NativeTurn
	case "interrupt":
		view, e := c.observe(ctx, in.Scope)
		if e != nil {
			return r, &NotSent{Reason: e.Error()}
		}
		if !view.Evidence.Complete {
			return r, &NotSent{Reason: "OpenCode 原执行范围不完整，不能自动中断"}
		}
		owned := false
		for _, message := range view.Messages {
			if message.Role != "user" || message.TurnID != in.NativeTurn {
				continue
			}
			for _, id := range in.OwnedInputs {
				if message.Operation == id {
					owned = true
				}
			}
		}
		if !owned {
			return r, &NotSent{Reason: "缺少该 OpenCode 轮次的自有输入证据"}
		}
		// Abort is session-scoped. Refuse if a newer user input is present.
		if len(view.Turns) == 0 || view.Turns[len(view.Turns)-1].ID != in.NativeTurn {
			return r, &NotSent{Reason: "已有新的原生输入，不能中断其他工作"}
		}
		var accepted bool
		err = s.call(ctx, "POST", path+"/abort", nil, &accepted)
		if err == nil && !accepted {
			return r, fmt.Errorf("OpenCode 未确认接受中断")
		}
		r.NativeTurn = in.NativeTurn
	default:
		return r, &NotSent{Reason: "不支持的 OpenCode 操作"}
	}
	return r, err
}

func (c *OpenCode) ObserveFresh(ctx context.Context, scope Scope) (View, error) {
	return c.Observe(ctx, scope) // OpenCode observations already query the native server.
}

func (c *OpenCode) Observe(ctx context.Context, scope Scope) (View, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observe(ctx, scope)
}

func (c *OpenCode) Reconcile(ctx context.Context, in Intent, receipt Receipt) (Resolution, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "opencode.reconcile", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "没有匹配原操作的完整原生证据"}}
	s, err := c.client(ctx, in.Scope)
	if err != nil {
		return r, err
	}
	id := receipt.NativeID
	if id == "" {
		id = in.Scope.NativeID
	}
	if in.Scope.NativeID != "" && id != in.Scope.NativeID {
		return r, nil
	}
	if in.Kind == "create" && id == "" {
		var candidates []opencodeSession
		if err = s.call(ctx, "GET", "/session?search="+url.QueryEscape(in.ID)+"&limit=100", nil, &candidates); err != nil {
			return r, err
		}
		if len(candidates) >= 100 {
			return r, nil
		}
		for _, candidate := range candidates {
			if candidate.Metadata.Operation == in.ID && candidate.Directory == in.Scope.Cwd {
				if id != "" {
					return r, fmt.Errorf("多个 OpenCode 会话匹配原创建意图")
				}
				id = candidate.ID
			}
		}
	}
	if id == "" {
		return r, nil
	}
	if in.Kind == "create" {
		var candidate opencodeSession
		if err = s.call(ctx, "GET", "/session/"+url.PathEscape(id), nil, &candidate); err != nil {
			return r, err
		}
		if candidate.Metadata.Operation != in.ID {
			return r, nil
		}
	}
	scope := in.Scope
	scope.NativeID = id
	view, err := c.observe(ctx, scope)
	if err != nil {
		return r, err
	}
	r.View, r.Evidence = view, view.Evidence
	r.Receipt.NativeID = id
	if in.Kind == "create" {
		if view.Evidence.Complete {
			r.State, r.Status = "finished", "succeeded"
		}
		return r, nil
	}
	if in.Kind == "respond" && in.Pending != nil {
		for _, p := range view.Pending {
			if p.ID == in.Pending.ID {
				return r, nil
			}
		}
		if view.Evidence.Complete {
			r.State, r.Status = "finished", "succeeded"
		}
		return r, nil
	}
	turnID := in.NativeTurn
	if in.Kind == "send" {
		turnID, err = opencodeMessageID(in)
		if err != nil {
			return r, err
		}
		matched := false
		for _, m := range view.Messages {
			if m.Role == "user" && m.TurnID == turnID && m.Operation == in.ID && m.Text == in.Text {
				matched = true
			}
		}
		if !matched {
			return r, nil
		}
	}
	if receipt.NativeTurn != "" && receipt.NativeTurn != turnID {
		return r, nil
	}
	r.Receipt.NativeTurn = turnID
	for _, turn := range view.Turns {
		if turn.ID != turnID {
			continue
		}
		if turn.Status == "inProgress" {
			if view.Status == "running" || strings.HasPrefix(view.Status, "waiting_") {
				r.State = "running"
			}
			return r, nil
		}
		if !view.Evidence.Complete {
			return r, nil
		}
		if !view.ExecutionClear {
			r.State = "running"
			return r, nil
		}
		r.State = "finished"
		switch turn.Status {
		case "completed":
			r.Status = "succeeded"
		case "interrupted":
			r.Status = "interrupted"
		default:
			r.Status = "failed"
		}
		if in.Kind == "interrupt" {
			r.Status = "succeeded"
		}
		return r, nil
	}
	return r, nil
}

func (c *OpenCode) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, s := range c.servers {
		s.stop()
		delete(c.servers, key)
	}
	return nil
}
