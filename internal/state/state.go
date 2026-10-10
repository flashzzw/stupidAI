package state

import (
	"crypto/rand"
	"regexp"
	"strings"
	"time"
)

const ProbePrompt = "请执行 date -u +%FT%TZ，只返回命令输出。"

func ID() string { return "netcap-go:" + rand.Text() }

type Health struct {
	Status      string    `json:"status"`
	LastSuccess time.Time `json:"last_success_at,omitzero"`
	RetryAt     time.Time `json:"retry_at,omitzero"`
	Failures    int       `json:"failures"`
	Reason      string    `json:"reason,omitempty"`
}
type Goal struct {
	ID           string `json:"id"`
	File         string `json:"file"`
	Cwd          string `json:"cwd"`
	Snapshot     string `json:"snapshot"`
	Hash         string `json:"sha256"`
	Objective    string `json:"objective"`
	TokenBudget  *int64 `json:"token_budget,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	CreateTag    string `json:"create_tag,omitempty"`
	Status       string `json:"status"`
	NativeStatus string `json:"native_status,omitempty"`
	Reason       string `json:"reason,omitempty"`
	PauseReason  string `json:"pause_reason,omitempty"`
	GoalIntent   bool   `json:"goal_intent"`
	LastTurn     string `json:"last_completed_turn,omitempty"`
	TokensUsed   int64  `json:"tokens_used"`
}
type Weather struct {
	ThreadID    string    `json:"thread_id,omitempty"`
	CreateTag   string    `json:"create_tag,omitempty"`
	CityIndex   int       `json:"city_index"`
	LastCity    string    `json:"last_city,omitempty"`
	LastOutput  string    `json:"last_output,omitempty"`
	LastSuccess time.Time `json:"last_success_at,omitzero"`
	RetryAt     time.Time `json:"retry_at,omitzero"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	HumanOwned  bool      `json:"human_owned"`
	Failures    int       `json:"failures"`
}
type Probe struct {
	ID        string    `json:"id,omitempty"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at,omitzero"`
	Output    string    `json:"output,omitempty"`
	Attempts  int       `json:"attempts"`
}
type Operation struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"`
	ThreadID           string    `json:"thread_id"`
	City               string    `json:"city"`
	QueueID            string    `json:"queue_id,omitempty"`
	TurnID             string    `json:"turn_id,omitempty"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	FinishedAt         time.Time `json:"finished_at,omitzero"`
	Cancel             bool      `json:"cancel_requested"`
	Human              bool      `json:"human_takeover"`
	InterruptRequested bool      `json:"interrupt_requested"`
	Reason             string    `json:"reason,omitempty"`
}
type Observation struct {
	LastTurn  string `json:"last_turn"`
	LastUser  string `json:"last_user"`
	UpdatedAt int64  `json:"updated_at"`
}
type Event struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}
type State struct {
	Version     int                    `json:"version"`
	Running     bool                   `json:"running"`
	PauseReason string                 `json:"pause_reason,omitempty"`
	NeedProbe   bool                   `json:"need_probe"`
	ManualProbe bool                   `json:"manual_probe"`
	Health      Health                 `json:"health"`
	Probe       Probe                  `json:"probe"`
	Weather     Weather                `json:"weather"`
	Goals       []*Goal                `json:"goals"`
	CurrentGoal string                 `json:"current_goal_id,omitempty"`
	Operation   *Operation             `json:"operation,omitempty"`
	History     []Operation            `json:"operations"`
	Observed    map[string]Observation `json:"observed"`
	Events      []Event                `json:"events"`
	Seq         int64                  `json:"seq"`
}

func New() *State {
	return &State{Version: 2, Running: true, NeedProbe: true, Health: Health{Status: "unknown"}, Probe: Probe{Status: "idle"}, Weather: Weather{Status: "idle"}, Goals: []*Goal{}, History: []Operation{}, Events: []Event{}, Observed: map[string]Observation{}}
}
func (s *State) Goal(id string) *Goal {
	for _, g := range s.Goals {
		if g.ID == id {
			return g
		}
	}
	return nil
}
func (s *State) OwnsInput(id string) bool {
	if id == "" {
		return false
	}
	if s.Operation != nil && s.Operation.ID == id {
		return true
	}
	for _, o := range s.History {
		if o.ID == id {
			return true
		}
	}
	return false
}
func (s *State) Log(now time.Time, kind, message string) {
	s.Seq++
	s.Events = append(s.Events, Event{s.Seq, now, kind, Redact(message)})
	if len(s.Events) > 300 {
		s.Events = s.Events[len(s.Events)-300:]
	}
}
func (s *State) Finish(now time.Time, status, reason string) {
	o := s.Operation
	o.Status = status
	o.FinishedAt = now
	o.Reason = Redact(reason)
	s.History = append(s.History, *o)
	if len(s.History) > 500 {
		s.History = s.History[len(s.History)-500:]
	}
	s.Log(now, status, o.Kind+" "+o.City+" "+reason)
	s.Operation = nil
}

var secret = regexp.MustCompile(`(?i)(bearer\s+\S+|\bsk-[A-Za-z0-9_-]+|(?:api[_-]?key|token|authorization)\s*["']?\s*[:=]\s*["']?[^\s,;}"']+)`)

func Redact(s string) string {
	s = secret.ReplaceAllString(s, "[redacted]")
	s = strings.ReplaceAll(s, "\x00", "")
	r := []rune(s)
	if len(r) > 1000 {
		s = string(r[:1000]) + "…"
	}
	return s
}
