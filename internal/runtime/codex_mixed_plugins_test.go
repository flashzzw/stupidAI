package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codex-netcap/internal/codex"
)

func codexMixedScope(t *testing.T) Scope {
	t.Helper()
	s := codexPluginScope(t)
	s.Cwd = t.TempDir()
	s.StateNamespace = s.Instance.Profile
	p := &s.PluginSelections[0]
	file := filepath.Join(p.SourcePath, "skills/method/SKILL.md")
	body := []byte("---\nname: method\ndescription: Owned method\n---\nOWNED_METHOD\n")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	p.Files = append(p.Files, PluginFile{Path: "skills", Kind: "directory", Mode: 0700}, PluginFile{Path: "skills/method", Kind: "directory", Mode: 0700}, PluginFile{Path: "skills/method/SKILL.md", Kind: "file", Mode: 0600, SHA256: hex.EncodeToString(h[:])})
	p.Components = append(p.Components, PluginComponent{Kind: "skill", Name: "method", Path: "skills/method/SKILL.md", NativeName: p.Name + ":method", Count: 1})
	return s
}

func mixedPolicyHarness(t *testing.T, pure ...bool) (*Codex, *nativeFixture, Scope, string, *[]string) {
	t.Helper()
	c, f, _ := nativeHarness(t)
	s := codexMixedScope(t)
	if len(pure) > 0 && pure[0] {
		s = codexPluginScope(t)
		s.Cwd = t.TempDir()
		s.StateNamespace = s.Instance.Profile
	}
	return pluginPolicyHarnessWithScope(t, c, f, s)
}

func pluginPolicyHarnessWithScope(t *testing.T, c *Codex, f *nativeFixture, s Scope) (*Codex, *nativeFixture, Scope, string, *[]string) {
	t.Helper()
	policy := []string{"retained@owned"}
	path := filepath.Join(s.StateNamespace, "sessions", "mixed.jsonl")
	f.call = func(method string, params map[string]any) (any, error) {
		thread := map[string]any{"id": "created", "model": "fixture", "modelProvider": "fixture", "cwd": s.Cwd, "path": path, "status": map[string]any{"type": "idle"}, "turns": []any{}}
		switch method {
		case "config/read":
			return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
		case "thread/start", "thread/resume":
			return map[string]any{"thread": thread, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request", "disabledPluginIds": policy}, nil
		case "thread/read":
			return map[string]any{"thread": thread}, nil
		case "thread/turns/list":
			return map[string]any{"data": []any{}}, nil
		case "mcpServerStatus/list":
			rows := []any{}
			for _, component := range s.PluginSelections[0].Components {
				if component.Kind == "mcp" {
					rows = append(rows, map[string]any{"name": component.NativeName, "pluginId": s.PluginSelections[0].NativeSource, "runtimeStatus": "connected", "tools": map[string]any{}})
				}
			}
			return map[string]any{"data": rows}, nil
		case "turn/start":
			b, _ := json.Marshal(params["disabledPluginIds"])
			var got []string
			_ = json.Unmarshal(b, &got)
			if !slices.Equal(got, policy) {
				t.Fatalf("turn dropped inherited policy: got %s want %v", b, policy)
			}
			return map[string]any{"turn": map[string]any{"id": "sent"}}, nil
		default:
			return emptyControl(method)
		}
	}
	if _, err := c.Execute(context.Background(), Intent{Kind: "create", Scope: s, Permission: "read_only"}); err != nil {
		t.Fatal(err)
	}
	s.NativeID = "created"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeMixedPolicy(t, path, s, policy)
	return c, f, s, path, &policy
}

func writeMixedPolicy(t *testing.T, path string, s Scope, ids []string) {
	t.Helper()
	saveSkillContextRows(t, path, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": s.NativeID, "cwd": s.Cwd, "cli_version": "0.158.0", "model_provider": s.Route.Provider}},
		map[string]any{"type": "turn_context", "payload": map[string]any{"turn_id": "previous", "cwd": s.Cwd, "disabled_plugin_ids": ids}},
	})
}

