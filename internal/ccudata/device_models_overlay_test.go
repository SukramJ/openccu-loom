// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import "testing"

// TestDeviceModelLabelOverlayFillsUpstreamGaps guards the curated
// device-model overlay entries that close upstream catalogue gaps:
// HmIP-DLP and HmIP-UDI-SMI55 ship icons and parameter help in the
// extract but no device_models_{en,de} label, so without the overlay
// the MQTT discovery payload would omit model_id for these devices and
// the discovery-snapshot model_id invariant would flag them. The
// overlay (translation_custom/device_models_{en,de}.json) supplies the
// label; this test pins that it resolves through the real embedded
// catalogue for both locales.
func TestDeviceModelLabelOverlayFillsUpstreamGaps(t *testing.T) {
	t.Parallel()

	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}

	cases := []struct {
		name   string
		locale string
		model  string
		want   string
	}{
		{"DLP en", "en", "HmIP-DLP", "Homematic IP Door Lock Drive - pro"},
		{"DLP de", "de", "HmIP-DLP", "Homematic IP Türschlossantrieb - pro"},
		{"UDI-SMI55 en", "en", "HmIP-UDI-SMI55", "Homematic IP Universal Dimming Control Element - motion detector"},
		{"UDI-SMI55 de", "de", "HmIP-UDI-SMI55", "Homematic IP Universal Dimmeraufsatz - Bewegungsmelder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tr.DeviceModelLabel(tc.locale, tc.model, ""); got != tc.want {
				t.Errorf("DeviceModelLabel(%q, %q, \"\") = %q, want %q",
					tc.locale, tc.model, got, tc.want)
			}
		})
	}
}
