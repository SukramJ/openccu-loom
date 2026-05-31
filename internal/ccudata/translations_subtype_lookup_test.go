// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import "testing"

// TestDeviceModelLabelSubtypePropagation verifies that 25 device models
// that previously emitted empty model_id in MQTT discovery now resolve
// through the extended lookup chain (vendor-prefix strip + suffix-strip
// + SUBTYPE fallback).
//
// The synthetic translation table mirrors the keys present in the
// Real
func TestDeviceModelLabelSubtypePropagation(t *testing.T) {
	t.Parallel()

	table := map[string]string{
		// HmIP "bare" SUBTYPE keys (lower-cased)
		"ps":    "Pluggable Switch",
		"psm":   "Pluggable Switch and Meter",
		"psmco": "Pluggable Switch and Meter, CO",
		"rc8":   "Remote Control 8",
		"bdt":   "Brand Dimmer",
		"smi":   "Motion Detector indoor",
		"smi55": "Motion Detector indoor 55",
		"wrc6":  "Wall Remote 6",
		"fsm":   "Fixed Switch Meter",
		"fsm16": "Fixed Switch Meter 16",
		"krc4":  "Key Ring Remote 4",
		"krca":  "Key Ring Remote Alarm",
		"fdt":   "Flush Dimmer",
		"srh":   "Rotary Handle",
		"sth":   "Temperature/Humidity",
		"bsm":   "Branded Switch Meter",
		"smo":   "Motion Detector outdoor",
		"swsd":  "Smoke Detector",
		// SUBTYPE-keyed entries for the eTRV family.
		"trv":   "Radiator Thermostat",
		"trv-b": "Radiator Thermostat - basic",
		"trv-c": "Radiator Thermostat - compact",
		"trv-e": "Radiator Thermostat - Evo",
	}
	tr := &Translations{
		DeviceModels: map[string]map[string]string{
			"de": table,
		},
	}

	cases := []struct {
		name    string
		model   string
		subtype string
		want    string
	}{
		// Stage 2 — strip "HMIP-" / "HmIP-" prefix.
		{"HMIP-PS uppercase", "HMIP-PS", "", "Pluggable Switch"},
		{"HMIP-PSM uppercase", "HMIP-PSM", "", "Pluggable Switch and Meter"},
		{"HmIP-RC8", "HmIP-RC8", "", "Remote Control 8"},
		{"HmIP-BDT", "HmIP-BDT", "", "Brand Dimmer"},
		{"HmIP-SMI", "HmIP-SMI", "", "Motion Detector indoor"},
		{"HmIP-WRC6", "HmIP-WRC6", "", "Wall Remote 6"},
		{"HmIP-FSM16", "HmIP-FSM16", "", "Fixed Switch Meter 16"},
		{"HmIP-KRC4", "HmIP-KRC4", "", "Key Ring Remote 4"},
		{"HmIP-KRCA", "HmIP-KRCA", "", "Key Ring Remote Alarm"},
		{"HmIP-FDT", "HmIP-FDT", "", "Flush Dimmer"},
		{"HmIP-SRH", "HmIP-SRH", "", "Rotary Handle"},
		{"HmIP-STH", "HmIP-STH", "", "Temperature/Humidity"},
		{"HmIP-PSMCO", "HmIP-PSMCO", "", "Pluggable Switch and Meter, CO"},
		{"HmIP-BSM", "HmIP-BSM", "", "Branded Switch Meter"},
		{"HmIP-SMO", "HmIP-SMO", "", "Motion Detector outdoor"},

		// Stage 4 — strip prefix + iteratively drop trailing "-X"
		// tokens until a key matches.
		{"HmIP-SMO-2", "HmIP-SMO-2", "", "Motion Detector outdoor"},
		{"HmIP-SMO-A", "HmIP-SMO-A", "", "Motion Detector outdoor"},
		{"HmIP-SWSD-2", "HmIP-SWSD-2", "", "Smoke Detector"},

		// Stage 5/6 — SUBTYPE-keyed eTRV family. The CCU TYPE is the
		// detailed variant; the SUBTYPE strips the variant tail to
		// the family root.
		{"HmIP-eTRV-2 with SUBTYPE=TRV", "HmIP-eTRV-2", "TRV", "Radiator Thermostat"},
		{"HmIP-eTRV-2 I9F with SUBTYPE=TRV", "HmIP-eTRV-2 I9F", "TRV", "Radiator Thermostat"},
		{"HmIP-eTRV-B with SUBTYPE=TRV-B", "HmIP-eTRV-B", "TRV-B", "Radiator Thermostat - basic"},
		{"HmIP-eTRV-B-2 R4M with SUBTYPE=TRV-B", "HmIP-eTRV-B-2 R4M", "TRV-B", "Radiator Thermostat - basic"},
		{"HmIP-eTRV-B1 with SUBTYPE=TRV-B", "HmIP-eTRV-B1", "TRV-B", "Radiator Thermostat - basic"},
		{"HmIP-eTRV-C-2 with SUBTYPE=TRV-C", "HmIP-eTRV-C-2", "TRV-C", "Radiator Thermostat - compact"},
		{"HmIP-eTRV-E with SUBTYPE=TRV-E", "HmIP-eTRV-E", "TRV-E", "Radiator Thermostat - Evo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tr.DeviceModelLabel("de", tc.model, tc.subtype); got != tc.want {
				t.Errorf("DeviceModelLabel(de, %q, %q) = %q, want %q",
					tc.model, tc.subtype, got, tc.want)
			}
		})
	}
}

// TestDeviceModelLabelStagePriority verifies the lookup order:
// full model first, then prefix-stripped, then SUBTYPE.
func TestDeviceModelLabelStagePriority(t *testing.T) {
	t.Parallel()

	tr := &Translations{
		DeviceModels: map[string]map[string]string{
			"de": {
				"hmip-ps": "Full Match",
				"ps":      "Stripped Match",
				"foo":     "Subtype Match",
			},
		},
	}

	if got := tr.DeviceModelLabel("de", "HmIP-PS", "FOO"); got != "Full Match" {
		t.Errorf("full-match should win, got %q", got)
	}
	if got := tr.DeviceModelLabel("de", "HmIP-XXX", "FOO"); got != "Subtype Match" {
		t.Errorf("subtype fallback expected, got %q", got)
	}
	if got := tr.DeviceModelLabel("de", "HmIP-PS-2", ""); got != "Stripped Match" {
		t.Errorf("strip-suffix fallback expected, got %q", got)
	}
}
