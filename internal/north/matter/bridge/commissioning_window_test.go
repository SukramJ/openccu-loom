// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// ─── fakeFailSafeArmer ───────────────────────────────────────────────────────

type fakeFailSafeArmer struct {
	calls   atomic.Int32
	seconds []uint32
	fabric  []uint8
	err     error
}

func (f *fakeFailSafeArmer) ArmFailSafeFor(_ context.Context, seconds uint32, fabricIndex uint8) error {
	f.calls.Add(1)
	f.seconds = append(f.seconds, seconds)
	f.fabric = append(f.fabric, fabricIndex)
	return f.err
}

// ─── fakePaseSessionCloser ───────────────────────────────────────────────────

type fakePaseSessionCloser struct {
	calls atomic.Int32
	err   error
}

func (f *fakePaseSessionCloser) ClosePaseSessions(_ context.Context) error {
	f.calls.Add(1)
	return f.err
}

// ─── CommissioningWindow unit tests ──────────────────────────────────────────

func TestCommissioningWindow_NewlyCreated_IsClosed(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusClosed {
		t.Errorf("Status = %v, want Closed", snap.Status)
	}
	if !snap.AdminFabricIsNull {
		t.Error("AdminFabricIsNull = false, want true for a fresh window")
	}
	if !snap.AdminVendorIsNull {
		t.Error("AdminVendorIsNull = false, want true for a fresh window")
	}
}

func TestCommissioningWindow_OpenWindow_Valid_StatusBecomesEnhanced(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	})
	if err != nil {
		t.Fatalf("OpenWindow(600s): unexpected error: %v", err)
	}
	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusEnhanced {
		t.Errorf("Status = %v, want Enhanced after OpenWindow", snap.Status)
	}
}

func TestCommissioningWindow_OpenWindow_Twice_ReturnsBusy(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("first OpenWindow: unexpected error: %v", err)
	}
	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	})
	if !errors.Is(err, wire.ErrAdmCommBusy) {
		t.Errorf("second OpenWindow: want ErrAdmCommBusy, got %v", err)
	}
}

func TestCommissioningWindow_OpenWindow_DurationTooShort_ReturnsInvalid(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 179, // < 180
	})
	if !errors.Is(err, bridge.ErrCommissioningWindowDurationInvalid) {
		t.Errorf("OpenWindow(179s): want ErrCommissioningWindowDurationInvalid, got %v", err)
	}
}

func TestCommissioningWindow_OpenWindow_DurationTooLong_ReturnsInvalid(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 901, // > 900
	})
	if !errors.Is(err, bridge.ErrCommissioningWindowDurationInvalid) {
		t.Errorf("OpenWindow(901s): want ErrCommissioningWindowDurationInvalid, got %v", err)
	}
}

// TestParityMatterJS_CommissioningWindow_DurationInvalidReturnsInvalidCommand
// verifies that ErrCommissioningWindowDurationInvalid implements
// im.StatusCodeError returning InvalidCommand (0x85). Mirrors matter.js
// AdministratorCommissioningServer.ts:#assertCommissioningWindowRequirements
// (lines 199-209) which throws Status.InvalidCommand for out-of-range timeouts.
func TestParityMatterJS_CommissioningWindow_DurationInvalidReturnsInvalidCommand(t *testing.T) {
	t.Parallel()

	var sc im.StatusCodeError
	if !errors.As(bridge.ErrCommissioningWindowDurationInvalid, &sc) {
		t.Fatal("ErrCommissioningWindowDurationInvalid does not implement im.StatusCodeError")
	}
	if sc.MatterStatusCode() != im.StatusInvalidCommand {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusInvalidCommand (0x85)", sc.MatterStatusCode())
	}
}

func TestCommissioningWindow_DurationBoundaries_Valid(t *testing.T) {
	t.Parallel()
	for _, dur := range []uint16{180, 900} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			w := bridge.NewCommissioningWindow()
			err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
				CommissioningTimeoutSeconds: dur,
			})
			if err != nil {
				t.Errorf("OpenWindow(%ds): unexpected error: %v", dur, err)
			}
		})
	}
}

func TestCommissioningWindow_RevokeWindow_OnClosedWindow_IsIdempotent(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	// Window is closed; revoke should be a no-op.
	err := w.RevokeWindow(context.Background())
	if err != nil {
		t.Errorf("RevokeWindow on closed window: unexpected error: %v", err)
	}
	// Still closed.
	if snap := w.CurrentWindow(); snap.Status != wire.WindowStatusClosed {
		t.Errorf("Status after RevokeWindow on closed = %v, want Closed", snap.Status)
	}
}

