package app

import (
	"fmt"

	"codex-netcap/internal/runtime"
)

func codexSkillAssetSelection(scope runtime.Scope, ref CapabilityReference, mode string) (runtime.PluginSelection, error) {
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return runtime.PluginSelection{}, err
	}
	if ref.Asset.Kind != "skill" || stamp != ref.Source || diagnoseResources(ref.Asset, tree).Status == "invalid" {
		return runtime.PluginSelection{}, fmt.Errorf("Skill 来源或完整资源诊断无效")
	}
	metadata, err := skillDocumentMetadata(tree.files()["SKILL.md"])
	if err != nil {
		return runtime.PluginSelection{}, err
	}
	name, _ := metadata["name"].(string)
	source := runtime.PluginSelection{AssetID: ref.Asset.ID, Name: name, Mode: mode, SourcePath: stamp.Resolved, SourceHash: stamp.SHA256, RootMode: tree.Mode}
	for _, file := range tree.Entries {
		source.Files = append(source.Files, runtime.PluginFile{Path: file.Path, Kind: file.Kind, Mode: file.Mode, SHA256: file.SHA256})
	}
	return runtime.BindCodexSkillAsset(scope, source, tree.Kind)
}
