// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---- helpers ----

func newGencomm(t *testing.T, cfg core.GeneralCommissioningConfig) *core.GeneralCommissioning {
	t.Helper()
	gc, err := core.NewGeneralCommissioning(cfg)
	if err != nil {
		t.Fatalf("NewGeneralCommissioning: %v", err)
	}
	return gc
}

func defaultGencomm(t *testing.T) *core.GeneralCommissioning {
	t.Helper()
	return newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability:           core.RegulatoryIndoorOutdoor,
		SupportsConcurrentConnection: true,
	})
}

// ---- ClusterID / Revision ----

func TestGencomm_ClusterID(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	if got := gc.MatterClusterID(); got != 0x0030 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0030", got)
	}
}

func TestGencomm_ClusterRevision(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	v, ok := gc.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 2 {
		t.Fatalf("ClusterRevision = %v, want 2", v)
	}
}

// ---- Constructor validation ----

func TestGencomm_InvalidLocationCapability(t *testing.T) {
	t.Parallel()
	_, err := core.NewGeneralCommissioning(core.GeneralCommissioningConfig{
		LocationCapability: 99,
	})
	if err == nil {
		t.Fatal("expected error for LocationCapability=99, got nil")
	}
}

func TestGencomm_FailSafeMaxSecondsDefault(t *testing.T) {
	t.Parallel()
	// FailSafeMaxSeconds < 900 → defaults to 900 per matter.js
	// packages/model/src/standard/elements/general-commissioning.element.ts:66
	// (ArmFailSafe ExpiryLengthSeconds field default = 900 s).
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 0,
	})
	v, ok := gc.MatterRead(0x0001) // BasicCommissioningInfo
	if !ok {
		t.Fatal("BasicCommissioningInfo: ok=false")
	}
	info := v.(core.BasicCommissioningInfoStruct)
	if info.FailSafeExpiryLengthSeconds != 900 {
		t.Fatalf("FailSafeExpiryLengthSeconds = %d, want 900", info.FailSafeExpiryLengthSeconds)
	}
}

func TestGencomm_CumulativeFailSafeBumpedToMax(t *testing.T) {
	t.Parallel()
	// CumulativeFailSafeMaxSeconds < FailSafeMaxSeconds → bumped.
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability:           core.RegulatoryIndoor,
		FailSafeMaxSeconds:           600,
		CumulativeFailSafeMaxSeconds: 100, // less than 600
	})
	v, ok := gc.MatterRead(0x0001)
	if !ok {
		t.Fatal("BasicCommissioningInfo: ok=false")
	}
	info := v.(core.BasicCommissioningInfoStruct)
	if info.MaxCumulativeFailsafeSeconds < info.FailSafeExpiryLengthSeconds {
		t.Fatalf("MaxCumulativeFailsafeSeconds=%d < FailSafeExpiryLengthSeconds=%d",
			info.MaxCumulativeFailsafeSeconds, info.FailSafeExpiryLengthSeconds)
	}
}

// ---- Read all initial attributes ----

func TestGencomm_ReadAllInitial(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability:           core.RegulatoryIndoor,
		SupportsConcurrentConnection: true,
		FailSafeMaxSeconds:           600,
		CumulativeFailSafeMaxSeconds: 1200,
	})

	v, ok := gc.MatterRead(0x0000) // Breadcrumb
	if !ok {
		t.Fatal("Breadcrumb: ok=false")
	}
	if v.(uint64) != 0 {
		t.Fatalf("Breadcrumb = %v, want 0", v)
	}

	// BasicCommissioningInfo.
	v, ok = gc.MatterRead(0x0001)
	if !ok {
		t.Fatal("BasicCommissioningInfo: ok=false")
	}
	_ = v.(core.BasicCommissioningInfoStruct)

	// RegulatoryConfig == LocationCapability initially.
	v, ok = gc.MatterRead(0x0002)
	if !ok {
		t.Fatal("RegulatoryConfig: ok=false")
	}
	if v.(uint8) != core.RegulatoryIndoor {
		t.Fatalf("RegulatoryConfig = %v, want %v", v, core.RegulatoryIndoor)
	}

	// LocationCapability.
	v, ok = gc.MatterRead(0x0003)
	if !ok {
		t.Fatal("LocationCapability: ok=false")
	}
	if v.(uint8) != core.RegulatoryIndoor {
		t.Fatalf("LocationCapability = %v, want %v", v, core.RegulatoryIndoor)
	}

	// SupportsConcurrentConnection.
	v, ok = gc.MatterRead(0x0004)
	if !ok {
		t.Fatal("SupportsConcurrentConnection: ok=false")
	}
	if !v.(bool) {
		t.Fatal("SupportsConcurrentConnection = false, want true")
	}

	// FeatureMap == 0.
	v, ok = gc.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) != 0 {
		t.Fatalf("FeatureMap = %v, want 0", v)
	}
}

