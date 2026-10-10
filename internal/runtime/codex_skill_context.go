package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

func skillContextError() error {
	return fmt.Errorf("原生 Skill 上下文不完整、超出读取上限、格式不受支持或身份不匹配；未返回部分记录")
}

func (c *Codex) ReadSkillContext(ctx context.Context, scope Scope) (SkillContext, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || scope.NativeID == "" || !filepath.IsAbs(scope.Cwd) || scope.StateNamespace == "" {
		return SkillContext{}, skillContextError()
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return SkillContext{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return SkillContext{}, fmt.Errorf("无法连接已绑定的 Codex 后台读取 Skill 上下文")
	}
	data, err := readCodexRollout(ctx, p, scope)
	if err != nil {
		return SkillContext{}, skillContextError()
	}
	frames, err := readCodexSkillFrames(ctx, scope, data)
	if err != nil {
		return SkillContext{}, err
	}
	hash := sha256.Sum256(data)
	return SkillContext{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.rollout.skill_context", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, RolloutSHA256: hex.EncodeToString(hash[:]), Frames: frames}, nil
}

func readCodexSkillFrames(ctx context.Context, scope Scope, data []byte) ([]SkillContextFrame, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 65536), 4<<20)
	owned, full, reported := false, false, false
	descriptions := []SkillContextDescription{}
	frames := []SkillContextFrame{}
	projectedBytes := 0
	positions := map[string]int{}
	loads := map[string]bool{}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var entry struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Payload   json.RawMessage `json:"payload"`
			Metadata  struct {
				ClientAuthored *bool `json:"client_authored"`
			} `json:"metadata"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			return nil, skillContextError()
		}
		if !owned && entry.Type != "session_meta" {
			return nil, skillContextError()
		}
		switch entry.Type {
		case "session_meta":
			var meta struct {
				ID       string `json:"id"`
				Cwd      string `json:"cwd"`
				Version  string `json:"cli_version"`
				Provider string `json:"model_provider"`
			}
			if owned || json.Unmarshal(entry.Payload, &meta) != nil || meta.ID != scope.NativeID || meta.Cwd != scope.Cwd || meta.Version != "0.158.0" || meta.Provider != scope.Route.Provider {
				return nil, skillContextError()
			}
			owned = true
		case "world_state":
			var state struct {
				Full  *bool                      `json:"full"`
				State map[string]json.RawMessage `json:"state"`
			}
			if json.Unmarshal(entry.Payload, &state) != nil || state.Full == nil || state.State == nil || !full && !*state.Full {
				return nil, skillContextError()
			}
			if *state.Full {
				full = true
				reported = false
				descriptions = []SkillContextDescription{}
			}
			if raw, ok := state.State["host_skills"]; ok {
				if string(raw) == "null" {
					reported = false
					descriptions = []SkillContextDescription{}
					continue
				}
				var patch map[string]json.RawMessage
				if json.Unmarshal(raw, &patch) != nil || patch == nil {
					return nil, skillContextError()
				}
				if content, exists := patch["body"]; exists {
					var body string
					if string(content) == "null" || json.Unmarshal(content, &body) != nil {
						return nil, skillContextError()
					}
					var err error
					descriptions, err = skillContextDescriptions(body)
					if err != nil {
						return nil, err
					}
					reported = true
				}
			}
		case "turn_context":
			var turn struct {
				ID  string `json:"turn_id"`
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(entry.Payload, &turn) != nil || !skillContextText(turn.ID, 300) || turn.Cwd != scope.Cwd || len(frames) >= 200 {
				return nil, skillContextError()
			}
			if entry.Timestamp != "" {
				if _, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err != nil {
					return nil, skillContextError()
				}
			}
			for _, d := range descriptions {
				projectedBytes += len(d.Name) + len(d.Description) + len(d.Path)
			}
			if projectedBytes > 4<<20 {
				return nil, skillContextError()
			}
			frames = append(frames, SkillContextFrame{TurnID: turn.ID, RecordedAt: entry.Timestamp, DescriptionsReported: full && reported, Descriptions: append([]SkillContextDescription{}, descriptions...), Loads: []SkillBodyLoad{}})
			positions[turn.ID] = len(frames) - 1
		case "response_item":
			var item struct {
				Type    string `json:"type"`
				ID      string `json:"id"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Meta struct {
					TurnID string   `json:"turn_id"`
					Kinds  []string `json:"content_item_kinds"`
				} `json:"internal_chat_message_metadata_passthrough"`
			}
			if json.Unmarshal(entry.Payload, &item) != nil {
				return nil, skillContextError()
			}
			if !slices.Contains(item.Meta.Kinds, "skills.selected_skill_instructions") {
				continue
			}
			index, ok := positions[item.Meta.TurnID]
			if !ok || item.Type != "message" || item.Role != "user" || !skillContextText(item.ID, 300) || loads[item.ID] || len(item.Meta.Kinds) != len(item.Content) || entry.Metadata.ClientAuthored != nil && *entry.Metadata.ClientAuthored {
				return nil, skillContextError()
			}
			loads[item.ID] = true
			for n, kind := range item.Meta.Kinds {
				if kind != "skills.selected_skill_instructions" {
					continue
				}
				if item.Content[n].Type != "input_text" || len(loads) > 2048 {
					return nil, skillContextError()
				}
				load, err := skillContextLoad(item.ID, item.Content[n].Text)
				if err != nil {
					return nil, err
				}
				frames[index].Loads = append(frames[index].Loads, load)
			}
		}
	}
	if scanner.Err() != nil || !owned {
		return nil, skillContextError()
	}
	return frames, nil
}

