package runtime

import "context"

// Skill discovery samples directory/profile configuration, not a thread's
// loaded instructions or method usage. Keep it separate from execution evidence.
type SkillInventory struct {
	Cwd      string            `json:"cwd"`
	Source   string            `json:"source"`
	At       string            `json:"at"`
	Complete bool              `json:"complete"`
	Skills   []DiscoveredSkill `json:"skills"`
	Errors   []SkillScanError  `json:"errors"`
}

type DiscoveredSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	PluginID    string `json:"plugin_id,omitempty"`
}

type SkillScanError struct {
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

type SkillDiscoverer interface {
	DiscoverSkills(context.Context, Scope) (SkillInventory, error)
}

// The native key and advertised name are different identifiers in Claude.
// DiscoveryPath binds a profile/project entry (possibly a shared symlink) to
// the immutable asset source, rather than assuming the displayed name is a key.
type SkillSelection struct {
	AssetID        string               `json:"asset_id"`
	Name           string               `json:"name"`
	NativeKey      string               `json:"native_key"`
	Mode           string               `json:"mode"`
	Source         string               `json:"source"`
	SourcePath     string               `json:"source_path"`
	DiscoveryPath  string               `json:"discovery_path"`
	Delivery       string               `json:"delivery,omitempty"`
	PluginName     string               `json:"plugin_name,omitempty"`
	ResourceRoot   string               `json:"resource_root,omitempty"`
	ResourceKind   string               `json:"resource_kind,omitempty"`
	ResourceHash   string               `json:"resource_hash,omitempty"`
	CodexDirectory *CodexDirectorySkill `json:"codex_directory,omitempty"`
}

type SkillEvidence struct {
	AssetID           string `json:"asset_id"`
	Name              string `json:"name"`
	NativeKey         string `json:"native_key"`
	Mode              string `json:"mode"`
	Source            string `json:"source"`
	CommandAdvertised bool   `json:"command_advertised"`
	DescriptionListed bool   `json:"description_listed"`
	Verified          bool   `json:"verified"`
	At                string `json:"at"`
	Detail            string `json:"detail"`
	Coverage          string `json:"coverage,omitempty"`
}
