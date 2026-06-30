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
		{"3014F711A0001F5A4993D962", "5A4993D962"}, // long UDN tail → last 10, case kept
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
