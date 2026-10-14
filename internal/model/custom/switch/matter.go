// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"context"

	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write carries. The bridge is a controller-facing foreground path — a
// tap in a Matter app must not queue behind a background refresh — so it
// dispatches at High.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached these calls defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// Compile-time assertions: Switch participates in the Matter source
// surface (ADR 0012) as an OnOffPlugInUnit endpoint whose OnOff cluster
// is go-fabric's [onoff.Server] (ADR 0084 phase 2).
var (
	_ interfaces.MatterEndpointSource     = (*Switch)(nil)
	_ interfaces.MatterClusterDataVersion = (*Switch)(nil)
	// Inherited from the embedded *generic.Switch: an external STATE flip
	// (physical button / CCU program) propagates to Apple's Subscribe.
	_ interfaces.MatterChangeNotifier = (*Switch)(nil)
)

// matterDeviceTypeOnOffPlugInUnit is the Device Library type a switching
// actuator advertises; the endpoint assembler may upgrade it to
// OnOffLight (0x0100) when the hosting model classifies the channel as a
// light role. The type mandates the LT feature on OnOff, which the server
// advertises; the cluster's ids, revision and lists come from go-fabric's
// generated definition and are pinned against the matter.js snapshot by
// TestHmLgtSwitchOnOffMatchesMatterJS.
const matterDeviceTypeOnOffPlugInUnit uint16 = onoff.DeviceTypeOnOffPlugInUnit

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *Switch) MatterDeviceType() uint16 { return matterDeviceTypeOnOffPlugInUnit }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Switch contributes OnOff. The Groups + ScenesManagement clusters the
// Device Library makes mandatory for OnOffPlugInUnit (0x010A) are stack
// state: the endpoint assembler mounts both from the node's group state
// (go-fabric endpoint/materialize.go mountGroups / mountScenes).
//
// Electrical measurement is deliberately absent. ElectricalPowerMeasurement
// (0x0090) and ElectricalEnergyMeasurement (0x0091) used to be attached here
// from sibling channels, but the Device Library specifies neither for 0x010A
// in any role — its carrier is ElectricalSensor (0x0510), which also makes
// PowerTopology mandatory alongside them. The metering channel now projects
// its own consolidated ElectricalSensor endpoint; see
// matteradapter.ElectricalGroupDPKey.
func (s *Switch) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{s.matterOnOffServer()}
}

// matterOnOffServer builds the switch's OnOff server once. The Sink is
// the profile's own [Switch.TurnOn] / [Switch.TurnOff] at
// [matterDispatchPriority] — group-state aware and gated on
// [Switch.IsStateChange] like every other caller — and the STATE data
// point's confirmed-update hook feeds the CCU's state back through
// [onoff.Server.Set], the one path that stores without asking the Sink,
// so a wall switch or a CCU program reaches a subscribed controller and a
// confirmed command does not echo into a second write.
//
// The server starts from the observed STATE, false while nothing was
// observed: Apple Home's HAP mapper drops the whole OnOffPlugInUnit
// accessory when OnOff is null on the first Subscribe report (verified
// empirically: bridge pair succeeds, sensors visible, the switch absent),
// and the OnOff attribute is mandatory and non-nullable
// (on-off.element.ts:29), so the server never reports null.
func (s *Switch) matterOnOffServer() *onoff.Server {
	return s.matterOnOff.Server(func() *onoff.Server {
		on, _ := s.IsOn()
		srv := generic.MustMatterOnOffServer(generic.MatterOnOffConfig{
			Lighting:    true,
			DataVersion: &s.DataVersionTracker,
			Initial:     on,
			Sink: func(ctx context.Context, on bool) error {
				if on {
					return s.TurnOn(ctx, matterDispatchPriority)
				}
				return s.TurnOff(ctx, matterDispatchPriority)
			},
		})
		_ = s.OnConfirmedUpdate(func(_, on bool) { srv.Set(on) })
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if on, observed := s.IsOn(); observed {
			srv.Set(on)
		}
		return srv
	})
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion]:
// the OnOff cluster's counter, bumped by the server on every change it
// stores and by the STATE confirmed-update hook in [New], so
// DataVersionFilter evaluation detects both a command and a CCU-side
// flip.
func (s *Switch) MatterDataVersion() uint32 { return s.Current() }
