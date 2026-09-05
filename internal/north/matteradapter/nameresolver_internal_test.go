// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package matteradapter (internal white-box tests for the model-backed
// naming authority).
package matteradapter

import (
	"testing"

	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── node label ──────────────────────────────────────────────────────

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

// keyFor is the endpoint key the assembly would build for (dev, ch,
// param). A nil channel yields channel 0, which no test device carries
// — that is how the "source outside the indexed fleet" path is reached.
func keyFor(dev *device.Device, ch *device.Channel, param string) store.EndpointKey {
	key := store.EndpointKey{DeviceAddress: dev.Address, DPKey: param}
	if ch != nil {
		key.ChannelNo = ch.Number
	}
	return key
}

// nodeLabelFor composes the NodeLabel the assembly stamps on an
// endpoint of ch — the same two steps makeSpec takes: ask the naming
// authority for the base label, then compose + cap.
func nodeLabelFor(dev *device.Device, ch *device.Channel, paramSuffix, channelWord string) string {
	r := newModelNameResolver([]*device.Device{dev}, nil, channelWord)
	return endpoint.ComposeNodeLabel(r.EndpointLabel(keyFor(dev, ch, "")), paramSuffix)
}

func TestNodeLabel_DeviceNameOnly(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Wohnzimmer")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	got := nodeLabelFor(dev, ch, "", "Kanal")
	// Channel 1 with no name → "Kanal 1" appended.
	want := "Wohnzimmer Kanal 1"
	if got != want {
		t.Errorf("node label device only: got %q, want %q", got, want)
	}
}

func TestNodeLabel_DeviceAndChannelName(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Haus")
	ch := makeChannel(dev, "ABC0001:2", 2, "Schlafzimmer")
	got := nodeLabelFor(dev, ch, "", "Kanal")
	if got != "Haus Schlafzimmer" {
		t.Errorf("node label device+channel: got %q, want %q", got, "Haus Schlafzimmer")
	}
}

func TestNodeLabel_WithParamSuffix(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "Sensor")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	got := nodeLabelFor(dev, ch, "TEMPERATURE", "Kanal")
	// "Sensor Kanal 1 (TEMPERATURE)" — must be ≤ 32 bytes
	if len(got) > 32 {
		t.Errorf("node label suffix: result %q exceeds 32 bytes (%d)", got, len(got))
	}
}

func TestNodeLabel_NoNameFallsBackToAddress(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "") // no name
	ch := makeChannel(dev, "ABC0001:0", 0, "")
	got := nodeLabelFor(dev, ch, "", "Kanal")
	// Device address when Name is empty, channel 0 has no number suffix.
	if got != "ABC0001" {
		t.Errorf("node label no name: got %q, want %q", got, "ABC0001")
	}
}

