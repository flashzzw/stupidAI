package app

import (
	"fmt"
	"path/filepath"
	"slices"

	"codex-netcap/internal/runtime"
)

func codexDirectorySkillSelection(scope runtime.Scope, ref CapabilityReference, mode string) (runtime.SkillSelection, error) {
	var out runtime.SkillSelection
	if err := validateAsset(ref.Asset); err != nil {
		return out, err
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return out, err
	}
	if ref.Asset.CodexSkill == "" || stamp != ref.Source || diagnoseResources(ref.Asset, tree).Status == "invalid" {
		return out, fmt.Errorf("原生 Skill 映射或完整资源版本无效")
	}
	metadata, err := skillDocumentMetadata(tree.files()["SKILL.md"])
	if err != nil {
		return out, err
	}
	name, _ := metadata["name"].(string)
	entry := stamp.Resolved
	if tree.Kind == "directory" {
		entry = filepath.Join(entry, "SKILL.md")
	}
	resolved, err := filepath.EvalSymlinks(ref.Asset.CodexSkill)
	if err != nil || resolved != entry {
		return out, fmt.Errorf("原生 Skill 入口没有指向所登记的资源；硬链接别名不能代替完整目录绑定")
	}
	out = runtime.SkillSelection{AssetID: ref.Asset.ID, Name: name, NativeKey: ref.Asset.CodexSkill, Mode: mode, Source: "codex_directory", SourcePath: entry, DiscoveryPath: ref.Asset.CodexSkill, Delivery: "native_directory", ResourceRoot: stamp.Resolved, ResourceKind: tree.Kind, ResourceHash: stamp.SHA256, CodexDirectory: &runtime.CodexDirectorySkill{RootMode: tree.Mode}}
	for _, file := range tree.Entries {
		out.CodexDirectory.Files = append(out.CodexDirectory.Files, runtime.PluginFile{Path: file.Path, Kind: file.Kind, Mode: file.Mode, SHA256: file.SHA256})
	}
	return out, runtime.ValidateCodexDirectorySkill(scope, out)
}

// Explicit replacement/inheritance may retire changed original directory files.
// Every unchanged dependency and every candidate resource must still match.
func verifyExecutionChangeSources(session Session, candidate *ExecutionSnapshot) error {
	original := queryCopy(session.ExecutionSnapshot)
	if runtime.CodexConfigurationSourceAdoption(session.Scope, executionChangeScope(session.Scope, candidate)) {
		// Only the explicitly adopted configuration may drift. Capability
		// references to those same files still retain their independent stamp.
		original.CodexConfigurationSources = nil
		original.CodexProjectHooks = queryCopy(candidate.CodexProjectHooks)
		original.ProfileSource = candidate.ProfileSource
	}
	if codexHookSourceAdoption(original, candidate) {
		original.CodexUserHooks = nil
		original.CodexProjectHooks = nil
	}
	if localSettingsAdoption(original, candidate) {
		// Only the explicitly replaced local files may drift. Profile/custom
		// settings and all capability dependencies remain independently checked.
		original.ClaudeLocalSettings = queryCopy(candidate.ClaudeLocalSettings)
	}
	if session.Scope.Instance.Kind == "codex" {
		original.CapabilityRefs = slices.DeleteFunc(original.CapabilityRefs, func(ref CapabilityReference) bool {
			selected := slices.ContainsFunc(original.SkillSelections, func(s runtime.SkillSelection) bool {
				return s.AssetID == ref.Asset.ID && s.Delivery == "native_directory" && s.CodexDirectory != nil
			})
			if !selected {
				return false
			}
			for _, next := range candidate.CapabilityRefs {
				if next.Asset.ID == ref.Asset.ID {
					return digest(next) != digest(ref)
				}
			}
			return true
		})
	}
	for _, snapshot := range []*ExecutionSnapshot{original, candidate} {
		for _, source := range checkExecutionSources(snapshot) {
			if source.Status != "unchanged" {
				return fmt.Errorf("改配资源仍待核对（%s）：%s", source.Name, source.Detail)
			}
		}
	}
	return nil
}
