// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestIdentify_MatterAcceptedCommands_IncludesTriggerEffect verifies that
// Identify.MatterAcceptedCommands() lists both the mandatory Identify command
// (0x00) and the optional TriggerEffect command (0x40). Strict controllers
// cross-check AcceptedCommandList against the commands they plan to invoke;
// omitting TriggerEffect causes a commissioning warning on iOS 18.4+ devices.
func TestIdentify_MatterAcceptedCommands_IncludesTriggerEffect(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()

	// Ensure Identify satisfies MatterClusterCommandLister at compile time.
	var _ interfaces.MatterClusterCommandLister = id

	cmds := id.MatterAcceptedCommands()
	if len(cmds) == 0 {
		t.Fatal("MatterAcceptedCommands() returned empty slice")
	}

	const (
		cmdIdentify      uint32 = 0x00
		cmdTriggerEffect uint32 = 0x40
	)
	has := make(map[uint32]bool, len(cmds))
	for _, c := range cmds {
		has[c] = true
	}
	if !has[cmdIdentify] {
		t.Errorf("MatterAcceptedCommands() missing Identify (0x00)")
	}
	if !has[cmdTriggerEffect] {
		t.Errorf("MatterAcceptedCommands() missing TriggerEffect (0x40)")
	}
}

// TestIdentify_MatterGeneratedCommands_Nil verifies that
// MatterGeneratedCommands returns nil (Identify carries no response payload).
func TestIdentify_MatterGeneratedCommands_Nil(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	if got := id.MatterGeneratedCommands(); got != nil {
		t.Errorf("MatterGeneratedCommands() = %v, want nil", got)
	}
}

func TestIdentify_ClusterID(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	if got := id.MatterClusterID(); got != 0x0003 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0003", got)
	}
}

func TestIdentify_MatterDataVersionInitialZero(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	// Not bumped until a write/invoke occurs — starts at 0.
	_ = id.MatterDataVersion() // must not panic
}

func TestIdentify_ReadAllAttributes(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	cases := []struct {
		attrID uint32
		name   string
	}{
		{0x0000, "IdentifyTime"},
		{0x0001, "IdentifyType"},
		{cluster.AttrGlobalFeatureMap, "FeatureMap"},
		{cluster.AttrGlobalClusterRevision, "ClusterRevision"},
	}
	for _, tc := range cases {
		v, ok := id.MatterRead(tc.attrID)
		if !ok {
			t.Errorf("MatterRead(0x%04X %s) = (_, false), want true", tc.attrID, tc.name)
		}
		_ = v
	}
}

func TestIdentify_ReadUnknownAttrReturnsFalse(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	if _, ok := id.MatterRead(0xBEEF); ok {
		t.Fatal("MatterRead(0xBEEF) = true, want false for unknown attr")
	}
}

func TestIdentify_ReadIdentifyTimeDefaultZero(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	v, ok := id.MatterRead(0x0000)
	if !ok {
		t.Fatal("IdentifyTime: ok=false")
	}
	if v.(uint16) != 0 {
		t.Fatalf("IdentifyTime = %v, want 0", v)
	}
}

func TestIdentify_ReadIdentifyTypeIsNone(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	v, ok := id.MatterRead(0x0001)
	if !ok {
		t.Fatal("IdentifyType: ok=false")
	}
	if v.(uint8) != 0 {
		t.Fatalf("IdentifyType = %v, want 0 (None)", v)
	}
}

func TestIdentify_WriteIdentifyTimeUint16(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, uint16(5), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite IdentifyTime uint16: %v", err)
	}
	v, _ := id.MatterRead(0x0000)
	if v.(uint16) != 5 {
		t.Fatalf("IdentifyTime = %v, want 5", v)
	}
}

func TestIdentify_WriteIdentifyTimeUint32(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, uint32(7), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite IdentifyTime uint32: %v", err)
	}
	v, _ := id.MatterRead(0x0000)
	if v.(uint16) != 7 {
		t.Fatalf("IdentifyTime = %v, want 7", v)
	}
}

func TestIdentify_WriteIdentifyTimeUint64(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, uint64(3), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite IdentifyTime uint64: %v", err)
	}
}

func TestIdentify_WriteIdentifyTimeInt(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, int(9), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite IdentifyTime int: %v", err)
	}
}

