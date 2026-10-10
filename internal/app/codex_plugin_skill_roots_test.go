package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestCodexPluginSkillRootsKeepDeclaredPathsAndExcludeDefaults(t *testing.T) {
	for _, roots := range []string{`"./methods"`, `["./methods"]`} {
		t.Run(roots, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			root := cfg.CapabilityAssets[0].Source
			files := map[string]string{
				".codex-plugin/plugin.json":      `{"name":"fixture-plugin","version":"1.2.3","skills":` + roots + `}`,
				"methods/grouped-entry/SKILL.md": "---\nname: method\ndescription: Custom path method\n---\nRead [guide](../../references/guide.md).\n",
				"references/guide.md":            "Owned relative guide.\n",
				"skills/default/SKILL.md":        "---\nname: default\ndescription: Must not be selected\n---\nDEFAULT_BODY\n",
			}
			for name, body := range files {
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a := openFixture(t, cfg, newFixture())
			r, err := a.Apply(Command{ID: "custom-skill", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
			if err != nil {
				t.Fatal(err)
			}
			read, err := a.ReadExecution(r.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			pack := read.Snapshot.PluginSelections[0]
			if len(pack.Components) != 2 {
				t.Fatalf("wrong declared components: %+v", pack.Components)
			}
			var method runtime.PluginComponent
			for _, c := range pack.Components {
				if c.Kind == "skill" {
					method = c
				}
			}
			if method.Name != "method" || method.NativeName != "fixture-plugin:method" || method.Path != "methods/grouped-entry/SKILL.md" {
				t.Fatalf("lost declared method path: %+v", method)
			}
			scope := runtime.Scope{Instance: read.Snapshot.Instance, SessionID: r.SessionID, PluginSelections: []runtime.PluginSelection{pack}}
			bound, err := runtime.ResolveCodexSkillInputs(scope, []runtime.SkillInput{{AssetID: "plugin", Name: "fixture-plugin:method"}})
			if err != nil || len(bound) != 1 || bound[0].Path != filepath.Join(pack.CachePath, "methods/grouped-entry/SKILL.md") {
				t.Fatalf("wrong explicit path: %+v %v", bound, err)
			}
			if _, err := runtime.ResolveCodexSkillInputs(scope, []runtime.SkillInput{{AssetID: "plugin", Name: "fixture-plugin:default"}}); err == nil {
				t.Fatal("replaced default Skill still selectable")
			}
		})
	}
}

func TestCodexPluginSkillRootsRejectAmbiguousDeclarations(t *testing.T) {
	for name, roots := range map[string]string{"empty-string": `""`, "empty-array": `[]`, "file-path": `"./skills/default/SKILL.md"`, "outside": `"../outside"`, "missing-directory": `"./missing"`} {
		t.Run(name, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			root := cfg.CapabilityAssets[0].Source
			p := filepath.Join(root, "skills/default/SKILL.md")
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("---\nname: default\ndescription: default method\n---\nDefault body\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".codex-plugin/plugin.json"), []byte(`{"name":"fixture-plugin","version":"1.2.3","skills":`+roots+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			f := newFixture()
			a := openFixture(t, cfg, f)
			_, err := a.Apply(Command{ID: "invalid-root", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
			if err == nil || f.creates.Load() != 0 {
				t.Fatal("ambiguous or non-directory Skill root was dispatched")
			}
		})
	}
}
