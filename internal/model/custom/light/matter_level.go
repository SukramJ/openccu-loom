// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"time"

	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The light's LevelControl runs on the hand-off path of go-fabric's
// server: the kit builds [levelcontrol.NativeSource] over [lightRamp]
// (levelcontroldef.Declare{Ramp}), which keeps Options and OnLevel,
// applies the ExecuteIfOff gate, turns each of the eight commands into
// one target level and one duration, and couples the "with On/Off"
// commands to the device's OnOff server — the device ramps natively
// through RAMP_TIME. The transition engine is not used on purpose — it
// would write one LEVEL per 100 ms step over the radio for the whole
// transition, where one atomic {LEVEL, RAMP_TIME} put_paramset lets the
// dimmer do the same. RemainingTime therefore reads 0
// (notes/parity/by_design.md BD-Matter-LevelControl-NativeRamp).

// lightRamp is the [levelcontroldef.NativeRamp] of a [Light]: the
// dimmer's single LEVEL knob, ramped by the device through RAMP_TIME.
type lightRamp struct{ l *Light }

var _ levelcontroldef.NativeRamp = lightRamp{}

// Level implements [levelcontroldef.NativeRamp]: the observed brightness
// on the 0..254 scale, unknown until the CCU answered.
func (r lightRamp) Level() (uint8, bool) {
	b, observed := r.l.Brightness()
	if !observed {
		return 0, false
	}
	return brightnessToMatter(b), true
}

// Ramp implements [levelcontroldef.NativeRamp]: LEVEL to level, over d
// where the channel carries RAMP_TIME. A "with On/Off" command to
// MinLevel is LEVEL=0 — an HM dimmer's single knob is its On/Off too, so
// the device couples the two itself (Matter §1.6.4.1.2, chip
// LevelControlCluster.cpp:359-361). The OnOff attribute follows: the
// source switches it off once the ramp to the minimum has arrived — on
// the LEVEL confirmation the ramp notifies ([lightRamp.OnMatterValueChanged]),
// or at the ramp's deadline — and the LEVEL confirmation corrects the
// value if the device answered otherwise.
func (r lightRamp) Ramp(ctx context.Context, level uint8, d time.Duration, withOnOff bool) error {
	l := r.l
	off := withOnOff && level <= levelcontroldef.LightingLevelMin
	target := matterLevelToHM(level)
	if off {
		target = 0
	}
	if d > 0 && l.Capabilities.Transition {
		// A positive transition becomes the device-side ramp: HM dimmers
		// ramp natively when RAMP_TIME accompanies LEVEL in one atomic
		// put_paramset, so the whole transition is the device's.
		return l.setLevelRamped(ctx, target, d, matterDispatchPriority)
	}
	return l.SetLevel(ctx, target, matterDispatchPriority)
}

// Stop implements [levelcontroldef.NativeRamp]: the classic HM dimmers
// (HM-LC-Dim*) carry a RAMP_STOP action that halts a running ramp at the
// current level; HmIP dimmers carry none, and a Stop there is a Success
// that changes nothing.
func (r lightRamp) Stop(ctx context.Context) error {
	l := r.l
	if !l.hasRampStop || l.Float == nil || l.Writer == nil {
		return nil
	}
	ctx = custom.EnsureContext(ctx)
	addr := l.DataPointKey().ChannelAddress
	if err := l.Writer.SetValue(ctx, addr, hmenum.ParameterRampStop, true, matterDispatchPriority); err != nil {
		return errors.Join(errors.New("light: RAMP_STOP"), err)
	}
	return nil
}

// OnMatterValueChanged implements [contract.ChangeNotifier] on the ramp:
// the native source settles an owed switch-off the moment the device
// notifies, so a ramp to the minimum that the CCU confirmed switches the
// OnOff attribute off then, not only at the ramp's deadline.
func (r lightRamp) OnMatterValueChanged(cb func()) func() {
	return r.l.OnMatterValueChanged(cb)
}

// setLevelRamped drives LEVEL to target over the given ramp duration.
// The off direction (target 0, only reachable via the WithOnOff
// MinLevel coupling) goes through [Light.TurnOffWithRamp]; every other
// target goes through [Light.TurnOnWith] — both bundle
// {LEVEL, RAMP_TIME, ON_TIME=TimerNotUsed} into one atomic put_paramset
// so the device performs the transition natively.
func (l *Light) setLevelRamped(ctx context.Context, target float64, ramp time.Duration, priority hmenum.CommandPriority) error {
	if target == 0 {
		return l.TurnOffWithRamp(ctx, ramp, priority)
	}
	return l.TurnOnWith(ctx, OnConfig{Brightness: &target, RampTime: &ramp}, priority)
}
