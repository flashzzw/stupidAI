// Native probe policy fixture. This binary is not part of the console and
// does not change production capability flags.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type probeCase struct {
	Label    string           `json:"label"`
	Scenario string           `json:"scenario,omitempty"`
	Instance runtime.Instance `json:"instance"`
	Route    runtime.Route    `json:"route"`
}

type manifest struct {
	Root  string      `json:"root"`
	Cases []probeCase `json:"cases"`
}

var fixtureID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (c probeCase) name() string {
	name := c.Instance.Kind + "-" + c.Label
	if c.Scenario != "" {
		name += "-" + c.Scenario
	}
	return name
}

func knownScenario(scenario string) bool {
	switch scenario {
	case "", "authentication", "quota", "rate_limit", "service", "tool_failure":
		return true
	}
	return false
}

func inside(root, path string) bool {
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, actual)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func loadManifest(path string) (manifest, error) {
	var m manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	root, err := filepath.EvalSymlinks(m.Root)
	if err != nil || !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "netcap-native-probe-") || !inside(os.TempDir(), root) || !inside(root, path) || len(m.Cases) == 0 || len(m.Cases) > 21 {
		return m, fmt.Errorf("native probe fixture must use an owned temporary root and 1–21 cases")
	}
	m.Root = root
	seen := map[string]bool{}
	models := map[string]string{"codex": "gpt-6-astra", "claude": "claude-sonnet-4-6", "opencode": "fixture-model"}
	for _, c := range m.Cases {
		name := c.name()
		endpoint, err := url.Parse(c.Route.Endpoint)
		key := "NETCAP_NATIVE_PROBE_" + strings.ToUpper(c.Instance.Kind) + "_" + c.Label
		if seen[name] || !knownScenario(c.Scenario) || (c.Scenario != "" && c.Label != "A") || (c.Label != "A" && c.Label != "B") || models[c.Instance.Kind] == "" || c.Route.Model != models[c.Instance.Kind] || c.Route.InstanceID != c.Instance.ID || !fixtureID.MatchString(c.Instance.ID) || !fixtureID.MatchString(c.Route.ID) || c.Route.ConfigRevision == "" || c.Route.CredentialRevision == "" || c.Route.CredentialRef != "env:"+key || os.Getenv(key) != "fixture-"+c.Label {
			return m, fmt.Errorf("native probe fixture requires unique agent/label cases and synthetic credentials")
		}
		seen[name] = true
		if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return m, fmt.Errorf("native probe fixture permits only explicit local HTTP endpoints")
		}
		info, statErr := os.Stat(c.Instance.Profile)
		if statErr != nil || !info.IsDir() || !inside(root, c.Instance.Profile) || !filepath.IsAbs(c.Instance.Executable) || (c.Instance.Kind == "codex" && !filepath.IsAbs(c.Instance.ServerExecutable)) {
			return m, fmt.Errorf("native probe fixture profile/executable is not explicitly isolated")
		}
	}
	return m, nil
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func verifyResult(c probeCase, mode string, resolution runtime.Resolution, receipt runtime.Receipt, started time.Time) (any, error) {
	if !resolution.Evidence.Complete || !resolution.View.Evidence.Complete || !resolution.View.ExecutionClear {
		return nil, fmt.Errorf("native execution scope is not completely reconciled")
	}
	if c.Scenario == "" {
		if resolution.Status != "succeeded" {
			return nil, fmt.Errorf("native terminal is %s", resolution.Status)
		}
		return runtime.VerifyProbe(resolution.View, receipt.NativeTurn, mode, started)
	}
	for _, turn := range resolution.View.Turns {
		if turn.ID != receipt.NativeTurn {
			continue
		}
		if c.Scenario != "tool_failure" {
			if resolution.Status != "failed" || turn.Status != "failed" || turn.Failure == nil || turn.Failure.Source == "" || turn.Failure.Kind != c.Scenario {
				return nil, fmt.Errorf("expected structured %s native failure, got %+v", c.Scenario, turn.Failure)
			}
			return turn.Failure, nil
		}
		_, err := runtime.VerifyProbe(resolution.View, receipt.NativeTurn, mode, started)
		if err == nil {
			return nil, fmt.Errorf("failed tool was accepted as probe success")
		}
		if turn.Failure != nil && turn.Failure.Kind != "tool" && turn.Failure.Kind != "session" && turn.Failure.Kind != "cancelled" && turn.Failure.Kind != "unknown" {
			return nil, fmt.Errorf("tool failure was attributed to a provider failure: %s", turn.Failure.Kind)
		}
		for _, tool := range resolution.View.Tools {
			if tool.TurnID == turn.ID && tool.Command == "exit 7" {
				return map[string]string{"expected_failure": "tool_failure", "native_status": resolution.Status, "assertion_error": err.Error()}, nil
			}
		}
		return nil, fmt.Errorf("missing native evidence for the rejected/failed tool")
	}
	return nil, fmt.Errorf("missing matched native terminal turn")
}