func TestCommissioningWindow_RevokeWindow_OnOpenWindow_StatusReturnsToClosed(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}
	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusClosed {
		t.Errorf("Status after RevokeWindow = %v, want Closed", snap.Status)
	}
}

func TestCommissioningWindow_TransitionHook_CalledOnOpenAndRevoke(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	var count atomic.Int32
	w.SetTransitionHook(func() { count.Add(1) })

	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}

	if n := count.Load(); n != 2 {
		t.Errorf("transition hook call count = %d, want 2 (one Open + one Revoke)", n)
	}
}

// ─── CommissioningWindowOpener unit tests ────────────────────────────────────

func TestCommissioningWindowOpener_NoPasscode_ReturnsNotConfigured(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xF00, 0 /* passcode = 0 */, 0x1234, 0x5678)
	_, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if !errors.Is(err, bridge.ErrCommissioningWindowNotConfigured) {
		t.Errorf("want ErrCommissioningWindowNotConfigured, got %v", err)
	}
}

func TestCommissioningWindowOpener_ValidPasscode_ReturnsArtifacts(t *testing.T) {
	t.Parallel()
	const (
		discriminator uint16 = 0xF00
		passcode      uint32 = 20202021
		vendorID      uint16 = 0x1234
		productID     uint16 = 0x5678
		durationSec   uint16 = 600
	)
	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, discriminator, passcode, vendorID, productID)
	result, err := opener.OpenCommissioningWindow(context.Background(), durationSec)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow: unexpected error: %v", err)
	}

	// QR code must start with "MT:".
	if !strings.HasPrefix(result.QRCode, "MT:") {
		t.Errorf("QRCode = %q; want prefix \"MT:\"", result.QRCode)
	}

	// Manual code must be 11 characters (10 digits + Verhoeff check digit).
	if len(result.ManualCode) != 11 {
		t.Errorf("ManualCode = %q (len=%d), want 11 characters", result.ManualCode, len(result.ManualCode))
	}

	// Duration must be echoed back.
	if result.DurationSeconds != durationSec {
		t.Errorf("DurationSeconds = %d, want %d", result.DurationSeconds, durationSec)
	}
}

func TestCommissioningWindowOpener_SecondOpen_ReturnsAlreadyOpen(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xF00, 20202021, 0x1234, 0x5678)

	if _, err := opener.OpenCommissioningWindow(context.Background(), 600); err != nil {
		t.Fatalf("first OpenCommissioningWindow: unexpected error: %v", err)
	}
	_, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if !errors.Is(err, bridge.ErrCommissioningWindowAlreadyOpen) {
		t.Errorf("second OpenCommissioningWindow: want ErrCommissioningWindowAlreadyOpen, got %v", err)
	}
}

// ─── FailSafeArmer ───────────────────────────────────────────────────────────

// TestCommissioningWindow_FailSafeArmer_CalledOnOpen verifies that the
// FailSafeArmer is invoked once after a successful window open, with the
// window duration and fabricIndex=0 per Matter §11.19.6.
func TestCommissioningWindow_FailSafeArmer_CalledOnOpen(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	armer := &fakeFailSafeArmer{}
	w.SetFailSafeArmer(armer)

	const duration uint16 = 600
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: duration,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}

	if n := armer.calls.Load(); n != 1 {
		t.Errorf("ArmFailSafeFor call count = %d, want 1", n)
	}
	if len(armer.seconds) < 1 || armer.seconds[0] != uint32(duration) {
		t.Errorf("ArmFailSafeFor seconds = %v, want [%d]", armer.seconds, duration)
	}
	if len(armer.fabric) < 1 || armer.fabric[0] != 0 {
		t.Errorf("ArmFailSafeFor fabricIndex = %v, want [0] (pre-commissioning)", armer.fabric)
	}
}

// TestCommissioningWindow_FailSafeArmer_NotCalledWhenWindowRejected verifies
// that the FailSafeArmer is NOT invoked when OpenWindow rejects the call
// (e.g. duplicate open).
func TestCommissioningWindow_FailSafeArmer_NotCalledWhenWindowRejected(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	armer := &fakeFailSafeArmer{}
	w.SetFailSafeArmer(armer)

	// Open the window successfully.
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("first OpenWindow: unexpected error: %v", err)
	}
	firstCalls := armer.calls.Load()

	// Second open must be rejected (BUSY). FailSafeArmer must not be called again.
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); !errors.Is(err, wire.ErrAdmCommBusy) {
		t.Errorf("second OpenWindow: want ErrAdmCommBusy, got %v", err)
	}
	if n := armer.calls.Load(); n != firstCalls {
		t.Errorf("ArmFailSafeFor call count after rejected open = %d, want %d (no extra arm)", n, firstCalls)
	}
}

