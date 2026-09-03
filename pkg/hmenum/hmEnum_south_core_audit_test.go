// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import (
	"slices"
	"testing"
)

// TestHmEnumFlagStickyIsFirmwareBit4 pins FLAGS bit 4 as the CCU
// firmware defines it.
//
// The rfd enum block gives the whole family as single bits:
// ../OpenCCU-Base/src/libhsscomm/HSSParameter.h:65-69 — FLG_VISIBLE=(1<<0),
// FLG_INTERNAL=(1<<1), FLG_TRANSFORM=(1<<2), FLG_SERVICE=(1<<3),
// FLG_STICKY=(1<<4). The accumulator that emits FLAGS onto the XML-RPC
// paramset description ORs those bits and nothing else
// (../OpenCCU-Base/src/libhsscomm/HSSParameter.cpp:82).
//
// The two concrete descriptor values below are the ones a decimal-10
// constant gets wrong in both directions. They are observed, not
// constructed: a census of the 129,395 captured paramset parameter
// descriptions in the reference descriptor corpus yields FLAGS ∈
// {0,1,2,3,5,9,11,25} and no 10 at all — 25 occurs 168 times and is
// carried only by UNREACH (156) and STICKY_UNREACH (12), the two
// parameters whose service message is documented not to self-clear.
func TestHmEnumFlagStickyIsFirmwareBit4(t *testing.T) {
	if FlagSticky != 16 {
		t.Fatalf("FlagSticky=%d, want 16 (FLG_STICKY=(1<<4), HSSParameter.h:69)", FlagSticky)
	}
	// FLAGS=25 = VISIBLE|SERVICE|STICKY — the HmIP UNREACH row.
	if !Flag(25).IsSticky() {
		t.Errorf("Flag(25).IsSticky()=false, want true (VISIBLE|SERVICE|STICKY)")
	}
	// FLAGS=11 = VISIBLE|INTERNAL|SERVICE — bit 4 clear.
	if Flag(11).IsSticky() {
		t.Errorf("Flag(11).IsSticky()=true, want false (bit 4 is clear)")
	}
}

// TestHmEnumEdgeTriggerParametersDeriveFromClickEvents pins the
// containment that used to be a second hand-written literal: every
// click-event parameter is an edge trigger, and the edge-trigger set
// adds exactly the two identity tokens CODE_ID and CODE_STATE.
//
// A press parameter added to [ClickEvents] alone would otherwise get a
// data point and a visibility entry but stay subject to the event
// coordinator's unchanged-value dedup, so a repeated keypad press would
// never reach the alarm intent router.
func TestHmEnumEdgeTriggerParametersDeriveFromClickEvents(t *testing.T) {
	for p := range ClickEvents {
		if _, ok := edgeTriggerParameters[p]; !ok {
			t.Errorf("click event %s is not an edge-trigger parameter", p)
		}
	}
	extra := make([]Parameter, 0, 2)
	for p := range edgeTriggerParameters {
		if _, ok := ClickEvents[p]; !ok {
			extra = append(extra, p)
		}
	}
	slices.Sort(extra)
	want := []Parameter{ParameterCodeID, ParameterCodeState}
	if !slices.Equal(extra, want) {
		t.Errorf("edge triggers beyond the click events = %v, want %v", extra, want)
	}
}

// TestHmEnumEventGroupIsAnExportedCategory pins the correction to
// [ValidationExemptDataPointCategories]'s doc: event_group is not withheld from
// the north-bound planes. It carries a DataPointType, and the MQTT
// discovery plane publishes it as a Home Assistant event component
// (internal/north/mqtt/category_component.go).
func TestHmEnumEventGroupIsAnExportedCategory(t *testing.T) {
	if _, ok := CategoryToType[DataPointCategoryEventGroup]; !ok {
		t.Fatalf("event_group has no DataPointType — the export ban would be real after all")
	}
	if _, ok := ValidationExemptDataPointCategories[DataPointCategoryEventGroup]; !ok {
		t.Fatalf("event_group left ValidationExemptDataPointCategories; the doc it pins needs rewriting")
	}
}

// TestHmEnumSmokeAlarmVocabularyMatchesDeviceDescriptor pins the four
// SMOKE_DETECTOR_ALARM_STATUS constants against the VALUE_LIST the
// device itself declares — member set and order.
//
// Source: the captured HmIP-SWSD paramset description in the reference
// descriptor corpus — channel VCU2822385:1, VALUES /
// SMOKE_DETECTOR_ALARM_STATUS, TYPE ENUM, DEFAULT "IDLE_OFF",
// VALUE_LIST ["IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM",
// "SECONDARY_ALARM"]; identical on the HmIP-SWSD-2 capture
// (VCU2098109:1). The list has four members: IDLE_ON appears in no
// captured descriptor and is not part of this vocabulary, so a fixture
// that carries it is describing a device that does not exist.
func TestHmEnumSmokeAlarmVocabularyMatchesDeviceDescriptor(t *testing.T) {
	declared := []SmokeDetectorAlarmStatus{
		SmokeDetectorAlarmStatusIdleOff,
		SmokeDetectorAlarmStatusPrimaryAlarm,
		SmokeDetectorAlarmStatusIntrusionAlarm,
		SmokeDetectorAlarmStatusSecondaryAlarm,
	}
	want := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}
	got := make([]string, 0, len(declared))
	for _, s := range declared {
		got = append(got, string(s))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("VALUE_LIST order = %v, want %v (HmIP-SWSD.json VCU2822385:1)", got, want)
	}
}
