//go:build linux && nativecodex

package runtime

import (
	"context"
	"encoding/json"
)

// InvalidateRetirementReceipt keeps the owned native connection alive while
// withholding fields required to acknowledge one real detach or resume. The
// complete native reply is captured only as test evidence, never as authority
// given to Runtime. It does not simulate a separate shared daemon/client.
func (f *NativeApplicationFixture) InvalidateRetirementReceipt(nativeID, method string) <-chan NativeConfigurationReply {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	p := &invalidRetirementReceipt{codexConnection: f.Runtime.clients[f.Instance.ID], nativeID: nativeID, method: method, received: make(chan NativeConfigurationReply, 1)}
	f.Runtime.clients[f.Instance.ID] = p
	return p.received
}

type invalidRetirementReceipt struct {
	codexConnection
	nativeID, method string
	received         chan NativeConfigurationReply
	done             bool
}

func (p *invalidRetirementReceipt) Call(ctx context.Context, method string, params, out any) error {
	raw := mustNativeJSON(params)
	var request struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(raw, &request)
	if p.done || method != p.method || request.ThreadID != p.nativeID {
		return p.codexConnection.Call(ctx, method, params, out)
	}
	var result json.RawMessage
	if err := p.codexConnection.Call(ctx, method, params, &result); err != nil {
		return err
	}
	p.done = true
	p.received <- NativeConfigurationReply{Params: raw, Result: result}
	return json.Unmarshal([]byte(`{}`), out)
}
