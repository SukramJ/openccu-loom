// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package valve implements the valve custom data point.
//
// Two concrete flavours:
//   - [Irrigation] — a STATE-switched on/off valve with an optional
//     ON_TIME duration for garden / drip systems; composes
//     [*generic.Switch].
//   - [Modulating] — a LEVEL-based 0–1 proportional valve (radiator
//     thermostat wrap); composes [*generic.Float].
package valve

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Writer is an alias for [custom.Writer].
type Writer = custom.Writer

// --- Irrigation valve ---

// GroupStateDataPoint is the read side of a group-channel STATE slot.
//
// The IPIrrigationValve profile maps [hmenum.FieldGroupState] to STATE
// on the channel one below the valve's own (ChannelFields[-1]) — a
// status transmitter that mirrors the valve for HA / MQTT / REST
// without being the valve's own STATE. That companion parameter's wire
// shape differs by model: most report it read-only (OPERATIONS lacks
// WRITE, resolving to a [*generic.BinarySensor]), while a few keep it
// writable (a [*generic.Switch]). Naming the capability instead of a
// shape is what keeps both reachable.
type GroupStateDataPoint interface {
	Value() (bool, bool)
	Parameter() hmenum.Parameter
	ModifiedAt() time.Time
}

var (
	_ GroupStateDataPoint = (*generic.Switch)(nil)
	_ GroupStateDataPoint = (*generic.BinarySensor)(nil)
)

// Irrigation is an on/off valve with an optional programmable run time.
type Irrigation struct {
	*generic.Switch
	custom.BaseDP

	groupState *custom.GroupState

	// groupStateDP is the optional group-channel STATE indicator bound
	// by [applyGroupState]. Nil when the profile carries no
	// FieldGroupState mapping or the device does not expose it on the
	// resolved channel.
	groupStateDP GroupStateDataPoint
}

// NewIrrigation constructs an Irrigation valve that wraps the
// channel's existing STATE [*generic.Switch] (registered by the
// device pipeline via [device.Channel.Put]). The wire-level DP IS
// the embedded DP, so CCU value-change events flow into the same
// DataPoint[bool] instance the north-bound listeners are wired to.
//
// Without embedding the channel's STATE DP, a CCU-pushed STATE
// echo would land on the channel's wire-DP but never reach the
// custom-wrapper's listeners.
//
// Returns nil when ch carries no *generic.Switch for STATE.
func NewIrrigation(ch *device.Channel, group custom.RebasedChannelGroupConfig) *Irrigation {
	sw := custom.SwitchField(custom.ResolveSlotOr(ch, group, hmenum.FieldState, hmenum.ParameterState))
	if sw == nil {
		return nil
	}
	v := &Irrigation{
		Switch:     sw,
		groupState: custom.NewGroupState(),
	}
	v.registerIrrigationServices()
	return v
}

// GroupState returns the group-membership tracker.
func (v *Irrigation) GroupState() *custom.GroupState { return v.groupState }

// SetGroupState binds an optional group-channel STATE data point. Used
// by the materializer ([applyGroupState]) for irrigation valves whose
// profile schema names a companion group-level STATE indicator on a
// sibling channel. Pass nil to clear.
//
// The bound value is exposed only through [Irrigation.GroupStateValue];
// it never overwrites [Irrigation.IsOpen], which always reflects this
// channel's own valve state — the group indicator and the channel's own
// STATE are different slots with different meanings.
func (v *Irrigation) SetGroupState(dp GroupStateDataPoint) { v.groupStateDP = dp }

// GroupStateValue returns the bound group-channel STATE indicator's
// value and whether it has been observed. Returns (false, false) when
// the profile carries no FieldGroupState mapping or the device does not
// expose it on the resolved channel.
func (v *Irrigation) GroupStateValue() (on, observed bool) {
	if v.groupStateDP == nil {
		return false, false
	}
	return v.groupStateDP.Value()
}

// IsStateChange reports whether the next open/close write is materially
// a state change. Returns true when:
//   - no value has been observed yet (first command always goes through), or
//   - an on-time timer is currently running or has been deferred (the
//     arming side-effect must reach the wire), or
//   - `target` differs from the last observed open/closed state.
func (v *Irrigation) IsStateChange(target bool) bool {
	if v.IsTimerStateChange() {
		return true
	}
	cur, ok := v.IsOpen()
	if !ok {
		return true
	}
	return cur != target
}

// Address returns the channel address.
func (v *Irrigation) Address() string { return v.DataPointKey().ChannelAddress }

// IsRefreshed reports whether the irrigation valve's STATE data point has
// been observed at least once.
func (v *Irrigation) IsRefreshed() bool {
	if v.Switch == nil {
		return false
	}
	return v.Switch.IsRefreshed()
}

