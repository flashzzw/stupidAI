package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type Adapters struct {
	byKind       map[string]Adapter
	stateDir     string
	probeMu      sync.Mutex
	probeServers map[string]*codexProbeServer
}

func NewAdapters(stateDir string) *Adapters {
	return &Adapters{stateDir: stateDir, probeServers: map[string]*codexProbeServer{}, byKind: map[string]Adapter{"codex": NewCodex(), "claude": NewClaude(stateDir), "opencode": NewOpenCode(stateDir)}}
}

func (a *Adapters) scope(ctx context.Context, scope Scope, create bool) (Scope, error) {
	if err := VerifyStateNamespace(scope); err != nil {
		return scope, err
	}
	if scope.Purpose != "probe" {
		return scope, nil
	}
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	resolved, err := probeScope(ctx, a.stateDir, scope, create)
	if err != nil {
		return scope, err
	}
	if resolved.Instance.Kind == "codex" {
		server := a.probeServers[scope.SessionID]
		if server == nil || !server.alive() {
			if err := VerifyCredentialStamp(scope); err != nil {
				return scope, err
			}
			server, err = startCodexProbeServer(ctx, resolved)
			if err != nil {
				return scope, err
			}
			a.probeServers[scope.SessionID] = server
		}
	}
	return resolved, nil
}

func (a *Adapters) adapter(kind string) (Adapter, error) {
	r, ok := a.byKind[kind]
	if !ok {
		return nil, fmt.Errorf("不支持的运行时 %q", kind)
	}
	return r, nil
}

func (a *Adapters) Describe(ctx context.Context, i Instance) (Description, error) {
	r, err := a.adapter(i.Kind)
	if err != nil {
		return Description{}, err
	}
	return r.Describe(ctx, i)
}

func (a *Adapters) DiscoverSkills(ctx context.Context, scope Scope) (SkillInventory, error) {
	// Discovery must never start an isolated probe or resume a thread.
	if scope.Purpose == "probe" {
		return SkillInventory{}, fmt.Errorf("探测会话不支持 Skill 来源发现")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return SkillInventory{}, err
	}
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return SkillInventory{}, err
	}
	discoverer, ok := r.(SkillDiscoverer)
	if !ok {
		return SkillInventory{}, fmt.Errorf("此后端尚未接通 Skill 来源发现")
	}
	return discoverer.DiscoverSkills(ctx, scope)
}
func (a *Adapters) Execute(ctx context.Context, in Intent) (Receipt, error) {
	if in.Kind == "create" || in.Kind == "send" || in.Kind == "respond" || in.Kind == "apply_execution" {
		if err := VerifyCredentialStamp(in.Scope); err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
	}
	resolved, err := a.scope(ctx, in.Scope, in.Kind == "create")
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	in.Scope = resolved
	r, err := a.adapter(in.Scope.Instance.Kind)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	return r.Execute(ctx, in)
}
func (a *Adapters) Observe(ctx context.Context, s Scope) (View, error) {
	resolved, err := a.scope(ctx, s, false)
	if err != nil {
		return View{}, err
	}
	s = resolved
	r, err := a.adapter(s.Instance.Kind)
	if err != nil {
		return View{}, err
	}
	return r.Observe(ctx, s)
}
func (a *Adapters) ObserveFresh(ctx context.Context, s Scope) (View, error) {
	resolved, err := a.scope(ctx, s, false)
	if err != nil {
		return View{}, err
	}
	r, err := a.adapter(resolved.Instance.Kind)
	if err != nil {
		return View{}, err
	}
	return r.ObserveFresh(ctx, resolved)
}
func (a *Adapters) Reconcile(ctx context.Context, in Intent, receipt Receipt) (Resolution, error) {
	resolved, err := a.scope(ctx, in.Scope, false)
	if err != nil {
		return Resolution{State: "unknown"}, err
	}
	in.Scope = resolved
	r, err := a.adapter(in.Scope.Instance.Kind)
	if err != nil {
		return Resolution{State: "unknown"}, err
	}
	return r.Reconcile(ctx, in, receipt)
}
func (a *Adapters) Close() error {
	var failures []error
	for _, r := range a.byKind {
		failures = append(failures, r.Close())
	}
	a.probeMu.Lock()
	for _, server := range a.probeServers {
		server.stop()
	}
	a.probeMu.Unlock()
	return errors.Join(failures...)
}
