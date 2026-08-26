// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestOpcreds_OnFailSafeExpiry_ClearsPendingTrustRoot verifies that
// OnFailSafeExpiry cancels all pending commissioning fields.
//
// Flow: AddTrustedRootCertificate sets pendingTrustRoot → simulate expiry
// by calling OnFailSafeExpiry directly → all pending fields must be nil /
// zero afterwards. Mirrors Matter §11.18.6.4 and the pending-state reset
// that occurs when the FailSafe window expires between
// AddTrustedRootCertificate and AddNOC.
func TestOpcreds_OnFailSafeExpiry_ClearsPendingTrustRoot(t *testing.T) {
	t.Parallel()

	rootRaw, _, _ := buildTestNOCAndRoot(t)

	oc, _ := opcredsWithFakeStore(t)
	// Allow AddTrustedRootCertificate without a real FailSafe gate.
	oc.SetIsFailSafeArmed(func() bool { return true })

	// Install the trusted root.
	_, err := oc.MatterInvoke(
		context.Background(),
		0x0B, // AddTrustedRootCertificate
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh,
	)
	if err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Verify the root is pending (TrustedRootCertificates attribute must
	// include it before AddNOC commits it).
	raw, ok := oc.MatterRead(0x0004) // TrustedRootCertificates
	if !ok {
		t.Fatal("TrustedRootCertificates: ok=false")
	}
	roots := raw.([][]byte)
	if len(roots) == 0 {
		t.Fatal("expected pending root in TrustedRootCertificates before expiry, got empty list")
	}

	// Simulate FailSafe expiry.
	oc.OnFailSafeExpiry(context.Background(), 0)

	// After expiry the pending root must be gone from the in-memory view.
	raw, ok = oc.MatterRead(0x0004)
	if !ok {
		t.Fatal("TrustedRootCertificates after expiry: ok=false")
	}
	roots = raw.([][]byte)
	if len(roots) != 0 {
		t.Fatalf("TrustedRootCertificates after expiry: want empty, got %d entries", len(roots))
	}
}

// TestOpcreds_OnFailSafeExpiry_ClearsAllPendingFields verifies that every
// pending-commissioning field is zeroed after OnFailSafeExpiry, including
// those set by CSRRequest and AddTrustedRootCertificate.
func TestOpcreds_OnFailSafeExpiry_ClearsAllPendingFields(t *testing.T) {
	t.Parallel()

	rootRaw, _, _ := buildTestNOCAndRoot(t)

	oc, _ := opcredsWithFakeStore(t)
	oc.SetIsFailSafeArmed(func() bool { return true })

	ctx := context.Background()

	// CSRRequest populates pendingPrivKey + pendingCSRNonce + pendingCSRSessionID.
	csrNonce := make([]byte, 32)
	for i := range csrNonce {
		csrNonce[i] = byte(i + 1)
	}
	_, err := oc.MatterInvoke(ctx, 0x04, // CSRRequest
		core.CSRRequest{CSRNonce: csrNonce, IsForUpdateNOC: false},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}

	// AddTrustedRootCertificate populates pendingTrustRoot + pendingTrustRootDER.
	_, err = oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Sanity: CSR was issued, so a second AddTrustedRootCertificate should
	// hit the duplicate-root guard (pendingTrustRoot != nil).
	_, err = oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected constraint error on duplicate AddTrustedRootCertificate, got nil")
	}

	// Fire the expiry.
	oc.OnFailSafeExpiry(ctx, 0)

	// After expiry the root is gone from the attribute.
	raw, ok := oc.MatterRead(0x0004)
	if !ok {
		t.Fatal("TrustedRootCertificates: ok=false")
	}
	if roots := raw.([][]byte); len(roots) != 0 {
		t.Fatalf("want 0 roots after expiry, got %d", len(roots))
	}

	// After expiry a fresh AddTrustedRootCertificate must succeed (pending
	// state cleared — no duplicate-root guard hit).
	_, err = oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddTrustedRootCertificate after expiry: %v", err)
	}
}

