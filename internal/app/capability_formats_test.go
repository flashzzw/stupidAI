package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func formatAsset(t *testing.T, files map[string][]byte) CapabilityAsset {
	t.Helper()
	asset := CapabilityAsset{ID: "portable", Name: "Portable plugin", Kind: "cli_plugin", Version: "v1", Backends: []string{"codex"}, Source: t.TempDir(), Management: "managed"}
	for name, data := range files {
		p := filepath.Join(asset.Source, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return asset
}
func formatDiagnosis(t *testing.T, asset CapabilityAsset) CapabilityDiagnosis {
	t.Helper()
	tree, _, err := readCapabilityResources(asset)
	if err != nil {
		t.Fatal(err)
	}
	return diagnoseResources(asset, tree)
}
func TestPortablePluginCanonicalRootAndOverlayPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, root, overlay, hook string
		valid                     bool
	}{
		{"inline_replaces_overlay", `{"name":"root-name","extensions":{"com.openai":{"hooks":"./inline.json"}}}`, `{"name":"ignored-name","hooks":"./missing.json","skills":"./missing-skills"}`, "Inline", true},
		{"empty_inline_replaces_overlay", `{"name":"root-name","extensions":{"com.openai":{}}}`, `{"name":"ignored-name","hooks":"./missing.json"}`, "Default", true},
		{"fallback_overlay", `{"name":"root-name"}`, `{"name":"ignored-name","hooks":"./overlay.json","skills":"./missing-skills"}`, "Overlay", true},
		{"no_overlay", `{"name":"root-name"}`, "", "Default", true},
		{"inline_array", `{"name":"root-name","extensions":{"com.openai":{"hooks":[{"hooks":{"Inline":[{"hooks":[{"type":"command","command":"echo fixture"}]}]}}]}}}`, "", "Inline", true},
		{"wrong_inline_type", `{"name":"root-name","extensions":{"com.openai":42}}`, `{"name":"ignored-name"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{"plugin.json": []byte(tc.root), "skills/method/SKILL.md": []byte("---\nname: canonical\ndescription: method\n---\n"), "mcp.json": []byte(`{"mcpServers":{"canonical":{"type":"streamable-http","url":"https://example.test/mcp"}}}`), ".mcp.json": []byte(`{"mcpServers":{"legacy":{"command":"python3","args":["../missing.py"]}}}`)}
			for filename, event := range map[string]string{"hooks/hooks.json": "Default", "inline.json": "Inline", "overlay.json": "Overlay"} {
				files[filename] = []byte(`{"hooks":{"` + event + `":[{"hooks":[{"type":"command","command":"echo fixture"}]}]}}`)
			}
			if tc.overlay != "" {
				files[".codex-plugin/plugin.json"] = []byte(tc.overlay)
			}
			d := formatDiagnosis(t, formatAsset(t, files))
			if d.Portable != tc.valid || (d.Status != "invalid") != tc.valid {
				t.Fatalf("unexpected diagnosis: %+v", d)
			}
			if !tc.valid {
				return
			}
			found := map[string]bool{}
			for _, c := range d.Components {
				found[c.Kind+":"+c.Name] = true
			}
			if !found["plugin:root-name"] || !found["skill:canonical"] || !found["mcp:canonical"] || found["mcp:legacy"] || found["plugin:ignored-name"] {
				t.Fatalf("canonical components lost: %+v", found)
			}
			for _, hook := range []string{"Default", "Inline", "Overlay"} {
				if found["hook:"+hook] != (hook == tc.hook) {
					t.Fatalf("wrong hooks: %+v", found)
				}
			}
			if d.NativeStatus != "unverified" {
				t.Fatal("static parsing claimed native load")
			}
			for range 10 {
				if digest(d) != digest(formatDiagnosis(t, formatAsset(t, files))) {
					t.Fatal("unstable component diagnosis")
				}
			}
		})
	}
}
func TestPortablePluginMissingDeclaredResourceAndMCPTransport(t *testing.T) {
	for _, tc := range []struct{ name, root, mcp, code string }{
		{"icon", `{"name":"root","extensions":{"com.openai":{"interface":{"logo":"./assets/missing.png"}}}}`, "", "missing_dependency"},
		{"hook", `{"name":"root","extensions":{"com.openai":{"hooks":"../outside.json"}}}`, "", "invalid_component_path"},
		{"apps", `{"name":"root","extensions":{"com.openai":{"apps":"./.app.json"}}}`, "", "missing_component"},
		{"transport", `{"name":"root"}`, `{"mcpServers":{"test":{"url":"https://example.test/mcp"}}}`, "invalid_mcp_transport"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{"plugin.json": []byte(tc.root)}
			if tc.mcp != "" {
				files["mcp.json"] = []byte(tc.mcp)
			}
			d := formatDiagnosis(t, formatAsset(t, files))
			found := false
			for _, issue := range d.Issues {
				found = found || issue.Code == tc.code
			}
			if d.Portable || !found {
				t.Fatalf("missing %s: %+v", tc.code, d)
			}
		})
	}
}
func TestCodexExplicitHooksReplaceDefaultForLegacyPlugin(t *testing.T) {
	files := map[string][]byte{".codex-plugin/plugin.json": []byte(`{"name":"legacy","hooks":"./custom.json"}`), "custom.json": []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo fixture"}]}]}}`), "hooks/hooks.json": []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"./missing.py"}]}]}}`)}
	d := formatDiagnosis(t, formatAsset(t, files))
	if !d.Portable {
		t.Fatalf("ignored default poisoned legacy config: %+v", d)
	}
	for _, c := range d.Components {
		if c.Kind == "hook" && c.Name == "Stop" {
			t.Fatal("ignored hook included")
		}
	}
}
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aL9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestCapabilityBinaryResourcesRoundTripAndRemainPassive(t *testing.T) {
	files := map[string][]byte{"plugin.json": []byte(`{"name":"portable","extensions":{"com.openai":{"interface":{"logo":"./assets/icon.png"}}}}`), "assets/icon.png": tinyPNG(t), "scripts/module.wasm": {0, 97, 115, 109, 1, 0, 0, 0}, "assets/sample.bin": {0xff, 0xfe, 0x01, 0x02}}
	a := openFixture(t, fixtureConfig(t), newFixture())
	asset := formatAsset(t, files)
	os.Chmod(filepath.Join(asset.Source, "scripts/module.wasm"), 0700)
	libraryChange(t, a, "register", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	data := exportedPackage(t, a, asset.ID, "v1")
	dest := openFixture(t, fixtureConfig(t), newFixture())
	cmd := packageCommand(t, dest, "import", CapabilityPackageImportRequest{Archive: data, AssetID: "copy", Action: "register"})
	if _, err := dest.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	item := libraryRead(t, dest, "copy")
	for p, want := range files {
		actual, err := os.ReadFile(filepath.Join(item.Asset.Source, p))
		if err != nil || !bytes.Equal(actual, want) {
			t.Fatalf("resource changed: %s %v", p, err)
		}
	}
	info, err := os.Stat(filepath.Join(item.Asset.Source, "scripts/module.wasm"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("binary permission changed")
	}
	if len(dest.Snapshot().Sessions) > 0 {
		t.Fatal("resource import launched an execution")
	}
	p, err := dest.PreviewCapabilityPackageExport(context.Background(), CapabilityPackageExportRequest{AssetID: "copy", Revision: item.Revision, Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(p.Diagnosis)
	if !strings.Contains(string(encoded), "image/png") || !strings.Contains(string(encoded), "application/wasm") {
		t.Fatalf("binary classification absent: %s", encoded)
	}
}
func TestBinaryAndArchiveContentStillRejectCredentialsAndAccountState(t *testing.T) {
	var nested bytes.Buffer
	w := zip.NewWriter(&nested)
	f, _ := w.Create("auth.json")
	f.Write([]byte(`{"api_key":"nested-private-value"}`))
	w.Close()
	for _, data := range [][]byte{append(tinyPNG(t), []byte(`secret = "embedded-private-value"`)...), []byte("SQLite format 3\x00hidden state"), nested.Bytes()} {
		asset := formatAsset(t, map[string][]byte{"plugin.json": []byte(`{"name":"portable"}`), "assets/data.bin": data})
		d := formatDiagnosis(t, asset)
		if d.Portable {
			t.Fatal("binary content bypassed credential/state checks")
		}
		encoded, _ := json.Marshal(d)
		if strings.Contains(string(encoded), "private-value") {
			t.Fatal("diagnostics leaked secret")
		}
	}
}

func TestCapabilityCompressedResourceInspectionBounds(t *testing.T) {
	gzipData := func(name string, data []byte) []byte {
		var out bytes.Buffer
		w := gzip.NewWriter(&out)
		w.Name = name
		w.Write(data)
		w.Close()
		return out.Bytes()
	}
	tarData := func(name string, data []byte) []byte {
		var out bytes.Buffer
		w := tar.NewWriter(&out)
		w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Format: tar.FormatUSTAR})
		w.Write(data)
		w.Close()
		return out.Bytes()
	}
	zipData := func(name string, data []byte) []byte {
		var out bytes.Buffer
		w := zip.NewWriter(&out)
		f, _ := w.Create(name)
		f.Write(data)
		w.Close()
		return out.Bytes()
	}
	cases := []struct {
		name     string
		data     []byte
		portable bool
	}{
		{"clean_zip", zipData("fixture.bin", []byte{0, 1, 2}), true},
		{"clean_tar_gz", gzipData("fixture.tar", tarData("fixture.bin", []byte{0, 1, 2})), true},
		{"renamed_account_in_zip", zipData("sessions/history.txt", []byte("history")), false},
		{"account_in_tar", tarData("auth.json", []byte("account")), false},
		{"account_in_gzip", gzipData("auth.json", []byte("account")), false},
		{"second_gzip_member_account", append(gzipData("first.txt", []byte("fixture")), gzipData("auth.json", []byte("account"))...), false},
		{"zip_traversal", zipData("../outside", []byte("fixture")), false},
		{"zip_bomb", zipData("too-large", bytes.Repeat([]byte("a"), int(capabilityMaxBytes)+1)), false},
		{"gzip_bomb", gzipData("too-large", bytes.Repeat([]byte("a"), int(capabilityMaxBytes)+1)), false},
		{"nested_too_deep", zipData("1.zip", zipData("2.zip", zipData("3.zip", zipData("data", []byte("fixture"))))), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asset := formatAsset(t, map[string][]byte{"plugin.json": []byte(`{"name":"portable"}`), "assets/content": tc.data})
			d := formatDiagnosis(t, asset)
			if d.Portable != tc.portable {
				t.Fatalf("unexpected portability: %+v", d)
			}
		})
	}
}
func TestPortablePluginAppMappingsAndOnlySelectedEnvironment(t *testing.T) {
	asset := formatAsset(t, map[string][]byte{
		"plugin.json":               []byte(`{"name":"portable","extensions":{"com.openai":{"apps":"./.app.json"}}}`),
		".app.json":                 []byte(`{"apps":{"fixture":{"id":"plugin_asdk_app_fixture"}}}`),
		".codex-plugin/plugin.json": []byte(`{"name":"legacy","hooks":{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo ${IGNORED_ENV}"}]}]}}}`),
		".mcp.json":                 []byte(`{"mcpServers":{"ignored":{"url":"https://example.test/mcp","headers":{"Authorization":"${IGNORED_TOKEN}"}}}}`),
		"mcp.json":                  []byte(`{"mcpServers":{"active":{"type":"streamable-http","url":"https://example.test/mcp","headers":{"Authorization":"${ACTIVE_TOKEN}"}}}}`),
	})
	d := formatDiagnosis(t, asset)
	if !d.Portable || strings.Join(d.RequiredEnvironment, ",") != "ACTIVE_TOKEN" {
		t.Fatalf("ignored definitions leaked into runtime requirements: %+v", d)
	}
	found := false
	for _, c := range d.Components {
		found = found || c.Kind == "registered_mcp" && c.Name == "fixture"
	}
	if !found {
		t.Fatal("app mapping omitted")
	}
}

