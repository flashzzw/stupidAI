package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func codexPluginScope(t *testing.T) Scope {
	t.Helper()
	s := mcpScope(t)
	s.SessionID = "session"
	s.NativeID = ""
	s.MCPSelections = nil
	s.Route = Route{Provider: "fixture", Model: "fixture", CredentialRef: "profile:fixture"}
	root := t.TempDir()
	p := PluginSelection{AssetID: "plugin", Name: "fixture-plugin", Mode: "enabled", Version: "1.0.0", SourcePath: root, SourceHash: hex.EncodeToString(make([]byte, 32)), RootMode: 0700, Components: []PluginComponent{{Kind: "mcp", Name: "identity", NativeName: "identity", Path: ".mcp.json", Count: 1}}}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ path, body string }{{".codex-plugin/plugin.json", `{"name":"fixture-plugin","version":"1.0.0"}`}, {".mcp.json", `{"mcpServers":{"identity":{"command":"fixture"}}}`}} {
		path := filepath.Join(root, file.path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file.body), 0600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(file.body))
		p.Files = append(p.Files, PluginFile{Path: file.path, Kind: "file", Mode: 0600, SHA256: hex.EncodeToString(h[:])})
	}
	p.Files = append(p.Files, PluginFile{Path: ".codex-plugin", Kind: "directory", Mode: 0700})
	p.NativeSource, p.CachePath, _ = CodexPluginLocation(s, p)
	s.PluginSelections = []PluginSelection{p}
	return s
}

