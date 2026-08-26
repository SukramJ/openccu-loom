// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- SysvarDpSwitch ---

// TestSysvarDpSwitchIsOnTrue verifies that IsOn returns true when the
// underlying sysvar holds a boolean true value.
func TestSysvarDpSwitchIsOnTrue(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "sw"}, ValueType: hmenum.HubValueTypeLogic}
	sw := &SysvarDpSwitch{Sysvar: sv}
	sv.OnValue(hmtypes.BoolValue(true))
	if !sw.IsOn() {
		t.Fatal("IsOn() must return true when value is bool true")
	}
}

// TestSysvarDpSwitchIsOnFalse verifies that IsOn returns false when the
// underlying sysvar holds a boolean false value.
func TestSysvarDpSwitchIsOnFalse(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "sw"}, ValueType: hmenum.HubValueTypeLogic}
	sw := &SysvarDpSwitch{Sysvar: sv}
	sv.OnValue(hmtypes.BoolValue(false))
	if sw.IsOn() {
		t.Fatal("IsOn() must return false when value is bool false")
	}
}

// TestSysvarDpSwitchIsOnUnobserved verifies that IsOn returns false when
// no value has been observed yet.
func TestSysvarDpSwitchIsOnUnobserved(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "sw"}, ValueType: hmenum.HubValueTypeLogic}
	sw := &SysvarDpSwitch{Sysvar: sv}
	if sw.IsOn() {
		t.Fatal("IsOn() must return false when value has not been observed")
	}
}

// TestSysvarDpSwitchTurnOn verifies that TurnOn delegates to SetSysvar
// with value=true.
func TestSysvarDpSwitchTurnOn(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "sw", "", hmenum.HubValueTypeLogic, w)
	sw := &SysvarDpSwitch{Sysvar: sv}
	if err := sw.TurnOn(context.Background()); err != nil {
		t.Fatalf("TurnOn() unexpected error: %v", err)
	}
	got := w.last.Load()
	pair, ok := got.([2]any)
	if !ok {
		t.Fatal("writer was not called")
	}
	if pair[0] != "sw" {
		t.Errorf("writer name=%q want %q", pair[0], "sw")
	}
	if pair[1] != true {
		t.Errorf("writer value=%v want true", pair[1])
	}
}

// TestSysvarDpSwitchTurnOff verifies that TurnOff delegates to SetSysvar
// with value=false.
func TestSysvarDpSwitchTurnOff(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "sw", "", hmenum.HubValueTypeLogic, w)
	sw := &SysvarDpSwitch{Sysvar: sv}
	if err := sw.TurnOff(context.Background()); err != nil {
		t.Fatalf("TurnOff() unexpected error: %v", err)
	}
	got := w.last.Load()
	pair, ok := got.([2]any)
	if !ok {
		t.Fatal("writer was not called")
	}
	if pair[1] != false {
		t.Errorf("writer value=%v want false", pair[1])
	}
}

// --- SysvarDpBinarySensor ---

// TestSysvarDpBinarySensorIsOn verifies read-only bool extraction.
func TestSysvarDpBinarySensorIsOn(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "motion"}, ValueType: hmenum.HubValueTypeLogic}
	bs := &SysvarDpBinarySensor{Sysvar: sv}

	// before first observation
	if bs.IsOn() {
		t.Fatal("IsOn() must be false before first observation")
	}
	sv.OnValue(hmtypes.BoolValue(true))
	if !bs.IsOn() {
		t.Fatal("IsOn() must return true after true observation")
	}
	sv.OnValue(hmtypes.BoolValue(false))
	if bs.IsOn() {
		t.Fatal("IsOn() must return false after false observation")
	}
}

// TestSysvarDpBinarySensorIsReadOnly verifies that a BinarySensor has no
// writer (Writer == nil means no SetSysvar available).
func TestSysvarDpBinarySensorIsReadOnly(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "motion"}, ValueType: hmenum.HubValueTypeLogic}
	bs := &SysvarDpBinarySensor{Sysvar: sv}
	if bs.Writer != nil {
		t.Fatal("BinarySensor must be constructed without a writer")
	}
}

// --- SysvarDpText ---

// TestSysvarDpTextSetValueOK verifies that SetTextValue succeeds for a
// string within the default limit.
func TestSysvarDpTextSetValueOK(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "msg", "", hmenum.HubValueTypeString, w)
	txt := &SysvarDpText{Sysvar: sv}
	if err := txt.SetTextValue(context.Background(), "hello"); err != nil {
		t.Fatalf("SetTextValue() unexpected error: %v", err)
	}
}

// TestSysvarDpTextSetValueRespectsDefaultLength verifies that a string
// longer than 255 chars is rejected when no explicit MaxLength is set.
func TestSysvarDpTextSetValueRespectsDefaultLength(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "msg", "", hmenum.HubValueTypeString, w)
	txt := &SysvarDpText{Sysvar: sv}
	long := strings.Repeat("x", 256)
	if err := txt.SetTextValue(context.Background(), long); err == nil {
		t.Fatal("SetTextValue() must return error for 256-char string when MaxLength=0")
	}
}

