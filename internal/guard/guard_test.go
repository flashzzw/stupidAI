package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/config"
	"codex-netcap/internal/state"
)

type call struct {
	Method string
	Params map[string]any
}
type fakeProbe struct {
	active    bool
	starts    int
	source    string
	result    *codex.ProbeResult
	cancelled bool
}

func (p *fakeProbe) Start(source string) error {
	p.active = true
	p.starts++
	p.source = source
	return nil
}
func (p *fakeProbe) Poll() *codex.ProbeResult {
	if p.result == nil {
		return nil
	}
	r := p.result
	p.result = nil
	p.active = false
	return r
}
func (p *fakeProbe) Cancel() {
	if p.active {
		p.cancelled = true
		p.result = &codex.ProbeResult{Cancelled: true}
	}
}
func (p *fakeProbe) Active() bool { return p.active }

type fakeCodex struct {
	threads      map[string]codex.Thread
	turns        map[string][]codex.Turn
	queues       map[string][]codex.QueueItem
	goals        map[string]*codex.NativeGoal
	events       []codex.Message
	calls        []call
	autoStart    bool
	afterAdd     func() error
	deleteResult *bool
	seq          int
	now          func() time.Time
	maxActive    int
}

func newFake() *fakeCodex {
	return &fakeCodex{threads: map[string]codex.Thread{}, turns: map[string][]codex.Turn{}, queues: map[string][]codex.QueueItem{}, goals: map[string]*codex.NativeGoal{}}
}
func (f *fakeCodex) Close() error           { return nil }
func (f *fakeCodex) Drain() []codex.Message { e := f.events; f.events = nil; return e }
func (f *fakeCodex) count(method string) int {
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}
func (f *fakeCodex) addThread(id string) {
	f.threads[id] = codex.Thread{ID: id, Model: "model", Provider: "provider", Cwd: "/tmp", Status: codex.Status{Type: "idle"}}
}
func (f *fakeCodex) start(id string, q codex.QueueItem) codex.Turn {
	f.seq++
	t := codex.Turn{ID: fmt.Sprintf("turn-%d", f.seq), Status: "inProgress", StartedAt: f.now().Unix(), ItemsView: "full", Items: []codex.Item{{ID: "item-" + q.ID, Type: "userMessage", ClientID: q.ClientID}}}
	f.turns[id] = append([]codex.Turn{t}, f.turns[id]...)
	v := f.threads[id]
	v.Status.Type = "active"
	f.threads[id] = v
	for i, item := range f.queues[id] {
		if item.ID == q.ID {
			f.queues[id] = append(f.queues[id][:i], f.queues[id][i+1:]...)
			break
		}
	}
	return t
}
func (f *fakeCodex) complete(id, answer, status string) {
	t := &f.turns[id][0]
	t.Status = status
	t.CompletedAt = f.now().Unix()
	if answer != "" {
		t.Items = append(t.Items, codex.Item{ID: "answer-" + t.ID, Type: "agentMessage", Text: answer, Phase: "final_answer"})
	}
	if status == "failed" {
		t.Error = &codex.TurnError{Message: "503 service unavailable"}
	}
	thread := f.threads[id]
	thread.Status.Type = "idle"
	f.threads[id] = thread
}
func (f *fakeCodex) Call(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, _ := json.Marshal(params)
	p := map[string]any{}
	_ = json.Unmarshal(b, &p)
	f.calls = append(f.calls, call{method, p})
	id, _ := p["threadId"].(string)
	var result any
	switch method {
	case "thread/loaded/list":
		ids := []string{}
		for id, t := range f.threads {
			if t.Status.Type != "notLoaded" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		result = map[string]any{"data": ids}
	case "thread/read":
		t, ok := f.threads[id]
		if !ok {
			return &codex.RPCError{Code: -32000, Message: "not found"}
		}
		result = map[string]any{"thread": t}
	case "thread/resume":
		t := f.threads[id]
		t.Status.Type = "idle"
		f.threads[id] = t
		result = codex.Started{Thread: t}
	case "thread/turns/list":
		result = map[string]any{"data": f.turns[id]}
	case "thread/items/list":
		items := []any{}
		for _, t := range f.turns[id] {
			if t.ID == p["turnId"] {
				for _, i := range t.Items {
					items = append(items, map[string]any{"turnId": t.ID, "item": i})
				}
			}
		}
		result = map[string]any{"data": items}
	case "thread/goal/get":
		result = map[string]any{"goal": f.goals[id]}
	case "thread/goal/set":
		goal := f.goals[id]
		if goal == nil {
			goal = &codex.NativeGoal{ThreadID: id}
			f.goals[id] = goal
		}
		if objective, ok := p["objective"].(string); ok {
			goal.Objective = objective
		}
		goal.Status = p["status"].(string)
		if goal.Status == "active" {
			n := 0
			for _, g := range f.goals {
				if g.Status == "active" {
					n++
				}
			}
			f.maxActive = max(f.maxActive, n)
			f.start(id, codex.QueueItem{ID: fmt.Sprintf("native-%d", f.seq), ClientID: "native-goal"})
		}
		result = map[string]any{"goal": goal}
	case "thread/start":
		f.seq++
		id = fmt.Sprintf("owned-%d", f.seq)
		f.addThread(id)
		t := f.threads[id]
		t.Source = p["threadSource"].(string)
		t.Cwd = p["cwd"].(string)
		f.threads[id] = t
		result = codex.Started{Thread: t}
	case "thread/queue/list":
		result = map[string]any{"data": f.queues[id]}
	case "thread/queue/add":
		f.seq++
		q := codex.QueueItem{ID: fmt.Sprintf("q-%d", f.seq), ClientID: p["clientUserMessageId"].(string)}
		f.queues[id] = append(f.queues[id], q)
		if f.autoStart {
			f.start(id, q)
		}
		if f.afterAdd != nil {
			if err := f.afterAdd(); err != nil {
				return err
			}
		}
		result = map[string]any{"queuedSubmission": q}
	case "thread/queue/start":
		if f.threads[id].Status.Type != "idle" {
			return &codex.RPCError{Code: -32000, Message: "busy"}
		}
		found := false
		for _, q := range f.queues[id] {
			if q.ID == p["queuedSubmissionId"] {
				result = map[string]any{"turn": f.start(id, q)}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing queue")
		}
	case "thread/queue/delete":
		deleted := false
		for i, q := range f.queues[id] {
			if q.ID == p["queuedSubmissionId"] && (f.deleteResult == nil || *f.deleteResult) {
				f.queues[id] = append(f.queues[id][:i], f.queues[id][i+1:]...)
				deleted = true
				break
			}
		}
		result = map[string]any{"deleted": deleted}
	case "turn/interrupt":
		for i, t := range f.turns[id] {
			if t.ID == p["turnId"] {
				f.turns[id][i].Status = "interrupted"
				f.turns[id][i].CompletedAt = f.now().Unix()
			}
		}
		t := f.threads[id]
		t.Status.Type = "idle"
		f.threads[id] = t
		result = map[string]any{}
	default:
		return fmt.Errorf("unexpected RPC %s", method)
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

type fixture struct {
	t     *testing.T
	g     *Guard
	f     *fakeCodex
	p     *fakeProbe
	store *state.Store
	now   time.Time
}

func setup(t *testing.T, goalCount int) *fixture {
	t.Helper()
	c := config.Default()
	c.StateDir = t.TempDir()
	c.CodexHome = t.TempDir()
	c.Cities = []string{"纽约", "洛杉矶", "多伦多"}
	c.WeatherScript = filepath.Join(t.TempDir(), "weather.py")
	c.PollSeconds = 1
	for i := range goalCount {
		id := fmt.Sprintf("goal-%d", i+1)
		file := filepath.Join(t.TempDir(), "goal.md")
		if err := os.WriteFile(file, []byte("Complete "+id), 0600); err != nil {
			t.Fatal(err)
		}
		c.Goals = append(c.Goals, config.Goal{ID: id, File: file, Cwd: t.TempDir()})
	}
	store, err := state.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	x := &fixture{t: t, f: newFake(), p: &fakeProbe{}, store: store, now: time.Unix(1800000000, 0)}
	x.g = New(c, state.New(), store.Save)
	x.g.Now = func() time.Time { return x.now }
	x.f.now = x.g.Now
	x.g.Runner = x.p
	x.g.SetClient(x.f)
	if err = x.g.Initialize(); err != nil {
		t.Fatal(err)
	}
	return x
}
func (x *fixture) step() {
	x.t.Helper()
	x.now = x.now.Add(time.Second)
	if err := x.g.Step(context.Background()); err != nil {
		x.t.Fatal(err)
	}
}
func (x *fixture) apply(c Command) {
	x.t.Helper()
	if err := x.g.Apply(context.Background(), c); err != nil {
		x.t.Fatal(err)
	}
}
func (x *fixture) probeOK() {
	x.t.Helper()
	if !x.p.active {
		x.step()
	}
	if !x.p.active {
		x.t.Fatal("probe did not start")
	}
	x.p.result = &codex.ProbeResult{Success: true, DateExecuted: true, Output: x.now.UTC().Format("2006-01-02T15:04:05Z")}
	x.step()
}
func (x *fixture) weatherRunning() {
	x.t.Helper()
	for i := 0; i < 6; i++ {
		if o := x.g.State.Operation; o != nil && len(x.f.turns[o.ThreadID]) > 0 {
			return
		}
		x.step()
	}
	x.t.Fatal("weather did not start")
}
func (x *fixture) weatherOK() {
	x.t.Helper()
	x.weatherRunning()
	o := x.g.State.Operation
	zero := 0
	x.f.turns[o.ThreadID][0].Items = append(x.f.turns[o.ThreadID][0].Items, codex.Item{Type: "commandExecution", Command: x.g.Config.WeatherCommand(o.City), ExitCode: &zero})
	x.f.complete(o.ThreadID, o.City+"：晴，20°C", "completed")
	x.step()
}
func (x *fixture) goalRunning(id string) {
	x.t.Helper()
	for i := 0; i < 8; i++ {
		j := x.g.State.Goal(id)
		if j != nil && j.ThreadID != "" && x.f.goals[j.ThreadID] != nil && x.f.goals[j.ThreadID].Status == "active" {
			return
		}
		x.step()
	}
	x.t.Fatal("goal did not activate")
}

func TestRunAutomaticallyProbesUntilSuccess(t *testing.T) {
	x := setup(t, 0)
	x.step()
	if !x.g.State.Running || x.p.starts != 1 || x.f.count("thread/start") != 0 {
		t.Fatal("run did not start with temporary probe")
	}
	x.p.result = &codex.ProbeResult{Error: "503 unavailable"}
	x.step()
	if x.g.State.Health.RetryAt.Sub(x.now) < 2*time.Second {
		t.Fatal("missing ordinary backoff")
	}
	x.now = x.now.Add(time.Minute)
	x.step()
	if x.p.starts != 2 {
		t.Fatal("did not retry temporary probe")
	}
	x.probeOK()
	if x.g.State.NeedProbe || x.g.State.Weather.ThreadID == "" {
		t.Fatal("did not transition to persistent weather")
	}
}
func TestPressureErrorsHaveNoBackoff(t *testing.T) {
	for _, s := range []string{"high demand", "We're experiencing HIGH DEMAND", "rate limit", "rateLimitExceeded", "HTTP 429 Too Many Requests", "status code: 429", "429"} {
		if RetryDelay(s, 9) != 0 {
			t.Error("unexpected pressure backoff", s)
		}
	}
	if RetryDelay("HTTP 503", 1) <= 0 {
		t.Fatal("other failure lacks backoff")
	}
	if RetryDelay("HTTP 503 request_id=abc429xyz", 1) <= 0 {
		t.Fatal("request ID was mistaken for rate limit")
	}
}
func TestProbeFailureNeverStartsGoal(t *testing.T) {
	x := setup(t, 1)
	x.step()
	x.p.result = &codex.ProbeResult{Error: "rate limit"}
	x.step()
	if x.p.starts != 2 || x.f.count("thread/start") != 0 {
		t.Fatal("pressure retry or probe gate failed")
	}
}
func TestWeatherUsesPersistentThreadAndOnlyAdvancesAfterToolSuccess(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	id := x.g.State.Weather.ThreadID
	x.weatherOK()
	if x.g.State.Weather.CityIndex != 1 || x.g.State.Weather.LastCity != "纽约" {
		t.Fatal("city did not advance")
	}
	success := x.g.State.Weather.LastSuccess
	x.now = success.Add(8 * time.Second)
	x.step()
	if x.g.State.Operation != nil {
		t.Fatal("interval not based on completion")
	}
	x.now = success.Add(11 * time.Second)
	x.weatherRunning()
	if x.g.State.Operation.City != "洛杉矶" || x.g.State.Operation.ThreadID != id {
		t.Fatal("did not reuse same saved thread")
	}
	x.f.complete(id, "OK", "completed")
	x.step()
	if x.g.State.Weather.CityIndex != 1 {
		t.Fatal("meaningless reply advanced city")
	}
	for _, c := range x.f.calls {
		if c.Method == "thread/queue/add" && !strings.Contains(fmt.Sprint(c.Params["input"]), "weather.py") {
			t.Fatal("wrong keepalive prompt")
		}
	}
}
func TestAllManagedSessionsAreUnrestricted(t *testing.T) {
	x := setup(t, 1)
	x.probeOK()
	x.goalRunning("goal-1")
	for _, c := range x.f.calls {
		if c.Method == "thread/start" && (c.Params["sandbox"] != "danger-full-access" || c.Params["approvalPolicy"] != "never") {
			t.Fatal("restricted managed session", c.Params)
		}
	}
}
func TestNativeGoalCompletionControlsSequentialFreshSessions(t *testing.T) {
	x := setup(t, 2)
	x.probeOK()
	x.goalRunning("goal-1")
	first := x.g.State.Goal("goal-1")
	x.f.complete(first.ThreadID, "I am complete", "completed")
	x.step()
	if x.g.State.Goal("goal-2").ThreadID != "" {
		t.Fatal("text response treated as native completion")
	}
	x.f.goals[first.ThreadID].Status = "complete"
	x.step()
	x.goalRunning("goal-2")
	second := x.g.State.Goal("goal-2")
	if first.ThreadID == second.ThreadID || first.Status != "complete" || x.f.maxActive > 1 {
		t.Fatal("goals did not run sequentially in new sessions")
	}
	x.f.complete(second.ThreadID, "done", "completed")
	x.f.goals[second.ThreadID].Status = "complete"
	x.step()
	x.weatherRunning()
	if !x.g.State.Running {
		t.Fatal("native goal's own messages mistaken for human")
	}
}
func TestCompleteGoalWaitsForActiveTurn(t *testing.T) {
	x := setup(t, 2)
	x.probeOK()
	x.goalRunning("goal-1")
	j := x.g.State.Goal("goal-1")
	x.f.goals[j.ThreadID].Status = "complete"
	x.step()
	if x.g.State.Goal("goal-2").ThreadID != "" {
		t.Fatal("next goal started before prior turn ended")
	}
}
func TestGoalReorderingDoesNotSwitchCurrent(t *testing.T) {
	x := setup(t, 2)
	x.probeOK()
	x.goalRunning("goal-1")
	x.apply(Command{Action: "goal-order", Order: []string{"goal-2", "goal-1"}})
	x.step()
	if x.g.State.CurrentGoal != "goal-1" || x.g.State.Goal("goal-2").ThreadID != "" {
		t.Fatal("reorder started a second goal")
	}
}
func TestGoalFailurePausesBeforeReprobeAndResumesSameGoal(t *testing.T) {
	x := setup(t, 1)
	x.probeOK()
	x.goalRunning("goal-1")
	j := x.g.State.Goal("goal-1")
	id := j.ThreadID
	x.f.complete(id, "", "failed")
	x.f.turns[id][0].Error = &codex.TurnError{Message: "high demand"}
	x.step()
	if x.f.goals[id].Status != "paused" || !x.p.Active() {
		t.Fatal("did not pause failed native goal before immediate probe")
	}
	x.probeOK()
	x.goalRunning("goal-1")
	if j.ThreadID != id || x.f.count("thread/start") != 1 {
		t.Fatal("recovery created replacement goal")
	}
}
func TestBlockedOrBudgetGoalDoesNotAdvance(t *testing.T) {
	for _, status := range []string{"blocked", "budgetLimited", "usageLimited"} {
		t.Run(status, func(t *testing.T) {
			x := setup(t, 2)
			x.probeOK()
			x.goalRunning("goal-1")
			j := x.g.State.Goal("goal-1")
			x.f.complete(j.ThreadID, "need decision", "completed")
			x.f.goals[j.ThreadID].Status = status
			x.step()
			if x.g.State.Goal("goal-2").ThreadID != "" || j.Status != "blocked" {
				t.Fatal("blocked goal advanced")
			}
		})
	}
}
func TestNewHumanSessionPausesWeatherAndRequiresResume(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.weatherOK()
	x.f.addThread("human")
	x.f.start("human", codex.QueueItem{ID: "u", ClientID: "user"})
	x.step()
	if x.g.State.Running || x.g.State.PauseReason != "human" {
		t.Fatal("human did not pause automation")
	}
	x.f.complete("human", "done", "completed")
	x.now = x.now.Add(time.Hour)
	x.step()
	if x.g.State.Running || x.g.State.Operation != nil {
		t.Fatal("resumed without explicit user intent")
	}
	x.apply(Command{Action: "resume"})
	x.step()
	if !x.p.active {
		t.Fatal("explicit resume did not reprobe")
	}
}
func TestResumingWeatherThreadHandsItToHuman(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.weatherOK()
	old := x.g.State.Weather.ThreadID
	x.f.start(old, codex.QueueItem{ID: "user-input", ClientID: "user"})
	x.step()
	if !x.g.State.Weather.HumanOwned || x.g.State.Running {
		t.Fatal("resumed weather conversation not handed over")
	}
	x.f.complete(old, "human done", "completed")
	x.step()
	x.apply(Command{Action: "resume"})
	x.probeOK()
	for i := 0; i < 3; i++ {
		x.step()
	}
	if x.g.State.Weather.ThreadID == old || !x.g.State.Running {
		t.Fatal("explicit resume reused human-owned weather thread or remained stuck")
	}
}
func TestHumanDuringNativeGoalPausesGoalNotHuman(t *testing.T) {
	x := setup(t, 1)
	x.probeOK()
	x.goalRunning("goal-1")
	x.f.addThread("human")
	x.f.start("human", codex.QueueItem{ID: "u", ClientID: "user"})
	x.step()
	j := x.g.State.Goal("goal-1")
	if x.g.State.Running || x.f.goals[j.ThreadID].Status != "paused" || x.f.count("turn/interrupt") != 0 {
		t.Fatal("goal did not yield safely")
	}
}
func TestHumanSteeredWeatherIsNotInterrupted(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.weatherRunning()
	id := x.g.State.Weather.ThreadID
	x.f.turns[id][0].Items = append(x.f.turns[id][0].Items, codex.Item{ID: "human", Type: "userMessage", ClientID: "user"})
	x.step()
	if x.f.count("turn/interrupt") != 0 || x.g.State.Running {
		t.Fatal("human input interrupted")
	}
}
func TestLostQueueReplyReconcilesWithoutResend(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.f.afterAdd = func() error { return &codex.Unknown{Cause: errors.New("lost reply")} }
	if err := x.g.Step(context.Background()); err == nil {
		t.Fatal("expected lost response")
	}
	x.f.afterAdd = nil
	s, err := x.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	x.g = New(x.g.Config, s, x.store.Save)
	x.g.Runner = x.p
	x.g.Now = func() time.Time { return x.now }
	x.g.SetClient(x.f)
	x.step()
	x.step()
	if x.f.count("thread/queue/add") != 1 {
		t.Fatal("duplicate weather message")
	}
}
func TestDeleteFalseIsNotCancellation(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.step()
	no := false
	x.f.deleteResult = &no
	x.apply(Command{Action: "pause"})
	x.step()
	if x.g.State.Operation == nil || x.g.State.Operation.Status != "uncertain" {
		t.Fatal("deleted=false discarded operation")
	}
}
func TestUnknownHistoryNeverResends(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.g.State.Operation = &state.Operation{ID: state.ID(), Kind: "weather", ThreadID: x.g.State.Weather.ThreadID, Status: "uncertain", CreatedAt: x.now}
	for i := 0; i < 3; i++ {
		x.step()
	}
	if x.f.count("thread/queue/add") != 0 || x.g.State.Operation == nil {
		t.Fatal("unknown operation retried")
	}
}
func TestPauseCancelsOnlyPureWeather(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.weatherRunning()
	x.apply(Command{Action: "pause"})
	x.step()
	x.step()
	if x.f.count("turn/interrupt") != 1 || x.g.State.Operation != nil {
		t.Fatal("weather did not stop")
	}
}
func TestGoalSnapshotIsStableAcrossFileEdits(t *testing.T) {
	x := setup(t, 1)
	j := x.g.State.Goal("goal-1")
	original, err := os.ReadFile(j.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(j.File, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = x.g.Initialize(); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(j.Snapshot)
	if string(actual) != string(original) {
		t.Fatal("task snapshot silently changed")
	}
}
func TestNoSendBeforeDurableIntent(t *testing.T) {
	x := setup(t, 0)
	x.probeOK()
	x.g.save = func(*state.State) error { return errors.New("disk full") }
	if err := x.g.Step(context.Background()); err == nil {
		t.Fatal("missing persistence failure")
	}
	if x.f.count("thread/queue/add") != 0 {
		t.Fatal("sent without durable intent")
	}
}
