// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"time"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// matterLevelServer builds the light's LevelControl server once, on the
// hand-off path of go-fabric's server (Transitions nil): go-fabric's
// [levelcontrol.NativeSource] keeps Options and OnLevel, applies the
// ExecuteIfOff gate, turns each of the eight commands into one target
// level and one duration, and hands both to [lightRamp] — the device
// ramps natively through RAMP_TIME. The transition engine is not used on
// purpose — it would write one LEVEL per 100 ms step over the radio for
// the whole transition, where one atomic {LEVEL, RAMP_TIME} put_paramset
// lets the dimmer do the same. RemainingTime therefore reads 0
// (notes/parity/by_design.md BD-Matter-LevelControl-NativeRamp).
func (l *Light) matterLevelServer() *levelcontrol.Server {
	l.matterLevelOnce.Do(func() {
		l.matterLevelSrv = levelcontrol.NewServer(levelcontrol.Config{
			Source: levelcontrol.NewNativeSource(lightRamp{l: l}, lightOnOff{l: l},
				levelcontrol.NativeConfig{Lighting: true}),
			DataVersion: &l.dataVersion,
			Lighting:    true,
		})
	})
	return l.matterLevelSrv
}

// lightRamp is the [levelcontrol.NativeRamp] of a [Light]: the dimmer's
// single LEVEL knob, ramped by the device through RAMP_TIME.
type lightRamp struct{ l *Light }

var _ levelcontrol.NativeRamp = lightRamp{}

// Level implements [levelcontrol.NativeRamp]: the observed brightness on
// the 0..254 scale, unknown until the CCU answered.
func (r lightRamp) Level() (uint8, bool) {
	b, observed := r.l.Brightness()
	if !observed {
		return 0, false
	}
	return brightnessToMatter(b), true
}

// Ramp implements [levelcontrol.NativeRamp]: LEVEL to level, over d where
// the channel carries RAMP_TIME. A "with On/Off" command to MinLevel is
// LEVEL=0 — an HM dimmer's single knob is its On/Off too, so the device
// couples the two itself (Matter §1.6.4.1.2, chip
// LevelControlCluster.cpp:359-361) — and the OnOff attribute follows the
// accepted write at once through the OnOff server's own Set: the host's
// change, so the OnOff Sink is not asked for a second CCU write, and the
// LEVEL confirmation corrects the value if the device answered otherwise.
// The source's own switch-off cannot do this: it switches off only while
// [levelcontrol.OnOff.OnOff] reports on, and [lightOnOff.OnOff] reads the
// light's effective level, which the accepted LEVEL=0 write has already
// made off — so the source sees nothing left to switch, for any d.
func (r lightRamp) Ramp(ctx context.Context, level uint8, d time.Duration, withOnOff bool) error {
	l := r.l
	off := withOnOff && level <= levelcontrol.LightingLevelMin
	target := matterLevelToHM(level)
	if off {
		target = 0
	}
	if d > 0 && l.Capabilities.Transition {
		// A positive transition becomes the device-side ramp: HM dimmers
		// ramp natively when RAMP_TIME accompanies LEVEL in one atomic
		// put_paramset, so the whole transition is the device's.
		if err := l.setLevelRamped(ctx, target, d, matterDispatchPriority); err != nil {
			return err
		}
	} else if err := l.SetLevel(ctx, target, matterDispatchPriority); err != nil {
		return err
	}
	if off {
		l.matterOnOffServer().Set(false)
	}
	return nil
}

// Stop implements [levelcontrol.NativeRamp]: the classic HM dimmers
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

// lightOnOff is the [levelcontrol.OnOff] the light's LevelControl couples
// to: the ExecuteIfOff gate reads the light's own on state (the single
// LEVEL knob: LEVEL>0 is on), and a "with On/Off" command sets only the
// OnOff attribute. The ramp that follows already switches the dimmer, so
// a second CCU write would cost radio budget and flash the old level.
type lightOnOff struct{ l *Light }

var _ levelcontrol.OnOff = lightOnOff{}

// OnOff implements [levelcontrol.OnOff].
func (o lightOnOff) OnOff() bool {
	on, _ := o.l.IsOn()
	return on
}

// SetOnOff implements [levelcontrol.OnOff]: the attribute through the
// OnOff server's Set — the host's own change, so the Sink is not called.
func (o lightOnOff) SetOnOff(_ context.Context, on bool) error {
	o.l.matterOnOffServer().Set(on)
	return nil
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