func TestGencomm_ReadUnknownAttr(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	v, ok := gc.MatterRead(0xDEAD)
	if ok || v != nil {
		t.Fatalf("unknown attr: got (%v, %v), want (nil, false)", v, ok)
	}
}

// ---- Write Breadcrumb ----

func TestGencomm_WriteBreadcrumbValid(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	if err := gc.MatterWrite(context.Background(), 0x0000, uint64(42), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite Breadcrumb: %v", err)
	}
	v, ok := gc.MatterRead(0x0000)
	if !ok {
		t.Fatal("Breadcrumb: ok=false")
	}
	if v.(uint64) != 42 {
		t.Fatalf("Breadcrumb = %v, want 42", v)
	}
}

func TestGencomm_WriteBreadcrumbWrongType(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	err := gc.MatterWrite(context.Background(), 0x0000, "not-a-uint64", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for wrong type, got nil")
	}
}

func TestGencomm_WriteReadOnlyAttr(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	// RegulatoryConfig (0x0002) is read-only via MatterWrite.
	err := gc.MatterWrite(context.Background(), 0x0002, uint8(1), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for write to read-only attr, got nil")
	}
}

// ---- Invoke ArmFailSafe ----

func TestGencomm_ArmFailSafe_Disarm(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	resp, err := gc.MatterInvoke(context.Background(), 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 0, Breadcrumb: 0},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe disarm: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	if gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = true after disarm, want false")
	}
}

func TestGencomm_ArmFailSafe_Arms(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	resp, err := gc.MatterInvoke(context.Background(), 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe arm: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after arm, want true")
	}
}

func TestGencomm_ArmFailSafe_ExceedsMax_IsValueOutsideRange(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 120,
	})
	resp, err := gc.MatterInvoke(context.Background(), 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 999, Breadcrumb: 0},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe over-max: unexpected error: %v", err)
	}
	r := resp.(core.ArmFailSafeResponse)
	if r.ErrorCode != core.CommissioningErrorValueOutsideRange {
		t.Fatalf("ErrorCode = %d, want ValueOutsideRange", r.ErrorCode)
	}
}

func TestGencomm_ArmFailSafe_SetsBreadcrumb(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	_, err := gc.MatterInvoke(context.Background(), 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 77},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}
	v, _ := gc.MatterRead(0x0000)
	if v.(uint64) != 77 {
		t.Fatalf("Breadcrumb = %v, want 77", v)
	}
}

func TestGencomm_ArmFailSafe_OnExpired_Hook(t *testing.T) {
	// Not t.Parallel() because this test is time-sensitive.
	const fabricIndex = uint8(3)
	called := make(chan uint8, 1)

	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
		OnFailSafeExpired: func(_ context.Context, idx uint8) {
			called <- idx
		},
	})

	// Pass fabricIndex via context so handleArmFailSafe captures it as
	// failSafeFabricIndex and the expiry hook fires with the right fabric.
	ctx := im.WithFabricFilter(context.Background(), false, fabricIndex)
	resp, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 1, Breadcrumb: 0},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}
	if resp.(core.ArmFailSafeResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode != OK")
	}

	select {
	case idx := <-called:
		if idx != fabricIndex {
			t.Fatalf("hook called with fabricIndex=%d, want %d", idx, fabricIndex)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnFailSafeExpired hook was not called within 3s")
	}
}

