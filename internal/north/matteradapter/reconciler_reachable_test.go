// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"testing"

	bdbidef "thingfabric.dev/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	"thingfabric.dev/go-fabric/fabrictest"
	"thingfabric.dev/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// reachableEvents reads the ReachableChanged events the node logged for the
// bridged device at addr, as a controller reads the event log, and returns
// their reachable values in order.
func (s *pairedSite) reachableEvents(t *testing.T, addr string) []bool {
	t.Helper()
	events, err := fabrictest.Events(s.ctx, s.pair, im.ConcreteEventPath{
		Endpoint: s.endpointOf(t, addr), HasEndpoint: true,
		Cluster: bdbidef.ClusterID, HasCluster: true,
		Event: bdbidef.EventReachableChanged, HasEvent: true,
	})
	if err != nil {
		t.Fatalf("read ReachableChanged: %v", err)
	}
	got := make([]bool, 0, len(events))
	for i := range events {
		var payload bdbidef.ReachableChangedEvent
		if err := payload.DecodeTLV(events[i].Value); err != nil {
			t.Fatalf("decode ReachableChanged: %v", err)
		}
		got = append(got, payload.ReachableNewValue)
	}
	return got
}

// TestReconcilerSetReachableReportsEachFlipOnce pins the forward's target:
// a flip of the physical device reaches the served Reachable attribute and
// fires exactly one ReachableChanged event; a repeated value fires none.
func TestReconcilerSetReachableReportsEachFlipOnce(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000201"
	s.sync(t, switchDevice(addr, "Lamp"))
	if got := s.reachableEvents(t, addr); len(got) != 0 {
		t.Fatalf("events before any flip = %v, want none", got)
	}

	s.rec.SetReachable(s.ctx, "ccu1", addr, false)
	if s.bridgedInfo(t, addr).Reachable {
		t.Error("Reachable after the flip to unreachable = true, want false")
	}
	if got := s.reachableEvents(t, addr); len(got) != 1 || got[0] {
		t.Fatalf("events after one flip = %v, want [false]", got)
	}

	s.rec.SetReachable(s.ctx, "ccu1", addr, false)
	if got := s.reachableEvents(t, addr); len(got) != 1 {
		t.Fatalf("events after a repeated value = %v, want still [false]", got)
	}

	s.rec.SetReachable(s.ctx, "ccu1", addr, true)
	if !s.bridgedInfo(t, addr).Reachable {
		t.Error("Reachable after the flip back = false, want true")
	}
	if got := s.reachableEvents(t, addr); len(got) != 2 || !got[1] {
		t.Fatalf("events after the flip back = %v, want [false true]", got)
	}
}

// TestReconcilerStatesTheModelsAvailabilityBeforeBinding pins the seed: a
// device the model holds as unreachable when the reconciler first binds it
// is served unreachable — a go-fabric device starts reachable, and no flip
// will come for a state that was already there.
func TestReconcilerStatesTheModelsAvailabilityBeforeBinding(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000202"
	dev := switchDevice(addr, "Lamp")
	dev.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceFalse)
	s.sync(t, dev)
	if s.bridgedInfo(t, addr).Reachable {
		t.Fatal("a device the model holds unreachable is served reachable")
	}
}

// TestReconcilerKeepsReachabilityAcrossAFirmwareRebuild pins the two halves
// of a rebuilt device: the source builds a new device on a firmware
// update, which the reconciler states the model's availability on before
// it replaces the held one, and a later flip reaches the new device, not
// the replaced one.
func TestReconcilerKeepsReachabilityAcrossAFirmwareRebuild(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000203"
	dev := switchDevice(addr, "Lamp")
	dev.Model = "HmIP-PS"
	dev.Firmware().Set(device.FirmwareInfo{Current: "1.0.0"})
	s.sync(t, dev)
	before := s.endpointOf(t, addr)

	dev.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceFalse)
	s.rec.SetReachable(s.ctx, "ccu1", addr, false)
	dev.Firmware().Set(device.FirmwareInfo{Current: "1.1.0"})
	s.sync(t, dev)
	if got := s.endpointOf(t, addr); got != before {
		t.Fatalf("endpoint after the rebuild = %d, want %d", got, before)
	}
	st := s.bridgedInfo(t, addr)
	if st.SoftwareVersionString != "1.1.0" {
		t.Fatalf("SoftwareVersionString = %q, want 1.1.0", st.SoftwareVersionString)
	}
	if st.Reachable {
		t.Fatal("the rebuilt device is served reachable while the model holds it unreachable")
	}

	dev.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityNotSet)
	s.rec.SetReachable(s.ctx, "ccu1", addr, true)
	if !s.bridgedInfo(t, addr).Reachable {
		t.Fatal("a flip after the rebuild did not reach the served device")
	}
}

// TestReconcilerStatesARenameOnTheHeldDevice pins that a changed name is a
// statement on the held device, not a replacement: the NodeLabel is the
// new name, the endpoint keeps its number and no ConfigurationVersion is
// raised.
func TestReconcilerStatesARenameOnTheHeldDevice(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000204"
	dev := switchDevice(addr, "Lamp")
	s.sync(t, dev)
	before := s.bridgedInfo(t, addr)
	ep := s.endpointOf(t, addr)

	dev.SetName("Reading lamp")
	s.sync(t, dev)
	after := s.bridgedInfo(t, addr)
	if after.NodeLabel == before.NodeLabel {
		t.Fatalf("NodeLabel after the rename = %q, unchanged", after.NodeLabel)
	}
	if got := s.endpointOf(t, addr); got != ep {
		t.Errorf("endpoint after the rename = %d, want %d", got, ep)
	}
	if after.ConfigurationVersion != before.ConfigurationVersion {
		t.Errorf("a rename moved ConfigurationVersion %d -> %d", before.ConfigurationVersion, after.ConfigurationVersion)
	}
}
