package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// This transient target authorizes checking the candidate's files during an
// explicit transition. The original scope still identifies the loaded version.
// It is never persisted or accepted from a request payload.
type codexHookSourceTarget struct {
	User          *CodexUserHookSource
	Project       *CodexProjectHookSources
	Configuration *CodexConfigurationSources
}

func hasCodexHookSources(scope Scope) bool {
	return scope.CodexUserHooks != nil || scope.CodexProjectHooks != nil
}

// CodexHookSourceAdoption describes a change, not permission to apply it.
// Source adoption can accompany capability changes but never changes identity,
// provider, permissions, profile or the ownership of state.
func CodexHookSourceAdoption(previous, target Scope) bool {
	if previous.Instance.Kind != "codex" || previous.NativeID == "" || previous.Purpose != "" || previous.CodexUserHooks == nil || previous.CodexProjectHooks == nil || target.CodexUserHooks == nil || target.CodexProjectHooks == nil {
		return false
	}
	if previous.CodexUserHooks.Version != 1 || target.CodexUserHooks.Version != 1 || previous.CodexProjectHooks.Version != 1 || target.CodexProjectHooks.Version != 1 {
		return false
	}
	if reflect.DeepEqual(previous.CodexUserHooks, target.CodexUserHooks) && reflect.DeepEqual(previous.CodexProjectHooks, target.CodexProjectHooks) {
		return false
	}
	if !CodexHookConfigurationTransition(previous, target) {
		return false
	}
	previous.CodexConfigurationSources = target.CodexConfigurationSources
	previous.CodexUserHooks = target.CodexUserHooks
	previous.CodexProjectHooks = target.CodexProjectHooks
	previous.MCPSelections = target.MCPSelections
	previous.SkillSelections = target.SkillSelections
	previous.PluginSelections = target.PluginSelections
	a, ae := json.Marshal(previous)
	b, be := json.Marshal(target)
	return ae == nil && be == nil && bytes.Equal(a, b)
}

func codexHookSourceScope(scope Scope) Scope {
	if scope.hookSourceTarget != nil {
		scope.CodexUserHooks = scope.hookSourceTarget.User
		scope.CodexProjectHooks = scope.hookSourceTarget.Project
		scope.CodexConfigurationSources = scope.hookSourceTarget.Configuration
		scope.hookSourceTarget = nil
	}
	return scope
}

func (c *Codex) codexHookSourceAdoptionScope(ctx context.Context, p codexConnection, previous, target Scope, proofs ...*CodexHookSourceProof) (Scope, error) {
	if !CodexSourceAdoption(previous, target) {
		return previous, fmt.Errorf("hook 文件采纳需要原版本、原身份和明确的候选来源")
	}
	if err := VerifyCodexUserHooks(target); err != nil {
		return previous, err
	}
	if err := verifyCodexProjectHooks(ctx, p, target); err != nil {
		return previous, err
	}
	previous.hookSourceTarget = &codexHookSourceTarget{target.CodexUserHooks, target.CodexProjectHooks, target.CodexConfigurationSources}
	// A file mismatch withdraws input readiness but does not prove that native
	// replaced its cached definition. Require our original establishment receipt
	// and unchanged remaining effective configuration on this same connection.
	if err := c.verifyHookConfiguration(ctx, p, previous); err != nil {
		if !errors.Is(err, errCodexHookWitnessMissing) || len(proofs) != 1 || proofs[0] == nil {
			return previous, fmt.Errorf("采纳 hook 文件前无法核对原连接配置：%w", err)
		}
		if err := c.verifyCodexHookSourceProof(ctx, p, previous, proofs[0]); err != nil {
			return previous, err
		}
		if err := codexRecoveryUnloaded(ctx, p, previous.NativeID); err != nil {
			return previous, fmt.Errorf("原连接已丢失；只能在原线程确认未加载后采纳：%w", err)
		}
		previous.hookSourceCold = true
	}
	if len(proofs) == 1 && proofs[0] != nil {
		if err := c.verifyCodexHookSourceProof(ctx, p, previous, proofs[0]); err != nil {
			return previous, err
		}
	}
	if codexConfigurationTargetHash(previous) != "" && (len(proofs) != 1 || proofs[0] == nil) {
		return previous, fmt.Errorf("配置采纳需要已保存的原配置与候选依据")
	}
	return previous, nil
}
