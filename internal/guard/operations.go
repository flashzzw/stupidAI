package guard

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/state"
)

func (g *Guard) weather(ctx context.Context) error {
	w := &g.State.Weather
	if w.HumanOwned {
		index := w.CityIndex
		*w = state.Weather{CityIndex: index, Status: "idle"}
		g.dirty = true
	}
	if w.Status == "uncertain" {
		g.Reason = "天气会话创建结果未知，等待恢复"
		return g.flush()
	}
	if g.Now().Before(w.RetryAt) || (!w.LastSuccess.IsZero() && g.Now().Before(w.LastSuccess.Add(time.Duration(g.Config.IntervalSeconds)*time.Second))) {
		g.Reason = "等待下一城市天气"
		return g.flush()
	}
	if w.ThreadID == "" {
		if w.CreateTag != "" {
			id, err := g.findTag(w.CreateTag)
			if err != nil {
				return err
			}
			if id == "" {
				w.Status = "uncertain"
				w.Reason = "天气线程创建回应丢失，resume 可重新核对"
				g.dirty = true
				return g.flush()
			}
			w.ThreadID = id
			return g.Commit()
		}
		w.CreateTag = state.ID()
		if err := g.Commit(); err != nil {
			return err
		}
		var out codex.Started
		if err := g.Client.Call(ctx, "thread/start", g.threadParams(filepath.Dir(g.Config.WeatherScript), w.CreateTag), &out); err != nil {
			return err
		}
		if out.Thread.ID == "" {
			return fmt.Errorf("天气会话未返回 ID")
		}
		w.ThreadID = out.Thread.ID
		g.Threads[w.ThreadID] = out.Thread
		g.attached[w.ThreadID] = true
		g.log("weather_created", "可 resume 的天气会话: "+w.ThreadID)
		return g.Commit()
	}
	if !g.attached[w.ThreadID] {
		turns, err := codex.Turns(ctx, g.Client, w.ThreadID, 0, false)
		if err != nil {
			return err
		}
		if len(turns) > 0 && turns[0].HumanInput(g.State.OwnsInput) {
			g.human(w.ThreadID)
			return g.flush()
		}
		var out codex.Started
		if err := g.Client.Call(ctx, "thread/resume", map[string]any{"threadId": w.ThreadID, "excludeTurns": true}, &out); err != nil {
			return err
		}
		g.attached[w.ThreadID] = true
	}
	t, err := codex.Read(ctx, g.Client, w.ThreadID)
	if err != nil {
		return err
	}
	q, err := codex.Queue(ctx, g.Client, w.ThreadID)
	if err != nil {
		return err
	}
	if t.Status.Type != "idle" || len(q) > 0 {
		g.human(w.ThreadID)
		return g.flush()
	}
	g.events()
	if !g.State.Running {
		return g.flush()
	}
	city := g.Config.Cities[w.CityIndex%len(g.Config.Cities)]
	o := &state.Operation{ID: state.ID(), Kind: "weather", ThreadID: w.ThreadID, City: city, Status: "prepared", CreatedAt: g.Now()}
	g.State.Operation = o
	w.Status = "running"
	g.log("weather_started", city+" thread="+w.ThreadID)
	if err := g.Commit(); err != nil {
		return err
	}
	var out struct {
		Item codex.QueueItem `json:"queuedSubmission"`
	}
	err = g.Client.Call(ctx, "thread/queue/add", map[string]any{"threadId": w.ThreadID, "clientUserMessageId": o.ID, "input": []any{map[string]any{"type": "text", "text": g.Config.WeatherPrompt(city)}}}, &out)
	if err != nil {
		o.Status = "uncertain"
		g.dirty = true
		return err
	}
	if out.Item.ID == "" {
		o.Status = "uncertain"
	} else {
		o.QueueID = out.Item.ID
		o.Status = "queued"
	}
	return g.Commit()
}
func (g *Guard) reconcileWeather(ctx context.Context) error {
	o := g.State.Operation
	if o == nil {
		return nil
	}
	g.events()
	q, err := codex.Queue(ctx, g.Client, o.ThreadID)
	if err != nil {
		return err
	}
	turns, err := codex.Turns(ctx, g.Client, o.ThreadID, o.CreatedAt.Add(-time.Minute).Unix(), true)
	if err != nil {
		return err
	}
	var matched *codex.Turn
	for i := range turns {
		t := &turns[i]
		if t.HasInput(o.ID) || (o.TurnID != "" && t.ID == o.TurnID) {
			if matched != nil && matched.ID != t.ID {
				o.Status = "uncertain"
				g.dirty = true
				return nil
			}
			matched = t
		}
	}
	if matched != nil {
		o.TurnID = matched.ID
		if matched.HumanInput(g.State.OwnsInput) {
			o.Human = true
			g.human(o.ThreadID)
		}
		if matched.Status != "inProgress" {
			return g.finishWeather(*matched)
		}
		o.Status = "running"
		g.dirty = true
		if !g.State.Running {
			o.Cancel = true
		}
		if !o.Human && g.Now().Sub(o.CreatedAt) > time.Duration(g.Config.WeatherTimeoutSeconds)*time.Second {
			o.Cancel = true
			o.Reason = "天气轮次超时"
		}
		if o.Cancel && !o.Human && !o.InterruptRequested {
			items, err := codex.Items(ctx, g.Client, o.ThreadID, o.TurnID)
			if err != nil {
				return err
			}
			check := codex.Turn{Items: items}
			if !check.HasInput(o.ID) || check.HumanInput(g.State.OwnsInput) {
				o.Human = true
				g.human(o.ThreadID)
				return nil
			}
			if err := g.Commit(); err != nil {
				return err
			}
			var out any
			if err := g.Client.Call(ctx, "turn/interrupt", map[string]any{"threadId": o.ThreadID, "turnId": o.TurnID}, &out); err != nil {
				o.Status = "uncertain"
				return err
			}
			o.InterruptRequested = true
			o.Status = "interrupting"
		}
		return nil
	}
	var own *codex.QueueItem
	for i := range q {
		if q[i].ClientID == o.ID {
			if own != nil {
				o.Status = "uncertain"
				g.dirty = true
				return nil
			}
			own = &q[i]
		}
	}
	if own == nil {
		o.Status = "uncertain"
		g.Reason = "天气请求结果未知，不重复发送"
		g.dirty = true
		return nil
	}
	o.QueueID = own.ID
	o.Status = "queued"
	g.dirty = true
	t, err := codex.Read(ctx, g.Client, o.ThreadID)
	if err != nil {
		return err
	}
	g.events()
	foreign := len(q) != 1 || t.Status.Type != "idle"
	if len(q) != 1 {
		for _, item := range q {
			if !g.State.OwnsInput(item.ClientID) {
				g.human(o.ThreadID)
			}
		}
	}
	if o.Cancel || !g.State.Running || foreign {
		o.Cancel = true
		if err := g.Commit(); err != nil {
			return err
		}
		var out struct {
			Deleted bool `json:"deleted"`
		}
		if err := g.Client.Call(ctx, "thread/queue/delete", map[string]any{"threadId": o.ThreadID, "queuedSubmissionId": o.QueueID}, &out); err != nil {
			o.Status = "uncertain"
			var rpc *codex.RPCError
			if errors.As(err, &rpc) {
				return nil
			}
			return err
		}
		if !out.Deleted {
			o.Status = "uncertain"
			return nil
		}
		latest, err := codex.Turns(ctx, g.Client, o.ThreadID, o.CreatedAt.Add(-time.Minute).Unix(), true)
		if err != nil {
			return err
		}
		for _, turn := range latest {
			if turn.HasInput(o.ID) {
				o.TurnID = turn.ID
				o.Status = "running"
				return nil
			}
		}
		g.State.Finish(g.Now(), "cancelled", "已删除自有天气队列项")
		g.State.Weather.Status = "idle"
		g.dirty = true
		return nil
	}
	if err := g.Commit(); err != nil {
		return err
	}
	var out struct {
		Turn codex.Turn `json:"turn"`
	}
	if err := g.Client.Call(ctx, "thread/queue/start", map[string]any{"threadId": o.ThreadID, "queuedSubmissionId": o.QueueID}, &out); err != nil {
		o.Status = "uncertain"
		o.Cancel = true
		var rpc *codex.RPCError
		if errors.As(err, &rpc) {
			return nil
		}
		return err
	}
	if out.Turn.ID == "" {
		o.Status = "uncertain"
	} else {
		o.TurnID = out.Turn.ID
		o.Status = "running"
	}
	return g.Commit()
}
func (g *Guard) finishWeather(turn codex.Turn) error {
	o := g.State.Operation
	w := &g.State.Weather
	if turn.Status != "completed" && turn.Status != "failed" && turn.Status != "interrupted" {
		o.Status = "uncertain"
		g.dirty = true
		return nil
	}
	now := g.Now()
	if turn.CompletedAt > 0 {
		now = time.Unix(turn.CompletedAt, 0)
	}
	if o.Human {
		g.State.Finish(now, "handed_over", "人工接管，不推进城市索引")
		w.Status = "human_owned"
		g.dirty = true
		return g.Commit()
	}
	if turn.Status == "completed" && strings.TrimSpace(turn.Answer()) != "" {
		g.success()
		executed := false
		for _, item := range turn.Items {
			if item.Type == "commandExecution" && strings.Contains(item.Command, filepath.Base(g.Config.WeatherScript)) && strings.Contains(item.Command, o.City) && item.ExitCode != nil && *item.ExitCode == 0 {
				executed = true
			}
		}
		if executed {
			w.LastCity = o.City
			w.LastOutput = state.Redact(turn.Answer())
			w.LastSuccess = now
			w.CityIndex = (w.CityIndex + 1) % len(g.Config.Cities)
			w.Failures = 0
			w.RetryAt = time.Time{}
			w.Reason = ""
			w.Status = "idle"
			g.State.Finish(now, "succeeded", turn.Answer())
		} else {
			w.Failures++
			w.Reason = "没有确认天气脚本成功执行；保留当前城市"
			w.RetryAt = g.Now().Add(RetryDelay(w.Reason, w.Failures))
			w.Status = "retrying"
			g.State.Finish(now, "weather_failed", w.Reason)
		}
	} else if turn.Status == "interrupted" {
		if o.Reason == "天气轮次超时" {
			g.failure(o.Reason)
		}
		w.Status = "idle"
		g.State.Finish(now, "interrupted", o.Reason)
	} else {
		reason := "天气轮次没有非空最终响应"
		if turn.Error != nil {
			reason = turn.Error.Message + " " + string(turn.Error.Info)
		}
		g.failure(reason)
		w.Status = "retrying"
		w.Reason = state.Redact(reason)
		g.State.Finish(now, "failed", reason)
		lower := strings.ToLower(reason)
		if strings.Contains(lower, "contextwindow") || strings.Contains(lower, "context window") {
			w.ThreadID = ""
			w.CreateTag = ""
			w.Status = "idle"
		}
	}
	g.lastPoll = time.Time{}
	g.dirty = true
	return g.Commit()
}
