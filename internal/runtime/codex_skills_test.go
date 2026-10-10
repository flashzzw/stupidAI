package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCodexSkillDiscoveryUsesDirectorySampleOnly(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "rpc-error", "wrong-cwd", "missing-data", "missing-skills", "missing-errors", "duplicate-cwd", "scan-error", "unknown-scope", "missing-enabled", "duplicate-path", "relative-path", "probe", "wrong-backend"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind = "codex"
			if mode == "probe" {
				scope.Purpose = "probe"
			}
			if mode == "wrong-backend" {
				scope.Instance.Kind = "claude"
			}
			entry := map[string]any{"cwd": scope.Cwd, "errors": []any{}}
			shared := map[string]any{"name": "shared", "description": "description only", "path": "/home/shared/SKILL.md", "scope": "user", "enabled": false}
			project := map[string]any{"name": "shared", "description": "same name, different source", "path": "/fixture/.agents/skills/shared/SKILL.md", "scope": "repo", "enabled": true, "pluginId": "fixture@local"}
			entry["skills"] = []any{shared, project}
			switch mode {
			case "empty":
				entry["skills"] = []any{}
			case "wrong-cwd":
				entry["cwd"] = "/other"
			case "missing-skills":
				delete(entry, "skills")
			case "missing-errors":
				delete(entry, "errors")
			case "scan-error":
				entry["errors"] = []any{map[string]any{"path": "/broken/SKILL.md", "message": "source-content-must-not-leak"}}
			case "unknown-scope":
				shared["scope"] = "future-managed"
			case "missing-enabled":
				delete(shared, "enabled")
			case "duplicate-path":
				entry["skills"] = []any{shared, shared}
			case "relative-path":
				shared["path"] = "relative/SKILL.md"
			}
			f.call = func(method string, params map[string]any) (any, error) {
				if method != "skills/list" || !reflect.DeepEqual(params, map[string]any{"cwds": []string{scope.Cwd}, "forceReload": true}) {
					t.Fatalf("discovery used thread/config mutation or arbitrary roots: %s %+v", method, params)
				}
				if mode == "rpc-error" {
					return nil, errors.New("source-content-must-not-leak")
				}
				data := []any{entry}
				if mode == "duplicate-cwd" {
					data = append(data, entry)
				}
				if mode == "missing-data" {
					data = nil
				}
				return map[string]any{"data": data}, nil
			}
			out, err := c.DiscoverSkills(context.Background(), scope)
			switch mode {
			case "rpc-error", "wrong-cwd", "missing-data", "missing-skills", "missing-errors", "duplicate-cwd", "probe", "wrong-backend":
				if err == nil || out.Skills != nil {
					t.Fatalf("invalid inventory accepted: %+v %v", out, err)
				}
			case "valid", "empty":
				if err != nil || !out.Complete || len(out.Errors) != 0 || out.At == "" || out.Source != "codex.skills/list" {
					t.Fatalf("missing discovery: %+v %v", out, err)
				}
				if mode == "valid" && (len(out.Skills) != 2 || out.Skills[0].Scope != "repo" || !out.Skills[0].Enabled || out.Skills[1].Enabled || out.Skills[0].PluginID != "fixture@local") {
					t.Fatalf("lost source or config state: %+v", out)
				}
			default:
				if err != nil || out.Complete || len(out.Errors) == 0 {
					t.Fatalf("partial result claimed complete: %+v %v", out, err)
				}
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "source-content-must-not-leak") || err != nil && strings.Contains(err.Error(), "source-content-must-not-leak") {
				t.Fatal("source contents leaked")
			}
			wantCalls := 1
			if mode == "probe" || mode == "wrong-backend" {
				wantCalls = 0
			}
			if len(f.methods) != wantCalls || len(f.replies) != 0 {
				t.Fatalf("unexpected side effects: %+v", f.methods)
			}
		})
	}
}
