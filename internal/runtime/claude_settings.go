package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Native settings.env wins over the SDK child environment. Refuse identity
// overrides instead of silently sending with a different route or state home.
// This checks the registered files, not the complete native inheritance graph.
func ValidateClaudeSettings(instance Instance) error {
	return validateClaudeSettings(instance, false)
}

// Native metadata/history reads keep the original account settings as data.
// They do not select a workbench route or execute apiKeyHelper, but settings
// that can redirect the state namespace must still block those reads.
func ValidateClaudeNativeReadSettings(instance Instance) error {
	return validateClaudeSettings(instance, true)
}

func validateClaudeSettings(instance Instance, nativeRead bool) error {
	if instance.Kind != "claude" {
		return nil
	}
	for _, source := range []struct {
		path     string
		optional bool
	}{
		{filepath.Join(instance.Profile, "settings.json"), true}, {instance.SettingsFile, false},
	} {
		if source.path == "" {
			continue
		}
		if err := validateClaudeSettingsSource(source.path, source.optional, nativeRead); err != nil {
			return err
		}
	}
	return nil
}

// ValidateClaudeExecutionSettings checks identity-affecting fields before any
// bridge operation. It does not establish complete policy or permission state.
func ValidateClaudeExecutionSettings(ctx context.Context, scope Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateClaudeSettings(scope.Instance); err != nil {
		return err
	}
	if scope.Instance.Kind != "claude" || scope.Purpose == "probe" {
		return nil
	}
	paths, err := claudeLocalSettingsPaths(ctx, scope)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateClaudeSettingsFile(path, true); err != nil {
			return err
		}
	}
	return nil
}

func validateClaudeSettingsFile(path string, optional bool) error {
	return validateClaudeSettingsSource(path, optional, false)
}

func validateClaudeSettingsSource(path string, optional, nativeRead bool) error {
	source, body, err := readClaudeSettingsFile(path, optional)
	if err != nil {
		return err
	}
	if source.Kind == "absent" {
		return nil
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}), &value) != nil || value == nil {
		return fmt.Errorf("Claude settings 必须是 JSON 对象：%s", path)
	}
	if raw, ok := value["apiKeyHelper"]; ok {
		if !nativeRead {
			return fmt.Errorf("Claude settings 的 apiKeyHelper 与登记的凭据引用冲突：%s", path)
		}
		var helper *string
		if json.Unmarshal(raw, &helper) != nil || helper == nil {
			return fmt.Errorf("Claude settings 的 apiKeyHelper 必须是字符串：%s", path)
		}
	}
	if raw, ok := value["env"]; ok {
		var env map[string]json.RawMessage
		if json.Unmarshal(raw, &env) != nil || env == nil {
			return fmt.Errorf("Claude settings.env 必须是字符串映射：%s", path)
		}
		for key, rawValue := range env {
			var value *string
			if json.Unmarshal(rawValue, &value) != nil || value == nil {
				return fmt.Errorf("Claude settings.env 必须是字符串映射：%s", path)
			}
			identityOverride := key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_CODE_PROJECT_DIR_NAME" || key == "HOME" || strings.HasPrefix(key, "XDG_")
			routeOverride := strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_")
			if nativeRead && identityOverride {
				return fmt.Errorf("Claude settings.env 含状态目录覆盖，不能核对原生只读来源：%s", path)
			}
			if identityOverride || routeOverride && !nativeRead {
				return fmt.Errorf("Claude settings.env 含路由、凭据或状态目录覆盖；请使用独立原生目录和登记路由：%s", path)
			}
		}
	}
	return nil
}
