package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/config"
)

type Codex struct {
	configurationRecoveries map[string]codexRecoveryWitness
	mu                      sync.Mutex
	clients                 map[string]codexConnection
	pending                 map[string]Pending
	streams                 map[string]map[string]Message
	hooks                   map[string]map[string]HookExecution
	creationPolicies        map[string]codexCreationPolicy
	directorySkillWitnesses map[string]codexConfigurationWitness
	hookWitnesses           map[string]codexConfigurationWitness
}

type codexConnection interface {
	codex.Client
	ConnectionID() string
	PendingRequest(json.RawMessage) (codex.Message, bool)
	Respond(context.Context, json.RawMessage, any) error
}

func NewCodex() *Codex {
	return &Codex{clients: map[string]codexConnection{}, pending: map[string]Pending{}, streams: map[string]map[string]Message{}, hooks: map[string]map[string]HookExecution{}, creationPolicies: map[string]codexCreationPolicy{}}
}

func instanceEnv(i Instance) []string {
	if i.isolatedHome != "" {
		return probeEnv(i)
	}
	env := []string{}
	for _, key := range append([]string{"PATH", "HOME", "USER", "LANG", "TERM", "SHELL"}, i.EnvAllow...) {
		if key != "CODEX_HOME" {
			if value, ok := os.LookupEnv(key); ok {
				env = append(env, key+"="+value)
			}
		}
	}
	return append(env, "CODEX_HOME="+i.Profile)
}

func (c *Codex) Describe(ctx context.Context, i Instance) (Description, error) {
	cmd := exec.CommandContext(ctx, i.Executable, "--version")
	cmd.Env = instanceEnv(i)
	b, err := cmd.Output()
	return describeVersion("codex", strings.TrimSpace(string(b)), err), err
}

func (c *Codex) client(ctx context.Context, i Instance) (codexConnection, error) {
	if p := c.clients[i.ID]; p != nil {
		if verified, ok := p.(*verifiedCodexConnection); ok && verified.peer.verify(i.Socket) != nil {
			c.failed(i)
		} else {
			if verified != nil {
				if err := verified.peer.verifyNamespace(ctx, i); err != nil {
					c.failed(i)
					return nil, err
				}
			}
			return p, nil
		}
	}
	peer, err := openCodexPeer(ctx, i)
	if err != nil {
		return nil, err
	}
	p, err := codex.ConnectEnv(ctx, config.Config{CodexBin: i.Executable, CodexHome: i.Profile, Socket: i.Socket, RPCTimeoutSeconds: 10}, instanceEnv(i))
	if err != nil {
		peer.connection.Close()
		return nil, err
	}
	if err = peer.verifyNamespace(ctx, i); err != nil {
		p.Close()
		peer.connection.Close()
		return nil, err
	}
	// The proxy now owns the live protocol connection; the temporary peer
	// inspection socket need not remain as an uninitialized native client.
	_ = peer.connection.Close()
	peer.connection = nil
	verified := &verifiedCodexConnection{codexConnection: p, peer: peer}
	c.clients[i.ID] = verified
	return verified, nil
}

func (c *Codex) failed(i Instance) {
	for id, witness := range c.configurationRecoveries {
		if witness.InstanceID == i.ID {
			delete(c.configurationRecoveries, id)
		}
	}
	for key := range c.directorySkillWitnesses {
		if strings.HasPrefix(key, i.ID+"/") {
			delete(c.directorySkillWitnesses, key)
		}
	}
	for key := range c.hookWitnesses {
		if strings.HasPrefix(key, i.ID+"/") {
			delete(c.hookWitnesses, key)
		}
	}
	if p := c.clients[i.ID]; p != nil {
		p.Close()
		delete(c.clients, i.ID)
	}
	for key, pending := range c.pending {
		if strings.HasPrefix(key, i.ID+"/") {
			pending.Status = "unverified"
			c.pending[key] = pending
		}
	}
	for key := range c.creationPolicies {
		if strings.HasPrefix(key, i.ID+"/") {
			delete(c.creationPolicies, key)
		}
	}
	for key, hooks := range c.hooks {
		if strings.HasPrefix(key, i.ID+"/") {
			for id, hook := range hooks {
				if hook.Status == "running" {
					hook.Status = "unknown"
					hooks[id] = hook
				}
			}
		}
	}
}

