// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// These tests lock in the fail-safe ownership + CASE-while-window-open
// guard added to ArmFailSafe. Mirrors matter.js
// GeneralCommissioningServer.ts:82-90 (CASE-steal rejection) and
// FailsafeTimer.reArm (packages/protocol/src/common/FailsafeTimer.ts:53-57)
// (fabric-mismatch re-arm/disarm rejection).

// TestGencomm_ArmFailSafe_CASEArmWhileWindowOpen_Busy verifies rule (a):
// a CASE session (fabricIndex != 0) may not arm an unarmed fail-safe
// while a commissioning window is open — the window is reserved for the
// PASE commissioner that opened it.
func TestGencomm_ArmFailSafe_CASEArmWhileWindowOpen_Busy(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	gc.SetIsCommissioningWindowOpen(func() bool { return true })

	ctx := im.WithFabricFilter(context.Background(), false, 2)
	resp, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (CASE, window open): unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorBusyWithOtherAdmin {
		t.Fatalf("ErrorCode = %d, want BusyWithOtherAdmin", r.ErrorCode)
	}
	if gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = true after rejected CASE arm, want false")
	}
}

// TestGencomm_ArmFailSafe_PASEArmWhileWindowOpen_OK is the control for
// rule (a): a PASE session (fabricIndex == 0) is exempt from the
// CASE-steal guard even while a commissioning window is open.
func TestGencomm_ArmFailSafe_PASEArmWhileWindowOpen_OK(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	gc.SetIsCommissioningWindowOpen(func() bool { return true })

	ctx := im.WithFabricFilter(context.Background(), false, 0)
	resp, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (PASE, window open): unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after accepted PASE arm, want true")
	}
}

// TestGencomm_ArmFailSafe_CASEArmWindowClosed_OK is the control for rule
// (a): with no commissioning window open, a CASE arm of an unarmed
// fail-safe proceeds normally — there is no reservation to protect.
func TestGencomm_ArmFailSafe_CASEArmWindowClosed_OK(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	gc.SetIsCommissioningWindowOpen(func() bool { return false })

	ctx := im.WithFabricFilter(context.Background(), false, 2)
	resp, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (CASE, window closed): unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after accepted CASE arm, want true")
	}
}

// TestGencomm_ArmFailSafe_ReArmDifferentFabric_Busy verifies rule (b):
// once armed by fabric F, a re-arm (ExpiryLengthSeconds != 0) requested
// by a different fabric is rejected — regardless of window state.
func TestGencomm_ArmFailSafe_ReArmDifferentFabric_Busy(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)

	owner := im.WithFabricFilter(context.Background(), false, 1)
	resp, err := gc.MatterInvoke(owner, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (owner arm): unexpected error: %v", err)
	}
	if resp.(core.ArmFailSafeResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("owner arm ErrorCode = %d, want OK", resp.(core.ArmFailSafeResponse).ErrorCode)
	}

	intruder := im.WithFabricFilter(context.Background(), false, 2)
	resp, err = gc.MatterInvoke(intruder, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (intruder re-arm): unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorBusyWithOtherAdmin {
		t.Fatalf("ErrorCode = %d, want BusyWithOtherAdmin", r.ErrorCode)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after rejected re-arm, want true (still owned by fabric 1)")
	}
}

// TestGencomm_ArmFailSafe_ReArmSameFabric_OK is the control for rule
// (b): the owning fabric may freely re-arm its own fail-safe window.
func TestGencomm_ArmFailSafe_ReArmSameFabric_OK(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)

	owner := im.WithFabricFilter(context.Background(), false, 1)
	resp, err := gc.MatterInvoke(owner, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (first arm): unexpected error: %v", err)
	}
	if resp.(core.ArmFailSafeResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("first arm ErrorCode = %d, want OK", resp.(core.ArmFailSafeResponse).ErrorCode)
	}

	resp, err = gc.MatterInvoke(owner, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 120, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (same-fabric re-arm): unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after same-fabric re-arm, want true")
	}
}

// TestGencomm_ArmFailSafe_DisarmDifferentFabric_Busy_And_StaysArmed
// verifies rule (c): once armed by fabric F, a disarm
// (ExpiryLengthSeconds == 0) requested by a different fabric is rejected
// with BusyWithOtherAdmin AND the fail-safe stays armed — its
// expiry/revert hook must not fire, since that hook rolls back
// in-flight commissioning state (pending NOC install) that the
// requesting fabric has no authority over. The owning fabric can still
// disarm afterwards, which both succeeds and fires the hook.
func TestGencomm_ArmFailSafe_DisarmDifferentFabric_Busy_And_StaysArmed(t *testing.T) {
	t.Parallel()
	const ownerFabric = uint8(1)
	const intruderFabric = uint8(2)
	fired := make(chan uint8, 2)

	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
		OnFailSafeExpired: func(_ context.Context, idx uint8) {
			fired <- idx
		},
	})

	owner := im.WithFabricFilter(context.Background(), false, ownerFabric)
	resp, err := gc.MatterInvoke(owner, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 600, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (owner arm): unexpected error: %v", err)
	}
	if resp.(core.ArmFailSafeResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("owner arm ErrorCode = %d, want OK", resp.(core.ArmFailSafeResponse).ErrorCode)
	}

	// Intruder disarm: rejected, fail-safe stays armed, hook does NOT fire.
	intruder := im.WithFabricFilter(context.Background(), false, intruderFabric)
	resp, err = gc.MatterInvoke(intruder, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 0, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (intruder disarm): unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorBusyWithOtherAdmin {
		t.Fatalf("intruder disarm ErrorCode = %d, want BusyWithOtherAdmin", r.ErrorCode)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after rejected intruder disarm, want true (stays armed)")
	}
	select {
	case idx := <-fired:
		t.Fatalf("OnFailSafeExpired fired with fabric %d after rejected intruder disarm; must not fire", idx)
	default:
	}

	// Owner disarm: accepted, hook fires with the owning fabric.
	resp, err = gc.MatterInvoke(owner, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 0, Breadcrumb: 0})
	if err != nil {
		t.Fatalf("ArmFailSafe (owner disarm): unexpected error: %v", err)
	}
	r = resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("owner disarm ErrorCode = %d, want OK", r.ErrorCode)
	}
	if gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = true after owner disarm, want false")
	}
	select {
	case idx := <-fired:
		if idx != ownerFabric {
			t.Fatalf("OnFailSafeExpired fired with fabric %d, want %d", idx, ownerFabric)
		}
	default:
		t.Fatal("OnFailSafeExpired did not fire after owner disarm")
	}
}
