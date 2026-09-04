// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// FireAction implements [ActionTrigger]. The CCU treats any write to an
// ACTION parameter as "do it now", so the bare bool is the canonical
// trigger value.
func (a *Action) FireAction(ctx context.Context, priority hmenum.CommandPriority) error {
	return a.Trigger(ctx, true, priority)
}

// MatterMeasurementClass implements
// [interfaces.MatterMeasurementSource]. Press-event parameters
// (PRESS_SHORT / PRESS_LONG / …) surface as MomentarySwitch (Matter
// §1.13 GenericSwitch); ACTION parameters with no press semantics
// opt out by returning None.
func (a *Action) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	if isPressParameter(hmenum.Parameter(a.Key.Parameter)) {
		return interfaces.MatterMeasurementMomentarySwitch
	}
	return interfaces.MatterMeasurementNone
}

// MatterSwitchPositions implements [cluster/wire.GenericSwitchSource].
func (a *Action) MatterSwitchPositions() uint8 { return 2 }

// MatterSwitchSupportsLongPress implements
// [cluster/wire.GenericSwitchSource].
func (a *Action) MatterSwitchSupportsLongPress() bool {
	return isLongPressParameter(hmenum.Parameter(a.Key.Parameter))
}

// WireMatterSwitchHandler mirrors [Button.WireMatterSwitchHandler]
// for Action DPs. Single-DP convenience wrapper: it wires a
// [ButtonGroup] of one so a lone Action follows the same Matter §1.13
// press-cycle state machine (PRESS_CONT suppression, LongPress
// synthesis before LongRelease) as a fully populated channel group.
// The Matter endpoint assembler does not use this path — it
// consolidates every press DP of a channel into one shared ButtonGroup.
func (a *Action) WireMatterSwitchHandler(h MatterSwitchEventEmitter) func() {
	return NewButtonGroup(a).WireMatterSwitchHandler(h)
}
