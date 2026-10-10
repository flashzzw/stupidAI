package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/config"
	"codex-netcap/internal/guard"
	"codex-netcap/internal/state"
)

type request struct {
	ctx     context.Context
	command *guard.Command
	path    string
	reply   chan response
}
type response struct {
	body []byte
	err  error
}

// Run owns the only Guard goroutine. HTTP handlers exchange serialized values
// with it, never pointers into mutable scheduling state.
func Run(ctx context.Context, c config.Config, out io.Writer) error {
	metaCtx, metaCancel := context.WithTimeout(ctx, 10*time.Second)
	err := c.LoadCities(metaCtx)
	metaCancel()
	if err != nil {
		return err
	}
	store, err := state.Open(c.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	s, err := store.Load()
	if err != nil {
		return err
	}
	g := guard.New(c, s, store.Save)
	if err = g.Initialize(); err != nil {
		return err
	}
	path := c.ControlSocket()
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("控制 socket 路径已被普通文件占用")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	in := make(chan request, 32)
	server := &http.Server{Handler: handler(in), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	defer server.Close()
	fmt.Fprintf(out, "Go 托管器已启动：%s（自动运行=%t）\n", path, s.Running)
	defer func() {
		if g.Client != nil {
			g.Client.Close()
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var reconnect time.Time
	var lastSeq int64
	for {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return g.Shutdown(stopCtx)
		case err := <-serveErr:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		case req := <-in:
			if req.ctx.Err() != nil {
				req.reply <- response{err: req.ctx.Err()}
				continue
			}
			var err error
			if req.command != nil {
				err = g.Apply(req.ctx, *req.command)
			}
			var body []byte
			if err == nil {
				switch req.path {
				case "/v1/sessions":
					body, err = json.Marshal(g.Snapshot().Sessions)
				case "/v1/goals", "/v1/tasks":
					body, err = json.Marshal(s.Goals)
				case "/v1/events":
					body, err = json.Marshal(s.Events)
				default:
					body, err = json.Marshal(g.Snapshot())
				}
			}
			req.reply <- response{body, err}
			var persist *guard.PersistenceError
			if errors.As(err, &persist) {
				return err
			}
		case <-ticker.C:
			if g.Client == nil && time.Now().After(reconnect) && g.Connection != "incompatible" {
				connectCtx, cancel := context.WithTimeout(ctx, c.RPCTimeout())
				p, err := codex.Connect(connectCtx, c)
				cancel()
				if err != nil {
					defaultSocket := filepath.Join(c.CodexHome, "app-server-control", "app-server-control.sock")
					if c.AutoStartDaemon && c.Socket == defaultSocket {
						startCtx, stop := context.WithTimeout(ctx, c.RPCTimeout())
						cmd := exec.CommandContext(startCtx, c.CodexBin, "app-server", "daemon", "start")
						cmd.Env = c.Env()
						_ = cmd.Run()
						stop()
					}
					g.Disconnect(err)
					reconnect = time.Now().Add(3 * time.Second)
				} else {
					g.SetClient(p)
				}
			}
			if g.Client != nil {
				stepCtx, cancel := context.WithTimeout(ctx, c.RPCTimeout())
				err := g.Step(stepCtx)
				cancel()
				if err != nil {
					var persist *guard.PersistenceError
					if errors.As(err, &persist) {
						return err
					}
					g.Disconnect(err)
					reconnect = time.Now().Add(3 * time.Second)
					var rpc *codex.RPCError
					if errors.As(err, &rpc) && (rpc.Code == -32601 || rpc.Code == -32602) {
						g.Connection = "incompatible"
						g.Reason = "后台协议不兼容: " + state.Redact(rpc.Message)
					}
				}
			}
			if s.Seq != lastSeq {
				if err := g.Commit(); err != nil {
					return err
				}
				for _, e := range s.Events {
					if e.Seq > lastSeq {
						fmt.Fprintf(out, "%s %s %s\n", e.At.Format(time.RFC3339), e.Kind, e.Message)
					}
				}
				lastSeq = s.Seq
			}
		}
	}
}

func handler(in chan<- request) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Header.Get("Origin") != "" {
			http.Error(w, "不接受浏览器跨来源控制", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		var cmd *guard.Command
		switch r.URL.Path {
		case "/v1/command":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
			defer r.Body.Close()
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			cmd = new(guard.Command)
			if err := d.Decode(cmd); err != nil {
				http.Error(w, "无效控制命令", http.StatusBadRequest)
				return
			}
			if err := d.Decode(new(any)); err != io.EOF {
				http.Error(w, "只接受一个 JSON 对象", http.StatusBadRequest)
				return
			}
		case "/v1/status", "/v1/sessions", "/v1/tasks", "/v1/goals", "/v1/events":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		req := request{ctx: r.Context(), command: cmd, path: r.URL.Path, reply: make(chan response, 1)}
		select {
		case in <- req:
		case <-r.Context().Done():
			return
		}
		select {
		case result := <-req.reply:
			if result.err != nil {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": state.Redact(result.err.Error())})
				return
			}
			_, _ = w.Write(result.body)
		case <-r.Context().Done():
		}
	})
}

func Call(ctx context.Context, c config.Config, path string, command *guard.Command) (json.RawMessage, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.ControlSocket())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	method := http.MethodGet
	var body io.Reader
	if command != nil {
		b, err := json.Marshal(command)
		if err != nil {
			return nil, err
		}
		method = http.MethodPost
		body = strings.NewReader(string(b))
		path = "/v1/command"
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://netcap"+path, body)
	if err != nil {
		return nil, err
	}
	if command != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 Go 托管器，请先运行 netcap run: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("控制失败: %s", strings.TrimSpace(string(b)))
	}
	return b, nil
}
