package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type codexConfigurationWitness struct{ ConnectionID, Fingerprint, SelectionHash, ConfigurationFingerprint string }

func codexScopedSelectionHash(scope Scope) string {
	b, _ := json.Marshal(struct {
		Instance             Instance
		Cwd                  string
		Route                Route
		Packs                []PluginSelection
		Skills               []SkillSelection
		UserHooks            *CodexUserHookSource       `json:",omitempty"`
		ProjectHooks         *CodexProjectHookSources   `json:",omitempty"`
		ConfigurationSources *CodexConfigurationSources `json:",omitempty"`
	}{scope.Instance, scope.Cwd, scope.Route, scope.PluginSelections, scope.SkillSelections, scope.CodexUserHooks, scope.CodexProjectHooks, scope.CodexConfigurationSources})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hasCodexHookSelections(scope Scope) bool {
	return slices.ContainsFunc(scope.PluginSelections, func(p PluginSelection) bool { return p.Hooks != nil })
}

func addCodexHookOverrides(scope Scope, features map[string]bool, inherited map[string]json.RawMessage, overrides map[string]any) error {
	if !hasCodexHookSelections(scope) {
		return nil
	}
	if !features["hooks"] && !features["codex_hooks"] {
		return fmt.Errorf("实例尚未启用原生 hooks，不能核对 hook 选择")
	}
	if len(inherited) > 4096 {
		return fmt.Errorf("继承 hook 信任状态超过核对范围")
	}
	state := map[string]json.RawMessage{}
	for key, value := range inherited {
		if !validInspectionText(key, 4096) || len(value) > 4096 {
			return fmt.Errorf("继承 hook 状态无效")
		}
		state[key] = append(json.RawMessage{}, value...)
	}
	for _, pack := range scope.PluginSelections {
		if pack.Hooks == nil {
			continue
		}
		if err := ValidatePluginHookSelection(scope.Instance, pack); err != nil {
			return err
		}
		for key := range inherited {
			if strings.HasPrefix(key, pack.NativeSource+":") {
				return fmt.Errorf("私有 hook 来源已有继承信任配置，不能确认独立绑定")
			}
		}
		for _, definition := range pack.Hooks.Inspection.Definitions {
			key := pack.NativeSource + strings.TrimPrefix(definition.Key, pack.Hooks.Inspection.NativeSource)
			value := map[string]any{"enabled": true}
			if pack.Hooks.Trust == "trusted" {
				value["trusted_hash"] = definition.DefinitionHash
			}
			state[key], _ = json.Marshal(value)
		}
	}
	overrides["hooks.state"] = state
	return nil
}

// Native APIs do not expose a per-thread hook trust table. This witness only
// describes configuration established on this connection, never an adopted
// loaded thread or a persisted claim of current native policy.
func codexHookFingerprint(ctx context.Context, p codexConnection, scope Scope) (string, error) {
	if !hasCodexHookSelections(scope) && !hasCodexHookSources(scope) {
		return "", nil
	}
	return codexConfigurationFingerprint(ctx, p, scope)
}

func codexConfigurationFingerprint(ctx context.Context, p codexConnection, scope Scope) (string, error) {
	full, _, err := codexConfigurationFingerprints(ctx, p, scope)
	return full, err
}

func codexConfigurationFingerprints(ctx context.Context, p codexConnection, scope Scope) (string, string, error) {
	if err := verifyCodexConfigurationSources(ctx, p, scope); err != nil {
		return "", "", err
	}
	if err := verifyCodexProjectHooks(ctx, p, scope); err != nil {
		return "", "", err
	}
	if err := VerifyCodexUserHooks(scope); err != nil {
		return "", "", err
	}
	var requirements map[string]any
	if hasCodexHookSelections(scope) {
		var err error
		requirements, err = codexHookRequirements(ctx, p, scope)
		if err != nil {
			return "", "", err
		}
	}
	var read struct {
		Config json.RawMessage `json:"config"`
	}
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": false}, &read); err != nil || read.Config == nil {
		return "", "", fmt.Errorf("无法核对继承配置")
	}
	var config map[string]any
	decoder := json.NewDecoder(bytes.NewReader(read.Config))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil || config == nil {
		return "", "", fmt.Errorf("继承配置响应无效")
	}
	if codexConfigurationTargetHash(scope) != "" {
		// Reconstruct only the previously recorded public setting. All other
		// values and provenance were checked against the explicit target.
		config["model_reasoning_effort"] = codexRecordedEffort(scope)
		if configurationDigest(config) != scope.CodexConfigurationSources.ConfigurationSHA256 {
			return "", "", fmt.Errorf("配置采纳不能重建原合并配置依据")
		}
	}
	value := struct {
		Instance             Instance
		Cwd                  string
		Route                Route
		Packs                []PluginSelection
		Skills               []SkillSelection
		Config               map[string]any
		Requirements         map[string]any
		UserHooks            *CodexUserHookSource       `json:",omitempty"`
		ProjectHooks         *CodexProjectHookSources   `json:",omitempty"`
		ConfigurationSources *CodexConfigurationSources `json:",omitempty"`
	}{scope.Instance, scope.Cwd, scope.Route, scope.PluginSelections, scope.SkillSelections, config, requirements, scope.CodexUserHooks, scope.CodexProjectHooks, scope.CodexConfigurationSources}
	b, err := json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(b)
	value.Requirements = nil
	b, err = json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	configuration := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), hex.EncodeToString(configuration[:]), nil
}

