package guard

import (
	"context"
	"fmt"
	"sort"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/state"
)

type Command struct {
	Action string   `json:"action"`
	ID     string   `json:"id,omitempty"`
	Order  []string `json:"order,omitempty"`
}
type Snapshot struct {
	Connection string         `json:"connection"`
	Reason     string         `json:"reason"`
	At         time.Time      `json:"observed_at"`
	State      *state.State   `json:"state"`
	Sessions   []codex.Thread `json:"sessions"`
}

func (g *Guard) Snapshot() Snapshot {
	sessions := []codex.Thread{}
	for _, t := range g.Threads {
		t.Turns = nil
		sessions = append(sessions, t)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return Snapshot{g.Connection, g.Reason, g.lastPoll, g.State, sessions}
}
func (g *Guard) Apply(ctx context.Context, c Command) error {
	s := g.State
	switch c.Action {
	case "pause":
		s.Running = false
		s.ManualProbe = false
		s.PauseReason = "user"
		g.Runner.Cancel()
		if s.Operation != nil {
			s.Operation.Cancel = true
		}
	case "resume":
		s.Running = true
		s.PauseReason = ""
		s.NeedProbe = true
		if s.Weather.ThreadID == "" && s.Operation == nil {
			s.Weather.CreateTag = ""
			s.Weather.Status = "idle"
		}
	case "probe":
		s.ManualProbe = true
		s.NeedProbe = true
	case "goal-retry":
		job := s.Goal(c.ID)
		if job == nil || c.ID != s.CurrentGoal {
			return fmt.Errorf("只能重试当前 Goal")
		}
		if job.NativeStatus == "budgetLimited" || job.NativeStatus == "usageLimited" {
			return fmt.Errorf("不能用重试绕过原生 Goal 预算或用量限制")
		}
		if job.Status != "blocked" && job.Status != "uncertain" && job.Status != "recovering" {
			return fmt.Errorf("当前 Goal 不需要重试")
		}
		if job.ThreadID == "" && !job.GoalIntent {
			job.CreateTag = ""
		}
		job.PauseReason = "retry"
		job.Status = "ready"
		job.Reason = ""
		s.Running = true
		s.NeedProbe = true
		s.PauseReason = ""
	case "goal-order":
		if len(c.Order) != len(s.Goals) {
			return fmt.Errorf("order 必须包含全部 Goal ID")
		}
		seen := map[string]bool{}
		jobs := []*state.Goal{}
		for _, id := range c.Order {
			job := s.Goal(id)
			if job == nil || seen[id] {
				return fmt.Errorf("order 含重复或未知 ID")
			}
			seen[id] = true
			jobs = append(jobs, job)
		}
		s.Goals = jobs
	default:
		return fmt.Errorf("不支持的操作 %q；新版使用 goals 文件清单，不再使用 watch 或 task-add", c.Action)
	}
	g.log(c.Action, c.ID)
	return g.Commit()
}
