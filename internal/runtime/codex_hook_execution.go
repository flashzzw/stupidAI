package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"codex-netcap/internal/codex"
)

var codexHookTimeout = regexp.MustCompile(`^hook timed out after [0-9]+(?:\.[0-9]+)?s$`)

func (c *Codex) collectHook(instance Instance, event codex.Message) {
	var data struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Run      struct {
			ID        string `json:"id"`
			Event     string `json:"eventName"`
			Source    string `json:"source"`
			Path      string `json:"sourcePath"`
			Status    string `json:"status"`
			Started   *int64 `json:"startedAt"`
			Completed *int64 `json:"completedAt"`
			Duration  *int64 `json:"durationMs"`
			Entries   []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"entries"`
		} `json:"run"`
	}
	decodeErr := json.Unmarshal(event.Params, &data)
	valid := func(s string, max int) bool { return s != "" && len(s) <= max && !strings.ContainsRune(s, '\x00') }
	h := data.Run
	if !valid(data.ThreadID, 500) {
		return
	}
	if !valid(h.ID, 16384) {
		h.ID = "unreported"
	}
	if len(data.TurnID) > 500 || strings.ContainsRune(data.TurnID, '\x00') {
		data.TurnID = "unreported"
	}
	key := instance.ID + "/" + data.ThreadID
	identity, _ := json.Marshal([]string{data.ThreadID, data.TurnID, h.ID})
	sum := sha256.Sum256(identity)
	id := "codex-hook-" + hex.EncodeToString(sum[:])
	if c.hooks[key] == nil {
		c.hooks[key] = map[string]HookExecution{}
	}
	item := HookExecution{ID: id, NativeRunID: h.ID, TurnID: data.TurnID, Status: "unknown", Source: "unreported", Detail: "原生 hook 生命周期元数据不完整，未转发命令或诊断正文。"}
	if decodeErr != nil || h.ID == "unreported" || data.TurnID == "unreported" || !valid(h.Event, 300) || !valid(h.Source, 300) || !valid(h.Path, 16384) || !filepath.IsAbs(h.Path) || !valid(h.Status, 300) || h.Started == nil || *h.Started <= 0 || *h.Started > 253402300799 {
		item.Inconsistent = true
		c.hooks[key][id] = mergeCodexHook(c.hooks[key][id], item)
		return
	}
	item.Event, item.Source, item.SourcePath, item.NativeStatus = h.Event, h.Source, h.Path, h.Status
	item.StartedAt = time.Unix(*h.Started, 0).UTC().Format(time.RFC3339)
	item.Detail = "原生报告 hook 来源路径；未报告完整插件 ID。命令与输出正文不在此展示。"
	if event.Method == "hook/started" && h.Status == "running" && h.Completed == nil {
		item.Status = "running"
	}
	if event.Method == "hook/completed" {
		if h.Completed != nil && *h.Completed >= *h.Started && *h.Completed <= 253402300799 && h.Duration != nil && *h.Duration >= 0 && *h.Duration <= 1<<53 {
			item.FinishedAt = time.Unix(*h.Completed, 0).UTC().Format(time.RFC3339)
			item.DurationMS = h.Duration
			switch h.Status {
			case "completed":
				item.Status = "success"
			case "failed", "blocked":
				item.Status = "error"
				if len(h.Entries) <= 128 {
					for _, entry := range h.Entries {
						if entry.Kind == "error" && len(entry.Text) <= 100 && codexHookTimeout.MatchString(entry.Text) {
							item.FailureKind = "timeout"
						}
					}
				}
			case "stopped":
				item.Status = "cancelled"
			}
		}
	}
	c.hooks[key][id] = mergeCodexHook(c.hooks[key][id], item)
}

func mergeCodexHook(old, next HookExecution) HookExecution {
	if old.ID == "" {
		return next
	}
	conflict := old.Inconsistent || next.Inconsistent || old.NativeRunID != next.NativeRunID || old.TurnID != next.TurnID || old.Event != next.Event || old.Source != next.Source || old.SourcePath != next.SourcePath || old.StartedAt != next.StartedAt
	if old.FinishedAt != "" && next.FinishedAt != "" && (old.FinishedAt != next.FinishedAt || old.NativeStatus != next.NativeStatus || (old.DurationMS == nil) != (next.DurationMS == nil) || old.DurationMS != nil && next.DurationMS != nil && *old.DurationMS != *next.DurationMS) {
		conflict = true
	}
	if conflict {
		old.Status = "unknown"
		old.Inconsistent = true
		old.Detail = "同一 hook 调用的原生元数据不一致；保留原归属并等待核对。"
		return old
	}
	if old.FinishedAt != "" && next.Status == "running" {
		return old
	}
	next.Operation = old.Operation
	return next
}

// RetainCodexHookExecutions preserves event history when a new connection does
// not replay old notifications. Missing unfinished calls remain unresolved.
func RetainCodexHookExecutions(previous []HookExecution, view *View) {
	items := map[string]HookExecution{}
	for _, h := range previous {
		if h.NativeRunID != "" {
			if h.Status == "running" {
				h.Status = "unknown"
			}
			items[h.ID] = h
		}
	}
	for _, h := range view.HookExecutions {
		items[h.ID] = mergeCodexHook(items[h.ID], h)
	}
	view.HookExecutions = nil
	for _, h := range items {
		view.HookExecutions = append(view.HookExecutions, h)
	}
	projectHookOccupancy(view)
	if len(view.Executions) > 0 {
		view.ExecutionClear = false
		if view.Status == "idle" {
			view.Status = "running"
			if !view.Evidence.Complete {
				view.Status = "unknown"
			}
		}
	}
}

func (c *Codex) projectHookExecutions(scope Scope, view *View) {
	for _, hook := range c.hooks[scope.Instance.ID+"/"+scope.NativeID] {
		view.HookExecutions = append(view.HookExecutions, hook)
	}
	projectHookOccupancy(view)
}

func projectHookOccupancy(view *View) {
	owned := map[string]bool{}
	for _, hook := range view.HookExecutions {
		owned[hook.ID] = true
	}
	var executions []Execution
	for _, execution := range view.Executions {
		if execution.Kind != "hook" || !owned[execution.ID] {
			executions = append(executions, execution)
		}
	}
	view.Executions = executions
	for _, hook := range view.HookExecutions {
		if hook.Status == "running" || hook.Status == "unknown" {
			view.Executions = append(view.Executions, Execution{ID: hook.ID, Kind: "hook", Status: hook.Status})
			if hook.Status == "unknown" {
				view.Evidence.Complete = false
				view.Evidence.Detail = "原生 hook 生命周期尚未核对完整"
			}
		}
	}
	slices.SortFunc(view.HookExecutions, func(a, b HookExecution) int {
		if a.StartedAt != b.StartedAt {
			return strings.Compare(a.StartedAt, b.StartedAt)
		}
		return strings.Compare(a.ID, b.ID)
	})
}
