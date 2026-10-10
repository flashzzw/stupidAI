package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"codex-netcap/internal/codex"
)

// Configuration adoption reuses the existing source transaction and its
// historical establishment proof. Only the verified effort setting may change;
// capability selections are independently bound by that transaction.
func CodexSourceAdoption(previous, target Scope) bool {
	return CodexHookSourceAdoption(previous, target) || CodexConfigurationSourceAdoption(previous, target)
}

func CodexConfigurationSourceAdoption(previous, target Scope) bool {
	a, b := previous.CodexConfigurationSources, target.CodexConfigurationSources
	if previous.Instance.Kind != "codex" || previous.NativeID == "" || previous.Purpose != "" || a == nil || b == nil || a.Version != 1 || b.Version != 1 || a.Profile != b.Profile || a.Cwd != b.Cwd || a.Settings == nil || b.Settings == nil || reflect.DeepEqual(a, b) {
		return false
	}
	diff := CompareConfigurationSettings(a.Settings, b.Settings)
	if (diff.Status != "changed" && diff.Status != "unchanged") || diff.OtherValuesChanged || diff.OtherSourcesChanged || a.Settings.ReasoningEffort.State != "set" || b.Settings.ReasoningEffort.State != "set" {
		return false
	}
	if len(a.Layers) != len(b.Layers) || a.Layers == nil || !reflect.DeepEqual(previous.CodexUserHooks, target.CodexUserHooks) || previous.CodexUserHooks == nil || !codexConfigurationHookMetadata(previous, target) {
		return false
	}
	for i, layer := range a.Layers {
		next := b.Layers[i]
		if layer.ID != next.ID || layer.Kind != next.Kind || layer.Disabled != next.Disabled || (layer.File == nil) != (next.File == nil) {
			return false
		}
		if layer.File == nil {
			if !reflect.DeepEqual(layer, next) {
				return false
			}
			continue
		}
		if layer.File.Path != next.File.Path || layer.File.ResolvedPath != next.File.ResolvedPath || layer.File.Kind != next.File.Kind {
			return false
		}
		// System and managed layers cannot piggyback on a local adoption.
		if layer.Kind != "user" && layer.Kind != "project" && !reflect.DeepEqual(layer, next) {
			return false
		}
	}
	previous.CodexConfigurationSources = target.CodexConfigurationSources
	previous.CodexProjectHooks = target.CodexProjectHooks
	previous.MCPSelections, previous.SkillSelections, previous.PluginSelections = target.MCPSelections, target.SkillSelections, target.PluginSelections
	return codexHookSourceProofScope(previous) == codexHookSourceProofScope(target)
}

func codexConfigurationHookMetadata(previous, target Scope) bool {
	a, b := previous.CodexProjectHooks, target.CodexProjectHooks
	if a == nil || b == nil || len(a.Files) != len(b.Files) {
		return false
	}
	copy := *a
	copy.Files = append([]CodexProjectHookFile{}, a.Files...)
	for i, file := range copy.Files {
		if file.LayerVersion == b.Files[i].LayerVersion {
			continue
		}
		matched := false
		for j, layer := range previous.CodexConfigurationSources.Layers {
			if layer.Kind == "project" && layer.File != nil && filepath.Dir(layer.File.Path) == filepath.Dir(file.File.Path) && layer.Version == file.LayerVersion && j < len(target.CodexConfigurationSources.Layers) && target.CodexConfigurationSources.Layers[j].Version == b.Files[i].LayerVersion {
				matched = true
			}
		}
		if !matched {
			return false
		}
		copy.Files[i].LayerVersion = b.Files[i].LayerVersion
	}
	return reflect.DeepEqual(&copy, b)
}

func codexConfigurationTargetHash(scope Scope) string {
	if scope.hookSourceTarget == nil || !CodexConfigurationSourceAdoption(scope, codexHookSourceScope(scope)) {
		return ""
	}
	return configurationDigest(scope.hookSourceTarget)
}

func codexRecordedEffort(scope Scope) string {
	if source := scope.CodexConfigurationSources; source != nil && source.Settings != nil && source.Settings.ReasoningEffort.State == "set" {
		return source.Settings.ReasoningEffort.Value
	}
	return ""
}

func verifyCodexEffort(scope Scope, thread codex.Thread) error {
	if effort := codexRecordedEffort(scope); effort != "" && thread.Status.Type != "notLoaded" && thread.Effort != effort {
		return fmt.Errorf("原线程 reasoning effort 与保存版本不一致；保持待核对")
	}
	return nil
}

func verifyCodexAdoptionEffort(ctx context.Context, p codexConnection, scope Scope) error {
	if codexConfigurationTargetHash(scope) == "" {
		return nil
	}
	thread, err := codex.Read(ctx, p, scope.NativeID)
	if err != nil || thread.ID != scope.NativeID {
		return fmt.Errorf("来源采纳前无法读取原线程设置")
	}
	return verifyCodexEffort(scope, thread)
}
