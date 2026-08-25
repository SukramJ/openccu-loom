// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package routingkey

import (
	"sync"
	"testing"
)

// TestHubSlugConcurrent locks the goroutine-safety of HubSlug: the
// Unicode transformer it uses carries internal state, so a shared
// instance would corrupt under parallel callers (the MQTT discovery
// builder calls it from concurrent publish paths). Run with -race.
func TestHubSlugConcurrent(t *testing.T) {
	const want = "loom_11a0001234_sysvar_aussen-temperatur"
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			got := CanonicalUniqueID("11a0001234", "sysvar", HubSlug("Außen Temperatur"), "")
			if got != want {
				t.Errorf("concurrent HubSlug = %q, want %q", got, want)
			}
		})
	}
	wg.Wait()
}

func TestSerialSuffix(t *testing.T) {
	cases := []struct {
		serial string
		want   string
	}{
		{"3014F711A0001234", "11a0001234"},
		{"MEQ1234567", "meq1234567"},
		{"SHORT", "short"},
		{"", ""},
		{"0123456789", "0123456789"},
		{"X0123456789", "0123456789"},
	}
	for _, c := range cases {
		if got := SerialSuffix(c.serial); got != c.want {
			t.Errorf("SerialSuffix(%q) = %q, want %q", c.serial, got, c.want)
		}
	}
}

// TestCanonicalSerial pins the shared per-CCU serial canonicalisation: last 10
// characters, case PRESERVED (unlike SerialSuffix, which lower-cases). This is
// the form both the ReGa GetSerial reader and SSDP discovery funnel through, so
// a runtime CCU serial and a discovered serial compare equal.
func TestCanonicalSerial(t *testing.T) {
	cases := []struct {
		serial string
		want   string
	}{
		{"3014F711A0001F0123456789", "0123456789"}, // long UDN tail → last 10, case kept
		{"0001ABCDEF12", "01ABCDEF12"},
		{"MEQ1234567", "MEQ1234567"}, // exactly 10 → verbatim
		{"SHORT", "SHORT"},           // shorter than 10 → verbatim
		{"", ""},                     // empty in, empty out (keeps the host-fallback path intact)
		{"X0123456789", "0123456789"},
	}
	for _, c := range cases {
		if got := CanonicalSerial(c.serial); got != c.want {
			t.Errorf("CanonicalSerial(%q) = %q, want %q", c.serial, got, c.want)
		}
	}
}

func TestCanonicalUniqueID(t *testing.T) {
	serial10 := SerialSuffix("3014F711A0001234") // 11a0001234
	cases := []struct {
		name        string
		address     string
		parameter   string
		eventPrefix string
		want        string
	}{
		{"device", "VCU1234567:1", "STATE", "", "loom_vcu1234567_1_state"},
		{"device-no-param", "VCU1234567:1", "", "", "loom_vcu1234567_1"},
		{"button-event", "VCU1234567:1", "PRESS_SHORT", "event", "loom_event_vcu1234567_1_press_short"},
		{"sysvar", "sysvar", "aussen-temperatur", "", "loom_11a0001234_sysvar_aussen-temperatur"},
		{"program", "program", "my-prog", "", "loom_11a0001234_program_my-prog"},
		{"internal", "INT0001234:1", "LEVEL", "", "loom_11a0001234_int0001234_1_level"},
		{"virtual-remote", "BidCoS-RF:1", "PRESS_SHORT", "", "loom_11a0001234_bidcos_rf_1_press_short"},
	}
	for _, c := range cases {
		got := CanonicalUniqueID(serial10, c.address, c.parameter, c.eventPrefix)
		if got != c.want {
			t.Errorf("%s: CanonicalUniqueID(%q, %q, %q, %q) = %q, want %q",
				c.name, serial10, c.address, c.parameter, c.eventPrefix, got, c.want)
		}
	}
}

// TestCalculatedUniqueIDMatchesConsumerMigration pins the calculated
// data-point key against the rewrite the Home Assistant drop-in performs
// when a config entry switches to this backend. Its migration maps the
// legacy key `<domain>_calculated_<dev>_<ch>_<param>` to
// `<domain>_loom_calculated_<dev>_<ch>_<param>`, so the key this package
// produces has to carry the family marker in the same slot.
//
// Emitting the key without the family marker does not merely look
// different - it orphans the entity the consumer just migrated (which
// holds the user's history, area and customisations) and spawns an empty
// duplicate beside it. That happened for every calculated data point on a
// real install, so the shape is pinned here rather than left to the
// call sites.
func TestCalculatedUniqueIDMatchesConsumerMigration(t *testing.T) {
	t.Parallel()
	const serial = "0123456789"
	cases := []struct {
		name    string
		address string
		param   string
		want    string
	}{
		{
			// A normal device: globally unique serial, so no central slot.
			name:    "device channel",
			address: "000A5928583F0F:1",
			param:   "SMOKE_ALARM",
			want:    "loom_calculated_000a5928583f0f_1_smoke_alarm",
		},
		{
			// An INT* virtual device repeats across CCUs, so the central
			// slot precedes the family marker - the consumer's migration
			// produces exactly this order.
			name:    "internal device carries the central slot first",
			address: "INT0000012:1",
			param:   "DEW_POINT",
			want:    "loom_0123456789_calculated_int0000012_1_dew_point",
		},
		{
			name:    "maintenance channel",
			address: "00109A49A51400:0",
			param:   "OPERATING_VOLTAGE_LEVEL",
			want:    "loom_calculated_00109a49a51400_0_operating_voltage_level",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CalculatedUniqueID(serial, tc.address, tc.param); got != tc.want {
				t.Errorf("CalculatedUniqueID(%q, %q) = %q, want %q", tc.address, tc.param, got, tc.want)
			}
		})
	}
}

// TestCalculatedUniqueIDDiffersFromPlainKey guards the reason the marker
// exists: a calculated data point and a VALUES parameter of the same name
// on the same channel must not collide.
func TestCalculatedUniqueIDDiffersFromPlainKey(t *testing.T) {
	t.Parallel()
	const serial = "0123456789"
	plain := CanonicalUniqueID(serial, "000A5928583F0F:1", "SMOKE_ALARM", "")
	calc := CalculatedUniqueID(serial, "000A5928583F0F:1", "SMOKE_ALARM")
	if plain == calc {
		t.Fatalf("calculated and plain keys collide: %q", plain)
	}
}
