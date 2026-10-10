//go:build linux && nativecodex

package runtime

import (
	"context"
	"encoding/json"
	"sync"
)

// RestrictNativeHookRequirements is a test-only transport boundary. Every
// request still reaches the owned native daemon; only its policy response gains
// a stricter managed-hooks-only requirement. This is not native MDM enforcement.
func (f *NativeApplicationFixture) RestrictNativeHookRequirements() *NativeHookRequirementsRestriction {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	p := &NativeHookRequirementsRestriction{nativeStdio: f.p}
	f.Runtime.clients[f.Instance.ID] = p
	return p
}

type NativeHookRequirementsRestriction struct {
	*nativeStdio
	mu      sync.Mutex
	samples []NativeHookRequirementsSample
}

type NativeHookRequirementsSample struct {
	Native    json.RawMessage `json:"native"`
	Delivered json.RawMessage `json:"delivered"`
}

func (p *NativeHookRequirementsRestriction) Samples() []NativeHookRequirementsSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]NativeHookRequirementsSample(nil), p.samples...)
}

func (p *NativeHookRequirementsRestriction) Call(ctx context.Context, method string, params, out any) error {
	if method != "configRequirements/read" {
		return p.nativeStdio.Call(ctx, method, params, out)
	}
	var original json.RawMessage
	if err := p.nativeStdio.Call(ctx, method, params, &original); err != nil {
		return err
	}
	var response struct {
		Requirements map[string]json.RawMessage `json:"requirements"`
	}
	if err := json.Unmarshal(original, &response); err != nil {
		return err
	}
	if response.Requirements == nil {
		response.Requirements = map[string]json.RawMessage{}
	}
	response.Requirements["allowManagedHooksOnly"] = json.RawMessage(`true`)
	delivered, err := json.Marshal(response)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.samples = append(p.samples, NativeHookRequirementsSample{Native: original, Delivered: delivered})
	p.mu.Unlock()
	if out != nil {
		return json.Unmarshal(delivered, out)
	}
	return nil
}
