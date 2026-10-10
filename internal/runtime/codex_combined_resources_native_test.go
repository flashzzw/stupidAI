//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"io"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestCodexNativeCombinedResourcesAndReceiptLoss(t *testing.T) {
	codexNativeHookSelectionFlow(t, true)
}

func TestCodexNativeOriginalConfigurationRecovery(t *testing.T) {
	codexNativeHookSelectionFlow(t, true, "normal")
}

func TestCodexNativeOriginalConfigurationRecoveryApplicationLoss(t *testing.T) {
	codexNativeHookSelectionFlow(t, true, "application-loss")
}
func TestCodexNativeOriginalConfigurationRecoveryNativeLoss(t *testing.T) {
	codexNativeHookSelectionFlow(t, true, "native-loss")
}

func TestCodexNativeOriginalConfigurationRecoveryAcknowledgedRestart(t *testing.T) {
	codexNativeHookSelectionFlow(t, true, "acknowledged-restart")
}

type nativeConfigurationApplicationLoss struct {
	recoveryArmed atomic.Bool
	holdRecovery  atomic.Bool
	*runtime.Codex
	armed    atomic.Bool
	received chan nativeApplicationConfigurationReply
}

func (r *nativeConfigurationApplicationLoss) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if in.Kind == "recover_execution" && r.holdRecovery.Load() {
		return runtime.Resolution{State: "unknown", Receipt: receipt, Evidence: runtime.Evidence{Source: "fixture.hold_recovery", Detail: "held after durable acknowledgement"}}, nil
	}
	return r.Codex.Reconcile(ctx, in, receipt)
}

type nativeApplicationConfigurationReply struct {
	Intent  runtime.Intent  `json:"intent"`
	Receipt runtime.Receipt `json:"receipt"`
}

func (r *nativeConfigurationApplicationLoss) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	receipt, err := r.Codex.Execute(ctx, in)
	if err == nil && (in.Kind == "apply_execution" && r.armed.Swap(false) || in.Kind == "recover_execution" && r.recoveryArmed.Swap(false)) {
		r.received <- nativeApplicationConfigurationReply{Intent: in, Receipt: receipt}
		return runtime.Receipt{}, io.ErrUnexpectedEOF
	}
	return receipt, err
}

func nativeConfigurationCallCount(t *testing.T, f *runtime.NativeApplicationFixture, nativeID string) int {
	t.Helper()
	n, err := f.CountConfigurationResumes(nativeID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
