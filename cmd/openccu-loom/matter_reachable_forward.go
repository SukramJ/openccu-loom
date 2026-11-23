// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// wireMatterReachableForward forwards every central's device-availability
// signal to notify, which states it on the physical device's bridged devices
// (matteradapter.Reconciler.SetReachable): their Reachable attribute, and on
// a change the §9.13.6 ReachableChanged event.
//
// It rides the registry observer, which replays over the centrals present at
// boot and runs for every one adopted later: the forward used to be wired
// twice, by a boot walk and again by the live-adopt hook. The returned remove
// drops every subscription.
func wireMatterReachableForward(reg *central.Registry, notify func(centralName, address string, reachable bool)) (remove func()) {
	if reg == nil || notify == nil {
		return func() {}
	}
	return reg.OnRegisterDeclared(wiring.Seam{
		Name:         "matter.reachable_forward",
		Collaborator: "subscribeMatterDeviceReachable",
		Phase:        wiring.PhasePerCentral,
		Why:          "a CCU device going unreachable, or coming back, never reaches a subscribed Matter controller: the ReachableChanged event and the Reachable report are pushed only by this forward",
	}, func(u *central.Unit) func() {
		return subscribeMatterDeviceReachable(u, notify)
	})
}

// subscribeMatterDeviceReachable forwards one central's AvailabilityChanged
// lifecycle events to notify. It returns the unsubscribe closure, or nil when
// the central has no bus to subscribe to.
func subscribeMatterDeviceReachable(u *central.Unit, notify func(centralName, address string, reachable bool)) func() {
	if u == nil || u.EventBus == nil || notify == nil {
		return nil
	}
	cName := u.Name()
	return u.EventBus.Subscribe(func(e hmevent.DeviceLifecycleEvent) {
		if e.Subtype != hmenum.DeviceLifecycleSubtypeAvailabilityChanged {
			return
		}
		notify(cName, e.Address, e.Available)
	})
}
