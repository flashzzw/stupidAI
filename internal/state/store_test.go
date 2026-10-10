package state

import (
	"testing"
	"time"
)

func TestRestartPreservesNativeGoalWeatherAndPause(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, e := Open(dir); e == nil {
		duplicate.Close()
		t.Fatal("two writers")
	}
	v := New()
	v.Running = false
	v.PauseReason = "human"
	v.CurrentGoal = "goal"
	v.Goals = []*Goal{{ID: "goal", ThreadID: "thread", Status: "running"}}
	v.Weather = Weather{ThreadID: "weather", HumanOwned: true, CityIndex: 2}
	v.Operation = &Operation{ID: ID(), ThreadID: "weather", Status: "uncertain", CreatedAt: time.Now()}
	v.Log(time.Now(), "test", "api_key=secret-value")
	if err = s.Save(v); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Running || !loaded.Weather.HumanOwned || loaded.Operation.ID != v.Operation.ID || loaded.CurrentGoal != "goal" || loaded.Weather.CityIndex != 2 {
		t.Fatal("state not preserved")
	}
	if loaded.Events[0].Message != "[redacted]" {
		t.Fatal("credential log")
	}
}
