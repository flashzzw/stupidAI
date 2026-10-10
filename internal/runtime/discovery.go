package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"codex-netcap/internal/codex"
)

// Discovery exposes native metadata, not a grant to attach or send input.
// Every request names one configured state namespace and one project.
type DiscoveryRequest struct {
	Instance Instance
	Cwd      string
	Cursor   string
	Limit    int
	Archived bool
}

type NativeSession struct {
	NativeID     string `json:"native_id"`
	Title        string `json:"title"`
	Cwd          string `json:"cwd"`
	Source       string `json:"source"`
	NativeStatus string `json:"native_status"`
	Provider     string `json:"provider,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type DiscoveryPage struct {
	Sessions []NativeSession `json:"sessions"`
	Next     string          `json:"next_cursor,omitempty"`
}

type NativeDiscovery interface {
	Discover(context.Context, DiscoveryRequest) (DiscoveryPage, error)
	InspectNative(context.Context, DiscoveryRequest, string) (NativeSession, error)
}

func (a *Adapters) discovery(request DiscoveryRequest) (NativeDiscovery, error) {
	r, err := a.adapter(request.Instance.Kind)
	if err != nil {
		return nil, err
	}
	d, ok := r.(NativeDiscovery)
	if !ok {
		return nil, fmt.Errorf("该适配器尚未接入原生会话发现")
	}
	return d, nil
}

func (a *Adapters) Discover(ctx context.Context, request DiscoveryRequest) (DiscoveryPage, error) {
	d, err := a.discovery(request)
	if err != nil {
		return DiscoveryPage{}, err
	}
	return d.Discover(ctx, request)
}

func (a *Adapters) InspectNative(ctx context.Context, request DiscoveryRequest, id string) (NativeSession, error) {
	d, err := a.discovery(request)
	if err != nil {
		return NativeSession{}, err
	}
	return d.InspectNative(ctx, request, id)
}

type discoveryThread struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Preview   string          `json:"preview"`
	Cwd       string          `json:"cwd"`
	Source    json.RawMessage `json:"source"`
	Status    codex.Status    `json:"status"`
	Provider  string          `json:"modelProvider"`
	UpdatedAt int64           `json:"updatedAt"`
}

func (t discoveryThread) metadata() NativeSession {
	source := "unknown"
	var name string
	if json.Unmarshal(t.Source, &name) == nil {
		switch name {
		case "cli", "vscode", "exec", "appServer", "unknown":
			source = name
		}
	} else {
		var object map[string]json.RawMessage
		if json.Unmarshal(t.Source, &object) == nil {
			if _, ok := object["subAgent"]; ok {
				source = "subAgent"
			}
			if _, ok := object["custom"]; ok {
				source = "custom"
			}
		}
	}
	title := t.Name
	if title == "" {
		title = t.Preview
	}
	result := NativeSession{NativeID: t.ID, Title: title, Cwd: t.Cwd, Source: source, NativeStatus: t.Status.Type, Provider: t.Provider}
	if t.UpdatedAt > 0 {
		result.UpdatedAt = time.Unix(t.UpdatedAt, 0).UTC().Format(time.RFC3339Nano)
	}
	return result
}

func (c *Codex) Discover(ctx context.Context, request DiscoveryRequest) (DiscoveryPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, request.Instance)
	if err != nil {
		return DiscoveryPage{}, err
	}
	var result struct {
		Data []discoveryThread `json:"data"`
		Next string            `json:"nextCursor"`
	}
	params := map[string]any{"cwd": request.Cwd, "limit": request.Limit, "archived": request.Archived,
		"sourceKinds":    []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"},
		"modelProviders": []string{}, "sortKey": "updated_at", "sortDirection": "desc", "useStateDbOnly": true}
	if request.Cursor != "" {
		params["cursor"] = request.Cursor
	}
	if err = p.Call(ctx, "thread/list", params, &result); err != nil {
		return DiscoveryPage{}, err
	}
	out := DiscoveryPage{Sessions: []NativeSession{}, Next: result.Next}
	for _, thread := range result.Data {
		out.Sessions = append(out.Sessions, thread.metadata())
	}
	return out, nil
}

func (c *Codex) InspectNative(ctx context.Context, request DiscoveryRequest, id string) (NativeSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, request.Instance)
	if err != nil {
		return NativeSession{}, err
	}
	var result struct {
		Thread discoveryThread `json:"thread"`
	}
	if err = p.Call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &result); err != nil {
		return NativeSession{}, err
	}
	return result.Thread.metadata(), nil
}

func (c *Claude) Discover(ctx context.Context, request DiscoveryRequest) (DiscoveryPage, error) {
	if request.Archived {
		return DiscoveryPage{}, fmt.Errorf("Claude SDK 未提供与 Codex 相同的归档筛选")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastUse[request.Instance.ID] = time.Now()
	p, err := c.client(ctx, request.Instance)
	if err != nil {
		return DiscoveryPage{}, err
	}
	var out DiscoveryPage
	err = p.call(ctx, "discover", map[string]any{"cwd": request.Cwd, "cursor": request.Cursor, "limit": request.Limit}, &out)
	return out, err
}

func (c *Claude) InspectNative(ctx context.Context, request DiscoveryRequest, id string) (NativeSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastUse[request.Instance.ID] = time.Now()
	p, err := c.client(ctx, request.Instance)
	if err != nil {
		return NativeSession{}, err
	}
	var out NativeSession
	err = p.call(ctx, "inspect_native", map[string]any{"cwd": request.Cwd, "native_id": id}, &out)
	return out, err
}