func (c *Codex) readFailed(i Instance, err error) {
	// A rejected read of one thread says nothing about the health of the shared
	// protocol connection. Keep sibling approval requests on that connection.
	var rpc *codex.RPCError
	if !errors.As(err, &rpc) {
		c.failed(i)
	}
}

func (c *Codex) Execute(ctx context.Context, in Intent) (Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if in.HookSourceProof != nil && in.Kind != "prepare_execution" && in.Kind != "apply_execution" {
		return Receipt{}, &NotSent{Reason: "hook 采纳依据仅用于已保存的准备/应用操作"}
	}
	in.Scope.RecoveryPermission = in.Permission
	if in.Kind == "prepare_execution" && in.ConfigurationTarget != nil && CodexSourceAdoption(in.Scope, *in.ConfigurationTarget) {
		in.Scope.hookSourceTarget = &codexHookSourceTarget{in.ConfigurationTarget.CodexUserHooks, in.ConfigurationTarget.CodexProjectHooks, in.ConfigurationTarget.CodexConfigurationSources}
	}
	if in.Kind != "interrupt" {
		if err := VerifyCodexConfigurationFiles(in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		if err := VerifyCodexUserHooks(in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
	}
	if in.ConfigurationTarget != nil && in.Kind != "prepare_execution" && in.Kind != "recover_execution" {
		return Receipt{}, &NotSent{Reason: "配置候选仅用于准备改配核对"}
	}
	if len(in.SkillInputs) > 0 && in.Kind != "send" {
		return Receipt{}, &NotSent{Reason: "显式 Skill 仅用于新消息输入"}
	}
	boundSkills, skillErr := ResolveCodexSkillInputs(in.Scope, in.SkillInputs)
	if skillErr != nil {
		return Receipt{}, &NotSent{Reason: skillErr.Error()}
	}
	p, err := c.client(ctx, in.Scope.Instance)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	if in.Kind != "interrupt" {
		if err := verifyCodexConfigurationSources(ctx, p, in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		if err := verifyCodexProjectHooks(ctx, p, in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
	}
	var result Receipt
	switch in.Kind {
	case "recover_execution":
		return c.recoverConfiguration(ctx, p, in)
	case "prepare_execution":
		return c.prepareExecution(ctx, p, in)
	case "apply_execution":
		return c.applyExecution(ctx, p, in)
	case "create":
		if err := prepareCodexPluginCaches(in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		overrides, routeErr := codexRouteConfig(ctx, p, in.Scope)
		if routeErr != nil {
			return Receipt{}, &NotSent{Reason: routeErr.Error()}
		}
		sandbox, approval, permissionErr := codexRecoveryPolicy(in.Scope)
		if permissionErr != nil {
			return Receipt{}, &NotSent{Reason: permissionErr.Error()}
		}
		var out codex.Started
		skillFingerprint, skillErr := c.prepareDirectorySkills(ctx, p, in.Scope)
		if skillErr != nil {
			return Receipt{}, &NotSent{Reason: skillErr.Error()}
		}
		hookFingerprint, hookErr := c.prepareSelectedHooks(ctx, p, in.Scope)
		if hookErr != nil {
			return Receipt{}, &NotSent{Reason: hookErr.Error()}
		}
		route := in.Scope.Route
		// Route identity is explicit even if the profile's default differs.
		err = p.Call(ctx, "thread/start", map[string]any{
			"cwd": in.Scope.Cwd, "model": route.Model, "modelProvider": route.Provider,
			"approvalPolicy": approval, "sandbox": sandbox, "historyMode": "legacy",
			"threadSource": "netcap-console:" + in.ID,
			"config":       overrides,
		}, &out)
		if err == nil {
			if out.Thread.ID == "" || out.Model != route.Model || out.Provider != route.Provider {
				err = fmt.Errorf("原生会话身份或实际路由不匹配")
			} else {
				result.NativeID = out.Thread.ID
				var policy *PermissionEvidence
				policy, err = verifyCodexRecoveryPermission(in.Scope, out)
				if err == nil {
					err = verifyCodexEffort(in.Scope, out.Thread)
				}
				if err == nil {
					policy.Source = "codex.thread/start"
					c.creationPolicies[in.Scope.Instance.ID+"/"+out.Thread.ID] = codexCreationPolicy{ConnectionID: p.ConnectionID(), Evidence: *policy, DisabledPluginIDs: out.DisabledPluginIDs, Cwd: in.Scope.Cwd, Model: route.Model, Provider: route.Provider}
					if codexMixedPlugins(in.Scope.PluginSelections) {
						err = c.initializePluginPolicy(ctx, p, in.Scope, out.Thread.ID, out.DisabledPluginIDs)
					}
					if err == nil {
						err = c.rememberSelectedHooks(ctx, p, in.Scope, out.Thread.ID, hookFingerprint)
						if err == nil {
							err = c.rememberDirectorySkills(ctx, p, in.Scope, out.Thread.ID, skillFingerprint)
						}
					}
				}
			}
		}
	case "send":
		if _, routeErr := codexRouteConfig(ctx, p, in.Scope); routeErr != nil {
			return Receipt{}, &NotSent{Reason: routeErr.Error()}
		}
		if _, _, permissionErr := codexRecoveryPolicy(in.Scope); permissionErr != nil {
			return Receipt{}, &NotSent{Reason: permissionErr.Error()}
		}
		// Restore an unloaded thread or sample an idle thread's current policy,
		// then prove the native execution scope is idle before sending input.
		view, preflightErr := c.observe(ctx, in.Scope)
		if preflightErr != nil {
			return Receipt{}, &NotSent{Reason: preflightErr.Error()}
		}
		if view.Status != "idle" || !view.ExecutionClear || !view.Evidence.Complete || view.PermissionEvidence == nil || !view.PermissionEvidence.Verified {
			return Receipt{}, &NotSent{Reason: "原生会话、队列或后台执行尚未确认空闲"}
		}
		if routeErr := verifyCodexThreadRoute(ctx, p, in.Scope); routeErr != nil {
			return Receipt{}, &NotSent{Reason: routeErr.Error()}
		}
		var out struct {
			Turn codex.Turn `json:"turn"`
		}
		input := []map[string]any{{"type": "text", "text": in.Text}}
		for _, skill := range boundSkills {
			input = append(input, map[string]any{"type": "skill", "name": skill.Name, "path": skill.Path})
		}
		params := map[string]any{"threadId": in.Scope.NativeID, "clientUserMessageId": in.ID, "model": in.Scope.Route.Model, "cwd": in.Scope.Cwd, "input": input}
		if err := c.addInputPluginPolicy(ctx, p, in.Scope, params); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		delete(c.creationPolicies, in.Scope.Instance.ID+"/"+in.Scope.NativeID)
		err = p.Call(ctx, "turn/start", params, &out)
		result = Receipt{NativeID: in.Scope.NativeID, NativeTurn: out.Turn.ID}
	case "interrupt":
		if err = verifyOwnedTurn(ctx, p, in); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		err = p.Call(ctx, "turn/interrupt", map[string]any{"threadId": in.Scope.NativeID, "turnId": in.NativeTurn}, nil)
		result = Receipt{NativeID: in.Scope.NativeID, NativeTurn: in.NativeTurn}
	case "respond":
		if _, routeErr := codexRouteConfig(ctx, p, in.Scope); routeErr != nil {
			return Receipt{}, &NotSent{Reason: routeErr.Error()}
		}
		if routeErr := verifyCodexThreadRoute(ctx, p, in.Scope); routeErr != nil {
			return Receipt{}, &NotSent{Reason: routeErr.Error()}
		}
		if _, matches := codexMCPEvidence(ctx, p, in.Scope, nil); !matches {
			return Receipt{}, &NotSent{Reason: "本次会话的原生 MCP 状态与选择尚未核对一致"}
		}
		result, err = c.respond(ctx, p, in)
	default:
		return Receipt{}, fmt.Errorf("不支持的 Runtime intent %q", in.Kind)
	}
	if err != nil {
		var notSent *NotSent
		if !errors.As(err, &notSent) {
			c.failed(in.Scope.Instance)
		}
	}
	return result, err
}

func (c *Codex) collect(i Instance, p codexConnection) {
	for _, event := range p.Drain() {
		if event.Method == "thread/settings/updated" {
			c.collectCreationPluginPolicy(i, event)
		}
		if event.Method == "hook/started" || event.Method == "hook/completed" {
			c.collectHook(i, event)
		}
		if event.Method == "serverRequest/resolved" {
			var data struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(event.Params, &data) == nil {
				id := i.ID + "/" + p.ConnectionID() + "/" + string(data.RequestID)
				if pending, ok := c.pending[id]; ok {
					pending.Status = "resolved"
					c.pending[id] = pending
				}
			}
		}
		if event.Method == "item/agentMessage/delta" {
			var part struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				ItemID   string `json:"itemId"`
				Delta    string `json:"delta"`
			}
			if json.Unmarshal(event.Params, &part) == nil && part.ThreadID != "" {
				key := i.ID + "/" + part.ThreadID
				if c.streams[key] == nil {
					c.streams[key] = map[string]Message{}
				}
				message := c.streams[key][part.ItemID]
				message.ID, message.TurnID, message.Role, message.Status = part.ItemID, part.TurnID, "assistant", "inProgress"
				if len(message.Text) < 200000 {
					message.Text += part.Delta
				}
				c.streams[key][part.ItemID] = message
			}
		}
		if len(event.ID) == 0 || event.Method == "" {
			continue
		}
		var refs struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		_ = json.Unmarshal(event.Params, &refs)
		pending := pendingFromRequest(i, p.ConnectionID(), event)
		c.pending[pending.ID] = pending
	}
	for id, pending := range c.pending {
		if !strings.HasPrefix(id, i.ID+"/") {
			continue
		}
		if pending.ResponseKind == "message_input" {
			continue
		}
		if pending.ConnectionID != p.ConnectionID() {
			pending.Status = "unverified"
		} else if _, valid := p.PendingRequest(pending.RequestID); !valid {
			if pending.Status != "submitted" {
				pending.Status = "resolved"
			}
		}
		c.pending[id] = pending
	}
}

func (c *Codex) Observe(ctx context.Context, scope Scope) (View, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observe(ctx, scope)
}

func (c *Codex) ObserveFresh(ctx context.Context, scope Scope) (View, error) {
	if !scope.NoAutoResume {
		if _, _, err := codexRecoveryPolicy(scope); err != nil {
			return View{}, err
		}
	}
	view, err := c.Observe(ctx, scope)
	if err == nil && !scope.NoAutoResume && view.Status == "idle" && (view.PermissionEvidence == nil || !view.PermissionEvidence.Verified) {
		err = fmt.Errorf("原生权限尚未核对，不能恢复输入控制")
	}
	return view, err
}

func (c *Codex) observe(ctx context.Context, scope Scope) (View, error) {
	return c.observeWithHistory(ctx, scope, consoleTurns, "")
}

func (c *Codex) observeWithHistory(ctx context.Context, scope Scope, readTurns func(context.Context, codex.Client, string) ([]codex.Turn, error), replyID string) (view View, resultErr error) {
	var p codexConnection
	defer func() {
		err := VerifyCodexUserHooks(scope)
		if err == nil {
			err = VerifyCodexConfigurationFiles(scope)
		}
		if err == nil && p != nil {
			err = verifyCodexConfigurationSources(ctx, p, scope)
		}
		if err == nil && p != nil {
			err = verifyCodexProjectHooks(ctx, p, scope)
		}
		if err != nil {
			view.ExecutionClear = false
			view.Status = "unknown"
			view.Evidence.Complete = false
			view.Evidence.Detail = err.Error()
		}
	}()
	view = View{NativeID: scope.NativeID, Status: "unknown", Messages: []Message{}, Pending: []Pending{}}
	view.TerminalCommand = "CODEX_HOME=" + config.ShellQuote(scope.Instance.Profile) + " " + config.ShellQuote(scope.Instance.Executable) + " --remote " + config.ShellQuote("unix://"+scope.Instance.Socket) + " resume " + config.ShellQuote(scope.NativeID)
	var err error
	p, err = c.client(ctx, scope.Instance)
	if err != nil {
		return view, err
	}
	if inspector, ok := p.(codexExecutorInspector); ok {
		view.Executor, err = inspector.ExecutorEvidence(ctx, scope.Instance)
		if err != nil {
			return view, err
		}
	}
	c.collect(scope.Instance, p)
	thread, err := codex.Read(ctx, p, scope.NativeID)
	if err != nil {
		c.readFailed(scope.Instance, err)
		return view, err
	}
	if err := verifyCodexEffort(scope, thread); err != nil {
		return view, err
	}
	if scope.StateNamespace != "" {
		cwd, cwdErr := filepath.EvalSymlinks(thread.Cwd)
		if cwdErr != nil || cwd != scope.Cwd || thread.ID != scope.NativeID {
			return view, fmt.Errorf("原生观察返回的会话身份或项目不匹配")
		}
	}
	if thread.Status.Type == "notLoaded" || thread.ID != scope.NativeID || thread.Cwd != scope.Cwd || thread.Model != scope.Route.Model || thread.Provider != scope.Route.Provider {
		delete(c.hookWitnesses, scope.Instance.ID+"/"+scope.NativeID)
		delete(c.directorySkillWitnesses, scope.Instance.ID+"/"+scope.NativeID)
	}
	if !scope.NoAutoResume && scope.RecoveryPermission != "" && thread.Status.Type != "idle" && thread.Status.Type != "active" && thread.Status.Type != "notLoaded" {
		return view, fmt.Errorf("原生运行状态未知，不能核对恢复权限")
	}
	if thread.Status.Type != "idle" || thread.ID != scope.NativeID || thread.Cwd != scope.Cwd || thread.Model != scope.Route.Model || thread.Provider != scope.Route.Provider {
		delete(c.creationPolicies, scope.Instance.ID+"/"+scope.NativeID)
	}
	// An unloaded managed thread needs the original explicit permission mode.
	// Loaded policy sampling waits for the full execution and request checks below.
	if !scope.NoAutoResume && thread.Status.Type == "notLoaded" {
		thread, view.PermissionEvidence, err = c.resumeWithPermission(ctx, p, scope, thread)
		if err != nil {
			return view, err
		}
		if err := verifyCodexEffort(scope, thread); err != nil {
			return view, err
		}
	}
	view.NativeStatus = thread.Status.Type
	turns, err := readTurns(ctx, p, scope.NativeID)
	if err != nil {
		c.readFailed(scope.Instance, err)
		return view, err
	}
	if len(turns) > 0 {
		delete(c.creationPolicies, scope.Instance.ID+"/"+scope.NativeID)
	}
	slices.Reverse(turns)
	view.Status = "idle"
	if thread.Status.Type == "active" {
		view.Status = "running"
	}
	for _, turn := range turns {
		view.Turns = append(view.Turns, Turn{ID: turn.ID, Status: turn.Status, CompletedAt: nativeTimestamp(turn.CompletedAt), Failure: codexFailure(turn.Error)})
		if turn.Status == "inProgress" {
			view.Status = "running"
		}
		for _, item := range turn.Items {
			if item.Type == "commandExecution" {
				view.Tools = append(view.Tools, ToolResult{ID: item.ID, TurnID: turn.ID, Command: item.Command, Output: item.Output, ExitCode: item.ExitCode, Complete: item.ExitCode != nil})
			}
			if item.Delivery == "async" && len(item.Questions) > 0 {
				c.recordAsync(scope, turn, item)
			}
			role, text := "tool", item.Output
			switch item.Type {
			case "userMessage":
				role, text = "user", item.Text
				for _, part := range item.Content {
					if part.Type == "text" {
						text += part.Text
					}
				}
			case "agentMessage":
				role, text = "assistant", item.Text
			case "mcpToolCall":
				text = item.Server + "." + item.Tool + " · " + item.Status
			default:
				if text == "" {
					text = item.Command
				}
			}
			if text != "" {
				view.Messages = append(view.Messages, Message{ID: item.ID, TurnID: turn.ID, Role: role, Text: text, Status: turn.Status, Operation: item.ClientID})
			}
		}
	}
	c.collect(scope.Instance, p)
	key := scope.Instance.ID + "/" + scope.NativeID
	if view.Status == "running" {
		for _, part := range c.streams[key] {
			found := false
			for n := range view.Messages {
				if view.Messages[n].ID == part.ID && view.Messages[n].TurnID == part.TurnID {
					if len(view.Messages[n].Text) < len(part.Text) {
						view.Messages[n].Text = part.Text
					}
					found = true
					break
				}
			}
			if !found {
				view.Messages = append(view.Messages, part)
			}
		}
	} else {
		delete(c.streams, key)
	}
	allowUnloaded := false
	if scope.NoAutoResume && thread.Status.Type == "notLoaded" && scope.ConfigurationBaseline != "" {
		baseline, baselineErr := configurationBaseline(ctx, p, scope.NativeID)
		allowUnloaded = baselineErr == nil && baseline == scope.ConfigurationBaseline
	}
	view.Executions, err = executions(ctx, p, scope.NativeID, allowUnloaded)
	view.Evidence = Evidence{Source: "codex.thread/read+turns/list+queue+executions", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: err == nil, Detail: "原生轮次、队列、后台终端和子会话核对"}
	if allowUnloaded && err == nil {
		view.Evidence.Detail = "原线程未加载；准备阶段完整空闲核对的历史指纹仍相同，队列、Goal 和子会话已核对"
	}
	if scope.Purpose == "probe" {
		tools, toolErr := codexProbeTools(scope, thread.Path)
		if toolErr != nil {
			return view, toolErr
		}
		view.Tools = append(view.Tools, tools...)
	}
	if err != nil {
		view.Evidence.Detail = "执行范围查询不完整: " + err.Error()
	}
	selectionsMatch := true
	if !scope.NoAutoResume || thread.Status.Type != "notLoaded" {
		// A disk thread has no live MCP inventory. Do not load it for a read.
		view.CapabilityEvidence, selectionsMatch = codexMCPEvidence(ctx, p, scope, turns)
	}
	if len(scope.PluginSelections) > 0 {
		view.PluginEvidence = codexPluginEvidence(scope, view.CapabilityEvidence)
		if codexMixedPlugins(scope.PluginSelections) {
			var policyErr error
			view.pluginPolicy, policyErr = c.readPluginPolicy(ctx, p, scope)
			policyMatches := policyErr == nil && view.pluginPolicy.matches(scope.PluginSelections) && thread.Status.Type != "notLoaded"
			selectionsMatch = selectionsMatch && policyMatches
			for n := range view.PluginEvidence {
				row := &view.PluginEvidence[n]
				row.Coverage = "cached_package_and_thread_policy"
				// A Skill-only package has no MCP row to carry cache/inventory
				// failures. Keep the complete selection gate in its evidence too.
				row.Verified = row.Verified && policyMatches && selectionsMatch
				coverage := "固定插件包与整包策略"
				for _, component := range row.Components {
					if component.Kind == "mcp" {
						coverage = "固定插件包、MCP 来源与整包策略"
						break
					}
				}
				row.Detail = coverage + "尚未核对一致"
				if row.Verified {
					row.Detail = coverage + "已核对；Skill 描述及正文载入以逐轮上下文为准，不代表方法已使用"
				}
			}
		}
		// A source can become natively discoverable after this independent
		// copy was selected. Reads must withdraw readiness too, without resume.
		if sourceErr := verifyCodexSkillAssetSources(ctx, p, scope); sourceErr != nil {
			selectionsMatch = false
			for n := range view.PluginEvidence {
				if scope.PluginSelections[n].SkillAsset != nil {
					view.PluginEvidence[n].Verified = false
					view.PluginEvidence[n].Detail = sourceErr.Error()
				}
			}
		}
		if hasCodexHookSelections(scope) {
			hookErr := c.verifySelectedHooks(ctx, p, scope)
			selectionsMatch = selectionsMatch && hookErr == nil
			for n := range view.PluginEvidence {
				if scope.PluginSelections[n].Hooks == nil {
					continue
				}
				row := &view.PluginEvidence[n]
				row.Coverage = "package_and_connection_hook_configuration"
				row.Verified = row.Verified && hookErr == nil
				row.Detail = "固定包、整包策略与本连接建立的 hook 配置已核对；信任与实际执行分别记录"
				if !row.Verified {
					row.Detail = "本连接的 hook 配置已有建立依据；完整包、MCP 或整包策略仍待核对"
				}
				if hookErr != nil {
					row.Detail = hookErr.Error()
				}
			}
		}
	}
	var skillMatches bool
	view.SkillEvidence, skillMatches = c.directorySkillEvidence(ctx, p, scope)
	selectionsMatch = selectionsMatch && skillMatches
	// Execution and inventory reads can deliver new requests or resolutions.
	// Project pending state after the last RPC before deciding to sample/send.
	c.collect(scope.Instance, p)
	c.expireAsync(scope, turns)
	for key, pending := range c.pending {
		if !strings.HasPrefix(key, scope.Instance.ID+"/") || pending.NativeID != scope.NativeID {
			continue
		}
		if pending.Status == "unverified" && view.Evidence.Complete {
			for _, turn := range view.Turns {
				if turn.ID == pending.NativeTurn && turn.Status != "inProgress" {
					pending.Status = "expired"
					c.pending[pending.ID] = pending
				}
			}
		}
		view.Pending = append(view.Pending, pending)
		if pending.Status != "resolved" && pending.Status != "expired" {
			delete(c.creationPolicies, scope.Instance.ID+"/"+scope.NativeID)
		}
		if pending.Status == "pending" || pending.Status == "submitted" {
			view.Status = "waiting_approval"
			if pending.ResponseKind == "user_input" || pending.ResponseKind == "async_input" || pending.ResponseKind == "message_input" {
				view.Status = "waiting_input"
			}
		} else if pending.Status == "unverified" && view.Status == "idle" {
			view.Status = "unknown"
		}
	}
	sortPending(view.Pending)
	c.projectHookExecutions(scope, &view)
	if len(view.Executions) > 0 {
		delete(c.creationPolicies, scope.Instance.ID+"/"+scope.NativeID)
	}
	view.ExecutionClear = view.Evidence.Complete && len(view.Executions) == 0 && view.Status == "idle" && selectionsMatch
	if view.Status == "idle" && !view.ExecutionClear {
		view.Status = "unknown"
		if view.Evidence.Complete && selectionsMatch {
			view.Status = "running"
		}
	}
	if !scope.NoAutoResume && scope.RecoveryPermission != "" && view.PermissionEvidence == nil && selectionsMatch && codexIdleForPermission(view, replyID) {
		return c.sampleLoadedPermission(ctx, p, scope, thread, view, readTurns, replyID)
	}
	return view, nil
}

func (c *Codex) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, p := range c.clients {
		p.Close()
		delete(c.clients, id)
	}
	c.creationPolicies = map[string]codexCreationPolicy{}
	c.configurationRecoveries = nil
	return nil
}
