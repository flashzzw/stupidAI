package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex-netcap/internal/codex"
)

func configurationAdoptionScopes() (Scope, Scope) {
	hash, next := strings.Repeat("a", 64), strings.Repeat("b", 64)
	settings := &ConfigurationSettings{Version: 1, ReasoningEffort: ConfigurationEffort{State: "set", Value: "low"}, EffortValueSHA256: hash, EffortSourcesSHA256: hash, OtherConfigurationSHA256: hash, OtherOriginsSHA256: hash, OtherLayersSHA256: hash}
	old := Scope{Instance: Instance{ID: "owned", Kind: "codex", Profile: "/profile"}, NativeID: "thread", Cwd: "/work", StateNamespace: "/state", CodexUserHooks: &CodexUserHookSource{Version: 1}, CodexProjectHooks: &CodexProjectHookSources{Version: 1, Files: []CodexProjectHookFile{{File: FileSource{Path: "/work/.codex/hooks.json", Kind: "absent"}, Declared: true, LayerVersion: "sha256:" + hash}}}, CodexConfigurationSources: &CodexConfigurationSources{Version: 1, Profile: "/profile", Cwd: "/work", ConfigurationData: ConfigurationData{Settings: settings, ConfigurationSHA256: hash, OriginsSHA256: hash, LayersSHA256: hash, Layers: []ConfigurationLayer{
		{ID: "user", Kind: "user", Version: "sha256:" + hash, File: &FileSource{Path: "/profile/config.toml", ResolvedPath: "/profile/config.toml", Kind: "file", SHA256: hash, Mode: 0600}},
		{ID: "project", Kind: "project", Version: "sha256:" + hash, File: &FileSource{Path: "/work/.codex/config.toml", ResolvedPath: "/work/.codex/config.toml", Kind: "file", SHA256: hash, Mode: 0600}},
		{ID: "system", Kind: "system", Version: "sha256:" + hash, File: &FileSource{Path: "/etc/codex/config.toml", ResolvedPath: "/etc/codex/config.toml", Kind: "absent"}},
	}}}}
	b, _ := json.Marshal(old)
	var target Scope
	_ = json.Unmarshal(b, &target)
	target.CodexConfigurationSources.ConfigurationSHA256 = next
	target.CodexConfigurationSources.Settings.ReasoningEffort.Value = "high"
	target.CodexConfigurationSources.Settings.EffortValueSHA256 = next
	target.CodexConfigurationSources.Layers[0].File.SHA256 = next
	return old, target
}

func TestCodexConfigurationAdoptionBoundary(t *testing.T) {
	for _, mode := range []string{"effort", "file-only", "project", "same", "other-value", "other-origin", "hidden-layer", "missing-settings", "unset-original", "unset-target", "unknown-target", "identity", "route", "state", "user-hook", "project-hook", "project-version", "file-path", "file-kind", "system-file", "layers"} {
		t.Run(mode, func(t *testing.T) {
			old, target := configurationAdoptionScopes()
			hash := strings.Repeat("c", 64)
			switch mode {
			case "file-only":
				copy := *old.CodexConfigurationSources.Settings
				target.CodexConfigurationSources.Settings = &copy
				target.CodexConfigurationSources.ConfigurationSHA256 = old.CodexConfigurationSources.ConfigurationSHA256
			case "project":
				target.CodexConfigurationSources.Layers[1].Version = "sha256:" + hash
				target.CodexProjectHooks.Files[0].LayerVersion = "sha256:" + hash
			case "same":
				target = old
			case "other-value":
				target.CodexConfigurationSources.Settings.OtherConfigurationSHA256 = hash
			case "other-origin":
				target.CodexConfigurationSources.Settings.OtherOriginsSHA256 = hash
			case "hidden-layer":
				target.CodexConfigurationSources.Settings.OtherLayersSHA256 = hash
			case "missing-settings":
				old.CodexConfigurationSources.Settings = nil
			case "unset-original":
				old.CodexConfigurationSources.Settings.ReasoningEffort = ConfigurationEffort{State: "unset"}
			case "unset-target":
				target.CodexConfigurationSources.Settings.ReasoningEffort = ConfigurationEffort{State: "unset"}
			case "unknown-target":
				target.CodexConfigurationSources.Settings.ReasoningEffort = ConfigurationEffort{State: "unrecognized"}
			case "identity":
				target.NativeID = "another"
			case "route":
				target.Route.Provider = "another"
			case "state":
				target.StateNamespace = "/other"
			case "user-hook":
				target.CodexUserHooks.File.SHA256 = hash
			case "project-hook":
				target.CodexProjectHooks.Files[0].File.Mode = 0640
			case "project-version":
				target.CodexProjectHooks.Files[0].LayerVersion = "sha256:" + hash
			case "file-path":
				target.CodexConfigurationSources.Layers[0].File.ResolvedPath = "/other"
			case "file-kind":
				target.CodexConfigurationSources.Layers[0].File.Kind = "absent"
			case "system-file":
				target.CodexConfigurationSources.Layers[2].File.Mode = 0640
			case "layers":
				target.CodexConfigurationSources.Layers = target.CodexConfigurationSources.Layers[:2]
			}
			want := mode == "effort" || mode == "file-only" || mode == "project"
			if got := CodexConfigurationSourceAdoption(old, target); got != want {
				t.Fatalf("adoption=%t want=%t", got, want)
			}
		})
	}
}

func TestCodexConfigurationAdoptionProofRequiresTarget(t *testing.T) {
	c, f, scope := nativeHarness(t)
	old, _ := configurationAdoptionScopes()
	old.Instance, old.Cwd, old.NativeID = scope.Instance, scope.Cwd, scope.NativeID
	// Proof target validation happens before any native mutation/read.
	proof := &CodexHookSourceProof{Version: 1, ScopeHash: codexHookSourceProofScope(old), Fingerprint: strings.Repeat("a", 64), RequirementsHash: strings.Repeat("a", 64), ConfigurationTargetHash: strings.Repeat("b", 64), At: "2026-10-07T00:00:00Z"}
	if err := c.verifyCodexHookSourceProof(context.Background(), f, old, proof); err == nil {
		t.Fatal("detached proof accepted")
	}
	if len(f.methods) > 0 {
		t.Fatal("unbound proof reached native")
	}
}

func TestCodexRecordedEffortRejectsLoadedMismatch(t *testing.T) {
	scope, _ := configurationAdoptionScopes()
	for _, status := range []string{"idle", "active", "notLoaded"} {
		thread := codex.Thread{Effort: "high"}
		thread.Status.Type = status
		if (verifyCodexEffort(scope, thread) == nil) != (status == "notLoaded") {
			t.Fatal("loaded mismatch accepted or unloaded treated as effective")
		}
	}
}

func TestCodexRecordedEffortRouteCheckWithoutCredential(t *testing.T) {
	_, f, _ := nativeHarness(t)
	scope, _ := configurationAdoptionScopes()
	f.call = func(method string, params map[string]any) (any, error) {
		if method != "thread/read" {
			t.Fatal("unexpected RPC", method)
		}
		return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "reasoningEffort": "high", "status": map[string]any{"type": "idle"}}}, nil
	}
	if verifyCodexThreadRoute(context.Background(), f, scope) == nil || len(f.methods) != 1 {
		t.Fatal("missing credential stamp bypassed recorded setting verification")
	}
}
