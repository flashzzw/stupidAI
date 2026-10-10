package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func probeEnv(instance Instance) []string {
	env := []string{}
	for _, key := range []string{"PATH", "USER", "LANG", "TERM", "SHELL"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, "HOME="+instance.isolatedHome, "CODEX_HOME="+instance.Profile, "CLAUDE_CONFIG_DIR="+instance.Profile,
		"XDG_CONFIG_HOME="+filepath.Join(instance.isolatedHome, "config"), "XDG_DATA_HOME="+filepath.Join(instance.isolatedHome, "data"), "XDG_CACHE_HOME="+filepath.Join(instance.isolatedHome, "cache"), "XDG_STATE_HOME="+filepath.Join(instance.isolatedHome, "state"),
		"GIT_CONFIG_NOSYSTEM=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1")
	return env
}

// probeScope is an execution policy of the selected route. It does not
// substitute another registered account or reuse a working session's state.
func probeScope(ctx context.Context, stateDir string, scope Scope, create bool) (Scope, error) {
	if scope.Purpose != "probe" {
		return scope, nil
	}
	if scope.SessionID == "" || strings.ContainsAny(scope.SessionID, `/\.`) {
		return scope, fmt.Errorf("探测缺少自有会话身份")
	}
	key, ok := strings.CutPrefix(scope.Route.CredentialRef, "env:")
	if !ok || key == "" || os.Getenv(key) == "" {
		return scope, fmt.Errorf("隔离探测目前需要已设置的 env: 凭据引用")
	}
	root := filepath.Join(stateDir, "probes", scope.SessionID)
	ownerPath := filepath.Join(root, "owner.json")
	owner, _ := json.Marshal(struct{ Session, Instance, Route string }{scope.SessionID, scope.Instance.ID, scope.Route.ID})
	if original, err := os.ReadFile(ownerPath); err == nil {
		if string(original) != string(owner) {
			return scope, fmt.Errorf("探测目录所有权不匹配")
		}
	} else if create && os.IsNotExist(err) {
		if err := os.MkdirAll(root, 0700); err != nil {
			return scope, err
		}
		if err := os.WriteFile(ownerPath, owner, 0600); err != nil {
			return scope, err
		}
	} else {
		return scope, fmt.Errorf("隔离探测目录不可核对")
	}
	original := scope.Instance
	scope.Instance.ID = original.ID + "-probe-" + scope.SessionID
	scope.Instance.Profile = filepath.Join(root, "profile")
	scope.Instance.SettingsFile = ""
	scope.Instance.isolatedHome = filepath.Join(root, "home")
	scope.Instance.EnvAllow = nil
	scope.Instance.Socket = filepath.Join(root, "server.sock")
	scope.Cwd = filepath.Join(root, "work")
	if create {
		for _, dir := range []string{scope.Instance.Profile, scope.Instance.isolatedHome, scope.Cwd} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return scope, err
			}
		}
	}
	// A real repository root prevents parent project rules from being loaded
	// when the controller's own state directory is nested in another project.
	if _, err := os.Stat(filepath.Join(scope.Cwd, ".git")); create && os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "-c", "init.templateDir=", "init", "--quiet", scope.Cwd)
		cmd.Env = probeEnv(scope.Instance)
		if err := cmd.Run(); err != nil {
			return scope, fmt.Errorf("不能建立隔离探测工作目录: %w", err)
		}
	}
	var name string
	var config []byte
	switch original.Kind {
	case "codex":
		if original.ServerExecutable == "" {
			return scope, fmt.Errorf("Codex 隔离探测需要显式 server_executable（已验证 0.158.0）")
		}
		name = "config.toml"
		config = []byte(fmt.Sprintf("model = %s\nmodel_provider = %s\napproval_policy = 'never'\nsandbox_mode = 'read-only'\n[model_providers.%s]\nname = 'Probe route'\nbase_url = %s\nenv_key = %s\nwire_api = 'responses'\nrequires_openai_auth = false\nsupports_websockets = false\nrequest_max_retries = 0\nstream_max_retries = 0\n[features]\napps = false\nplugins = false\nshell_snapshot = false\n", strconv.Quote(scope.Route.Model), strconv.Quote(scope.Route.Provider), strconv.Quote(scope.Route.Provider), strconv.Quote(scope.Route.Endpoint), strconv.Quote(key)))
	case "claude":
		name, config = "settings.json", []byte(`{"disableAllHooks":true}`)
	case "opencode":
		var source map[string]any
		b, err := os.ReadFile(filepath.Join(original.Profile, "opencode.json"))
		if err != nil || json.Unmarshal(b, &source) != nil {
			return scope, fmt.Errorf("OpenCode 探测 transport 配置无法读取")
		}
		providers, _ := source["provider"].(map[string]any)
		provider, _ := providers[scope.Route.Provider].(map[string]any)
		models, _ := provider["models"].(map[string]any)
		model, ok := models[scope.Route.Model]
		if !ok {
			return scope, fmt.Errorf("OpenCode 探测需要显式模型定义")
		}
		minimal := map[string]any{"models": map[string]any{scope.Route.Model: model}}
		for _, key := range []string{"npm", "name"} {
			if value, ok := provider[key]; ok {
				minimal[key] = value
			}
		}
		config, _ = json.Marshal(map[string]any{"plugin": []string{}, "mcp": map[string]any{}, "instructions": []string{}, "provider": map[string]any{scope.Route.Provider: minimal}, "permission": map[string]any{"*": "deny", "bash": map[string]string{"*": "deny", DateCommand: "allow"}}})
		name = "opencode.json"
	default:
		return scope, fmt.Errorf("未知探测运行时")
	}
	path := filepath.Join(scope.Instance.Profile, name)
	if before, err := os.ReadFile(path); err == nil {
		if string(before) != string(config) {
			return scope, fmt.Errorf("隔离探测配置已变化")
		}
	} else if create && os.IsNotExist(err) {
		if err := os.WriteFile(path, config, 0600); err != nil {
			return scope, err
		}
	} else {
		return scope, err
	}
	return scope, nil
}
