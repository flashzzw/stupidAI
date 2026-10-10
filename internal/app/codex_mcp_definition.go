package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"codex-netcap/internal/runtime"
)

func codexMCPSelections(ref CapabilityReference, mode string) ([]runtime.MCPSelection, error) {
	selections, err := claudeMCPSelections(ref, mode)
	if err != nil {
		return nil, err
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil || stamp != ref.Source {
		return nil, fmt.Errorf("共享 MCP 来源在固定版本期间变化")
	}
	for i := range selections {
		entry := tree.Name
		if tree.Kind == "directory" {
			entry = filepath.Base(selections[i].SourcePath)
		}
		sum := sha256.Sum256(tree.files()[entry])
		selections[i].Definition = &runtime.MCPDefinition{Version: 2, SHA256: hex.EncodeToString(sum[:]), AssetVersion: ref.Asset.Version, SourceHash: ref.Source.SHA256}
	}
	return selections, nil
}

func (a *Application) captureCodexMCPDefinitions(scope runtime.Scope) ([]runtime.MCPSelection, error) {
	reader, ok := a.runtime.(runtime.CodexMCPDefinitionReader)
	if !ok {
		return nil, fmt.Errorf("当前运行时尚不能固定 Codex 共享 MCP 的定义与环境引用")
	}
	before := queryCopy(scope.MCPSelections)
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	selections, err := reader.ReadCodexMCPDefinitions(ctx, queryCopy(scope))
	if err != nil {
		return nil, err
	}
	if len(selections) != len(before) {
		return nil, fmt.Errorf("运行时返回的 MCP 定义清单不完整")
	}
	for i, selection := range selections {
		original := before[i]
		if selection.AssetID != original.AssetID || selection.Server != original.Server || selection.Mode != original.Mode || selection.SourcePath != original.SourcePath ||
			(selection.Definition == nil) != (original.Definition == nil) {
			return nil, fmt.Errorf("运行时返回的 MCP 定义不属于所选资产")
		}
		if original.Definition != nil && (selection.Definition.Version != original.Definition.Version || selection.Definition.SHA256 != original.Definition.SHA256 ||
			selection.Definition.AssetVersion != original.Definition.AssetVersion || selection.Definition.SourceHash != original.Definition.SourceHash) {
			return nil, fmt.Errorf("运行时改变了所选 MCP 的文件版本")
		}
	}
	scope.MCPSelections = selections
	if err := runtime.ValidateCodexMCPDefinitions(scope); err != nil {
		return nil, err
	}
	return queryCopy(selections), nil
}

// Enabling an inactive definition is an explicit new snapshot. Bind its
// environment now; merely inspecting or disabling it never reads credentials.
func (a *Application) bindActivatedCodexMCPDefinitions(session Session, candidate *ExecutionSnapshot) error {
	if session.Scope.Instance.Kind != "codex" {
		return nil
	}
	indexes := []int{}
	selections := []runtime.MCPSelection{}
	for i, next := range candidate.MCPSelections {
		if next.Definition == nil || next.Mode != "enabled" {
			continue
		}
		for _, previous := range session.ExecutionSnapshot.MCPSelections {
			if previous.AssetID == next.AssetID && previous.Server == next.Server && previous.Mode == "disabled" {
				indexes = append(indexes, i)
				selections = append(selections, next)
			}
		}
	}
	if len(indexes) == 0 {
		return nil
	}
	scope := session.Scope
	scope.MCPSelections = selections
	scope.CodexConfigurationSources = candidate.CodexConfigurationSources
	bound, err := a.captureCodexMCPDefinitions(scope)
	if err != nil {
		return err
	}
	for i, index := range indexes {
		candidate.MCPSelections[index] = bound[i]
	}
	return nil
}
