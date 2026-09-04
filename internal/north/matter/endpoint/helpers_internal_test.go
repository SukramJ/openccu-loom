// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package endpoint (internal white-box tests for unexported helpers).
package endpoint

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// minimalStore is the smallest valid [Store] for tests that only need
// endpoint.New to succeed — it returns ErrEndpointNotFound for every
// lookup and assigns sequential IDs on upsert.
type minimalStore struct {
	nextID uint16
}

func newMinimalStore() *minimalStore { return &minimalStore{nextID: 2} }

func (s *minimalStore) GetEndpoint(_ context.Context, _ store.EndpointKey) (store.EndpointRecord, error) {
	return store.EndpointRecord{}, store.ErrEndpointNotFound
}

func (s *minimalStore) UpsertEndpointAssigning(_ context.Context, rec store.EndpointRecord) (uint16, error) {
	if rec.EndpointID == 0 {
		rec.EndpointID = s.nextID
		s.nextID++
	}
	return rec.EndpointID, nil
}

func (s *minimalStore) ListEndpoints(_ context.Context, _ string) ([]store.EndpointRecord, error) {
	return nil, nil
}

func (s *minimalStore) RemoveEndpoint(_ context.Context, _ store.EndpointKey) error {
	return nil
}

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
		class mattercontract.MeasurementClass
		want  uint16
	}{
		{mattercontract.MeasurementTemperature, 0x0302},
		{mattercontract.MeasurementHumidity, 0x0307},
		{mattercontract.MeasurementIlluminance, 0x0106},
		{mattercontract.MeasurementPressure, 0x0305},
		{mattercontract.MeasurementCO2, 0x002C},
		{mattercontract.MeasurementPM25, 0x002C},
		{mattercontract.MeasurementPM10, 0x002C},
		{mattercontract.MeasurementOccupancy, 0x0107},
		{mattercontract.MeasurementContact, 0x0015},
		// Leak rides on ContactSensor (0x0015), not WaterLeakDetector
		// (0x0043) — see mattercontract.MeasurementClassDeviceType.
		{mattercontract.MeasurementLeak, 0x0015},
		{mattercontract.MeasurementMomentarySwitch, 0x000F},
		{mattercontract.MeasurementBattery, 0x0000},
		{mattercontract.MeasurementPower, 0x0000},
		{mattercontract.MeasurementEnergy, 0x0000},
		{mattercontract.MeasurementNone, 0x0000},
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
	ch.SetName(name)
	return ch
}

func TestFriendlyName_DeviceNameOnly(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Wohnzimmer")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	got := friendlyName(dev, ch, "", "Kanal")
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
	got := friendlyName(dev, ch, "", "Kanal")
	if got != "Haus Schlafzimmer" {
		t.Errorf("friendlyName device+channel: got %q, want %q", got, "Haus Schlafzimmer")
	}
}

func TestFriendlyName_WithParamSuffix(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Sensor")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	got := friendlyName(dev, ch, "TEMPERATURE", "Kanal")
	// "Sensor Kanal 1 (TEMPERATURE)" — must be ≤ 32 bytes
	if len(got) > 32 {
		t.Errorf("friendlyName suffix: result %q exceeds 32 bytes (%d)", got, len(got))
	}
}

func TestFriendlyName_NoNameFallsBackToAddress(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "") // no name
	ch := makeChannel(dev, "ABC0001:0", 0, "")
	got := friendlyName(dev, ch, "", "Kanal")
	// Device address when Name is empty, channel 0 has no number suffix.
	if got != "ABC0001" {
		t.Errorf("friendlyName no name: got %q, want %q", got, "ABC0001")
	}
}

// TestFriendlyName_MatchesModelNaming pins the NodeLabel to
// [device.Channel.NameData] instead of an adapter-private
// de-duplication rule, so a future divergence between Matter and the
// model's naming authority (MQTT discovery, REST) fails here. The two
// cases below are exactly the ones the pre-fix adapter rule handled
// on its own — verified against [device.Channel.NameData] directly,
// not against a value re-derived by the test.
func TestFriendlyName_MatchesModelNaming(t *testing.T) {
	t.Parallel()

	t.Run("device name equals channel name", func(t *testing.T) {
		t.Parallel()
		dev := makeDevice("ABC0001", "Buecherregal")
		ch := makeChannel(dev, "ABC0001:1", 1, "Buecherregal")
		want := ch.NameData().TranslatedFullName()
		got := friendlyName(dev, ch, "", "Kanal")
		if got != want {
			t.Errorf("friendlyName = %q, want model naming %q", got, want)
		}
	})

	t.Run("channel name carries device name as a prefix", func(t *testing.T) {
		t.Parallel()
		// The channel name is not a pure duplicate here — it has a real
		// suffix word after the device-name prefix. The model's own
		// de-duplication rule (composeName in internal/model/naming)
		// strips only the duplicated prefix and keeps the suffix; the
		// pre-fix adapter rule instead dropped the whole channel name
		// whenever one name prefixed the other, producing "Buecherregal"
		// here instead of the model's "Buecherregal Schalt".
		dev := makeDevice("ABC0001", "Buecherregal")
		ch := makeChannel(dev, "ABC0001:1", 1, "Buecherregal Schalt")
		want := ch.NameData().TranslatedFullName()
		got := friendlyName(dev, ch, "", "Kanal")
		if got != want {
			t.Errorf("friendlyName = %q, want model naming %q", got, want)
		}
		if got != "Buecherregal Schalt" {
			t.Errorf("friendlyName = %q, want %q", got, "Buecherregal Schalt")
		}
	})
}

