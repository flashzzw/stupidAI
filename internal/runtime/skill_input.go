package runtime

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// A caller selects an asset/name pair. Native paths come only from the saved
// execution binding, never from an input field supplied by the browser.
type SkillInput struct {
	AssetID string `json:"asset_id"`
	Name    string `json:"name"`
}

type BoundSkillInput struct {
	SkillInput
	Path       string
	FileSHA256 string
}

// These fields are attached by Application from a uniquely correlated saved
// operation/snapshot. They describe requested input and body loading, not use.
type SkillInputEvidence struct {
	OperationID string                   `json:"operation_id"`
	SnapshotID  string                   `json:"snapshot_id"`
	Items       []SkillInputLoadEvidence `json:"items"`
}

type SkillInputLoadEvidence struct {
	SkillInput
	AssetVersion   string   `json:"asset_version"`
	Path           string   `json:"path"`
	ExpectedSHA256 string   `json:"expected_sha256"`
	Status         string   `json:"status"`
	RecordIDs      []string `json:"record_ids"`
}

func ResolveCodexSkillInputs(scope Scope, inputs []SkillInput) ([]BoundSkillInput, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || len(inputs) > 16 {
		return nil, fmt.Errorf("显式 Skill 输入仅支持普通 Codex 会话且每条最多 16 项")
	}
	seen := map[string]bool{}
	out := []BoundSkillInput{}
	for _, input := range inputs {
		key := input.AssetID + "\x00" + input.Name
		if seen[key] {
			return nil, fmt.Errorf("同一条消息不能重复选择同一 Skill")
		}
		seen[key] = true
		var matches []BoundSkillInput
		for _, selected := range scope.SkillSelections {
			if selected.AssetID != input.AssetID || selected.Name != input.Name || selected.Mode != "enabled" {
				continue
			}
			if err := ValidateCodexDirectorySkill(scope, selected); err != nil {
				return nil, err
			}
			for _, file := range selected.CodexDirectory.Files {
				if file.Path == "SKILL.md" {
					matches = append(matches, BoundSkillInput{SkillInput: input, Path: selected.DiscoveryPath, FileSHA256: file.SHA256})
				}
			}
		}
		for _, pack := range scope.PluginSelections {
			if pack.AssetID != input.AssetID || pack.Mode != "enabled" {
				continue
			}
			if err := validateCodexPluginBinding(scope, pack); err != nil {
				return nil, err
			}
			for _, component := range pack.Components {
				if component.Kind != "skill" || component.NativeName != input.Name {
					continue
				}
				for _, file := range pack.Files {
					if file.Path != component.Path || file.Kind != "file" {
						continue
					}
					if _, err := hex.DecodeString(file.SHA256); err != nil || len(file.SHA256) != 64 {
						return nil, fmt.Errorf("所选 Skill 缺少固定文件摘要")
					}
					matches = append(matches, BoundSkillInput{SkillInput: input, Path: filepath.Join(pack.CachePath, component.Path), FileSHA256: file.SHA256})
				}
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("所选 Skill 未在当前快照唯一启用")
		}
		out = append(out, matches[0])
	}
	return out, nil
}