// IsOpen reports whether the valve is open and whether its state has
// been observed.
func (v *Irrigation) IsOpen() (open, observed bool) { return v.Value() }

// OnState records a CCU STATE update.
func (v *Irrigation) OnState(open bool) { v.OnEvent(open) }

// Open opens the valve. When duration > 0 the operation is sent as a
// single atomic put_paramset bundle of {ON_TIME, STATE}. Without
// duration the call collapses to a single SetValue.
//
// When duration > 0 a [generic.CallParameterCollector] is attached to
// ctx for forward-compatible batching of ON_TIME + STATE. Mirrors
func (v *Irrigation) Open(ctx context.Context, duration time.Duration, priority hmenum.CommandPriority) error {
	ctx = custom.EnsureContext(ctx)
	if duration > 0 {
		if v.Writer == nil {
			return v.TurnOnWithTimer(ctx, duration, priority)
		}
		coll := generic.NewCollector(generic.WriterAsBackend(v.Writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		// ON_TIME + STATE are only staged by TurnOnWithTimer; the wire
		// call happens in the flush, so its error is the command result.
		return generic.FlushCollector(ctx, coll, v.TurnOnWithTimer(ctx, duration, priority))
	}
	if !v.IsStateChange(true) {
		return nil
	}
	return v.Set(ctx, true, priority)
}

// Close closes the valve immediately. Any pending ON_TIME deferred by a
// prior Open(duration) call is cancelled first so the timer does not
// re-open the valve after the close write lands on the wire.
func (v *Irrigation) Close(ctx context.Context, priority hmenum.CommandPriority) error {
	if !v.IsStateChange(false) {
		return nil
	}
	v.ResetTimerOnTime()
	return v.Set(custom.EnsureContext(ctx), false, priority)
}

// --- Modulating valve ---

// Modulating is a 0–1 position-valued valve (radiator thermostat
// wrap).
type Modulating struct {
	*generic.Float
	custom.BaseDP
}

// Category reports the HA data-point category. Explicit override: the
// embedded wire DP (*generic.Switch / *generic.Float) would otherwise
// promote its own Category() ("switch"/"number") and clients would
// spawn the valve on the wrong platform.
func (v *Irrigation) Category() hmenum.DataPointCategory { return hmenum.DataPointCategoryValve }

// Category reports the HA data-point category (see [Irrigation.Category]).
func (v *Modulating) Category() hmenum.DataPointCategory { return hmenum.DataPointCategoryValve }

// NewModulating constructs a Modulating valve that wraps the
// channel's existing LEVEL [*generic.Float] (registered by the
// device pipeline via [device.Channel.Put]).
//
// Returns nil when ch carries no *generic.Float for LEVEL.
func NewModulating(ch *device.Channel, group custom.RebasedChannelGroupConfig) *Modulating {
	lf := custom.FloatField(custom.ResolveSlotOr(ch, group, hmenum.FieldLevel, hmenum.ParameterLevel))
	if lf == nil {
		return nil
	}
	v := &Modulating{Float: lf}
	v.registerModulatingServices()
	return v
}

// Address returns the channel address.
func (v *Modulating) Address() string { return v.DataPointKey().ChannelAddress }

// IsRefreshed reports whether the modulating valve's LEVEL data point has
// been observed at least once.
func (v *Modulating) IsRefreshed() bool {
	if v.Float == nil {
		return false
	}
	return v.Float.IsRefreshed()
}

// Level returns the current valve position.
func (v *Modulating) Level() (custom.Position, bool) {
	f, ok := v.Value()
	if !ok {
		return custom.Position{}, false
	}
	return custom.NewPosition(f), true
}

// OnLevel records a CCU LEVEL update.
func (v *Modulating) OnLevel(f float64) { v.OnEvent(f) }

// IsStateChange reports whether commanding the given target position is a
// material state change. Returns true when no value has been observed yet.
// Treats differences smaller than 0.005 as equal to avoid spurious writes
// caused by floating-point round-trips across the CCU wire.
func (v *Modulating) IsStateChange(target float64) bool {
	if v.Float == nil {
		return true
	}
	cur, ok := v.Value()
	if !ok {
		return true
	}
	return math.Abs(cur-target) >= 0.005
}

// SetLevel commands a new position.
func (v *Modulating) SetLevel(ctx context.Context, f float64, priority hmenum.CommandPriority) error {
	p := custom.NewPosition(f)
	if err := v.Set(custom.EnsureContext(ctx), p.Level(), priority); err != nil {
		return fmt.Errorf("valve: SET level: %w", err)
	}
	return nil
}
