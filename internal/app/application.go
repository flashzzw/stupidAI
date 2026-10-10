package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type Application struct {
	mu                        sync.Mutex
	config                    Config
	instances                 []InstanceStatus
	store                     Persistence
	runtime                   runtime.Adapter
	state                     State
	busy                      map[string]bool
	observing                 map[string]bool
	fatal                     error
	ctx                       context.Context
	cancel                    context.CancelFunc
	wg                        sync.WaitGroup
	closed                    bool
	hookInspections           map[string]capabilityHookReceipt
	pluginSettingsStages      map[string]*pluginSettingsStage
	retiringInstalledSettings bool
}

type Snapshot struct {
	Sequence         uint64            `json:"sequence"`
	Sessions         []Session         `json:"sessions"`
	Operations       []Operation       `json:"operations"`
	Projects         []Project         `json:"projects"`
	Routes           []runtime.Route   `json:"routes"`
	Instances        []InstanceStatus  `json:"instances"`
	RouteStatuses    []RouteStatus     `json:"route_statuses"`
	Error            string            `json:"error,omitempty"`
	CapabilityAssets []CapabilityAsset `json:"capability_assets,omitempty"`
}

func Open(c Config, store Persistence, adapter runtime.Adapter) (*Application, error) {
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	if state.DomainEvents == nil {
		state.DomainEvents = newState().DomainEvents
	}
	if state.ObservationReceipts == nil {
		state.ObservationReceipts = map[string]ObservationReceipt{}
	}
	if state.Probes == nil {
		state.Probes = map[string]ProbeRecord{}
	}
	if state.RouteHealth == nil {
		state.RouteHealth = map[string]RouteHealth{}
	}
	if state.ExecutionProfiles == nil {
		state.ExecutionProfiles = map[string]ExecutionProfile{}
	}
	c, err = mergeExecutionProfiles(c, state.ExecutionProfiles)
	if err != nil {
		return nil, err
	}
	if state.CapabilityLibrary == nil {
		state.CapabilityLibrary = map[string]CapabilityEntry{}
	}
	if err := checkCapabilityStartup(c, state.CapabilityLibrary); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	a := &Application{config: c, store: store, runtime: adapter, state: state, busy: map[string]bool{}, observing: map[string]bool{}, ctx: ctx, cancel: cancel}
	a.instances = describeInstances(ctx, c, adapter)
	for id, probe := range a.state.Probes {
		if probe.DeadlineAt == "" && !probeTerminal(probe.Status) {
			started, err := time.Parse(time.RFC3339Nano, probe.StartedAt)
			if err != nil {
				probe.TimedOut = true
			} else {
				probe.DeadlineAt = started.Add(a.probeTimeout()).Format(time.RFC3339Nano)
			}
			a.state.Probes[id] = probe
		}
	}
	for id, op := range state.Operations {
		if !terminal(op.Status) {
			if op.Status == "prepared" {
				if op.Intent.Kind == "recover_execution" {
					s := a.state.Sessions[op.SessionID]
					releaseExecutionRecovery(&s, op)
					a.state.Sessions[s.ID] = s
				}
				if op.Intent.Kind == "apply_execution" || op.Intent.Kind == "prepare_execution" {
					s := a.state.Sessions[op.SessionID]
					if s.PendingExecutionChange != nil && s.PendingExecutionChange.OperationID == op.ID {
						s.PendingExecutionChange.Status = "pending"
					}
					a.state.Sessions[s.ID] = s
				}
				op.Status = "not_sent"
				op.Evidence = append(op.Evidence, runtime.Evidence{Source: "console.restart", At: now(), Complete: true, Detail: "只有 prepared 意图，没有进入 dispatching；证明未发送"})
				a.state.Operations[id] = op
				session := a.state.Sessions[op.SessionID]
				if session.Scope.NativeID == "" {
					session.Status = "closed"
				} else {
					session.Status = "unknown"
					session.Connection = "reconciling"
				}
				a.state.Sessions[session.ID] = session
				continue
			}
			op.Status = "uncertain"
			op.Evidence = append(op.Evidence, runtime.Evidence{Source: "console.restart", At: now(), Detail: "重启后先核对，不能推断原执行已停止"})
			a.state.Operations[id] = op
			s := a.state.Sessions[op.SessionID]
			s.Status, s.Connection = "unknown", "reconciling"
			a.state.Sessions[s.ID] = s
		}
	}
	for id, session := range a.state.Sessions {
		if session.ControlMode == "observed" || session.Origin == "native" {
			continue
		}
		// Existing Alpha records can acquire a binding only when the saved
		// route, instance policy and original profile fingerprint still match.
		if session.RouteFingerprint == "" && runtime.VerifyCredentialStamp(session.Scope) == nil {
			profile, err := profileFingerprint(session.Scope.Instance)
			if err == nil && profile == session.Fingerprint {
				session.RouteFingerprint, _ = routeFingerprint(session.Scope.Instance, session.Scope.Route)
				if a.verifyRouteBinding(session) != nil {
					session.RouteFingerprint = ""
				}
			}
		}
		if err := a.requireCapability(session.Scope.Instance.ID, "history"); err != nil {
			session.Status, session.Connection = "unknown", "incompatible"
			session.View.Evidence = runtime.Evidence{Source: "console.compatibility", At: now(), Detail: err.Error()}
		}
		for n := range session.View.Pending {
			if session.View.Pending[n].Status == "pending" || session.View.Pending[n].Status == "submitted" {
				session.View.Pending[n].Status = "unverified"
			}
		}
		a.state.Sessions[id] = session
	}
	if err = store.Save(a.state); err != nil {
		cancel()
		return nil, err
	}
	a.wg.Add(1)
	go a.poll()
	return a, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func clone(s State) State {
	b, _ := json.Marshal(s)
	var result State
	_ = json.Unmarshal(b, &result)
	return result
}

// commit runs with mu held. The in-memory facts advance only after SQLite does.
func (a *Application) commit(next State, kind, sessionID string) error {
	for id, session := range next.Sessions {
		retainCodexHookHistory(a.state.Sessions[id], &session.View)
		bindCodexHookOperations(&session, next.Operations)
		bindClaudeInstructionLoads(&session, next.Operations)
		if session.Scope.Instance.Kind == "codex" && session.Connection != "ready" {
			for n := range session.View.HookExecutions {
				if session.View.HookExecutions[n].Status == "running" {
					session.View.HookExecutions[n].Status = "unknown"
				}
			}
			runtime.RetainCodexHookExecutions(nil, &session.View)
		}
		if session.Scope.Instance.Kind == "codex" && session.Status == "idle" && (session.View.Status == "unknown" || session.View.Status == "running") {
			session.Status = session.View.Status
		}
		next.Sessions[id] = session
	}
	a.collectRouteHealth(a.state, &next)
	if err := collectManagedEvents(a.state, &next, now()); err != nil {
		return err
	}
	next.Sequence++
	next.Events = append(next.Events, Event{Seq: next.Sequence, Kind: kind, SessionID: sessionID, At: now()})
	if len(next.Events) > 500 {
		next.Events = next.Events[len(next.Events)-500:]
	}
	if err := a.store.Save(next); err != nil {
		a.fatal = fmt.Errorf("状态持久化失败，已停止新发送: %w", err)
		return a.fatal
	}
	a.state = next
	return nil
}

func (a *Application) Apply(command Command) (CommandReceipt, error) {
	if command.SkillInput != nil && command.Kind != "send" {
		return CommandReceipt{}, fmt.Errorf("skill_input 仅用于发送一条新消息")
	}
	if command.Kind == "import_capability_package" {
		return a.importCapabilityPackage(command)
	}
	if command.Kind == "manage_capability" {
		return a.manageCapability(command)
	}
	if command.Kind == "register_execution" {
		return a.registerExecutionProfile(command)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applyCommand(command, false)
}

func (a *Application) applyCommand(command Command, automatic bool) (CommandReceipt, error) {
	if a.closed {
		return CommandReceipt{}, fmt.Errorf("控制台正在关闭")
	}
	if a.fatal != nil {
		return CommandReceipt{}, a.fatal
	}
	if !validID(command.ID) {
		return CommandReceipt{}, fmt.Errorf("command_id 无效")
	}
	b, _ := json.Marshal(command)
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	if receipt, ok := a.state.Commands[command.ID]; ok {
		if receipt.Hash != hash {
			return CommandReceipt{}, fmt.Errorf("command_id 已用于其他内容")
		}
		return queryCopy(receipt), nil
	}
	if session, ok := a.state.Sessions[command.SessionID]; ok && session.Origin == "native" && command.Kind != "archive" && command.Kind != "unarchive" {
		return CommandReceipt{}, fmt.Errorf("原生只读来源尚未取得原执行控制证据；只能读取历史或调整本地归档状态")
	}
	if command.CapabilityChange != nil {
		return CommandReceipt{}, fmt.Errorf("capability_change 仅用于能力库管理命令")
	}
	if command.CapabilityPackage != nil {
		return CommandReceipt{}, fmt.Errorf("capability_package 仅用于资源包导入命令")
	}
	if command.Kind == "prepare_mcp_login" {
		return a.prepareMCPLogin(command, hash)
	}
	if command.MCPLogin != nil {
		return CommandReceipt{}, fmt.Errorf("mcp_login 仅用于准备原生认证命令")
	}
	if command.Kind == "recover_execution" {
		return a.startExecutionRecovery(command, hash)
	}
	if command.ExecutionRecovery != nil {
		return CommandReceipt{}, fmt.Errorf("execution_recovery 仅用于明确恢复命令")
	}
	if command.Kind == "stage_execution" || command.Kind == "discard_execution" || command.Kind == "prepare_execution" || command.Kind == "apply_execution" {
		return a.changeExecution(command, hash)
	}
	if command.ExecutionChange != nil {
		return CommandReceipt{}, fmt.Errorf("execution_change 仅用于明确的改配命令")
	}
	if command.ExecutionProfile != nil {
		return CommandReceipt{}, fmt.Errorf("execution_profile 仅用于执行配置登记")
	}
	if command.SkillInput != nil {
		selected, ok := a.state.Sessions[command.SessionID]
		if !ok || selected.Origin != "console" || selected.ExecutionSnapshot == nil || command.SkillInput.SnapshotID == "" || command.SkillInput.SnapshotID != selected.ExecutionSnapshot.ID || len(command.SkillInput.Items) == 0 {
			return CommandReceipt{}, fmt.Errorf("Skill 请求需要当前会话快照及明确选择；改配后请重新选择")
		}
		if _, err := runtime.ResolveCodexSkillInputs(selected.Scope, command.SkillInput.Items); err != nil {
			return CommandReceipt{}, err
		}
	}
	next := clone(a.state)
	session, exists := next.Sessions[command.SessionID]
	if session.ExecutionHold && (command.Kind == "send" || command.Kind == "respond" || command.Kind == "resume") {
		return CommandReceipt{}, fmt.Errorf("改配准备或应用核对中，原会话保持暂停；完成核对或撤销待应用后再继续")
	}
	if (len(command.CapabilityRefs) > 0 || len(command.CapabilitySelections) > 0) && command.Kind != "create" {
		return CommandReceipt{}, fmt.Errorf("capability_refs/capability_selections 仅用于新建；原会话改配请使用 execution_change 专用命令")
	}
	if exists && session.Origin == "probe" && !automatic && (command.Kind == "send" || command.Kind == "respond" || command.Kind == "resume") {
		return CommandReceipt{}, fmt.Errorf("探测会话只接受已登记的固定动作，不能注入交互输入")
	}
	receipt := CommandReceipt{ID: command.ID, Hash: hash, Status: "accepted", SessionID: command.SessionID}
	var op *Operation
	var pending *runtime.Pending
	var nativeTurn string
	switch command.Kind {
	case "create", "probe":
		var project Project
		var route runtime.Route
		var instance runtime.Instance
		for _, p := range a.config.Projects {
			if p.ID == command.ProjectID {
				project = p
			}
		}
		for _, r := range a.config.Routes {
			if r.ID == command.RouteID {
				route = r
			}
		}
		for _, i := range a.config.Instances {
			if i.ID == route.InstanceID {
				instance = i
			}
		}
		if command.Kind == "probe" {
			if command.ProbeMode == "" {
				command.ProbeMode = "utc-date"
			}
			if err := validateProbeConfig(instance, route, command.ProbeMode); err != nil {
				return receipt, err
			}
			key, err := healthKey(instance, route)
			if err != nil {
				return receipt, err
			}
			for _, probe := range a.state.Probes {
				if probe.RouteID == route.ID && !probeTerminal(probe.Status) {
					if probe.HealthKey != key || probe.Mode != command.ProbeMode {
						return receipt, fmt.Errorf("路由已有不同配置或模式的探测，先核对原操作")
					}
					receipt.SessionID, receipt.OperationID, receipt.Status = probe.SessionID, probe.CreateOperationID, "coalesced"
					next.Commands[command.ID] = receipt
					if err := a.commit(next, "probe.coalesced", probe.SessionID); err != nil {
						return CommandReceipt{}, err
					}
					return receipt, nil
				}
			}
			health := a.state.RouteHealth[key]
			expires, _ := time.Parse(time.RFC3339Nano, health.ExpiresAt)
			if health.State == "healthy" && time.Now().Before(expires) && (health.Proof == command.ProbeMode || health.Proof == "utc-date") {
				probe := a.state.Probes[health.LastProbeID]
				receipt.SessionID, receipt.Status = probe.SessionID, "cached"
				next.Commands[command.ID] = receipt
				if err := a.commit(next, "probe.cached", probe.SessionID); err != nil {
					return CommandReceipt{}, err
				}
				return receipt, nil
			}
			if err := a.admitProbe(route.ID, ""); err != nil {
				return receipt, err
			}
			if err := a.admitRouteHealth(key); err != nil {
				return receipt, err
			}
			project = Project{ID: "_probes", Name: "路由探测", Cwd: filepath.Join(a.config.StateDir, "probes")}
			command.Permission = "read_only"
			command.Title = route.ID + " · " + command.ProbeMode
		}
		if project.ID == "" || route.ID == "" || instance.ID == "" {
			return receipt, fmt.Errorf("请选择已登记的项目与路由")
		}
		if command.Permission != "read_only" && command.Permission != "workspace" {
			return receipt, fmt.Errorf("请选择 read_only 或 workspace 权限")
		}
		fingerprint, err := profileFingerprint(instance)
		if err != nil {
			return receipt, err
		}
		session = Session{ID: uuid.NewString(), Title: strings.TrimSpace(command.Title), ProjectID: project.ID,
			Scope: runtime.Scope{Instance: instance, Route: route, Cwd: project.Cwd}, Fingerprint: fingerprint,
			Origin: "console", ControlMode: "managed", Permission: command.Permission, Status: "starting", Connection: "reconciling", Generation: 1,
			UpdatedAt: now(), View: runtime.View{Messages: []runtime.Message{}, Pending: []runtime.Pending{}}}
		if session.Title == "" {
			session.Title = "新会话"
		}
		if len(session.Title) > 240 {
			return receipt, fmt.Errorf("标题过长")
		}
		session.Scope.SessionID = session.ID
		session.Scope.CredentialStamp, err = runtime.CredentialStamp(route)
		if err != nil {
			return receipt, err
		}
		if command.Kind == "probe" {
			session.Origin = "probe"
			session.Scope.Purpose = "probe"
			session.Scope.Cwd = filepath.Join(a.config.StateDir, "probes", session.ID, "work")
		}
		session.Scope.StateNamespace, err = runtime.StateNamespace(session.Scope)
		if err != nil {
			return receipt, err
		}
		session.RouteFingerprint, err = routeFingerprint(instance, route)
		if err != nil {
			return receipt, err
		}
		session.ExecutionSnapshot, err = a.captureExecution(session, command.CapabilityRefs, command.CapabilitySelections)
		if err != nil {
			return receipt, err
		}
		if session.ExecutionSnapshot != nil {
			session.Scope.MCPSelections = session.ExecutionSnapshot.MCPSelections
			session.Scope.SkillSelections = session.ExecutionSnapshot.SkillSelections
			session.Scope.PluginSelections = session.ExecutionSnapshot.PluginSelections
			session.Scope.SettingsGuardVersion = claudeSettingsGuardVersion(session.ExecutionSnapshot)
			session.Scope.ClaudeSettingsSources = claudeNativeSettingsSources(session.ExecutionSnapshot)
			session.Scope.CodexUserHooks = codexUserHooks(session.ExecutionSnapshot)
			session.Scope.CodexProjectHooks = codexProjectHooks(session.ExecutionSnapshot)
			session.Scope.CodexConfigurationSources = codexConfigurationSources(session.ExecutionSnapshot)
		}
		receipt.SessionID = session.ID
	case "send":
		if !exists {
			return receipt, fmt.Errorf("会话不存在")
		}
		if session.ControlMode == "observed" || session.Status != "idle" || a.occupied(session.ID) || a.busy[session.ID] {
			return receipt, fmt.Errorf("会话尚未空闲或操作结果待核对")
		}
		if strings.TrimSpace(command.Text) == "" || len(command.Text) > 200000 {
			return receipt, fmt.Errorf("消息为空或过长")
		}
		if err := a.verifyRouteBinding(session); err != nil {
			return receipt, err
		}
		fingerprint, err := profileFingerprint(session.Scope.Instance)
		if err != nil || fingerprint != session.Fingerprint {
			return receipt, fmt.Errorf("实例配置已变化，需核对路由后创建新会话")
		}
		session.Status = "running"
		session.Generation++
	case "respond":
		if !exists || session.ControlMode == "observed" || a.busy[session.ID] {
			return receipt, fmt.Errorf("会话不可控制或已有命令正在发送")
		}
		for _, item := range session.View.Pending {
			if item.ID == command.PendingID && item.Status == "pending" {
				copy := item
				pending = &copy
			}
		}
		if pending == nil {
			return receipt, fmt.Errorf("请求已处理、过期或尚待核对")
		}
		if err := a.verifyRouteBinding(session); err != nil {
			return receipt, err
		}
		for _, existing := range next.Operations {
			if existing.Intent.Pending != nil && existing.Intent.Pending.ID == pending.ID && !terminal(existing.Status) {
				return receipt, fmt.Errorf("该请求已有回复操作，不能重复回答")
			}
		}
		session.Generation++
	case "interrupt":
		if !exists || session.ControlMode == "observed" || a.busy[session.ID] {
			return receipt, fmt.Errorf("会话不可中止")
		}
		for _, existing := range next.Operations {
			if existing.SessionID == session.ID && createsInput(existing.Intent) && !terminal(existing.Status) && existing.Receipt.NativeTurn != "" {
				nativeTurn = existing.Receipt.NativeTurn
			}
		}
		if nativeTurn == "" {
			return receipt, fmt.Errorf("没有可确认归属的在途轮次，请先核对或转人工")
		}
		session.Paused = true
		session.Generation++
	case "pause", "takeover", "resume":
		if !exists || session.ControlMode == "observed" {
			return receipt, fmt.Errorf("会话不可控制或仅观察")
		}
		if command.Kind == "resume" && (a.occupied(session.ID) || a.busy[session.ID]) {
			return receipt, fmt.Errorf("未决操作不会被 resume 清除")
		}
		// Resume remains paused until its fresh, read-only native check commits.
		session.Paused = true
		session.Generation++
		if command.Kind == "takeover" {
			session.ControlMode = "human_takeover"
		}
		if command.Kind == "resume" {
			if err := a.verifyRouteBinding(session); err != nil {
				return receipt, err
			}
		}
	case "archive", "unarchive":
		if !exists {
			return receipt, fmt.Errorf("会话不存在")
		}
		// Archive changes local visibility only. In-flight observations and
		// receipts must still advance the original execution generation.
		session.Archived = command.Kind == "archive"
	case "reconcile":
		if !exists || session.ControlMode == "observed" {
			return receipt, fmt.Errorf("仅观察来源没有可控制的原生连接")
		}
	case "associate":
		if !exists || a.busy[session.ID] || session.ControlMode == "observed" {
			return receipt, fmt.Errorf("会话不存在或正在处理写入")
		}
		target, ok := next.Operations[command.OperationID]
		if !ok || target.SessionID != session.ID || terminal(target.Status) || (target.Intent.Kind != "create" && target.Intent.Kind != "send") {
			return receipt, fmt.Errorf("只能关联本会话尚未核对的创建或发送操作")
		}
		if command.NativeID == "" || len(command.NativeID) > 200 || len(command.NativeTurn) > 200 {
			return receipt, fmt.Errorf("候选原生引用无效")
		}
		session.Generation++
	default:
		return receipt, fmt.Errorf("不支持的控制台命令 %q", command.Kind)
	}
	capability := map[string]string{"create": "create", "probe": "probe", "send": "send", "interrupt": "interrupt", "resume": "resume", "associate": "recovery"}[command.Kind]
	if command.Kind == "respond" {
		capability = "approval"
		if pending.ResponseKind != "approval" {
			capability = "question"
		}
	}
	if capability != "" {
		if err := a.requireCapability(session.Scope.Instance.ID, capability); err != nil {
			return receipt, err
		}
	}
	if command.Kind == "create" || command.Kind == "probe" || command.Kind == "send" || command.Kind == "respond" || command.Kind == "interrupt" || command.Kind == "associate" || command.Kind == "resume" {
		id := uuid.NewString()
		op = &Operation{ID: id, CommandID: command.ID, SessionID: session.ID, Generation: session.Generation,
			Intent: runtime.Intent{ID: id, Kind: command.Kind, Scope: session.Scope, Text: command.Text, Permission: session.Permission, Pending: pending, OptionID: command.OptionID, Answers: command.Answers, NativeTurn: nativeTurn},
			Status: "prepared", CreatedAt: now(), Evidence: []runtime.Evidence{}}
		op.Intent.CreatedAt = op.CreatedAt
		if session.ExecutionSnapshot != nil {
			op.ExecutionSnapshotID = session.ExecutionSnapshot.ID
		}
		if command.Kind == "probe" {
			op.Intent.Kind = "create"
		}
		for _, existing := range next.Operations {
			if existing.SessionID == session.ID && createsInput(existing.Intent) {
				op.Intent.OwnedInputs = append(op.Intent.OwnedInputs, existing.ID)
			}
		}
		if command.Kind == "send" {
			op.Intent.OwnedInputs = append(op.Intent.OwnedInputs, id)
			if command.SkillInput != nil {
				op.Intent.SkillInputs = queryCopy(command.SkillInput.Items)
			}
		}
		if command.Kind == "associate" {
			op.TargetOperationID = command.OperationID
			op.Receipt = runtime.Receipt{NativeID: command.NativeID, NativeTurn: command.NativeTurn}
		}
		receipt.OperationID = id
		next.Operations[id] = *op
		if command.Kind == "probe" {
			key, _ := healthKey(session.Scope.Instance, session.Scope.Route)
			next.Probes[session.ID] = ProbeRecord{ID: session.ID, SessionID: session.ID, RouteID: session.Scope.Route.ID, HealthKey: key, Mode: command.ProbeMode, CreateOperationID: op.ID, Status: "starting", StartedAt: op.CreatedAt, DeadlineAt: time.Now().UTC().Add(a.probeTimeout()).Format(time.RFC3339Nano)}
		}
		if command.Kind == "send" && automatic && session.Origin == "probe" {
			probe := next.Probes[session.ID]
			probe.SendOperationID = op.ID
			probe.Status = "running"
			next.Probes[session.ID] = probe
		}
		if command.Kind == "interrupt" && automatic && session.Origin == "probe" {
			probe := next.Probes[session.ID]
			probe.StopOperationID, probe.Status = op.ID, "stopping"
			next.Probes[session.ID] = probe
		}
	}
	session.UpdatedAt = now()
	next.Sessions[session.ID] = session
	next.Commands[command.ID] = receipt
	if err := a.commit(next, "command.accepted", session.ID); err != nil {
		return CommandReceipt{}, err
	}
	if op != nil {
		a.busy[session.ID] = true
		a.wg.Add(1)
		go a.dispatch(*op)
	}
	return receipt, nil
}

func createsInput(in runtime.Intent) bool {
	return in.Kind == "send" || (in.Kind == "respond" && in.Pending != nil && in.Pending.ResponseKind == "message_input")
}

func (a *Application) occupied(sessionID string) bool {
	for _, op := range a.state.Operations {
		if op.SessionID == sessionID && !terminal(op.Status) {
			return true
		}
	}
	return false
}

func (a *Application) dispatch(op Operation) {
	defer a.wg.Done()
	a.mu.Lock()
	next := clone(a.state)
	current := next.Operations[op.ID]
	cancelReason := ""
	if a.closed || next.Sessions[op.SessionID].Generation != op.Generation {
		cancelReason = "发送前代次变化，撤销自有 prepared 意图"
	} else if err := runtime.VerifyStateNamespace(op.Intent.Scope); err != nil {
		cancelReason = err.Error()
	} else if op.Intent.Kind == "prepare_execution" || op.Intent.Kind == "apply_execution" || op.Intent.Kind == "recover_execution" {
		if err := a.verifyExecutionChangeOperation(next.Sessions[op.SessionID], op); err != nil {
			cancelReason = err.Error()
		}
	} else if next.Sessions[op.SessionID].ExecutionSnapshot != nil && (op.Intent.Kind == "create" || op.Intent.Kind == "send" || op.Intent.Kind == "respond" || op.Intent.Kind == "resume") {
		session := next.Sessions[op.SessionID]
		if err := verifyExecutionSources(session); err != nil {
			cancelReason = err.Error()
		} else if session.ExecutionSnapshot != nil && op.ExecutionSnapshotID != session.ExecutionSnapshot.ID {
			cancelReason = "操作引用的执行快照与原会话不一致"
		}
	} else if op.Intent.Scope.Purpose == "probe" && op.Intent.Kind == "send" {
		session := next.Sessions[op.SessionID]
		probe := next.Probes[session.ID]
		if session.Paused || session.ControlMode != "managed" {
			cancelReason = "发送前自动化已暂停或转人工"
		}
		if err := a.verifyRouteBinding(session); err != nil {
			cancelReason = err.Error()
		}
		if err := a.admitProbe(probe.RouteID, session.ID); err != nil {
			cancelReason = err.Error()
		}
		if err := a.admitRouteHealth(probe.HealthKey); err != nil {
			cancelReason = err.Error()
		}
		if deadline, err := time.Parse(time.RFC3339Nano, probe.DeadlineAt); err == nil && time.Now().After(deadline) {
			cancelReason = "发送前探测期限已过"
		}
	}
	if cancelReason != "" {
		if op.Intent.Kind == "recover_execution" {
			a.finishExecutionRecoveryReceipt(next, current, runtime.Receipt{}, &runtime.NotSent{Reason: cancelReason})
			delete(a.busy, op.SessionID)
			a.mu.Unlock()
			return
		}
		if op.Intent.Kind == "prepare_execution" || op.Intent.Kind == "apply_execution" {
			a.finishExecutionChangeReceipt(next, current, runtime.Receipt{}, &runtime.NotSent{Reason: cancelReason})
			delete(a.busy, op.SessionID)
			a.mu.Unlock()
			return
		}
		current.Status = "not_sent"
		current.Error = cancelReason
		current.Evidence = append(current.Evidence, runtime.Evidence{Source: "console.dispatch", At: now(), Complete: true, Detail: cancelReason})
		next.Operations[op.ID] = current
		if op.Intent.Kind == "create" {
			session := next.Sessions[op.SessionID]
			session.Status = "closed"
			next.Sessions[session.ID] = session
		}
		_ = a.commit(next, "operation.not_sent", op.SessionID)
		delete(a.busy, op.SessionID)
		a.mu.Unlock()
		return
	}
	current.Status = "dispatching"
	next.Operations[op.ID] = current
	if err := a.commit(next, "operation.dispatching", op.SessionID); err != nil {
		delete(a.busy, op.SessionID)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	timeout := 15 * time.Second
	if op.Intent.Kind == "create" {
		// Cold native startup may initialize explicitly configured plugin
		// dependencies before accepting a session. The intent is already durable.
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	var receipt runtime.Receipt
	var err error
	var association runtime.Resolution
	var handoff runtime.View
	if op.Intent.Kind == "prepare_execution" || op.Intent.Kind == "apply_execution" || op.Intent.Kind == "recover_execution" {
		// A display poll may already be restoring the old binding. Wait for
		// that read to retire before the configuration RPC; busy prevents new
		// polls and Generation prevents the retired view from committing.
		err = a.awaitExecutionObservation(ctx, op)
		if err == nil {
			receipt, err = a.runtime.Execute(ctx, op.Intent)
		}
	} else if op.Intent.Kind == "resume" {
		scope := op.Intent.Scope
		scope.RecoveryPermission = op.Intent.Permission
		handoff, err = a.runtime.ObserveFresh(ctx, scope)
	} else if op.Intent.Kind == "associate" {
		a.mu.Lock()
		target := a.state.Operations[op.TargetOperationID]
		a.mu.Unlock()
		association, err = a.runtime.Reconcile(ctx, target.Intent, op.Receipt)
		if err == nil && ((association.State != "running" && association.State != "finished") || !association.Evidence.Complete) {
			err = fmt.Errorf("候选引用未通过原生身份与输入核对")
		}
		if err == nil && op.Intent.Scope.NativeID != "" && association.Receipt.NativeID != op.Intent.Scope.NativeID {
			err = fmt.Errorf("候选引用不能替换已绑定的原生会话")
		}
		receipt = association.Receipt
	} else {
		receipt, err = a.runtime.Execute(ctx, op.Intent)
	}
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.busy, op.SessionID)
	next = clone(a.state)
	current = next.Operations[op.ID]
	session := next.Sessions[op.SessionID]
	if op.Intent.Kind == "recover_execution" {
		a.finishExecutionRecoveryReceipt(next, current, receipt, err)
		return
	}
	if op.Intent.Kind == "prepare_execution" || op.Intent.Kind == "apply_execution" {
		a.finishExecutionChangeReceipt(next, current, receipt, err)
		return
	}
	if op.Intent.Kind == "resume" {
		a.finishHandoff(next, current, handoff, err)
		return
	}
	current.Receipt = receipt
	if op.Intent.Kind == "associate" {
		current.Status = "failed"
		current.Evidence = append(current.Evidence, association.Evidence)
		if err != nil {
			current.Error = err.Error()
		} else {
			current.Status = "succeeded"
			target := next.Operations[op.TargetOperationID]
			target.Receipt = association.Receipt
			target.Error = ""
			target.Evidence = append(target.Evidence, association.Evidence)
			target.Status = "running"
			if association.State == "finished" {
				target.Status = association.Status
			}
			next.Operations[target.ID] = target
			session.Scope.NativeID = association.Receipt.NativeID
			session.View = association.View
			session.Status = association.View.Status
		}
		next.Operations[op.ID] = current
		next.Sessions[session.ID] = session
		_ = a.commit(next, "operation.associated", session.ID)
		return
	}
	if err != nil {
		current.Status, current.Error = "uncertain", err.Error()
		current.Evidence = append(current.Evidence, runtime.Evidence{Source: "runtime.execute", At: now(), Detail: "可能已送达，等待核对"})
		session.Status, session.Connection = "unknown", "disconnected"
		var notSent *runtime.NotSent
		if errors.As(err, &notSent) {
			current.Status = "not_sent"
			current.Evidence = append(current.Evidence, runtime.Evidence{Source: "runtime.before_send", At: now(), Complete: true, Detail: notSent.Reason})
			if op.Intent.Kind == "create" && receipt.NativeID == "" && session.Scope.NativeID == "" {
				// Positive pre-send evidence means there is no native session
				// whose execution could reserve automatic capacity.
				session.Status = "closed"
			}
		}
	} else {
		current.Status = "acknowledged"
		if session.Scope.NativeID != "" && receipt.NativeID != session.Scope.NativeID {
			current.Status, current.Error = "uncertain", "回执原生身份不匹配，保留原绑定等待核对"
			session.Status, session.Connection, session.Paused = "unknown", "disconnected", true
			next.Operations[op.ID], next.Sessions[session.ID] = current, session
			_ = a.commit(next, "operation.identity_mismatch", session.ID)
			return
		}
		session.Scope.NativeID = receipt.NativeID
		session.Connection = "ready"
		if op.Intent.Kind == "create" {
			current.Status, session.Status = "succeeded", "idle"
		}
	}
	// A late receipt records facts only; it never restores ownership or unpauses.
	next.Operations[op.ID] = current
	next.Sessions[session.ID] = session
	_ = a.commit(next, "operation.receipt", session.ID)
}

func (a *Application) poll() {
	defer a.wg.Done()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			if a.fatal == nil && !a.closed {
				a.startInstalledSettingsRetirement()
				a.tickProbes()
				if a.fatal != nil {
					a.mu.Unlock()
					continue
				}
				for id, session := range a.state.Sessions {
					if a.busy[id] || a.observing[id] || (session.Scope.NativeID == "" && !a.occupied(id)) || session.Status == "closed" || session.ControlMode == "observed" || session.Origin == "native" || a.requireCapability(session.Scope.Instance.ID, "history") != nil {
						continue
					}
					a.observing[id] = true
					a.wg.Add(1)
					go a.observe(session)
				}
			}
			a.mu.Unlock()
		}
	}
}

func (a *Application) observe(session Session) {
	defer a.wg.Done()
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	a.mu.Lock()
	active := []Operation{}
	for _, op := range a.state.Operations {
		if op.SessionID == session.ID && !terminal(op.Status) {
			if p := session.PendingExecutionChange; p != nil && p.RecoveryOperationID != "" && p.OperationID == op.ID {
				continue
			}
			active = append(active, op)
		}
	}
	a.mu.Unlock()
	sort.Slice(active, func(i, j int) bool { return active[i].CreatedAt < active[j].CreatedAt })
	type answer struct {
		op         Operation
		resolution runtime.Resolution
		err        error
	}
	answers := []answer{}
	var view runtime.View
	var observationErr error
	namespaceErr := runtime.VerifyStateNamespace(session.Scope)
	if len(active) == 0 {
		observationErr = namespaceErr
		if observationErr == nil {
			scope := session.Scope
			scope.RecoveryPermission = session.Permission
			a.preservePausedRecoveryObservation(session, &scope)
			if session.ExecutionHold {
				scope.NoAutoResume = true
				if session.PendingExecutionChange != nil {
					scope.ConfigurationBaseline = session.PendingExecutionChange.Baseline
				}
			}
			view, observationErr = a.runtime.Observe(ctx, scope)
		}
	}
	for _, op := range active {
		if namespaceErr != nil {
			answers = append(answers, answer{op: op, err: namespaceErr})
			observationErr = namespaceErr
			continue
		}
		if op.Intent.Kind == "resume" || op.Intent.Kind == "prepare_execution" {
			// Restart cannot replay a control grant. The user can request a new
			// handoff after this interrupted local check is marked not_sent.
			answers = append(answers, answer{op: op, resolution: runtime.Resolution{State: "finished", Status: "not_sent",
				Evidence: runtime.Evidence{Source: "console.handoff", At: now(), Complete: true, Detail: "恢复核对被中断，保持暂停；可重新申请"}}})
			continue
		}
		resolution, err := a.runtime.Reconcile(ctx, op.Intent, op.Receipt)
		if err == nil && session.Scope.NativeID != "" && ((resolution.View.NativeID != "" && resolution.View.NativeID != session.Scope.NativeID) || (resolution.Receipt.NativeID != "" && resolution.Receipt.NativeID != session.Scope.NativeID)) {
			err = fmt.Errorf("核对结果的原生身份不匹配")
		}
		answers = append(answers, answer{op, resolution, err})
		if err != nil {
			observationErr = err
		} else if resolution.View.NativeID != "" {
			view = resolution.View
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.observing, session.ID)
	if a.closed || a.fatal != nil {
		return
	}
	next := clone(a.state)
	current := next.Sessions[session.ID]
	if current.Generation != session.Generation || a.busy[session.ID] {
		return
	}
	if view.NativeID != "" && current.Scope.NativeID != "" && view.NativeID != current.Scope.NativeID {
		observationErr = fmt.Errorf("观察结果的原生身份不匹配，保留原绑定")
	}
	if observationErr != nil {
		invalidatePermissionEvidence(&current, view)
		current.Connection = "disconnected"
		current.Status, current.Paused = "unknown", true
		current.View.Evidence = runtime.Evidence{Source: "console.observe", At: now(), Detail: observationErr.Error()}
	} else {
		current.Connection = "ready"
		if view.NativeID != "" {
			view.Pending = mergePending(current.View.Pending, view)
			retainCodexHookHistory(current, &view)
			current.View = view
			current.Status = view.Status
			current.Scope.NativeID = view.NativeID
			for _, input := range externalInputs(view, current.ID, next.Operations) {
				if !slices.Contains(current.InputBaseline, input) {
					if current.ControlMode != "human_takeover" {
						current.Generation++
					}
					current.ControlMode = "human_takeover"
					current.Paused = true
				}
			}
		}
	}
	for _, answer := range answers {
		op := next.Operations[answer.op.ID]
		resolution := answer.resolution
		if createsInput(op.Intent) && resolution.State == "finished" && resolution.Status != "not_sent" && unresolvedCodexHooks(current) {
			resolution.State = "unknown"
			resolution.Evidence.Complete = false
			resolution.Evidence.Detail = "原生 hook 尚无可核对的完整终态，原输入操作保持待核对"
		}
		if op.Intent.Kind == "recover_execution" && answer.err == nil && resolution.State == "finished" && resolution.Status == "succeeded" && resolution.Evidence.Complete {
			if err := a.finishVerifiedExecutionRecovery(next, &current, op, resolution.Receipt); err != nil {
				observationErr = err
				resolution.State = "unknown"
				resolution.Evidence.Complete = false
				resolution.Evidence.Detail = err.Error()
			} else if op.Intent.ClaudeInstalledRecovery != nil || op.Intent.ClaudeOriginalQueryRecovery != nil {
				current.View, current.Status, current.Connection = resolution.View, resolution.View.Status, "ready"
			}
		}
		if op.Intent.Kind == "apply_execution" && answer.err == nil && resolution.State == "finished" && resolution.Status == "succeeded" && resolution.Evidence.Complete {
			if err := a.promoteExecutionChange(&current, op); err != nil {
				observationErr = err
				resolution.State = "unknown"
				resolution.Evidence.Detail = err.Error()
			}
		}
		if op.Intent.Kind == "prepare_execution" && current.PendingExecutionChange != nil && current.PendingExecutionChange.OperationID == op.ID {
			current.PendingExecutionChange.Status = "pending"
		}
		if op.Intent.Kind == "apply_execution" && answer.err == nil && resolution.State == "finished" && resolution.Status == "not_sent" && resolution.Evidence.Complete && current.PendingExecutionChange != nil && current.PendingExecutionChange.OperationID == op.ID {
			current.PendingExecutionChange.Status = "pending"
		}
		if answer.err != nil {
			op.Status = "uncertain"
			op.Error = answer.err.Error()
		} else {
			op.Evidence = append(op.Evidence, resolution.Evidence)
			if len(op.Evidence) > 40 {
				op.Evidence = op.Evidence[len(op.Evidence)-40:]
			}
			switch {
			case resolution.State == "finished" && resolution.Evidence.Complete:
				op.Status = resolution.Status
				op.Error = ""
			case resolution.State == "running":
				op.Status = "running"
			default:
				op.Status = "uncertain"
			}
			if resolution.Receipt.NativeID != "" {
				op.Receipt = resolution.Receipt
			}
		}
		next.Operations[op.ID] = op
		if op.Status == "uncertain" {
			current.Status = "unknown"
		}
	}
	old := a.state.Sessions[session.ID]
	old.View.Evidence.At = current.View.Evidence.At
	left, _ := json.Marshal(old)
	right, _ := json.Marshal(current)
	if string(left) == string(right) && len(active) == 0 {
		return
	}
	current.UpdatedAt = now()
	next.Sessions[session.ID] = current
	_ = a.commit(next, "session.updated", session.ID)
}

func mergePending(previous []runtime.Pending, view runtime.View) []runtime.Pending {
	result := append([]runtime.Pending{}, view.Pending...)
	seen := map[string]bool{}
	for _, pending := range result {
		seen[pending.ID] = true
	}
	for _, pending := range previous {
		if seen[pending.ID] {
			continue
		}
		if pending.Status == "pending" || pending.Status == "submitted" || pending.Status == "unverified" {
			pending.Status = "unverified"
			if view.Evidence.Complete && view.ExecutionClear {
				pending.Status = "expired"
			}
		}
		result = append(result, pending)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (a *Application) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := clone(a.state)
	out := Snapshot{Sequence: state.Sequence, Sessions: []Session{}, Operations: []Operation{}, Projects: append([]Project{}, a.config.Projects...), Routes: append([]runtime.Route{}, a.config.Routes...)}
	out.Instances = queryCopy(a.instances)
	out.CapabilityAssets = a.capabilityAssets()
	out.RouteStatuses = a.routeStatuses()
	out.Projects = a.projects()
	for _, s := range state.Sessions {
		access := a.sessionAccess(s)
		s.Access = &access
		out.Sessions = append(out.Sessions, s)
	}
	for _, o := range state.Operations {
		out.Operations = append(out.Operations, o)
	}
	sort.Slice(out.Operations, func(i, j int) bool { return out.Operations[i].CreatedAt < out.Operations[j].CreatedAt })
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].UpdatedAt > out.Sessions[j].UpdatedAt })
	if a.fatal != nil {
		out.Error = a.fatal.Error()
	}
	return out
}

func (a *Application) Events(after uint64) ([]Event, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	gap := after > a.state.Sequence || len(a.state.Events) > 0 && after+1 < a.state.Events[0].Seq
	events := []Event{}
	for _, e := range a.state.Events {
		if e.Seq > after {
			events = append(events, e)
		}
	}
	return events, gap
}

func (a *Application) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	next := clone(a.state)
	for id, s := range next.Sessions {
		s.Paused = true
		next.Sessions[id] = s
	}
	err := a.commit(next, "console.stopping", "")
	a.cancel()
	a.mu.Unlock()
	a.wg.Wait()
	_ = a.runtime.Close()
	if closeErr := a.store.Close(); err == nil {
		err = closeErr
	}
	return err
}
