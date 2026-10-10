package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func awaitRequest(t *testing.T, p *Proxy, id json.RawMessage, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, ok := p.PendingRequest(id)
		if ok == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("request validity never became %v", want)
}

func TestRespondUsesOriginalStringOrNumericIDOnce(t *testing.T) {
	for _, id := range []json.RawMessage{json.RawMessage(`44`), json.RawMessage(`"approval-44"`)} {
		t.Run(string(id), func(t *testing.T) {
			received := make(chan Message, 1)
			p := proxyPair(t, func(ws *websocket.Conn) {
				_ = ws.WriteJSON(map[string]any{"id": id, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "test", "turnId": "turn"}})
				var response Message
				if ws.ReadJSON(&response) == nil {
					received <- response
				}
				_, _, _ = ws.ReadMessage()
			})
			awaitRequest(t, p, id, true)
			var wg sync.WaitGroup
			var succeeded atomic.Int32
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := p.Respond(context.Background(), id, map[string]string{"decision": "accept"})
					if err == nil {
						succeeded.Add(1)
					} else if !errors.Is(err, ErrRequestExpired) {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if succeeded.Load() != 1 {
				t.Fatalf("reply count %d", succeeded.Load())
			}
			select {
			case response := <-received:
				if string(response.ID) != string(id) || response.Method != "" || string(response.Result) != `{"decision":"accept"}` {
					t.Fatalf("wrong native response: %+v", response)
				}
			case <-time.After(time.Second):
				t.Fatal("native response not received")
			}
		})
	}
}

func TestResolvedAndDisconnectedRequestsCannotBeReplied(t *testing.T) {
	id := json.RawMessage(`12`)
	clear := make(chan struct{})
	p := proxyPair(t, func(ws *websocket.Conn) {
		_ = ws.WriteJSON(map[string]any{"id": id, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "test"}})
		<-clear
		_ = ws.WriteJSON(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "test", "requestId": id}})
		_, _, _ = ws.ReadMessage()
	})
	awaitRequest(t, p, id, true)
	close(clear)
	awaitRequest(t, p, id, false)
	if err := p.Respond(context.Background(), id, map[string]any{}); !errors.Is(err, ErrRequestExpired) {
		t.Fatal(err)
	}
	p.Close()
	if err := p.Respond(context.Background(), id, map[string]any{}); !errors.Is(err, ErrRequestExpired) {
		t.Fatal(err)
	}
}
