// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"thingfabric.dev/go-fabric/cluster/onoff"
	fabricdevice "thingfabric.dev/go-fabric/device"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// confirmedHookCount is how many confirmed-update hooks the switch's
// data point carries.
func confirmedHookCount(s *Switch) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.confirmedUpdateCallbacks)
}

// matterSwitchDeviceFor returns the device a switch projects for host.
func matterSwitchDeviceFor(t *testing.T, s *Switch, host interfaces.MatterHostFacts) *fabricdevice.Device {
	t.Helper()
	d, err := s.MatterDevice(interfaces.MatterDeviceRequest{
		Options: fabricdevice.Options{StableKey: fabricdevice.StringKey("switch"), Name: "switch"},
		Host:    host,
	})
	if err != nil {
		t.Fatalf("MatterDevice: %v", err)
	}
	if d == nil {
		t.Fatal("MatterDevice: a STATE switch must project a device")
	}
	return d
}

// A changed host fact is a new device, the same facts the held one; the
// model hooks are registered once and feed whichever device the holder
// holds when they fire, so a confirmation after a rebuild reaches the
// new device's OnOff server, not the detached first one.
func TestSwitchMatter_RebuildOnHostFactsFeedsTheNewDevice(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	hostA := interfaces.MatterHostFacts{Identity: interfaces.MatterStatedIdentity{SoftwareVersionString: "1.0"}}
	hostB := interfaces.MatterHostFacts{Identity: interfaces.MatterStatedIdentity{SoftwareVersionString: "1.1"}}

	first := matterSwitchDeviceFor(t, s, hostA)
	hooksAfterFirst := confirmedHookCount(s)

	second := matterSwitchDeviceFor(t, s, hostB)
	if second == first {
		t.Fatal("a changed SoftwareVersionString returned the held device; go-fabric takes the stated identity only at construction, so it must be a new one")
	}
	if again := matterSwitchDeviceFor(t, s, hostB); again != second {
		t.Fatalf("the same host facts returned a different device (%p vs %p); the holder rebuilds only on a change", again, second)
	}
	if got := confirmedHookCount(s); got != hooksAfterFirst {
		t.Fatalf("confirmed-update hooks after the rebuild = %d, want %d: Bind must run once, not per build", got, hooksAfterFirst)
	}

	s.OnEvent(true)

	if v, ok := matterOnOffOf(t, second).MatterRead(onoff.AttrOnOff); !ok || v != true {
		t.Fatalf("new device OnOff after a confirmed STATE=true = (%v, %v), want (true, true): the hook fed a detached device", v, ok)
	}
	if v, ok := matterOnOffOf(t, first).MatterRead(onoff.AttrOnOff); !ok || v != false {
		t.Errorf("detached first device OnOff = (%v, %v), want (false, true): nothing feeds a device the holder no longer holds", v, ok)
	}
}
