package guard

import (
	"context"
	"fmt"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/state"
)

func (g *Guard) findTag(tag string) (string, error) {
	id := ""
	for _, t := range g.Threads {
		if tag != "" && t.Source == tag {
			if id != "" {
				return "", fmt.Errorf("创建标记匹配多个线程")
			}
			id = t.ID
		}
	}
	return id, nil
}
func (g *Guard) threadParams(cwd, tag string) map[string]any {
	p := map[string]any{"cwd": cwd, "approvalPolicy": "never", "sandbox": "danger-full-access", "threadSource": tag, "ephemeral": false, "config": map[string]any{"features.goals": true}}
	if g.Config.Model != "" {
		p["model"] = g.Config.Model
	}
	if g.Config.Provider != "" {
		p["modelProvider"] = g.Config.Provider
	}
	return p
}
func (g *Guard) startGoal(ctx context.Context, job *state.Goal) error {
	if job.ThreadID == "" {
		if job.CreateTag != "" {
			id, err := g.findTag(job.CreateTag)
			if err != nil {
				return err
			}
			if id == "" {
				job.Status = "uncertain"
				job.Reason = "线程创建结果未知，等待 goal-retry 核对"
				g.dirty = true
				return g.flush()
			}
			job.ThreadID = id
			job.Status = "ready"
			return g.Commit()
		}
		job.CreateTag = state.ID()
		job.Status = "creating"
		if err := g.Commit(); err != nil {
			return err
		}
		var out codex.Started
		if err := g.Client.Call(ctx, "thread/start", g.threadParams(job.Cwd, job.CreateTag), &out); err != nil {
			return err
		}
		if out.Thread.ID == "" {
			return fmt.Errorf("Goal 线程未返回 ID")
		}
		job.ThreadID = out.Thread.ID
		job.Status = "ready"
		g.Threads[job.ThreadID] = out.Thread
		g.attached[job.ThreadID] = true
		g.log("goal_created", job.ID+" thread="+job.ThreadID+" cwd="+job.Cwd)
		return g.Commit()
	}
	_, err := g.monitorGoal(ctx, job)
	return err
}
func (g *Guard) setGoal(ctx context.Context, job *state.Goal, status string, create bool) error {
	p := map[string]any{"threadId": job.ThreadID, "status": status}
	if create {
		p["objective"] = job.Objective
		if job.TokenBudget != nil {
			p["tokenBudget"] = *job.TokenBudget
		}
		job.GoalIntent = true
	}
	if err := g.Commit(); err != nil {
		return err
	}
	var out struct {
		Goal *codex.NativeGoal `json:"goal"`
	}
	if err := g.Client.Call(ctx, "thread/goal/set", p, &out); err != nil {
		return err
	}
	if out.Goal == nil {
		return fmt.Errorf("Goal 设置结果缺失，等待回读核对")
	}
	job.NativeStatus = out.Goal.Status
	job.TokensUsed = out.Goal.TokensUsed
	g.log("goal_"+status, job.ID)
	return g.Commit()
}
func (g *Guard) pauseGoal(ctx context.Context, job *state.Goal, reason string) error {
	job.PauseReason = reason
	return g.setGoal(ctx, job, "paused", false)
}
func (g *Guard) monitorGoal(ctx context.Context, job *state.Goal) (bool, error) {
	goal, err := codex.ReadGoal(ctx, g.Client, job.ThreadID)
	if err != nil {
		return true, err
	}
	t, err := codex.Read(ctx, g.Client, job.ThreadID)
	if err != nil {
		return true, err
	}
	if goal == nil {
		if job.GoalIntent {
			job.Status = "uncertain"
			job.Reason = "原生 Goal 写入结果未知或已被清除"
			g.dirty = true
			return true, nil
		}
		if !g.State.Running || g.State.NeedProbe {
			return false, nil
		}
		job.PauseReason = "start"
		return true, g.setGoal(ctx, job, "paused", true)
	}
	if goal.Objective != job.Objective {
		job.Status = "blocked"
		job.Reason = "原生 Goal 已被外部修改，不自动覆盖"
		g.dirty = true
		return true, nil
	}
	job.NativeStatus = goal.Status
	job.TokensUsed = goal.TokensUsed
	g.dirty = true
	turns, err := codex.Turns(ctx, g.Client, job.ThreadID, 0, false)
	if err != nil {
		return true, err
	}
	obs := state.Observation{LastUser: latestUser(turns), UpdatedAt: t.UpdatedAt}
	if len(turns) > 0 {
		obs.LastTurn = turns[0].ID
	}
	g.State.Observed[job.ThreadID] = obs
	for _, turn := range turns {
		if turn.Status == "inProgress" {
			continue
		}
		if turn.ID == job.LastTurn {
			break
		}
		job.LastTurn = turn.ID
		if turn.Status == "failed" {
			detail := "Goal 轮次失败"
			if turn.Error != nil {
				detail = turn.Error.Message + " " + string(turn.Error.Info)
			}
			g.failure(detail)
			job.PauseReason = "provider"
			job.Status = "recovering"
			job.Reason = state.Redact(detail)
		} else if turn.Status == "completed" && turn.Answer() != "" {
			g.success()
		}
		break
	}
	if goal.Status == "complete" {
		q, err := codex.Queue(ctx, g.Client, job.ThreadID)
		if err != nil {
			return true, err
		}
		if (t.Status.Type == "idle" || t.Status.Type == "notLoaded") && len(q) == 0 {
			job.Status = "complete"
			job.Reason = ""
			job.PauseReason = ""
			g.State.CurrentGoal = ""
			g.log("goal_complete", job.ID+" thread="+job.ThreadID)
			return false, g.Commit()
		}
		g.Reason = "Goal 已标记完成，等待当前轮次和队列结束"
		return true, nil
	}
	if goal.Status == "budgetLimited" || goal.Status == "usageLimited" {
		job.Status = "blocked"
		job.Reason = "原生 Goal 达到限制: " + goal.Status
		return true, nil
	}
	if !g.State.Running || g.State.NeedProbe {
		if goal.Status == "active" {
			reason := g.State.PauseReason
			if g.State.Running {
				reason = "provider"
			}
			if reason == "" {
				reason = "probe"
			}
			if err := g.pauseGoal(ctx, job, reason); err != nil {
				return true, err
			}
		}
		if t.Status.Type == "active" {
			g.Reason = "等待暂停中的 Goal 当前轮次结束"
			return true, nil
		}
		return false, nil
	}
	if goal.Status == "blocked" && job.PauseReason != "provider" && job.PauseReason != "retry" {
		job.Status = "blocked"
		job.Reason = "原生 Goal 被阻塞，等待 goal-retry"
		return true, nil
	}
	if goal.Status == "paused" || goal.Status == "blocked" {
		if job.PauseReason == "" {
			job.Status = "blocked"
			job.Reason = "Goal 被外部暂停，等待显式 goal-retry"
			return true, nil
		}
		if t.Status.Type == "active" {
			return true, nil
		}
		if t.Status.Type == "notLoaded" {
			var out codex.Started
			if err := g.Client.Call(ctx, "thread/resume", map[string]any{"threadId": job.ThreadID, "excludeTurns": true}, &out); err != nil {
				return true, err
			}
			return true, nil
		}
		if err := g.setGoal(ctx, job, "active", false); err != nil {
			return true, err
		}
		job.PauseReason = ""
		job.Status = "running"
		job.Reason = ""
		return true, g.Commit()
	}
	if goal.Status == "active" {
		job.Status = "running"
		g.Reason = "原生 Goal 执行中: " + job.ID
		return true, nil
	}
	job.Status = "blocked"
	job.Reason = "无法处理原生 Goal 状态: " + goal.Status
	return true, nil
}
