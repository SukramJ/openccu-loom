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
func TestAlarmMessagesAdditionalInformationContainsEntries(t *testing.T) {
	a := NewAlarmMessages(nil)
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	a.Replace([]AlarmMessage{
		{
			ID:         "alarm-1",
			Name:       "Fenster offen",
			Address:    "HEQ0123456:1",
			DeviceName: "Window Sensor",
			StateValue: "OPEN",
			Counter:    3,
			Timestamp:  ts,
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
	if entry["address"] != "HEQ0123456:1" {
		t.Errorf("entry[address]=%v, want HEQ0123456:1", entry["address"])
	}
	if entry["state_value"] != "OPEN" {
		t.Errorf("entry[state_value]=%v, want OPEN", entry["state_value"])
	}
	if entry["counter"] != 3 {
		t.Errorf("entry[counter]=%v, want 3", entry["counter"])
	}
	if entry["timestamp"] != ts.Unix() {
		t.Errorf("entry[timestamp]=%v, want %d", entry["timestamp"], ts.Unix())
	}
}

// TestAlarmMessagesAdditionalInformationRoomsIncluded verifies that
// rooms are included in the entry when non-empty.
func TestAlarmMessagesAdditionalInformationRoomsIncluded(t *testing.T) {
	a := NewAlarmMessages(nil)
	a.Replace([]AlarmMessage{
		{
			ID:    "alarm-2",
			Rooms: []string{"Wohnzimmer", "Küche"},
		},
	})
	ai := a.AdditionalInformation()
	if len(ai) != 1 {
		t.Fatalf("AdditionalInformation() len=%d, want 1", len(ai))
	}
	rooms, ok := ai[0]["rooms"]
	if !ok {
		t.Fatal("entry must contain 'rooms' key when Rooms is non-empty")
	}
	if len(rooms.([]string)) != 2 {
		t.Errorf("entry[rooms] len=%d, want 2", len(rooms.([]string)))
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
// the expected fields.
func TestServiceMessagesAdditionalInformationContainsEntries(t *testing.T) {
	s := NewServiceMessages(nil)
	ts := time.Date(2026, 4, 15, 8, 30, 0, 0, time.UTC)
	s.Replace([]ServiceMessage{
		{
			ID:          "sm-1",
			Name:        "Low Battery",
			Address:     "HEQ0123456:0",
			DeviceName:  "Motion Sensor",
			Type:        hmenum.ServiceMessageTypeGeneric,
			Priority:    0,
			Quittable:   true,
			Counter:     1,
			Timestamp:   ts,
			Description: "Battery level below threshold",
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
	desc, ok := entry["description"]
	if !ok {
		t.Fatal("entry must contain 'description' when description is non-empty")
	}
	if desc != "Battery level below threshold" {
		t.Errorf("entry[description]=%v, want expected text", desc)
	}
}

// TestServiceMessagesAdditionalInformationDescriptionOmittedWhenEmpty
// verifies that the description key is omitted when description is "".
func TestServiceMessagesAdditionalInformationDescriptionOmittedWhenEmpty(t *testing.T) {
	s := NewServiceMessages(nil)
	s.Replace([]ServiceMessage{{ID: "sm-2", Description: ""}})
	ai := s.AdditionalInformation()
	if len(ai) != 1 {
		t.Fatalf("AdditionalInformation() len=%d, want 1", len(ai))
	}
	if _, ok := ai[0]["description"]; ok {
		t.Error("entry must NOT contain 'description' key when description is empty")
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
