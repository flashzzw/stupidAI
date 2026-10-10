package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"codex-netcap/internal/codex"
)

// Selection readiness is different from a turn's prepared Skill context.
type codexPluginPolicy struct {
	DisabledIDs []string
}

func codexMixedPlugins(packs []PluginSelection) bool {
	return slices.ContainsFunc(packs, func(p PluginSelection) bool {
		return slices.ContainsFunc(p.Components, func(c PluginComponent) bool { return c.Kind == "skill" || c.Kind == "hook" })
	})
}

func decodeCodexPluginIDs(raw json.RawMessage) ([]string, error) {
	var ids []string
	if json.Unmarshal(raw, &ids) != nil || ids == nil || len(ids) > 1024 {
		return nil, fmt.Errorf("原生插件禁用列表缺失或无效")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !skillContextText(id, 500) || strings.TrimSpace(id) != id || !strings.Contains(id, "@") || seen[id] {
			return nil, fmt.Errorf("原生插件禁用列表存在无效或重复来源")
		}
		seen[id] = true
	}
	return ids, nil
}

func mergeCodexPluginPolicy(ids []string, target, previous []PluginSelection) []string {
	out := append([]string{}, ids...)
	set := func(id string, disabled bool) {
		out = slices.DeleteFunc(out, func(existing string) bool { return existing == id })
		if disabled {
			out = append(out, id)
		}
	}
	// Private retired versions cannot reappear if another client keeps an old
	// configuration loaded. Unrelated/native inherited IDs retain their policy.
	for _, p := range previous {
		set(p.NativeSource, true)
	}
	for _, p := range target {
		set(p.NativeSource, p.Mode == "disabled")
	}
	return out
}

func (p *codexPluginPolicy) matches(packs []PluginSelection) bool {
	if p == nil {
		return false
	}
	for _, pack := range packs {
		if slices.Contains(p.DisabledIDs, pack.NativeSource) != (pack.Mode == "disabled") {
			return false
		}
	}
	return true
}

func (c *Codex) readPluginPolicy(ctx context.Context, p codexConnection, scope Scope) (*codexPluginPolicy, error) {
	c.collect(scope.Instance, p)
	data, err := readCodexRollout(ctx, p, scope)
	if errors.Is(err, errCodexRolloutNotMaterialized) {
		thread, e := codex.Read(ctx, p, scope.NativeID)
		c.collect(scope.Instance, p)
		initial, ok := c.creationPolicies[scope.Instance.ID+"/"+scope.NativeID]
		if !ok || initial.ConnectionID != p.ConnectionID() || e != nil || thread.ID != scope.NativeID || thread.Status.Type != "idle" || thread.Cwd != initial.Cwd || thread.Model != initial.Model || thread.Provider != initial.Provider || scope.Cwd != initial.Cwd || scope.Route.Model != initial.Model || scope.Route.Provider != initial.Provider {
			return nil, fmt.Errorf("尚无可核对的原生插件策略")
		}
		ids, e := decodeCodexPluginIDs(initial.DisabledPluginIDs)
		if e != nil {
			return nil, e
		}
		return &codexPluginPolicy{DisabledIDs: ids}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("插件策略日志不完整、范围无效或身份已变化")
	}
	// Once materialized, loss or corruption of the log cannot revive an older
	// creation acknowledgement, even if its history projection has no turns.
	delete(c.creationPolicies, scope.Instance.ID+"/"+scope.NativeID)
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 65536), 4<<20)
	owned := false
	var latest []string
	for scan.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var entry struct {
			Type     string          `json:"type"`
			Payload  json.RawMessage `json:"payload"`
			Metadata struct {
				ClientAuthored bool `json:"client_authored"`
			} `json:"metadata"`
		}
		if json.Unmarshal(scan.Bytes(), &entry) != nil || !owned && entry.Type != "session_meta" {
			return nil, fmt.Errorf("原生插件策略日志格式无效")
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
				return nil, fmt.Errorf("原生插件策略日志身份或版本不匹配")
			}
			owned = true
		case "turn_context":
			var turn struct {
				ID  string          `json:"turn_id"`
				Cwd string          `json:"cwd"`
				IDs json.RawMessage `json:"disabled_plugin_ids"`
			}
			if json.Unmarshal(entry.Payload, &turn) != nil || entry.Metadata.ClientAuthored || !skillContextText(turn.ID, 300) || turn.Cwd != scope.Cwd {
				return nil, fmt.Errorf("原生轮次插件策略无效")
			}
			latest, err = decodeCodexPluginIDs(turn.IDs)
			if err != nil {
				return nil, err
			}
		case "event_msg":
			var event struct {
				Type     string `json:"type"`
				ID       string `json:"thread_id"`
				Settings struct {
					IDs      json.RawMessage `json:"disabled_plugin_ids"`
					Cwd      string          `json:"cwd"`
					Provider string          `json:"model_provider_id"`
				} `json:"thread_settings"`
			}
			if json.Unmarshal(entry.Payload, &event) != nil {
				return nil, fmt.Errorf("原生插件策略事件无效")
			}
			if event.Type != "thread_settings_applied" {
				continue
			}
			if entry.Metadata.ClientAuthored || event.ID != scope.NativeID || event.Settings.Cwd != scope.Cwd || event.Settings.Provider != scope.Route.Provider {
				return nil, fmt.Errorf("原生插件策略事件不属于当前执行")
			}
			latest, err = decodeCodexPluginIDs(event.Settings.IDs)
			if err != nil {
				return nil, err
			}
		}
	}
	if scan.Err() != nil || !owned || latest == nil {
		return nil, fmt.Errorf("原生未报告完整插件策略")
	}
	return &codexPluginPolicy{DisabledIDs: latest}, nil
}

