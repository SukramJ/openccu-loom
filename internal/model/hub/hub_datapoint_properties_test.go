// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for HubDataPoint properties: Available, FullName, TranslationKey;
// AlarmMessages.AdditionalInformation; ServiceMessages.AdditionalInformation;
// ProgramDpButton.Available; SysvarDpBinarySensor.BoolValue.
package hub

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── HubDataPoint.Available ───────────────────────────────────────────

// TestHubDataPointAvailableFalseInitially verifies that Available returns
// false for a fresh HubDataPoint that has not yet received a confirmed value.
func TestHubDataPointAvailableFalseInitially(t *testing.T) {
	dp := HubDataPoint{Name: "test"}
	if dp.Available() {
		t.Fatal("Available() must be false on a fresh HubDataPoint (state uncertain)")
	}
}

// TestHubDataPointAvailableTrueAfterConfirm verifies that Available
// returns true after markCertain is called (first confirmed value).
func TestHubDataPointAvailableTrueAfterConfirm(t *testing.T) {
	dp := HubDataPoint{Name: "test"}
	dp.markCertain()
	if !dp.Available() {
		t.Fatal("Available() must be true after markCertain()")
	}
}

// TestSysvarAvailableTrueAfterFirstOnValue verifies that a Sysvar
// becomes available once OnValue is called (which clears state_uncertain).
func TestSysvarAvailableTrueAfterFirstOnValue(t *testing.T) {
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeLogic, nil)
	if sv.Available() {
		t.Fatal("fresh Sysvar must not be available before first value")
	}
	sv.OnValue(hmtypes.BoolValue(true))
	if !sv.Available() {
		t.Fatal("Sysvar must become available after OnValue")
	}
}

// ─── HubDataPoint.FullName ────────────────────────────────────────────

// TestHubDataPointFullNameEqualsName verifies that FullName returns the
// Name field for a base HubDataPoint.
func TestHubDataPointFullNameEqualsName(t *testing.T) {
	dp := HubDataPoint{Name: "Anwesenheit"}
	if got := dp.FullName(); got != "Anwesenheit" {
		t.Fatalf("FullName()=%q, want %q", got, "Anwesenheit")
	}
}

// TestSysvarFullName verifies that Sysvar.FullName delegates to Name.
func TestSysvarFullName(t *testing.T) {
	sv := NewSysvar("c1", "MyVariable", "", hmenum.HubValueTypeLogic, nil)
	if got := sv.FullName(); got != "MyVariable" {
		t.Fatalf("Sysvar.FullName()=%q, want MyVariable", got)
	}
}

// ─── HubDataPoint.TranslationKey ──────────────────────────────────────

// TestHubDataPointTranslationKeyEmptyByDefault verifies that the base
// HubDataPoint.TranslationKey returns "" (the generic hub DP has no
// HA-specific entity translation key by default).
func TestHubDataPointTranslationKeyEmptyByDefault(t *testing.T) {
	dp := HubDataPoint{Name: "x"}
	if got := dp.TranslationKey(); got != "" {
		t.Fatalf("TranslationKey()=%q, want empty string", got)
	}
}

// ─── AlarmMessages.AdditionalInformation ──────────────────────────────

// TestAlarmMessagesAdditionalInformationEmpty verifies that
// AdditionalInformation returns an empty (non-nil) slice when no
// alarms are registered.
func TestAlarmMessagesAdditionalInformationEmpty(t *testing.T) {
	a := NewAlarmMessages(nil)
	ai := a.AdditionalInformation()
	if ai == nil {
		t.Fatal("AdditionalInformation() must return non-nil slice")
	}
	if len(ai) != 0 {
		t.Fatalf("AdditionalInformation() len=%d, want 0 with no messages", len(ai))
	}
}

// TestAlarmMessagesAdditionalInformationContainsEntries verifies that
// AdditionalInformation returns one entry per alarm with the expected keys.
// An alarm entry has no device, channel or room (see [AlarmMessage]), so
// the map carries only identity, counter and timing fields.
func TestAlarmMessagesAdditionalInformationContainsEntries(t *testing.T) {
	a := NewAlarmMessages(nil)
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	a.Replace([]AlarmMessage{
		{
			ID:        "alarm-1",
			Name:      "Fenster offen",
			Counter:   3,
			Timestamp: ts,
		},
	})

	ai := a.AdditionalInformation()
	if len(ai) != 1 {
		t.Fatalf("AdditionalInformation() len=%d, want 1", len(ai))
	}
	entry := ai[0]
	if entry["id"] != "alarm-1" {
		t.Errorf("entry[id]=%v, want alarm-1", entry["id"])
	}
	if entry["name"] != "Fenster offen" {
		t.Errorf("entry[name]=%v, want Fenster offen", entry["name"])
	}
	if entry["counter"] != 3 {
		t.Errorf("entry[counter]=%v, want 3", entry["counter"])
	}
	if entry["timestamp"] != ts.Unix() {
		t.Errorf("entry[timestamp]=%v, want %d", entry["timestamp"], ts.Unix())
	}
	if _, ok := entry["last_timestamp"]; ok {
		t.Error("entry must NOT contain 'last_timestamp' key when LastTimestamp is zero")
	}
}

