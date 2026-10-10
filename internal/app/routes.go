package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
)

type RouteStatus struct {
	Route           runtime.Route `json:"route"`
	Identity        string        `json:"identity"`
	Fingerprint     string        `json:"config_fingerprint,omitempty"`
	Health          string        `json:"health"`
	Reason          string        `json:"reason"`
	ConfigError     string        `json:"config_error,omitempty"`
	Proof           string        `json:"proof,omitempty"`
	LastSuccessAt   string        `json:"last_success_at,omitempty"`
	ExpiresAt       string        `json:"expires_at,omitempty"`
	Probe           *ProbeRecord  `json:"probe,omitempty"`
	ProbeAvailable  bool          `json:"probe_available"`
	ProbeReason     string        `json:"probe_reason,omitempty"`
	NextAllowedAt   string        `json:"next_allowed_at,omitempty"`
	FailureKind     string        `json:"failure_kind,omitempty"`
	AdmissionReason string        `json:"admission_reason,omitempty"`
}

func (a *Application) projects() []Project {
	projects := append([]Project{}, a.config.Projects...)
	if len(a.state.Probes) > 0 {
		projects = append(projects, Project{ID: "_probes", Name: "路由探测"})
	}
	return projects
}

func (a *Application) routeStatuses() []RouteStatus {
	statuses := inspectRoutes(a.config)
	for n := range statuses {
		status := &statuses[n]
		instance, _ := configuredInstance(a.config, status.Route.InstanceID)
		if a.requireCapability(instance.ID, "route_evidence") != nil {
			status.Reason = "工作会话的完整路由证据尚未验证，不能用普通回复刷新健康"
		}
		if err := a.requireCapability(instance.ID, "probe"); err != nil {
			status.ProbeReason = err.Error()
		} else if err := validateProbeConfig(instance, status.Route, "utc-date"); err != nil {
			status.ProbeReason = err.Error()
		} else {
			status.ProbeAvailable = true
		}
		key, err := healthKey(instance, status.Route)
		if err != nil {
			continue
		}
		health, ok := a.state.RouteHealth[key]
		if ok {
			status.NextAllowedAt, status.FailureKind = health.NextAllowedAt, health.FailureKind
			status.Health, status.Proof, status.LastSuccessAt, status.ExpiresAt = health.State, health.Proof, health.LastSuccessAt, health.ExpiresAt
			status.Reason = health.Error
			if health.State == "healthy" {
				status.Reason = "已验证完整模型调用"
				expires, _ := time.Parse(time.RFC3339Nano, health.ExpiresAt)
				if !time.Now().Before(expires) {
					status.Health = "stale"
					status.Reason = "最近成功已超过健康 TTL"
				}
			}
		}
		if err := a.admitRouteHealth(key); err != nil {
			status.AdmissionReason = err.Error()
		}
		for _, probe := range a.state.Probes {
			if probe.HealthKey == key && (status.Probe == nil || probe.StartedAt > status.Probe.StartedAt) {
				copy := probe
				status.Probe = &copy
			}
		}
	}
	return statuses
}

func digest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// A route name is a display reference. Health evidence is scoped to its full
// configuration identity, including the credential reference and revisions.
func routeIdentity(route runtime.Route) string { return digest(route) }

func configuredInstance(config Config, id string) (runtime.Instance, bool) {
	for _, instance := range config.Instances {
		if instance.ID == id {
			return instance, true
		}
	}
	return runtime.Instance{}, false
}

func configuredRoute(config Config, id string) (runtime.Route, bool) {
	for _, route := range config.Routes {
		if route.ID == id {
			return route, true
		}
	}
	return runtime.Route{}, false
}

func routeFingerprint(instance runtime.Instance, route runtime.Route) (string, error) {
	profile, err := profileFingerprint(instance)
	if err != nil {
		return "", err
	}
	return routeFingerprintWithProfile(instance, route, profile)
}

func routeFingerprintWithProfile(instance runtime.Instance, route runtime.Route, profile string) (string, error) {
	parts := []any{instance, route, profile}
	stamp, err := runtime.CredentialStamp(route)
	if err != nil {
		return "", err
	}
	if stamp != "" {
		parts = append(parts, stamp)
	}
	return digest(parts), nil
}

func inspectRoutes(config Config) []RouteStatus {
	out := make([]RouteStatus, 0, len(config.Routes))
	for _, route := range config.Routes {
		status := RouteStatus{Route: route, Identity: routeIdentity(route), Health: "unknown", Reason: "尚无此配置身份上的完整模型调用证据"}
		instance, ok := configuredInstance(config, route.InstanceID)
		if !ok {
			status.ConfigError = "实例未登记"
		} else {
			fingerprint, err := routeFingerprint(instance, route)
			if err != nil {
				status.ConfigError = "实例 profile 无法读取"
			} else {
				status.Fingerprint = fingerprint
			}
		}
		if name, ok := strings.CutPrefix(route.CredentialRef, "env:"); ok {
			if name == "" || os.Getenv(name) == "" {
				status.ConfigError = "路由凭据环境变量尚未设置"
			}
		} else if instance.Kind != "codex" || !strings.HasPrefix(route.CredentialRef, "profile:") {
			status.ConfigError = "此运行时尚不支持该凭据引用方式"
		}
		out = append(out, status)
	}
	return out
}

func (a *Application) verifyRouteBinding(session Session) error {
	if err := verifyExecutionSources(session); err != nil {
		return err
	}
	return a.verifyRouteIdentity(session)
}

func (a *Application) verifyRouteIdentity(session Session) error {
	if err := a.verifyRouteRegistry(session); err != nil {
		return err
	}
	fingerprint, err := routeFingerprint(session.Scope.Instance, session.Scope.Route)
	if err != nil || fingerprint != session.RouteFingerprint {
		return fmt.Errorf("实例配置指纹已变化，原会话不能继续指定路由发送")
	}
	return nil
}

func (a *Application) verifyRouteRegistry(session Session) error {
	if err := runtime.VerifyStateNamespace(session.Scope); err != nil {
		return err
	}
	if err := runtime.VerifyCredentialStamp(session.Scope); err != nil {
		return err
	}
	route, ok := configuredRoute(a.config, session.Scope.Route.ID)
	if !ok || route != session.Scope.Route {
		return fmt.Errorf("会话绑定的路由配置已变化，请使用新 revision 创建新会话")
	}
	instance, ok := configuredInstance(a.config, route.InstanceID)
	if !ok || digest(instance) != digest(session.Scope.Instance) {
		return fmt.Errorf("会话的实例或配置策略已变化，请核对后创建新会话")
	}
	return nil
}

// Doctor reads configuration and CLI versions without opening the state store,
// contacting a native server or sending a model request.
func Doctor(ctx context.Context, config Config) (any, error) {
	adapters := runtime.NewAdapters(config.StateDir)
	defer adapters.Close()
	return struct {
		ReadOnly  bool             `json:"read_only"`
		Instances []InstanceStatus `json:"instances"`
		Routes    []RouteStatus    `json:"routes"`
	}{true, describeInstances(ctx, config, adapters), inspectRoutes(config)}, nil
}
