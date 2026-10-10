package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"codex-netcap/internal/config"
)

type Claude struct {
	mu       sync.Mutex
	stateDir string
	clients  map[string]*claudeTransport
	cache    map[string]claudeCachedView
	scopes   map[string]string
	lastUse  map[string]time.Time
	stop     chan struct{}
	stopped  chan struct{}
	closing  bool
}

func NewClaude(stateDir string) *Claude {
	c := &Claude{stateDir: stateDir, clients: map[string]*claudeTransport{}, cache: map[string]claudeCachedView{}, scopes: map[string]string{}, lastUse: map[string]time.Time{}, stop: make(chan struct{}), stopped: make(chan struct{})}
	go c.reapIdle()
	return c
}

func (c *Claude) Describe(ctx context.Context, i Instance) (Description, error) {
	cmd := exec.CommandContext(ctx, i.Executable, "--version")
	cmd.Env = claudeEnv(i)
	b, err := cmd.Output()
	description := describeVersion("claude", strings.TrimSpace(string(b)), err)
	if description.Compatibility == "supported" {
		description.LocalIPC = checkClaudeLocalIPC(ctx)
	}
	return description, err
}

func (c *Claude) client(ctx context.Context, i Instance) (*claudeTransport, error) {
	if p := c.clients[i.ID]; p != nil {
		select {
		case <-p.done:
			p.close()
			delete(c.clients, i.ID)
		default:
			return p, nil
		}
	}
	p, err := connectClaude(ctx, i, filepath.Join(c.stateDir, "runtimes", i.ID))
	if err == nil {
		c.clients[i.ID] = p
	}
	return p, err
}

func claudeCredential(route Route) (string, error) {
	key, ok := strings.CutPrefix(route.CredentialRef, "env:")
	if !ok || key == "" {
		return "", &NotSent{Reason: "Claude 当前需要 env: 凭据引用"}
	}
	value := os.Getenv(key)
	if value == "" {
		return "", &NotSent{Reason: "指定 Claude 路由的凭据环境变量未设置"}
	}
	return value, nil
}