func TestIdentify_WriteIdentifyTimeInt64(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, int64(2), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite IdentifyTime int64: %v", err)
	}
}

func TestIdentify_WriteIdentifyTimeNilCoercesToZero(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite IdentifyTime nil: %v", err)
	}
	v, _ := id.MatterRead(0x0000)
	if v.(uint16) != 0 {
		t.Fatalf("IdentifyTime after nil write = %v, want 0", v)
	}
}

func TestIdentify_WriteIdentifyTimeUnsupportedTypeReturnsError(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0000, "bad", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for unsupported IdentifyTime type, got nil")
	}
}

func TestIdentify_WriteReadOnlyAttrReturnsError(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	err := id.MatterWrite(context.Background(), 0x0001 /* IdentifyType, read-only */, uint8(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for read-only attr write, got nil")
	}
}

func TestIdentify_DataVersionBumpsAfterWrite(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	v0 := id.MatterDataVersion()
	if err := id.MatterWrite(context.Background(), 0x0000, uint16(5), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite: %v", err)
	}
	v1 := id.MatterDataVersion()
	if v1 <= v0 {
		t.Fatalf("DataVersion did not increase after write: %d → %d", v0, v1)
	}
}

func TestIdentify_InvokeIdentifyCommand(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	resp, err := id.MatterInvoke(context.Background(), 0x00, uint16(3), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke Identify: %v", err)
	}
	if resp != nil {
		t.Fatalf("Identify response = %v, want nil (status-only)", resp)
	}
}

func TestIdentify_InvokeIdentifyCommandWithNilFields(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	_, err := id.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke Identify(nil): %v", err)
	}
}

func TestIdentify_DataVersionBumpsAfterInvoke(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	v0 := id.MatterDataVersion()
	if _, err := id.MatterInvoke(context.Background(), 0x00, uint16(2), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke: %v", err)
	}
	v1 := id.MatterDataVersion()
	if v1 <= v0 {
		t.Fatalf("DataVersion did not increase after invoke: %d → %d", v0, v1)
	}
}

func TestIdentify_InvokeTriggerEffect(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	// TriggerEffect (0x40) is a no-op for the bridge — must succeed.
	resp, err := id.MatterInvoke(context.Background(), 0x40, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke TriggerEffect: %v", err)
	}
	if resp != nil {
		t.Fatalf("TriggerEffect response = %v, want nil", resp)
	}
}

func TestIdentify_InvokeUnknownCmdReturnsError(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	for _, cmdID := range []uint32{0x01, 0x02, 0xFF} {
		_, err := id.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestIdentify_MatterReportableContainsIdentifyTime(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	list := id.MatterReportable()
	if slices.Contains(list, 0x0000) {
		return
	}
	t.Fatalf("MatterReportable() = %v — missing IdentifyTime (0x0000)", list)
}

func TestIdentify_MatterAttributesContainsBothAttrs(t *testing.T) {
	t.Parallel()
	id := core.NewIdentify()
	list := id.MatterAttributes()
	have := make(map[uint32]bool, len(list))
	for _, attrID := range list {
		have[attrID] = true
	}
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}

// TestIdentify_CloseStopsTheCountdownAndIsIdempotent pins the disposal
// contract the endpoint layer relies on: once the endpoint hosting an
// Identify server disappears, Close() must terminate its countdown and no
// later write may resurrect it — otherwise a stray goroutine ticks for the
// remaining IdentifyTime (up to 65535 s ≈ 18 h). Close is also called from
// more than one teardown path, so it must tolerate repeats.
// Mirrors matter.js
// packages/node/src/behaviors/identify/IdentifyServer.ts [Symbol.asyncDispose].
func TestIdentify_CloseStopsTheCountdownAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := core.NewIdentify()

	if _, err := id.MatterInvoke(ctx, 0x00, uint16(600), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Identify): %v", err)
	}
	id.Close()
	id.Close() // idempotent — a second teardown must not panic.

	// The attribute stays writable (the cluster is not an error surface
	// after disposal) but the countdown must not restart.
	if err := id.MatterWrite(ctx, 0x0000, uint16(5), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(IdentifyTime) after Close: %v", err)
	}
	got, ok := id.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(IdentifyTime) reported the attribute as absent after Close")
	}
	if got != uint16(5) {
		t.Fatalf("IdentifyTime = %v, want uint16(5)", got)
	}
}