func TestFriendlyName_LengthCapping(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "VeryLongDeviceNameThatExceedsTheMatterLimit")
	ch := makeChannel(dev, "ABC0001:1", 1, "AlsoLongChannelName")
	got := friendlyName(dev, ch, "", "Kanal")
	if len(got) > 32 {
		t.Errorf("friendlyName capping: result %q has %d bytes, want ≤32", got, len(got))
	}
}

// ─── parameterSuffix ─────────────────────────────────────────────────

// fakeTranslator is a map-backed [device.ParameterTranslator] for
// internal endpoint tests. The key is "<channelType>|<parameter>"; a
// present key with an empty value represents the primary-parameter
// marker.
type fakeTranslator struct {
	entries map[string]string
}

func (f *fakeTranslator) ChannelTypedParameterLabelOk(channelType, parameter string) (string, bool) {
	v, ok := f.entries[channelType+"|"+parameter]
	return v, ok
}

func newAssemblerWithLabels(t *testing.T, tr device.ParameterTranslator) *Assembler {
	t.Helper()
	s := newMinimalStore()
	a, err := New(s, Config{VendorID: 1, ProductID: 1, NodeLabel: "x", Labels: tr}, nil)
	if err != nil {
		t.Fatalf("endpoint.New: %v", err)
	}
	return a
}

func TestParameterSuffix_NilLabels(t *testing.T) {
	t.Parallel()
	// nil Labels → title-cased parameter as suffix.
	a := newAssemblerWithLabels(t, nil)
	ch := makeChannel(makeDevice("ABC0001", "Sensor"), "ABC0001:1", 1, "")
	ch.Type = "WEATHER_TRANSMIT"
	got := a.parameterSuffix(ch, "TEMPERATURE")
	if got != "Temperature" {
		t.Errorf("nil Labels: got %q, want %q", got, "Temperature")
	}
}

func TestParameterSuffix_TranslatedLabel(t *testing.T) {
	t.Parallel()
	tr := &fakeTranslator{entries: map[string]string{
		"WEATHER_TRANSMIT|TEMPERATURE": "Temperatur",
	}}
	a := newAssemblerWithLabels(t, tr)
	ch := makeChannel(makeDevice("ABC0001", "Sensor"), "ABC0001:1", 1, "")
	ch.Type = "WEATHER_TRANSMIT"
	got := a.parameterSuffix(ch, "TEMPERATURE")
	if got != "Temperatur" {
		t.Errorf("translated label: got %q, want %q", got, "Temperatur")
	}
}

func TestParameterSuffix_PrimaryMarker(t *testing.T) {
	t.Parallel()
	// Explicit-empty translation is the primary-parameter marker → suffix omitted.
	tr := &fakeTranslator{entries: map[string]string{
		"SWITCH|STATE": "",
	}}
	a := newAssemblerWithLabels(t, tr)
	ch := makeChannel(makeDevice("ABC0001", "Switch"), "ABC0001:1", 1, "")
	ch.Type = "SWITCH"
	got := a.parameterSuffix(ch, "STATE")
	if got != "" {
		t.Errorf("primary marker: got %q, want empty", got)
	}
}

func TestParameterSuffix_ChannelTypedLookup(t *testing.T) {
	t.Parallel()
	// Label only registered for HEATING_CLIMATECONTROL_TRANSCEIVER; verifies
	// the channel type is passed through to the translator.
	tr := &fakeTranslator{entries: map[string]string{
		"HEATING_CLIMATECONTROL_TRANSCEIVER|SET_POINT_TEMPERATURE": "Solltemperatur",
	}}
	a := newAssemblerWithLabels(t, tr)

	t.Run("matching channel type returns translation", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel(makeDevice("DEF0001", "Thermostat"), "DEF0001:1", 1, "")
		ch.Type = "HEATING_CLIMATECONTROL_TRANSCEIVER"
		got := a.parameterSuffix(ch, "SET_POINT_TEMPERATURE")
		if got != "Solltemperatur" {
			t.Errorf("matching channel type: got %q, want %q", got, "Solltemperatur")
		}
	})

	t.Run("non-matching channel type falls back to title-case", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel(makeDevice("DEF0001", "Thermostat"), "DEF0001:2", 2, "")
		ch.Type = "SWITCH"
		got := a.parameterSuffix(ch, "SET_POINT_TEMPERATURE")
		if got != "Set Point Temperature" {
			t.Errorf("non-matching channel type: got %q, want %q", got, "Set Point Temperature")
		}
	})
}

func TestParameterSuffix_NilChannel(t *testing.T) {
	t.Parallel()
	// nil channel → channelType "" → bare parameter lookup / title-case fallback.
	tr := &fakeTranslator{entries: map[string]string{
		"|TEMPERATURE": "Temperatur",
	}}
	a := newAssemblerWithLabels(t, tr)
	got := a.parameterSuffix(nil, "TEMPERATURE")
	if got != "Temperatur" {
		t.Errorf("nil channel with bare-key entry: got %q, want %q", got, "Temperatur")
	}
}

func TestParameterSuffix_NilChannelTitleCase(t *testing.T) {
	t.Parallel()
	// nil channel, no catalogue entry → title-cased parameter.
	a := newAssemblerWithLabels(t, &fakeTranslator{entries: map[string]string{}})
	got := a.parameterSuffix(nil, "TEMPERATURE")
	if got != "Temperature" {
		t.Errorf("nil channel no entry: got %q, want %q", got, "Temperature")
	}
}