// TestAlarmMessagesAdditionalInformationLastTimestampIncluded verifies
// that last_timestamp is included (as Unix seconds) when non-zero.
func TestAlarmMessagesAdditionalInformationLastTimestampIncluded(t *testing.T) {
	a := NewAlarmMessages(nil)
	last := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	a.Replace([]AlarmMessage{
		{
			ID:            "alarm-2",
			LastTimestamp: last,
		},
	})
	ai := a.AdditionalInformation()
	if len(ai) != 1 {
		t.Fatalf("AdditionalInformation() len=%d, want 1", len(ai))
	}
	got, ok := ai[0]["last_timestamp"]
	if !ok {
		t.Fatal("entry must contain 'last_timestamp' key when LastTimestamp is non-zero")
	}
	if got != last.Unix() {
		t.Errorf("entry[last_timestamp]=%v, want %d", got, last.Unix())
	}
}

// ─── ServiceMessages.AdditionalInformation ────────────────────────────

// TestServiceMessagesAdditionalInformationEmpty verifies that
// AdditionalInformation returns an empty non-nil slice when no messages
// are registered.
func TestServiceMessagesAdditionalInformationEmpty(t *testing.T) {
	s := NewServiceMessages(nil)
	ai := s.AdditionalInformation()
	if ai == nil {
		t.Fatal("AdditionalInformation() must return non-nil slice")
	}
	if len(ai) != 0 {
		t.Fatalf("AdditionalInformation() len=%d, want 0", len(ai))
	}
}

// TestServiceMessagesAdditionalInformationContainsEntries verifies
// that AdditionalInformation returns one map per service message with
// the expected fields, including rooms/functions and last_timestamp
// when the CCU reported them.
func TestServiceMessagesAdditionalInformationContainsEntries(t *testing.T) {
	s := NewServiceMessages(nil)
	ts := time.Date(2026, 4, 15, 8, 30, 0, 0, time.UTC)
	last := time.Date(2026, 4, 16, 9, 0, 0, 0, time.UTC)
	s.Replace([]ServiceMessage{
		{
			ID:            "sm-1",
			Name:          "Low Battery",
			Address:       "HEQ0123456:0",
			DeviceName:    "Motion Sensor",
			Type:          hmenum.ServiceMessageTypeGeneric,
			Quittable:     true,
			Counter:       1,
			Timestamp:     ts,
			LastTimestamp: last,
			Rooms:         []string{"Living Room"},
			Functions:     []string{"Light", "Security"},
		},
	})

	ai := s.AdditionalInformation()
	if len(ai) != 1 {
		t.Fatalf("AdditionalInformation() len=%d, want 1", len(ai))
	}
	entry := ai[0]
	if entry["id"] != "sm-1" {
		t.Errorf("entry[id]=%v, want sm-1", entry["id"])
	}
	if entry["quittable"] != true {
		t.Errorf("entry[quittable]=%v, want true", entry["quittable"])
	}
	if entry["timestamp"] != ts.Unix() {
		t.Errorf("entry[timestamp]=%v, want %d", entry["timestamp"], ts.Unix())
	}
	lastTS, ok := entry["last_timestamp"]
	if !ok {
		t.Fatal("entry must contain 'last_timestamp' when LastTimestamp is non-zero")
	}
	if lastTS != last.Unix() {
		t.Errorf("entry[last_timestamp]=%v, want %d", lastTS, last.Unix())
	}
	rooms, ok := entry["rooms"].([]string)
	if !ok || len(rooms) != 1 || rooms[0] != "Living Room" {
		t.Errorf("entry[rooms]=%v, want [Living Room]", entry["rooms"])
	}
	fns, ok := entry["functions"].([]string)
	if !ok || len(fns) != 2 {
		t.Errorf("entry[functions]=%v, want 2 entries", entry["functions"])
	}
}

// TestServiceMessagesAdditionalInformationLastTimestampOmittedWhenZero
// verifies that the last_timestamp key is omitted when LastTimestamp is
// the Go zero time — the CCU's "never recurred" state, mirroring
// [AlarmMessages.AdditionalInformation].
func TestServiceMessagesAdditionalInformationLastTimestampOmittedWhenZero(t *testing.T) {
	s := NewServiceMessages(nil)
	s.Replace([]ServiceMessage{{ID: "sm-2"}})
	ai := s.AdditionalInformation()
	if len(ai) != 1 {
		t.Fatalf("AdditionalInformation() len=%d, want 1", len(ai))
	}
	if _, ok := ai[0]["last_timestamp"]; ok {
		t.Error("entry must NOT contain 'last_timestamp' key when LastTimestamp is zero")
	}
	if _, ok := ai[0]["rooms"]; ok {
		t.Error("entry must NOT contain 'rooms' key when Rooms is empty")
	}
	if _, ok := ai[0]["functions"]; ok {
		t.Error("entry must NOT contain 'functions' key when Functions is empty")
	}
}

