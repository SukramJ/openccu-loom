// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// schedule_coerce_test.go covers coerceInt, coerceFloat,
// scheduleToMap, splitChannelAddress, ScheduleQueryAdapter nil guards,
// SchedulesDomain.SetAuditRecorder, serializeSimpleScheduleWithDomain,
// toFloat (link_profile.go), and peerChannelType nil guards.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ============================================================
// coerceInt
// ============================================================

func TestCoerceIntInt(t *testing.T) {
	t.Parallel()
	v, ok := coerceInt(42)
	if !ok || v != 42 {
		t.Errorf("coerceInt(int) = (%d, %v), want (42, true)", v, ok)
	}
}

func TestCoerceIntInt32(t *testing.T) {
	t.Parallel()
	v, ok := coerceInt(int32(7))
	if !ok || v != 7 {
		t.Errorf("coerceInt(int32) = (%d, %v), want (7, true)", v, ok)
	}
}

func TestCoerceIntInt64(t *testing.T) {
	t.Parallel()
	v, ok := coerceInt(int64(100))
	if !ok || v != 100 {
		t.Errorf("coerceInt(int64) = (%d, %v), want (100, true)", v, ok)
	}
}

func TestCoerceIntFloat32(t *testing.T) {
	t.Parallel()
	v, ok := coerceInt(float32(3))
	if !ok || v != 3 {
		t.Errorf("coerceInt(float32) = (%d, %v), want (3, true)", v, ok)
	}
}

func TestCoerceIntFloat64(t *testing.T) {
	t.Parallel()
	v, ok := coerceInt(float64(5))
	if !ok || v != 5 {
		t.Errorf("coerceInt(float64) = (%d, %v), want (5, true)", v, ok)
	}
}

func TestCoerceIntString(t *testing.T) {
	t.Parallel()
	v, ok := coerceInt("99")
	if !ok || v != 99 {
		t.Errorf("coerceInt(string) = (%d, %v), want (99, true)", v, ok)
	}
}

func TestCoerceIntStringBad(t *testing.T) {
	t.Parallel()
	_, ok := coerceInt("not-a-number")
	if ok {
		t.Error("coerceInt bad string must return false")
	}
}

func TestCoerceIntUnsupported(t *testing.T) {
	t.Parallel()
	_, ok := coerceInt(struct{}{})
	if ok {
		t.Error("coerceInt struct must return false")
	}
}

// ============================================================
// coerceFloat
// ============================================================

func TestCoerceFloatFloat64(t *testing.T) {
	t.Parallel()
	v, ok := coerceFloat(1.5)
	if !ok || v != 1.5 {
		t.Errorf("coerceFloat(float64) = (%v, %v), want (1.5, true)", v, ok)
	}
}

func TestCoerceFloatFloat32(t *testing.T) {
	t.Parallel()
	v, ok := coerceFloat(float32(2.5))
	if !ok {
		t.Error("coerceFloat(float32) must succeed")
	}
	_ = v
}

func TestCoerceFloatInt(t *testing.T) {
	t.Parallel()
	v, ok := coerceFloat(3)
	if !ok || v != 3.0 {
		t.Errorf("coerceFloat(int) = (%v, %v), want (3.0, true)", v, ok)
	}
}

func TestCoerceFloatInt32(t *testing.T) {
	t.Parallel()
	v, ok := coerceFloat(int32(4))
	if !ok || v != 4.0 {
		t.Errorf("coerceFloat(int32) = (%v, %v), want (4.0, true)", v, ok)
	}
}

func TestCoerceFloatInt64(t *testing.T) {
	t.Parallel()
	v, ok := coerceFloat(int64(5))
	if !ok || v != 5.0 {
		t.Errorf("coerceFloat(int64) = (%v, %v), want (5.0, true)", v, ok)
	}
}

func TestCoerceFloatString(t *testing.T) {
	t.Parallel()
	v, ok := coerceFloat("3.14")
	if !ok || v != 3.14 {
		t.Errorf("coerceFloat(string) = (%v, %v), want (3.14, true)", v, ok)
	}
}

