// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write carries: a tap in a controller app is a foreground path and must
// not queue behind a background refresh. Spelled out because the zero
// value of [hmenum.CommandPriority] is Critical.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// Compile-time assertions: a generic STATE switch participates in the
// Matter source surface (ADR 0012) as an OnOffPlugInUnit endpoint whose
// OnOff cluster is go-fabric's [onoff.Server] (ADR 0084 phase 2).
var (
	_ interfaces.MatterEndpointSource = (*Switch)(nil)
	_ interfaces.MatterChangeNotifier = (*Switch)(nil)
)

// matterGenericSwitchDeviceType is OnOffPlugInUnit (0x010A): the nearest
// Device Library type for a bare writable STATE channel with no profile.
// It mandates the LT feature on OnOff, which the server advertises.
const matterGenericSwitchDeviceType uint16 = onoff.DeviceTypeOnOffPlugInUnit

// matterGenericSwitchEligible reports whether a generic Switch projects to
// Matter at all. Only STATE does: every other writable bool parameter a
// generic Switch may wrap (INHIBIT, ON_TIME-style flags, config toggles)
// is an operational setting with no Matter cluster, and exposing it as an
// OnOffPlugInUnit would present a controller with a "switch" that is not
// one.
func matterGenericSwitchEligible(p hmenum.Parameter) bool {
	return p == hmenum.ParameterState
}

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *Switch) MatterDeviceType() uint16 { return matterGenericSwitchDeviceType }

// MatterClusterServers implements [interfaces.MatterEndpointSource]: the
// OnOff cluster, served by one [onoff.Server] per switch. Returns nil
// for a switch whose parameter is not STATE, so the assembler skips the
// channel. The Groups and ScenesManagement clusters the device type
// mandates are stack state the endpoint assembler mounts.
func (s *Switch) MatterClusterServers() []interfaces.MatterClusterServer {
	if s == nil || !matterGenericSwitchEligible(s.Parameter()) {
		return nil
	}
	return []interfaces.MatterClusterServer{s.matterOnOffServer()}
}

// matterOnOffServer builds the switch's OnOff server once. The Sink is
// the STATE write at [matterDispatchPriority] — On through
// [Switch.TurnOn] so a configured ON_TIME companion applies, Off through
// [Switch.Set] — and the data point's confirmed-update hook feeds the
// CCU's state back through [onoff.Server.Set], the one path that stores
// without asking the Sink, so a physical button press or a CCU program
// reaches a subscribed controller and a confirmed command does not echo
// into a second write.
func (s *Switch) matterOnOffServer() *onoff.Server {
	return s.matterOnOff.Server(func() *onoff.Server {
		on, _ := s.Value()
		srv := MustMatterOnOffServer(MatterOnOffConfig{
			Lighting:    true,
			DataVersion: &s.matterVersion,
			Initial:     on,
			Sink: func(ctx context.Context, on bool) error {
				if on {
					return s.TurnOn(ctx, matterDispatchPriority)
				}
				return s.Set(ctx, false, matterDispatchPriority)
			},
		})
		feedback := NewMatterOnOffFeedback(srv)
		_ = s.OnConfirmedUpdate(func(_, on bool) { feedback.Set(on) })
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if on, observed := s.Value(); observed {
			srv.Set(on)
		}
		return srv
	})
}
