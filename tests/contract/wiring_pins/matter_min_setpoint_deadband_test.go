// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/thermo"
)

// TestPin_ThermostatServer_MinSetpointDeadBand_Default pins that a freshly
// constructed HEAT+COOL+AUTO ThermostatServer reports MinSetpointDeadBand
// (attribute 0x0019) as 20 (= 2.0°C), matching the matter.js HEAD default
// in packages/model/src/standard/elements/thermostat-cluster.element.ts.
func TestPin_ThermostatServer_MinSetpointDeadBand_Default(t *testing.T) {
	t.Parallel()
	srv := thermo.NewThermostatServer(thermo.ThermostatConfig{
		Features: thermo.ThermostatFeatureHEAT | thermo.ThermostatFeatureCOOL | thermo.ThermostatFeatureAUTO,
	})
	const attrMinSetpointDeadBand uint32 = 0x0019
	v, ok := srv.MatterRead(attrMinSetpointDeadBand)
	if !ok {
		t.Fatal("MatterRead(MinSetpointDeadBand 0x0019): ok=false (AUTO feature is present)")
	}
	got, ok2 := v.(int8)
	if !ok2 {
		t.Fatalf("MatterRead(0x0019): expected int8, got %T", v)
	}
	if got != 20 {
		t.Errorf("MinSetpointDeadBand = %d (%.1f°C), want 20 (2.0°C)", got, float64(got)/10)
	}
}