// TestGencomm_ArmFailSafe_DisarmFiresRevertHook verifies that an
// explicit disarm (ExpiryLengthSeconds=0) of an ARMED fail-safe runs
// the same revert path as the timeout: it fires OnFailSafeExpired so
// OperationalCredentials performs the AddNOC rollback. Without this a
// disarm before CommissioningComplete would leak a half-installed NOC.
// Mirrors chip GeneralCommissioningCluster.cpp:429-432
// (ForceFailSafeTimerExpiry) → FailSafeContext.cpp:66-76 cleanup.
func TestGencomm_ArmFailSafe_DisarmFiresRevertHook(t *testing.T) {
	t.Parallel()
	const fabricIndex = uint8(5)
	called := make(chan uint8, 1)

	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
		OnFailSafeExpired: func(_ context.Context, idx uint8) {
			called <- idx
		},
	})

	// Arm with a long window so it cannot time out before we disarm.
	ctx := im.WithFabricFilter(context.Background(), false, fabricIndex)
	if _, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 600, Breadcrumb: 0},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ArmFailSafe arm: %v", err)
	}

	// Explicit disarm must fire the revert hook with the armed fabric.
	resp, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 0, Breadcrumb: 0},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe disarm: %v", err)
	}
	if resp.(core.ArmFailSafeResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("disarm ErrorCode != OK")
	}
	if gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = true after disarm, want false")
	}
	select {
	case idx := <-called:
		if idx != fabricIndex {
			t.Fatalf("revert hook fabricIndex=%d, want %d", idx, fabricIndex)
		}
	default:
		t.Fatal("disarm did not fire OnFailSafeExpired revert hook")
	}
}

// TestGencomm_ArmFailSafe_DisarmUnarmedDoesNotFireHook verifies the
// revert hook does NOT fire on a disarm when no fail-safe was armed —
// there is no pending NOC to roll back.
func TestGencomm_ArmFailSafe_DisarmUnarmedDoesNotFireHook(t *testing.T) {
	t.Parallel()
	called := make(chan uint8, 1)
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
		OnFailSafeExpired: func(_ context.Context, idx uint8) {
			called <- idx
		},
	})
	if _, err := gc.MatterInvoke(context.Background(), 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 0, Breadcrumb: 0},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ArmFailSafe disarm: %v", err)
	}
	select {
	case <-called:
		t.Fatal("disarm of unarmed fail-safe fired the revert hook unexpectedly")
	default:
	}
}

// TestGencomm_SetOnFailSafeExpired_OverridesConstructorHook covers the
// post-construction setter the daemon uses to replace the bootstrap
// window-revoke hook with the production hook that also calls
// OpCreds.ClearPendingState. The original config hook is invoked once
// during NewGeneralCommissioning bootstrap; the override must be the
// one that fires at expiry time.
func TestGencomm_SetOnFailSafeExpired_OverridesConstructorHook(t *testing.T) {
	const fabricIndex = uint8(5)
	original := make(chan uint8, 1)
	override := make(chan uint8, 1)

	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
		OnFailSafeExpired: func(_ context.Context, idx uint8) {
			original <- idx
		},
	})
	gc.SetOnFailSafeExpired(func(_ context.Context, idx uint8) {
		override <- idx
	})

	ctx := im.WithFabricFilter(context.Background(), false, fabricIndex)
	if _, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 1},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	select {
	case got := <-override:
		if got != fabricIndex {
			t.Fatalf("override hook fabricIndex=%d, want %d", got, fabricIndex)
		}
	case <-original:
		t.Fatal("original constructor hook fired; override should win")
	case <-time.After(3 * time.Second):
		t.Fatal("override hook not called within 3s")
	}
}

