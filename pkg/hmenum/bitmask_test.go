// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

func TestOperationsBitmask(t *testing.T) {
	rw := OperationsRead | OperationsWrite
	if !rw.IsReadable() {
		t.Error("RW should be readable")
	}
	if !rw.IsWritable() {
		t.Error("RW should be writable")
	}
	if rw.IsEvent() {
		t.Error("RW should not include event")
	}
	if OperationsNone.IsReadable() {
		t.Error("NONE must not be readable")
	}
	if rw.IsDeterminable() {
		t.Error("RW should not include determine")
	}
	// The firmware spells the fourth bit out as operations="read,write,determine";
	// on the wire it arrives as bit 0x08.
	rwd := OperationsRead | OperationsWrite | OperationsDetermine
	if !rwd.IsDeterminable() {
		t.Error("RWD should be determinable")
	}
	if OperationsDetermine != 8 {
		t.Fatalf("OperationsDetermine=%d, want 8 (CCU determine bit)", OperationsDetermine)
	}
}

func TestFlagBitmask(t *testing.T) {
	f := FlagVisible | FlagService
	if !f.IsVisible() {
		t.Error("expected VISIBLE")
	}
	if !f.IsService() {
		t.Error("expected SERVICE")
	}
	if f.IsInternal() {
		t.Error("INTERNAL should not be set")
	}
}

func TestFlagStickyKeepsAiohomematicEncoding(t *testing.T) {
	// STICKY is documented as 0x10 but
	// The contract test locks the wire-incompatible-but-compatible value.
	if FlagSticky != 10 {
		t.Fatalf("FlagSticky=%d, want 10 (aiohomematic parity)", FlagSticky)
	}
}

func TestCommandPriorityCriticalIsZero(t *testing.T) {
	// CLAUDE.md §Critical Rules: CRITICAL == 0. Changing this is a spec
	// violation with potential for zero-value bugs across the codebase.
	if CommandPriorityCritical != 0 {
		t.Fatalf("CRITICAL=%d, want 0", CommandPriorityCritical)
	}
}

func TestCategoryToTypeCoversAllCategories(t *testing.T) {
	all := []DataPointCategory{
		DataPointCategoryAction, DataPointCategoryActionNumber,
		DataPointCategoryActionSelect, DataPointCategoryBinarySensor,
		DataPointCategoryButton, DataPointCategoryClimate,
		DataPointCategoryCover, DataPointCategoryEvent,
		DataPointCategoryEventGroup, DataPointCategoryHubBinarySensor,
		DataPointCategoryHubButton, DataPointCategoryHubNumber,
		DataPointCategoryHubSelect, DataPointCategoryHubSensor,
		DataPointCategoryHubSwitch, DataPointCategoryHubText,
		DataPointCategoryHubUpdate, DataPointCategoryLight,
		DataPointCategoryLock, DataPointCategoryNumber,
		DataPointCategoryScheduleSwitch, DataPointCategorySelect,
		DataPointCategorySensor, DataPointCategorySiren,
		DataPointCategorySwitch, DataPointCategoryText,
		DataPointCategoryTextDisplay, DataPointCategoryUpdate,
		DataPointCategoryValve, DataPointCategoryWeekProfile,
	}
	for _, c := range all {
		if _, ok := CategoryToType[c]; !ok {
			t.Errorf("CategoryToType missing entry for %s", c)
		}
	}
	// Undefined is intentionally absent — it never maps to a real type.
	if _, ok := CategoryToType[DataPointCategoryUndefined]; ok {
		t.Error("CategoryToType must not map 'undefined'")
	}
}

func TestActionCategoriesSet(t *testing.T) {
	for _, c := range []DataPointCategory{
		DataPointCategoryAction, DataPointCategoryActionNumber,
		DataPointCategoryActionSelect, DataPointCategoryButton,
	} {
		if !c.IsAction() {
			t.Errorf("%s should be action category", c)
		}
	}
	if DataPointCategorySensor.IsAction() {
		t.Error("sensor must not be action")
	}
}

func TestFlagIsTransformAndSticky(t *testing.T) {
	f := FlagTransform | FlagSticky
	if !f.IsTransform() {
		t.Error("IsTransform() should be true")
	}
	if !f.IsSticky() {
		t.Error("IsSticky() should be true")
	}
	plain := FlagVisible
	if plain.IsTransform() {
		t.Error("plain flag: IsTransform() should be false")
	}
	if plain.IsSticky() {
		t.Error("plain flag: IsSticky() should be false")
	}
}

func TestFlagHas(t *testing.T) {
	f := FlagVisible | FlagInternal
	if !f.Has(FlagVisible) {
		t.Error("Has(FlagVisible) should be true")
	}
	if !f.Has(FlagInternal) {
		t.Error("Has(FlagInternal) should be true")
	}
	if f.Has(FlagTransform) {
		t.Error("Has(FlagTransform) should be false")
	}
}

func TestRxModeHas(t *testing.T) {
	m := RxModeAlways | RxModeBurst
	if !m.Has(RxModeAlways) {
		t.Error("Has(RxModeAlways) should be true")
	}
	if !m.Has(RxModeBurst) {
		t.Error("Has(RxModeBurst) should be true")
	}
	if m.Has(RxModeWakeup) {
		t.Error("Has(RxModeWakeup) should be false")
	}
}

func TestDeviceTriggerEventTypeShort(t *testing.T) {
	cases := map[DeviceTriggerEventType]string{
		DeviceTriggerEventTypeDeviceError: "device_error",
		DeviceTriggerEventTypeImpulse:     "impulse",
		DeviceTriggerEventTypeKeypress:    "keypress",
	}
	for k, want := range cases {
		if got := k.Short(); got != want {
			t.Errorf("%s.Short() = %q, want %q", k, got, want)
		}
	}
	// No dot in the string → returns the full string.
	plain := DeviceTriggerEventType("nodot")
	if got := plain.Short(); got != "nodot" {
		t.Errorf("nodot.Short() = %q, want %q", got, "nodot")
	}
}

func TestConnectionStageDisplayName(t *testing.T) {
	cases := map[ConnectionStage]string{
		ConnectionStageLost:         "Connection Lost",
		ConnectionStageTCPAvailable: "TCP Port Available",
		ConnectionStageRPCAvailable: "RPC Responding",
		ConnectionStageWarmup:       "Warmup Period",
		ConnectionStageEstablished:  "Connection Established",
		ConnectionStage(99):         "Unknown",
	}
	for stage, want := range cases {
		if got := stage.DisplayName(); got != want {
			t.Errorf("ConnectionStage(%d).DisplayName() = %q, want %q", stage, got, want)
		}
	}
}
