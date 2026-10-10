package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyProbeInspectionDoesNotStartExitedServer(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "started")
	binary := filepath.Join(root, "controlled-server")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = '--version' ]; then printf 'codex-cli 0.158.0\\n'; exit 0; fi\nprintf started > '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	scope := Scope{SessionID: "owned-probe", Purpose: "probe", NoAutoResume: true,
		Instance: Instance{Profile: filepath.Join(root, "profile"), Socket: filepath.Join(root, "server.sock"), ServerExecutable: binary, isolatedHome: root}}
	if _, err := startCodexProbeServer(context.Background(), scope); err == nil {
		t.Fatal("exited probe server was considered available")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("read-only preview launched a new native server")
	}
	if _, err := os.Stat(filepath.Join(root, "server.json")); !os.IsNotExist(err) {
		t.Fatal("read-only preview wrote a startup reservation")
	}
}