// TestGencomm_SetOnFailSafeExpired_NilHookAtArmStillExpires covers the
// case where the post-setter installs the hook BEFORE the arm — the
// production path used by daemon.go. Even though the constructor was
// invoked with a nil OnFailSafeExpired, the setter must enable the
// watcher.
func TestGencomm_SetOnFailSafeExpired_NilAtConstruction(t *testing.T) {
	const fabricIndex = uint8(9)
	fired := make(chan uint8, 1)

	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	gc.SetOnFailSafeExpired(func(_ context.Context, idx uint8) {
		fired <- idx
	})

	ctx := im.WithFabricFilter(context.Background(), false, fabricIndex)
	if _, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 1},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	select {
	case got := <-fired:
		if got != fabricIndex {
			t.Fatalf("hook fabricIndex=%d, want %d", got, fabricIndex)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hook installed via SetOnFailSafeExpired did not fire")
	}
}

func TestGencomm_ArmFailSafe_ReArm_ExtendsWindow(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	ctx := context.Background()

	// First arm.
	_, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("first ArmFailSafe: %v", err)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("not armed after first arm")
	}

	// Re-arm with a longer window.
	resp, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 120},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("re-arm ArmFailSafe: %v", err)
	}
	if resp.(core.ArmFailSafeResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("re-arm ErrorCode != OK")
	}
	if !gc.FailSafeArmed() {
		t.Fatal("not armed after re-arm")
	}
}

func TestGencomm_ArmFailSafe_FabricFromContext_HookArg(t *testing.T) {
	// Not t.Parallel() — time-sensitive.
	const fabricIndex = uint8(7)
	called := make(chan uint8, 1)

	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
		OnFailSafeExpired: func(_ context.Context, idx uint8) {
			called <- idx
		},
	})

	// FabricIndex is conveyed through the context (as in production).
	ctx := im.WithFabricFilter(context.Background(), false, fabricIndex)
	_, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 1},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	select {
	case got := <-called:
		if got != fabricIndex {
			t.Fatalf("hook fabricIndex=%d, want %d", got, fabricIndex)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnFailSafeExpired not called within 3s")
	}
}

func TestGencomm_FailSafeArmed_ReturnsFalseAfterWindowExpired(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	// Arm with a 1-second window.
	_, err := gc.MatterInvoke(context.Background(), 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 1},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false immediately after arm")
	}
	// Wait for window to expire.
	time.Sleep(1100 * time.Millisecond)
	// FailSafeArmed checks time internally; must report false without
	// a watcher reset.
	if gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = true after window expired")
	}
}

// TestGencomm_CommissioningComplete_AcceptsAfterSetCurrentFabricReArm
// reproduces Apple's Multi-Admin pairing: Hub#1 arms FailSafe on
// fabric 1, AddNOC installs fabric 2 (system commissioner), and Apple
// expects the bridge to honour CommissioningComplete on fabric 2.
// Per Matter Core §11.18.6.16 a successful AddNOC SHALL re-arm the
// FailSafe so the new fabric is the failsafe fabric. The daemon
// implements this by calling [GeneralCommissioning.SetCurrentFabric]
// from the OnFabricInstalled hook; this test verifies the contract
// at the cluster boundary.
func TestGencomm_CommissioningComplete_AcceptsAfterSetCurrentFabricReArm(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})

	// Step 1: ArmFailSafe over Hub#1's CASE session (fabric 1).
	hub1 := im.WithFabricFilter(context.Background(), false, uint8(1))
	if _, err := gc.MatterInvoke(hub1, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ArmFailSafe(hub1): %v", err)
	}

	// Step 2: simulate AddNOC OnFabricInstalled hook firing with the
	// freshly-installed fabric index 2 — bridge code path:
	// daemon.go OnFabricInstalled → gc.SetCurrentFabric(2).
	gc.SetCurrentFabric(2)

	// Step 3: Hub#2 (system commissioner) opens its own CASE session
	// on fabric 2 and calls CommissioningComplete. Bridge MUST accept.
	hub2 := im.WithFabricFilter(context.Background(), false, uint8(2))
	resp, err := gc.MatterInvoke(hub2, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete(hub2): %v", err)
	}
	completion, ok := resp.(core.CommissioningCompleteResponse)
	if !ok {
		t.Fatalf("response: got %T, want CommissioningCompleteResponse", resp)
	}
	if completion.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d (%q), want OK — Apple Multi-Admin pair "+
			"would abort with HMMTRErrorDomain Code 9 here",
			completion.ErrorCode, completion.DebugText)
	}
}