func (c *Claude) Execute(ctx context.Context, in Intent) (Receipt, error) {
	denyingTool := IsClaudeToolDenial(in)
	executionScope := in.Scope
	if in.ClaudeInstalledRecovery != nil {
		var err error
		executionScope, err = claudeRecoveredScope(in)
		if err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
	}
	if len(in.Scope.ClaudeSettingsSources) > 0 && in.Scope.SettingsGuardVersion != 1 {
		return Receipt{}, &NotSent{Reason: "原生本地设置文件版本缺少阻止机制绑定"}
	}
	if in.Scope.SettingsGuardVersion != 0 && (in.Scope.SettingsGuardVersion != 1 || in.Scope.Purpose != "" || in.Scope.Instance.Kind != "claude") {
		return Receipt{}, &NotSent{Reason: "Claude 本地设置阻止机制版本无效"}
	}
	if in.Kind == "create" || in.Kind == "send" || in.Kind == "respond" && !denyingTool || in.Kind == "apply_execution" || in.Kind == "prepare_execution" || in.Kind == "recover_execution" {
		if err := ValidateClaudeExecutionSettings(ctx, executionScope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		startupScope := executionScope
		if in.Kind == "prepare_execution" && in.ConfigurationTarget != nil {
			startupScope = *in.ConfigurationTarget
		}
		if err := verifyClaudeInstalledStartup(ctx, startupScope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		expected := in.Scope.ClaudeSettingsSources
		if in.Kind == "prepare_execution" && in.ConfigurationTarget != nil {
			expected = in.ConfigurationTarget.ClaudeSettingsSources
		}
		if len(expected) > 0 {
			current, err := ReadClaudeLocalSettingsSources(ctx, in.Scope)
			if err != nil || !slices.Equal(expected, current) {
				return Receipt{}, &NotSent{Reason: "Claude 本地设置来源版本已变化，尚未发送原生操作"}
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if in.Kind == "create" && in.Scope.Purpose == "" {
		if _, err := claudeCredential(in.Scope.Route); err != nil {
			return Receipt{}, err
		}
		if err := validateClaudeCreateSettings(ctx, in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
	}
	c.lastUse[in.Scope.Instance.ID] = time.Now()
	defer delete(c.cache, in.Scope.Instance.ID+"/"+in.Scope.NativeID)
	var p *claudeTransport
	var err error
	if denyingTool {
		p = c.clients[in.Scope.Instance.ID]
		if c.closing || p == nil {
			return Receipt{}, &NotSent{Reason: "原审批连接已关闭；拒绝请求不会启动或恢复查询"}
		}
		select {
		case <-p.done:
			return Receipt{}, &NotSent{Reason: "原审批连接已关闭；请核对原请求"}
		default:
		}
	} else {
		p, err = c.client(ctx, in.Scope.Instance)
	}
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	if in.Scope.ClaudeInstalledSettings != nil && in.Kind != "apply_execution" && in.Kind != "interrupt" && in.ClaudeInstalledRecovery == nil && !denyingTool {
		use, err := c.holdInstalledSettings(ctx, p, in.Scope, false)
		if err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		defer use.file.Close()
	}
	params := map[string]any{"native_id": in.Scope.NativeID}
	var receipt Receipt
	switch in.Kind {
	case "prepare_execution":
		return c.prepareExecution(ctx, p, in)
	case "apply_execution":
		return c.applyExecution(ctx, p, in)
	case "recover_execution":
		return c.recoverExecution(ctx, p, in)
	case "create":
		credential, e := claudeCredential(in.Scope.Route)
		if e != nil {
			return receipt, e
		}
		params = map[string]any{"native_id": in.ID, "source": in.ID, "cwd": in.Scope.Cwd, "model": in.Scope.Route.Model,
			"endpoint": in.Scope.Route.Endpoint, "executable": in.Scope.Instance.Executable, "permission": in.Permission, "api_key": credential}
		params["probe"] = in.Scope.Purpose == "probe"
		if in.Scope.SettingsGuardVersion != 0 {
			params["settings_guard_version"] = in.Scope.SettingsGuardVersion
		}
		if len(in.Scope.ClaudeSettingsSources) > 0 {
			params["settings_sources"] = in.Scope.ClaudeSettingsSources
		}
		if len(in.Scope.MCPSelections) > 0 {
			params["mcp_selections"] = in.Scope.MCPSelections
		}
		if len(in.Scope.PluginSelections) > 0 {
			params["plugin_selections"] = in.Scope.PluginSelections
		}
		if len(in.Scope.SkillSelections) > 0 {
			params["skill_selections"] = in.Scope.SkillSelections
		}
		if in.Scope.Purpose != "probe" && in.Scope.Instance.SettingsFile != "" {
			params["settings_file"] = in.Scope.Instance.SettingsFile
		}
	case "send":
		view, _, e := c.observe(ctx, in.Scope)
		if e != nil {
			return receipt, &NotSent{Reason: e.Error()}
		}
		if !view.ExecutionClear || !view.Evidence.Complete {
			return receipt, &NotSent{Reason: "原 Claude 执行范围尚未确认空闲"}
		}
		credential, e := claudeCredential(in.Scope.Route)
		if e != nil {
			return receipt, e
		}
		params["api_key"], params["operation"], params["text"] = credential, in.ID, in.Text
	case "respond":
		if in.Pending == nil {
			return receipt, &NotSent{Reason: "缺少原始请求"}
		}
		var requestID string
		if json.Unmarshal(in.Pending.RequestID, &requestID) != nil {
			return receipt, &NotSent{Reason: "原始请求 ID 无效"}
		}
		params["request_id"], params["connection"] = requestID, in.Pending.ConnectionID
		if in.Pending.ResponseKind == "user_input" {
			answers := map[string]string{}
			for _, question := range in.Pending.Questions {
				values, ok := in.Answers[question.ID]
				if !ok || len(values) == 0 {
					return receipt, &NotSent{Reason: "缺少原问题回答"}
				}
				answers[question.Text] = strings.Join(values, ", ")
			}
			if len(answers) != len(in.Answers) {
				return receipt, &NotSent{Reason: "回答包含未知问题"}
			}
			params["answers"] = answers
		} else {
			for _, option := range in.Pending.Options {
				if option.ID == in.OptionID {
					var value string
					_ = json.Unmarshal(option.Value, &value)
					params["decision"] = value
				}
			}
			if params["decision"] == nil {
				return receipt, &NotSent{Reason: "选项不属于原始请求"}
			}
			if params["decision"] == "allow" && p.pendingPermissions != 1 {
				return receipt, &NotSent{Reason: "当前 bridge 未声明原审批权限观察约束，不能批准；请拒绝或中止原输入"}
			}
		}
	case "interrupt":
		view, _, e := c.observe(ctx, in.Scope)
		if e != nil {
			return receipt, &NotSent{Reason: e.Error()}
		}
		if !view.Evidence.Complete {
			return receipt, &NotSent{Reason: "原 Claude 输入与后台状态无法完整核对，不能自动中断"}
		}
		for _, message := range view.Messages {
			if message.Role == "user" && message.TurnID == in.NativeTurn {
				owned := false
				for _, id := range in.OwnedInputs {
					if message.Operation == id {
						owned = true
					}
				}
				if !owned {
					return receipt, &NotSent{Reason: "原 Claude 轮次包含人工输入"}
				}
			}
		}
		params["native_turn"] = in.NativeTurn
	default:
		return receipt, &NotSent{Reason: "不支持的 Claude 操作"}
	}
	id := in.Scope.NativeID
	if in.Kind == "create" {
		id = in.ID
	}
	c.scopes[in.Scope.Instance.ID+"/"+id] = in.Scope.Instance.ID
	err = p.call(ctx, in.Kind, params, &receipt)
	return receipt, err
}

func (c *Claude) Observe(ctx context.Context, scope Scope) (View, error) {
	return c.observeWithCache(ctx, scope, true)
}

func (c *Claude) ObserveFresh(ctx context.Context, scope Scope) (View, error) {
	return c.observeWithCache(ctx, scope, false)
}

func (c *Claude) observeWithCache(ctx context.Context, scope Scope, allowCache bool) (View, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := scope.Instance.ID + "/" + scope.NativeID
	if cached, ok := c.cache[key]; allowCache && ok && cached.view.ExecutionClear {
		interval := 2 * time.Second
		if c.clients[scope.Instance.ID] == nil {
			interval = claudeIdleAfter
		}
		if time.Since(cached.at) < interval {
			return cached.view, nil
		}
	}
	v, snapshot, err := c.observe(ctx, scope)
	if err == nil && !snapshot.Live && c.canReap(scope.Instance.ID) {
		c.clients[scope.Instance.ID].close()
		delete(c.clients, scope.Instance.ID)
	}
	return v, err
}

func (c *Claude) observe(ctx context.Context, scope Scope) (View, claudeSnapshot, error) {
	var snapshot claudeSnapshot
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return View{}, snapshot, err
	}
	if err = p.call(ctx, "snapshot", map[string]any{"native_id": scope.NativeID}, &snapshot); err != nil {
		return View{}, snapshot, err
	}
	if snapshot.Config.ID != scope.NativeID || snapshot.Config.Cwd != scope.Cwd || snapshot.Config.Model != scope.Route.Model || snapshot.Config.Endpoint != scope.Route.Endpoint || !slices.Equal(snapshot.Config.MCPSelections, scope.MCPSelections) || !slices.Equal(snapshot.Config.SkillSelections, scope.SkillSelections) || !EqualPluginSelections(snapshot.Config.PluginSelections, scope.PluginSelections) {
		return View{}, snapshot, fmt.Errorf("Claude 会话与原操作的目录/模型/端点不匹配")
	}
	view := projectClaude(scope, snapshot)
	if snapshot.Config.SettingsGuardVersion != scope.SettingsGuardVersion {
		return View{}, snapshot, fmt.Errorf("Claude 本地设置阻止机制与原绑定不一致")
	}
	if !slices.Equal(snapshot.Config.SettingsSources, scope.ClaudeSettingsSources) {
		return View{}, snapshot, fmt.Errorf("Claude 本地设置文件版本与原绑定不一致")
	}
	if !claudeInstalledConfigMatches(scope, snapshot) {
		return View{}, snapshot, fmt.Errorf("Claude 已安装插件执行设置与原绑定不一致")
	}
	// Observation reports the saved native binding and its live settings guard.
	// Source freshness gates dispatch, so local drift can still be explicitly
	// adopted without making the original query's guard impossible to inspect.
	view.TerminalCommand = "CLAUDE_CONFIG_DIR=" + config.ShellQuote(scope.Instance.Profile) + " " + config.ShellQuote(scope.Instance.Executable) + " --resume " + config.ShellQuote(scope.NativeID)
	if scope.ClaudeInstalledSettings != nil {
		// An independent CLI does not inherit the controller's file-use lock.
		// Do not present an ordinary command that would silently lose overrides.
		view.TerminalCommand = ""
	} else if scope.Instance.SettingsFile != "" {
		view.TerminalCommand += " --settings " + config.ShellQuote(scope.Instance.SettingsFile)
	}
	c.cache[scope.Instance.ID+"/"+scope.NativeID] = claudeCachedView{view: view, at: time.Now(), instance: scope.Instance.ID}
	c.scopes[scope.Instance.ID+"/"+scope.NativeID] = scope.Instance.ID
	return view, snapshot, nil
}

func (c *Claude) Reconcile(ctx context.Context, in Intent, receipt Receipt) (Resolution, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if in.Kind == "recover_execution" {
		return c.reconcileRecovery(ctx, in, receipt)
	}
	if in.Kind == "apply_execution" {
		return c.reconcileExecution(ctx, in, receipt)
	}
	r := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "claude.sdk", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "缺少可验证的原生输入与执行结果"}}
	id := receipt.NativeID
	if id == "" {
		id = in.Scope.NativeID
	}
	if id == "" && in.Kind == "create" {
		id = in.ID
	}
	if in.Scope.NativeID != "" && in.Scope.NativeID != id {
		return r, nil
	}
	scope := in.Scope
	scope.NativeID = id
	view, snapshot, err := c.observe(ctx, scope)
	if err != nil {
		return r, err
	}
	r.View, r.Evidence = view, view.Evidence
	r.Receipt.NativeID = id
	if in.Kind == "create" {
		if snapshot.Config.Source == in.ID && snapshot.initialized() && view.Evidence.Complete {
			r.State, r.Status = "finished", "succeeded"
		}
		return r, nil
	}
	if in.Kind == "respond" {
		resolved := view.ExecutionClear
		if in.Pending != nil {
			for _, turn := range view.Turns {
				if turn.ID == in.Pending.NativeTurn && turn.Status != "inProgress" {
					resolved = true
				}
			}
		}
		if view.Evidence.Complete && resolved {
			r.State, r.Status = "finished", "succeeded"
		}
		return r, nil
	}
	turnID := in.ID
	if in.Kind == "interrupt" {
		turnID = in.NativeTurn
	} else {
		matched := false
		for _, m := range view.Messages {
			if m.Role == "user" && m.Operation == in.ID && m.Text == in.Text {
				matched = true
			}
		}
		if !matched {
			return r, nil
		}
	}
	r.Receipt.NativeTurn = turnID
	for _, turn := range view.Turns {
		if turn.ID != turnID {
			continue
		}
		if turn.Status == "inProgress" {
			if snapshot.Live && snapshot.Active == turnID {
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

func (c *Claude) Close() error {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return nil
	}
	c.closing = true
	close(c.stop)
	for id, p := range c.clients {
		p.close()
		delete(c.clients, id)
	}
	c.mu.Unlock()
	<-c.stopped
	return nil
}

const claudeIdleAfter = 30 * time.Second

type claudeCachedView struct {
	view     View
	at       time.Time
	instance string
}

func (c *Claude) canReap(instance string) bool {
	if time.Since(c.lastUse[instance]) < claudeIdleAfter {
		return false
	}
	found := false
	for key, owner := range c.scopes {
		if owner != instance {
			continue
		}
		found = true
		cached, ok := c.cache[key]
		if !ok || !cached.view.ExecutionClear || !cached.view.Evidence.Complete {
			return false
		}
	}
	return found
}

func (c *Claude) reapIdle() {
	defer close(c.stopped)
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-timer.C:
			c.mu.Lock()
			for id, p := range c.clients {
				if c.canReap(id) {
					p.close()
					delete(c.clients, id)
				}
			}
			c.mu.Unlock()
		}
	}
}
