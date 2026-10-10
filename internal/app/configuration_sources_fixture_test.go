package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex-netcap/internal/runtime"
)

// Application fixtures attest only their owned user file; real layer merging
// and native admission are covered by Runtime and fixed-native tests.
func (f *fixtureRuntime) ReadCodexConfigurationSources(_ context.Context, scope runtime.Scope) (*runtime.CodexConfigurationSources, error) {
	path := filepath.Join(scope.Instance.Profile, "config.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	h := fmt.Sprintf("%x", sha256.Sum256(body))
	identity := strings.Repeat("1", 64)
	return &runtime.CodexConfigurationSources{Version: 1, Profile: scope.Instance.Profile, Cwd: scope.Cwd, ConfigurationData: runtime.ConfigurationData{
		ConfigurationSHA256: h, OriginsSHA256: identity, LayersSHA256: h, Origins: []runtime.ConfigurationOrigin{},
		Layers: []runtime.ConfigurationLayer{{ID: identity, Kind: "user", Version: "sha256:" + h, SHA256: h, ConfigurationSHA256: h, File: &runtime.FileSource{Path: path, ResolvedPath: resolved, Kind: "file", SHA256: h, Mode: uint32(info.Mode())}}},
	}}, nil
}
