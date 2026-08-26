// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests covering the Security & Safety domain's identity currency:
// SecuritySourceRef construction/derivation and the AlarmBlockerReason
// enum.

package hmevent_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ---------------------------------------------------------------------------
// SecurityRefKey / NewSecuritySourceRef
// ---------------------------------------------------------------------------

func TestSecurityRefKey_Format(t *testing.T) {
	t.Parallel()
	got := hmevent.SecurityRefKey("ccu-main", "HmIP-RF", "ABC0123456:1", "STATE")
	want := "ccu-main|HmIP-RF|ABC0123456:1|STATE"
	if got != want {
		t.Fatalf("SecurityRefKey() = %q, want %q", got, want)
	}
}

func TestNewSecuritySourceRef_DerivesRefAndDeviceAddress(t *testing.T) {
	t.Parallel()
	ref := hmevent.NewSecuritySourceRef("ccu-main", "HmIP-RF", "ABC0123456:1", "STATE")

	wantRef := hmevent.SecurityRefKey("ccu-main", "HmIP-RF", "ABC0123456:1", "STATE")
	if ref.Ref != wantRef {
		t.Errorf("Ref = %q, want %q", ref.Ref, wantRef)
	}
	if ref.Central != "ccu-main" {
		t.Errorf("Central = %q, want ccu-main", ref.Central)
	}
	if ref.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID = %q, want HmIP-RF", ref.InterfaceID)
	}
	if ref.ChannelAddress != "ABC0123456:1" {
		t.Errorf("ChannelAddress = %q, want ABC0123456:1", ref.ChannelAddress)
	}
	if ref.DeviceAddress != "ABC0123456" {
		t.Errorf("DeviceAddress = %q, want ABC0123456", ref.DeviceAddress)
	}
	if ref.Parameter != "STATE" {
		t.Errorf("Parameter = %q, want STATE", ref.Parameter)
	}
	// Fields NewSecuritySourceRef does not derive stay at their zero
	// value; callers fill them in when the source is an enrolled
	// sensor.
	if ref.SensorID != "" {
		t.Errorf("SensorID = %q, want empty", ref.SensorID)
	}
	if ref.Name != "" {
		t.Errorf("Name = %q, want empty", ref.Name)
	}
	if ref.AtMS != 0 {
		t.Errorf("AtMS = %d, want 0", ref.AtMS)
	}
}

// ---------------------------------------------------------------------------
// SecurityDeviceAddress
// ---------------------------------------------------------------------------

func TestSecurityDeviceAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		addr string
		want string
	}{
		{"channel suffix stripped", "ABC123:4", "ABC123"},
		{"no colon stays unchanged", "ABC123", "ABC123"},
		{"empty stays empty", "", ""},
		{"leading colon yields empty prefix", ":4", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hmevent.SecurityDeviceAddress(tc.addr); got != tc.want {
				t.Errorf("SecurityDeviceAddress(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SecuritySourceRef.Empty
// ---------------------------------------------------------------------------

func TestSecuritySourceRef_Empty(t *testing.T) {
	t.Parallel()
	var zero hmevent.SecuritySourceRef
	if !zero.Empty() {
		t.Error("zero value: Empty() = false, want true")
	}

	withRef := hmevent.SecuritySourceRef{Ref: "ccu-main|HmIP-RF|ABC123:1|STATE"}
	if withRef.Empty() {
		t.Error("ref set: Empty() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// AlarmBlockerReason
// ---------------------------------------------------------------------------

func TestAlarmBlockerReason_Valid(t *testing.T) {
	t.Parallel()
	valid := []hmevent.AlarmBlockerReason{
		hmevent.AlarmBlockerReasonOpen,
		hmevent.AlarmBlockerReasonUnreachable,
		hmevent.AlarmBlockerReasonSabotage,
		hmevent.AlarmBlockerReasonLowBattery,
		hmevent.AlarmBlockerReasonBypassed,
	}
	for _, r := range valid {
		if !r.Valid() {
			t.Errorf("AlarmBlockerReason(%q).Valid() = false, want true", r)
		}
	}

	invented := hmevent.AlarmBlockerReason("earthquake")
	if invented.Valid() {
		t.Errorf("AlarmBlockerReason(%q).Valid() = true, want false", invented)
	}
}

func TestAlarmBlockerReason_StringRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []hmevent.AlarmBlockerReason{
		hmevent.AlarmBlockerReasonOpen,
		hmevent.AlarmBlockerReasonUnreachable,
		hmevent.AlarmBlockerReasonSabotage,
		hmevent.AlarmBlockerReasonLowBattery,
		hmevent.AlarmBlockerReasonBypassed,
		hmevent.AlarmBlockerReason("earthquake"),
	}
	for _, r := range cases {
		s := r.String()
		if hmevent.AlarmBlockerReason(s) != r {
			t.Errorf("String() round trip failed for %q: got %q", r, s)
		}
	}
}
