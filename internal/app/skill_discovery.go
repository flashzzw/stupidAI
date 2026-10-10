package app

import (
	"os"
	"path/filepath"
	"strings"

	"codex-netcap/internal/runtime"
)

// Discovery describes the current directory inventory and the saved snapshot
// separately. Neither control hints nor a matching path attest current loading,
// resource bytes, or admission. Do not enrich these with the latest asset version.
type SkillDiscovery struct {
	runtime.SkillInventory
	SnapshotID      string                `json:"snapshot_id"`
	Controls        []SkillSourceControl  `json:"controls"`
	SavedSelections []SavedDirectorySkill `json:"saved_selections"`
}

type SkillSourceControl struct {
	Path    string `json:"path"`
	Control string `json:"control"`
	Detail  string `json:"detail"`
}

type SavedDirectorySkill struct {
	AssetID    string `json:"asset_id"`
	Version    string `json:"version"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Mode       string `json:"mode"`
	Discovered bool   `json:"discovered"`
}

func explainSkillDiscovery(session Session, inventory runtime.SkillInventory) SkillDiscovery {
	out := SkillDiscovery{SkillInventory: inventory, Controls: []SkillSourceControl{}, SavedSelections: []SavedDirectorySkill{}}
	paths, names := map[string]bool{}, map[string]int{}
	identities := make([]os.FileInfo, len(inventory.Skills))
	identitiesComplete := inventory.Complete && len(inventory.Errors) == 0
	for i, item := range inventory.Skills {
		paths[item.Path] = true
		names[item.Name]++
		if filepath.IsAbs(item.Path) && filepath.Clean(item.Path) == item.Path && filepath.Base(item.Path) == "SKILL.md" && !strings.ContainsRune(item.Path, '\x00') {
			identities[i], _ = os.Stat(item.Path)
		}
		if identities[i] == nil || !identities[i].Mode().IsRegular() {
			identitiesComplete = false
		}
	}
	for i, item := range inventory.Skills {
		row := SkillSourceControl{Path: item.Path}
		switch {
		case item.Scope == "system" || item.Scope == "admin":
			row.Control, row.Detail = "unsupported", "当前没有此类来源的独立选配入口；不能据此宣称已排除或覆盖原生策略。"
		case item.PluginID != "":
			row.Control, row.Detail = "plugin_bundle", "此项属于 CLI 插件，需按插件完整来源登记并整包选配；目录启停不代表本会话的插件选择或信任。"
		case item.Scope != "user" && item.Scope != "repo":
			row.Control, row.Detail = "unknown", "此来源类别尚未核对。"
		case names[item.Name] > 1:
			row.Control, row.Detail = "ambiguous", "原生清单存在同名 Skill，当前不能按单项独立选配。"
		default:
			aliases := 0
			for _, other := range identities {
				if identities[i] != nil && other != nil && os.SameFile(identities[i], other) {
					aliases++
				}
			}
			switch {
			case aliases > 1:
				row.Control, row.Detail = "ambiguous", "多个原生入口指向同一文件，当前不能按路径别名单独选配。"
			case !identitiesComplete:
				row.Control, row.Detail = "unknown", "清单不完整或入口身份无法读取，暂不能排除同名与文件别名；请修复来源后刷新。"
			default:
				row.Control, row.Detail = "native_directory", "普通原生入口：可在能力库登记此准确路径，再通过执行改配选择启用、禁用或恢复继承。登记与应用仍须核对完整资源及原生准入。"
			}
		}
		out.Controls = append(out.Controls, row)
	}
	if snapshot := session.ExecutionSnapshot; snapshot != nil {
		out.SnapshotID = snapshot.ID
		for _, selected := range snapshot.SkillSelections {
			if selected.Delivery != "native_directory" || selected.CodexDirectory == nil {
				continue
			}
			version := ""
			for _, ref := range snapshot.CapabilityRefs {
				if ref.Asset.ID == selected.AssetID {
					version = ref.Asset.Version
					break
				}
			}
			out.SavedSelections = append(out.SavedSelections, SavedDirectorySkill{AssetID: selected.AssetID, Version: version, Name: selected.Name, Path: selected.DiscoveryPath, Mode: selected.Mode, Discovered: paths[selected.DiscoveryPath]})
		}
	}
	return out
}
