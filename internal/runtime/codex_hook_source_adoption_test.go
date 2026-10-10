package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexHookSourceAdoptionNeedsOriginalConnectionEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "missing-witness", "foreign-connection", "different-config", "different-identity", "legacy", "candidate-drift"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, old, config, _ := projectHookHarness(t)
			config["config"] = map[string]any{}
			originalCall := f.call
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "config/read" && params["includeLayers"] == false {
					return config, nil
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
			before, err := c.prepareSelectedHooks(context.Background(), f, old)
			if err != nil || before == "" {
				t.Fatal("file binding lacks establishment evidence", err)
			}
			if err := c.rememberSelectedHooks(context.Background(), f, old, old.NativeID, before); err != nil {
				t.Fatal(err)
			}
			projectWrite(t, path, `{"hooks":{"Stop":[]}}`)
			if err := c.verifyHookConfiguration(context.Background(), f, old); err == nil {
				t.Fatal("changed file considered current")
			}
			target := old
			target.CodexUserHooks, err = ReadCodexUserHooks(target)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-witness":
				delete(c.hookWitnesses, old.Instance.ID+"/"+old.NativeID)
			case "foreign-connection":
				w := c.hookWitnesses[old.Instance.ID+"/"+old.NativeID]
				w.ConnectionID = "foreign"
				c.hookWitnesses[old.Instance.ID+"/"+old.NativeID] = w
			case "different-config":
				config["config"] = map[string]any{"approval_policy": "never"}
			case "different-identity":
				target.NativeID = "another"
			case "legacy":
				old.CodexUserHooks = nil
			case "candidate-drift":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			transition, err := c.codexHookSourceAdoptionScope(context.Background(), f, old, target)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("unproven adoption accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if transition.CodexUserHooks != old.CodexUserHooks {
				t.Fatal("transition rewrote original snapshot")
			}
			if err := VerifyCodexUserHooks(transition); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCodexUserHooks(old); err == nil {
				t.Fatal("ordinary scope inherited transition permission")
			}
		})
	}
}