// TestCommissioningWindow_FailSafeArmer_ArmErrorDoesNotAbortWindow verifies
// that a FailSafeArmer error does not abort the window open — the window is
// already open when the arm fires, and aborting would leave the cluster in
// an inconsistent state with the wire-level Success that was already returned.
func TestCommissioningWindow_FailSafeArmer_ArmErrorDoesNotAbortWindow(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	armer := &fakeFailSafeArmer{err: errors.New("fail-safe arm failed")}
	w.SetFailSafeArmer(armer)

	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	})
	if err != nil {
		t.Errorf("OpenWindow: unexpected error %v — arm failure must not abort window", err)
	}
	if snap := w.CurrentWindow(); snap.Status != wire.WindowStatusEnhanced {
		t.Errorf("Status after arm error = %v, want Enhanced (window is open)", snap.Status)
	}
}

// TestCommissioningWindow_RevokeWindow_DisarmsFailSafe verifies the H9 fix:
// RevokeWindow disarms the fail-safe (Matter §11.19.7.3 step 1) via a
// FailSafeArmer.ArmFailSafeFor(ctx, 0, 0) call, unconditionally and before
// the window-state check. Without this, the fail-safe armed by OpenWindow
// (or by the commissioner's own ArmFailSafe) stays armed for the full
// window duration and the next OpenCommissioningWindow is rejected Busy.
func TestCommissioningWindow_RevokeWindow_DisarmsFailSafe(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	armer := &fakeFailSafeArmer{}
	w.SetFailSafeArmer(armer)

	const duration uint16 = 600
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: duration,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}

	// fakeFailSafeArmer records every call (not just the last), so the
	// slice must hold both the window-open arm and the revoke disarm.
	if n := armer.calls.Load(); n != 2 {
		t.Fatalf("ArmFailSafeFor call count = %d, want 2 (one arm on Open + one disarm on Revoke)", n)
	}
	if armer.seconds[0] != uint32(duration) || armer.fabric[0] != 0 {
		t.Errorf("first ArmFailSafeFor (Open) = (seconds=%d, fabric=%d), want (%d, 0)",
			armer.seconds[0], armer.fabric[0], duration)
	}
	// The RevokeWindow disarm must be the last recorded call, with
	// seconds=0 (disarm) and fabricIndex=0.
	last := len(armer.seconds) - 1
	if armer.seconds[last] != 0 {
		t.Errorf("RevokeWindow disarm seconds = %d, want 0", armer.seconds[last])
	}
	if armer.fabric[last] != 0 {
		t.Errorf("RevokeWindow disarm fabricIndex = %d, want 0", armer.fabric[last])
	}
}

// fakeFailSafeArmerChecker implements both FailSafeArmer and
// FailSafeChecker over shared state, so a test can observe the
// "armed → Busy" linkage that OpenWindow's FailSafeChecker guard
// enforces (see TestCommissioningWindow_OpenWindow_RejectsBusyWhenFailSafeArmed)
// without wiring a real GeneralCommissioning cluster server.
type fakeFailSafeArmerChecker struct {
	armed atomic.Bool
}

func (f *fakeFailSafeArmerChecker) ArmFailSafeFor(_ context.Context, seconds uint32, _ uint8) error {
	f.armed.Store(seconds != 0)
	return nil
}

func (f *fakeFailSafeArmerChecker) FailSafeArmed() bool {
	return f.armed.Load()
}

// TestCommissioningWindow_OpenAfterRevoke_NotBusy verifies the end-to-end
// H9 regression: OpenWindow arms the fail-safe, RevokeWindow disarms it,
// and a subsequent OpenWindow must succeed rather than being rejected
// ErrAdmCommBusy by the FailSafeChecker guard. Before the H9 fix,
// RevokeWindow left the fail-safe armed for the remainder of the window
// duration, so this second OpenWindow would have hit Busy.
func TestCommissioningWindow_OpenAfterRevoke_NotBusy(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	fake := &fakeFailSafeArmerChecker{}
	w.SetFailSafeArmer(fake)
	w.SetFailSafeChecker(fake)

	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("first OpenWindow: unexpected error: %v", err)
	}
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}

	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	})
	if err != nil {
		t.Errorf("OpenWindow after RevokeWindow: unexpected error %v, want nil (fail-safe must be disarmed)", err)
	}
}

// ─── PaseSessionCloser ───────────────────────────────────────────────────────