func TestCodexMixedPluginInputRequiresPolicyBeyondMCPInventory(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled-in-native-policy", "missing-policy"} {
		t.Run(mode, func(t *testing.T) {
			c, _, s, path, ids := mixedPolicyHarness(t)
			if mode == "disabled-in-native-policy" {
				*ids = append(*ids, s.PluginSelections[0].NativeSource)
			}
			if mode == "missing-policy" {
				*ids = nil
			}
			writeMixedPolicy(t, path, s, *ids)
			observed, err := c.Observe(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			want := mode == "enabled"
			if observed.ExecutionClear != want || len(observed.PluginEvidence) != 1 || observed.PluginEvidence[0].Verified != want {
				t.Fatalf("MCP inventory promoted without policy: %+v", observed)
			}
			if want && observed.PluginEvidence[0].Coverage != "cached_package_and_thread_policy" {
				t.Fatal("Skill loading claimed from inventory")
			}
			got, err := c.Execute(context.Background(), Intent{Kind: "send", ID: "input", Scope: s, Permission: "read_only", Text: "owned"})
			if want && (err != nil || got.NativeTurn != "sent") {
				t.Fatalf("valid selection cannot send: %+v %v", got, err)
			}
			if !want && err == nil {
				t.Fatal("unverified native policy accepted input")
			}
		})
	}
}

func TestCodexMixedPluginReenablePreservesUnrelatedPolicyAndNeverReplays(t *testing.T) {
	for _, mode := range []string{"reenable", "pure-policy", "lost-settings-receipt", "lost-resume-receipt"} {
		t.Run(mode, func(t *testing.T) {
			c, f, original, path, ids := mixedPolicyHarness(t, mode == "pure-policy")
			original.PluginSelections[0].Mode = "disabled"
			*ids = append(*ids, original.PluginSelections[0].NativeSource)
			writeMixedPolicy(t, path, original, *ids)
			target := original
			target.PluginSelections = append([]PluginSelection{}, original.PluginSelections...)
			target.PluginSelections[0].Mode = "enabled"
			enabled, updates, resumes := false, 0, 0
			base := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "mcpServerStatus/list":
					if !enabled {
						return map[string]any{"data": []any{}}, nil
					}
				case "thread/settings/update":
					updates++
					b, _ := json.Marshal(params["disabledPluginIds"])
					_ = json.Unmarshal(b, ids)
					writeMixedPolicy(t, path, original, *ids)
					if mode == "lost-settings-receipt" {
						return nil, errors.New("lost settings acknowledgement")
					}
					return map[string]any{}, nil
				case "thread/unsubscribe":
					return map[string]any{"status": "unsubscribed"}, nil
				case "thread/resume":
					resumes++
					if slices.Contains(*ids, original.PluginSelections[0].NativeSource) {
						t.Error("resume did not clear old selected disable policy")
					}
					enabled = true
					if mode == "lost-resume-receipt" {
						return nil, errors.New("lost resume acknowledgement")
					}
				}
				return base(method, params)
			}
			prepared, err := c.Execute(context.Background(), Intent{Kind: "prepare_execution", Scope: original, Permission: "read_only"})
			if err != nil {
				t.Fatal(err)
			}
			in := Intent{Kind: "apply_execution", Scope: target, PreviousScope: &original, ConfigurationBaseline: prepared.ConfigurationBaseline, Permission: "read_only"}
			receipt, err := c.Execute(context.Background(), in)
			if (mode == "reenable" || mode == "pure-policy") && err != nil {
				t.Fatal(err)
			}
			if mode != "reenable" && mode != "pure-policy" {
				var notSent *NotSent
				if err == nil || errors.As(err, &notSent) {
					t.Fatal("mutation uncertainty misclassified as safe retry")
				}
				f.closed = false
				c.clients[original.Instance.ID] = f
			}
			if updates != 1 || !slices.Equal(*ids, []string{"retained@owned"}) {
				t.Fatalf("policy not replaced safely: updates=%d ids=%v", updates, *ids)
			}
			beforeResumes := resumes
			for i := 0; i < 2; i++ {
				out, err := c.Reconcile(context.Background(), in, receipt)
				if err != nil {
					t.Fatal(err)
				}
				if (out.State == "finished") != (mode != "lost-settings-receipt") {
					t.Fatalf("unverified application outcome: %+v", out)
				}
			}
			if updates != 1 || resumes != beforeResumes {
				t.Fatal("recovery replayed a policy mutation")
			}
		})
	}
}