func TestCoerceFloatStringBad(t *testing.T) {
	t.Parallel()
	_, ok := coerceFloat("not-a-float")
	if ok {
		t.Error("coerceFloat bad string must return false")
	}
}

func TestCoerceFloatUnsupported(t *testing.T) {
	t.Parallel()
	_, ok := coerceFloat(true)
	if ok {
		t.Error("coerceFloat bool must return false")
	}
}

// ============================================================
// scheduleToMap
// ============================================================

func TestScheduleToMapNil(t *testing.T) {
	t.Parallel()
	m, err := scheduleToMap(nil)
	if err != nil {
		t.Fatalf("scheduleToMap nil: %v", err)
	}
	if m == nil {
		t.Error("scheduleToMap nil must return empty map, not nil")
	}
}

func TestScheduleToMapNonNil(t *testing.T) {
	t.Parallel()
	dto := &hmapi.ClimateSchedule{}
	m, err := scheduleToMap(dto)
	if err != nil {
		t.Fatalf("scheduleToMap non-nil: %v", err)
	}
	if m == nil {
		t.Error("scheduleToMap non-nil must return non-nil map")
	}
}

// ============================================================
// splitChannelAddress
// ============================================================

func TestSplitChannelAddressNormal(t *testing.T) {
	t.Parallel()
	dev, ch := splitChannelAddress("DEV001:2")
	if dev != "DEV001" || ch != 2 {
		t.Errorf("splitChannelAddress = (%q, %d), want (DEV001, 2)", dev, ch)
	}
}

func TestSplitChannelAddressNoColon(t *testing.T) {
	t.Parallel()
	dev, ch := splitChannelAddress("DEV001")
	if dev != "DEV001" || ch != 0 {
		t.Errorf("splitChannelAddress no colon = (%q, %d), want (DEV001, 0)", dev, ch)
	}
}

func TestSplitChannelAddressNonNumericSuffix(t *testing.T) {
	t.Parallel()
	dev, ch := splitChannelAddress("DEV001:abc")
	if dev != "DEV001:abc" || ch != 0 {
		t.Errorf("splitChannelAddress non-numeric = (%q, %d), want (DEV001:abc, 0)", dev, ch)
	}
}

// ============================================================
// ScheduleQueryAdapter nil-domain guards
// ============================================================

func TestScheduleQueryAdapterNilDomain(t *testing.T) {
	t.Parallel()
	a := NewScheduleQueryAdapter(nil)

	if _, err := a.GetClimateSchedule(context.Background(), "DEV:1"); err == nil {
		t.Error("GetClimateSchedule nil domain must error")
	}
	if _, err := a.SetClimateSchedule(context.Background(), "DEV:1", nil); err == nil {
		t.Error("SetClimateSchedule nil domain must error")
	}
	if err := a.SetActiveProfile(context.Background(), "DEV:1", 1); err == nil {
		t.Error("SetActiveProfile nil domain must error")
	}
	if _, err := a.GetDeviceSchedule(context.Background(), "DEV001"); err == nil {
		t.Error("GetDeviceSchedule nil domain must error")
	}
	if _, err := a.SetDeviceSchedule(context.Background(), "DEV001", nil); err == nil {
		t.Error("SetDeviceSchedule nil domain must error")
	}
	if err := a.SetDeviceActiveProfile(context.Background(), "DEV001", "P1"); err == nil {
		t.Error("SetDeviceActiveProfile nil domain must error")
	}
}

// ============================================================
// SchedulesDomain.SetAuditRecorder
// ============================================================

func TestSchedulesDomainSetAuditRecorderNil(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	got := s.SetAuditRecorder(nil)
	if got == nil {
		t.Fatal("SetAuditRecorder must return non-nil receiver")
	}
}

// ============================================================
// serializeSimpleScheduleWithDomain
// ============================================================

