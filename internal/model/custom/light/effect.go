// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// EffectLight is a [ColorLight] that additionally exposes a PROGRAM
// parameter selecting one of a predefined set of effects (e.g. "Off",
// "Slow color change", "Medium color change", "Fast color change",
// "Campfire", "Waterfall", "TV simulation").
//
// The effect list is sourced from the PROGRAM data point's VALUE_LIST
// at construction; callers select by index.
type EffectLight struct {
	*ColorLight

	program *generic.Integer
	effects []string
}

// colorDimmerEffects is the effect vocabulary of the RF colour dimmers.
// It is hard-coded because the device is: PROGRAM is a bare INTEGER
// 0..255 with no VALUE_LIST, so the wire carries no names at all and the
// CCU's own configuration surface labels the parameter "program number".
// The seven entries and their order are the reference's
// (`CustomDpColorDimmerEffect._effects`, light.py:478-486) — the index
// written to PROGRAM is the position in this list, so the order is the
// contract, not a presentation choice.
var colorDimmerEffects = []string{
	"Off",
	"Slow color change",
	"Medium color change",
	"Fast color change",
	"Campemit",
	"Waterfall",
	"TV simulation",
}

// NewEffectLight constructs an EffectLight against the channel from cfg.
//
// programChannel names the channel carrying PROGRAM. The RF colour
// dimmers keep it two channels above the light's own (HM-LC-RGBW-WM
// reports PROGRAM on channel 3 while the light sits on channel 1), which
// is what the profile's channel-field mapping says; passing nil falls
// back to the light's own channel.
func NewEffectLight(cfg Config) *EffectLight {
	return newEffectLightOn(cfg, cfg.Channel, nil)
}

// newEffectLightOn is [NewEffectLight] with an explicit PROGRAM channel
// and an explicit COLOR channel — the RF colour dimmers spread LEVEL,
// COLOR and PROGRAM over three consecutive channels.
func newEffectLightOn(cfg Config, programChannel, colorChannel *device.Channel) *EffectLight {
	cl := newColorLightOn(cfg, colorChannel)
	if programChannel == nil {
		programChannel = cfg.Channel
	}
	prog := custom.IntegerField(programChannel, hmenum.ParameterProgram)
	var effects []string
	if programChannel != nil {
		if dp := programChannel.Parameter(hmenum.ParameterProgram); dp != nil {
			effects = append([]string(nil), dp.ParameterData().ValueList...)
		}
	}
	if len(effects) == 0 && prog != nil {
		// The device advertises no names for its programs. Substituting
		// the vocabulary the actuator actually implements is what makes
		// the effect surface work at all: without it every label the
		// daemon offers is one no lookup can resolve, so every selection
		// is refused and the reported effect stays empty forever.
		effects = append([]string(nil), colorDimmerEffects...)
	}
	el := &EffectLight{
		ColorLight: cl,
		program:    prog,
		effects:    effects,
	}
	if el.Float != nil {
		el.registerEffectLightServices()
	}
	if prog != nil {
		_ = prog.OnConfirmedUpdate(func(_, _ int32) { el.dataVersion.Bump() })
	}
	return el
}

// NamePostfix overrides [ColorLight.NamePostfix] with the "effect"
// Suffix
func (l *EffectLight) NamePostfix() string { return "effect" }

// Subscribe overrides [ColorLight.Subscribe] to also replay the PROGRAM
// (effect) DP when it already carries an observed value. Without the replay,
// a reconnect that runs Subscribe after the initial CCU push would leave the
// effect index stale until the next push event.
func (l *EffectLight) Subscribe(ch *device.Channel) func() {
	unsub := l.ColorLight.Subscribe(ch)
	if l.program != nil {
		if v, observed := l.program.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.program.OnEvent(iv)
			}
		}
	}
	return unsub
}

// Effects returns the labels of all effects this light supports (sourced from
// the PROGRAM VALUE_LIST).
func (l *EffectLight) Effects() []string {
	if l.effects == nil {
		return nil
	}
	return append([]string(nil), l.effects...)
}

// Effect returns the currently selected effect index and label.
// Returns observed=false when no effect has been observed yet.
func (l *EffectLight) Effect() (idx int32, label string, observed bool) {
	if l.program == nil {
		return 0, "", false
	}
	v, ok := l.program.Value()
	if !ok {
		return 0, "", false
	}
	if int(v) >= 0 && int(v) < len(l.effects) {
		return v, l.effects[v], true
	}
	return v, "", true
}

// TurnOn overrides [Light.TurnOn] to handle the effect-reset behaviour:
// when turn_on is called without an explicit Effect target and the light
// currently has an active effect (index != 0), a reset to effect 0 is sent
// first so the CCU clears the running pattern before applying the new
// brightness. When an explicit Effect is requested the reset is skipped.
//
// Mirrors CustomDpColorDimmerEffect.turn_on (light.py:496-511).
func (l *EffectLight) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	if l.program != nil {
		if idx, _, observed := l.Effect(); observed && idx != 0 {
			// Active effect: reset to 0 before the brightness command so the CCU
			// clears the running pattern. The subsequent TurnOn from Light resets
			// the level, matching the collector_order=5 / collector_order=95 split
			// in the Python reference.
			if err := l.program.Set(custom.EnsureContext(ctx), 0, priority); err != nil {
				return fmt.Errorf("effectlight: reset PROGRAM: %w", err)
			}
		}
	}
	return l.ColorLight.TurnOn(ctx, priority)
}

// SetEffect selects an effect by its index in [Effects].
//
// Asks IsStateChangeFull first, matching the turn_on guard pattern. That
// check cannot suppress a repeat today — the commanded-effect accessor
// it consults holds no state (see the hook block in light.go) — so a
// repeated effect command does reach the CCU.
func (l *EffectLight) SetEffect(ctx context.Context, idx int32, priority hmenum.CommandPriority) error {
	if l.program == nil {
		return errors.New("effectlight: channel missing PROGRAM")
	}
	if idx < 0 || (len(l.effects) > 0 && int(idx) >= len(l.effects)) {
		return fmt.Errorf("effectlight: effect index %d out of range [0,%d)", idx, len(l.effects))
	}
	var effectLabel string
	if int(idx) < len(l.effects) {
		effectLabel = l.effects[idx]
	}
	if !l.IsStateChangeFull(StateChangeArgsFull{Effect: &effectLabel}) {
		return nil
	}
	if err := l.program.Set(custom.EnsureContext(ctx), idx, priority); err != nil {
		return fmt.Errorf("effectlight: SET PROGRAM: %w", err)
	}
	return nil
}

// SetEffectByLabel selects an effect by its label as exposed in
// [Effects]. Returns an error when the label is unknown.
func (l *EffectLight) SetEffectByLabel(ctx context.Context, label string, priority hmenum.CommandPriority) error {
	for i, e := range l.effects {
		if e == label {
			return l.SetEffect(ctx, int32(i), priority) //nolint:gosec // bounded by len(l.effects); see #20
		}
	}
	return fmt.Errorf("effectlight: unknown effect label %q", label)
}
