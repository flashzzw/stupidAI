package runtime

import (
	"fmt"
	"reflect"
	"slices"
)

type PluginHookSelection struct {
	Trust      string                  `json:"trust"`
	Inspection PluginHookInspection    `json:"inspection"`
	Expected   []PluginHookExpectation `json:"expected"`
}

func equalPluginHooks(a, b *PluginHookSelection) bool { return reflect.DeepEqual(a, b) }

func pluginHookPackage(pack PluginSelection) PluginHookPackage {
	return PluginHookPackage{AssetID: pack.AssetID, Name: pack.Name, Version: pack.Version,
		SourcePath: pack.SourcePath, SourceHash: pack.SourceHash, RootMode: pack.RootMode, Files: pack.Files, Expected: pack.Hooks.Expected}
}

func ValidatePluginHookSelection(instance Instance, pack PluginSelection) error {
	h := pack.Hooks
	if h == nil || pack.SkillAsset != nil || h.Trust != "trusted" && h.Trust != "untrusted" {
		return fmt.Errorf("插件 hook 缺少明确的版本与信任绑定")
	}
	if err := ValidatePluginHookInspection(instance, pluginHookPackage(pack), h.Inspection); err != nil {
		return err
	}
	for _, definition := range h.Inspection.Definitions {
		if definition.Background {
			return fmt.Errorf("异步 hook 选配尚未核对")
		}
	}
	for _, expected := range h.Expected {
		if !slices.ContainsFunc(pack.Components, func(c PluginComponent) bool {
			return c.Kind == "hook" && c.Path == expected.Path && c.NativeName == expected.Event && c.Count == expected.Count
		}) {
			return fmt.Errorf("hook 组件与固定定义清单不一致")
		}
	}
	return nil
}
