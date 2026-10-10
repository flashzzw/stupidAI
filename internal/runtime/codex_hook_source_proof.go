package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// CodexHookSourceProof records the original configuration established by this
// client. It is historical evidence, never a claim that a thread is loaded.
// Candidate files and current native occupancy must still be checked separately.
type CodexHookSourceProof struct {
	ConfigurationTargetHash string `json:"configuration_target_hash,omitempty"`
	Version                 int    `json:"version"`
	ScopeHash               string `json:"scope_hash"`
	Fingerprint             string `json:"fingerprint"`
	RequirementsHash        string `json:"requirements_hash"`
	At                      string `json:"at"`
}

type CodexHookSourceProofReader interface {
	ReadCodexHookSourceProof(context.Context, Scope, Scope, *CodexHookSourceProof) (*CodexHookSourceProof, error)
}

func codexHookSourceProofScope(scope Scope) string {
	b, _ := json.Marshal(scope)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func ValidateCodexHookSourceProof(scope Scope, proof *CodexHookSourceProof) error {
	if proof == nil || proof.Version != 1 || scope.Instance.Kind != "codex" || scope.NativeID == "" || scope.Purpose != "" || proof.ScopeHash != codexHookSourceProofScope(scope) || (!hookDefinitionHash.MatchString(proof.Fingerprint) || !hookDefinitionHash.MatchString(proof.RequirementsHash)) {
		return fmt.Errorf("hook 采纳的原配置依据缺失或不属于原绑定")
	}
	if at, err := time.Parse(time.RFC3339Nano, proof.At); err != nil || at.IsZero() {
		return fmt.Errorf("hook 采纳的原配置依据时间无效")
	}
	if proof.ConfigurationTargetHash != "" && !hookDefinitionHash.MatchString(proof.ConfigurationTargetHash) {
		return fmt.Errorf("配置采纳的候选来源依据无效")
	}
	return nil
}

func (a *Adapters) ReadCodexHookSourceProof(ctx context.Context, previous, target Scope, prior *CodexHookSourceProof) (*CodexHookSourceProof, error) {
	adapter, err := a.adapter(previous.Instance.Kind)
	if err != nil {
		return nil, err
	}
	reader, ok := adapter.(CodexHookSourceProofReader)
	if !ok {
		return nil, fmt.Errorf("运行时尚不能记录 hook 采纳的原配置依据")
	}
	return reader.ReadCodexHookSourceProof(ctx, previous, target, prior)
}

func (c *Codex) ReadCodexHookSourceProof(ctx context.Context, previous, target Scope, prior *CodexHookSourceProof) (*CodexHookSourceProof, error) {
	if !CodexSourceAdoption(previous, target) {
		return nil, fmt.Errorf("需要原身份和明确的 hook 文件候选")
	}
	if err := VerifyStateNamespace(previous); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, previous.Instance)
	if err != nil {
		return nil, err
	}
	scope := previous
	scope.hookSourceTarget = &codexHookSourceTarget{target.CodexUserHooks, target.CodexProjectHooks, target.CodexConfigurationSources}
	if prior != nil {
		if err := c.verifyCodexHookSourceProof(ctx, p, scope, prior); err != nil {
			return nil, err
		}
		copy := *prior
		return &copy, nil
	}
	if err := c.verifyHookConfiguration(ctx, p, scope); err != nil {
		return nil, fmt.Errorf("尚未保存原配置依据，且原连接无法核对：%w", err)
	}
	if err := verifyCodexAdoptionEffort(ctx, p, scope); err != nil {
		return nil, err
	}
	requirements, err := codexHookSourceRequirementsHash(ctx, p)
	if err != nil {
		return nil, err
	}
	witness := c.hookWitnesses[scope.Instance.ID+"/"+scope.NativeID]
	return &CodexHookSourceProof{Version: 1, ScopeHash: codexHookSourceProofScope(previous), Fingerprint: witness.Fingerprint, RequirementsHash: requirements, ConfigurationTargetHash: codexConfigurationTargetHash(scope), At: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func (c *Codex) verifyCodexHookSourceProof(ctx context.Context, p codexConnection, scope Scope, proof *CodexHookSourceProof) error {
	if err := ValidateCodexHookSourceProof(scope, proof); err != nil {
		return err
	}
	if proof.ConfigurationTargetHash != codexConfigurationTargetHash(scope) {
		return fmt.Errorf("配置采纳依据与已保存候选来源不一致")
	}
	if err := verifyCodexAdoptionEffort(ctx, p, scope); err != nil {
		return err
	}
	requirements, err := codexHookSourceRequirementsHash(ctx, p)
	if err != nil {
		return err
	}
	if requirements != proof.RequirementsHash {
		return fmt.Errorf("候选保存后原生受管策略已变化；请重新核对")
	}
	current, err := codexConfigurationFingerprint(ctx, p, scope)
	if err != nil {
		return err
	}
	if current != proof.Fingerprint {
		return fmt.Errorf("原配置的非采纳部分已变化；保存的依据不能用于当前候选")
	}
	return nil
}

// Requirements are bound at proposal time even for ordinary file hooks, whose
// original connection witness does not include managed policy. Never infer the
// absence of policy from a failed read or persist its potentially private body.
func codexHookSourceRequirementsHash(ctx context.Context, p codexConnection) (string, error) {
	requirements, err := readCodexHookRequirements(ctx, p)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(requirements)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
