package runtime

import (
	"context"
	"errors"
	"testing"

	"codex-netcap/internal/codex"
)

func TestConfigurationBaselineIncludesToolFieldsOutsideDisplayProjection(t *testing.T) {
	output := "first tool output"
	f := &nativeFixture{call: func(string, map[string]any) (any, error) {
		return map[string]any{"thread": map[string]any{"id": "native", "turns": []any{map[string]any{"id": "turn", "status": "completed", "items": []any{map[string]any{"id": "tool", "type": "customToolCall", "providerSpecificOutput": output}}}}}}, nil
	}}
	before, err := configurationBaseline(context.Background(), f, "native")
	if err != nil {
		t.Fatal(err)
	}
	output = "changed tool output"
	after, err := configurationBaseline(context.Background(), f, "native")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("display projection hid a change in native tool history")
	}
}

func TestConfigurationApplyRequiresUnloadedUnchangedIdleProofAndNeverReplays(t *testing.T) {
	for _, scenario := range []string{"success", "lost-receipt", "loaded", "missing-baseline", "changed-history", "queued", "terminal-error", "unloaded-after-apply", "wrong-inventory", "unknown-after-apply"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, _ := nativeHarness(t)
			scope := mcpScope(t)
			scope.Route = Route{Provider: "fixture", Model: "model"}
			loaded, selected, altered, queued, terminalError := true, true, false, false, false
			nativeUnknown := false
			resumes := 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					return mcpConfig(scope, "user", scope.MCPSelections[0].SourcePath, map[string]any{"command": "fixture"}), nil
				case "thread/read", "thread/turns/list":
					turns := []any{map[string]any{"id": "old-turn", "status": "completed", "items": []any{}}}
					if altered {
						turns = append(turns, map[string]any{"id": "external-turn", "status": "completed", "items": []any{}})
					}
					status := "notLoaded"
					if nativeUnknown {
						return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": "unknown"}, "turns": turns}}, nil
					}
					if loaded {
						status = "idle"
					}
					if method == "thread/turns/list" {
						return map[string]any{"data": turns}, nil
					}
					return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": status}, "turns": turns}}, nil
				case "thread/backgroundTerminals/list":
					if terminalError {
						return nil, errors.New("unavailable inventory")
					}
					if !loaded {
						return nil, &codex.RPCError{Code: -32600, Message: "thread not found: native"}
					}
				case "thread/queue/list":
					if queued {
						return map[string]any{"data": []any{map[string]any{"id": "q"}}}, nil
					}
				case "mcpServerStatus/list":
					status := "disabled"
					if selected {
						status = "connected"
					}
					return map[string]any{"data": []any{map[string]any{"name": "identity", "runtimeStatus": status, "pluginId": nil, "tools": map[string]any{}}}}, nil
				case "thread/resume":
					resumes++
					loaded = true
					selected = scenario == "wrong-inventory"
					if scenario == "lost-receipt" {
						return nil, errors.New("fixture lost real receipt")
					}
					return map[string]any{"thread": map[string]any{"id": "native"}, "model": "model", "modelProvider": "fixture"}, nil
				}
				return emptyControl(method)
			}
			prepared, err := c.Execute(context.Background(), Intent{Kind: "prepare_execution", Scope: scope})
			if err != nil || prepared.ConfigurationBaseline == "" || resumes != 0 {
				t.Fatalf("prepare: %+v %v", prepared, err)
			}
			loaded = false
			scope.MCPSelections[0].Mode = "disabled"
			intent := Intent{Kind: "apply_execution", Scope: scope, ConfigurationBaseline: prepared.ConfigurationBaseline}
			switch scenario {
			case "loaded":
				loaded = true
			case "missing-baseline":
				intent.ConfigurationBaseline = ""
			case "changed-history":
				altered = true
			case "queued":
				queued = true
			case "terminal-error":
				terminalError = true
			}
			receipt, err := c.Execute(context.Background(), intent)
			if scenario == "loaded" || scenario == "missing-baseline" || scenario == "changed-history" || scenario == "queued" || scenario == "terminal-error" {
				var notSent *NotSent
				if !errors.As(err, &notSent) || resumes != 0 {
					t.Fatalf("unsafe apply: %v, resumes %d", err, resumes)
				}
				return
			}
			if scenario == "lost-receipt" {
				var notSent *NotSent
				if err == nil || errors.As(err, &notSent) {
					t.Fatalf("lost receipt classified as safe retry: %v", err)
				}
				f.closed = false
				c.clients[scope.Instance.ID] = f
			} else if err != nil {
				t.Fatal(err)
			}
			if scenario == "unloaded-after-apply" {
				loaded = false
			}
			if scenario == "unknown-after-apply" {
				nativeUnknown = true
			}
			for range 2 {
				resolution, err := c.Reconcile(context.Background(), intent, receipt)
				if err != nil {
					t.Fatal(err)
				}
				want := "finished"
				if scenario == "wrong-inventory" || scenario == "unloaded-after-apply" || scenario == "unknown-after-apply" {
					want = "unknown"
				}
				if resolution.State != want || resumes != 1 {
					t.Fatalf("reconcile %+v resumes=%d", resolution, resumes)
				}
			}
		})
	}
}