// TestOpcreds_OnFailSafeExpiry_IdempotentOnCleanState verifies that
// calling OnFailSafeExpiry when no pending state exists is a no-op.
func TestOpcreds_OnFailSafeExpiry_IdempotentOnCleanState(t *testing.T) {
	t.Parallel()
	oc, _ := opcredsWithFakeStore(t)
	// Must not panic or error.
	oc.OnFailSafeExpiry(context.Background(), 0)
	oc.OnFailSafeExpiry(context.Background(), 1)
}

// TestOpcreds_OnFailSafeExpiry_WiredViaGeneralCommissioning verifies the
// end-to-end wiring: GeneralCommissioning fires OnFailSafeExpiry on the
// OperationalCredentials instance when the FailSafe timer expires.
func TestOpcreds_OnFailSafeExpiry_WiredViaGeneralCommissioning(t *testing.T) {
	// Not t.Parallel() — depends on wall-clock timer (1 s window).
	rootRaw, _, _ := buildTestNOCAndRoot(t)

	oc, _ := opcredsWithFakeStore(t)
	oc.SetIsFailSafeArmed(func() bool { return true })

	gc, err := core.NewGeneralCommissioning(core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoorOutdoor,
		FailSafeMaxSeconds: 600,
	})
	if err != nil {
		t.Fatalf("NewGeneralCommissioning: %v", err)
	}

	// Wire expiry → OpCreds cleanup.
	gc.SetOnFailSafeExpired(func(ctx context.Context, fabricIndex uint8) {
		oc.OnFailSafeExpiry(ctx, fabricIndex)
	})

	ctx := context.Background()

	// Arm with a 1-second window.
	_, armErr := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 1},
		hmenum.CommandPriorityHigh)
	if armErr != nil {
		t.Fatalf("ArmFailSafe: %v", armErr)
	}

	// Install a trusted root while the window is armed.
	_, err = oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Verify pending root is present.
	raw, _ := oc.MatterRead(0x0004)
	if roots := raw.([][]byte); len(roots) == 0 {
		t.Fatal("expected pending root before expiry")
	}

	// Wait for the FailSafe timer to fire (≤ 3 s).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		raw, _ = oc.MatterRead(0x0004)
		roots := raw.([][]byte)
		if len(roots) == 0 {
			return // success: expiry hook cleared the pending root
		}
	}
	t.Fatal("pending trust root not cleared after FailSafe expiry (3 s timeout)")
}

// TestOpcreds_OnFailSafeExpiry_AllowsNocCycleAfterExpiry verifies that
// after expiry the commissioning state machine can run a full
// CSRRequest → AddTrustedRootCertificate sequence again without hitting
// stale-state guards.
func TestOpcreds_OnFailSafeExpiry_AllowsNocCycleAfterExpiry(t *testing.T) {
	t.Parallel()

	rootRaw, _, _ := buildTestNOCAndRoot(t)

	oc, _ := opcredsWithFakeStore(t)
	oc.SetIsFailSafeArmed(func() bool { return true })
	ctx := core.WithInvokeSessionID(context.Background(), 0) // PASE / session-0 path

	csrNonce := make([]byte, 32)
	for i := range csrNonce {
		csrNonce[i] = byte(i + 7)
	}

	// First cycle: CSR + root → partial commissioning.
	if _, err := oc.MatterInvoke(ctx, 0x04,
		core.CSRRequest{CSRNonce: csrNonce},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("cycle1 CSRRequest: %v", err)
	}
	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("cycle1 AddTrustedRootCertificate: %v", err)
	}

	// FailSafe expires before AddNOC.
	oc.OnFailSafeExpiry(ctx, 0)

	// Second cycle: must succeed without duplicate-root or missing-CSR errors.
	if _, err := oc.MatterInvoke(ctx, 0x04,
		core.CSRRequest{CSRNonce: csrNonce},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("cycle2 CSRRequest: %v", err)
	}
	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("cycle2 AddTrustedRootCertificate after expiry: %v", err)
	}
}
