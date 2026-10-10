package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"codex-netcap/internal/runtime"
)

// One registration joins existing Instance and Route contracts. Definitions are
// additive and immutable; changing an active identity requires a new ID/revision.
type ExecutionProfile struct {
	Instance runtime.Instance `json:"instance"`
	Route    runtime.Route    `json:"route"`
}

type ExecutionProfilePreview struct {
	Profile         ExecutionProfile `json:"profile"`
	Instance        InstanceStatus   `json:"instance_status"`
	Route           RouteStatus      `json:"route_status"`
	ControlEndpoint string           `json:"control_endpoint"`
	ControlStatus   string           `json:"control_status"`
	ControlDetail   string           `json:"control_detail"`
}

func appendExecutionProfile(c Config, profile ExecutionProfile) (Config, error) {
	if profile.Instance.Kind != "codex" && profile.Instance.Kind != "claude" {
		return c, fmt.Errorf("执行配置登记仅支持 Codex/Claude")
	}
	if profile.Route.InstanceID != profile.Instance.ID {
		return c, fmt.Errorf("登记路由必须引用同一份执行实例")
	}
	if _, ok := configuredInstance(c, profile.Instance.ID); ok {
		return c, fmt.Errorf("执行实例 ID 已登记；修改配置请使用新 ID 与 revision")
	}
	if _, ok := configuredRoute(c, profile.Route.ID); ok {
		return c, fmt.Errorf("路由 ID 已登记")
	}
	if len(c.Instances) >= 200 {
		return c, fmt.Errorf("执行实例登记上限为 200")
	}
	c = queryCopy(c)
	c.Instances = append(c.Instances, profile.Instance)
	c.Routes = append(c.Routes, profile.Route)
	return c, nil
}