// TestCommissioningWindow_PaseSessionCloser_CalledOnRevoke verifies that the
// PaseSessionCloser is invoked on RevokeWindow regardless of whether the
// window is open — per Matter §11.19.7.3 step 1.
func TestCommissioningWindow_PaseSessionCloser_CalledOnRevoke(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	closer := &fakePaseSessionCloser{}
	w.SetPaseSessionCloser(closer)

	// Open the window then revoke.
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}

	if n := closer.calls.Load(); n != 1 {
		t.Errorf("ClosePaseSessions call count = %d, want 1", n)
	}
}

// TestCommissioningWindow_PaseSessionCloser_CalledOnRevokeEvenWhenClosed
// verifies that ClosePaseSessions is called even when no commissioning window
// is open — §11.19.7.3 step 1 is unconditional.
func TestCommissioningWindow_PaseSessionCloser_CalledOnRevokeEvenWhenClosed(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	closer := &fakePaseSessionCloser{}
	w.SetPaseSessionCloser(closer)

	// Revoke on a closed window — closer must still fire.
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow on closed window: unexpected error: %v", err)
	}
	if n := closer.calls.Load(); n != 1 {
		t.Errorf("ClosePaseSessions call count on closed-window revoke = %d, want 1", n)
	}
}

// TestCommissioningWindow_PaseSessionCloser_ErrorDoesNotAbortRevoke verifies
// that a ClosePaseSessions error does not prevent the window from being closed.
func TestCommissioningWindow_PaseSessionCloser_ErrorDoesNotAbortRevoke(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	closer := &fakePaseSessionCloser{err: errors.New("session close failed")}
	w.SetPaseSessionCloser(closer)

	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Errorf("RevokeWindow: unexpected error %v — session-close failure must not abort revoke", err)
	}
	if snap := w.CurrentWindow(); snap.Status != wire.WindowStatusClosed {
		t.Errorf("Status after revoke with session-close error = %v, want Closed", snap.Status)
	}
}

// ─── windowMode (BasicWindowOpen vs EnhancedWindowOpen) ─────────────────────

// TestCommissioningWindow_IsBasicWindow_StatusBasic verifies that a
// window opened with IsBasicWindow=true returns WindowStatusBasic (2).
// Mirrors matter.js AdministratorCommissioningServer.ts:114-119.
func TestCommissioningWindow_IsBasicWindow_StatusBasic(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		IsBasicWindow:               true,
	}); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusBasic {
		t.Errorf("Status = %v, want WindowStatusBasic (2)", snap.Status)
	}
}

// TestCommissioningWindow_IsBasicWindowFalse_StatusEnhanced verifies that
// a window opened with IsBasicWindow=false returns WindowStatusEnhanced (1).
// Mirrors matter.js AdministratorCommissioningServer.ts:114-119.
func TestCommissioningWindow_IsBasicWindowFalse_StatusEnhanced(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		IsBasicWindow:               false,
	}); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusEnhanced {
		t.Errorf("Status = %v, want WindowStatusEnhanced (1)", snap.Status)
	}
}

// TestCommissioningWindow_IsBasicWindow_ClearedOnRevoke ensures the
// mode is reset after RevokeWindow so a subsequent open is not
// accidentally marked basic.
func TestCommissioningWindow_IsBasicWindow_ClearedOnRevoke(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	_ = w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		IsBasicWindow:               true,
	})
	_ = w.RevokeWindow(context.Background())
	// Second open without IsBasicWindow set → must be Enhanced.
	_ = w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
	})
	if snap := w.CurrentWindow(); snap.Status != wire.WindowStatusEnhanced {
		t.Errorf("Status after revoke+reopen = %v, want Enhanced", snap.Status)
	}
}

// ─── AdminFabricIndex / AdminVendorID from params ────────────────────────────

// TestCommissioningWindow_AdminFabric_StoredFromParams verifies that
// non-zero AdminFabricIndex / AdminVendorID from OpenWindowParams are
// reflected in CurrentWindow's snapshot.
// Mirrors matter.js AdministratorCommissioningServer.ts:176-180.
func TestCommissioningWindow_AdminFabric_StoredFromParams(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		AdminFabricIndex:            3,
		AdminVendorID:               0x1234,
	}); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	snap := w.CurrentWindow()
	if snap.AdminFabricIsNull {
		t.Error("AdminFabricIsNull = true, want false when AdminFabricIndex was provided")
	}
	if snap.AdminFabricIndex != 3 {
		t.Errorf("AdminFabricIndex = %d, want 3", snap.AdminFabricIndex)
	}
	if snap.AdminVendorIsNull {
		t.Error("AdminVendorIsNull = true, want false when AdminVendorID was provided")
	}
	if snap.AdminVendorID != 0x1234 {
		t.Errorf("AdminVendorID = 0x%X, want 0x1234", snap.AdminVendorID)
	}
}