func runCase(parent context.Context, m manifest, c probeCase, mode string) (err error) {
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	name := c.name() + "-" + mode
	// Keep the nested Codex Unix socket below Linux's sockaddr_un path bound.
	stateDir := filepath.Join(m.Root, "r", uuid.NewString()[:8])
	r := runtime.NewAdapters(stateDir)
	defer func() { err = errors.Join(err, r.Close()) }()
	description, err := r.Describe(ctx, c.Instance)
	if err != nil || description.Compatibility != "supported" {
		return fmt.Errorf("%s has not passed the base CLI version contract", name)
	}
	scope := runtime.Scope{SessionID: uuid.NewString(), Purpose: "probe", Instance: c.Instance, Route: c.Route, Cwd: filepath.Join(m.Root, "work")}
	scope.CredentialStamp, err = runtime.CredentialStamp(c.Route)
	if err != nil {
		return err
	}
	created, err := r.Execute(ctx, runtime.Intent{ID: uuid.NewString(), Kind: "create", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope, Permission: "read_only"})
	if err != nil {
		return fmt.Errorf("%s create: %w", name, err)
	}
	if created.NativeID == "" {
		return fmt.Errorf("%s create has no native identity", name)
	}
	scope.NativeID = created.NativeID
	started := time.Now().UTC()
	id := uuid.NewString()
	intent := runtime.Intent{ID: id, Kind: "send", CreatedAt: started.Format(time.RFC3339Nano), Scope: scope, Text: runtime.ProbePrompt(mode), Permission: "read_only", OwnedInputs: []string{id}}
	receipt, err := r.Execute(ctx, intent)
	if err != nil {
		return fmt.Errorf("%s send (never retried): %w", name, err)
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		resolution, err := r.Reconcile(ctx, intent, receipt)
		if err != nil {
			return fmt.Errorf("%s reconciliation: %w", name, err)
		}
		if resolution.Receipt.NativeID != "" {
			receipt = resolution.Receipt
		}
		if err := writeJSON(filepath.Join(m.Root, "results", name+"-view.json"), resolution.View); err != nil {
			return err
		}
		if resolution.State == "finished" {
			result, err := verifyResult(c, mode, resolution, receipt, started)
			if err != nil {
				return fmt.Errorf("%s proof: %w", name, err)
			}
			if err := writeJSON(filepath.Join(m.Root, "results", name+".json"), map[string]any{"agent": c.Instance.Kind, "label": c.Label, "mode": mode, "result": result, "receipt": receipt, "scope": scope, "state_dir": stateDir, "version": description.Version, "proof": resolution.Evidence}); err != nil {
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s execution remained unverified: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func run(path string) error {
	m, err := loadManifest(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(m.Root, "results"), 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for _, c := range m.Cases {
		modes := []string{"utc-date", "text"}
		if c.Scenario != "" {
			modes = []string{"utc-date"}
		}
		for _, mode := range modes {
			if err := runCase(ctx, m, c, mode); err != nil {
				return err
			}
			fmt.Println("PASS", c.name()+"-"+mode)
		}
	}
	return nil
}

func main() {
	path := flag.String("fixture", "", "owned fixture manifest; no production capability override")
	flag.Parse()
	if *path == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