func TestSerializeSimpleScheduleWithDomainNonLock(t *testing.T) {
	t.Parallel()
	entries := []hmapi.SimpleScheduleEntry{
		{
			SlotNo:   1,
			Weekdays: []string{"MONDAY"},
			Time:     "07:30",
			Level:    1.0,
		},
	}
	m, err := serializeSimpleScheduleWithDomain(entries, "switch", schedule.SimpleMaxSlot, nil)
	if err != nil {
		t.Fatalf("serializeSimpleScheduleWithDomain switch: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil map")
	}
}

// TestSerializeSimpleScheduleWithDomainLock asserts every door-lock action
// reaches the paramset as the (LEVEL, DURATION_BASE, DURATION_FACTOR)
// triplet its table entry declares.
//
// putParamset merges sparsely, so a key the encoder omits leaves whatever
// the device holds. `lock_autorelock_start` is (0, 0, 0) — the one action
// whose duration used to vanish on the way out, leaving the slot on the
// firmware default (7, 31), which the read side maps back to
// `lock_autorelock_end`: the operator asked for auto-relock to start and
// the door kept it switched off.
func TestSerializeSimpleScheduleWithDomainLock(t *testing.T) {
	t.Parallel()

	for action, want := range schedule.LockActionTable {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			entries := []hmapi.SimpleScheduleEntry{
				{
					SlotNo:     1,
					Weekdays:   []string{"MONDAY"},
					Time:       "07:30",
					LockMode:   "door_lock",
					LockAction: string(action),
				},
			}
			m, err := serializeSimpleScheduleWithDomain(entries, "lock", schedule.SimpleMaxSlot, nil)
			if err != nil {
				t.Fatalf("serializeSimpleScheduleWithDomain lock: %v", err)
			}
			base, baseSet := m["01_WP_DURATION_BASE"]
			factor, factorSet := m["01_WP_DURATION_FACTOR"]
			if !baseSet || !factorSet {
				t.Fatalf("%v: DURATION_BASE/FACTOR not written — the sparse merge leaves the device's stale pair in place", action)
			}
			if base != want.DurBase() || factor != want.DurFactor() {
				t.Errorf("%v: DURATION = (%v, %v), want (%d, %d)", action, base, factor, want.DurBase(), want.DurFactor())
			}
			if lvl := m["01_WP_LEVEL"]; lvl != want.Level() {
				t.Errorf("%v: LEVEL = %v, want %v", action, lvl, want.Level())
			}
		})
	}
}

func TestSerializeSimpleScheduleWithDomainError(t *testing.T) {
	t.Parallel()
	// slot_no 0 is out of range → must error
	entries := []hmapi.SimpleScheduleEntry{
		{SlotNo: 0, Weekdays: []string{"MONDAY"}, Time: "07:00"},
	}
	_, err := serializeSimpleScheduleWithDomain(entries, "switch", schedule.SimpleMaxSlot, nil)
	if err == nil {
		t.Fatal("expected error for slot_no=0")
	}
}

// ============================================================
// toFloat (link_profile.go)
// ============================================================

func TestToFloatFloat64(t *testing.T) {
	t.Parallel()
	v, ok := toFloat(3.14)
	if !ok || v != 3.14 {
		t.Errorf("toFloat float64 = (%v, %v), want (3.14, true)", v, ok)
	}
}

func TestToFloatFloat32(t *testing.T) {
	t.Parallel()
	_, ok := toFloat(float32(1.5))
	if !ok {
		t.Error("toFloat float32 must succeed")
	}
}

func TestToFloatInt(t *testing.T) {
	t.Parallel()
	v, ok := toFloat(5)
	if !ok || v != 5.0 {
		t.Errorf("toFloat int = (%v, %v), want (5.0, true)", v, ok)
	}
}

func TestToFloatInt32(t *testing.T) {
	t.Parallel()
	v, ok := toFloat(int32(2))
	if !ok || v != 2.0 {
		t.Errorf("toFloat int32 = (%v, %v)", v, ok)
	}
}