func mergeExecutionProfiles(c Config, saved map[string]ExecutionProfile) (Config, error) {
	ids := make([]string, 0, len(saved))
	for id := range saved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := saved[id]
		// A previously registered definition may later be added to the startup
		// file verbatim. Conflicts fail explicitly instead of rebinding sessions.
		i, hasI := configuredInstance(c, p.Instance.ID)
		r, hasR := configuredRoute(c, p.Route.ID)
		if hasI || hasR {
			if !hasI || !hasR || digest(i) != digest(p.Instance) || r != p.Route {
				return c, fmt.Errorf("已保存执行配置 %s 与启动配置冲突", id)
			}
			continue
		}
		var err error
		c, err = appendExecutionProfile(c, p)
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func normalizedRegistration(c Config, p ExecutionProfile) (Config, ExecutionProfile, error) {
	for _, path := range []string{p.Instance.Executable, p.Instance.Profile, p.Instance.Socket, p.Instance.SettingsFile, p.Instance.Bridge, p.Instance.NodeExecutable, p.Instance.ServerExecutable} {
		if path != "" && !filepath.IsAbs(path) {
			return c, p, fmt.Errorf("登记路径必须是绝对路径，避免控制台工作目录改变来源")
		}
	}
	// Validate only the new definition; unrelated project aliases stay bound.
	one, err := normalizeConfig(Config{Version: 1, StateDir: c.StateDir, Instances: []runtime.Instance{p.Instance}, Routes: []runtime.Route{p.Route}}, "/")
	if err != nil {
		return c, p, err
	}
	p.Instance = one.Instances[0]
	p.Route = one.Routes[0]
	c, err = appendExecutionProfile(c, p)
	return c, p, err
}

func (a *Application) previewProfile(ctx context.Context, c Config, p ExecutionProfile) (ExecutionProfilePreview, error) {
	c, p, err := normalizedRegistration(c, p)
	if err != nil {
		return ExecutionProfilePreview{}, err
	}
	status := describeInstances(ctx, Config{Instances: []runtime.Instance{p.Instance}}, a.runtime)[0]
	for _, i := range c.Instances {
		if i.ID == p.Instance.ID || i.Kind != p.Instance.Kind {
			continue
		}
		ns, e := runtime.StateNamespace(runtime.Scope{Instance: i})
		if e == nil && ns != "" && ns == status.StateNamespace {
			status.SharedStateWith = append(status.SharedStateWith, i.ID)
		}
	}
	out := ExecutionProfilePreview{Profile: p, Instance: status, Route: inspectRoutes(Config{Instances: []runtime.Instance{p.Instance}, Routes: []runtime.Route{p.Route}})[0], ControlEndpoint: status.ControlEndpoint, ControlStatus: status.ControlStatus, ControlDetail: status.ControlDetail}
	return out, nil
}

func (a *Application) PreviewExecutionProfile(ctx context.Context, p ExecutionProfile) (ExecutionProfilePreview, error) {
	a.mu.Lock()
	c := queryCopy(a.config)
	a.mu.Unlock()
	return a.previewProfile(ctx, c, p)
}

func (a *Application) registerExecutionProfile(command Command) (CommandReceipt, error) {
	receipt := CommandReceipt{ID: command.ID, Hash: digest(command), Status: "registered"}
	allowed := Command{ID: command.ID, Kind: "register_execution", ExecutionProfile: command.ExecutionProfile}
	if !validID(command.ID) || command.ExecutionProfile == nil || digest(allowed) != receipt.Hash {
		return receipt, fmt.Errorf("登记命令仅接受 command_id、kind 与 execution_profile")
	}
	a.mu.Lock()
	if old, ok := a.state.Commands[command.ID]; ok {
		a.mu.Unlock()
		if old.Hash != receipt.Hash {
			return receipt, fmt.Errorf("command_id 已用于其他内容")
		}
		return old, nil
	}
	c := queryCopy(a.config)
	a.mu.Unlock()
	preview, err := a.previewProfile(a.ctx, c, *command.ExecutionProfile)
	if err != nil {
		// A concurrent retry may have committed while this request diagnosed.
		a.mu.Lock()
		old, ok := a.state.Commands[command.ID]
		a.mu.Unlock()
		if ok && old.Hash == receipt.Hash {
			return old, nil
		}
		return receipt, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return receipt, fmt.Errorf("控制台正在关闭")
	}
	if a.fatal != nil {
		return receipt, a.fatal
	}
	if old, ok := a.state.Commands[command.ID]; ok {
		if old.Hash != receipt.Hash {
			return receipt, fmt.Errorf("command_id 已用于其他内容")
		}
		return old, nil
	}
	c, p, err := normalizedRegistration(a.config, preview.Profile)
	if err != nil {
		return receipt, err
	}
	next := clone(a.state)
	if next.ExecutionProfiles == nil {
		next.ExecutionProfiles = map[string]ExecutionProfile{}
	}
	next.ExecutionProfiles[p.Instance.ID] = p
	receipt.ExecutionProfileID = p.Instance.ID
	next.Commands[command.ID] = receipt
	if err := a.commit(next, "execution.registered", ""); err != nil {
		return CommandReceipt{}, err
	}
	a.config = c
	a.instances = append(a.instances, preview.Instance)
	markSharedInstances(a.instances)
	return receipt, nil
}

// Refresh only diagnosis, without touching definitions or running sessions.
func (a *Application) DiagnoseExecutionProfiles(ctx context.Context) ([]InstanceStatus, error) {
	a.mu.Lock()
	c := queryCopy(a.config)
	a.mu.Unlock()
	statuses := describeInstances(ctx, c, a.runtime)
	a.mu.Lock()
	defer a.mu.Unlock()
	if digest(c) != digest(a.config) {
		return nil, fmt.Errorf("诊断期间登记已变化，请重试")
	}
	if a.closed {
		return nil, fmt.Errorf("控制台正在关闭")
	}
	if a.fatal != nil {
		return nil, a.fatal
	}
	if err := a.commit(clone(a.state), "execution.diagnosed", ""); err != nil {
		return nil, err
	}
	a.instances = statuses
	return queryCopy(statuses), nil
}
