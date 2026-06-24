// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// schedules_pure_test.go covers the pure-logic helpers in schedules.go
// (no registry / CCU required).

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ============================================================
// formatMinutes tests
// ============================================================

func TestFormatMinutesZero(t *testing.T) {
	t.Parallel()
	if got := formatMinutes(0); got != "00:00" {
		t.Errorf("formatMinutes(0) = %q, want 00:00", got)
	}
}

func TestFormatMinutesNormal(t *testing.T) {
	t.Parallel()
	if got := formatMinutes(90); got != "01:30" {
		t.Errorf("formatMinutes(90) = %q, want 01:30", got)
	}
}

func TestFormatMinutesMaxClamped(t *testing.T) {
	t.Parallel()
	// 24*60 = 1440
	if got := formatMinutes(24 * 60); got != "24:00" {
		t.Errorf("formatMinutes(1440) = %q, want 24:00", got)
	}
}

func TestFormatMinutesOverflowClamped(t *testing.T) {
	t.Parallel()
	got := formatMinutes(9999)
	if got != "24:00" {
		t.Errorf("formatMinutes(9999) = %q, want 24:00 (clamped)", got)
	}
}

func TestFormatMinutesNegativeClamped(t *testing.T) {
	t.Parallel()
	got := formatMinutes(-5)
	if got != "00:00" {
		t.Errorf("formatMinutes(-5) = %q, want 00:00 (clamped)", got)
	}
}

// ============================================================
// minutesFromTime tests
// ============================================================

func TestMinutesFromTimeNormal(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("08:30"); got != 510 {
		t.Errorf("minutesFromTime(08:30) = %d, want 510", got)
	}
}

func TestMinutesFromTimeMidnight(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("24:00"); got != 1440 {
		t.Errorf("minutesFromTime(24:00) = %d, want 1440", got)
	}
}

func TestMinutesFromTimeZero(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("00:00"); got != 0 {
		t.Errorf("minutesFromTime(00:00) = %d, want 0", got)
	}
}

func TestMinutesFromTimeNoColon(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("0830"); got != -1 {
		t.Errorf("minutesFromTime(0830) = %d, want -1", got)
	}
}

func TestMinutesFromTimeBadHour(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("xx:30"); got != -1 {
		t.Errorf("minutesFromTime(xx:30) = %d, want -1", got)
	}
}

func TestMinutesFromTimeBadMinute(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("08:xx"); got != -1 {
		t.Errorf("minutesFromTime(08:xx) = %d, want -1", got)
	}
}

func TestMinutesFromTimeOutOfRange(t *testing.T) {
	t.Parallel()
	if got := minutesFromTime("25:00"); got != -1 {
		t.Errorf("minutesFromTime(25:00) = %d, want -1", got)
	}
}

// ============================================================
// hasSimpleScheduleParams tests
// ============================================================

func TestHasSimpleScheduleParamsTrue(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"01_WP_WEEKDAY":      0,
		"01_WP_LEVEL":        0.5,
		"01_WP_FIXED_HOUR":   8,
		"01_WP_FIXED_MINUTE": 0,
	}
	if !hasSimpleScheduleParams(raw) {
		t.Error("map with WP_ keys must return true")
	}
}

func TestHasSimpleScheduleParamsFalse(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"P1_ENDTIME_MONDAY_1":     "08:00",
		"P1_TEMPERATURE_MONDAY_1": 21.0,
	}
	if hasSimpleScheduleParams(raw) {
		t.Error("climate schedule keys must not match simple pattern")
	}
}

func TestHasSimpleScheduleParamsEmpty(t *testing.T) {
	t.Parallel()
	if hasSimpleScheduleParams(map[string]any{}) {
		t.Error("empty map must return false")
	}
}

// ============================================================
// domainFromWeekProfileType tests
// ============================================================

func TestDomainFromWeekProfileTypeSwitch(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("SWITCH_WEEK_PROFILE"); got != "switch" {
		t.Errorf("SWITCH_WEEK_PROFILE → %q, want switch", got)
	}
}

func TestDomainFromWeekProfileTypeDimmer(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("DIMMER_WEEK_PROFILE"); got != "light" {
		t.Errorf("DIMMER_WEEK_PROFILE → %q, want light", got)
	}
}

func TestDomainFromWeekProfileTypeLightWeek(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("LIGHT_WEEK_PROFILE"); got != "light" {
		t.Errorf("LIGHT_WEEK_PROFILE → %q, want light", got)
	}
}

func TestDomainFromWeekProfileTypeBlind(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("BLIND_WEEK_PROFILE"); got != "cover" {
		t.Errorf("BLIND_WEEK_PROFILE → %q, want cover", got)
	}
}

func TestDomainFromWeekProfileTypeShutter(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("SHUTTER_WEEK_PROFILE"); got != "cover" {
		t.Errorf("SHUTTER_WEEK_PROFILE → %q, want cover", got)
	}
}

func TestDomainFromWeekProfileTypeLock(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("LOCK_WEEK_PROFILE"); got != "lock" {
		t.Errorf("LOCK_WEEK_PROFILE → %q, want lock", got)
	}
}

