// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"strings"
	"testing"
)

// uniqueIDFrom replicates the UniqueID derivation rule:
// lowercases, replaces ':' and '-' with '_', optionally prepends prefix.
func uniqueIDFrom(address, parameter, prefix string) string {
	s := strings.ToLower(address)
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "-", "_")
	if parameter != "" {
		s = s + "_" + strings.ToLower(parameter)
	}
	if prefix != "" {
		s = prefix + "_" + s
	}
	return s
}

// ---------------------------------------------------------------------------
// UniqueID format per interface type
// ---------------------------------------------------------------------------

func TestUniqueIDFormat_HmIPRF(t *testing.T) {
	t.Parallel()
	// HmIP-RF addresses: VCU<serial>:<channel>
	got := uniqueIDFrom("VCU1234567:1", "STATE", "")
	want := "vcu1234567_1_state"
	if got != want {
		t.Fatalf("HmIP-RF uid = %q, want %q", got, want)
	}
}

func TestUniqueIDFormat_BidCosRF(t *testing.T) {
	t.Parallel()
	// BidCos-RF addresses: MEQ<serial>:<channel>
	got := uniqueIDFrom("MEQ0001234:1", "LEVEL", "")
	want := "meq0001234_1_level"
	if got != want {
		t.Fatalf("BidCos-RF uid = %q, want %q", got, want)
	}
}

func TestUniqueIDFormat_BidCosWired(t *testing.T) {
	t.Parallel()
	// BidCos-Wired devices often have a hyphen in the address.
	got := uniqueIDFrom("ABC-DEF:2", "LEVEL", "")
	want := "abc_def_2_level"
	if got != want {
		t.Fatalf("BidCos-Wired uid = %q, want %q", got, want)
	}
}

func TestUniqueIDFormat_HmIPWired(t *testing.T) {
	t.Parallel()
	got := uniqueIDFrom("HmIPW-DRDI3-4:3", "PRESS_SHORT", "")
	want := "hmipw_drdi3_4_3_press_short"
	if got != want {
		t.Fatalf("HmIP-Wired uid = %q, want %q", got, want)
	}
}

func TestUniqueIDFormat_VirtualDevices(t *testing.T) {
	t.Parallel()
	// VirtualDevices / CCU-Jack: same address scheme, different path roots.
	got := uniqueIDFrom("INT0001234:1", "LEVEL", "")
	want := "int0001234_1_level"
	if got != want {
		t.Fatalf("VirtualDevices uid = %q, want %q", got, want)
	}
}

func TestUniqueIDFormat_CUxD(t *testing.T) {
	t.Parallel()
	// CUxD addresses contain dots and underscores e.g. CUX2801001:1
	got := uniqueIDFrom("CUX2801001:1", "ON_TIME", "")
	want := "cux2801001_1_on_time"
	if got != want {
		t.Fatalf("CUxD uid = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// UniqueID without parameter (channel-level)
// ---------------------------------------------------------------------------

func TestChannelUniqueIDNoParameter(t *testing.T) {
	t.Parallel()
	got := uniqueIDFrom("VCU1234567:0", "", "")
	want := "vcu1234567_0"
	if got != want {
		t.Fatalf("channel uid = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// UniqueID with prefix (events, buttons, force_to_sensor)
// ---------------------------------------------------------------------------

func TestUniqueIDWithEventPrefix(t *testing.T) {
	t.Parallel()
	got := uniqueIDFrom("VCU1234567:1", "PRESS_SHORT", "event")
	want := "event_vcu1234567_1_press_short"
	if got != want {
		t.Fatalf("event uid = %q, want %q", got, want)
	}
}

func TestUniqueIDWithSensorPrefix(t *testing.T) {
	t.Parallel()
	// force_to_sensor wraps a binary sensor as a numeric sensor; prefix is "sensor".
	got := uniqueIDFrom("VCU1234567:1", "LOWBAT", "sensor")
	want := "sensor_vcu1234567_1_lowbat"
	if got != want {
		t.Fatalf("sensor uid = %q, want %q", got, want)
	}
}