// TestCommissioningWindow_AdminFabric_ZeroIsNull verifies that
// AdminFabricIndex=0 / AdminVendorID=0 keeps the null flag set — this
// matches the pre-fabric commissioning scenario.
func TestCommissioningWindow_AdminFabric_ZeroIsNull(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		AdminFabricIndex:            0,
		AdminVendorID:               0,
	}); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	snap := w.CurrentWindow()
	if !snap.AdminFabricIsNull {
		t.Errorf("AdminFabricIsNull = false, want true when AdminFabricIndex=0")
	}
	if !snap.AdminVendorIsNull {
		t.Errorf("AdminVendorIsNull = false, want true when AdminVendorID=0")
	}
}

// TestCommissioningWindow_AdminFabric_ClearedOnRevoke verifies that
// admin fabric metadata is cleared after RevokeWindow.
func TestCommissioningWindow_AdminFabric_ClearedOnRevoke(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	_ = w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		AdminFabricIndex:            7,
		AdminVendorID:               0xABCD,
	})
	_ = w.RevokeWindow(context.Background())
	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusClosed {
		t.Errorf("Status = %v after revoke, want Closed", snap.Status)
	}
}

// ─── RandomDiscriminator / RandomPasscode / RandomSalt ───────────────────────

// TestRandomDiscriminator_InRange verifies the result is a valid 12-bit value.
func TestRandomDiscriminator_InRange(t *testing.T) {
	t.Parallel()
	for range 20 {
		v, err := bridge.RandomDiscriminator()
		if err != nil {
			t.Fatalf("RandomDiscriminator: %v", err)
		}
		if v > 0x0FFF {
			t.Errorf("discriminator 0x%04X out of 12-bit range", v)
		}
	}
}

// TestRandomPasscode_NotInvalidSet verifies that no invalid passcode
// (spec §5.1.1.1.1) is returned across a sufficient number of samples.
func TestRandomPasscode_NotInvalidSet(t *testing.T) {
	t.Parallel()
	invalid := map[uint32]bool{
		0: true, 11111111: true, 22222222: true, 33333333: true,
		44444444: true, 55555555: true, 66666666: true, 77777777: true,
		88888888: true, 99999999: true, 12345678: true, 87654321: true,
	}
	for range 50 {
		v, err := bridge.RandomPasscode()
		if err != nil {
			t.Fatalf("RandomPasscode: %v", err)
		}
		if invalid[v] {
			t.Errorf("RandomPasscode returned invalid passcode %08d", v)
		}
		if v >= 99999999 {
			t.Errorf("RandomPasscode %d >= 99999999 (out of 8-digit range)", v)
		}
	}
}

// ─── FailSafeChecker ─────────────────────────────────────────────────────────

type fakeFailSafeChecker struct {
	armed bool
}

func (f *fakeFailSafeChecker) FailSafeArmed() bool { return f.armed }

// TestCommissioningWindow_OpenWindow_RejectsBusyWhenFailSafeArmed verifies
// that OpenWindow returns BUSY when the FailSafeChecker reports an armed
// fail-safe, preventing a second commissioner from overwriting the active
// commissioning session.
func TestCommissioningWindow_OpenWindow_RejectsBusyWhenFailSafeArmed(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	checker := &fakeFailSafeChecker{armed: true}
	w.SetFailSafeChecker(checker)

	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	})
	if !errors.Is(err, wire.ErrAdmCommBusy) {
		t.Errorf("OpenWindow with armed FailSafe: got %v, want ErrAdmCommBusy", err)
	}
}

// TestCommissioningWindow_OpenWindow_AllowsWhenFailSafeDisarmed verifies
// that OpenWindow proceeds normally when the FailSafeChecker reports the
// fail-safe as disarmed.
func TestCommissioningWindow_OpenWindow_AllowsWhenFailSafeDisarmed(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	checker := &fakeFailSafeChecker{armed: false}
	w.SetFailSafeChecker(checker)

	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
	})
	if err != nil {
		t.Errorf("OpenWindow with disarmed FailSafe: unexpected error: %v", err)
	}
}

// TestRandomSalt_Length16 verifies the returned salt is exactly 16 bytes.
func TestRandomSalt_Length16(t *testing.T) {
	t.Parallel()
	s, err := bridge.RandomSalt()
	if err != nil {
		t.Fatalf("RandomSalt: %v", err)
	}
	if len(s) != 16 {
		t.Errorf("RandomSalt: len = %d, want 16", len(s))
	}
}
