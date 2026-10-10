package runtime

import "slices"

type PluginFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256,omitempty"`
}
type PluginComponent struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	NativeName string `json:"native_name"`
	Count      int    `json:"count"`
}
type PluginSelection struct {
	Hooks          *PluginHookSelection  `json:"hooks,omitempty"`
	ManifestSource *PluginManifestSource `json:"manifest_source,omitempty"`
	SkillAsset     *SkillAssetDelivery   `json:"skill_asset,omitempty"`
	AssetID        string                `json:"asset_id"`
	Name           string                `json:"name"`
	Mode           string                `json:"mode"`
	Version        string                `json:"version"`
	SourcePath     string                `json:"source_path"`
	SourceHash     string                `json:"source_hash"`
	RootMode       uint32                `json:"root_mode"`
	Files          []PluginFile          `json:"files"`
	Components     []PluginComponent     `json:"components"`
	NativeSource   string                `json:"native_source,omitempty"`
	CachePath      string                `json:"cache_path,omitempty"`
}

type PluginManifestSource struct {
	Identity string `json:"identity"`
	Settings string `json:"settings"`
	Layout   string `json:"layout"`
}
type PluginEvidence struct {
	Coverage     string            `json:"coverage,omitempty"`
	AssetID      string            `json:"asset_id"`
	Name         string            `json:"name"`
	Mode         string            `json:"mode"`
	Version      string            `json:"version"`
	NativeSource string            `json:"native_source"`
	Verified     bool              `json:"verified"`
	At           string            `json:"at"`
	Detail       string            `json:"detail"`
	Components   []PluginComponent `json:"components"`
}

func EqualPluginSelections(a, b []PluginSelection) bool {
	return slices.EqualFunc(a, b, func(x, y PluginSelection) bool {
		delivery := x.SkillAsset == nil && y.SkillAsset == nil || x.SkillAsset != nil && y.SkillAsset != nil && *x.SkillAsset == *y.SkillAsset
		manifest := x.ManifestSource == nil && y.ManifestSource == nil || x.ManifestSource != nil && y.ManifestSource != nil && *x.ManifestSource == *y.ManifestSource
		return delivery && manifest && equalPluginHooks(x.Hooks, y.Hooks) && x.AssetID == y.AssetID && x.Name == y.Name && x.Mode == y.Mode && x.Version == y.Version && x.SourcePath == y.SourcePath && x.SourceHash == y.SourceHash && x.RootMode == y.RootMode && x.NativeSource == y.NativeSource && x.CachePath == y.CachePath && slices.Equal(x.Files, y.Files) && slices.Equal(x.Components, y.Components)
	})
}