// ─── ProgramDpButton.Available ────────────────────────────────────────

// TestProgramDpButtonAvailableReturnsFalseBeforeObservation verifies
// that Available returns false on a fresh program that has not yet
// been observed.
func TestProgramDpButtonAvailableReturnsFalseBeforeObservation(t *testing.T) {
	pg := NewProgram("c1", "p1", "Evening", "", false, nil)
	btn := &ProgramDpButton{Program: pg}
	if btn.Available() {
		t.Fatal("Available() must be false before first observation")
	}
}

// TestProgramDpButtonAvailableReturnsTrueWhenActive verifies that
// Available returns true when the program has been observed as active.
func TestProgramDpButtonAvailableReturnsTrueWhenActive(t *testing.T) {
	pg := NewProgram("c1", "p1", "Evening", "", false, nil)
	pg.OnActive(true)
	btn := &ProgramDpButton{Program: pg}
	if !btn.Available() {
		t.Fatal("Available() must be true when program is active and observed")
	}
}

// TestProgramDpButtonAvailableReturnsFalseWhenInactive verifies that
// Available returns false when the program is inactive (mirrors
// Available → False when not active).
func TestProgramDpButtonAvailableReturnsFalseWhenInactive(t *testing.T) {
	pg := NewProgram("c1", "p1", "Evening", "", false, nil)
	pg.OnActive(false)
	btn := &ProgramDpButton{Program: pg}
	if btn.Available() {
		t.Fatal("Available() must be false when program is inactive")
	}
}

// ─── SysvarDpBinarySensor.BoolValue ───────────────────────────────────

// TestSysvarDpBinarySensorBoolValueUnobserved verifies that BoolValue
// returns (false, false) before the first observation — mirroring
// Python's None return for an unobserved sysvar.
func TestSysvarDpBinarySensorBoolValueUnobserved(t *testing.T) {
	sv := NewSysvar("c1", "motion", "", hmenum.HubValueTypeLogic, nil)
	bs := &SysvarDpBinarySensor{Sysvar: sv}
	val, ok := bs.BoolValue()
	if ok {
		t.Fatal("BoolValue() ok must be false before first observation")
	}
	if val {
		t.Fatal("BoolValue() value must be false before first observation")
	}
}

// TestSysvarDpBinarySensorBoolValueTrue verifies that BoolValue
// returns (true, true) after a true observation.
func TestSysvarDpBinarySensorBoolValueTrue(t *testing.T) {
	sv := NewSysvar("c1", "motion", "", hmenum.HubValueTypeLogic, nil)
	bs := &SysvarDpBinarySensor{Sysvar: sv}
	sv.OnValue(hmtypes.BoolValue(true))
	val, ok := bs.BoolValue()
	if !ok {
		t.Fatal("BoolValue() ok must be true after observation")
	}
	if !val {
		t.Fatal("BoolValue() value must be true after true observation")
	}
}

// TestSysvarDpBinarySensorBoolValueFalse verifies that BoolValue
// returns (false, true) after a false observation.
func TestSysvarDpBinarySensorBoolValueFalse(t *testing.T) {
	sv := NewSysvar("c1", "motion", "", hmenum.HubValueTypeLogic, nil)
	bs := &SysvarDpBinarySensor{Sysvar: sv}
	sv.OnValue(hmtypes.BoolValue(false))
	val, ok := bs.BoolValue()
	if !ok {
		t.Fatal("BoolValue() ok must be true after observation (even false value)")
	}
	if val {
		t.Fatal("BoolValue() value must be false after false observation")
	}
}

// TestSysvarDpBinarySensorBoolValueNonBoolKind verifies that BoolValue
// returns (false, false) when the stored value is not a bool.
func TestSysvarDpBinarySensorBoolValueNonBoolKind(t *testing.T) {
	sv := NewSysvar("c1", "count", "", hmenum.HubValueTypeInteger, nil)
	bs := &SysvarDpBinarySensor{Sysvar: sv}
	sv.OnValue(hmtypes.IntValue(5))
	val, ok := bs.BoolValue()
	if ok {
		t.Fatal("BoolValue() ok must be false for non-bool value")
	}
	if val {
		t.Fatal("BoolValue() value must be false for non-bool value")
	}
}

// ─── stub for ProgramDpButton tests ─────────────────────────────────────────

// stub types (stubInstall, stubSysvar, stubProgram) are defined in hub_test.go.
// context is imported here for ProgramDpButton.Press call.
var _ = context.Background // ensure context import is used
