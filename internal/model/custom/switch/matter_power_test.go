// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// fakeFloat is a minimal MatterFloatMeasurementSource used to exercise the
// AttachPowerSource / AttachEnergySource paths without a real generic.Sensor.
type fakeFloat struct{}

func (fakeFloat) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementPower
}
func (fakeFloat) MatterFloatValue() (float64, bool) { return 0, false }

// TestSwitch_DefaultClusterServersBaseline verifies that a fresh Switch exposes
// exactly three cluster servers: OnOff (0x0006), Groups (0x0004), and
// ScenesManagement (0x0062) — the mandatory baseline for the OnOffPlugInUnit
// device-type per matter.js HEAD on-off-plug-in-unit.element.ts.
func TestSwitch_DefaultClusterServersBaseline(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	servers := s.MatterClusterServers()
	if len(servers) != 3 {
		t.Fatalf("MatterClusterServers() = %d entries, want 3 (OnOff + Groups + ScenesManagement)", len(servers))
	}
	wantIDs := []uint32{0x0006, 0x0004, 0x0062}
	wantNames := []string{"OnOff", "Groups", "ScenesManagement"}
	for i, want := range wantIDs {
		if got := servers[i].MatterClusterID(); got != want {
			t.Errorf("servers[%d] ClusterID = 0x%04X, want 0x%04X (%s)", i, got, want, wantNames[i])
		}
	}
}

// TestSwitch_AttachPowerAddsCluster verifies that AttachPowerSource adds a
// fourth cluster server with ClusterID 0x0090 (ElectricalPowerMeasurement)
// after the three mandatory baseline clusters.
func TestSwitch_AttachPowerAddsCluster(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	s.AttachPowerSource(fakeFloat{})
	servers := s.MatterClusterServers()
	if len(servers) != 4 {
		t.Fatalf("MatterClusterServers() = %d entries, want 4", len(servers))
	}
	wantIDs := []uint32{0x0006, 0x0004, 0x0062, 0x0090}
	wantNames := []string{"OnOff", "Groups", "ScenesManagement", "ElectricalPowerMeasurement"}
	for i, want := range wantIDs {
		if got := servers[i].MatterClusterID(); got != want {
			t.Errorf("servers[%d] ClusterID = 0x%04X, want 0x%04X (%s)", i, got, want, wantNames[i])
		}
	}
}

// TestSwitch_AttachEnergyAddsCluster verifies that AttachEnergySource adds a
// fourth cluster server with ClusterID 0x0091 (ElectricalEnergyMeasurement)
// after the three mandatory baseline clusters.
func TestSwitch_AttachEnergyAddsCluster(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	s.AttachEnergySource(fakeFloat{})
	servers := s.MatterClusterServers()
	if len(servers) != 4 {
		t.Fatalf("MatterClusterServers() = %d entries, want 4", len(servers))
	}
	wantIDs := []uint32{0x0006, 0x0004, 0x0062, 0x0091}
	wantNames := []string{"OnOff", "Groups", "ScenesManagement", "ElectricalEnergyMeasurement"}
	for i, want := range wantIDs {
		if got := servers[i].MatterClusterID(); got != want {
			t.Errorf("servers[%d] ClusterID = 0x%04X, want 0x%04X (%s)", i, got, want, wantNames[i])
		}
	}
}

// TestSwitch_AttachBothAddsTwoClusters verifies that attaching both power and
// energy sources results in five cluster servers: OnOff, Groups,
// ScenesManagement, ElectricalPower, and ElectricalEnergy.
func TestSwitch_AttachBothAddsTwoClusters(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	s.AttachPowerSource(fakeFloat{})
	s.AttachEnergySource(fakeFloat{})
	servers := s.MatterClusterServers()
	if len(servers) != 5 {
		t.Fatalf("MatterClusterServers() = %d entries, want 5", len(servers))
	}
	wantIDs := []uint32{0x0006, 0x0004, 0x0062, 0x0090, 0x0091}
	wantNames := []string{"OnOff", "Groups", "ScenesManagement", "ElectricalPowerMeasurement", "ElectricalEnergyMeasurement"}
	for i, want := range wantIDs {
		if got := servers[i].MatterClusterID(); got != want {
			t.Errorf("servers[%d] ClusterID = 0x%04X, want 0x%04X (%s)", i, got, want, wantNames[i])
		}
	}
}

// TestSwitch_AttachPowerNilClears verifies that calling AttachPowerSource(nil)
// after a prior attachment reduces the cluster count back to 3 (the baseline
// OnOff + Groups + ScenesManagement).
func TestSwitch_AttachPowerNilClears(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	s.AttachPowerSource(fakeFloat{})
	// Confirm attachment is visible.
	if n := len(s.MatterClusterServers()); n != 4 {
		t.Fatalf("precondition: expected 4 servers after attach, got %d", n)
	}
	s.AttachPowerSource(nil)
	servers := s.MatterClusterServers()
	if len(servers) != 3 {
		t.Fatalf("MatterClusterServers() after nil clear = %d entries, want 3", len(servers))
	}
	wantIDs := []uint32{0x0006, 0x0004, 0x0062}
	wantNames := []string{"OnOff", "Groups", "ScenesManagement"}
	for i, want := range wantIDs {
		if got := servers[i].MatterClusterID(); got != want {
			t.Errorf("servers[%d] ClusterID = 0x%04X, want 0x%04X (%s)", i, got, want, wantNames[i])
		}
	}
}
