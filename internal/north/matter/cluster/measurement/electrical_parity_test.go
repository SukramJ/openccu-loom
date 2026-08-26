// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package measurement_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestParity_ElectricalEnergyMeasurement_DataVersion_NonZero checks that
// ElectricalEnergyServer satisfies MatterClusterDataVersion and that its
// initial DataVersion is non-zero. Apple Home's MTRDevice cache discards
// cluster state with DataVersion=0 (Matter §10.6.5 "zero is reserved for
// absent or invalid"). Mirrors matter.js
// packages/protocol/src/interaction/InteractionServer.ts DataVersion
// per-cluster random init (Crypto.getRandomUint32, skip-zero).
func TestParity_ElectricalEnergyMeasurement_DataVersion_NonZero(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementEnergy, val: 100, obs: true}
	var s interfaces.MatterClusterServer = measurement.NewElectricalEnergyServer(src)
	dv, ok := s.(interface{ MatterDataVersion() uint32 })
	if !ok {
		t.Fatal("ElectricalEnergyServer does not implement MatterDataVersion")
	}
	if got := dv.MatterDataVersion(); got == 0 {
		t.Errorf("ElectricalEnergy DataVersion = 0, want non-zero (Matter §10.6.5)")
	}
}

// TestParity_ElectricalPowerMeasurement_DataVersion_NonZero checks that
// ElectricalPowerServer satisfies MatterClusterDataVersion and that its
// initial DataVersion is non-zero. Mirrors matter.js
// packages/protocol/src/interaction/InteractionServer.ts DataVersion
// per-cluster random init.
func TestParity_ElectricalPowerMeasurement_DataVersion_NonZero(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPower, val: 1500, obs: true}
	var s interfaces.MatterClusterServer = measurement.NewElectricalPowerServer(src)
	dv, ok := s.(interface{ MatterDataVersion() uint32 })
	if !ok {
		t.Fatal("ElectricalPowerServer does not implement MatterDataVersion")
	}
	if got := dv.MatterDataVersion(); got == 0 {
		t.Errorf("ElectricalPower DataVersion = 0, want non-zero (Matter §10.6.5)")
	}
}