var skillRootLine = regexp.MustCompile("^- `(r[0-9]+)` = `([^`]+)`$")
var skillDescriptionLine = regexp.MustCompile(`^- (.+?): (.*) \(file: ([^\r\n]+)\)$`)

func skillContextText(s string, max int) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}
func skillContextPath(s string) bool {
	return skillContextText(s, 16384) && filepath.IsAbs(s) && filepath.Clean(s) == s
}

func skillContextDescriptions(body string) ([]SkillContextDescription, error) {
	roots := map[string]string{}
	out := []SkillContextDescription{}
	section := ""
	found := false
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if line == "### Skill roots" {
			section = "roots"
			continue
		}
		if line == "### Available skills" {
			if found {
				return nil, skillContextError()
			}
			found = true
			section = "skills"
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if section == "" {
			continue
		}
		if section == "roots" {
			m := skillRootLine.FindStringSubmatch(line)
			if m == nil || roots[m[1]] != "" || !skillContextPath(m[2]) {
				return nil, skillContextError()
			}
			roots[m[1]] = m[2]
			continue
		}
		m := skillDescriptionLine.FindStringSubmatch(line)
		if m == nil || !skillContextText(m[1], 300) || len(m[2]) > 8000 || len(out) >= 2048 {
			return nil, skillContextError()
		}
		path := m[3]
		if !filepath.IsAbs(path) {
			alias, rel, ok := strings.Cut(path, "/")
			if !ok || roots[alias] == "" || !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
				return nil, skillContextError()
			}
			path = filepath.Join(roots[alias], rel)
		}
		if !skillContextPath(path) || seen[path] {
			return nil, skillContextError()
		}
		seen[path] = true
		out = append(out, SkillContextDescription{Name: m[1], Description: m[2], Path: path})
	}
	if !found {
		return nil, skillContextError()
	}
	return out, nil
}

func skillContextLoad(id, text string) (SkillBodyLoad, error) {
	const prefix = "<skill>\n<name>"
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, "\n</skill>") {
		return SkillBodyLoad{}, skillContextError()
	}
	name, rest, ok := strings.Cut(strings.TrimPrefix(text, prefix), "</name>\n<path>")
	if !ok || !skillContextText(name, 300) {
		return SkillBodyLoad{}, skillContextError()
	}
	path, body, ok := strings.Cut(rest, "</path>\n")
	if !ok || !skillContextPath(path) {
		return SkillBodyLoad{}, skillContextError()
	}
	hash := sha256.Sum256([]byte(text))
	bodyHash := sha256.Sum256([]byte(strings.TrimSuffix(body, "\n</skill>")))
	return SkillBodyLoad{ID: id, Name: name, Path: path, ContentSHA256: hex.EncodeToString(hash[:]), BodySHA256: hex.EncodeToString(bodyHash[:])}, nil
}