func (c *Codex) addInputPluginPolicy(ctx context.Context, p codexConnection, scope Scope, params map[string]any) error {
	if !codexMixedPlugins(scope.PluginSelections) {
		return nil
	}
	policy, err := c.readPluginPolicy(ctx, p, scope)
	if err != nil {
		return err
	}
	if !policy.matches(scope.PluginSelections) {
		return fmt.Errorf("整包原生策略与保存选择不一致，不能输入")
	}
	params["disabledPluginIds"] = mergeCodexPluginPolicy(policy.DisabledIDs, scope.PluginSelections, nil)
	return nil
}

func (c *Codex) collectCreationPluginPolicy(i Instance, event codex.Message) {
	var changed struct {
		ID       string `json:"threadId"`
		Settings struct {
			IDs      json.RawMessage `json:"disabledPluginIds"`
			Cwd      string          `json:"cwd"`
			Model    string          `json:"model"`
			Provider string          `json:"modelProvider"`
			Approval json.RawMessage `json:"approvalPolicy"`
			Sandbox  json.RawMessage `json:"sandboxPolicy"`
		} `json:"threadSettings"`
	}
	if json.Unmarshal(event.Params, &changed) != nil || changed.ID == "" {
		for key := range c.creationPolicies {
			if strings.HasPrefix(key, i.ID+"/") {
				delete(c.creationPolicies, key)
			}
		}
		return
	}
	key := i.ID + "/" + changed.ID
	initial, ok := c.creationPolicies[key]
	if !ok {
		return
	}
	s := changed.Settings
	_, err := decodeCodexPluginIDs(s.IDs)
	_, permissionErr := verifyCodexRecoveryPermission(Scope{RecoveryPermission: initial.Evidence.ExpectedMode}, codex.Started{ApprovalPolicy: s.Approval, Sandbox: s.Sandbox})
	if err != nil || permissionErr != nil || initial.Cwd != s.Cwd || initial.Model != s.Model || initial.Provider != s.Provider {
		delete(c.creationPolicies, key)
		return
	}
	initial.DisabledPluginIDs = append(json.RawMessage{}, s.IDs...)
	c.creationPolicies[key] = initial
}

func (c *Codex) initializePluginPolicy(ctx context.Context, p codexConnection, scope Scope, nativeID string, raw json.RawMessage) error {
	ids, err := decodeCodexPluginIDs(raw)
	if err != nil {
		return err
	}
	next := mergeCodexPluginPolicy(ids, scope.PluginSelections, nil)
	encoded, _ := json.Marshal(next)
	if _, err := decodeCodexPluginIDs(encoded); err != nil {
		return err
	}
	if !slices.Equal(ids, next) {
		if err = p.Call(ctx, "thread/settings/update", map[string]any{"threadId": nativeID, "disabledPluginIds": next}, nil); err != nil {
			return err
		}
	}
	key := scope.Instance.ID + "/" + nativeID
	initial := c.creationPolicies[key]
	initial.DisabledPluginIDs, _ = json.Marshal(next)
	c.creationPolicies[key] = initial
	return nil
}
