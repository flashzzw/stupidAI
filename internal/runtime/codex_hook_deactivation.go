package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// CodexHookDeactivation describes a candidate, not permission to apply it.
// All non-hook bindings stay exact; local hook packages may only be disabled
// or retired, and no enabled local hook may remain in the candidate.
func CodexHookDeactivation(before, target Scope) bool {
	if before.Instance.Kind != "codex" || before.NativeID == "" || before.Purpose != "" || len(before.PluginSelections) > 16 || len(target.PluginSelections) > 16 {
		return false
	}
	identity := before
	identity.PluginSelections = target.PluginSelections
	a, _ := json.Marshal(identity)
	b, _ := json.Marshal(target)
	if !bytes.Equal(a, b) {
		return false
	}
	wanted := map[string]PluginSelection{}
	for _, pack := range target.PluginSelections {
		if _, exists := wanted[pack.AssetID]; exists || pack.Hooks != nil && pack.Mode != "disabled" {
			return false
		}
		wanted[pack.AssetID] = pack
	}
	changed := false
	seen := map[string]bool{}
	for _, old := range before.PluginSelections {
		if seen[old.AssetID] {
			return false
		}
		seen[old.AssetID] = true
		next, exists := wanted[old.AssetID]
		delete(wanted, old.AssetID)
		if !exists {
			if old.Hooks == nil {
				return false
			}
			changed = true
			continue
		}
		if old.Hooks == nil {
			if !reflect.DeepEqual(old, next) {
				return false
			}
			continue
		}
		if next.Hooks == nil || old.Mode != "enabled" && old.Mode != "disabled" {
			return false
		}
		copy := old
		copy.Mode = next.Mode
		if old.Hooks.Trust == "trusted" && next.Hooks.Trust == "untrusted" {
			hooks := *old.Hooks
			hooks.Trust = next.Hooks.Trust
			copy.Hooks = &hooks
		}
		if !reflect.DeepEqual(copy, next) {
			return false
		}
		changed = changed || !reflect.DeepEqual(old, next)
	}
	return changed && len(wanted) == 0
}

func (c *Codex) managedHookDeactivation(ctx context.Context, p codexConnection, original, target Scope) (Scope, error) {
	if !CodexHookDeactivation(original, target) {
		return original, nil
	}
	requirements, err := readCodexHookRequirements(ctx, p)
	if err != nil {
		return original, err
	}
	if requirements == nil || requirements["allowManagedHooksOnly"] != true {
		return original, nil
	}
	original.hookDeactivation = true
	original.directorySkillChange = false // Unchanged directory resources still need full checks.
	return original, c.verifyHookDeactivationConfiguration(ctx, p, original)
}

func (c *Codex) verifyHookDeactivationConfiguration(ctx context.Context, p codexConnection, scope Scope) error {
	w, ok := c.hookWitnesses[scope.Instance.ID+"/"+scope.NativeID]
	if !scope.hookDeactivation || !ok || w.ConnectionID != p.ConnectionID() || w.SelectionHash != codexScopedSelectionHash(scope) || w.ConfigurationFingerprint == "" {
		return fmt.Errorf("撤去受管策略限制的本地 hook 仍需原连接的配置建立依据；不会采用未知配置")
	}
	_, configuration, err := codexConfigurationFingerprints(ctx, p, scope)
	if err != nil {
		return err
	}
	if configuration != w.ConfigurationFingerprint {
		return fmt.Errorf("原连接建立的配置已变化，不能仅按受管 hook 撤去流程处理")
	}
	return nil
}