// TestNodeLabel_MatchesModelNaming pins the NodeLabel to
// [device.Channel.NameData] instead of an adapter-private
// de-duplication rule, so a future divergence between Matter and the
// model's naming authority (MQTT discovery, REST) fails here. The two
// cases below are exactly the ones the pre-fix adapter rule handled
// on its own — verified against [device.Channel.NameData] directly,
// not against a value re-derived by the test.
func TestNodeLabel_MatchesModelNaming(t *testing.T) {
	t.Parallel()

	t.Run("device name equals channel name", func(t *testing.T) {
		t.Parallel()
		dev := makeDevice("ABC0001", "Buecherregal")
		ch := makeChannel(dev, "ABC0001:1", 1, "Buecherregal")
		want := ch.NameData().TranslatedFullName()
		got := nodeLabelFor(dev, ch, "", "Kanal")
		if got != want {
			t.Errorf("node label = %q, want model naming %q", got, want)
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
		got := nodeLabelFor(dev, ch, "", "Kanal")
		if got != want {
			t.Errorf("node label = %q, want model naming %q", got, want)
		}
		if got != "Buecherregal Schalt" {
			t.Errorf("node label = %q, want %q", got, "Buecherregal Schalt")
		}
	})
}

func TestNodeLabel_LengthCapping(t *testing.T) {
	t.Parallel()
	dev := makeDevice("ABC0001", "VeryLongDeviceNameThatExceedsTheMatterLimit")
	ch := makeChannel(dev, "ABC0001:1", 1, "AlsoLongChannelName")
	got := nodeLabelFor(dev, ch, "", "Kanal")
	if len(got) > 32 {
		t.Errorf("node label capping: result %q has %d bytes, want ≤32", got, len(got))
	}
}

// ─── parameter label ─────────────────────────────────────────────────

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

// parameterLabelFor asks the model-backed resolver for the label the
// assembly would append to a measurement endpoint's NodeLabel.
func parameterLabelFor(tr device.ParameterTranslator, dev *device.Device, ch *device.Channel, parameter string) string {
	r := newModelNameResolver([]*device.Device{dev}, tr, "Kanal")
	return r.ParameterLabel(keyFor(dev, ch, parameter))
}

func TestParameterLabel_NilLabels(t *testing.T) {
	t.Parallel()
	// nil Labels → title-cased parameter as suffix.
	dev := makeDevice("ABC0001", "Sensor")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	ch.Type = "WEATHER_TRANSMIT"
	got := parameterLabelFor(nil, dev, ch, "TEMPERATURE")
	if got != "Temperature" {
		t.Errorf("nil Labels: got %q, want %q", got, "Temperature")
	}
}

func TestParameterLabel_TranslatedLabel(t *testing.T) {
	t.Parallel()
	tr := &fakeTranslator{entries: map[string]string{
		"WEATHER_TRANSMIT|TEMPERATURE": "Temperatur",
	}}
	dev := makeDevice("ABC0001", "Sensor")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	ch.Type = "WEATHER_TRANSMIT"
	got := parameterLabelFor(tr, dev, ch, "TEMPERATURE")
	if got != "Temperatur" {
		t.Errorf("translated label: got %q, want %q", got, "Temperatur")
	}
}

func TestParameterLabel_PrimaryMarker(t *testing.T) {
	t.Parallel()
	// Explicit-empty translation is the primary-parameter marker → suffix omitted.
	tr := &fakeTranslator{entries: map[string]string{
		"SWITCH|STATE": "",
	}}
	dev := makeDevice("ABC0001", "Switch")
	ch := makeChannel(dev, "ABC0001:1", 1, "")
	ch.Type = "SWITCH"
	got := parameterLabelFor(tr, dev, ch, "STATE")
	if got != "" {
		t.Errorf("primary marker: got %q, want empty", got)
	}
}

func TestParameterLabel_ChannelTypedLookup(t *testing.T) {
	t.Parallel()
	// Label only registered for HEATING_CLIMATECONTROL_TRANSCEIVER; verifies
	// the channel type is passed through to the translator.
	tr := &fakeTranslator{entries: map[string]string{
		"HEATING_CLIMATECONTROL_TRANSCEIVER|SET_POINT_TEMPERATURE": "Solltemperatur",
	}}

	t.Run("matching channel type returns translation", func(t *testing.T) {
		t.Parallel()
		dev := makeDevice("DEF0001", "Thermostat")
		ch := makeChannel(dev, "DEF0001:1", 1, "")
		ch.Type = "HEATING_CLIMATECONTROL_TRANSCEIVER"
		got := parameterLabelFor(tr, dev, ch, "SET_POINT_TEMPERATURE")
		if got != "Solltemperatur" {
			t.Errorf("matching channel type: got %q, want %q", got, "Solltemperatur")
		}
	})

	t.Run("non-matching channel type falls back to title-case", func(t *testing.T) {
		t.Parallel()
		dev := makeDevice("DEF0001", "Thermostat")
		ch := makeChannel(dev, "DEF0001:2", 2, "")
		ch.Type = "SWITCH"
		got := parameterLabelFor(tr, dev, ch, "SET_POINT_TEMPERATURE")
		if got != "Set Point Temperature" {
			t.Errorf("non-matching channel type: got %q, want %q", got, "Set Point Temperature")
		}
	})
}

func TestParameterLabel_UnknownChannel(t *testing.T) {
	t.Parallel()
	// A key that resolves to no channel → channelType "" → bare
	// parameter lookup / title-case fallback.
	tr := &fakeTranslator{entries: map[string]string{
		"|TEMPERATURE": "Temperatur",
	}}
	dev := makeDevice("ABC0001", "Sensor")
	got := parameterLabelFor(tr, dev, nil, "TEMPERATURE")
	if got != "Temperatur" {
		t.Errorf("unknown channel with bare-key entry: got %q, want %q", got, "Temperatur")
	}
}

func TestParameterLabel_UnknownChannelTitleCase(t *testing.T) {
	t.Parallel()
	// No channel, no catalogue entry → title-cased parameter.
	dev := makeDevice("ABC0001", "Sensor")
	got := parameterLabelFor(&fakeTranslator{entries: map[string]string{}}, dev, nil, "TEMPERATURE")
	if got != "Temperature" {
		t.Errorf("unknown channel no entry: got %q, want %q", got, "Temperature")
	}
}
