package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestConsoleConfigurationIsIndependentAndRestrictsBinding(t *testing.T) {
	root := t.TempDir()
	base := map[string]any{"version": 1, "state_dir": "console-state", "listen": "127.0.0.1:0",
		"projects":  []any{map[string]any{"id": "project", "cwd": root}},
		"instances": []any{map[string]any{"id": "codex", "kind": "codex", "executable": "/bin/false", "profile": "profile", "socket": "test.sock", "policy_revision": "1"}},
		"routes":    []any{map[string]any{"id": "route", "instance_id": "codex", "provider": "fixture", "endpoint": "http://127.0.0.1:9010/v1", "model": "fixture", "credential_ref": "profile:fixture", "credential_revision": "1", "config_revision": "1"}}}
	path := filepath.Join(root, "console.json")
	write := func() {
		t.Helper()
		b, _ := json.Marshal(base)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != filepath.Join(root, "console-state") || c.Instances[0].Profile != filepath.Join(root, "profile") {
		t.Fatal("relative paths were not resolved against config directory")
	}
	asset := map[string]any{"id": "shared", "name": "Shared", "kind": "mcp", "version": "1", "source": "assets/mcp.json", "backends": []string{"codex", "claude"}}
	base["capability_assets"] = []any{asset}
	write()
	c, err = LoadConfig(path)
	if err != nil || c.CapabilityAssets[0].Source != filepath.Join(root, "assets/mcp.json") {
		t.Fatalf("asset registration path was not resolved: %+v %v", c.CapabilityAssets, err)
	}
	base["capability_assets"] = []any{asset, asset}
	write()
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("duplicate asset identity accepted")
	}
	delete(base, "capability_assets")
	base["weather_script"] = "missing.py"
	write()
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("legacy/weather field silently accepted")
	}
	delete(base, "weather_script")
	base["listen"] = "0.0.0.0:9000"
	write()
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("public binding accepted")
	}
	if _, err = LoadConfig(""); err == nil {
		t.Fatal("serve inherited a default profile")
	}
}

func TestOpenCodeSchemaAnnotationIsNotConfigurationDrift(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "opencode.json")
	instance := runtime.Instance{Kind: "opencode", Profile: dir}
	if err := os.WriteFile(file, []byte(`{"permission":"ask","model":"fixture/first"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := profileFingerprint(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("{\n\"$schema\":\"https://opencode.ai/config.json\",\n\"model\":\"fixture/first\",\"permission\":\"ask\"\n}"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := profileFingerprint(instance)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("native schema annotation/formatting invalidated the session")
	}
	if err = os.WriteFile(file, []byte(`{"permission":"ask","model":"fixture/second"}`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := profileFingerprint(instance)
	if err != nil {
		t.Fatal(err)
	}
	if changed == before {
		t.Fatal("actual model configuration change was ignored")
	}
}
