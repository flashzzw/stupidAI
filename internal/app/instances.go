package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"codex-netcap/internal/runtime"
)

type InstanceStatus struct {
	ID                string              `json:"id"`
	Name              string              `json:"name,omitempty"`
	Kind              string              `json:"kind"`
	Executable        string              `json:"executable"`
	Profile           string              `json:"profile"`
	SettingsFile      string              `json:"settings_file,omitempty"`
	Bridge            string              `json:"bridge,omitempty"`
	NodeExecutable    string              `json:"node_executable,omitempty"`
	ControlEndpoint   string              `json:"control_endpoint,omitempty"`
	ControlStatus     string              `json:"control_status"`
	ControlDetail     string              `json:"control_detail"`
	StateNamespace    string              `json:"state_namespace,omitempty"`
	SharedStateWith   []string            `json:"shared_state_with,omitempty"`
	ConfiguredVersion string              `json:"configured_version"`
	Description       runtime.Description `json:"description"`
	ConfigError       string              `json:"config_error,omitempty"`
	NativeReadError   string              `json:"native_read_error,omitempty"`
}

func describeInstances(ctx context.Context, config Config, adapter runtime.Adapter) []InstanceStatus {
	out := make([]InstanceStatus, 0, len(config.Instances))
	for _, instance := range config.Instances {
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		description, err := adapter.Describe(check, instance)
		cancel()
		if err != nil {
			description.Compatibility = "unavailable"
		}
		if description.Compatibility == "" {
			description.Compatibility = "unverified"
		}
		status := InstanceStatus{ID: instance.ID, Name: instance.Name, Kind: instance.Kind, Executable: instance.Executable,
			Profile: instance.Profile, SettingsFile: instance.SettingsFile, Bridge: instance.Bridge, NodeExecutable: instance.NodeExecutable,
			ConfiguredVersion: instance.Version, Description: description}
		status.StateNamespace, err = runtime.StateNamespace(runtime.Scope{Instance: instance})
		if err != nil {
			status.ConfigError = "原生状态目录无法解析，请检查 profile"
			status.NativeReadError = status.ConfigError
		}
		if _, err := profileFingerprint(instance); err != nil {
			status.ConfigError = err.Error()
		}
		if _, err := nativeReadFingerprint(instance); err != nil {
			status.NativeReadError = err.Error()
		}
		if instance.Kind == "claude" {
			status.ControlStatus, status.ControlDetail = "owned_bridge", "创建会话时使用此实例的 SDK bridge；诊断不启动会话"
			if info, err := os.Stat(instance.Bridge); err != nil || info.IsDir() {
				status.ConfigError = "Claude bridge 不存在，请先构建并检查 bridge 路径"
				status.NativeReadError = status.ConfigError
			}
			if instance.NodeExecutable != "" {
				if info, err := os.Stat(instance.NodeExecutable); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
					status.ConfigError = "Node 可执行文件不可用，请检查 node_executable"
					status.NativeReadError = status.ConfigError
				}
			}
		} else if instance.Kind == "codex" {
			status.ControlEndpoint = instance.Socket
			status.ControlStatus, status.ControlDetail = "unavailable", "控制 socket 不存在或不是 Unix socket"
			if info, err := os.Stat(instance.Socket); err == nil && info.Mode()&os.ModeSocket != 0 {
				status.ControlStatus, status.ControlDetail = "socket_present", "本地 socket 存在；原生进程身份与凭据在连接时核对"
			}
		}
		out = append(out, status)
	}
	markSharedInstances(out)
	return out
}

func markSharedInstances(out []InstanceStatus) {
	for n := range out {
		out[n].SharedStateWith = nil
		if out[n].StateNamespace == "" {
			continue
		}
		for m := range out {
			if n != m && out[n].Kind == out[m].Kind && out[n].StateNamespace == out[m].StateNamespace {
				out[n].SharedStateWith = append(out[n].SharedStateWith, out[m].ID)
			}
		}
	}
}

func (a *Application) requireCapability(instanceID, capability string) error {
	return a.requireCapabilityConfig(instanceID, capability, false)
}

func (a *Application) requireNativeHistory(instanceID string) error {
	return a.requireCapabilityConfig(instanceID, "history", true)
}

func (a *Application) requireCapabilityConfig(instanceID, capability string, nativeRead bool) error {
	for _, status := range a.instances {
		if status.ID != instanceID {
			continue
		}
		configError := status.ConfigError
		if nativeRead {
			configError = status.NativeReadError
		}
		if configError != "" {
			return fmt.Errorf("%s: %s", instanceID, configError)
		}
		if status.Description.Compatibility == "supported" && status.Description.Capabilities[capability] == "supported" {
			return nil
		}
		return fmt.Errorf("实例 %s 的 %s 能力未验证或不可用，请检查 CLI 版本；可查看执行详情或返回原终端", instanceID, capability)
	}
	return fmt.Errorf("实例已不在当前配置中，请返回原终端")
}
