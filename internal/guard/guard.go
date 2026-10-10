package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/config"
	"codex-netcap/internal/state"
)

type Saver func(*state.State) error
type PersistenceError struct{ Err error }

func (e *PersistenceError) Error() string { return "状态持久化失败: " + e.Err.Error() }
func (e *PersistenceError) Unwrap() error { return e.Err }

type Guard struct {
	Config     config.Config
	State      *state.State
	Client     codex.Client
	Runner     codex.ProbeRunner
	Now        func() time.Time
	Threads    map[string]codex.Thread
	Connection string
	Reason     string
	save       Saver
	dirty      bool
	lastPoll   time.Time
	scanned    bool
	attached   map[string]bool
}

func New(c config.Config, s *state.State, save Saver) *Guard {
	s.NeedProbe = true
	s.Probe.Status = "idle"
	s.Health.Status = "unknown"
	if s.Operation != nil {
		s.Operation.Status = "uncertain"
	}
	return &Guard{Config: c, State: s, Runner: codex.NewExecProbe(c), Now: time.Now, Threads: map[string]codex.Thread{}, Connection: "disconnected", save: save, attached: map[string]bool{}}
}
func (g *Guard) Commit() error {
	if err := g.save(g.State); err != nil {
		return &PersistenceError{err}
	}
	g.dirty = false
	return nil
}
func (g *Guard) flush() error {
	if g.dirty {
		return g.Commit()
	}
	return nil
}
func (g *Guard) log(kind, msg string) { g.State.Log(g.Now(), kind, msg); g.dirty = true }
func (g *Guard) Initialize() error {
	if len(g.Config.Cities) == 0 {
		return fmt.Errorf("天气城市表未加载")
	}
	dir := filepath.Join(g.Config.StateDir, "goals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	ordered := []*state.Goal{}
	listed := map[string]bool{}
	for _, cfg := range g.Config.Goals {
		listed[cfg.ID] = true
		if old := g.State.Goal(cfg.ID); old != nil {
			if old.File != cfg.File || old.Cwd != cfg.Cwd {
				return fmt.Errorf("Goal %s 已登记，请使用新 ID 更换文件或目录", cfg.ID)
			}
			ordered = append(ordered, old)
			continue
		}
		b, err := os.ReadFile(cfg.File)
		if err != nil {
			return err
		}
		if len(b) > 1<<20 || len(strings.TrimSpace(string(b))) == 0 {
			return fmt.Errorf("Goal %s 文件为空或超过 1 MiB", cfg.ID)
		}
		sum := sha256.Sum256(b)
		hash := hex.EncodeToString(sum[:])
		snap := filepath.Join(dir, hash+".md")
		if err = os.WriteFile(snap, b, 0600); err != nil {
			return err
		}
		objective := fmt.Sprintf("请在 %s 中执行 Goal 文档 %s。该文件是任务的固定快照。读取全文，完成其中已授权的任务和验收条件，实际完成后将原生 Goal 标记为 complete；需要用户决定时明确报告阻塞。", cfg.Cwd, snap)
		if len([]rune(objective)) > 4000 {
			return fmt.Errorf("Goal %s 的路径导致 objective 超过 4000 字符", cfg.ID)
		}
		ordered = append(ordered, &state.Goal{ID: cfg.ID, File: cfg.File, Cwd: cfg.Cwd, Snapshot: snap, Hash: hash, Objective: objective, TokenBudget: cfg.TokenBudget, Status: "pending"})
	}
	for _, old := range g.State.Goals {
		if !listed[old.ID] {
			if old.ID == g.State.CurrentGoal && old.Status != "complete" {
				return fmt.Errorf("不能从清单移除当前未完成 Goal %s", old.ID)
			}
			if old.Status != "complete" {
				old.Status = "removed"
			}
			ordered = append(ordered, old)
		}
	}
	g.State.Goals = ordered
	return g.Commit()
}
func (g *Guard) SetClient(c codex.Client) {
	if g.Client != nil {
		g.Client.Close()
	}
	g.Client = c
	g.Connection = "reconciling"
	g.lastPoll = time.Time{}
	g.attached = map[string]bool{}
}
func (g *Guard) Disconnect(err error) {
	if g.Client != nil {
		g.Client.Close()
	}
	g.Client = nil
	g.Connection = "disconnected"
	g.Runner.Cancel()
	g.State.NeedProbe = true
	if g.State.Operation != nil {
		g.State.Operation.Status = "uncertain"
	}
	g.Reason = state.Redact(err.Error())
	g.log("disconnected", g.Reason)
}
func (g *Guard) own(t codex.Thread) bool {
	seen := map[string]bool{}
	for t.ID != "" && !seen[t.ID] {
		seen[t.ID] = true
		if t.Source != "" && t.Source == g.State.Probe.ID {
			return true
		}
		if t.ID == g.State.Weather.ThreadID && !g.State.Weather.HumanOwned {
			return true
		}
		for _, job := range g.State.Goals {
			if t.ID == job.ThreadID && job.Status != "complete" && job.Status != "removed" {
				return true
			}
		}
		t = g.Threads[t.Parent]
	}
	return false
}
func (g *Guard) human(id string) {
	s := g.State
	if id == s.Weather.ThreadID {
		s.Weather.HumanOwned = true
		s.Weather.Status = "human_owned"
		if o := s.Operation; o != nil {
			o.Cancel = true
		}
	}
	if s.Running || s.ManualProbe {
		s.Running = false
		s.ManualProbe = false
		s.PauseReason = "human"
		s.NeedProbe = true
		g.Runner.Cancel()
		if o := s.Operation; o != nil {
			o.Cancel = true
		}
		g.log("human_pause", "人工介入: "+id+"；等待显式 resume")
	}
}
func (g *Guard) events() {
	for _, m := range g.Client.Drain() {
		var p struct {
			ThreadID string       `json:"threadId"`
			TurnID   string       `json:"turnId"`
			Item     codex.Item   `json:"item"`
			Status   codex.Status `json:"status"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			continue
		}
		if m.Method == "thread/started" || m.Method == "thread/status/changed" || strings.HasPrefix(m.Method, "thread/queue/") {
			g.lastPoll = time.Time{}
		}
		if (m.Method == "item/started" || m.Method == "item/completed") && p.Item.Type == "userMessage" && !g.State.OwnsInput(p.Item.ClientID) {
			t := g.Threads[p.ThreadID]
			if p.ThreadID == g.State.Weather.ThreadID || !g.own(t) {
				if p.ThreadID != "" {
					g.human(p.ThreadID)
				}
			}
			if o := g.State.Operation; o != nil && o.ThreadID == p.ThreadID && o.TurnID == p.TurnID {
				o.Human = true
			}
		}
		if o := g.State.Operation; o != nil && p.ThreadID == o.ThreadID && p.Item.ClientID == o.ID && p.TurnID != "" {
			o.TurnID = p.TurnID
			o.Status = "running"
			g.dirty = true
		}
	}
}
func latestUser(turns []codex.Turn) string {
	for _, t := range turns {
		for i := len(t.Items) - 1; i >= 0; i-- {
			if t.Items[i].Type == "userMessage" {
				return t.Items[i].ID
			}
		}
	}
	return ""
}
func (g *Guard) observe(ctx context.Context) error {
	ids, err := codex.Loaded(ctx, g.Client)
	if err != nil {
		return err
	}
	threads := map[string]codex.Thread{}
	for _, id := range ids {
		t, e := codex.Read(ctx, g.Client, id)
		if e != nil {
			return e
		}
		threads[id] = t
	}
	g.Threads = threads
	for _, id := range ids {
		t := threads[id]
		owned := g.own(t)
		weather := id == g.State.Weather.ThreadID
		if !owned && t.Status.Type == "active" {
			g.human(id)
		}
		if owned && !weather {
			continue
		}
		if t.Ephemeral {
			continue
		}
		q, e := codex.Queue(ctx, g.Client, id)
		if e != nil {
			return e
		}
		for _, item := range q {
			if !g.State.OwnsInput(item.ClientID) {
				g.human(id)
			}
		}
		turns, e := codex.Turns(ctx, g.Client, id, 0, false)
		if e != nil {
			return e
		}
		user := latestUser(turns)
		previous, known := g.State.Observed[id]
		if weather && !g.State.Weather.HumanOwned && len(turns) > 0 && turns[0].HumanInput(g.State.OwnsInput) {
			g.human(id)
			if o := g.State.Operation; o != nil && turns[0].ID == o.TurnID {
				o.Human = true
			}
		}
		if !owned && user != "" && ((known && previous.LastUser != user) || (!known && g.scanned)) {
			g.human(id)
		}
		obs := state.Observation{LastUser: user, UpdatedAt: t.UpdatedAt}
		if len(turns) > 0 {
			obs.LastTurn = turns[0].ID
		}
		if obs != previous {
			g.State.Observed[id] = obs
			g.dirty = true
		}
	}
	g.scanned = true
	g.Connection = "ready"
	return nil
}
func (g *Guard) Step(ctx context.Context) error {
	if g.Client == nil {
		return nil
	}
	g.events()
	if g.lastPoll.IsZero() || g.Now().Sub(g.lastPoll) >= time.Duration(g.Config.PollSeconds)*time.Second {
		if err := g.observe(ctx); err != nil {
			return err
		}
		g.lastPoll = g.Now()
	}
	g.events()
	if result := g.Runner.Poll(); result != nil {
		g.State.Probe.Output = state.Redact(result.Output)
		g.State.ManualProbe = false
		if result.Cancelled {
			g.State.Probe.Status = "cancelled"
			g.State.NeedProbe = true
			g.log("probe_cancelled", "临时日期探测已停止")
		} else if result.Success {
			g.State.Probe.Status = "succeeded"
			g.State.NeedProbe = false
			g.success()
			g.log("probe_success", result.Output)
		} else {
			g.State.Probe.Status = "failed"
			g.failure(result.Error)
			g.log("probe_failure", result.Error)
		}
	}
	if !g.State.Running && !g.State.ManualProbe {
		g.Runner.Cancel()
		if o := g.State.Operation; o != nil {
			o.Cancel = true
		}
	}
	if g.State.Operation != nil {
		if err := g.reconcileWeather(ctx); err != nil {
			return err
		}
		return g.flush()
	}
	job := g.currentGoal()
	if job != nil && job.ThreadID != "" {
		busy, err := g.monitorGoal(ctx, job)
		if err != nil {
			return err
		}
		if busy {
			return g.flush()
		}
	}
	if g.Runner.Active() {
		g.Reason = "临时会话正在执行 UTC 日期探测"
		return g.flush()
	}
	if !g.State.Running && !g.State.ManualProbe {
		g.Reason = "已暂停: " + g.State.PauseReason
		return g.flush()
	}
	if g.State.NeedProbe || g.State.ManualProbe {
		if g.Now().Before(g.State.Health.RetryAt) {
			g.Reason = "等待故障退避至 " + g.State.Health.RetryAt.Format(time.RFC3339)
			return g.flush()
		}
		g.State.Probe.ID = state.ID()
		g.State.Probe.Status = "running"
		g.State.Probe.StartedAt = g.Now()
		g.State.Probe.Attempts++
		if err := g.Commit(); err != nil {
			return err
		}
		if err := g.Runner.Start(g.State.Probe.ID); err != nil {
			g.State.Probe.Status = "failed"
			g.State.ManualProbe = false
			g.failure(err.Error())
			return g.flush()
		}
		g.log("probe_started", "临时会话执行 date -u +%FT%TZ")
		return g.flush()
	}
	if !g.State.Running {
		return g.flush()
	}
	job = g.currentGoal()
	if job != nil {
		if job.Status == "blocked" || job.Status == "uncertain" {
			g.Reason = "Goal 等待处理: " + job.Reason
			return g.flush()
		}
		return g.startGoal(ctx, job)
	}
	return g.weather(ctx)
}
func (g *Guard) currentGoal() *state.Goal {
	if g.State.CurrentGoal != "" {
		return g.State.Goal(g.State.CurrentGoal)
	}
	for _, job := range g.State.Goals {
		if job.Status != "complete" && job.Status != "removed" {
			g.State.CurrentGoal = job.ID
			g.dirty = true
			return job
		}
	}
	return nil
}
func (g *Guard) success() {
	g.State.Health.Status = "healthy"
	g.State.Health.LastSuccess = g.Now()
	g.State.Health.Failures = 0
	g.State.Health.RetryAt = time.Time{}
	g.State.Health.Reason = ""
	g.dirty = true
}

var status429 = regexp.MustCompile(`(?i)\b(?:http(?:\s+status)?|status(?:\s+code)?|code)[\s:="']+429\b`)

func RetryDelay(message string, n int) time.Duration {
	m := strings.ToLower(message)
	normalized := strings.NewReplacer("_", " ", "-", " ").Replace(m)
	if strings.Contains(normalized, "high demand") || strings.Contains(normalized, "rate limit") || strings.Contains(m, "ratelimit") || strings.Contains(m, "too many requests") || strings.TrimSpace(m) == "429" || status429.MatchString(m) {
		return 0
	}
	delay := min(60, 3*(1<<min(max(n-1, 0), 5)))
	return time.Duration(float64(delay) * (.8 + rand.Float64()*.4) * float64(time.Second))
}
func (g *Guard) failure(message string) {
	h := &g.State.Health
	h.Failures++
	h.Reason = state.Redact(message)
	h.RetryAt = g.Now().Add(RetryDelay(message, h.Failures))
	h.Status = "retrying"
	g.State.NeedProbe = true
	g.dirty = true
}
func (g *Guard) Shutdown(ctx context.Context) error {
	g.Runner.Cancel()
	for g.Runner.Active() {
		if g.Runner.Poll() != nil {
			g.State.Probe.Status = "cancelled"
			break
		}
		select {
		case <-ctx.Done():
			return g.Commit()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if job := g.State.Goal(g.State.CurrentGoal); job != nil && job.ThreadID != "" && g.Client != nil {
		goal, err := codex.ReadGoal(ctx, g.Client, job.ThreadID)
		if err == nil && goal != nil && goal.Objective == job.Objective && goal.Status == "active" {
			if err = g.pauseGoal(ctx, job, "shutdown"); err != nil {
				g.log("shutdown_pending", err.Error())
			}
		}
	}
	if g.State.Operation != nil {
		g.State.Operation.Cancel = true
		if g.Client != nil {
			if err := g.reconcileWeather(ctx); err != nil {
				g.State.Operation.Status = "uncertain"
			}
		}
	}
	return g.Commit()
}
