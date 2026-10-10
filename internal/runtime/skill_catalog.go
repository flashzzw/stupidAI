package runtime

import "context"

// A source catalog samples one existing native query. It is separate from
// directory discovery and from proof that a Skill body was loaded or used.
type SkillSourceCatalog struct {
	NativeID     string                   `json:"native_id"`
	Cwd          string                   `json:"cwd"`
	Source       string                   `json:"source"`
	At           string                   `json:"at"`
	Complete     bool                     `json:"complete"`
	Items        []SkillSourceItem        `json:"items"`
	Limitations  []string                 `json:"limitations"`
	Instructions *InstructionSourceSample `json:"instructions,omitempty"`
}

type InstructionSourceSample struct {
	Available bool                    `json:"available"`
	Items     []InstructionSourceItem `json:"items"`
}

type InstructionSourceItem struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Tokens int64  `json:"tokens"`
}

type SkillSourceItem struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Source            string   `json:"source"`
	PluginName        string   `json:"plugin_name,omitempty"`
	Aliases           []string `json:"aliases"`
	CommandAdvertised bool     `json:"command_advertised"`
	DescriptionListed bool     `json:"description_listed"`
	Builtin           bool     `json:"builtin"`
	Ambiguous         bool     `json:"ambiguous"`
	Control           string   `json:"control"`
	Detail            string   `json:"detail"`
	AssetID           string   `json:"asset_id,omitempty"`
	BoundPath         string   `json:"bound_path,omitempty"`
	ConfiguredMode    string   `json:"configured_mode,omitempty"`
}

type SkillCatalogReader interface {
	ReadSkillCatalog(context.Context, Scope) (SkillSourceCatalog, error)
}

func (a *Adapters) ReadSkillCatalog(ctx context.Context, scope Scope) (SkillSourceCatalog, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return SkillSourceCatalog{}, err
	}
	reader, ok := r.(SkillCatalogReader)
	if !ok {
		return SkillSourceCatalog{}, unsupportedSkillCatalog()
	}
	return reader.ReadSkillCatalog(ctx, scope)
}
