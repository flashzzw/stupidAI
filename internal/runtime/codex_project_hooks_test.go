package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func projectHookHarness(t *testing.T) (*Codex, *nativeFixture, Scope, map[string]any, map[string]any) {
	t.Helper()
	c, f, s := nativeHarness(t)
	t.Cleanup(func() { _ = c.Close() })
	s.Instance.Kind = "codex"
	s.Instance.Profile = t.TempDir()
	s.Cwd = t.TempDir()
	if err := os.WriteFile(filepath.Join(s.Instance.Profile, "config.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"layers": []any{map[string]any{"name": map[string]any{"type": "user", "file": filepath.Join(s.Instance.Profile, "config.toml")}}}}
	entry := map[string]any{"cwd": s.Cwd, "hooks": []any{}, "warnings": []any{}, "errors": []any{}}
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "config/read":
			if params["cwd"] != s.Cwd || params["includeLayers"] != true {
				t.Fatal(params)
			}
			return config, nil
		case "hooks/list":
			if !reflect.DeepEqual(params["cwds"], []string{s.Cwd}) {
				t.Fatal(params)
			}
			return map[string]any{"data": []any{entry}}, nil
		}
		return nil, fmt.Errorf("unexpected method %s", method)
	}
	return c, f, s, config, entry
}
func projectLayer(path string) map[string]any {
	return map[string]any{"name": map[string]any{"type": "project", "dotCodexFolder": filepath.Dir(path)}, "version": "sha256:" + strings.Repeat("1", 64), "disabledReason": nil}
}
func projectHook(path string) map[string]any {
	return map[string]any{"source": "project", "sourcePath": path, "isManaged": false}
}
func projectWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexProjectHooksJoinDeclaredAndDiscoveredSources(t *testing.T) {
	for _, scenario := range []string{"same-file", "worktree", "missing-hook", "untrusted", "no-layer"} {
		t.Run(scenario, func(t *testing.T) {
			_, f, s, config, entry := projectHookHarness(t)
			path := filepath.Join(s.Cwd, ".codex/hooks.json")
			actual := path
			layer := projectLayer(path)
			if scenario != "no-layer" {
				config["layers"] = append(config["layers"].([]any), layer)
			}
			if scenario == "worktree" {
				actual = filepath.Join(t.TempDir(), ".codex/hooks.json")
			}
			if scenario != "missing-hook" && scenario != "no-layer" {
				projectWrite(t, path, `{"hooks":{}}`)
			}
			if scenario == "worktree" {
				projectWrite(t, actual, `{"hooks":{}}`)
			}
			if scenario == "untrusted" {
				layer["disabledReason"] = "untrusted"
			} else if scenario != "missing-hook" && scenario != "no-layer" {
				entry["hooks"] = []any{projectHook(actual)}
			}
			saved, err := readCodexProjectHooks(context.Background(), f, s)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Files == nil {
				t.Fatal("nil is not proven empty")
			}
			byPath := map[string]CodexProjectHookFile{}
			for _, file := range saved.Files {
				byPath[file.File.Path] = file
			}
			switch scenario {
			case "no-layer":
				if len(saved.Files) != 0 {
					t.Fatal(saved)
				}
			case "worktree":
				if len(saved.Files) != 2 || !byPath[path].Declared || byPath[path].Discovered || !byPath[actual].Discovered || byPath[actual].Declared {
					t.Fatal(saved)
				}
			case "missing-hook":
				if len(saved.Files) != 1 || byPath[path].File.Kind != "absent" || byPath[path].Discovered {
					t.Fatal(saved)
				}
			case "untrusted":
				if !byPath[path].LayerDisabled || byPath[path].Discovered {
					t.Fatal(saved)
				}
			default:
				if len(saved.Files) != 1 || !byPath[path].Declared || !byPath[path].Discovered {
					t.Fatal(saved)
				}
			}
			s.CodexProjectHooks = saved
			if err := verifyCodexProjectHooks(context.Background(), f, s); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexProjectHooksRejectPartialOrInvalidMetadata(t *testing.T) {
	for _, scenario := range []string{"no-layers", "no-user", "duplicate-user", "wrong-user", "duplicate-project", "relative-folder", "unclean-folder", "wrong-folder", "bad-version", "missing-hooks", "missing-warnings", "missing-errors", "warning", "error", "wrong-cwd", "unknown-source", "managed-project", "missing-managed", "relative-source", "wrong-source-name", "discovered-missing", "too-many-layers", "call-error"} {
		t.Run(scenario, func(t *testing.T) {
			_, f, s, config, entry := projectHookHarness(t)
			path := filepath.Join(s.Cwd, ".codex/hooks.json")
			projectWrite(t, path, `{"hooks":{}}`)
			layer := projectLayer(path)
			hook := projectHook(path)
			config["layers"] = append(config["layers"].([]any), layer)
			entry["hooks"] = []any{hook}
			switch scenario {
			case "no-layers":
				delete(config, "layers")
			case "no-user":
				config["layers"] = []any{layer}
			case "duplicate-user":
				config["layers"] = append(config["layers"].([]any), config["layers"].([]any)[0])
			case "wrong-user":
				config["layers"].([]any)[0].(map[string]any)["name"].(map[string]any)["file"] = "/missing/config.toml"
			case "duplicate-project":
				config["layers"] = append(config["layers"].([]any), layer)
			case "relative-folder":
				layer["name"].(map[string]any)["dotCodexFolder"] = "relative/.codex"
			case "unclean-folder":
				layer["name"].(map[string]any)["dotCodexFolder"] = s.Cwd + "/extra/../.codex"
			case "wrong-folder":
				layer["name"].(map[string]any)["dotCodexFolder"] = s.Cwd + "/config"
			case "bad-version":
				layer["version"] = "unchecked"
			case "missing-hooks":
				delete(entry, "hooks")
			case "missing-warnings":
				delete(entry, "warnings")
			case "missing-errors":
				delete(entry, "errors")
			case "warning":
				entry["warnings"] = []any{"partial"}
			case "error":
				entry["errors"] = []any{"invalid"}
			case "wrong-cwd":
				entry["cwd"] = "/other"
			case "unknown-source":
				hook["source"] = "future"
			case "managed-project":
				hook["isManaged"] = true
			case "missing-managed":
				delete(hook, "isManaged")
			case "relative-source":
				hook["sourcePath"] = ".codex/hooks.json"
			case "wrong-source-name":
				hook["sourcePath"] = s.Cwd + "/.codex/other.json"
			case "discovered-missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "too-many-layers":
				for len(config["layers"].([]any)) <= 128 {
					config["layers"] = append(config["layers"].([]any), map[string]any{})
				}
			case "call-error":
				f.call = func(string, map[string]any) (any, error) { return nil, fmt.Errorf("unavailable") }
			}
			if saved, err := readCodexProjectHooks(context.Background(), f, s); err == nil || saved != nil {
				t.Fatal("accepted incomplete source", saved, err)
			}
		})
	}
}

func TestCodexProjectHooksDetectChangesDuringRead(t *testing.T) {
	for _, change := range []string{"metadata", "file"} {
		t.Run(change, func(t *testing.T) {
			_, f, s, config, entry := projectHookHarness(t)
			path := filepath.Join(s.Cwd, ".codex/hooks.json")
			projectWrite(t, path, `{"hooks":{}}`)
			config["layers"] = append(config["layers"].([]any), projectLayer(path))
			entry["hooks"] = []any{projectHook(path)}
			calls := 0
			original := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "config/read" {
					calls++
					if calls == 2 {
						if change == "file" {
							projectWrite(t, path, `{"hooks":{"Stop":[]}}`)
						} else {
							config["layers"] = config["layers"].([]any)[:1]
						}
					}
				}
				return original(method, params)
			}
			if saved, err := readCodexProjectHooks(context.Background(), f, s); err == nil || saved != nil {
				t.Fatal("accepted mid-read drift")
			}
		})
	}
}

func TestCodexProjectHookDriftRejectsDispatch(t *testing.T) {
	for _, change := range []string{"content", "new-source", "removed-source", "layer-version", "layer-enabled", "legacy"} {
		t.Run(change, func(t *testing.T) {
			c, f, s, config, entry := projectHookHarness(t)
			path := filepath.Join(s.Cwd, ".codex/hooks.json")
			projectWrite(t, path, `{"hooks":{}}`)
			layer := projectLayer(path)
			config["layers"] = append(config["layers"].([]any), layer)
			entry["hooks"] = []any{projectHook(path)}
			var err error
			s.CodexProjectHooks, err = readCodexProjectHooks(context.Background(), f, s)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content":
				projectWrite(t, path, `{"hooks":{"Stop":[]}}`)
			case "new-source":
				extra := filepath.Join(t.TempDir(), ".codex/hooks.json")
				projectWrite(t, extra, `{"hooks":{}}`)
				entry["hooks"] = append(entry["hooks"].([]any), projectHook(extra))
			case "removed-source":
				entry["hooks"] = []any{}
			case "layer-version":
				layer["version"] = "sha256:" + strings.Repeat("2", 64)
			case "layer-enabled":
				layer["disabledReason"] = "untrusted"
			case "legacy":
				s.CodexProjectHooks = nil
			}
			if change == "legacy" {
				f.methods = nil
				if err := verifyCodexProjectHooks(context.Background(), f, s); err != nil || len(f.methods) != 0 {
					t.Fatal("legacy synthesized metadata", err)
				}
				return
			}
			if err := verifyCodexProjectHooks(context.Background(), f, s); err == nil {
				t.Fatal("drift accepted")
			}
			for _, kind := range []string{"create", "send", "respond", "prepare_execution", "apply_execution", "recover_execution"} {
				f.methods = nil
				_, err := c.Execute(context.Background(), Intent{Kind: kind, Scope: s})
				if err == nil || !strings.Contains(err.Error(), "hook") {
					t.Fatalf("%s: %v", kind, err)
				}
				for _, method := range f.methods {
					if method != "config/read" && method != "hooks/list" {
						t.Fatalf("drift dispatched %s", method)
					}
				}
			}
		})
	}
}

func TestCodexProjectHookSavedBindingIntegrity(t *testing.T) {
	for _, change := range []string{"version", "source", "cwd", "profile", "purpose", "nil-files", "duplicate-file", "no-provenance", "invented-layer", "missing-discovered", "file-mode", "file-delete", "file-create", "symlink-target"} {
		t.Run(change, func(t *testing.T) {
			_, f, s, config, entry := projectHookHarness(t)
			path := filepath.Join(s.Cwd, ".codex/hooks.json")
			if change != "file-create" {
				projectWrite(t, path, `{"hooks":{}}`)
			}
			if change == "symlink-target" {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			config["layers"] = append(config["layers"].([]any), projectLayer(path))
			if change != "file-create" {
				entry["hooks"] = []any{projectHook(path)}
			}
			saved, err := readCodexProjectHooks(context.Background(), f, s)
			if err != nil {
				t.Fatal(err)
			}
			s.CodexProjectHooks = saved
			switch change {
			case "version":
				saved.Version = 2
			case "source":
				saved.Source = "unverified"
			case "cwd":
				saved.Cwd = "/other"
			case "profile":
				saved.Profile = "/other"
			case "purpose":
				s.Purpose = "probe"
			case "nil-files":
				saved.Files = nil
			case "duplicate-file":
				saved.Files = append(saved.Files, saved.Files[0])
			case "no-provenance":
				saved.Files[0].Declared = false
				saved.Files[0].Discovered = false
			case "invented-layer":
				saved.Files[0].Declared = false
			case "missing-discovered":
				saved.Files[0].File.Kind = "absent"
			case "file-mode":
				err = os.Chmod(path, 0400)
			case "file-delete":
				err = os.Remove(path)
			case "file-create":
				projectWrite(t, path, `{"hooks":{}}`)
			case "symlink-target":
				projectWrite(t, path+".new", `{"hooks":{}}`)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(path+".new", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyCodexProjectHookFiles(s); err == nil {
				t.Fatal("invalid saved binding accepted")
			}
		})
	}
}
