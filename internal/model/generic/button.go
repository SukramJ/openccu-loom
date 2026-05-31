// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Button represents a CCU button channel — a stateless trigger that
// always "sends true" when the user presses it. Unlike [Action], the
// Button parameter is a specific PRESS_* variant, and no type
// conversion occurs.
type Button struct {
	*DataPoint[bool]
}

// NewButton constructs a Button. Optimistic tracking is force-disabled
// because button presses are stateless triggers — the CCU never echoes a
// confirmation, so a tracker would always run into the rollback timeout.
func NewButton(cfg Spec) *Button {
	cfg.OptimisticDisabled = true
	b := &Button{DataPoint: NewDataPoint[bool](cfg)}
	b.RegisterService("press", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.Press(ctx, priority)
	})
	return b
}

// Press fires the button.
func (b *Button) Press(ctx context.Context, priority hmenum.CommandPriority) error {
	if !b.IsWritable() && b.Descriptor.Type != hmenum.ParameterTypeAction {
		return ErrNotWritable
	}
	return b.sendAndObserve(ctx, true, true, priority)
}

// MatterMeasurementClass implements [interfaces.MatterMeasurementSource].
// Press-event parameters surface as MomentarySwitch (Matter §1.13
// GenericSwitch); other parameter shapes opt out by returning None.
func (b *Button) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	switch hmenum.Parameter(b.Key.Parameter) {
	case hmenum.ParameterPress, hmenum.ParameterPressShort,
		hmenum.ParameterPressLong, hmenum.ParameterPressLongStart,
		hmenum.ParameterPressLongRelease, hmenum.ParameterPressCont:
		return interfaces.MatterMeasurementMomentarySwitch
	default:
		return interfaces.MatterMeasurementNone
	}
}

// MatterSwitchPositions implements
// [cluster/wire.GenericSwitchSource]. HM buttons advertise two
// positions: idle + pressed. Multi-tap counters are tracked via the
// MultiPress* events, not the position attribute.
func (b *Button) MatterSwitchPositions() uint8 { return 2 }

// MatterSwitchSupportsLongPress implements
// [cluster/wire.GenericSwitchSource]. True when the source parameter
// is one of the long-press variants (PRESS_LONG, PRESS_LONG_START,
// PRESS_LONG_RELEASE, PRESS_CONT).
func (b *Button) MatterSwitchSupportsLongPress() bool {
	switch hmenum.Parameter(b.Key.Parameter) {
	case hmenum.ParameterPressLong, hmenum.ParameterPressLongStart,
		hmenum.ParameterPressLongRelease, hmenum.ParameterPressCont:
		return true
	default:
		return false
	}
}

// MatterSwitchEventEmitter is the receiver-side surface
// `Button.WireMatterSwitchHandler` drives. The wire-side
// `cluster/wire.GenericSwitch` satisfies this contract by
// implementing the four Fire* methods directly.
type MatterSwitchEventEmitter interface {
	FireInitialPress(newPosition uint8)
	FireShortRelease(previousPosition uint8)
	FireLongPress(newPosition uint8)
	FireLongRelease(previousPosition uint8)
}

// WireMatterSwitchHandler subscribes the receiver to this Button's
// value-change stream and dispatches the appropriate Matter §1.13
// GenericSwitch event(s) on each press transition. Returns an
// idempotent unsubscribe closure.
//
// Parameter → event mapping:
//
//	PRESS_SHORT, PRESS         → InitialPress + ShortRelease
//	PRESS_LONG, PRESS_LONG_START → InitialPress + LongPress
//	PRESS_LONG_RELEASE         → LongRelease
//	PRESS_CONT                 → LongPress (continuous; one event per
//	                             update — controllers apply their own
//	                             debounce window)
//
// The emitter's cluster server gates LongPress / LongRelease
// internally on the source's `MatterSwitchSupportsLongPress` so
// dispatching them on a short-press-only Button is a no-op.
func (b *Button) WireMatterSwitchHandler(h MatterSwitchEventEmitter) func() {
	if h == nil {
		return func() {}
	}
	param := hmenum.Parameter(b.Key.Parameter)
	return b.OnUpdate(func(_, next bool) {
		if !next {
			// Only emit on the rising edge — HM presses are
			// momentary; the falling edge to false is the implicit
			// "release" the cluster surfaces via ShortRelease /
			// LongRelease above.
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
			// Button DPs only project the press / long-press parameter
			// family onto Matter GenericSwitch events. Any other
			// Parameter value reaching this hook is silently ignored.
		}
	})
}
