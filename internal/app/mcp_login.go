package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"codex-netcap/internal/config"
	"codex-netcap/internal/runtime"
)

type MCPLoginRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Server     string `json:"server"`
}

// A durable preparation receipt contains only bindings and local paths. Native
// OAuth owns the browser interaction, callback, token storage and refresh.
type MCPLoginPlan struct {
	SnapshotID string `json:"snapshot_id"`
	Server     string `json:"server"`
	AssetID    string `json:"asset_id"`
	Version    string `json:"version"`
	Profile    string `json:"profile"`
	Directory  string `json:"directory"`
	Command    string `json:"command"`
	CreatedAt  string `json:"created_at"`
}

func (a *Application) prepareMCPLogin(command Command, hash string) (CommandReceipt, error) {
	receipt := CommandReceipt{ID: command.ID, Hash: hash, SessionID: command.SessionID, Status: "prepared"}
	allowed := Command{ID: command.ID, Kind: command.Kind, SessionID: command.SessionID, MCPLogin: command.MCPLogin}
	if command.MCPLogin == nil || digest(command) != digest(allowed) {
		return receipt, fmt.Errorf("原生认证准备只接受会话、快照和 MCP 服务名")
	}
	session, ok := a.state.Sessions[command.SessionID]
	if !ok || session.Origin != "console" || session.ControlMode == "observed" || session.Scope.Instance.Kind != "claude" || session.ExecutionSnapshot == nil {
		return receipt, fmt.Errorf("原生认证准备需要已有执行快照的普通 Claude 会话")
	}
	if err := a.verifyRouteBinding(session); err != nil {
		return receipt, err
	}
	snapshot := session.ExecutionSnapshot
	if command.MCPLogin.SnapshotID != snapshot.ID && session.PendingExecutionChange != nil {
		snapshot = session.PendingExecutionChange.Snapshot
	}
	if snapshot == nil || command.MCPLogin.SnapshotID != snapshot.ID {
		return receipt, fmt.Errorf("认证目标快照已变化，请重新选择")
	}
	for _, check := range checkExecutionSources(snapshot) {
		if check.Status != "unchanged" {
			return receipt, fmt.Errorf("认证来源待核对：%s", check.Name)
		}
	}
	index := slices.IndexFunc(snapshot.MCPSelections, func(s runtime.MCPSelection) bool { return s.Server == command.MCPLogin.Server })
	if index < 0 {
		return receipt, fmt.Errorf("MCP 服务不属于所选快照")
	}
	selection := snapshot.MCPSelections[index]
	refIndex := slices.IndexFunc(snapshot.CapabilityRefs, func(r CapabilityReference) bool { return r.Asset.ID == selection.AssetID })
	if refIndex < 0 {
		return receipt, fmt.Errorf("MCP 缺少已固定的资源引用")
	}
	ref := snapshot.CapabilityRefs[refIndex]
	bindings, err := claudeMCPSelections(ref, selection.Mode)
	if err != nil {
		return receipt, err
	}
	if !slices.Contains(bindings, selection) {
		return receipt, fmt.Errorf("MCP 来源与快照绑定不一致")
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return receipt, err
	}
	if stamp != ref.Source {
		return receipt, fmt.Errorf("认证来源已变化")
	}
	definition, err := jsonObject(tree.files()[filepath.Base(selection.SourcePath)])
	if err != nil {
		return receipt, err
	}
	servers, _ := definition["mcpServers"].(map[string]any)
	server, _ := servers[selection.Server].(map[string]any)
	if server["type"] != "http" && server["type"] != "sse" {
		return receipt, fmt.Errorf("原生 OAuth 认证准备仅适用于 HTTP/SSE MCP")
	}
	if _, exists := server["headersHelper"]; exists {
		// A project-scoped login definition also changes native credential
		// scrubbing. Merely wrapping the helper with cd cannot preserve binding.
		return receipt, fmt.Errorf("此 MCP 使用动态认证命令；独立 OAuth 登录目录会改变 helper 的工作目录与原生凭据环境规则，当前无法保持原绑定，请在原执行配置中处理认证")
	}
	// Pin exactly one registered definition. Approval in this private project
	// covers only that definition; native user/managed restrictions still apply.
	copy, err := json.Marshal(map[string]any{"mcpServers": map[string]any{selection.Server: server}})
	if err != nil {
		return receipt, err
	}
	for _, check := range checkExecutionSources(snapshot) {
		if check.Status != "unchanged" {
			return receipt, fmt.Errorf("准备期间 MCP 来源发生变化")
		}
	}
	root := filepath.Join(a.config.StateDir, "mcp-login")
	if err := os.MkdirAll(root, 0700); err != nil {
		return receipt, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return receipt, fmt.Errorf("认证准备目录无效")
	}
	dir, err := os.MkdirTemp(root, "login-")
	if err != nil {
		return receipt, err
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0700); err != nil {
		return receipt, err
	}
	write := func(path string, data []byte) error {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if err := write(filepath.Join(dir, ".mcp.json"), copy); err != nil {
		return receipt, err
	}
	if err := write(filepath.Join(dir, ".claude", "settings.local.json"), []byte(`{"enableAllProjectMcpServers":true}`)); err != nil {
		return receipt, err
	}
	for _, path := range []string{filepath.Join(dir, ".claude"), dir, root, a.config.StateDir} {
		f, err := os.Open(path)
		if err != nil {
			return receipt, err
		}
		err = f.Sync()
		_ = f.Close()
		if err != nil {
			return receipt, err
		}
	}
	plan := MCPLoginPlan{SnapshotID: snapshot.ID, Server: selection.Server, AssetID: ref.Asset.ID, Version: ref.Asset.Version, Profile: snapshot.Instance.Profile, Directory: dir, CreatedAt: now()}
	plan.Command = "cd -- " + config.ShellQuote(dir) + " && CLAUDE_CONFIG_DIR=" + config.ShellQuote(snapshot.Instance.Profile) + " " + config.ShellQuote(snapshot.Instance.Executable)
	if snapshot.Instance.SettingsFile != "" {
		plan.Command += " --settings " + config.ShellQuote(snapshot.Instance.SettingsFile)
	}
	plan.Command += " mcp login " + config.ShellQuote(selection.Server) + " --no-browser"
	receipt.MCPLogin = &plan
	next := clone(a.state)
	next.Commands[command.ID] = receipt
	if err := a.commit(next, "mcp.login_prepared", session.ID); err != nil {
		return CommandReceipt{}, err
	}
	saved = true
	return queryCopy(receipt), nil
}
