// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Switch is a bool-typed writable data point with an optional ON_TIME
// companion that limits how long the output stays high.
type Switch struct {
	*DataPoint[bool]
	TimerSlot

	// matterLT holds the Lighting-feature OnOff attributes the Matter
	// projection answers. See internal/model/generic/switch_matter.go.
	matterLT matterOnOffLTState
}

// NewSwitch constructs a Switch.
func NewSwitch(cfg Spec) *Switch {
	s := &Switch{DataPoint: NewDataPoint[bool](cfg)}
	s.matterLT.initMatterOnOffLT()
	s.RegisterService("turn_on", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return s.TurnOn(ctx, priority)
	})
	s.RegisterService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return s.Set(ctx, false, priority)
	})
	s.RegisterService("set", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := paramBool(params, "value")
		if err != nil {
			return err
		}
		return s.Set(ctx, v, priority)
	})
	return s
}

// Set writes on under the underlying parameter.
func (s *Switch) Set(ctx context.Context, on bool, priority hmenum.CommandPriority) error {
	if !s.IsWritable() {
		return ErrNotWritable
	}
	return s.sendAndObserve(ctx, on, on, priority)
}

// TurnOn switches the output on. When a deferred timer is pending
// (set via [SetTimerOnTime]) or `onTime` is non-zero, the operation
// is sent as a single atomic put_paramset bundle of {ON_TIME, STATE}
// Mirrors.
// The deferred timer is consumed by every TurnOn call.
func (s *Switch) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	return s.turnOnWithTimer(ctx, nil, priority)
}

// TurnOnWithTimer switches the output on for `onTime` and clears any
// previously deferred timer. Sends ON_TIME + STATE atomically when the writer
// is a [ParamsetWriter].
func (s *Switch) TurnOnWithTimer(ctx context.Context, onTime time.Duration, priority hmenum.CommandPriority) error {
	return s.turnOnWithTimer(ctx, &onTime, priority)
}

// TurnOff switches the output off. When `rampTime` is positive (set
// from a wrapper / not directly here) the caller should use the
// per-domain helper to bundle. The plain TurnOff is a single
// SetValue.
func (s *Switch) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	return s.Set(ctx, false, priority)
}

// SetOnTime writes the paired ON_TIME parameter on the same channel, in
// seconds. Value is clamped to ≥ 0.
//
// Seconds is the CCU's own unit on both stacks, and the CCU owns the
// encoding, so the raw figure must not be pre-scaled here: BidCos declares
// `<logical type="float" min="0.0" max="85825945.6" unit="s"/>` and applies
// a ×10 scale followed by an 11+5-bit tinyfloat
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_s_644.xml:274-279), and the
// HmIP receivers declare `.Unit=s`
// (../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:271, :625,
// :688, :691) with the
// same tinyfloat converter (HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createOnTimeParameter,
// max 8580000.0).
//
// Only the lower bound is clamped. The two stacks declare different
// ceilings, and neither is readable from a single place at this layer, so
// an oversized duration is reshaped or refused downstream rather than
// rejected here.
func (s *Switch) SetOnTime(ctx context.Context, d time.Duration, priority hmenum.CommandPriority) error {
	if s.Writer == nil {
		return ErrNoWriter
	}
	seconds := d.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	// Route through the collector when the caller opened one, so ON_TIME
	// travels in the same wire call as the STATE it qualifies. Writing it
	// directly is what split a bounded switch-on into two radio
	// transmissions even though the caller had staged them together.
	if coll := CollectorFromContext(ctx); coll != nil {
		if err := coll.AddParam(
			s.Key.ChannelAddress, s.Key.ParamsetKey, string(hmenum.ParameterOnTime), seconds, 0,
		); err == nil {
			return nil
		}
		// A consumed collector falls through to the direct write.
	}
	if err := s.Writer.SetValue(
		ctx,
		s.Key.ChannelAddress,
		hmenum.ParameterOnTime,
		seconds,
		priority,
	); err != nil {
		return fmt.Errorf("switch: set ON_TIME: %w", err)
	}
	return nil
}

