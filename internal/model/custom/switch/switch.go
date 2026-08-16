// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package switchdev implements the Switch custom data point — a thin
// wrapper around [generic.Switch] that keeps the pre-refactor API for
// existing callers and delegates state, optimistic bookkeeping, and
// SetOnTime to the generic layer.
package switchdev

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// startUpOnOffNull is the sentinel stored in Switch.startUpOnOff when
// the attribute is null (Matter quality X on StartUpOnOff 0x4003).
// The uint32 guard value (above 0xFF) cannot be confused with an enum8.
const startUpOnOffNull uint32 = 0xFFFFFFFF

// Writer is an alias for [custom.Writer] so existing callers of
// switchdev.Writer keep compiling.
type Writer = custom.Writer

// Switch is a single on/off data point composed on top of
// [*generic.Switch].
type Switch struct {
	*generic.Switch
	custom.BaseDP

	groupState *custom.GroupState

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every successful MatterWrite / MatterInvoke
	// so DataVersionFilter evaluation correctly detects cluster changes.
	hmtypes.DataVersionTracker

	// Optional Matter measurement-source attachments populated via
	// [Switch.AttachPowerSource] / [Switch.AttachEnergySource]. When
	// set they appear as additional cluster servers on the Switch's
	// bridged Matter endpoint (ElectricalPowerMeasurement 0x0090 +
	// ElectricalEnergyMeasurement 0x0091). Unset for switches that have
	// no energy hardware (the default HmIP-FSM3 / etc.).
	//
	// Held atomically, like the writable OnOff attributes below: the
	// ingest pipeline attaches them one stage *after* the custom data
	// point is published to its channel, so the Matter assembler can be
	// walking this Switch from the IM goroutine while they are written.
	attachedPowerSource  atomic.Pointer[interfaces.MatterFloatMeasurementSource]
	attachedEnergySource atomic.Pointer[interfaces.MatterFloatMeasurementSource]

	// LT-feature writable OnOff attributes stored atomically so
	// MatterRead and MatterWrite race safely without a dedicated mutex.
	// matter.js OnOffServer.ts:80 (offWaitTime), :102 (onTime), :39 (startUpOnOff).
	onTime       atomic.Uint32 // uint16 seconds; 0 = none
	offWaitTime  atomic.Uint32 // uint16 seconds; 0 = none
	startUpOnOff atomic.Uint32 // startUpOnOffNull = null (default)

	// globalSceneControl mirrors the LT-gated GlobalSceneControl
	// attribute (0x4000): true after On / OnWithTimedOff /
	// OnWithRecallGlobalScene, false after OffWithEffect; a plain Off
	// leaves it unchanged. Defaults to true. matter.js
	// packages/node/src/behaviors/on-off/OnOffServer.ts:97-104 (on),
	// :158-169 (offWithEffect). Held directly on Switch (rather than a
	// separate cluster-server projection) so the value survives
	// [Switch.MatterClusterServers] reconstruction.
	globalSceneControl atomic.Bool
}

// New constructs a Switch that wraps the channel's existing
// STATE [*generic.Switch] (registered by the device pipeline via
// [device.Channel.Put]). The wire-level DP IS the embedded DP, so
// CCU value-change events flow into the same DataPoint[bool]
// instance the Matter Subscribe listener is wired to.
//
// Without embedding the channel's STATE DP, a CCU-pushed STATE
// echo would land on the channel's wire-DP but never reach the
// custom-wrapper's listeners, so external state changes (wall
// switch, MQTT, ReGa) never surface to Matter.
//
// Returns nil when ch carries no *generic.Switch for STATE — the
// channel either does not expose STATE in its VALUES paramset or
// the wire-DP was registered with a different concrete type. The
// caller (ipSwitchConstructor / rfSwitchConstructor) treats nil as
// "skip custom-DP registration on this channel".
func New(ch *device.Channel) *Switch {
	sw := custom.SwitchField(ch, hmenum.ParameterState)
	if sw == nil {
		return nil
	}
	s := &Switch{
		Switch:     sw,
		groupState: custom.NewGroupState(),
	}
	s.startUpOnOff.Store(startUpOnOffNull)
	s.globalSceneControl.Store(true)
	s.registerSwitchServices()
	// Bump the OnOff cluster's DataVersion on every CCU-confirmed STATE
	// transition (Matter §10.6.5: DataVersion MUST monotonically advance
	// whenever any cluster attribute changes). Without this, Apple
	// HAP-Mapper dedupes ReportData with an unchanged DataVersion and
	// the HMOutlet projection stops mirroring relay flips that
	// originate outside Matter (wall switch, MQTT, REST, ReGa).
	// Mirrors matter.js packages/node/src/behavior/Behavior.ts where
	// `events.<attr>$Changed` auto-advances the cluster's dataVersion.
	s.OnConfirmedUpdate(func(_, _ bool) { s.Bump() })
	return s
}

