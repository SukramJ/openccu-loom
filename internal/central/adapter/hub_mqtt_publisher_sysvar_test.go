// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── hub.SysvarBoundAsFloat (the MQTT plane's numeric-bound conversion) ──────

func TestSysvarBoundAsFloat(t *testing.T) {
	t.Parallel()

	// nil pointer → nil
	if got := hub.SysvarBoundAsFloat(nil); got != nil {
		t.Fatalf("nil input: want nil, got %v", got)
	}

	// zero-value ParamValue (Kind=None) → nil
	none := hmtypes.NoneValue()
	if got := hub.SysvarBoundAsFloat(&none); got != nil {
		t.Fatalf("KindNone: want nil, got %v", got)
	}

	// Float kind
	fv := hmtypes.FloatValue(12.5)
	got := hub.SysvarBoundAsFloat(&fv)
	if got == nil {
		t.Fatal("Float kind: got nil, want *12.5")
	}
	if *got != 12.5 {
		t.Fatalf("Float kind: got %v, want 12.5", *got)
	}

	// Int kind — an INTEGER sysvar carries its bound here, not in .Float.
	iv := hmtypes.IntValue(42)
	got = hub.SysvarBoundAsFloat(&iv)
	if got == nil {
		t.Fatal("Int kind: got nil, want *42.0")
	}
	if *got != 42.0 {
		t.Fatalf("Int kind: got %v, want 42.0", *got)
	}

	// String kind → nil
	sv := hmtypes.StringValue("hello")
	if got := hub.SysvarBoundAsFloat(&sv); got != nil {
		t.Fatalf("String kind: want nil, got %v", got)
	}

	// Bool kind → nil
	bv := hmtypes.BoolValue(true)
	if got := hub.SysvarBoundAsFloat(&bv); got != nil {
		t.Fatalf("Bool kind: want nil, got %v", got)
	}

	// List kind → nil
	lv := hmtypes.ListValue([]string{"a", "b"})
	if got := hub.SysvarBoundAsFloat(&lv); got != nil {
		t.Fatalf("List kind: want nil, got %v", got)
	}
}

// ─── Fix 4: sysvarStateForMQTT ───────────────────────────────────────────────

func TestSysvarStateForMQTT(t *testing.T) {
	t.Parallel()

	// nil sysvar → returns raw unchanged
	raw := any("unchanged")
	if got := sysvarStateForMQTT(nil, raw); got != raw {
		t.Fatalf("nil sysvar: got %v, want %v", got, raw)
	}

	// sysvar with empty ValueList → returns raw unchanged
	svNoList := hub.NewSysvar("ccu-01", "Empty", "", hmenum.HubValueTypeList, nil)
	if got := sysvarStateForMQTT(svNoList, raw); got != raw {
		t.Fatalf("empty ValueList: got %v, want %v", got, raw)
	}

	// Build a sysvar with a ValueList for the remaining cases.
	svList := hub.NewSysvar("ccu-01", "Mode", "", hmenum.HubValueTypeList, nil)
	svList.ValueList = []string{"Aus", "Niedrig", "Normal", "Hoch"}

	// int=2 → "Normal"
	if got := sysvarStateForMQTT(svList, 2); got != "Normal" {
		t.Fatalf("int=2: got %v, want Normal", got)
	}

	// int=0 → "Aus"
	if got := sysvarStateForMQTT(svList, 0); got != "Aus" {
		t.Fatalf("int=0: got %v, want Aus", got)
	}

	// int=-1 → raw (out of range)
	if got := sysvarStateForMQTT(svList, -1); got != -1 {
		t.Fatalf("int=-1: got %v, want -1 (raw)", got)
	}

	// int=99 → raw (out of range)
	if got := sysvarStateForMQTT(svList, 99); got != 99 {
		t.Fatalf("int=99: got %v, want 99 (raw)", got)
	}

	// float64=1.0 → "Niedrig"
	if got := sysvarStateForMQTT(svList, float64(1.0)); got != "Niedrig" {
		t.Fatalf("float64=1.0: got %v, want Niedrig", got)
	}

	// int64=2 → "Normal"
	if got := sysvarStateForMQTT(svList, int64(2)); got != "Normal" {
		t.Fatalf("int64=2: got %v, want Normal", got)
	}

	// string "hello" → returned unchanged (not an index type)
	if got := sysvarStateForMQTT(svList, "hello"); got != "hello" {
		t.Fatalf("string raw: got %v, want hello", got)
	}
}

// TestSysvarStateForMQTT_ScannedListValueResolvesToLabel replays the
// production defect end-to-end across the two halves that must agree:
// the CCU scan delivers a LIST sysvar value as the quoted string "0";
// parseSysvarValue must type it as the integer index so
// sysvarStateForMQTT resolves the label the discovery's enum options
// advertise. When the parse half leaves the value a string, the state
// topic carries the raw "0" and HA rejects it against the options list
// ("got '0', allowed: Aus, …").
func TestSysvarStateForMQTT_ScannedListValueResolvesToLabel(t *testing.T) {
	t.Parallel()

	sv := hub.NewSysvar("ccu-01", "S_Alarm_System_Status", "", hmenum.HubValueTypeList, nil)
	sv.ValueList = []string{"Aus", "Aktivierung", "Hüllschutz", "Vollschutz", "Wiederholung"}

	pv, ok := parseSysvarValue(hmenum.HubValueTypeList, []byte(`"0"`))
	if !ok {
		t.Fatal("parseSysvarValue must accept the scanned list index")
	}
	sv.OnValue(pv)

	val, observed := sv.Value()
	if !observed {
		t.Fatal("sysvar must be observed after OnValue")
	}
	if got := sysvarStateForMQTT(sv, val.Unwrap()); got != "Aus" {
		t.Fatalf("state payload = %v, want label Aus", got)
	}
}
