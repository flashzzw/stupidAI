package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type opencodeServerRecord struct {
	SessionID       string `json:"session_id,omitempty"`
	PID             int    `json:"pid"`
	Birth           string `json:"birth"`
	URL             string `json:"url"`
	Epoch           string `json:"epoch"`
	Digest          string `json:"digest"`
	EffectiveDigest string `json:"effective_digest,omitempty"`
}

type opencodeServer struct {
	record     opencodeServerRecord
	root       string
	cwd        string
	password   string
	cmd        *exec.Cmd
	exited     chan struct{}
	http       *http.Client
	cancel     context.CancelFunc
	streamDone chan struct{}
	mu         sync.Mutex
	connected  bool
	seen       map[string]bool
	parts      map[string]map[string]opencodePart
	events     int64
	stopOnDeny bool
}

func (s *opencodeServer) verifyConfig(ctx context.Context, scope Scope) error {
	var cfg map[string]any
	if err := s.call(ctx, "GET", "/config", nil, &cfg); err != nil {
		return err
	}
	delete(cfg, "$schema")
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var effective struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
		Experimental struct {
			ContinueOnDeny bool `json:"continue_loop_on_deny"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(b, &effective); err != nil {
		return err
	}
	key, _ := strings.CutPrefix(scope.Route.CredentialRef, "env:")
	actual, ok := effective.Provider[scope.Route.Provider]
	if !ok || actual.Options.BaseURL != scope.Route.Endpoint || actual.Options.APIKey != os.Getenv(key) {
		return fmt.Errorf("OpenCode 实际配置未采用指定端点/凭据")
	}
	// POST /mcp mutates the runtime registry without updating GET /config.
	// Track its names separately; transient connection statuses are not policy.
	var registry map[string]json.RawMessage
	if err := s.call(ctx, "GET", "/mcp", nil, &registry); err != nil {
		return err
	}
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	b, _ = json.Marshal([]any{cfg, names})
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	if s.record.EffectiveDigest != "" && s.record.EffectiveDigest != digest {
		return fmt.Errorf("OpenCode 有效目录/MCP/权限配置已变化，需人工核对策略 revision")
	}
	if s.record.EffectiveDigest == "" {
		s.record.EffectiveDigest = digest
		b, _ = json.Marshal(s.record)
		if err := writeOpenCodeRecord(filepath.Join(s.root, "server.json"), b); err != nil {
			return err
		}
	}
	s.stopOnDeny = !effective.Experimental.ContinueOnDeny
	return nil
}

func opencodeKey(scope Scope) string {
	parts := []any{scope.Instance, scope.Route, scope.Cwd}
	// Native remembered permissions and MCP state are directory/server scoped.
	// A console session therefore owns its server and native data partition.
	if scope.SessionID != "" {
		parts = append(parts, scope.SessionID)
	}
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func opencodeConfig(scope Scope) ([]byte, string, error) {
	b, err := os.ReadFile(filepath.Join(scope.Instance.Profile, "opencode.json"))
	if err != nil {
		return nil, "", err
	}
	var cfg map[string]any
	if err = json.Unmarshal(b, &cfg); err != nil {
		return nil, "", fmt.Errorf("OpenCode profile 不是有效 JSON")
	}
	delete(cfg, "$schema")
	key, ok := strings.CutPrefix(scope.Route.CredentialRef, "env:")
	if !ok || os.Getenv(key) == "" {
		return nil, "", fmt.Errorf("OpenCode 路由需要已设置的 env: 凭据引用")
	}
	providers, _ := cfg["provider"].(map[string]any)
	provider, _ := providers[scope.Route.Provider].(map[string]any)
	if provider == nil {
		return nil, "", fmt.Errorf("OpenCode profile 未定义指定 provider")
	}
	options, _ := provider["options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
		provider["options"] = options
	}
	options["baseURL"], options["apiKey"] = scope.Route.Endpoint, os.Getenv(key)
	cfg["model"], cfg["small_model"] = scope.Route.Provider+"/"+scope.Route.Model, scope.Route.Provider+"/"+scope.Route.Model
	cfg["autoupdate"], cfg["share"] = false, "disabled"
	b, err = json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), err
}

func processBirth(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	end := strings.LastIndex(string(b), ") ")
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(b)[end+2:])
	if len(fields) < 20 || fields[0] == "Z" {
		return ""
	}
	return fields[19]
}

func (s *opencodeServer) alive() bool {
	return s.record.Birth != "" && processBirth(s.record.PID) == s.record.Birth
}

func startOpenCode(ctx context.Context, scope Scope, root string, cfg []byte, digest string) (*opencodeServer, error) {
	for _, dir := range []string{root, "home", "config", "data", "cache", "state"} {
		path := dir
		if dir != root {
			path = filepath.Join(root, dir)
		}
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
	}
	secretPath := filepath.Join(root, "server-token")
	password, err := os.ReadFile(secretPath)
	if os.IsNotExist(err) {
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return nil, err
		}
		password = []byte(hex.EncodeToString(secret[:]))
		err = os.WriteFile(secretPath, password, 0600)
	}
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(string(password))
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("OpenCode server 认证文件损坏")
	}
	s := &opencodeServer{root: root, cwd: scope.Cwd, password: string(password), http: &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, seen: map[string]bool{}, parts: map[string]map[string]opencodePart{}}
	recordPath := filepath.Join(root, "server.json")
	if b, e := os.ReadFile(recordPath); e == nil {
		if e = json.Unmarshal(b, &s.record); e != nil {
			return nil, fmt.Errorf("OpenCode server 所有权记录损坏")
		}
		u, e := url.Parse(s.record.URL)
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("OpenCode server 地址不是自有本机端点")
		}
		if s.record.Digest != digest {
			return nil, fmt.Errorf("OpenCode 实例配置已变化；需核对并更新路由/策略 revision")
		}
		if s.record.SessionID != scope.SessionID {
			return nil, fmt.Errorf("OpenCode server 所有权与会话不匹配")
		}
		if s.alive() {
			var health struct {
				Healthy bool   `json:"healthy"`
				Version string `json:"version"`
			}
			if e = s.call(ctx, "GET", "/global/health", nil, &health); e != nil || !health.Healthy || health.Version != "1.18.32" {
				return nil, fmt.Errorf("原 OpenCode server 尚不能验证")
			}
			s.subscribe()
			return s, nil
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	var epoch [16]byte
	if _, err = rand.Read(epoch[:]); err != nil {
		return nil, err
	}
	s.record = opencodeServerRecord{SessionID: scope.SessionID, URL: fmt.Sprintf("http://127.0.0.1:%d", port), Epoch: hex.EncodeToString(epoch[:]), Digest: digest, EffectiveDigest: s.record.EffectiveDigest}
	cmd := exec.Command(scope.Instance.Executable, "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = scope.Cwd
	cmd.Env = []string{}
	for _, key := range append([]string{"PATH", "USER", "LANG", "TERM", "SHELL"}, scope.Instance.EnvAllow...) {
		if strings.HasPrefix(key, "OPENCODE_") || strings.HasPrefix(key, "XDG_") || key == "HOME" {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+filepath.Join(root, "home"), "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "XDG_DATA_HOME="+filepath.Join(root, "data"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"), "XDG_STATE_HOME="+filepath.Join(root, "state"),
		"OPENCODE_CONFIG="+filepath.Join(scope.Instance.Profile, "opencode.json"), "OPENCODE_CONFIG_CONTENT="+string(cfg), "OPENCODE_SERVER_USERNAME=opencode", "OPENCODE_SERVER_PASSWORD="+s.password,
		"OPENCODE_DISABLE_MODELS_FETCH=true", "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	s.cmd = cmd
	s.exited = make(chan struct{})
	go func() { _ = cmd.Wait(); close(s.exited) }()
	s.record.PID, s.record.Birth = cmd.Process.Pid, processBirth(cmd.Process.Pid)
	b, _ := json.Marshal(s.record)
	if err = writeOpenCodeRecord(recordPath, b); err != nil {
		s.stop()
		return nil, err
	}
	for {
		var health struct {
			Healthy bool   `json:"healthy"`
			Version string `json:"version"`
		}
		probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err = s.call(probe, "GET", "/global/health", nil, &health)
		cancel()
		if err == nil && health.Healthy && health.Version == "1.18.32" {
			break
		}
		if !s.alive() {
			s.stop()
			return nil, fmt.Errorf("自有 OpenCode server 启动失败")
		}
		select {
		case <-ctx.Done():
			s.stop()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.subscribe()
	return s, nil
}

type opencodeHTTPError struct{ Status int }

func (e *opencodeHTTPError) Error() string {
	return fmt.Sprintf("OpenCode HTTP %d，原操作需按原生记录核对", e.Status)
}

func (s *opencodeServer) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	u, err := url.Parse(s.record.URL + path)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("directory", s.cwd)
	u.RawQuery = query.Encode()
	var payload io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		payload = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, u.String(), payload)
	if err != nil {
		return nil, err
	}
	r.SetBasicAuth("opencode", s.password)
	r.Header.Set("Content-Type", "application/json")
	return s.http.Do(r)
}

func (s *opencodeServer) call(ctx context.Context, method, path string, body, out any) error {
	r, err := s.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return &opencodeHTTPError{Status: r.StatusCode}
	}
	if out == nil || r.StatusCode == http.StatusNoContent {
		_, err = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	if err = decoder.Decode(out); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("OpenCode 响应不完整或包含多余数据")
	}
	return nil
}

func (s *opencodeServer) subscribe() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.streamDone = make(chan struct{})
	go func() {
		defer close(s.streamDone)
		for ctx.Err() == nil {
			r, err := s.request(ctx, "GET", "/event", nil)
			if err == nil && r.StatusCode == 200 {
				s.mu.Lock()
				s.connected = true
				s.parts = map[string]map[string]opencodePart{}
				s.seen = map[string]bool{}
				s.mu.Unlock()
				scan := bufio.NewScanner(r.Body)
				scan.Buffer(make([]byte, 65536), 4<<20)
				for scan.Scan() {
					line := scan.Bytes()
					if bytes.HasPrefix(line, []byte("data: ")) {
						s.event(line[6:])
					}
				}
			}
			if r != nil {
				_ = r.Body.Close()
			}
			s.mu.Lock()
			s.connected = false
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
}

func (s *opencodeServer) event(b []byte) {
	var event struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Properties struct {
			SessionID string       `json:"sessionID"`
			MessageID string       `json:"messageID"`
			PartID    string       `json:"partID"`
			Field     string       `json:"field"`
			Delta     string       `json:"delta"`
			Part      opencodePart `json:"part"`
		} `json:"properties"`
	}
	if json.Unmarshal(b, &event) != nil || event.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[event.ID] {
		return
	}
	s.seen[event.ID] = true
	s.events++
	if len(s.seen) > 10000 {
		s.seen = map[string]bool{event.ID: true}
	}
	data := event.Properties
	if s.parts[data.SessionID] == nil {
		s.parts[data.SessionID] = map[string]opencodePart{}
	}
	if event.Type == "message.part.updated" {
		if old := s.parts[data.SessionID][data.Part.ID]; old.EventID < event.ID {
			data.Part.EventID = event.ID
			s.parts[data.SessionID][data.Part.ID] = data.Part
		}
	}
	if event.Type == "message.part.delta" && data.Field == "text" {
		part, ok := s.parts[data.SessionID][data.PartID]
		if ok && part.EventID < event.ID && len(part.Text) < 200000 {
			part.Text += data.Delta
			part.EventID = event.ID
			s.parts[data.SessionID][data.PartID] = part
		}
	}
}

func (s *opencodeServer) stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.streamDone
	}
	if s.alive() {
		_ = syscall.Kill(-s.record.PID, syscall.SIGTERM)
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.alive() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.alive() {
		_ = syscall.Kill(-s.record.PID, syscall.SIGKILL)
	}
	if s.exited != nil {
		<-s.exited
	}
	s.http.CloseIdleConnections()
}
