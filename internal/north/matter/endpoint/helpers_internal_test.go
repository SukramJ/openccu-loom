// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package endpoint (internal white-box tests for unexported helpers).
package endpoint

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ─── truncateUTF8 ────────────────────────────────────────────────────

func TestTruncateUTF8_BelowMax(t *testing.T) {
	t.Parallel()
	s := "hello"
	got := truncateUTF8(s, 32)
	if got != s {
		t.Errorf("truncateUTF8 below max: got %q, want %q", got, s)
	}
}

func TestTruncateUTF8_ExactlyMax(t *testing.T) {
	t.Parallel()
	s := "12345678901234567890123456789012" // 32 bytes
	got := truncateUTF8(s, 32)
	if got != s {
		t.Errorf("truncateUTF8 exactly max: got %q, want %q", got, s)
	}
}

func TestTruncateUTF8_ASCIIOver(t *testing.T) {
	t.Parallel()
	s := "123456789012345678901234567890123" // 33 bytes ASCII
	got := truncateUTF8(s, 32)
	if len(got) != 32 {
		t.Errorf("truncateUTF8 ASCII over: len=%d want 32", len(got))
	}
	if got != s[:32] {
		t.Errorf("truncateUTF8 ASCII over: got %q, want %q", got, s[:32])
	}
}

func TestTruncateUTF8_MultiByteAtCutPoint(t *testing.T) {
	t.Parallel()
	// Build a 32-byte string where byte 31 is the second byte of a 2-byte rune
	// so the naive [:32] would cut in the middle of a rune.
	// "ä" = 0xC3 0xA4 (2 bytes, U+00E4). Place it straddling byte 31/32.
	// 30 ASCII bytes + "ä" (2 bytes) = 32 bytes total. Cutting at 31 must snap
	// back to 30 (skip the leading byte 0xC3 at index 30).
	prefix := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 30 bytes
	s := prefix + "ä"                          // 32 bytes total
	if len(s) != 32 {
		t.Fatalf("test setup: len=%d want 32", len(s))
	}
	// Cutting at maxBytes=31 should snap back to 30 (before the 2-byte rune).
	got := truncateUTF8(s, 31)
	if got != prefix {
		t.Errorf("truncateUTF8 multi-byte at cut: got %q, want %q", got, prefix)
	}
}

// ─── measurementDeviceType ───────────────────────────────────────────

func TestMeasurementDeviceType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		class interfaces.MatterMeasurementClass
		want  uint16
	}{
		{interfaces.MatterMeasurementTemperature, 0x0302},
		{interfaces.MatterMeasurementHumidity, 0x0307},
		{interfaces.MatterMeasurementIlluminance, 0x0106},
		{interfaces.MatterMeasurementPressure, 0x0305},
		{interfaces.MatterMeasurementCO2, 0x002C},
		{interfaces.MatterMeasurementPM25, 0x002C},
		{interfaces.MatterMeasurementPM10, 0x002C},
		{interfaces.MatterMeasurementOccupancy, 0x0107},
		{interfaces.MatterMeasurementContact, 0x0015},
		{interfaces.MatterMeasurementLeak, 0x0043},
		{interfaces.MatterMeasurementMomentarySwitch, 0x000F},
		{interfaces.MatterMeasurementBattery, 0x0000},
		{interfaces.MatterMeasurementPower, 0x0000},
		{interfaces.MatterMeasurementEnergy, 0x0000},
		{interfaces.MatterMeasurementNone, 0x0000},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := measurementDeviceType(tc.class)
			if got != tc.want {
				t.Errorf("class=%d: got 0x%04X, want 0x%04X", tc.class, got, tc.want)
			}
		})
	}
}

// ─── friendlyName ────────────────────────────────────────────────────

func makeDevice(addr, name string) *device.Device {
	return device.New(device.Config{
		Address: addr,
		Name:    name,
	})
}

func makeChannel(dev *device.Device, addr string, no int, name string) *device.Channel {
	ch := dev.AddChannel(addr, no, "TEST_CHANNEL", hmenum.ParamsetKeyValues)
	ch.Name = name
	return ch
}

func TestFriendlyName_DeviceNameOnly(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Wohnzimmer")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	got := friendlyName(dev, ch, "")
	// Channel 1 with no name → "Kanal 1" appended.
	want := "Wohnzimmer Kanal 1"
	if got != want {
		t.Errorf("friendlyName device only: got %q, want %q", got, want)
	}
}

func TestFriendlyName_DeviceAndChannelName(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Haus")
	ch := makeChannel(dev, "ABC0001:2", 2, "Schlafzimmer")
	got := friendlyName(dev, ch, "")
	if got != "Haus Schlafzimmer" {
		t.Errorf("friendlyName device+channel: got %q, want %q", got, "Haus Schlafzimmer")
	}
}

func TestFriendlyName_WithParamSuffix(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Sensor")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	got := friendlyName(dev, ch, "TEMPERATURE")
	// "Sensor Kanal 1 (TEMPERATURE)" — must be ≤ 32 bytes
	if len(got) > 32 {
		t.Errorf("friendlyName suffix: result %q exceeds 32 bytes (%d)", got, len(got))
	}
}

func TestFriendlyName_NoNameFallsBackToAddress(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "") // no name
	ch := makeChannel(dev, "ABC0001:0", 0, "")
	got := friendlyName(dev, ch, "")
	// Device address when Name is empty, channel 0 has no number suffix.
	if got != "ABC0001" {
		t.Errorf("friendlyName no name: got %q, want %q", got, "ABC0001")
	}
}

func TestFriendlyName_LengthCapping(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "VeryLongDeviceNameThatExceedsTheMatterLimit")
	ch := makeChannel(dev, "ABC0001:1", 1, "AlsoLongChannelName")
	got := friendlyName(dev, ch, "")
	if len(got) > 32 {
		t.Errorf("friendlyName capping: result %q has %d bytes, want ≤32", got, len(got))
	}
}
