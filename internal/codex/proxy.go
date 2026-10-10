package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"codex-netcap/internal/config"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Method  string `json:"-"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("RPC %d %s: %s", e.Code, e.Method, e.Message) }

// Unknown means bytes may have reached Codex. Retrying a mutation is unsafe.
type Unknown struct{ Cause error }

func (e *Unknown) Error() string { return "Codex 操作结果未知: " + e.Cause.Error() }
func (e *Unknown) Unwrap() error { return e.Cause }

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
type Client interface {
	Call(context.Context, string, any, any) error
	Drain() []Message
	Close() error
}

type pipeConn struct {
	r, w *os.File
	once sync.Once
	stop func()
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeConn) Close() error {
	p.once.Do(func() {
		p.r.Close()
		p.w.Close()
		if p.stop != nil {
			p.stop()
		}
	})
	return nil
}

type addr string

func (a addr) Network() string           { return "pipe" }
func (a addr) String() string            { return string(a) }
func (p *pipeConn) LocalAddr() net.Addr  { return addr("guard") }
func (p *pipeConn) RemoteAddr() net.Addr { return addr("codex-cli-proxy") }
func (p *pipeConn) SetDeadline(t time.Time) error {
	return errors.Join(p.SetReadDeadline(t), p.SetWriteDeadline(t))
}
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return p.r.SetReadDeadline(t) }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return p.w.SetWriteDeadline(t) }

type Proxy struct {
	ws           *websocket.Conn
	mu           sync.Mutex
	write        sync.Mutex
	pending      map[string]chan Message
	requests     map[string]Message
	connectionID string
	events       []Message
	next         uint64
	done         chan struct{}
	once         sync.Once
	readErr      error
}

func Connect(ctx context.Context, c config.Config) (*Proxy, error) {
	return ConnectEnv(ctx, c, c.Env())
}

// ConnectEnv lets the console supply an isolated environment without changing
// the legacy guard's configuration inheritance.
func ConnectEnv(ctx context.Context, c config.Config, env []string) (*Proxy, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return nil, err
	}
	cmd := exec.Command(c.CodexBin, "app-server", "proxy", "--sock", c.Socket)
	cmd.Env = env
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return nil, err
	}
	stdinR.Close()
	stdoutW.Close()
	wait := make(chan struct{})
	go func() { _ = cmd.Wait(); close(wait) }()
	pc := &pipeConn{r: stdoutR, w: stdinW, stop: func() { _ = cmd.Process.Kill(); <-wait }}
	dialer := websocket.Dialer{HandshakeTimeout: c.RPCTimeout(), NetDialContext: func(context.Context, string, string) (net.Conn, error) { return pc, nil }}
	ws, resp, err := dialer.DialContext(ctx, "ws://codex-cli-proxy/", nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("CLI proxy 握手失败: %w", err)
	}
	p := newProxy(ws)
	var init struct {
		CodexHome string `json:"codexHome"`
	}
	if err = p.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "netcap_go", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}}, &init); err != nil {
		p.Close()
		return nil, err
	}
	if init.CodexHome != "" && init.CodexHome != c.CodexHome {
		// The CLI reports its canonical home. A symlink spelling is the same
		// state only when both paths still resolve to the same existing directory.
		actual, actualErr := filepath.EvalSymlinks(init.CodexHome)
		requested, requestedErr := filepath.EvalSymlinks(c.CodexHome)
		if actualErr == nil {
			actual, actualErr = filepath.Abs(actual)
		}
		if requestedErr == nil {
			requested, requestedErr = filepath.Abs(requested)
		}
		if actualErr != nil || requestedErr != nil || actual != requested {
			p.Close()
			return nil, fmt.Errorf("后台配置目录与 codex_home 不一致")
		}
	}
	if err = p.notify(ctx, map[string]any{"method": "initialized"}); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}
func newProxy(ws *websocket.Conn) *Proxy {
	p := &Proxy{ws: ws, pending: map[string]chan Message{}, requests: map[string]Message{}, connectionID: uuid.NewString(), done: make(chan struct{})}
	ws.SetReadLimit(16 << 20)
	go p.read()
	return p
}
func (p *Proxy) read() {
	for {
		var m Message
		if err := p.ws.ReadJSON(&m); err != nil {
			p.fail(err)
			return
		}
		p.mu.Lock()
		if len(m.ID) > 0 && m.Method == "" {
			ch := p.pending[string(m.ID)]
			if ch != nil {
				ch <- m
				delete(p.pending, string(m.ID))
			}
		} else {
			if len(m.ID) > 0 && m.Method != "" {
				p.requests[string(m.ID)] = m
			}
			if m.Method == "serverRequest/resolved" {
				var resolved struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(m.Params, &resolved) == nil {
					delete(p.requests, string(resolved.RequestID))
				}
			}
			if len(p.events) >= 4096 {
				p.mu.Unlock()
				p.fail(errors.New("事件队列溢出，必须重新核对"))
				return
			}
			p.events = append(p.events, m)
		}
		p.mu.Unlock()
	}
}
func (p *Proxy) fail(err error) {
	p.once.Do(func() { p.mu.Lock(); p.readErr = err; p.mu.Unlock(); close(p.done); _ = p.ws.Close() })
}
func (p *Proxy) Close() error { p.fail(io.EOF); return nil }
func (p *Proxy) Drain() []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.events
	p.events = nil
	return v
}
func (p *Proxy) notify(ctx context.Context, v any) error {
	p.write.Lock()
	defer p.write.Unlock()
	return p.writeMessage(ctx, v)
}

func (p *Proxy) writeMessage(ctx context.Context, v any) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	if err := p.ws.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return p.ws.WriteJSON(v)
}

var ErrRequestExpired = errors.New("原生请求已处理、过期或连接已失效")

func (p *Proxy) ConnectionID() string { return p.connectionID }

// PendingRequest is scoped to this live transport. Persisting an ID does not
// make it valid on a replacement connection.
func (p *Proxy) PendingRequest(id json.RawMessage) (Message, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	request, ok := p.requests[string(id)]
	return request, ok && p.readErr == nil
}

// Respond writes a JSON-RPC response with the ORIGINAL server request ID.
// Claim before writing: partial writes can be unknown but are never replayed.
func (p *Proxy) Respond(ctx context.Context, id json.RawMessage, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.write.Lock()
	defer p.write.Unlock()
	p.mu.Lock()
	_, exists := p.requests[string(id)]
	if !exists || p.readErr != nil {
		p.mu.Unlock()
		return ErrRequestExpired
	}
	delete(p.requests, string(id))
	p.mu.Unlock()
	if err := p.writeMessage(ctx, map[string]any{"id": id, "result": result}); err != nil {
		p.fail(err)
		return &Unknown{err}
	}
	return nil
}
func (p *Proxy) Call(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return &Unknown{io.EOF}
	default:
	}
	p.mu.Lock()
	p.next++
	id := p.next
	key := strconv.FormatUint(id, 10)
	ch := make(chan Message, 1)
	p.pending[key] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, key); p.mu.Unlock() }()
	if err := p.notify(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		p.fail(err)
		return &Unknown{err}
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			m.Error.Method = method
			return m.Error
		}
		if out != nil {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return &Unknown{fmt.Errorf("invalid %s result: %w", method, err)}
			}
		}
		return nil
	case <-ctx.Done():
		p.fail(ctx.Err())
		return &Unknown{ctx.Err()}
	case <-p.done:
		p.mu.Lock()
		err := p.readErr
		p.mu.Unlock()
		return &Unknown{err}
	}
}

type Version struct {
	Status string `json:"status"`
	CLI    string `json:"cliVersion"`
	Server string `json:"appServerVersion"`
	Socket string `json:"socketPath"`
}

func Versions(ctx context.Context, c config.Config) (Version, error) {
	var v Version
	cmd := exec.CommandContext(ctx, c.CodexBin, "app-server", "daemon", "version")
	cmd.Env = c.Env()
	b, err := cmd.Output()
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(b, &v)
	return v, err
}
