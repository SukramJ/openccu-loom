// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Action is a write-only parameter whose TYPE is ACTION. It carries
// no observable state — every Trigger invocation sends the current
// value to the CCU, and the CCU never emits an acknowledging event.
//
// Use Action for parameters like SUBMIT, RESET_MOTION, or CONFIRM —
// cases where the CCU expects any write to count as "do it now".
type Action struct {
	*DataPoint[any]
}

// NewAction constructs an Action.
func NewAction(cfg Spec) *Action {
	return &Action{DataPoint: NewDataPoint[any](cfg)}
}

// Trigger sends value on the wire. For ACTION parameters the value is
// usually a bare bool "true"; callers that need a different shape can
// pass it through.
func (a *Action) Trigger(ctx context.Context, value any, priority hmenum.CommandPriority) error {
	if !a.IsWritable() && a.Descriptor.Type != hmenum.ParameterTypeAction {
		return ErrNotWritable
	}
	if a.Writer == nil {
		return ErrNoWriter
	}
	return a.Writer.SetValue(
		ctx,
		a.Key.ChannelAddress,
		hmenum.Parameter(a.Key.Parameter),
		value,
		priority,
	)
}

// MatterMeasurementClass implements
// [interfaces.MatterMeasurementSource]. Press-event parameters
// (PRESS_SHORT / PRESS_LONG / …) surface as MomentarySwitch (Matter
// §1.13 GenericSwitch); ACTION parameters with no press semantics
// opt out by returning None.
func (a *Action) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	switch hmenum.Parameter(a.Key.Parameter) {
	case hmenum.ParameterPress, hmenum.ParameterPressShort,
		hmenum.ParameterPressLong, hmenum.ParameterPressLongStart,
		hmenum.ParameterPressLongRelease, hmenum.ParameterPressCont:
		return interfaces.MatterMeasurementMomentarySwitch
	default:
		return interfaces.MatterMeasurementNone
	}
}

// MatterSwitchPositions implements [cluster/wire.GenericSwitchSource].
func (a *Action) MatterSwitchPositions() uint8 { return 2 }

// MatterSwitchSupportsLongPress implements
// [cluster/wire.GenericSwitchSource].
func (a *Action) MatterSwitchSupportsLongPress() bool {
	switch hmenum.Parameter(a.Key.Parameter) {
	case hmenum.ParameterPressLong, hmenum.ParameterPressLongStart,
		hmenum.ParameterPressLongRelease, hmenum.ParameterPressCont:
		return true
	default:
		return false
	}
}

// WireMatterSwitchHandler mirrors [Button.WireMatterSwitchHandler]
// for Action DPs. ACTION-typed parameters fire on every Trigger
// invocation; the value comparison in OnUpdate is therefore against
// any non-nil change, since Action's underlying type is `any`.
func (a *Action) WireMatterSwitchHandler(h MatterSwitchEventEmitter) func() {
	if h == nil {
		return func() {}
	}
	param := hmenum.Parameter(a.Key.Parameter)
	return a.OnUpdate(func(_, next any) {
		if next == nil {
			return
		}
		// A `false` boolean is also a no-op — Action ACTION wire
		// type usually carries `true` to indicate "fire", but
		// defensively skip.
		if b, ok := next.(bool); ok && !b {
			return
		}
		switch param {
		case hmenum.ParameterPress, hmenum.ParameterPressShort:
			h.FireInitialPress(1)
			h.FireShortRelease(0)
		case hmenum.ParameterPressLong, hmenum.ParameterPressLongStart:
			h.FireInitialPress(1)
			h.FireLongPress(1)
		case hmenum.ParameterPressLongRelease:
			h.FireLongRelease(0)
		case hmenum.ParameterPressCont:
			h.FireLongPress(1)
		default:
			// Action DPs project only the press / long-press parameter
			// family onto Matter GenericSwitch events. Other Parameter
			// values reaching this hook (e.g. non-action wire types on
			// a misconfigured custom DP) are silently ignored.
		}
	})
}
