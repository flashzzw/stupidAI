package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitHomeAndStrictConfig(t *testing.T) {
	t.Setenv("CODEX_HOME", "/wrong")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.CodexHome != "/home/zzw/.codex" {
		t.Fatal("inherited wrong CODEX_HOME")
	}
	n := 0
	for _, v := range c.Env() {
		if strings.HasPrefix(v, "CODEX_HOME=") {
			n++
			if v != "CODEX_HOME=/home/zzw/.codex" {
				t.Fatal(v)
			}
		}
	}
	if n != 1 {
		t.Fatal("duplicate home")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	for _, body := range []string{`{"typo":true}`, `{"poll_seconds":0}`, `{} {}`, `{"codex_home":""}`} {
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(path); err == nil {
			t.Fatalf("accepted invalid config %s", body)
		}
	}
}
