package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func proxyPair(t *testing.T, serve func(*websocket.Conn)) *Proxy {
	t.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		defer close(done)
		serve(ws)
	}))
	t.Cleanup(server.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := newProxy(ws)
	t.Cleanup(func() {
		p.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server reader leaked")
		}
	})
	return p
}
func TestCorrelatesConcurrentResponsesAndServerRequests(t *testing.T) {
	p := proxyPair(t, func(ws *websocket.Conn) {
		var requests []Message
		for range 8 {
			var m Message
			if ws.ReadJSON(&m) != nil {
				return
			}
			requests = append(requests, m)
		}
		_ = ws.WriteJSON(map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": "human"}})
		_ = ws.WriteJSON(map[string]any{"id": 44, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "human"}})
		for i := len(requests) - 1; i >= 0; i-- {
			m := requests[i]
			_ = ws.WriteJSON(map[string]any{"id": m.ID, "result": m.Params})
		}
		_, _, _ = ws.ReadMessage()
	})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var out map[string]int
			if err := p.Call(ctx, "echo", map[string]int{"n": i}, &out); err != nil {
				t.Error(err)
			} else if out["n"] != i {
				t.Errorf("response crossed requests: %+v", out)
			}
		}()
	}
	wg.Wait()
	events := p.Drain()
	if len(events) != 2 || events[1].Method != "item/commandExecution/requestApproval" {
		t.Fatalf("lost server request: %+v", events)
	}
}
func TestTimeoutIsUnknownAndClosesConnection(t *testing.T) {
	p := proxyPair(t, func(ws *websocket.Conn) { var m Message; _ = ws.ReadJSON(&m); _, _, _ = ws.ReadMessage() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := p.Call(ctx, "mutate", map[string]any{}, new(any))
	var unknown *Unknown
	if !errors.As(err, &unknown) {
		t.Fatalf("timeout not uncertain: %v", err)
	}
	if err = p.Call(context.Background(), "again", nil, new(any)); !errors.As(err, &unknown) {
		t.Fatal("connection reused after timeout")
	}
}
func TestRPCRejectionDistinctFromUnknown(t *testing.T) {
	p := proxyPair(t, func(ws *websocket.Conn) {
		var m Message
		_ = ws.ReadJSON(&m)
		_ = ws.WriteJSON(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "unsupported"}})
		_, _, _ = ws.ReadMessage()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rpc *RPCError
	err := p.Call(ctx, "missing", nil, new(any))
	if !errors.As(err, &rpc) || rpc.Code != -32601 {
		t.Fatal(err)
	}
}

type scriptClient func(context.Context, string, any, any) error

func (c scriptClient) Call(ctx context.Context, m string, p, o any) error { return c(ctx, m, p, o) }
func (scriptClient) Drain() []Message                                     { return nil }
func (scriptClient) Close() error                                         { return nil }
func TestItemEnvelopeAndPaginationMatch0158Schema(t *testing.T) {
	calls := 0
	c := scriptClient(func(_ context.Context, m string, p, out any) error {
		calls++
		if m != "thread/items/list" {
			return fmt.Errorf("unexpected %s", m)
		}
		params := p.(map[string]any)
		body := `{"data":[{"turnId":"t","item":{"id":"u","type":"userMessage","clientId":"ours"}}],"nextCursor":"next"}`
		if calls == 2 {
			if params["cursor"] != "next" {
				t.Fatal("missing cursor")
			}
			body = `{"data":[{"turnId":"t","item":{"id":"a","type":"agentMessage","text":"OK","phase":"final_answer"}}],"nextCursor":null}`
		}
		return json.Unmarshal([]byte(body), out)
	})
	items, err := Items(context.Background(), c, "thread", "t")
	if err != nil {
		t.Fatal(err)
	}
	turn := Turn{Items: items}
	if len(items) != 2 || !turn.HasInput("ours") || turn.Answer() != "OK" {
		t.Fatalf("wrong item envelope: %+v", items)
	}
}

func TestLegacyHistoryFallbackKeepsOrderAndInputs(t *testing.T) {
	c := scriptClient(func(_ context.Context, m string, p, out any) error {
		if m == "thread/turns/list" || m == "thread/items/list" {
			return &RPCError{Code: -32601, Message: "list_turns is not supported yet"}
		}
		if m != "thread/read" || p.(map[string]any)["includeTurns"] != true {
			return fmt.Errorf("unexpected %s", m)
		}
		return json.Unmarshal([]byte(`{"thread":{"id":"h","turns":[{"id":"old","status":"completed","items":[]},{"id":"new","status":"inProgress","items":[{"id":"u","type":"userMessage","clientId":"ours"}]}]}}`), out)
	})
	turns, err := Turns(context.Background(), c, "h", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].ID != "new" {
		t.Fatal(turns)
	}
	items, err := Items(context.Background(), c, "h", "new")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ClientID != "ours" {
		t.Fatal(items)
	}
}

func TestUnmaterializedThreadHasNoHistoryOnlyWhenIdle(t *testing.T) {
	for _, status := range []string{"idle", "active", "notLoaded"} {
		t.Run(status, func(t *testing.T) {
			c := scriptClient(func(_ context.Context, m string, p, out any) error {
				if m == "thread/turns/list" {
					return &RPCError{Code: -32600, Message: "thread h is not materialized yet; thread/turns/list is unavailable before first user message"}
				}
				if m == "thread/read" {
					return json.Unmarshal([]byte(`{"thread":{"id":"h","status":{"type":"`+status+`"}}}`), out)
				}
				return fmt.Errorf("unexpected %s", m)
			})
			turns, err := Turns(context.Background(), c, "h", 0, true)
			if status == "idle" {
				if err != nil || len(turns) != 0 {
					t.Fatalf("idle new thread: %v %v", turns, err)
				}
			} else if err == nil {
				t.Fatal("treated unknown history as empty")
			}
		})
	}
}