func TestCapabilityUTF16CredentialDetectionPreservesValuePrivacy(t *testing.T) {
	text := `{"api_key":"utf16-private-value"}`
	for _, little := range []bool{true, false} {
		data := []byte{0xfe, 0xff}
		if little {
			data = []byte{0xff, 0xfe}
		}
		for _, r := range text {
			if little {
				data = append(data, byte(r), 0)
			} else {
				data = append(data, 0, byte(r))
			}
		}
		d := formatDiagnosis(t, formatAsset(t, map[string][]byte{"plugin.json": []byte(`{"name":"portable"}`), "assets/data.bin": data}))
		encoded, _ := json.Marshal(d)
		if d.Portable || strings.Contains(string(encoded), "utf16-private-value") {
			t.Fatal("UTF-16 credential accepted or disclosed")
		}
	}
}

func TestPluginRuntimeDataPlaceholdersRemainPortableWithoutCopyingState(t *testing.T) {
	for _, variable := range []string{"PLUGIN_DATA", "CLAUDE_PLUGIN_DATA"} {
		asset := formatAsset(t, map[string][]byte{"plugin.json": []byte(`{"name":"portable","extensions":{"com.openai":{"hooks":{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo ${` + variable + `}"}]}]}}}}}`)})
		d := formatDiagnosis(t, asset)
		if !d.Portable || len(d.RequiredEnvironment) != 0 {
			t.Fatalf("CLI-owned runtime data treated as packaged state or user credential: %+v", d)
		}
		found := false
		for _, issue := range d.Issues {
			found = found || issue.Code == "runtime_binding"
		}
		if !found {
			t.Fatal("runtime binding not explained")
		}
	}
}