func TestDomainFromWeekProfileTypeDoorLock(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("DOOR_LOCK_WEEK_PROFILE"); got != "lock" {
		t.Errorf("DOOR_LOCK_WEEK_PROFILE → %q, want lock", got)
	}
}

func TestDomainFromWeekProfileTypeValve(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("VALVE_WEEK_PROFILE"); got != "valve" {
		t.Errorf("VALVE_WEEK_PROFILE → %q, want valve", got)
	}
}

func TestDomainFromWeekProfileTypeWater(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("WATER_WEEK_PROFILE"); got != "valve" {
		t.Errorf("WATER_WEEK_PROFILE → %q, want valve", got)
	}
}

func TestDomainFromWeekProfileTypeHeating(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("HEATING_WEEK_PROFILE"); got != "climate" {
		t.Errorf("HEATING_WEEK_PROFILE → %q, want climate", got)
	}
}

func TestDomainFromWeekProfileTypeClimatecontrol(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("CLIMATECONTROL_WEEK_PROFILE"); got != "climate" {
		t.Errorf("CLIMATECONTROL_WEEK_PROFILE → %q, want climate", got)
	}
}

func TestDomainFromWeekProfileTypeUnknown(t *testing.T) {
	t.Parallel()
	if got := domainFromWeekProfileType("UNKNOWN_THING"); got != "" {
		t.Errorf("unknown type → %q, want empty", got)
	}
}

// ============================================================
// domainFromActorType tests
// ============================================================

func TestDomainFromActorTypeSwitch(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("SWITCH_VIRTUAL_RECEIVER"); got != "switch" {
		t.Errorf("SWITCH_VIRTUAL → %q, want switch", got)
	}
}

func TestDomainFromActorTypeEnergieMeter(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("ENERGIE_METER_TRANSMITTER"); got != "switch" {
		t.Errorf("ENERGIE_METER → %q, want switch", got)
	}
}

func TestDomainFromActorTypeDimmer(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("DIMMER_VIRTUAL_RECEIVER"); got != "light" {
		t.Errorf("DIMMER_VIRTUAL → %q, want light", got)
	}
}

func TestDomainFromActorTypeLight(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("LIGHT_VIRTUAL_RECEIVER"); got != "light" {
		t.Errorf("LIGHT_VIRTUAL → %q, want light", got)
	}
}

func TestDomainFromActorTypeOpticalSignal(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("OPTICAL_SIGNAL_RECEIVER"); got != "light" {
		t.Errorf("OPTICAL_SIGNAL → %q, want light", got)
	}
}

func TestDomainFromActorTypeBlind(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("BLIND_VIRTUAL_RECEIVER"); got != "cover" {
		t.Errorf("BLIND_VIRTUAL → %q, want cover", got)
	}
}

func TestDomainFromActorTypeShutter(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("SHUTTER_VIRTUAL_RECEIVER"); got != "cover" {
		t.Errorf("SHUTTER_VIRTUAL → %q, want cover", got)
	}
}

func TestDomainFromActorTypeDoorLock(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("DOOR_LOCK_TRANSMITTER"); got != "lock" {
		t.Errorf("DOOR_LOCK → %q, want lock", got)
	}
}

func TestDomainFromActorTypeKeymatic(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("KEYMATIC_TRANSMITTER"); got != "lock" {
		t.Errorf("KEYMATIC → %q, want lock", got)
	}
}

func TestDomainFromActorTypeWater(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("WATER_TRANSMITTER"); got != "valve" {
		t.Errorf("WATER → %q, want valve", got)
	}
}

func TestDomainFromActorTypeValve(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("VALVE_RECEIVER"); got != "valve" {
		t.Errorf("VALVE → %q, want valve", got)
	}
}

func TestDomainFromActorTypeUnknown(t *testing.T) {
	t.Parallel()
	if got := domainFromActorType("SENSOR_TRANSMITTER"); got != "" {
		t.Errorf("unknown actor type → %q, want empty", got)
	}
}

// ============================================================
// detectLockMode tests
// ============================================================

func TestDetectLockModeDoorLock(t *testing.T) {
	t.Parallel()
	if got := detectLockMode([]string{"1_LOCK", "2_UNLOCK"}); got != "door_lock" {
		t.Errorf("detectLockMode([1_LOCK,...]) = %q, want door_lock", got)
	}
}

func TestDetectLockModeUserPermission(t *testing.T) {
	t.Parallel()
	if got := detectLockMode([]string{"2_LOCK", "3_UNLOCK"}); got != "user_permission" {
		t.Errorf("detectLockMode([2_...]) = %q, want user_permission", got)
	}
}

func TestDetectLockModeEmpty(t *testing.T) {
	t.Parallel()
	if got := detectLockMode(nil); got != "user_permission" {
		t.Errorf("detectLockMode(nil) = %q, want user_permission", got)
	}
}

// ============================================================
// detectLockAction tests
// ============================================================

