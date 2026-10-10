package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"codex-netcap/internal/codex"
)

// Only the transport bound to the selected local daemon can attest its
// environment. A config/read response is disk configuration, not thread state.
type codexCredentialVerifier interface {
	VerifyCredential(context.Context, Instance, string, string) error
}

func verifyCodexThreadRoute(ctx context.Context, p codexConnection, scope Scope) error {
	if scope.CredentialStamp == "" && codexRecordedEffort(scope) == "" {
		return nil
	}
	thread, err := codex.Read(ctx, p, scope.NativeID)
	if err != nil {
		return fmt.Errorf("发送前无法核对 Codex 原生会话路由")
	}
	if thread.ID != scope.NativeID || thread.Model != scope.Route.Model || thread.Provider != scope.Route.Provider || filepath.Clean(thread.Cwd) != filepath.Clean(scope.Cwd) {
		return fmt.Errorf("Codex 原生会话的目录、模型或供应商已变化，请核对后创建新会话")
	}
	return verifyCodexEffort(scope, thread)
}

var codexProviderKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func codexRouteConfig(ctx context.Context, p codexConnection, scope Scope) (map[string]any, error) {
	if err := verifyCodexConfigurationSources(ctx, p, scope); err != nil {
		return nil, err
	}
	if err := VerifyCodexUserHooks(scope); err != nil {
		return nil, err
	}
	if err := verifyCodexProjectHooks(ctx, p, scope); err != nil {
		return nil, err
	}
	overrides, err := codexProviderRouteConfig(ctx, p, scope)
	if err != nil {
		return nil, err
	}
	if err = addCodexMCPOverrides(ctx, p, scope, overrides); err != nil {
		return nil, err
	}
	if err = addCodexPluginOverrides(ctx, p, scope, overrides); err != nil {
		return nil, err
	}
	if len(scope.SkillSelections) > 0 && !scope.directorySkillChange {
		if err = addCodexDirectorySkillOverrides(ctx, p, scope, overrides); err != nil {
			return nil, err
		}
	}
	if effort := codexRecordedEffort(scope); effort != "" {
		overrides["model_reasoning_effort"] = effort
	}
	return overrides, nil
}

func codexProviderRouteConfig(ctx context.Context, p codexConnection, scope Scope) (map[string]any, error) {
	// Restrict the dotted override key to one provider component. Other names
	// need a verified quoting contract before they can be used for writes.
	if !codexProviderKey.MatchString(scope.Route.Provider) {
		return nil, fmt.Errorf("Codex provider 标识不能安全映射到已验证的配置覆盖路径")
	}
	prefix := "model_providers." + scope.Route.Provider + "."
	overrides := map[string]any{prefix + "base_url": scope.Route.Endpoint}
	key, envRef := strings.CutPrefix(scope.Route.CredentialRef, "env:")
	if !envRef {
		if scope.Route.CredentialRef == "" || strings.HasPrefix(scope.Route.CredentialRef, "profile:") {
			return overrides, nil
		}
		return nil, fmt.Errorf("Codex 凭据引用方式不受支持")
	}
	value := os.Getenv(key)
	if key == "" || value == "" {
		return nil, fmt.Errorf("指定 Codex 路由的凭据环境变量未设置")
	}
	if scope.CredentialStamp == "" || scope.CredentialStamp != credentialStamp(scope.Route.CredentialRef, value) {
		return nil, fmt.Errorf("会话凭据快照缺失或已变化，请更新 revision 后创建新会话")
	}
	var read struct {
		Config struct {
			Providers map[string]json.RawMessage `json:"model_providers"`
		} `json:"config"`
	}
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": false}, &read); err != nil {
		return nil, fmt.Errorf("无法核对 Codex 路由配置来源")
	}
	if err := verifyCodexProviderAuth(read.Config.Providers[scope.Route.Provider]); err != nil {
		return nil, err
	}
	verifier, ok := p.(codexCredentialVerifier)
	if !ok {
		return nil, fmt.Errorf("Codex 连接没有可核对的后台凭据来源")
	}
	if err := verifier.VerifyCredential(ctx, scope.Instance, key, value); err != nil {
		return nil, err
	}
	overrides[prefix+"env_key"] = key
	overrides[prefix+"requires_openai_auth"] = false
	return overrides, nil
}

func verifyCodexProviderAuth(raw json.RawMessage) error {
	var provider map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &provider) != nil || provider == nil {
		return fmt.Errorf("Codex 有效配置没有可核对的指定 provider")
	}
	for _, field := range []string{"experimental_bearer_token", "auth", "aws", "gateway_oauth"} {
		if value := provider[field]; len(value) != 0 && string(value) != "null" && string(value) != `""` {
			return fmt.Errorf("Codex provider 存在额外认证来源，请使用只采用指定 env: 凭据的 profile")
		}
	}
	for _, field := range []string{"http_headers", "env_http_headers"} {
		var headers map[string]json.RawMessage
		if raw := provider[field]; len(raw) != 0 && json.Unmarshal(raw, &headers) != nil {
			return fmt.Errorf("Codex provider header 配置无法核对")
		}
		for name := range headers {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-goog-api-key":
				return fmt.Errorf("Codex provider header 存在冲突认证来源，请核对独立 profile")
			}
		}
	}
	// Query parameters may select credentials or an alternate upstream, and
	// are outside the declared endpoint identity used by the console.
	var query map[string]json.RawMessage
	if raw := provider["query_params"]; len(raw) != 0 {
		if json.Unmarshal(raw, &query) != nil || len(query) > 0 {
			return fmt.Errorf("Codex provider 附加查询参数未包含在指定路由身份中")
		}
	}
	return nil
}
