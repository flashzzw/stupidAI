package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func skillContextHarness(t *testing.T) (*Codex, *nativeFixture, Scope, string, []any) {
	t.Helper()
	c, f, scope := nativeHarness(t)
	scope.Instance.Kind = "codex"
	scope.Instance.Profile = t.TempDir()
	scope.StateNamespace = scope.Instance.Profile
	path := filepath.Join(scope.Instance.Profile, "sessions", "owned.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scope.Instance.Profile, "plugins/cache/owned")
	body := "\n## Skills\n### Skill roots\n- `r0` = `" + root + "`\n### Available skills\n- fixture:method: Recorded description (file: r0/fixture/1/skills/method/SKILL.md)\n"
	rows := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "cli_version": "0.158.0", "model_provider": scope.Route.Provider}},
		map[string]any{"type": "world_state", "payload": map[string]any{"full": true, "state": map[string]any{"host_skills": map[string]any{"body": body, "includeInstructions": true}}}},
		map[string]any{"timestamp": "2026-10-04T00:00:00Z", "type": "turn_context", "payload": map[string]any{"turn_id": "one", "cwd": scope.Cwd}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "id": "native-load", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "<skill>\n<name>fixture:method</name>\n<path>" + root + "/fixture/1/skills/method/SKILL.md</path>\nPRIVATE_SKILL_BODY\n</skill>"}}, "internal_chat_message_metadata_passthrough": map[string]any{"turn_id": "one", "content_item_kinds": []string{"skills.selected_skill_instructions"}}}},
		map[string]any{"type": "world_state", "payload": map[string]any{"full": false, "state": map[string]any{"host_skills": map[string]any{"body": "\n## Skills\n### Available skills\n"}}}},
		map[string]any{"timestamp": "2026-10-04T00:00:01Z", "type": "turn_context", "payload": map[string]any{"turn_id": "two", "cwd": scope.Cwd}},
	}
	saveSkillContextRows(t, path, rows)
	f.call = func(method string, params map[string]any) (any, error) {
		if method != "thread/read" || params["includeTurns"] != false || params["threadId"] != scope.NativeID {
			t.Fatalf("context read mutated native state: %s", method)
		}
		return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "path": path}}, nil
	}
	return c, f, scope, path, rows
}

