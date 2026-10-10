package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentRosterStatus is the CLI's session-list declaration, not a verified
// process identity, tool lifecycle, complete execution state, or input grant.
type AgentRosterStatus struct {
	Kind       string `json:"kind"`
	JobID      string `json:"job_id,omitempty"`
	PID        *int   `json:"pid,omitempty"`
	State      string `json:"state,omitempty"`
	Status     string `json:"status,omitempty"`
	WaitingFor string `json:"waiting_for,omitempty"`
	StartedAt  int64  `json:"started_at"`
}

const claudeRosterLimit = 4 << 20

type rosterOutput struct{ data bytes.Buffer }

func (b *rosterOutput) Write(p []byte) (int, error) {
	if len(p) > claudeRosterLimit-b.data.Len() {
		return 0, fmt.Errorf("原生会话清单超过读取上限")
	}
	return b.data.Write(p)
}

func (c *Claude) ObserveNativeExecution(ctx context.Context, scope Scope) (View, error) {
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || scope.StateNamespace == "" || !filepath.IsAbs(scope.Cwd) {
		return View{}, fmt.Errorf("Claude 状态采样需要已保存的状态目录与项目绑定")
	}
	if id, err := uuid.Parse(scope.NativeID); err != nil || id.String() != scope.NativeID {
		return View{}, fmt.Errorf("Claude 状态采样需要完整原生会话 UUID")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return View{}, err
	}
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Query the saved canonical namespace, so a temporary alias retarget cannot
	// redirect a read to another profile between the before/after checks.
	instance := scope.Instance
	instance.Profile = scope.StateNamespace
	d, err := c.Describe(check, instance)
	if err != nil || d.Compatibility != "supported" {
		return View{}, fmt.Errorf("Claude 状态采样仅支持已验证的 CLI 2.1.280")
	}
	// --json is the fixed, noninteractive query path. Never fall back to the
	// interactive `agents`, `attach`, `--resume`, or ordinary SDK Observe path.
	cmd := exec.CommandContext(check, scope.Instance.Executable, "agents", "--json", "--all", "--cwd", scope.Cwd)
	cmd.Env, cmd.Dir = claudeEnv(instance), scope.Cwd
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	var output rosterOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if check.Err() != nil {
			return View{}, check.Err()
		}
		return View{}, fmt.Errorf("Claude 会话清单查询失败或超过读取上限；保留原执行")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return View{}, err
	}
	return projectClaudeRoster(scope, output.data.Bytes())
}

func projectClaudeRoster(scope Scope, data []byte) (View, error) {
	var rows []struct {
		ID, SessionID, Cwd, Kind, State, Status, WaitingFor string
		PID                                                 *int
		StartedAt                                           int64
	}
	if len(data) > claudeRosterLimit || !strings.HasPrefix(strings.TrimSpace(string(data)), "[") || json.Unmarshal(data, &rows) != nil {
		return View{}, fmt.Errorf("Claude 会话清单格式无效；不能核对原执行")
	}
	var match *AgentRosterStatus
	for _, row := range rows {
		if row.SessionID != scope.NativeID {
			continue
		}
		if match != nil {
			return View{}, fmt.Errorf("原生会话 UUID 在清单中不唯一；不能选择替代执行器")
		}
		if !filepath.IsAbs(row.Cwd) || filepath.Clean(row.Cwd) != filepath.Clean(scope.Cwd) {
			return View{}, fmt.Errorf("原生会话清单的项目目录与保存绑定不符")
		}
		if (row.Kind != "interactive" && row.Kind != "background") || row.StartedAt <= 0 || (row.PID != nil && *row.PID <= 0) || len(row.ID) > 128 || (row.Kind == "background" && row.ID == "") {
			return View{}, fmt.Errorf("原生会话清单缺少有效身份字段")
		}
		match = &AgentRosterStatus{Kind: row.Kind, JobID: row.ID, PID: row.PID, StartedAt: row.StartedAt,
			State:      knownRosterValue(row.State, "working", "blocked", "done", "failed", "stopped"),
			Status:     knownRosterValue(row.Status, "busy", "waiting", "idle"),
			WaitingFor: knownRosterValue(row.WaitingFor, "permission prompt", "input needed", "sandbox request", "worker request", "dialog open")}
	}
	if match == nil {
		return View{}, fmt.Errorf("清单未返回原生会话的完整 UUID；不按短作业 ID 匹配，也不启动或恢复会话")
	}
	return View{NativeID: scope.NativeID, NativeStatus: "roster_only", Status: "unknown", AgentRoster: match,
		Evidence: Evidence{Source: "claude.agents_json", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: false,
			Detail: "固定 CLI 会话清单采样；state/status/PID 是原生声明，尚未核对进程身份、在途工具、审批完整性或输入归属。idle/done 均不证明执行已清空。"}}, nil
}

func knownRosterValue(value string, allowed ...string) string {
	if value == "" {
		return ""
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "unknown"
}