func TestToFloatInt64(t *testing.T) {
	t.Parallel()
	v, ok := toFloat(int64(7))
	if !ok || v != 7.0 {
		t.Errorf("toFloat int64 = (%v, %v)", v, ok)
	}
}

func TestToFloatBoolTrue(t *testing.T) {
	t.Parallel()
	v, ok := toFloat(true)
	if !ok || v != 1.0 {
		t.Errorf("toFloat true = (%v, %v), want (1.0, true)", v, ok)
	}
}

func TestToFloatBoolFalse(t *testing.T) {
	t.Parallel()
	v, ok := toFloat(false)
	if !ok || v != 0.0 {
		t.Errorf("toFloat false = (%v, %v), want (0.0, true)", v, ok)
	}
}

func TestToFloatString(t *testing.T) {
	t.Parallel()
	v, ok := toFloat("2.718")
	if !ok || v != 2.718 {
		t.Errorf("toFloat string = (%v, %v)", v, ok)
	}
}

func TestToFloatStringBad(t *testing.T) {
	t.Parallel()
	_, ok := toFloat("not-a-float")
	if ok {
		t.Error("toFloat bad string must return false")
	}
}

func TestToFloatUnsupported(t *testing.T) {
	t.Parallel()
	_, ok := toFloat(struct{}{})
	if ok {
		t.Error("toFloat struct must return false")
	}
}

// ============================================================
// peerChannelType nil registry / empty address
// ============================================================

func TestPeerChannelTypeNilRegistry(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	if got := a.peerChannelType("DEV001:1"); got != "" {
		t.Errorf("peerChannelType nil registry = %q, want empty", got)
	}
}

func TestPeerChannelTypeEmptyAddress(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{registry: central.NewRegistry()}
	if got := a.peerChannelType(""); got != "" {
		t.Errorf("peerChannelType empty = %q, want empty", got)
	}
}

func TestPeerChannelTypeNotFound(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{registry: central.NewRegistry()}
	if got := a.peerChannelType("NOSUCHDEV:1"); got != "" {
		t.Errorf("peerChannelType not found = %q, want empty", got)
	}
}

// ============================================================
// isWeekProfileChannel
// ============================================================

func TestIsWeekProfileChannelTrue(t *testing.T) {
	t.Parallel()
	cases := []string{"SWITCH_WEEK_PROFILE", "HEATING_WEEK_PROFILE", "WEEK_PROFILE"}
	for _, tc := range cases {
		if !isWeekProfileChannel(tc) {
			t.Errorf("isWeekProfileChannel(%q) = false, want true", tc)
		}
	}
}

func TestIsWeekProfileChannelFalse(t *testing.T) {
	t.Parallel()
	cases := []string{"SWITCH_VIRTUAL_RECEIVER", "DIMMER", ""}
	for _, tc := range cases {
		if isWeekProfileChannel(tc) {
			t.Errorf("isWeekProfileChannel(%q) = true, want false", tc)
		}
	}
}

// ============================================================
// SchedulesDomain.FindScheduleChannel nil-registry guard
// ============================================================

func TestSchedulesDomainFindScheduleChannelNilRegistry(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	_, err := s.FindScheduleChannel(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// decodeFloat (link_profile.go)
// ============================================================

func TestDecodeFloadEmptyRaw(t *testing.T) {
	t.Parallel()
	_, ok := decodeFloat(nil)
	if ok {
		t.Error("decodeFloat empty must return false")
	}
}

func TestDecodeFloatValid(t *testing.T) {
	t.Parallel()
	importJSON := []byte("3.14")
	v, ok := decodeFloat(importJSON)
	if !ok || v != 3.14 {
		t.Errorf("decodeFloat = (%v, %v), want (3.14, true)", v, ok)
	}
}

func TestDecodeFloatInvalid(t *testing.T) {
	t.Parallel()
	_, ok := decodeFloat([]byte(`"not-a-number"`))
	if ok {
		t.Error("decodeFloat non-number must return false")
	}
}