func saveSkillContextRows(t *testing.T, path string, rows []any) {
	t.Helper()
	var encoded strings.Builder
	for _, r := range rows {
		b, _ := json.Marshal(r)
		encoded.Write(b)
		encoded.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(encoded.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSkillContextKeepsDescriptionAndBodyHistorySeparate(t *testing.T) {
	c, f, scope, _, _ := skillContextHarness(t)
	root := filepath.Join(scope.Instance.Profile, "plugins/cache/owned")
	out, err := c.ReadSkillContext(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Complete || out.NativeID != scope.NativeID || len(out.Frames) != 2 {
		t.Fatalf("missing context: %+v", out)
	}
	first, last := out.Frames[0], out.Frames[1]
	if first.TurnID != "one" || len(first.Descriptions) != 1 || first.Descriptions[0].Path != root+"/fixture/1/skills/method/SKILL.md" || len(first.Loads) != 1 || first.Loads[0].Name != "fixture:method" || first.Loads[0].ContentSHA256 == "" {
		t.Fatalf("lost native body/description: %+v", first)
	}
	if last.TurnID != "two" || !last.DescriptionsReported || len(last.Descriptions) != 0 || len(last.Loads) != 0 {
		t.Fatalf("old history promoted to current context: %+v", last)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "PRIVATE_SKILL_BODY") || strings.Contains(string(b), "plugin_id") {
		t.Fatal("body leaked or plugin identity guessed from path")
	}
	if len(f.methods) != 2 {
		t.Fatal("context read did not recheck bound native identity")
	}
}

func TestCodexSkillContextRejectsIncompleteOrForeignEvidence(t *testing.T) {
	for _, mode := range []string{"foreign-path", "symlink-outside", "wrong-header", "wrong-version", "duplicate-header", "partial-line", "missing-full", "unknown-alias", "escaping-alias", "client-authored-load", "orphan-load", "changed-native", "replaced-file", "unknown-load-format", "spoofed-user-text", "unreported-context", "expanded-output"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope, path, rows := skillContextHarness(t)
			first := rows[0].(map[string]any)["payload"].(map[string]any)
			world := rows[1].(map[string]any)["payload"].(map[string]any)
			host := world["state"].(map[string]any)["host_skills"].(map[string]any)
			load := rows[3].(map[string]any)
			payload := load["payload"].(map[string]any)
			meta := payload["internal_chat_message_metadata_passthrough"].(map[string]any)
			switch mode {
			case "foreign-path":
				path = filepath.Join(t.TempDir(), "other.jsonl")
			case "symlink-outside":
				other := filepath.Join(t.TempDir(), "other.jsonl")
				saveSkillContextRows(t, other, rows)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			case "wrong-header":
				first["id"] = "different"
			case "wrong-version":
				first["cli_version"] = "future"
			case "duplicate-header":
				rows = append(rows, rows[0])
			case "partial-line":
			case "missing-full":
				world["full"] = false
			case "unknown-alias":
				host["body"] = strings.Replace(host["body"].(string), "file: r0/", "file: r9/", 1)
			case "escaping-alias":
				host["body"] = strings.Replace(host["body"].(string), "file: r0/", "file: r0/../", 1)
			case "client-authored-load":
				load["metadata"] = map[string]any{"client_authored": true}
			case "orphan-load":
				meta["turn_id"] = "other"
			case "unknown-load-format":
				payload["content"] = []any{map[string]any{"type": "input_text", "text": "PRIVATE_INVALID_BODY"}}
			case "spoofed-user-text":
				meta["content_item_kinds"] = []string{"user.text"}
			case "unreported-context":
				delete(world["state"].(map[string]any), "host_skills")
			case "expanded-output":
				body := "\n## Skills\n### Available skills\n"
				for i := 0; i < 100; i++ {
					body += "- method" + strconv.Itoa(i) + ": " + strings.Repeat("d", 1000) + " (file: /fixture/skill-" + strconv.Itoa(i) + "/SKILL.md)\n"
				}
				host["body"] = body
				rows = rows[:4]
				for i := 0; i < 60; i++ {
					rows = append(rows, map[string]any{"type": "turn_context", "payload": map[string]any{"turn_id": "extra-" + strconv.Itoa(i), "cwd": scope.Cwd}})
				}

			}
			if mode != "symlink-outside" {
				saveSkillContextRows(t, path, rows)
			}
			if mode == "partial-line" {
				b, _ := os.ReadFile(path)
				if err := os.WriteFile(path, b[:len(b)-1], 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			f.call = func(method string, params map[string]any) (any, error) {
				if method != "thread/read" || params["includeTurns"] != false {
					t.Fatal("read mutated native state")
				}
				calls++
				cwd := scope.Cwd
				if calls == 2 && mode == "changed-native" {
					cwd = "/changed"
				}
				if calls == 2 && mode == "replaced-file" {
					saveSkillContextRows(t, path+".new", rows)
					if err := os.Rename(path+".new", path); err != nil {
						t.Fatal(err)
					}
				}
				return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": cwd, "path": path}}, nil
			}
			out, err := c.ReadSkillContext(context.Background(), scope)
			if mode == "spoofed-user-text" || mode == "unreported-context" {
				if err != nil || !out.Complete {
					t.Fatal(err)
				}
				if mode == "spoofed-user-text" && len(out.Frames[0].Loads) != 0 {
					t.Fatal("user text claimed Skill body load")
				}
				if mode == "unreported-context" && out.Frames[0].DescriptionsReported {
					t.Fatal("missing context claimed empty descriptions")
				}
			} else if err == nil || out.Complete || len(out.Frames) > 0 {
				t.Fatalf("invalid context accepted: %s complete=%t frames=%d error=%v", mode, out.Complete, len(out.Frames), err)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), "PRIVATE_") || err != nil && strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("body leaked")
			}
		})
	}
}
