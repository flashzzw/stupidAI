// Native collision fixture: production adapters with identical native IDs in
// separate Claude state directories. Never uses the user's CLI configuration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type profile struct {
	Instance runtime.Instance `json:"instance"`
	Route    runtime.Route    `json:"route"`
}
type input struct {
	StateDir string    `json:"state_dir"`
	NativeID string    `json:"native_id"`
	Cwd      string    `json:"cwd"`
	Profiles []profile `json:"profiles"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	var config input
	if err := json.NewDecoder(os.Stdin).Decode(&config); err != nil {
		return err
	}
	if len(config.Profiles) != 2 {
		return fmt.Errorf("need two fixture profiles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	native := config.NativeID
	if native == "" {
		native = uuid.NewString()
	}
	scopes := []runtime.Scope{}
	adapter := runtime.NewAdapters(config.StateDir)
	defer func() { adapter.Close() }()
	for _, p := range config.Profiles {
		scope := runtime.Scope{Instance: p.Instance, Route: p.Route, Cwd: config.Cwd, SessionID: uuid.NewString()}
		if (scope.Instance.Kind != "claude" && scope.Instance.Kind != "codex") || !strings.HasPrefix(scope.Instance.Profile, "/tmp/netcap-") || !strings.HasPrefix(scope.Route.Endpoint, "http://127.0.0.1:") {
			return fmt.Errorf("only owned local CLI fixtures allowed")
		}
		var err error
		scope.StateNamespace, err = runtime.StateNamespace(scope)
		if err != nil {
			return err
		}
		scope.CredentialStamp, err = runtime.CredentialStamp(scope.Route)
		if err != nil {
			return err
		}
		if config.NativeID == "" {
			receipt, err := adapter.Execute(ctx, runtime.Intent{ID: native, Kind: "create", Scope: scope, Permission: "read_only"})
			if err != nil {
				return err
			}
			if receipt.NativeID != native {
				return fmt.Errorf("native ID changed")
			}
		}
		scope.NativeID = native
		view, err := adapter.ObserveFresh(ctx, scope)
		if err != nil {
			return err
		}
		if view.NativeID != native {
			return fmt.Errorf("initial native read used a different ID")
		}

		scopes = append(scopes, scope)
	}
	if scopes[0].StateNamespace == scopes[1].StateNamespace {
		return fmt.Errorf("not separate native state")
	}
	wait := func(scope runtime.Scope, marker string) (runtime.View, error) {
		for ctx.Err() == nil {
			view, err := adapter.ObserveFresh(ctx, scope)
			if err != nil {
				return view, err
			}
			body, _ := json.Marshal(view.Messages)
			if view.NativeID != native {
				return view, fmt.Errorf("observed wrong native ID")
			}
			if view.ExecutionClear && view.Evidence.Complete && strings.Contains(string(body), "fixture "+marker) {
				return view, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return runtime.View{}, ctx.Err()
	}
	views := []runtime.View{}
	for round := 1; round <= 3; round++ {
		if round == 2 {
			if err := adapter.Close(); err != nil {
				return err
			}
			adapter = runtime.NewAdapters(config.StateDir)
		}
		for n, scope := range scopes {
			letter := []string{"A", "B"}[n]
			text := fmt.Sprintf("NETCAP_%s_TURN_%d SAME_NATIVE_%s", letter, round, letter)
			_, err := adapter.Execute(ctx, runtime.Intent{ID: uuid.NewString(), Kind: "send", Scope: scope, Text: text, Permission: "read_only"})
			if err != nil {
				return err
			}
		}
		views = nil
		for n, scope := range scopes {
			letter := []string{"A", "B"}[n]
			marker := fmt.Sprintf("%s: NETCAP_%s_TURN_%d", letter, letter, round)
			view, err := wait(scope, marker)
			if err != nil {
				return err
			}
			body, _ := json.Marshal(view.Messages)
			opposite := []string{"B", "A"}[n]
			if strings.Contains(string(body), "SAME_NATIVE_"+opposite) {
				return fmt.Errorf("same-ID history crossed state namespace")
			}
			views = append(views, view)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"native_id": native, "state_namespaces": []string{scopes[0].StateNamespace, scopes[1].StateNamespace}, "checks": map[string]bool{"same_actual_native_id": true, "distinct_state_directories": true, "three_rounds_without_history_mix": true, "fresh_adapter_resume_keeps_original_ids": true}, "message_counts": []int{len(views[0].Messages), len(views[1].Messages)}})
}
