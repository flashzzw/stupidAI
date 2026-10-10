package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func packageFixture(t *testing.T, id, version string) CapabilityAsset {
	t.Helper()
	asset := libraryAsset(t, id, version, "managed")
	if err := os.WriteFile(filepath.Join(asset.Source, "SKILL.md"), []byte("---\nname: 'portable-method'\ndescription: |\n  Read the helper.\n  Keep the result local.\n---\n[Helper](helper.py)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return asset
}
func exportedPackage(t *testing.T, a *Application, id, version string) []byte {
	t.Helper()
	item := libraryRead(t, a, id)
	r := CapabilityPackageExportRequest{AssetID: id, Revision: item.Revision, Version: version}
	p, err := a.PreviewCapabilityPackageExport(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Diagnosis.Portable {
		t.Fatalf("unexpected nonportable diagnosis: %+v", p.Diagnosis)
	}
	r.PreviewHash = p.PreviewHash
	data, err := a.ExportCapabilityPackage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func packageCommand(t *testing.T, a *Application, key string, r CapabilityPackageImportRequest) Command {
	t.Helper()
	p, err := a.PreviewCapabilityPackageImport(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = p.PreviewHash
	return Command{ID: key, Kind: "import_capability_package", CapabilityPackage: &r}
}
func TestCapabilityPackageRoundTripVersionsAndRestart(t *testing.T) {
	origin := openFixture(t, fixtureConfig(t), newFixture())
	asset := packageFixture(t, "method", "v1")
	libraryChange(t, origin, "one", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	v1 := libraryRead(t, origin, asset.ID)
	next := packageFixture(t, "method", "v2")
	libraryChange(t, origin, "two", CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: v1.Revision, Asset: &next})
	zipV1 := exportedPackage(t, origin, "method", "v1")
	zipV2 := exportedPackage(t, origin, "method", "v2")
	if !bytes.Equal(zipV1, exportedPackage(t, origin, "method", "v1")) {
		t.Fatal("export not deterministic")
	}
	manifest, tree, err := decodeCapabilityPackage(zipV1)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Asset.Version != "v1" || !bytes.Equal(tree.files()["helper.py"], []byte("print('v1')\n")) {
		t.Fatal("historical export selected current bytes")
	}
	meta, _ := json.Marshal(manifest)
	if strings.Contains(string(meta), asset.Source) || strings.Contains(string(meta), "session_id") {
		t.Fatal("manifest includes runtime state")
	}
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	cmd := packageCommand(t, a, "import", CapabilityPackageImportRequest{Archive: zipV1, AssetID: "copy", Action: "register"})
	if len(a.Workspace().CapabilityAssets) != 0 {
		t.Fatal("preview mutated catalog")
	}
	first, err := a.Apply(cmd)
	if err != nil {
		t.Fatal(err)
	}
	imported := libraryRead(t, a, "copy")
	if imported.Asset.Source == v1.Asset.Source || imported.Asset.Version != "v1" || imported.Management != "managed" {
		t.Fatal("import did not create separate pinned version")
	}
	script := filepath.Join(imported.Asset.Source, "helper.py")
	info, err := os.Stat(script)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("executable permission lost: %v", err)
	}
	body, err := os.ReadFile(script)
	if err != nil || string(body) != "print('v1')\n" {
		t.Fatal("file content changed")
	}
	sid := createWithAsset(t, a, "use-copy", "copy")
	before, _ := a.ReadExecution(sid)
	cmd2 := packageCommand(t, a, "update", CapabilityPackageImportRequest{Archive: zipV2, AssetID: "copy", Action: "update", BaseRevision: imported.Revision})
	if _, err = a.Apply(cmd2); err != nil {
		t.Fatal(err)
	}
	after, _ := a.ReadExecution(sid)
	if digest(before.Snapshot) != digest(after.Snapshot) {
		t.Fatal("import update mutated execution")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	if got, err := a.Apply(cmd); err != nil || got != first {
		t.Fatal("lost durable import receipt")
	}
	current := libraryRead(t, a, "copy")
	if len(current.Versions) != 2 || current.Asset.Version != "v2" {
		t.Fatal("lost version history on restart")
	}
}
func TestCapabilityPackageConcurrentImportAndFailedPersistence(t *testing.T) {
	source := openFixture(t, fixtureConfig(t), newFixture())
	asset := packageFixture(t, "method", "v1")
	libraryChange(t, source, "one", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	data := exportedPackage(t, source, "method", "v1")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent", true: "persistence_failure"}[fail], func(t *testing.T) {
			c := fixtureConfig(t)
			a := openFixture(t, c, newFixture())
			cmd := packageCommand(t, a, "import", CapabilityPackageImportRequest{Archive: data, Action: "register"})
			if fail {
				store := &failingStore{Persistence: a.store}
				store.fail.Store(true)
				a.mu.Lock()
				a.store = store
				a.mu.Unlock()
			}
			var wg sync.WaitGroup
			var receipts [6]CommandReceipt
			for n := range receipts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := a.Apply(cmd)
					if (err != nil) != fail {
						t.Errorf("import error: %v", err)
					}
					receipts[n] = got
				}()
			}
			wg.Wait()
			paths, err := filepath.Glob(filepath.Join(c.StateDir, "capabilities", "versions", "*"))
			want := 1
			if fail {
				want = 0
			}
			if err != nil || len(paths) != want || len(a.Workspace().CapabilityAssets) != want {
				t.Fatalf("uncommitted resources remain: %v %v", paths, err)
			}
			if !fail {
				for _, got := range receipts {
					if got != receipts[0] {
						t.Fatal("duplicate receipts differ")
					}
				}
			}
		})
	}
}
func TestCapabilityPackageExportPreviewInvalidated(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	asset := packageFixture(t, "method", "v1")
	asset.Management = ""
	libraryChange(t, a, "one", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	for _, reason := range []string{"source", "usage", "revision"} {
		t.Run(reason, func(t *testing.T) {
			item := libraryRead(t, a, asset.ID)
			r := CapabilityPackageExportRequest{AssetID: asset.ID, Revision: item.Revision, Version: "v1"}
			p, err := a.PreviewCapabilityPackageExport(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			r.PreviewHash = p.PreviewHash
			switch reason {
			case "source":
				os.WriteFile(filepath.Join(asset.Source, "helper.py"), []byte("changed"), 0700)
			case "usage":
				createWithAsset(t, a, "use", asset.ID)
			case "revision":
				libraryChange(t, a, "disable", CapabilityChangeRequest{Action: "disable", AssetID: asset.ID, BaseRevision: item.Revision})
			}
			if _, err = a.ExportCapabilityPackage(context.Background(), r); err == nil {
				t.Fatal("stale export accepted")
			}
		})
	}
}

// Rebuild the ZIP to exercise validation using malformed input, without writing it to disk.
func alterPackage(t *testing.T, data []byte, mutate func(*zip.FileHeader, []byte) ([]byte, bool), extra string) []byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range r.File {
		stream, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		b.ReadFrom(stream)
		stream.Close()
		h := f.FileHeader
		body, keep := mutate(&h, b.Bytes())
		if !keep {
			continue
		}
		entry, err := w.CreateHeader(&h)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write(body)
	}
	if extra != "" {
		entry, err := w.Create(extra)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte("extra"))
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestCapabilityPackageRejectsMalformedArchivesBeforeWriting(t *testing.T) {
	origin := openFixture(t, fixtureConfig(t), newFixture())
	asset := packageFixture(t, "method", "v1")
	libraryChange(t, origin, "one", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	data := exportedPackage(t, origin, "method", "v1")
	for _, tc := range []string{"traversal", "absolute", "backslash", "duplicate", "case_collision", "symlink", "extra", "missing", "checksum", "mode", "undeclared_parent", "manifest_duplicate", "zip_bomb"} {
		t.Run(tc, func(t *testing.T) {
			extra := ""
			switch tc {
			case "extra":
				extra = "resources/extra"
			case "duplicate":
				extra = "resources/helper.py"
			case "case_collision":
				extra = "resources/HELPER.py"
			}
			bad := alterPackage(t, data, func(h *zip.FileHeader, body []byte) ([]byte, bool) {
				if h.Name == "resources/helper.py" {
					switch tc {
					case "traversal":
						h.Name = "resources/../escape"
					case "absolute":
						h.Name = "/tmp/escape"
					case "backslash":
						h.Name = `resources\escape`
					case "symlink":
						h.SetMode(os.ModeSymlink | 0777)
					case "missing":
						return body, false
					case "checksum":
						body = []byte("changed")
					case "mode":
						h.SetMode(0600)
					case "zip_bomb":
						body = bytes.Repeat([]byte("x"), int(capabilityMaxBytes)+1)
					}
				}
				if h.Name == "netcap-capability.json" {
					switch tc {
					case "undeclared_parent":
						var m CapabilityPackageManifest
						json.Unmarshal(body, &m)
						m.Entries = append(m.Entries, ResourceEntry{Path: "missing/dir", Kind: "directory", Mode: 0700})
						body, _ = json.Marshal(m)
					case "manifest_duplicate":
						body = bytes.Replace(body, []byte(`"format":`), []byte(`"format":"ignored","format":`), 1)
					}
				}
				return body, true
			}, extra)
			c := fixtureConfig(t)
			a := openFixture(t, c, newFixture())
			if _, err := a.PreviewCapabilityPackageImport(context.Background(), CapabilityPackageImportRequest{Archive: bad, Action: "register"}); err == nil {
				t.Fatal("unsafe archive preview accepted")
			}
			paths, _ := filepath.Glob(filepath.Join(c.StateDir, "capabilities", "versions", "*"))
			if len(paths) != 0 || len(a.Workspace().CapabilityAssets) != 0 {
				t.Fatal("invalid preview wrote state")
			}
		})
	}
}
func TestCapabilityDiagnosisSkillAndMCP(t *testing.T) {
	for _, tc := range []struct {
		name, kind, entry, body, code string
		portable                      bool
	}{
		{"yaml", "skill", "SKILL.md", "---\nname: method\ndescription: |\n  Multi\n  line\n---\n", "", true},
		{"duplicate_yaml", "skill", "SKILL.md", "---\nname: one\nname: two\ndescription: test\n---\n", "invalid_frontmatter", false},
		{"missing_metadata", "skill", "SKILL.md", "# test", "missing_skill_metadata", false},
		{"missing_script", "skill", "SKILL.md", "---\nname: method\ndescription: test\n---\n[helper](scripts/missing.py)", "missing_dependency", false},
		{"outside_script", "skill", "SKILL.md", "---\nname: method\ndescription: test\n---\n[helper](../outside.py)", "external_dependency", false},
		{"mcp_env", "mcp", "mcp.json", `{"mcpServers":{"test":{"url":"https://example.test/mcp","headers":{"Authorization":"Bearer ${TEST_TOKEN}"}}}}`, "", true},
		{"mcp_literal", "mcp", "mcp.json", `{"mcpServers":{"test":{"url":"https://example.test/mcp","headers":{"Authorization":"hidden-value"}}}}`, "sensitive_content", false},
		{"mcp_outside", "mcp", "mcp.json", `{"mcpServers":{"test":{"command":"python3","args":["../private.py"]}}}`, "external_dependency", false},
		{"mcp_helper_binding", "mcp", "mcp.json", `{"mcpServers":{"test":{"type":"http","url":"https://example.test/mcp","headersHelper":"./project-auth.sh ${ACCOUNT}"}}}`, "runtime_binding", true},
		{"mcp_helper_invalid", "mcp", "mcp.json", `{"mcpServers":{"test":{"type":"http","url":"https://example.test/mcp","headersHelper":42}}}`, "invalid_mcp_headers_helper", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, tc.entry), []byte(tc.body), 0600)
			asset := CapabilityAsset{Kind: tc.kind, Source: dir, Backends: []string{"codex", "claude"}}
			tree, _, err := readCapabilityResources(asset)
			if err != nil {
				t.Fatal(err)
			}
			d := diagnoseResources(asset, tree)
			if d.NativeStatus != "unverified" || d.Portable != tc.portable {
				t.Fatalf("diagnosis: %+v", d)
			}
			if tc.code != "" {
				found := false
				for _, i := range d.Issues {
					found = found || i.Code == tc.code
				}
				if !found {
					t.Fatalf("missing %s: %+v", tc.code, d)
				}
			}
			if tc.name == "mcp_env" && strings.Join(d.RequiredEnvironment, ",") != "TEST_TOKEN" {
				t.Fatal("environment not listed")
			}
			encoded, _ := json.Marshal(d)
			if bytes.Contains(encoded, []byte("hidden-value")) {
				t.Fatal("diagnostics leaked credential")
			}
		})
	}
}
func TestCapabilityExportRejectsCaseCollisions(t *testing.T) {
	asset := packageFixture(t, "method", "v1")
	os.WriteFile(filepath.Join(asset.Source, "HELPER.py"), []byte("case collision"), 0600)
	if _, _, err := readCapabilityResources(asset); err == nil {
		t.Fatal("nonportable case collision accepted")
	}
}

func TestCapabilityPluginDiagnosticsComponentsAndBoundaries(t *testing.T) {
	for _, backend := range []string{"codex", "claude"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"." + backend + "-plugin/plugin.json": `{"name":"fixture-plugin","skills":["./custom"],"mcpServers":"./more.json"}`,
				"skills/default/SKILL.md":             "---\nname: default\ndescription: default method\n---\n",
				"custom/extra/SKILL.md":               "---\nname: custom\ndescription: custom method\n---\n[tool](../../scripts/tool.py)\n",
				".mcp.json":                           `{"mcpServers":{"same":{"command":"python3","args":["./scripts/tool.py"]}}}`,
				"more.json":                           `{"mcpServers":{"same":{"url":"https://example.test/mcp"}}}`,
				"hooks/hooks.json":                    `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"python3 ${CLAUDE_PLUGIN_ROOT}/scripts/tool.py"}]}]}}`,
				"scripts/tool.py":                     "print('local')\n",
			}
			for p, body := range files {
				target := filepath.Join(dir, p)
				os.MkdirAll(filepath.Dir(target), 0700)
				os.WriteFile(target, []byte(body), 0600)
			}
			asset := CapabilityAsset{Kind: "cli_plugin", Source: dir, Backends: []string{backend}}
			tree, _, err := readCapabilityResources(asset)
			if err != nil {
				t.Fatal(err)
			}
			d := diagnoseResources(asset, tree)
			if !d.Portable || d.Status != "static_checked" {
				t.Fatalf("valid plugin: %+v", d)
			}
			found := map[string]bool{}
			for _, c := range d.Components {
				found[c.Kind+":"+c.Name] = true
				if c.IndependentSwitch {
					t.Fatal("plugin component gained independent switch")
				}
			}
			if !found["skill:custom"] || !found["mcp:same"] || !found["hook:SessionStart"] || found["skill:default"] != (backend == "claude") {
				t.Fatalf("component defaults/overrides: %+v", found)
			}
			duplicate := false
			for _, issue := range d.Issues {
				duplicate = duplicate || issue.Code == "duplicate_mcp"
			}
			if !duplicate {
				t.Fatal("duplicate MCP hidden")
			}
			for range 20 {
				if digest(d) != digest(diagnoseResources(asset, tree)) {
					t.Fatal("unstable diagnosis changes preview")
				}
			}
			os.Remove(filepath.Join(dir, "scripts/tool.py"))
			tree, _, err = readCapabilityResources(asset)
			if err != nil {
				t.Fatal(err)
			}
			if diagnoseResources(asset, tree).Status != "invalid" {
				t.Fatal("missing plugin script hidden")
			}
		})
	}
}
func TestCapabilityPackageRejectsAccountStateAndLiteralCredentials(t *testing.T) {
	for _, name := range []string{"auth.json", "settings.json", "config.toml", "helper.py"} {
		t.Run(name, func(t *testing.T) {
			a := openFixture(t, fixtureConfig(t), newFixture())
			asset := packageFixture(t, "method", "v1")
			asset.Management = ""
			body := []byte("not a resource\n")
			if name == "helper.py" {
				body = []byte(`secret = "fixture-private-value"`)
			}
			os.WriteFile(filepath.Join(asset.Source, name), body, 0600)
			libraryChange(t, a, "one", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
			item := libraryRead(t, a, asset.ID)
			r := CapabilityPackageExportRequest{AssetID: asset.ID, Revision: item.Revision, Version: "v1"}
			p, err := a.PreviewCapabilityPackageExport(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if p.Diagnosis.Portable {
				t.Fatal("unsafe source declared portable")
			}
			r.PreviewHash = p.PreviewHash
			if data, err := a.ExportCapabilityPackage(context.Background(), r); err == nil || len(data) > 0 || strings.Contains(err.Error(), "fixture-private-value") {
				t.Fatal("unsafe export or value leaked")
			}
			// A hand-crafted ZIP goes through the same content checks when imported.
			tree, _, err := readCapabilityResources(asset)
			if err != nil {
				t.Fatal(err)
			}
			data, err := encodeCapabilityPackage(CapabilityPackageManifest{Format: capabilityPackageFormat, Asset: packageAsset(asset), RootName: tree.Name, RootKind: tree.Kind, RootMode: tree.Mode, Entries: tree.Entries}, tree)
			if err != nil {
				t.Fatal(err)
			}
			dest := openFixture(t, fixtureConfig(t), newFixture())
			cmd := packageCommand(t, dest, "import", CapabilityPackageImportRequest{Archive: data, Action: "register"})
			if _, err = dest.Apply(cmd); err == nil || len(dest.Workspace().CapabilityAssets) > 0 {
				t.Fatal("unsafe archive imported")
			}
		})
	}
}
func TestCapabilityProfileReferenceDiagnosisRemainsExternal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	os.WriteFile(p, []byte("[mcp_servers.fixture]\ncommand='python3'\n"), 0600)
	asset := CapabilityAsset{Kind: "mcp", Source: p, CodexMCP: "fixture", Backends: []string{"codex"}}
	tree, _, err := readCapabilityResources(asset)
	if err != nil {
		t.Fatal(err)
	}
	d := diagnoseResources(asset, tree)
	if d.Status == "invalid" || d.Portable || d.Format != "codex_profile_reference" {
		t.Fatalf("external mapping diagnosis: %+v", d)
	}
}
