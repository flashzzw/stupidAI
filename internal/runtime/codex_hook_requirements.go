package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Private local plugin caches are not managed hook sources. Read the native
// requirements separately: config/read contains configuration, not every forced
// policy. Keep the complete requirements in the fingerprint, never in UI output.
func codexHookRequirements(ctx context.Context, p codexConnection, scope Scope) (map[string]any, error) {
	requirements, err := readCodexHookRequirements(ctx, p)
	if err != nil {
		return nil, err
	}
	onlyManaged := requirements != nil && requirements["allowManagedHooksOnly"] == true
	if scope.hookDeactivation {
		if !onlyManaged {
			return nil, fmt.Errorf("准备撤去 hook 期间受管策略已变化，请重新核对")
		}
		return requirements, nil
	}
	if onlyManaged {
		for _, pack := range scope.PluginSelections {
			if pack.Hooks != nil && pack.Mode == "enabled" {
				return nil, &codexManagedHooksOnly{}
			}
		}
	}
	return requirements, nil
}

type codexManagedHooksOnly struct{}

func (*codexManagedHooksOnly) Error() string {
	return "原生策略仅允许受管 hook；此本地插件不能启用，信任审核不能覆盖该限制"
}

func readCodexHookRequirements(ctx context.Context, p codexConnection) (map[string]any, error) {
	var response struct {
		Requirements json.RawMessage `json:"requirements"`
	}
	if err := p.Call(ctx, "configRequirements/read", map[string]any{}, &response); err != nil || len(response.Requirements) == 0 || len(response.Requirements) > 4<<20 {
		return nil, fmt.Errorf("无法核对原生 hook 受管策略，未确认本地插件可用")
	}
	var requirements map[string]any
	decoder := json.NewDecoder(bytes.NewReader(response.Requirements))
	decoder.UseNumber()
	if err := decoder.Decode(&requirements); err != nil {
		return nil, fmt.Errorf("原生 hook 受管策略响应无效")
	}
	if requirements == nil {
		return nil, nil // The native response explicitly reports no requirements.
	}
	onlyManaged, exists := requirements["allowManagedHooksOnly"]
	if !exists {
		return nil, fmt.Errorf("原生策略未提供可核对的 hook 来源限制")
	}
	if onlyManaged != nil {
		_, valid := onlyManaged.(bool)
		if !valid {
			return nil, fmt.Errorf("原生 hook 来源限制格式无效")
		}
	}
	return requirements, nil
}
