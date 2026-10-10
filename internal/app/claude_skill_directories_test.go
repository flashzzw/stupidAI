package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestSessionClaudeSkillDirectoriesUseSavedRoots(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	f := &catalogFixture{fixtureRuntime: newFixture()}
	f.read = func(scope runtime.Scope) (runtime.SkillSourceCatalog, error) {
		return runtime.SkillSourceCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "claude.sdk.current_query", Items: []runtime.SkillSourceItem{}, Limitations: []string{"native query does not report paths"}}, nil
	}
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before := digest(a.Snapshot())
	out, err := a.ReadSessionSkillCatalog(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(out)
	var response struct {
		Complete    bool `json:"complete"`
		Directories struct {
			Source  string                                     `json:"source"`
			Entries []struct{ Path, NativeKey, Source string } `json:"entries"`
		} `json:"directories"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(c.Instances[0].Profile, "skills", "native-key", "SKILL.md")
	found := false
	for _, entry := range response.Directories.Entries {
		found = found || entry.Path == want
	}
	if !found || response.Directories.Source != "claude.bound_directories" || response.Complete {
		t.Fatal("catalog lacks separate bound-directory discovery", string(body))
	}
	if digest(a.Snapshot()) != before || f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("discovery changed execution")
	}
}

func TestClaudeSkillDirectoryBoundaries(t *testing.T) {
	for _, scenario := range []string{"metadata-only", "same-key", "file-alias", "broken-link", "invalid-metadata", "oversize", "entry-limit", "cancelled", "saved-version"} {
		t.Run(scenario, func(t *testing.T) {
			c, source := claudeSkillFixture(t)
			session := Session{Scope: runtime.Scope{Instance: c.Instances[0], Cwd: c.Projects[0].Cwd}}
			write := func(p, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			user := filepath.Join(c.Instances[0].Profile, "skills")
			switch scenario {
			case "same-key":
				write(filepath.Join(session.Scope.Cwd, ".claude/skills/native-key/SKILL.md"), "---\nname: another-method\n---\n")
			case "file-alias":
				if err := os.Symlink(source, filepath.Join(user, "alias")); err != nil {
					t.Fatal(err)
				}
			case "broken-link":
				if err := os.Symlink(filepath.Join(user, "missing"), filepath.Join(user, "broken")); err != nil {
					t.Fatal(err)
				}
			case "invalid-metadata":
				write(filepath.Join(user, "broken/SKILL.md"), "---\nname: [PRIVATE_MALFORMED\n---\nPRIVATE_BODY")
			case "oversize":
				write(filepath.Join(user, "oversize/SKILL.md"), strings.Repeat("x", (256<<10)+1))
			case "entry-limit":
				for i := 0; i < 2050; i++ {
					if err := os.WriteFile(filepath.Join(user, fmt.Sprintf("entry-%04d", i)), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled":
				cancel()
			case "saved-version":
				session.ExecutionSnapshot = &ExecutionSnapshot{ID: "original", SkillSelections: []runtime.SkillSelection{{AssetID: "shared", Name: "saved-name", Source: "userSettings", DiscoveryPath: filepath.Join(user, "no-longer-present/SKILL.md"), Mode: "disabled"}}, CapabilityRefs: []CapabilityReference{{Asset: CapabilityAsset{ID: "shared", Version: "v1"}}}}
			}
			out := discoverClaudeSkillDirectories(ctx, session)
			incomplete := scenario == "broken-link" || scenario == "invalid-metadata" || scenario == "oversize" || scenario == "entry-limit" || scenario == "cancelled"
			if out.Complete == incomplete {
				t.Fatalf("wrong coverage: %+v", out)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "PRIVATE_") {
				t.Fatal("directory discovery leaked body or parse input")
			}
			if incomplete && len(out.Errors) == 0 {
				t.Fatal("incomplete scan has no reason")
			}
			if scenario == "same-key" || scenario == "file-alias" {
				for _, entry := range out.Entries {
					if entry.Control != "ambiguous" {
						t.Fatal("ambiguous source suggested independent control", entry)
					}
				}
			}
			if scenario == "metadata-only" {
				if len(out.Entries) != 1 || out.Entries[0].NativeKey != "native-key" || out.Entries[0].Name != "displayed-method" || out.Entries[0].ResolvedPath != filepath.Join(source, "SKILL.md") {
					t.Fatal("directory key, declaration or resolved path confused", out)
				}
			}
			if scenario == "saved-version" {
				if len(out.SavedSelections) != 1 || out.SavedSelections[0].Version != "v1" || out.SavedSelections[0].Discovered || out.SavedSelections[0].Name != "saved-name" || out.SnapshotID != "original" {
					t.Fatal("saved version or missing entry was rewritten", out)
				}
			}
		})
	}
}