func TestCodexPluginObserveRequiresExactSourceAndCache(t *testing.T) {
	c, f, _ := nativeHarness(t)
	s := codexPluginScope(t)
	// Use the public creation path to prepare the native cache.
	f.call = func(method string, params map[string]any) (any, error) {
		if method == "config/read" {
			return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
		}
		return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly", "networkAccess": false}, "approvalPolicy": "on-request"}, nil
	}
	if _, err := c.Execute(context.Background(), Intent{ID: "create", Kind: "create", Scope: s, Permission: "read_only"}); err != nil {
		t.Fatal(err)
	}
	s.NativeID = "created"
	p := s.PluginSelections[0]
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": "created", "cwd": s.Cwd, "status": map[string]string{"type": "idle"}, "turns": []any{}}}, nil
		case "thread/turns/list":
			return map[string]any{"data": []any{}}, nil
		case "mcpServerStatus/list":
			return map[string]any{"data": []any{map[string]any{"name": "identity", "pluginId": p.NativeSource, "runtimeStatus": "connected", "tools": map[string]any{"whoami": map[string]any{}}}}}, nil
		default:
			return emptyControl(method)
		}
	}
	v, err := c.Observe(context.Background(), s)
	if err != nil || !v.ExecutionClear || len(v.PluginEvidence) != 1 || !v.PluginEvidence[0].Verified || len(v.CapabilityEvidence) != 1 || v.CapabilityEvidence[0].AssetID != "plugin" || v.CapabilityEvidence[0].PluginID != p.NativeSource {
		b, _ := json.Marshal(v)
		t.Fatalf("plugin selection was not reconciled: %s %v", b, err)
	}
	if err := os.WriteFile(filepath.Join(p.CachePath, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	v, err = c.Observe(context.Background(), s)
	if err != nil || v.ExecutionClear || len(v.PluginEvidence) != 1 || v.PluginEvidence[0].Verified {
		t.Fatalf("changed cache still accepted: %+v %v", v.PluginEvidence, err)
	}
}

func TestCodexPluginCreateUsesPrivateCacheAndOnlyThreadOverride(t *testing.T) {
	c, f, _ := nativeHarness(t)
	s := codexPluginScope(t)
	p := s.PluginSelections[0]
	started := false
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "config/read":
			return map[string]any{"config": map[string]any{"plugins": map[string]any{}, "mcp_servers": map[string]any{}, "features": map[string]any{"plugins": true}}, "layers": []any{}}, nil
		case "thread/start":
			started = true
			o := params["config"].(map[string]any)
			if o["plugins."+p.NativeSource+".enabled"] != true {
				t.Errorf("private plugin override missing: %+v", o)
			}
			b, err := os.ReadFile(filepath.Join(p.CachePath, ".mcp.json"))
			if err != nil || string(b) != `{"mcpServers":{"identity":{"command":"fixture"}}}` {
				t.Errorf("native start preceded fixed cache: %s %v", b, err)
			}
			return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly", "networkAccess": false}, "approvalPolicy": "on-request"}, nil
		default:
			t.Errorf("unexpected native mutation: %s", method)
			return nil, nil
		}
	}
	before, _ := os.ReadFile(filepath.Join(s.Instance.Profile, "config.toml"))
	_, err := c.Execute(context.Background(), Intent{ID: "create", Kind: "create", Scope: s, Permission: "read_only"})
	if err != nil || !started {
		t.Fatalf("create: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.Instance.Profile, "config.toml"))
	if string(after) != string(before) {
		t.Fatal("plugin selection wrote user profile")
	}
}

func TestCodexPluginChangedCacheRejectsInputsWithoutRepair(t *testing.T) {
	for _, mode := range []string{"contents", "extra-file", "permissions", "missing", "outside-link"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			s := codexPluginScope(t)
			p := s.PluginSelections[0]
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "config/read" {
					return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
				}
				return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly", "networkAccess": false}, "approvalPolicy": "on-request"}, nil
			}
			if _, err := c.Execute(context.Background(), Intent{ID: "create", Kind: "create", Scope: s, Permission: "read_only"}); err != nil {
				t.Fatal(err)
			}
			s.NativeID = "created"
			switch mode {
			case "contents":
				if err := os.WriteFile(filepath.Join(p.CachePath, ".mcp.json"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra-file":
				if err := os.WriteFile(filepath.Join(p.CachePath, "extra"), []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(filepath.Join(p.CachePath, ".mcp.json"), 0400); err != nil {
					t.Fatal(err)
				}
			case "missing", "outside-link":
				if err := os.Rename(p.CachePath, p.CachePath+"-retained"); err != nil {
					t.Fatal(err)
				}
				if mode == "outside-link" {
					if err := os.Symlink(t.TempDir(), p.CachePath); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, readBefore := os.ReadFile(filepath.Join(p.CachePath, ".mcp.json"))
			f.methods = nil
			for _, kind := range []string{"send", "respond"} {
				if _, err := c.Execute(context.Background(), Intent{ID: kind, Kind: kind, Scope: s, Permission: "read_only", Text: "not sent"}); err == nil {
					t.Fatal("changed cache accepted")
				}
			}
			for _, method := range f.methods {
				if method != "config/read" {
					t.Fatalf("input or repair attempted: %s", method)
				}
			}
			after, readAfter := os.ReadFile(filepath.Join(p.CachePath, ".mcp.json"))
			if string(before) != string(after) || (readBefore == nil) != (readAfter == nil) {
				t.Fatal("input preflight repaired native cache")
			}
		})
	}
}

func TestCodexPluginCreateRejectsConflictingInheritedConfiguration(t *testing.T) {
	for _, mode := range []string{"same-plugin", "same-mcp", "plugins-disabled"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			s := codexPluginScope(t)
			started := false
			f.call = func(method string, params map[string]any) (any, error) {
				if method != "config/read" {
					started = true
					return nil, nil
				}
				cfg := map[string]any{"features": map[string]bool{"plugins": true}}
				switch mode {
				case "same-plugin":
					cfg["plugins"] = map[string]any{"fixture-plugin@user": map[string]any{"enabled": true}}
				case "same-mcp":
					cfg["mcp_servers"] = map[string]any{"identity": map[string]any{"command": "user-command", "enabled": false}}
				case "plugins-disabled":
					cfg["features"] = map[string]bool{"plugins": false}
				}
				return map[string]any{"config": cfg}, nil
			}
			if _, err := c.Execute(context.Background(), Intent{ID: "create", Kind: "create", Scope: s, Permission: "read_only"}); err == nil || started {
				t.Fatal("conflicting plugin configuration was started")
			}
		})
	}
}

func TestCodexPluginDisabledNeedsCompleteExclusion(t *testing.T) {
	for _, mode := range []string{"absent", "wrong-source", "unexpected-component", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			s := codexPluginScope(t)
			s.NativeID = "created"
			s.PluginSelections[0].Mode = "disabled"
			p := s.PluginSelections[0]
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "thread/read":
					return map[string]any{"thread": map[string]any{"id": "created", "cwd": s.Cwd, "status": map[string]string{"type": "idle"}, "turns": []any{}}}, nil
				case "thread/turns/list":
					return map[string]any{"data": []any{}}, nil
				case "mcpServerStatus/list":
					if mode == "read-error" {
						return nil, fmt.Errorf("inventory unavailable")
					}
					rows := []any{map[string]any{"name": "unrelated", "pluginId": nil, "runtimeStatus": "connected", "tools": map[string]any{}}}
					if mode == "wrong-source" {
						rows = append(rows, map[string]any{"name": "identity", "pluginId": "other@user", "runtimeStatus": "connected", "tools": map[string]any{}})
					}
					if mode == "unexpected-component" {
						rows = append(rows, map[string]any{"name": "unexpected", "pluginId": p.NativeSource, "runtimeStatus": "connected", "tools": map[string]any{}})
					}
					return map[string]any{"data": rows}, nil
				default:
					return emptyControl(method)
				}
			}
			v, err := c.Observe(context.Background(), s)
			want := mode == "absent"
			if err != nil || v.ExecutionClear != want || len(v.PluginEvidence) != 1 || v.PluginEvidence[0].Verified != want {
				t.Fatalf("wrong exclusion: %+v %v", v, err)
			}
			if want && (len(v.CapabilityEvidence) != 2 || v.CapabilityEvidence[0].RuntimeStatus != "disabled" || v.CapabilityEvidence[1].Mode != "inherit") {
				t.Fatal("disabled selection lost unrelated inherited MCP")
			}
		})
	}
}
