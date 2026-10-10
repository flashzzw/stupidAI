package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func installedCatalogFixture(t *testing.T) (Config, string, string, string) {
	t.Helper()
	c, _ := claudeSkillFixture(t)
	market, cache := t.TempDir(), t.TempDir()
	writePluginMetadata(t, filepath.Join(market, ".claude-plugin/marketplace.json"), `{"name":"local","plugins":[{"name":"selected","source":"./selected"}]}`)
	writePluginMetadata(t, filepath.Join(market, "selected/.claude-plugin/plugin.json"), `{"name":"selected","version":"1.0.0","description":"PRIVATE_BODY_NOT_EXPORTED"}`)
	registry := filepath.Join(c.Instances[0].Profile, "plugins/installed_plugins.json")
	writePluginMetadata(t, registry, map[string]any{"version": 2, "plugins": map[string]any{"selected@local": []any{map[string]any{"scope": "user", "version": "1.0.0", "installPath": cache}}}})
	writePluginMetadata(t, filepath.Join(filepath.Dir(registry), "known_marketplaces.json"), map[string]any{"local": map[string]any{"source": map[string]any{"source": "directory", "path": market}, "installLocation": market}})
	return c, market, cache, registry
}

func writePluginMetadata(t *testing.T, path string, value any) {
	t.Helper()
	var body []byte
	if text, ok := value.(string); ok {
		body = []byte(text)
	} else {
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledPluginCatalogPreservesExecutionAndSeparatesPaths(t *testing.T) {
	c, market, cache, registry := installedCatalogFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before := a.Snapshot()
	out, err := a.ReadSessionInstalledPlugins(context.Background(), id)
	if err != nil || !out.Complete || len(out.Entries) != 1 || out.Source != "claude.installed_registry" {
		t.Fatalf("catalog: %+v %v", out, err)
	}
	entry := out.Entries[0]
	if entry.SourceKey != "selected@local" || entry.Scope != "user" || entry.CachePath != cache || entry.DeclaredPath != filepath.Join(market, "selected") || entry.ResolvedPath == entry.CacheResolvedPath || entry.ManifestVersion != "1.0.0" || entry.RegisteredVersion != "1.0.0" || out.RegistryPath != registry {
		t.Fatalf("wrong declaration: %+v", entry)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "PRIVATE_BODY") || strings.Contains(string(encoded), `"enabled"`) || strings.Contains(string(encoded), `"verified"`) || digest(before) != digest(a.Snapshot()) || f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("metadata read altered execution or claimed native loading")
	}
}

func TestInstalledPluginCatalogMetadataBoundaries(t *testing.T) {
	for _, mode := range []string{"version-drift", "cache-missing", "registry-missing", "invalid-json", "duplicate-json", "schema", "unknown-market", "remote", "different-market-path", "duplicate-declaration", "escape", "link-escape", "market-metadata-escape", "plugin-metadata-escape", "invalid-scope", "large", "fifo", "file-link", "cancel", "limit"} {
		t.Run(mode, func(t *testing.T) {
			c, market, cache, registry := installedCatalogFixture(t)
			known := filepath.Join(filepath.Dir(registry), "known_marketplaces.json")
			ctx := context.Background()
			switch mode {
			case "version-drift":
				writePluginMetadata(t, filepath.Join(market, "selected/.claude-plugin/plugin.json"), `{"name":"selected","version":"2.0.0"}`)
			case "cache-missing":
				if err := os.Remove(cache); err != nil {
					t.Fatal(err)
				}
			case "registry-missing":
				if err := os.Remove(registry); err != nil {
					t.Fatal(err)
				}
			case "invalid-json":
				writePluginMetadata(t, registry, `{"PRIVATE_BODY":broken`)
			case "duplicate-json":
				writePluginMetadata(t, registry, `{"version":2,"version":2,"plugins":{}}`)
			case "schema":
				writePluginMetadata(t, registry, `{"version":3,"plugins":{}}`)
			case "unknown-market":
				writePluginMetadata(t, known, `{}`)
			case "remote":
				writePluginMetadata(t, known, `{"local":{"source":{"source":"github","repo":"PRIVATE_BODY"}}}`)
			case "different-market-path":
				writePluginMetadata(t, known, map[string]any{"local": map[string]any{"source": map[string]any{"source": "directory", "path": market}, "installLocation": cache}})
			case "duplicate-declaration":
				writePluginMetadata(t, filepath.Join(market, ".claude-plugin/marketplace.json"), `{"name":"local","plugins":[{"name":"selected","source":"./selected"},{"name":"selected","source":"./selected"}]}`)
			case "escape":
				writePluginMetadata(t, filepath.Join(market, ".claude-plugin/marketplace.json"), `{"name":"local","plugins":[{"name":"selected","source":"../outside"}]}`)
			case "link-escape":
				if err := os.Rename(filepath.Join(market, "selected"), filepath.Join(cache, "selected")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(cache, "selected"), filepath.Join(market, "selected")); err != nil {
					t.Fatal(err)
				}
			case "market-metadata-escape", "plugin-metadata-escape":
				metadata := filepath.Join(market, ".claude-plugin")
				if mode == "plugin-metadata-escape" {
					metadata = filepath.Join(market, "selected/.claude-plugin")
				}
				external := filepath.Join(cache, "metadata")
				if err := os.Rename(metadata, external); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, metadata); err != nil {
					t.Fatal(err)
				}
			case "invalid-scope":
				writePluginMetadata(t, registry, map[string]any{"version": 2, "plugins": map[string]any{"selected@local": []any{map[string]any{"scope": "project", "version": "1.0.0", "installPath": cache}}}})
			case "large":
				writePluginMetadata(t, registry, strings.Repeat("x", (1<<20)+1))
			case "fifo", "file-link":
				if err := os.Remove(registry); err != nil {
					t.Fatal(err)
				}
				var err error
				if mode == "fifo" {
					err = syscall.Mkfifo(registry, 0600)
				} else {
					err = os.Symlink(known, registry)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "limit":
				rows := make([]any, 513)
				for i := range rows {
					rows[i] = map[string]any{"scope": "user", "version": "1.0.0", "installPath": cache}
				}
				writePluginMetadata(t, registry, map[string]any{"version": 2, "plugins": map[string]any{"selected@local": rows}})
			}
			a := openFixture(t, c, newFixture())
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			out, err := a.ReadSessionInstalledPlugins(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if out.Complete != (mode == "cache-missing" || mode == "registry-missing") {
				t.Fatalf("unexpected completeness: %+v", out)
			}
			if mode == "version-drift" && (out.Entries[0].RegisteredVersion != "1.0.0" || out.Entries[0].ManifestVersion != "2.0.0" || out.Entries[0].Status != "version_mismatch") {
				t.Fatal("version drift hidden")
			}
			if mode == "cache-missing" && out.Entries[0].CacheStatus != "missing" {
				t.Fatal("missing cache hidden")
			}
			if strings.HasSuffix(mode, "metadata-escape") && (len(out.Entries) != 1 || out.Entries[0].ManifestVersion != "" || out.Entries[0].Status != "unavailable") {
				t.Fatal("external metadata was followed")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "PRIVATE_BODY") {
				t.Fatal("metadata body leaked")
			}
		})
	}
}

type catalogChangeContext struct {
	context.Context
	once   sync.Once
	change func()
}

func (c *catalogChangeContext) Err() error { c.once.Do(c.change); return c.Context.Err() }

func TestInstalledPluginCatalogRejectsChangedOwnership(t *testing.T) {
	c, _, _, _ := installedCatalogFixture(t)
	a := openFixture(t, c, newFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	ctx := &catalogChangeContext{Context: context.Background(), change: func() {
		if _, err := a.Apply(Command{ID: "catalog-takeover", Kind: "takeover", SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := a.ReadSessionInstalledPlugins(ctx, id); err == nil {
		t.Fatal("stale ownership accepted")
	}
}