// TestGencomm_CommissioningComplete_RejectsWithoutReArm proves the
// negative: without the SetCurrentFabric re-arm, a CommissioningComplete
// from a different fabric must still be rejected with
// InvalidAuthentication — the spec-conformance side of the same flow.
func TestGencomm_CommissioningComplete_RejectsWithoutReArm(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})

	hub1 := im.WithFabricFilter(context.Background(), false, uint8(1))
	if _, err := gc.MatterInvoke(hub1, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 0},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	// No SetCurrentFabric(2) — simulates the pre-Bug-F state where
	// AddNOC failed to re-arm the failsafe.
	hub2 := im.WithFabricFilter(context.Background(), false, uint8(2))
	resp, err := gc.MatterInvoke(hub2, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete: %v", err)
	}
	completion, ok := resp.(core.CommissioningCompleteResponse)
	if !ok {
		t.Fatalf("response: got %T, want CommissioningCompleteResponse", resp)
	}
	if completion.ErrorCode != core.CommissioningErrorInvalidAuthentication {
		t.Fatalf("ErrorCode = %d, want InvalidAuthentication", completion.ErrorCode)
	}
}

// ---- Invoke SetRegulatoryConfig ----

func TestGencomm_SetRegulatoryConfig_ValidIndoor(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoorOutdoor,
		FailSafeMaxSeconds: 600,
	})
	resp, err := gc.MatterInvoke(context.Background(), 0x02,
		core.SetRegulatoryConfigRequest{
			NewRegulatoryConfig: core.RegulatoryIndoor,
			CountryCode:         "DE",
			Breadcrumb:          1,
		},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetRegulatoryConfig: %v", err)
	}
	r := resp.(core.SetRegulatoryConfigResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	v, _ := gc.MatterRead(0x0002)
	if v.(uint8) != core.RegulatoryIndoor {
		t.Fatalf("RegulatoryConfig = %v, want Indoor", v)
	}
}

func TestGencomm_SetRegulatoryConfig_EmptyCountryCodeAccepted(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoorOutdoor,
		FailSafeMaxSeconds: 600,
	})
	resp, err := gc.MatterInvoke(context.Background(), 0x02,
		core.SetRegulatoryConfigRequest{
			NewRegulatoryConfig: core.RegulatoryIndoor,
			CountryCode:         "",
		},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetRegulatoryConfig empty CC: %v", err)
	}
	if resp.(core.SetRegulatoryConfigResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode != OK for empty CountryCode")
	}
}

func TestGencomm_SetRegulatoryConfig_ThreeCharCountryCode_ValueOutsideRange(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoorOutdoor,
		FailSafeMaxSeconds: 600,
	})
	resp, err := gc.MatterInvoke(context.Background(), 0x02,
		core.SetRegulatoryConfigRequest{
			NewRegulatoryConfig: core.RegulatoryIndoor,
			CountryCode:         "DEU",
		},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetRegulatoryConfig 3-char CC: unexpected error: %v", err)
	}
	if resp.(core.SetRegulatoryConfigResponse).ErrorCode != core.CommissioningErrorValueOutsideRange {
		t.Fatalf("ErrorCode != ValueOutsideRange for 3-char CountryCode")
	}
}

func TestGencomm_SetRegulatoryConfig_ValueAboveMax_OutsideRange(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoorOutdoor,
		FailSafeMaxSeconds: 600,
	})
	resp, err := gc.MatterInvoke(context.Background(), 0x02,
		core.SetRegulatoryConfigRequest{
			NewRegulatoryConfig: 3, // > RegulatoryIndoorOutdoor (2)
			CountryCode:         "DE",
		},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetRegulatoryConfig invalid value: unexpected error: %v", err)
	}
	if resp.(core.SetRegulatoryConfigResponse).ErrorCode != core.CommissioningErrorValueOutsideRange {
		t.Fatalf("ErrorCode != ValueOutsideRange for value > 2")
	}
}