func TestCodexMixedPluginMaterializedPolicyCannotFallbackAfterRemoval(t *testing.T) {
	c, _, s, path, _ := mixedPolicyHarness(t)
	first, err := c.Observe(context.Background(), s)
	if err != nil || !first.ExecutionClear {
		t.Fatal("initial policy not verified", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	after, err := c.Observe(context.Background(), s)
	if err == nil && after.ExecutionClear {
		t.Fatal("missing materialized policy fell back to obsolete creation receipt")
	}
}

func TestCodexMixedPluginFreshPolicyRejectsChangedNativeLogPath(t *testing.T) {
	c, f, s, path, _ := mixedPolicyHarness(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	base := f.call
	sampled := false
	f.call = func(method string, params map[string]any) (any, error) {
		out, err := base(method, params)
		if method == "thread/read" && err == nil {
			if sampled {
				out.(map[string]any)["thread"].(map[string]any)["path"] = "/tmp/foreign-policy.jsonl"
			}
			if value, ok := params["includeTurns"].(bool); ok && !value {
				sampled = true
			}
		}
		return out, err
	}
	v, err := c.Observe(context.Background(), s)
	if err == nil && v.ExecutionClear {
		t.Fatal("fresh fallback ignored changed native logfile")
	}
}

func TestCodexMixedPluginPolicyRejectsIncompleteOrForeignRecords(t *testing.T) {
	for _, mode := range []string{"missing", "null", "duplicate", "wrong-id", "wrong-provider", "client-authored", "wrong-version", "partial", "outside-link"} {
		t.Run(mode, func(t *testing.T) {
			c, _, s, path, _ := mixedPolicyHarness(t)
			var payload = map[string]any{"type": "thread_settings_applied", "thread_id": s.NativeID, "thread_settings": map[string]any{"cwd": s.Cwd, "model_provider_id": s.Route.Provider, "disabled_plugin_ids": []string{"retained@owned"}}}
			settings := payload["thread_settings"].(map[string]any)
			event := map[string]any{"type": "event_msg", "payload": payload}
			switch mode {
			case "missing":
				delete(settings, "disabled_plugin_ids")
			case "null":
				settings["disabled_plugin_ids"] = nil
			case "duplicate":
				settings["disabled_plugin_ids"] = []string{"retained@owned", "retained@owned"}
			case "wrong-id":
				payload["thread_id"] = "other"
			case "wrong-provider":
				settings["model_provider_id"] = "other"
			case "client-authored":
				event["metadata"] = map[string]bool{"client_authored": true}
			}
			b, _ := os.ReadFile(path)
			e, _ := json.Marshal(event)
			b = append(b, append(e, '\n')...)
			if mode == "wrong-version" {
				b = []byte(strings.ReplaceAll(string(b), "0.158.0", "other"))
			}
			if mode == "partial" {
				b = b[:len(b)-1]
			}
			if mode == "outside-link" {
				outside := filepath.Join(t.TempDir(), "policy.jsonl")
				if err := os.WriteFile(outside, b, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			v, err := c.Observe(context.Background(), s)
			if err == nil && (v.ExecutionClear || v.PluginEvidence[0].Verified) {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func TestCodexMixedPluginFreshPolicyTracksNativeSettingsEvents(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "missing-list"}[valid], func(t *testing.T) {
			c, f, s, path, ids := mixedPolicyHarness(t)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			settings := map[string]any{"cwd": s.Cwd, "model": "fixture", "modelProvider": "fixture", "approvalPolicy": "on-request", "sandboxPolicy": map[string]any{"type": "readOnly"}}
			if valid {
				*ids = append(*ids, "external@native")
				settings["disabledPluginIds"] = *ids
			}
			raw, _ := json.Marshal(map[string]any{"threadId": s.NativeID, "threadSettings": settings})
			f.events = append(f.events, codex.Message{Method: "thread/settings/updated", Params: raw})
			base := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "thread/resume" {
					return nil, &codex.RPCError{Code: -32600, Message: "no rollout found for thread id " + s.NativeID}
				}
				return base(method, params)
			}
			out, err := c.Execute(context.Background(), Intent{Kind: "send", ID: "fresh-input", Scope: s, Permission: "read_only", Text: "owned"})
			if valid && (err != nil || out.NativeTurn != "sent") {
				t.Fatalf("fresh policy not preserved: %+v %v", out, err)
			}
			if !valid && err == nil {
				t.Fatal("unreported fresh policy was treated as empty")
			}
		})
	}
}

func TestCodexMixedPluginAsyncAnswerPreservesWholePolicy(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{true: "unverified", false: "idle-answer"}[blocked], func(t *testing.T) {
			c, f, s, path, ids := mixedPolicyHarness(t)
			base := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				out, err := base(method, params)
				if method == "thread/read" && err == nil {
					thread := out.(map[string]any)["thread"].(map[string]any)
					question := map[string]any{"id": "question", "type": "agentMessage", "delivery": "async", "questions": []any{map[string]any{"title": "Continue?", "options": []string{"yes"}}}}
					thread["turns"] = []any{map[string]any{"id": "question-turn", "status": "completed", "items": []any{question}}}
				}
				return out, err
			}
			v, err := c.Observe(context.Background(), s)
			if err != nil || len(v.Pending) != 1 {
				t.Fatalf("question not observed: %+v %v", v, err)
			}
			if blocked {
				*ids = nil
				writeMixedPolicy(t, path, s, *ids)
			}
			out, err := c.Execute(context.Background(), Intent{Kind: "respond", ID: "answer", Scope: s, Permission: "read_only", Pending: &v.Pending[0], Answers: map[string][]string{"question-0": {"yes"}}})
			if !blocked && (err != nil || out.NativeTurn != "sent") {
				t.Fatalf("async policy lost: %+v %v", out, err)
			}
			if blocked && err == nil {
				t.Fatal("async input accepted unverified policy")
			}
		})
	}
}

func TestCodexMixedPluginCreateSavesWholePolicyWithoutInput(t *testing.T) {
	c, f, _ := nativeHarness(t)
	s := codexMixedScope(t)
	s.PluginSelections[0].Mode = "disabled"
	updates := 0
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "config/read":
			return map[string]any{"config": map[string]any{"features": map[string]bool{"plugins": true}}}, nil
		case "thread/start":
			return map[string]any{"thread": map[string]any{"id": "created"}, "model": "fixture", "modelProvider": "fixture", "sandbox": map[string]any{"type": "readOnly"}, "approvalPolicy": "on-request", "disabledPluginIds": []string{"retained@owned"}}, nil
		case "thread/settings/update":
			updates++
			b, _ := json.Marshal(params["disabledPluginIds"])
			var ids []string
			_ = json.Unmarshal(b, &ids)
			if params["threadId"] != "created" || len(params) != 2 || len(ids) != 2 || !slices.Contains(ids, "retained@owned") || !slices.Contains(ids, s.PluginSelections[0].NativeSource) {
				t.Fatalf("wrong whole policy: %+v", params)
			}
			return map[string]any{}, nil
		default:
			t.Fatalf("creation sent unrelated mutation: %s", method)
			return nil, nil
		}
	}
	r, err := c.Execute(context.Background(), Intent{ID: "mixed-create", Kind: "create", Scope: s, Permission: "read_only"})
	if err != nil || r.NativeID != "created" || updates != 1 {
		t.Fatalf("mixed selection did not save whole native policy: %+v %v updates=%d", r, err, updates)
	}
}