// TestSysvarDpTextSetValueRespectsCustomLength verifies that an explicit
// MaxLength is enforced, rejecting strings that exceed it.
func TestSysvarDpTextSetValueRespectsCustomLength(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "tag", "", hmenum.HubValueTypeString, w)
	txt := &SysvarDpText{Sysvar: sv, MaxLength: 10}
	if err := txt.SetTextValue(context.Background(), "12345678901"); err == nil {
		t.Fatal("SetTextValue() must return error for string exceeding MaxLength=10")
	}
	// Exactly at limit must succeed.
	if err := txt.SetTextValue(context.Background(), "1234567890"); err != nil {
		t.Fatalf("SetTextValue() must accept string at MaxLength=10, got: %v", err)
	}
}

// TestSysvarDpTextValue verifies that TextValue returns the last stored
// string value.
func TestSysvarDpTextValue(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "msg"}, ValueType: hmenum.HubValueTypeString}
	txt := &SysvarDpText{Sysvar: sv}
	_, ok := txt.TextValue()
	if ok {
		t.Fatal("TextValue() must return ok=false before first observation")
	}
	sv.OnValue(hmtypes.StringValue("greet"))
	v, ok := txt.TextValue()
	if !ok {
		t.Fatal("TextValue() must return ok=true after observation")
	}
	if v != "greet" {
		t.Errorf("TextValue()=%q want %q", v, "greet")
	}
}

// --- ProgramDpButton ---

// TestProgramDpButtonPress verifies that Press delegates to the
// program's writer via Execute.
func TestProgramDpButtonPress(t *testing.T) {
	w := &stubProgram{}
	pg := NewProgram("c1", "prog-42", "Lights Off", "", false, w)
	btn := &ProgramDpButton{Program: pg}
	if err := btn.Press(context.Background()); err != nil {
		t.Fatalf("Press() unexpected error: %v", err)
	}
	if got := w.lastID.Load(); got != "prog-42" {
		t.Errorf("Press() called writer with id=%v want %q", got, "prog-42")
	}
}

// --- WrapSysvar factory ---

// TestWrapSysvarReturnsSwitch verifies that a writable logic sysvar is
// wrapped as SysvarDpSwitch.
func TestWrapSysvarReturnsSwitch(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "s", "", hmenum.HubValueTypeLogic, w)
	got := WrapSysvar(sv)
	if _, ok := got.(*SysvarDpSwitch); !ok {
		t.Fatalf("WrapSysvar() returned %T, want *SysvarDpSwitch", got)
	}
}

// TestWrapSysvarReturnsBinarySensor verifies that a read-only logic
// sysvar is wrapped as SysvarDpBinarySensor.
func TestWrapSysvarReturnsBinarySensor(t *testing.T) {
	sv := NewSysvar("c1", "motion", "", hmenum.HubValueTypeLogic, nil)
	got := WrapSysvar(sv)
	if _, ok := got.(*SysvarDpBinarySensor); !ok {
		t.Fatalf("WrapSysvar() returned %T, want *SysvarDpBinarySensor", got)
	}
}

// TestWrapSysvarReturnsText verifies that a string sysvar is wrapped as
// SysvarDpText.
func TestWrapSysvarReturnsText(t *testing.T) {
	sv := NewSysvar("c1", "msg", "", hmenum.HubValueTypeString, nil)
	got := WrapSysvar(sv)
	if _, ok := got.(*SysvarDpText); !ok {
		t.Fatalf("WrapSysvar() returned %T, want *SysvarDpText", got)
	}
}

// TestWrapSysvarReturnsBaseForUnknown verifies that a numeric (number,
// integer, float) sysvar is returned unchanged as the base *Sysvar.
func TestWrapSysvarReturnsBaseForUnknown(t *testing.T) {
	for _, vt := range []hmenum.HubValueType{
		hmenum.HubValueTypeNumber,
		hmenum.HubValueTypeInteger,
		hmenum.HubValueTypeFloat,
	} {
		sv := NewSysvar("c1", "n", "", vt, nil)
		got := WrapSysvar(sv)
		if _, ok := got.(*Sysvar); !ok {
			t.Errorf("WrapSysvar(%s) returned %T, want *Sysvar", vt, got)
		}
	}
}

// TestSysvarInternalIsRaceFreeAgainstAHubScan pins that the internal flag
// is read and written through the sysvar's own lock.
//
// Every hub scan rewrites the flag on the live objects north-bound
// listings are walking at the same time, so a reader that took the field
// directly raced the refresh — visible only under -race, and only when a
// listing happened to overlap a scan.
func TestSysvarInternalIsRaceFreeAgainstAHubScan(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "Presence", "", hmenum.HubValueTypeLogic, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 500 {
			sv.SetInternal(i%2 == 0)
		}
	}()
	for range 500 {
		_ = sv.Internal()
	}
	<-done

	sv.SetInternal(true)
	if !sv.Internal() {
		t.Error("Internal() did not report the value SetInternal wrote")
	}
}
