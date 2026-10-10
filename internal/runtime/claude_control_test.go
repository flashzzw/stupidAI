package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeFreshObservationDoesNotReuseDisconnectedIdleCache(t *testing.T) {
	root := t.TempDir()
	c := NewClaude(root)
	t.Cleanup(func() { _ = c.Close() })
	scope := Scope{Instance: Instance{ID: "fixture", Kind: "claude", Profile: root,
		NodeExecutable: filepath.Join(root, "missing-node")}, NativeID: "original"}
	c.mu.Lock()
	c.cache["fixture/original"] = claudeCachedView{instance: "fixture", at: time.Now(),
		view: View{NativeID: "original", Status: "idle", ExecutionClear: true, Evidence: Evidence{Complete: true}}}
	c.mu.Unlock()
	// An idle/reaped instance can still use its saved view for display.
	if view, err := c.Observe(context.Background(), scope); err != nil || view.Status != "idle" {
		t.Fatalf("display observation did not reuse the valid cache: %+v, %v", view, err)
	}
	// Control handoff has to contact the bridge even within the cache lifetime.
	if view, err := c.ObserveFresh(context.Background(), scope); err == nil || view.ExecutionClear {
		t.Fatalf("disconnected runtime granted control from an old cache: %+v, %v", view, err)
	}
}