// ---- Invoke CommissioningComplete ----

func TestGencomm_CommissioningComplete_NoFailSafe(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	// fabric=1 simulates a CASE session; the PASE-reject (fabric=0) fires
	// first otherwise, masking the NoFailSafe check we want to exercise.
	ctx := im.WithFabricFilter(context.Background(), false, 1)
	resp, err := gc.MatterInvoke(ctx, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete (no fail-safe): %v", err)
	}
	r := resp.(core.CommissioningCompleteResponse)
	if r.ErrorCode != core.CommissioningErrorNoFailSafe {
		t.Fatalf("ErrorCode = %d, want NoFailSafe", r.ErrorCode)
	}
}

func TestGencomm_CommissioningComplete_AfterArmFailSafe(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	// fabric=1 simulates a CASE session throughout commissioning.
	ctx := im.WithFabricFilter(context.Background(), false, 1)

	// Arm first.
	_, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	resp, err := gc.MatterInvoke(ctx, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete: %v", err)
	}
	r := resp.(core.CommissioningCompleteResponse)
	if r.ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode = %d, want OK", r.ErrorCode)
	}
	if gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = true after CommissioningComplete")
	}
}

func TestGencomm_CommissioningComplete_PASESession_Rejected(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	// Arm under fabric=1 (CASE) to get a valid fail-safe window.
	armCtx := im.WithFabricFilter(context.Background(), false, 1)
	_, err := gc.MatterInvoke(armCtx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	// CommissioningComplete over a PASE session (fabric=0) must be rejected.
	paseCtx := im.WithFabricFilter(context.Background(), false, 0)
	resp, err := gc.MatterInvoke(paseCtx, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete (PASE): %v", err)
	}
	r := resp.(core.CommissioningCompleteResponse)
	if r.ErrorCode != core.CommissioningErrorInvalidAuthentication {
		t.Fatalf("ErrorCode = %d, want InvalidAuthentication", r.ErrorCode)
	}
	// FailSafe must still be armed — PASE rejection must not clear it.
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafeArmed = false after PASE-rejected CommissioningComplete")
	}
}

func TestGencomm_CommissioningComplete_FabricMismatch_Rejected(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	// Arm under fabric=3.
	armCtx := im.WithFabricFilter(context.Background(), false, 3)
	_, err := gc.MatterInvoke(armCtx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}

	// CommissioningComplete from a different fabric (5) must be rejected.
	mismatchCtx := im.WithFabricFilter(context.Background(), false, 5)
	resp, err := gc.MatterInvoke(mismatchCtx, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete (fabric mismatch): %v", err)
	}
	r := resp.(core.CommissioningCompleteResponse)
	if r.ErrorCode != core.CommissioningErrorInvalidAuthentication {
		t.Fatalf("ErrorCode = %d, want InvalidAuthentication", r.ErrorCode)
	}
}

func TestGencomm_CommissioningComplete_BreadcrumbReset(t *testing.T) {
	// Locks D-56: matter.js GeneralCommissioningServer.ts:255
	// `this.state.breadcrumb = BigInt(0)` on success.
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	ctx := im.WithFabricFilter(context.Background(), false, 2)

	// Arm with a non-zero Breadcrumb.
	_, err := gc.MatterInvoke(ctx, 0x00,
		core.ArmFailSafeRequest{ExpiryLengthSeconds: 60, Breadcrumb: 42},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("ArmFailSafe: %v", err)
	}
	v, _ := gc.MatterRead(0x0000)
	if v.(uint64) != 42 {
		t.Fatalf("Breadcrumb after ArmFailSafe = %v, want 42", v)
	}

	// CommissioningComplete must reset Breadcrumb to 0.
	resp, err := gc.MatterInvoke(ctx, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CommissioningComplete: %v", err)
	}
	if resp.(core.CommissioningCompleteResponse).ErrorCode != core.CommissioningErrorOK {
		t.Fatalf("ErrorCode != OK")
	}
	v, _ = gc.MatterRead(0x0000)
	if v.(uint64) != 0 {
		t.Fatalf("Breadcrumb after CommissioningComplete = %v, want 0", v)
	}
}