func (c *Codex) prepareSelectedHooks(ctx context.Context, p codexConnection, scope Scope) (string, error) {
	if !hasCodexHookSelections(scope) && !hasCodexHookSources(scope) {
		return "", nil
	}
	for _, pack := range scope.PluginSelections {
		if pack.Hooks == nil {
			continue
		}
		if err := ValidatePluginHookSelection(scope.Instance, pack); err != nil {
			return "", err
		}
		actual, err := c.InspectPluginHooks(ctx, scope.Instance, pluginHookPackage(pack))
		if err != nil {
			return "", err
		}
		if len(actual.Definitions) != len(pack.Hooks.Inspection.Definitions) {
			return "", fmt.Errorf("hook 原生定义数量已变化")
		}
		for _, definition := range actual.Definitions {
			if !slices.Contains(pack.Hooks.Inspection.Definitions, definition) {
				return "", fmt.Errorf("hook 原生定义与审核版本不符")
			}
		}
	}
	return codexHookFingerprint(ctx, p, scope)
}

func (c *Codex) rememberSelectedHooks(ctx context.Context, p codexConnection, scope Scope, nativeID, before string) error {
	// A nonempty fingerprint also attests an acknowledged transition to no
	// local hook selections. Exclusion policy alone cannot prove retirement.
	if !hasCodexHookSelections(scope) && before == "" {
		return nil
	}
	key := scope.Instance.ID + "/" + nativeID
	delete(c.hookWitnesses, key)
	after, configuration, err := codexConfigurationFingerprints(ctx, p, scope)
	if err != nil || before == "" || before != after {
		return fmt.Errorf("建立 hook 配置期间继承配置已变化或无法核对")
	}
	if c.hookWitnesses == nil {
		c.hookWitnesses = map[string]codexConfigurationWitness{}
	}
	c.hookWitnesses[key] = codexConfigurationWitness{ConnectionID: p.ConnectionID(), Fingerprint: after, SelectionHash: codexScopedSelectionHash(scope), ConfigurationFingerprint: configuration}
	return nil
}

func (c *Codex) verifySelectedHooks(ctx context.Context, p codexConnection, scope Scope) error {
	if !hasCodexHookSelections(scope) {
		return nil
	}
	if scope.hookDeactivation {
		return c.verifyHookDeactivationConfiguration(ctx, p, scope)
	}
	return c.verifyHookConfiguration(ctx, p, scope)
}

var errCodexHookWitnessMissing = errors.New("当前加载的 hook 配置缺少本连接的建立依据；保持待核对")

func (c *Codex) verifyHookConfiguration(ctx context.Context, p codexConnection, scope Scope) error {
	key := scope.Instance.ID + "/" + scope.NativeID
	w, ok := c.hookWitnesses[key]
	if !ok || w.ConnectionID != p.ConnectionID() {
		if _, err := codexHookRequirements(ctx, p, scope); err != nil {
			return err
		}
		return errCodexHookWitnessMissing
	}
	// Read-only reconciliation may ask about a saved candidate before it is
	// applied. A different expectation is not evidence that the established
	// configuration drifted, and must not erase the current version's witness.
	if w.SelectionHash != codexScopedSelectionHash(scope) {
		return fmt.Errorf("本连接尚未建立该候选 hook 配置；当前版本依据保留")
	}
	// Keep the old establishment witness when only recorded hook files drift.
	// It cannot admit input, but an explicit source adoption may need it.
	if err := VerifyCodexUserHooks(scope); err != nil {
		return err
	}
	if err := VerifyCodexConfigurationFiles(scope); err != nil {
		return err // Preserve historical establishment while refusing input.
	}
	if err := verifyCodexProjectHooks(ctx, p, scope); err != nil {
		return err
	}
	current, err := codexConfigurationFingerprint(ctx, p, scope)
	if err != nil || current != w.Fingerprint {
		var managedOnly *codexManagedHooksOnly
		// Policy rejection withdraws readiness, but does not erase the fact
		// that this connection established the unchanged local configuration.
		if !errors.As(err, &managedOnly) {
			delete(c.hookWitnesses, key)
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("hook 来源、选择、继承配置或受管策略已变化；保持待核对")
	}
	return nil
}
