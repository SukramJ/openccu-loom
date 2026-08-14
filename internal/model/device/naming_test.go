// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestDataPointNameAndFullName(t *testing.T) {
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Address:     "ABC0001",
		Name:        "Wohnzimmer Licht",
	})
	ch := d.AddChannel("ABC0001:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	ch.SetName("Wohnzimmer Licht-Kanal")

	if got := ch.DataPointName("LEVEL"); got != "Kanal LEVEL" {
		t.Fatalf("DataPointName = %q want 'Kanal LEVEL'", got)
	}
	if got := ch.DataPointFullName("LEVEL"); got != "Wohnzimmer Licht Kanal LEVEL" {
		t.Fatalf("DataPointFullName = %q want 'Wohnzimmer Licht Kanal LEVEL'", got)
	}
}

func TestDataPointNameWithoutChannelName(t *testing.T) {
	d := New(Config{Address: "ABC0001", Name: "Sensor"})
	ch := d.AddChannel("ABC0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	if got := ch.DataPointName("LOW_BAT"); got != "LOW_BAT" {
		t.Fatalf("expected parameter-only name, got %q", got)
	}
	if got := ch.DataPointFullName("LOW_BAT"); got != "Sensor LOW_BAT" {
		t.Fatalf("expected device-prefixed name, got %q", got)
	}
}

func TestDataPointFullNameNoParameterFallsBackToDevice(t *testing.T) {
	d := New(Config{Address: "ABC0001", Name: "Heizkörper Bad"})
	ch := d.AddChannel("ABC0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	if got := ch.DataPointFullName(""); got != "Heizkörper Bad" {
		t.Fatalf("expected device-only fallback, got %q", got)
	}
}

func TestDataPointNameNilChannel(t *testing.T) {
	var ch *Channel
	if got := ch.DataPointName("LEVEL"); got != "LEVEL" {
		t.Fatalf("nil channel should return parameter, got %q", got)
	}
	if got := ch.DataPointFullName("LEVEL"); got != "LEVEL" {
		t.Fatalf("nil channel full-name should return parameter, got %q", got)
	}
}

func TestCustomDataPointName(t *testing.T) {
	d := New(Config{Address: "ABC0001", Name: "Wohnzimmer Licht"})
	ch := d.AddChannel("ABC0001:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	ch.SetName("Wohnzimmer Licht-Kanal")

	// No postfix → behaves like DataPointName.
	if got := ch.CustomDataPointName("LEVEL", ""); got != "Kanal LEVEL" {
		t.Fatalf("CustomDataPointName empty postfix = %q", got)
	}
	// Postfix replaces parameter, uppercased per
	if got := ch.CustomDataPointName("LEVEL", "color"); got != "Kanal COLOR" {
		t.Fatalf("CustomDataPointName postfix = %q want 'Kanal COLOR'", got)
	}
	if got := ch.CustomDataPointFullName("LEVEL", "color_temp"); got != "Wohnzimmer Licht Kanal COLOR_TEMP" {
		t.Fatalf("CustomDataPointFullName = %q", got)
	}
}

func TestGenerateTranslationKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Wohnzimmer Licht", "wohnzimmer_licht"},
		{"Hello.World", "hello_world"},
		{"foo-bar", "foo_bar"},
		{"trailing  spaces  ", "trailing_spaces"},
		{"UPPER", "upper"},
	}
	for _, c := range cases {
		if got := GenerateTranslationKey(c.in); got != c.want {
			t.Errorf("GenerateTranslationKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
