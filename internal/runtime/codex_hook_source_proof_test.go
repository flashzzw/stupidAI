package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCodexHookSourceProofColdBoundary(t *testing.T) {
	for _, scenario := range []string{"unloaded", "loaded", "missing-list", "external-loaded", "bad-policy-hash", "changed-config", "wrong-binding", "bad-version", "bad-time", "candidate-drift", "no-proof", "policy-error", "policy-changed", "warm-policy-error", "warm-policy-changed"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, old, config, _ := projectHookHarness(t)
			config["config"] = map[string]any{}
			originalCall := f.call
			status := "notLoaded"
			complete := true
			policyError, policyChanged := false, false
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "config/read" && params["includeLayers"] == false {
					return config, nil
				}
				if method == "configRequirements/read" {
					if policyError {
						return nil, fmt.Errorf("owned policy read failure")
					}
					if policyChanged {
						return map[string]any{"requirements": map[string]any{"allowManagedHooksOnly": true}}, nil
					}
					return map[string]any{"requirements": nil}, nil
				}
				if method == "thread/read" {
					return map[string]any{"thread": map[string]any{"id": old.NativeID, "status": map[string]any{"type": status}}}, nil
				}
				if method == "thread/loaded/list" {
					if scenario == "external-loaded" {
						return map[string]any{"data": []string{old.NativeID}}, nil
					}
					if !complete {
						return map[string]any{}, nil
					}
					return map[string]any{"data": []string{}}, nil
				}
				return originalCall(method, params)
			}
			path := filepath.Join(old.Instance.Profile, "hooks.json")
			projectWrite(t, path, `{"hooks":{}}`)
			var err error
			old.CodexUserHooks, err = ReadCodexUserHooks(old)
			if err != nil {
				t.Fatal(err)
			}
			old.CodexProjectHooks, err = readCodexProjectHooks(context.Background(), f, old)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, err := c.prepareSelectedHooks(context.Background(), f, old)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.rememberSelectedHooks(context.Background(), f, old, old.NativeID, fingerprint); err != nil {
				t.Fatal(err)
			}
			projectWrite(t, path, `{"hooks":{"Stop":[]}}`)
			target := old
			target.CodexUserHooks, err = ReadCodexUserHooks(target)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := c.ReadCodexHookSourceProof(context.Background(), old, target, nil)
			if err != nil || ValidateCodexHookSourceProof(old, proof) != nil {
				t.Fatal("original evidence not captured", err)
			}
			if scenario != "warm-policy-error" && scenario != "warm-policy-changed" {
				delete(c.hookWitnesses, old.Instance.ID+"/"+old.NativeID)
			}
			switch scenario {
			case "policy-error", "warm-policy-error":
				policyError = true
			case "policy-changed", "warm-policy-changed":
				policyChanged = true
			case "loaded":
				status = "idle"
			case "missing-list":
				complete = false
			case "changed-config":
				config["config"] = map[string]any{"approval_policy": "never"}
			case "wrong-binding":
				proof.ScopeHash = "foreign"
			case "bad-version":
				proof.Version = 2
			case "bad-policy-hash":
				proof.RequirementsHash = "missing"
			case "bad-time":
				proof.At = "unknown"
			case "candidate-drift":
				projectWrite(t, path, `{"hooks":{"PreToolUse":[]}}`)
			case "no-proof":
				proof = nil
			}
			f.methods = nil
			defer func() {
				for _, method := range f.methods {
					if method == "thread/resume" || method == "thread/unsubscribe" || method == "thread/settings/update" || method == "turn/start" {
						t.Errorf("proof-only path dispatched %s", method)
					}
				}
			}()
			transition, err := c.codexHookSourceAdoptionScope(context.Background(), f, old, target, proof)
			if scenario != "unloaded" {
				if err == nil {
					t.Fatal("unproven cold adoption accepted")
				}
				return
			}
			if err != nil || !transition.hookSourceCold {
				t.Fatal("verified unloaded adoption rejected", err)
			}
			if !reflect.DeepEqual(transition.CodexUserHooks, old.CodexUserHooks) {
				t.Fatal("historical binding replaced")
			}
			copied, err := c.ReadCodexHookSourceProof(context.Background(), old, target, proof)
			if err != nil || !reflect.DeepEqual(copied, proof) {
				t.Fatal("saved original proof rewritten", err)
			}
			for _, method := range f.methods {
				if method != "configRequirements/read" && method != "config/read" && method != "hooks/list" && method != "thread/read" && method != "thread/loaded/list" {
					t.Fatal(fmt.Sprintf("proof check sent mutation %s", method))
				}
			}
		})
	}
}