// turnOnWithTimer is the shared implementation of [TurnOn]
// [TurnOnWithTimer]. When `explicit` is non-nil, the timer is taken
// from it; otherwise the previously deferred [SetTimerOnTime] value
// is consumed. When neither yields a timer, the call collapses to
// the plain Set(true) behaviour.
func (s *Switch) turnOnWithTimer(ctx context.Context, explicit *time.Duration, priority hmenum.CommandPriority) error {
	if !s.IsWritable() {
		return ErrNotWritable
	}
	var onTime *time.Duration
	if explicit != nil {
		onTime = explicit
	} else {
		onTime = s.consumePending()
	}
	// A nil or negative timer means "no timer requested" — collapse to a
	// plain STATE=true write. An explicit zero is carried through, but not
	// because it is an opcode: neither stack has a timer-cancel value. On
	// BidCos the LEVEL_SET frame carries `omit_if="0"` on its ON_TIME field
	// (../OpenCCU-Base/src/devicetypes/rftypes/rf_s_644.xml:230; FLAG_OMIT
	// means "leave the parameter out of the frame when its value equals
	// this one" — ../OpenCCU-Base/src/libhsscomm/FrameDescription.h:60), so
	// an encoded 0 vanishes from the radio frame and the device sees a
	// plain unbounded switch-on. `<reset_after_send param="ON_TIME"/>` at
	// :270 then restores ON_TIME's default of 0.0 after every STATE write,
	// so a stale timer cannot leak either. Carrying the zero is therefore a
	// no-op on BidCos, not a necessity — it is kept so the caller's explicit
	// intent is not silently reshaped into a different wire call.
	//
	// What ON_TIME=0 does on a plain HmIP VALUES write is UNVERIFIED: the
	// legacy layer passes a FLOAT through unchanged, and whether the valve
	// firmware reads a zero direct-execution time as "no timer" is device
	// behaviour no readable CCU source states.
	if onTime == nil || *onTime < 0 {
		return s.Set(ctx, true, priority)
	}
	// A collector in the context owns the bundling: staging both values
	// lets it decide the wire shape, and it is the only path that can
	// also fold in whatever else the caller staged for this channel.
	// Trying the writer's paramset capability first would bypass it and
	// send this pair on its own.
	if coll := CollectorFromContext(ctx); coll != nil {
		errOn := coll.AddParam(s.Key.ChannelAddress, s.Key.ParamsetKey,
			string(hmenum.ParameterOnTime), onTime.Seconds(), 0)
		errState := coll.Add(s, true, 0)
		if errOn == nil && errState == nil {
			// Send stages the optimistic value and keeps the rollback
			// closure; applying it here would drop that closure and
			// show the switch as on before the wire call happened.
			return nil
		}
		// A consumed collector falls through to the direct paths below.
	}
	// Atomic ON_TIME + STATE via put_paramset when supported.
	if pw, ok := s.Writer.(ParamsetWriter); ok {
		seconds := onTime.Seconds()
		values := map[string]any{
			string(hmenum.ParameterOnTime):            seconds,
			string(hmenum.Parameter(s.Key.Parameter)): true,
		}
		rb := s.ApplyOptimistic(true)
		if err := pw.PutParamset(ctx, s.Key.ChannelAddress, hmenum.ParamsetKeyValues, values, priority); err != nil {
			// Wire failed → roll back the optimistic STATE immediately so the
			// user-visible value stays truthful, instead of lingering until the
			// optimistic-update timeout (#3238). Mirrors the direct-send path in
			// sendAndObserve and the Channel.Set collector path.
			if rb != nil {
				rb()
			}
			return fmt.Errorf("switch: turn-on with timer: %w", err)
		}
		return nil
	}
	// Fallback: ON_TIME first (so the device honours it once STATE
	// flips), then STATE.
	if err := s.SetOnTime(ctx, *onTime, priority); err != nil {
		return err
	}
	return s.Set(ctx, true, priority)
}