func TestDetectLockActionKnownLock(t *testing.T) {
	t.Parallel()
	// Round-trip: every entry in the canonical table must survive
	// encode → detect without loss.
	for action, raw := range schedule.LockActionTable {
		name := string(action)
		got := detectLockAction(raw.Level(), raw.DurBase(), raw.DurFactor())
		if got != name {
			t.Errorf("detectLockAction round-trip(%q): got %q", name, got)
		}
	}
}

func TestDetectLockActionFallback(t *testing.T) {
	t.Parallel()
	// Unknown combination falls back to "lock_autorelock_start" (zero-value).
	got := detectLockAction(999.0, 999, 999)
	if got != "lock_autorelock_start" {
		t.Errorf("unknown combination → %q, want lock_autorelock_start", got)
	}
}

// ============================================================
// detectLockPermission tests
// ============================================================

func TestDetectLockPermissionGranted(t *testing.T) {
	t.Parallel()
	if got := detectLockPermission(1.0); got != "granted" {
		t.Errorf("level=1.0 → %q, want granted", got)
	}
}

func TestDetectLockPermissionBoundary(t *testing.T) {
	t.Parallel()
	if got := detectLockPermission(0.5); got != "granted" {
		t.Errorf("level=0.5 → %q, want granted", got)
	}
}

func TestDetectLockPermissionNotGranted(t *testing.T) {
	t.Parallel()
	if got := detectLockPermission(0.0); got != "not_granted" {
		t.Errorf("level=0.0 → %q, want not_granted", got)
	}
}

func TestDetectLockPermissionBelowBoundary(t *testing.T) {
	t.Parallel()
	if got := detectLockPermission(0.4); got != "not_granted" {
		t.Errorf("level=0.4 → %q, want not_granted", got)
	}
}

// ============================================================
// labels.go: ParameterLabelAdapter and MqttParameterLabelAdapter
// ============================================================

func TestParameterLabelAdapterNilTranslations(t *testing.T) {
	t.Parallel()
	a := NewParameterLabelAdapter(nil, "en")
	if got := a.ParameterLabel("LEVEL"); got != "" {
		t.Errorf("nil translations → %q, want empty", got)
	}
}

func TestParameterLabelAdapterNilReceiver(t *testing.T) {
	t.Parallel()
	var a *ParameterLabelAdapter
	// Must not panic.
	if got := a.ParameterLabel("LEVEL"); got != "" {
		t.Errorf("nil receiver → %q, want empty", got)
	}
}

func TestChannelTypedParameterLabelNil(t *testing.T) {
	t.Parallel()
	var a *ParameterLabelAdapter
	if got := a.ChannelTypedParameterLabel("DIMMER", "LEVEL"); got != "" {
		t.Errorf("nil receiver → %q, want empty", got)
	}
}

func TestChannelTypeLabelNil(t *testing.T) {
	t.Parallel()
	var a *ParameterLabelAdapter
	if got := a.ChannelTypeLabel("DIMMER"); got != "" {
		t.Errorf("nil receiver → %q, want empty", got)
	}
}

func TestMqttParameterLabelAdapterNilInner(t *testing.T) {
	t.Parallel()
	a := NewMqttParameterLabelAdapter(nil)
	if got := a.ParameterLabel("DIMMER", "LEVEL"); got != "" {
		t.Errorf("nil inner → %q, want empty", got)
	}
}

func TestMqttParameterLabelAdapterNilReceiver(t *testing.T) {
	t.Parallel()
	var a *MqttParameterLabelAdapter
	if got := a.ParameterLabel("DIMMER", "LEVEL"); got != "" {
		t.Errorf("nil receiver → %q, want empty", got)
	}
}

// ============================================================
// stubs.go: MVP stubs
// ============================================================

func TestParamsetsAdapterGetParamsetReturnsUnimplemented(t *testing.T) {
	t.Parallel()
	a := NewParamsetsAdapter()
	_, err := a.GetParamset(context.Background(), "addr:1", hmenum.ParamsetKeyValues)
	if err == nil {
		t.Fatal("expected ErrUnimplemented")
	}
}

func TestParamsetsAdapterPutParamsetReturnsUnimplemented(t *testing.T) {
	t.Parallel()
	a := NewParamsetsAdapter()
	err := a.PutParamset(context.Background(), "addr:1", hmenum.ParamsetKeyValues, nil)
	if err == nil {
		t.Fatal("expected ErrUnimplemented")
	}
}

func TestIncidentsAdapterReturnsNil(t *testing.T) {
	t.Parallel()
	a := NewIncidentsAdapter()
	if got := a.Incidents(); got != nil {
		t.Errorf("Incidents() = %v, want nil", got)
	}
}

func TestBackupAdapterNilRegistryTrigger(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(nil)
	_, err := a.TriggerBackup(context.Background())
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestBackupAdapterNilStorageList(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(nil)
	entries, err := a.List(context.Background())
	if err != nil || entries != nil {
		t.Errorf("List with nil storage = (%v, %v), want (nil, nil)", entries, err)
	}
}

func TestBackupAdapterNilStorageRestore(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(nil)
	_, err := a.Restore(context.Background(), "backup_id")
	if err == nil {
		t.Fatal("expected error (ErrRestoreUnsupported)")
	}
}