// ---- Invoke unknown command ----

func TestGencomm_Invoke_UnknownCmd(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	_, err := gc.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
}

// ---- MatterReportable ----

func TestGencomm_Reportable(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	attrs := gc.MatterReportable()
	for _, want := range []uint32{0x0000, 0x0002} {
		if !slices.Contains(attrs, want) {
			t.Errorf("MatterReportable missing attr 0x%04X", want)
		}
	}
}

// ---- Concurrent safety ----

func TestGencomm_Concurrent_Race(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability: core.RegulatoryIndoor,
		FailSafeMaxSeconds: 600,
	})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				_, _ = gc.MatterInvoke(ctx, 0x00,
					core.ArmFailSafeRequest{ExpiryLengthSeconds: 60},
					hmenum.CommandPriorityHigh)
			case 1:
				_, _ = gc.MatterRead(0x0000)
				_, _ = gc.MatterRead(0x0002)
			case 2:
				_ = gc.MatterWrite(ctx, 0x0000, uint64(99), hmenum.CommandPriorityHigh)
			}
		}(i)
	}
	wg.Wait()
}

// TestAutoArmOnPaseEstablished_ArmsWhenNotAlreadyArmed verifies that
// AutoArmOnPaseEstablished arms the fail-safe when no prior arm exists.
func TestAutoArmOnPaseEstablished_ArmsWhenNotAlreadyArmed(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	if gc.FailSafeArmed() {
		t.Fatal("precondition: FailSafe should not be armed")
	}
	gc.AutoArmOnPaseEstablished(context.Background())
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafe should be armed after AutoArmOnPaseEstablished")
	}
}

// TestAutoArmOnPaseEstablished_DoesNotOverwriteExistingArm verifies
// that AutoArmOnPaseEstablished is a no-op when the fail-safe is
// already armed by an explicit ArmFailSafe command from the commissioner.
func TestAutoArmOnPaseEstablished_DoesNotOverwriteExistingArm(t *testing.T) {
	t.Parallel()
	gc := newGencomm(t, core.GeneralCommissioningConfig{
		LocationCapability:           core.RegulatoryIndoorOutdoor,
		FailSafeMaxSeconds:           900,
		CumulativeFailSafeMaxSeconds: 900,
	})
	ctx := context.Background()
	// Explicitly arm with 900 s.
	if err := gc.ArmFailSafeFor(ctx, 900, 0); err != nil {
		t.Fatalf("ArmFailSafeFor: %v", err)
	}
	// AutoArm must not overwrite the existing 900-s window.
	gc.AutoArmOnPaseEstablished(ctx)
	// Fail-safe must still be armed (unchanged).
	if !gc.FailSafeArmed() {
		t.Fatal("FailSafe should remain armed after AutoArmOnPaseEstablished no-op")
	}
}

func TestGenComm_MatterDataVersion(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	_ = gc.MatterDataVersion() // must not panic
}

func TestGenComm_MatterAttributes(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	list := gc.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	// Breadcrumb (0x0000) and BasicCommissioningInfo (0x0001) are mandatory.
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}

func TestGenComm_MatterAcceptedCommands(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	list := gc.MatterAcceptedCommands()
	if len(list) == 0 {
		t.Fatal("MatterAcceptedCommands() is empty")
	}
}

func TestGenComm_MatterGeneratedCommands(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	list := gc.MatterGeneratedCommands()
	if len(list) == 0 {
		t.Fatal("MatterGeneratedCommands() is empty")
	}
}

func TestGenComm_ArmFailSafeForArmsThenExpires(t *testing.T) {
	t.Parallel()
	gc := defaultGencomm(t)
	// ArmFailSafeFor with a short window (must not block).
	err := gc.ArmFailSafeFor(context.Background(), 60, 1)
	if err != nil {
		t.Fatalf("ArmFailSafeFor: %v", err)
	}
}
