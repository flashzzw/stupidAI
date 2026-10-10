package runtime

// This is evidence about a conditional native callback, not a full settings
// freeze or an authorization to bypass file-source admission checks.
type ClaudeSettingsGuardEvidence struct {
	Version int                    `json:"version"`
	State   string                 `json:"state"`
	Detail  string                 `json:"detail"`
	Changes []ClaudeSettingsChange `json:"changes"`
}

type ClaudeSettingsChange struct {
	Source   string `json:"source"`
	Path     string `json:"path"`
	At       string `json:"at"`
	Decision string `json:"decision"`
}
