// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// device_test.go — tests for DeviceDescription fields, helpers, and
// JSON serialisation: Subtype, ParentType, Direction, IsDevice/IsChannel,
// LinkSourceRoles, LinkTargetRoles, TeamChannels, ChannelNo.

package hmproto_test

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestDeviceDescriptionFirmwareUpdateState locks the wire key for the HmIP
// firmware-update lifecycle: it must decode from FIRMWARE_UPDATE_STATE (the
// field the installable-update gate reads), not FIRMWARE_STATE or UPDATE_STATE.
func TestDeviceDescriptionFirmwareUpdateState(t *testing.T) {
	t.Parallel()
	const raw = `{"ADDRESS":"ABC","AVAILABLE_FIRMWARE":"1.2.0","FIRMWARE":"1.0.0","FIRMWARE_UPDATE_STATE":"READY_FOR_UPDATE"}`
	var d hmproto.DeviceDescription
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if d.FirmwareUpdateState != "READY_FOR_UPDATE" {
		t.Fatalf("FirmwareUpdateState = %q, want READY_FOR_UPDATE", d.FirmwareUpdateState)
	}
	if d.AvailableFirmware != "1.2.0" {
		t.Fatalf("AvailableFirmware = %q, want 1.2.0", d.AvailableFirmware)
	}
}

// ---------------------------------------------------------------------------
// Subtype, ParentType, Direction fields
// ---------------------------------------------------------------------------

