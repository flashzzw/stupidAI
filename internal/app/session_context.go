package app

import "codex-netcap/internal/runtime"

// SessionContext is a display projection. Recorded binding, current registry
// definition and sampled backend evidence have separate origins and lifetimes.
type SessionContext struct {
	Basis          string                    `json:"basis"`
	Binding        AccessBinding             `json:"binding"`
	RegistryStatus string                    `json:"registry_status"`
	RegistryDetail string                    `json:"registry_detail"`
	Executor       *runtime.ExecutorEvidence `json:"executor,omitempty"`
}

type AccessBinding struct {
	InstanceID      string `json:"instance_id"`
	Name            string `json:"name,omitempty"`
	Kind            string `json:"kind"`
	Executable      string `json:"executable,omitempty"`
	Profile         string `json:"profile,omitempty"`
	SettingsFile    string `json:"settings_file,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Bridge          string `json:"bridge,omitempty"`
	DeclaredVersion string `json:"declared_version,omitempty"`
	StateNamespace  string `json:"state_namespace,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
	NativeID        string `json:"native_id,omitempty"`
	Permission      string `json:"permission"`
}

func (a *Application) sessionContext(session Session) SessionContext {
	i := session.Scope.Instance
	out := SessionContext{Basis: "saved_binding", RegistryStatus: "missing", RegistryDetail: "当前登记中没有此执行配置；保留记录中的绑定，不自动改用其他实例。",
		Binding: AccessBinding{InstanceID: i.ID, Name: i.Name, Kind: i.Kind, Executable: i.Executable, Profile: i.Profile, SettingsFile: i.SettingsFile, Endpoint: i.Socket, Bridge: i.Bridge, DeclaredVersion: i.Version, StateNamespace: session.Scope.StateNamespace, Cwd: session.Scope.Cwd, NativeID: session.Scope.NativeID, Permission: session.Permission}}
	if session.ControlMode != "observed" && (session.Origin == "console" || session.Origin == "probe" || session.Origin == "native") {
		out.Executor = session.View.Executor
	} else {
		out.Basis = "recorded_context"
		out.Binding.Permission = "unverified"
	}
	if current, ok := configuredInstance(a.config, i.ID); ok {
		// Renaming a registry entry does not replace its execution binding.
		current.Name, i.Name = "", ""
		out.RegistryStatus = "matching"
		out.RegistryDetail = "登记定义与记录相同；没有在此读取文件、连接后台或验证原配置实际生效。"
		if digest(current) != digest(i) {
			out.RegistryStatus = "changed"
			out.RegistryDetail = "当前登记定义与记录不同；下方保留原绑定，操作仍须独立核对。"
		}
	}
	if session.Origin == "probe" {
		out.RegistryStatus = "isolated"
		out.RegistryDetail = "探测使用派生的隔离目录；不把它视为普通会话的当前配置。"
	}
	return out
}