// Address returns the channel address the Switch writes to.
func (s *Switch) Address() string { return s.DataPointKey().ChannelAddress }

// IsOn mirrors the pre-refactor accessor and returns (on, observed).
func (s *Switch) IsOn() (on, observed bool) { return s.Value() }

// IsRefreshed reports whether the underlying STATE wire DP has been observed
// at least once. Single-slot custom DPs delegate straight to the embedded
// data point's observation flag — no need to allocate an AggregateView.
func (s *Switch) IsRefreshed() bool {
	_, ok := s.RawValue()
	return ok
}

// SubDataPointKeys returns the wire identifier of the underlying
// STATE data point.
func (s *Switch) SubDataPointKeys() []hmtypes.DataPointKey {
	return []hmtypes.DataPointKey{s.DataPointKey()}
}

// OnState is the ingestion alias preserved for backwards compatibility.
func (s *Switch) OnState(on bool) { s.OnEvent(on) }

// GroupState returns the group-membership tracker. Returns the same instance
// across calls.
func (s *Switch) GroupState() *custom.GroupState { return s.groupState }

// IsStateChange reports whether the next on/off write is materially a
// state change. Returns true when:
//   - no value has been observed yet (first command always goes through), or
//   - an on-time timer is currently running or has been deferred via
//     [SetTimerOnTime] (the arming side-effect must reach the wire even
//     when the boolean target matches the current state), or
//   - the desired target differs from the last observed state.
func (s *Switch) IsStateChange(target bool) bool {
	if s.IsTimerStateChange() {
		return true
	}
	cur, ok := s.IsOn()
	if !ok {
		return true
	}
	return cur != target
}

// TurnOn gates via [IsStateChange] before delegating to [generic.Switch.TurnOn].
// When the switch is already on (and no timer is pending), the wire write is
// suppressed to avoid spurious CCU events.
func (s *Switch) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	if !s.IsStateChange(true) {
		return nil
	}
	return s.Switch.TurnOn(ctx, priority)
}

// TurnOff gates via [IsStateChange] and clears any deferred ON_TIME timer
// before issuing the STATE=false write. Clearing the timer prevents a pending
// SetTimerOnTime call from re-opening the output after the off command lands.
func (s *Switch) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	if !s.IsStateChange(false) {
		return nil
	}
	s.ResetTimerOnTime()
	return s.Switch.TurnOff(ctx, priority)
}

// TurnOnFor switches the relay on for `d` and bundles ON_TIME + STATE into
// one atomic put_paramset (when the writer supports it).
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching of ON_TIME + STATE.
func (s *Switch) TurnOnFor(ctx context.Context, d time.Duration, priority hmenum.CommandPriority) error {
	ctx = custom.EnsureContext(ctx)
	if s.Writer == nil {
		return s.TurnOnWithTimer(ctx, d, priority)
	}
	coll := generic.NewCollector(generic.WriterAsBackend(s.Writer), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	// ON_TIME + STATE are only staged by TurnOnWithTimer; the wire call
	// happens in the flush, so its error is the result of the command.
	return generic.FlushCollector(ctx, coll, s.TurnOnWithTimer(ctx, d, priority))
}

// SetTimerOnTime stores `d` for the next [TurnOn] call.
func (s *Switch) SetTimerOnTime(d time.Duration) { s.Switch.SetTimerOnTime(d) }
