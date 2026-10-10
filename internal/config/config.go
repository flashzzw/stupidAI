package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Goal struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Cwd         string `json:"cwd"`
	TokenBudget *int64 `json:"token_budget,omitempty"`
}
type Config struct {
	CodexHome             string   `json:"codex_home"`
	CodexBin              string   `json:"codex_bin"`
	StateDir              string   `json:"state_dir"`
	Socket                string   `json:"codex_socket"`
	Model                 string   `json:"model"`
	Provider              string   `json:"provider"`
	WeatherScript         string   `json:"weather_script"`
	PythonBin             string   `json:"python_bin"`
	IntervalSeconds       int      `json:"weather_interval_seconds"`
	ProbeTimeoutSeconds   int      `json:"probe_timeout_seconds"`
	WeatherTimeoutSeconds int      `json:"weather_timeout_seconds"`
	PollSeconds           int      `json:"poll_seconds"`
	RPCTimeoutSeconds     int      `json:"rpc_timeout_seconds"`
	AutoStartDaemon       bool     `json:"auto_start_daemon"`
	Goals                 []Goal   `json:"goals"`
	Cities                []string `json:"-"`
}

func Default() Config {
	return Config{CodexHome: "/home/zzw/.codex", CodexBin: "codex", PythonBin: "python3", IntervalSeconds: 10, ProbeTimeoutSeconds: 120, WeatherTimeoutSeconds: 120, PollSeconds: 2, RPCTimeoutSeconds: 10, AutoStartDaemon: true}
}
func Load(path string) (Config, error) {
	c := Default()
	base, err := os.Getwd()
	if err != nil {
		return c, err
	}
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return c, e
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 1<<20))
		d.DisallowUnknownFields()
		if err = d.Decode(&c); err != nil {
			return c, fmt.Errorf("配置: %w", err)
		}
		if err = d.Decode(new(any)); err != io.EOF {
			return c, fmt.Errorf("配置必须只有一个 JSON 对象")
		}
		base, err = filepath.Abs(filepath.Dir(path))
		if err != nil {
			return c, err
		}
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(base, p)
	}
	if c.CodexHome == "" || c.CodexBin == "" || c.PythonBin == "" {
		return c, fmt.Errorf("codex_home、codex_bin、python_bin 不能为空")
	}
	c.CodexHome = resolve(c.CodexHome)
	if c.StateDir == "" {
		c.StateDir = filepath.Join(c.CodexHome, "netcap-go")
	} else {
		c.StateDir = resolve(c.StateDir)
	}
	if c.Socket == "" {
		c.Socket = filepath.Join(c.CodexHome, "app-server-control", "app-server-control.sock")
	} else {
		c.Socket = resolve(c.Socket)
	}
	if c.WeatherScript == "" {
		c.WeatherScript = filepath.Join(base, "weather.py")
		if _, e := os.Stat(c.WeatherScript); e != nil {
			if exe, e := os.Executable(); e == nil {
				candidate := filepath.Join(filepath.Dir(filepath.Dir(exe)), "weather.py")
				if _, e = os.Stat(candidate); e == nil {
					c.WeatherScript = candidate
				}
			}
		}
	} else {
		c.WeatherScript = resolve(c.WeatherScript)
	}
	for name, n := range map[string]int{"weather_interval_seconds": c.IntervalSeconds, "probe_timeout_seconds": c.ProbeTimeoutSeconds, "weather_timeout_seconds": c.WeatherTimeoutSeconds, "poll_seconds": c.PollSeconds, "rpc_timeout_seconds": c.RPCTimeoutSeconds} {
		if n < 1 || n > 86400 {
			return c, fmt.Errorf("%s 必须在 1..86400 内", name)
		}
	}
	if len(c.ControlSocket()) > 100 {
		return c, fmt.Errorf("state_dir 过长，Unix socket 路径必须不超过 100 字节")
	}
	seen := map[string]bool{}
	for i := range c.Goals {
		g := &c.Goals[i]
		if strings.TrimSpace(g.ID) == "" || strings.ContainsAny(g.ID, "/\\\x00") || seen[g.ID] || g.File == "" || g.Cwd == "" {
			return c, fmt.Errorf("每个 goal 必须有唯一 id、file、cwd")
		}
		seen[g.ID] = true
		g.File = resolve(g.File)
		g.Cwd = resolve(g.Cwd)
		if g.TokenBudget != nil && *g.TokenBudget <= 0 {
			return c, fmt.Errorf("%s 的 token_budget 必须为正数", g.ID)
		}
		info, e := os.Stat(g.Cwd)
		if e != nil || !info.IsDir() {
			return c, fmt.Errorf("Goal %s 的 cwd 不存在", g.ID)
		}
		if info, e = os.Stat(g.File); e != nil || !info.Mode().IsRegular() {
			return c, fmt.Errorf("Goal %s 的文件不存在", g.ID)
		}
	}
	return c, nil
}

// Reading city metadata never sends a weather request.
func (c *Config) LoadCities(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.PythonBin, c.WeatherScript, "--list-cities")
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("读取 weather.py 城市顺序失败: %w", err)
	}
	if err = json.Unmarshal(b, &c.Cities); err != nil {
		return err
	}
	if len(c.Cities) == 0 {
		return fmt.Errorf("城市列表为空")
	}
	for _, city := range c.Cities {
		if strings.TrimSpace(city) == "" {
			return fmt.Errorf("城市名称为空")
		}
	}
	return nil
}
func (c Config) ControlSocket() string     { return filepath.Join(c.StateDir, "control.sock") }
func (c Config) RPCTimeout() time.Duration { return time.Duration(c.RPCTimeoutSeconds) * time.Second }
func (c Config) Env() []string {
	out := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "CODEX_HOME=") {
			out = append(out, v)
		}
	}
	return append(out, "CODEX_HOME="+c.CodexHome)
}
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (c Config) WeatherCommand(city string) string {
	return ShellQuote(c.PythonBin) + " " + ShellQuote(c.WeatherScript) + " " + ShellQuote(city)
}
func (c Config) WeatherPrompt(city string) string {
	return fmt.Sprintf("请直接使用 %s 查询%s天气。用法：%s。请执行一次并原样返回输出，失败则原样返回错误，不要另找方法或展开解释。", c.WeatherScript, city, c.WeatherCommand(city))
}
