package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func sharedSkillScope(t *testing.T, kind string) Scope {
	t.Helper()
	s := codexPluginScope(t)
	s.Cwd, s.StateNamespace = t.TempDir(), s.Instance.Profile
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("---\nname: method\ndescription: Shared method\n---\nOwned shared method.\n")
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), body, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	source := PluginSelection{AssetID: "shared", Name: "method", Mode: "enabled", SourcePath: root, SourceHash: hex.EncodeToString(hash[:]), RootMode: 0700, Files: []PluginFile{{Path: "SKILL.md", Kind: "file", Mode: 0600, SHA256: hex.EncodeToString(hash[:])}}}
	if kind == "file" {
		source.SourcePath = filepath.Join(root, "SKILL.md")
		source.RootMode = 0600
	}
	p, err := BindCodexSkillAsset(s, source, kind)
	if err != nil {
		t.Fatal(err)
	}
	s.PluginSelections = []PluginSelection{p}
	return s
}

func TestCodexSharedSkillMaterializesOriginalBytesAndRejectsNativeAliases(t *testing.T) {
	for _, mode := range []string{"file", "directory", "native-entry", "native-alias", "native-hardlink", "incomplete-discovery", "source-drift", "nested-skill"} {
		t.Run(mode, func(t *testing.T) {
			kind := "directory"
			if mode == "file" {
				kind = "file"
			}
			s := sharedSkillScope(t, kind)
			pack := s.PluginSelections[0]
			entry := pack.SourcePath
			if kind == "directory" {
				entry = filepath.Join(entry, "SKILL.md")
			}
			body, err := os.ReadFile(entry)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "source-drift" {
				if err := os.WriteFile(entry, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "nested-skill" {
				if err := os.Mkdir(filepath.Join(pack.SourcePath, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(pack.SourcePath, "nested/SKILL.md"), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			c, f, _ := nativeHarness(t)
			started := 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
				case "skills/list":
					rows, problems := []any{}, []any{}
					if mode == "native-entry" || mode == "native-alias" || mode == "native-hardlink" {
						found := entry
						if mode == "native-alias" {
							found = filepath.Join(t.TempDir(), "SKILL.md")
							if err := os.Symlink(entry, found); err != nil {
								t.Fatal(err)
							}
						}
						if mode == "native-hardlink" {
							found = filepath.Join(t.TempDir(), "SKILL.md")
							if err := os.Link(entry, found); err != nil {
								t.Fatal(err)
							}
						}
						rows = append(rows, map[string]any{"name": "method", "description": "method", "path": found, "scope": "user", "enabled": true})
					}
					if mode == "incomplete-discovery" {
						problems = append(problems, map[string]any{"path": "unknown"})
					}
					return map[string]any{"data": []any{map[string]any{"cwd": s.Cwd, "skills": rows, "errors": problems}}}, nil
				case "thread/start":
					started++
					return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request", "disabledPluginIds": []string{}}, nil
				default:
					return emptyControl(method)
				}
			}
			_, err = c.Execute(context.Background(), Intent{ID: "create-shared", Kind: "create", Scope: s, Permission: "read_only"})
			allowed := mode == "file" || mode == "directory"
			if !allowed {
				if err == nil || started != 0 {
					t.Fatal("unsafe shared Skill created native thread")
				}
				return
			}
			if err != nil || started != 1 {
				t.Fatal("shared Skill creation failed", err)
			}
			loaded, err := os.ReadFile(filepath.Join(pack.CachePath, "skills/method/SKILL.md"))
			if err != nil || string(loaded) != string(body) {
				t.Fatal("shared method bytes were rewritten")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(entry), ".codex-plugin")); !os.IsNotExist(err) {
				t.Fatal("source modified to become a plugin")
			}
			if err := os.WriteFile(filepath.Join(pack.CachePath, "skills/method/SKILL.md"), []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Execute(context.Background(), Intent{ID: "retry", Kind: "create", Scope: s, Permission: "read_only"}); err == nil || started != 1 {
				t.Fatal("altered native cache repaired or accepted")
			}
		})
	}
}
