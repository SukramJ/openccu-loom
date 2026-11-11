// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package config

import (
	"testing"
)

// TestValidateNorthMeasurements pins when the forwarder block is checked:
// an enabled block needs a target the daemon can build, a disabled block
// may stay half-filled.
func TestValidateNorthMeasurements(t *testing.T) {
	t.Parallel()
	on, off := true, false
	cases := []struct {
		name    string
		block   NorthMeasurements
		wantErr string
	}{
		{"enabled without endpoint", NorthMeasurements{Enabled: &on}, "north.measurements.endpoint"},
		{"enabled with unknown kind", NorthMeasurements{Enabled: &on, Kind: "prometheus", Endpoint: "http://x:9090"}, "north.measurements.kind"},
		{"enabled influxdb target", NorthMeasurements{Enabled: &on, Kind: "InfluxDB", Endpoint: "http://influx:8086"}, ""},
		{"enabled with default kind", NorthMeasurements{Enabled: &on, Endpoint: "http://influx:8086"}, ""},
		{"disabled half-filled", NorthMeasurements{Enabled: &off, Kind: "prometheus"}, ""},
		{"unset half-filled", NorthMeasurements{Bucket: "loom"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Default()
			c.North.Measurements = tc.block
			err := c.Validate()
			if tc.wantErr == "" {
				assertAccepted(t, err)
				return
			}
			assertRejected(t, err, tc.wantErr)
		})
	}
}

// TestNorthMeasurementsEnabledFor pins the per-central gate: the master
// switch first, then the operator's exclusions.
func TestNorthMeasurementsEnabledFor(t *testing.T) {
	t.Parallel()
	on, off := true, false

	if (NorthMeasurements{}).EnabledFor("a") {
		t.Error("EnabledFor(a) = true with Enabled unset, want false (opt-in)")
	}
	if (NorthMeasurements{Enabled: &off}).EnabledFor("a") {
		t.Error("EnabledFor(a) = true with Enabled false, want false")
	}
	c := NorthMeasurements{Enabled: &on, DisabledCentrals: []string{"b"}}
	if !c.IsEnabled() {
		t.Error("IsEnabled() = false with Enabled true")
	}
	if !c.EnabledFor("a") {
		t.Error("EnabledFor(a) = false, want true when a is not in disabled_centrals")
	}
	if c.EnabledFor("b") {
		t.Error("EnabledFor(b) = true, want false when b is in disabled_centrals")
	}
}