// TestDeviceDescriptionSubtypeField verifies that Subtype is present,
// marshals/unmarshals with the CCU wire key "SUBTYPE", and is omitted when
// empty.
func TestDeviceDescriptionSubtypeField(t *testing.T) {
	t.Parallel()

	d := hmproto.DeviceDescription{
		Address: "ABC",
		Subtype: "DIMMER",
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var out hmproto.DeviceDescription
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.Subtype != "DIMMER" {
		t.Errorf("Subtype=%q want DIMMER", out.Subtype)
	}

	// omitempty: empty Subtype must not appear in JSON
	empty := hmproto.DeviceDescription{Address: "X"}
	emptyData, _ := json.Marshal(empty)
	if contains(emptyData, "SUBTYPE") {
		t.Error("empty Subtype must be omitted from JSON (omitempty)")
	}
}

// TestDeviceDescriptionParentTypeField verifies that ParentType marshals as
// "PARENT_TYPE" and is omitted when empty.
func TestDeviceDescriptionParentTypeField(t *testing.T) {
	t.Parallel()

	d := hmproto.DeviceDescription{
		Address:    "ABC:1",
		Parent:     "ABC",
		ParentType: "HM-CC-RT-DN",
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out hmproto.DeviceDescription
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.ParentType != "HM-CC-RT-DN" {
		t.Errorf("ParentType=%q want HM-CC-RT-DN", out.ParentType)
	}

	empty := hmproto.DeviceDescription{Address: "Y"}
	emptyData, _ := json.Marshal(empty)
	if contains(emptyData, "PARENT_TYPE") {
		t.Error("empty ParentType must be omitted from JSON (omitempty)")
	}
}

// TestDeviceDescriptionDirectionField verifies that Direction marshals as
// "DIRECTION" (pointer), is nil by default, and round-trips correctly.
func TestDeviceDescriptionDirectionField(t *testing.T) {
	t.Parallel()

	dir := 2 // receiver
	d := hmproto.DeviceDescription{
		Address:   "ABC:1",
		Direction: &dir,
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out hmproto.DeviceDescription
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.Direction == nil {
		t.Fatal("Direction must not be nil after round-trip")
	}
	if *out.Direction != 2 {
		t.Errorf("Direction=%d want 2", *out.Direction)
	}

	// nil Direction must be absent in JSON (omitempty with pointer)
	noDir := hmproto.DeviceDescription{Address: "Z"}
	noDirData, _ := json.Marshal(noDir)
	if contains(noDirData, "DIRECTION") {
		t.Error("nil Direction must be omitted from JSON (omitempty)")
	}
}

// TestDeviceDescriptionIsDeviceIsChannel verifies that the IsDevice/IsChannel
// helpers work correctly.
func TestDeviceDescriptionIsDeviceIsChannel(t *testing.T) {
	t.Parallel()

	dev := hmproto.DeviceDescription{Address: "ABC", Subtype: "SWITCH"}
	if !dev.IsDevice() {
		t.Error("device with no Parent must be IsDevice()==true")
	}
	if dev.IsChannel() {
		t.Error("device with no Parent must be IsChannel()==false")
	}

	dir := 1
	ch := hmproto.DeviceDescription{Address: "ABC:1", Parent: "ABC", Direction: &dir}
	if ch.IsDevice() {
		t.Error("channel with Parent set must be IsDevice()==false")
	}
	if !ch.IsChannel() {
		t.Error("channel with Parent set must be IsChannel()==true")
	}
}

// ---------------------------------------------------------------------------
// LinkSourceRoles, LinkTargetRoles, TeamChannels
// ---------------------------------------------------------------------------

func TestDeviceDescriptionLinkRolesRoundTrip(t *testing.T) {
	t.Parallel()
	d := hmproto.DeviceDescription{
		Address:         "ABC:1",
		LinkSourceRoles: []string{"KEYMATIC", "SWITCH"},
		LinkTargetRoles: []string{"BLIND", "DIMMER"},
		TeamChannels:    []string{"ABC:1", "DEF:1"},
	}

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var out hmproto.DeviceDescription
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(out.LinkSourceRoles) != 2 {
		t.Errorf("LinkSourceRoles len=%d want 2", len(out.LinkSourceRoles))
	}
	if out.LinkSourceRoles[0] != "KEYMATIC" {
		t.Errorf("LinkSourceRoles[0]=%q want KEYMATIC", out.LinkSourceRoles[0])
	}
	if len(out.LinkTargetRoles) != 2 {
		t.Errorf("LinkTargetRoles len=%d want 2", len(out.LinkTargetRoles))
	}
	if out.LinkTargetRoles[1] != "DIMMER" {
		t.Errorf("LinkTargetRoles[1]=%q want DIMMER", out.LinkTargetRoles[1])
	}
	if len(out.TeamChannels) != 2 {
		t.Errorf("TeamChannels len=%d want 2", len(out.TeamChannels))
	}
}

// TestDeviceDescriptionLinkRolesStringDecoding verifies that the
// CCU's native LINK_*_ROLES wire format — a single space-separated
// string — round-trips through [hmproto.LinkRoles.UnmarshalJSON].
// Without the custom unmarshaller the snapshot pipeline failed with
// `json: cannot unmarshal string into Go struct field …`.
func TestDeviceDescriptionLinkRolesStringDecoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty string", `{"ADDRESS":"X","LINK_SOURCE_ROLES":""}`, nil},
		{"single token", `{"ADDRESS":"X","LINK_SOURCE_ROLES":"CONDITIONAL_SWITCH"}`, []string{"CONDITIONAL_SWITCH"}},
		{"two tokens", `{"ADDRESS":"X","LINK_SOURCE_ROLES":"KEYMATIC SWITCH"}`, []string{"KEYMATIC", "SWITCH"}},
		{"trim and dedupe whitespace", `{"ADDRESS":"X","LINK_SOURCE_ROLES":"  A   B  "}`, []string{"A", "B"}},
		{"array shape (snapshot capture)", `{"ADDRESS":"X","LINK_SOURCE_ROLES":["A","B"]}`, []string{"A", "B"}},
		{"null", `{"ADDRESS":"X","LINK_SOURCE_ROLES":null}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var d hmproto.DeviceDescription
			if err := json.Unmarshal([]byte(tc.in), &d); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if len(d.LinkSourceRoles) != len(tc.want) {
				t.Fatalf("LinkSourceRoles=%#v want %#v", d.LinkSourceRoles, tc.want)
			}
			for i, v := range tc.want {
				if d.LinkSourceRoles[i] != v {
					t.Errorf("LinkSourceRoles[%d]=%q want %q", i, d.LinkSourceRoles[i], v)
				}
			}
		})
	}
}

func TestDeviceDescriptionLinkRolesOmitEmpty(t *testing.T) {
	t.Parallel()
	d := hmproto.DeviceDescription{Address: "ABC"}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, field := range []string{"LINK_SOURCE_ROLES", "LINK_TARGET_ROLES", "TEAM_CHANNELS"} {
		if contains(raw, field) {
			t.Errorf("empty %s must be omitted from JSON (omitempty)", field)
		}
	}
}

// ---------------------------------------------------------------------------
// DeviceDescription.ChannelNo
// ---------------------------------------------------------------------------

func TestDeviceDescriptionChannelNo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr string
		want int
	}{
		{"VCU1234567:0", 0},
		{"VCU1234567:3", 3},
		{"VCU1234567:12", 12},
		{"VCU1234567", -1}, // device — no colon
		{"ABC:", -1},       // colon at end, no digit
		{"ABC:x", -1},      // non-digit suffix
		{"", -1},           // empty
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			t.Parallel()
			d := hmproto.DeviceDescription{Address: tc.addr}
			if got := d.ChannelNo(); got != tc.want {
				t.Errorf("ChannelNo()=%d want %d (addr=%q)", got, tc.want, tc.addr)
			}
		})
	}
}

func TestDeviceDescriptionChannelNoIsDeviceWhenNegative(t *testing.T) {
	t.Parallel()
	d := hmproto.DeviceDescription{Address: "VCU1234567"}
	if d.ChannelNo() >= 0 {
		t.Error("device address (no colon) must return ChannelNo < 0")
	}
}

// ---------------------------------------------------------------------------
// helper
// ---------------------------------------------------------------------------

func contains(data []byte, substr string) bool {
	needle := []byte(substr)
	for i := 0; i+len(needle) <= len(data); i++ {
		if string(data[i:i+len(needle)]) == substr {
			return true
		}
	}
	return false
}
