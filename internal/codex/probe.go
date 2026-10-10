package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codex-netcap/internal/config"
	"codex-netcap/internal/state"
)

type ProbeResult struct {
	Success      bool
	Cancelled    bool
	Output       string
	Error        string
	DateExecuted bool
}
type ProbeRunner interface {
	Start(string) error
	Poll() *ProbeResult
	Cancel()
	Active() bool
}
type ExecProbe struct {
	config config.Config
	cancel context.CancelFunc
	result chan ProbeResult
	active bool
}

func NewExecProbe(c config.Config) *ExecProbe { return &ExecProbe{config: c} }
func ProbeArgs(c config.Config, source string) []string {
	a := []string{"exec", "--ephemeral", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "--json", "--thread-source", source}
	if c.Model != "" {
		a = append(a, "--model", c.Model)
	}
	if c.Provider != "" {
		a = append(a, "-c", "model_provider="+strconv.Quote(c.Provider))
	}
	return append(a, state.ProbePrompt)
}
func (p *ExecProbe) Start(source string) error {
	if p.active {
		return fmt.Errorf("临时探测仍在运行")
	}
	cwd := filepath.Join(p.config.StateDir, "probe")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		return err
	}
	out, err := os.CreateTemp(cwd, "stdout-")
	if err != nil {
		return err
	}
	stderr, err := os.CreateTemp(cwd, "stderr-")
	if err != nil {
		out.Close()
		os.Remove(out.Name())
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.config.ProbeTimeoutSeconds)*time.Second)
	cmd := exec.CommandContext(ctx, p.config.CodexBin, ProbeArgs(p.config, source)...)
	cmd.Env = p.config.Env()
	cmd.Dir = cwd
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err = cmd.Start(); err != nil {
		cancel()
		out.Close()
		stderr.Close()
		os.Remove(out.Name())
		os.Remove(stderr.Name())
		return err
	}
	p.cancel = cancel
	p.result = make(chan ProbeResult, 1)
	p.active = true
	resultCh := p.result
	go func() {
		err := cmd.Wait()
		out.Seek(0, io.SeekStart)
		stderr.Seek(0, io.SeekStart)
		result := parseProbe(io.LimitReader(out, 32<<20))
		detail, _ := io.ReadAll(io.LimitReader(stderr, 4096))
		if err != nil {
			result.Success = false
			if result.Error == "" {
				result.Error = err.Error() + ": " + string(detail)
			}
		}
		if ctx.Err() != nil {
			result.Success = false
			result.Cancelled = ctx.Err() == context.Canceled
			result.Error = ctx.Err().Error()
		}
		cancel()
		out.Close()
		stderr.Close()
		os.Remove(out.Name())
		os.Remove(stderr.Name())
		resultCh <- result
	}()
	return nil
}
func (p *ExecProbe) Active() bool { return p.active }
func (p *ExecProbe) Cancel() {
	if p.active && p.cancel != nil {
		p.cancel()
	}
}
func (p *ExecProbe) Poll() *ProbeResult {
	if !p.active {
		return nil
	}
	select {
	case r := <-p.result:
		p.active = false
		p.cancel = nil
		return &r
	default:
		return nil
	}
}

func parseProbe(r io.Reader) ProbeResult {
	result := ProbeResult{}
	completed, failed := false, false
	dateOutput := ""
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		var e struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   *struct {
				Message string `json:"message"`
			} `json:"error"`
			Item struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Command  string `json:"command"`
				Output   string `json:"aggregated_output"`
				ExitCode *int   `json:"exit_code"`
			} `json:"item"`
		}
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "turn.completed":
			completed = true
		case "turn.failed":
			failed = true
			if e.Error != nil {
				result.Error = e.Error.Message
			}
		case "error":
			result.Error = e.Message
		case "item.completed":
			if e.Item.Type == "agent_message" {
				result.Output = strings.TrimSpace(e.Item.Text)
			}
			if e.Item.Type == "command_execution" && strings.Contains(e.Item.Command, "date -u +%FT%TZ") && e.Item.ExitCode != nil && *e.Item.ExitCode == 0 {
				dateOutput = strings.TrimSpace(e.Item.Output)
				if _, err := time.Parse("2006-01-02T15:04:05Z", dateOutput); err == nil {
					result.DateExecuted = true
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Success = completed && !failed && result.DateExecuted && result.Output == dateOutput
	if !result.Success && result.Error == "" {
		result.Error = "没有确认 date -u +%FT%TZ 成功执行并原样返回 UTC 时间"
	}
	return result
}
