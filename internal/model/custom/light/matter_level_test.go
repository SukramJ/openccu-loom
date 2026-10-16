// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"maps"
	"math"
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"
	"github.com/SukramJ/go-fabric/cluster/onoff"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// levelCall is one southbound write the light issued: a SetValue (param
// set) or an atomic put_paramset (put set).
type levelCall struct {
	param hmenum.Parameter
	value any
	prio  hmenum.CommandPriority
	put   map[string]any
}

// levelRecWriter records every SetValue and PutParamset the light
// issues, so a test sees which parameter went out, with which value and
// priority, and whether the write was the atomic {LEVEL, RAMP_TIME}
// put_paramset of a device-side ramp.
type levelRecWriter struct {
	mu    sync.Mutex
	calls []levelCall
}

func (w *levelRecWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, value any, prio hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, levelCall{param: p, value: value, prio: prio})
	return nil
}

func (w *levelRecWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, prio hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	w.calls = append(w.calls, levelCall{put: cp, prio: prio})
	return nil
}

func (w *levelRecWriter) snapshot() []levelCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]levelCall(nil), w.calls...)
}

// newLevelRig builds a dimmer channel carrying LEVEL and, with rampStop,
// the RAMP_STOP action of the classic HM-LC-Dim* dimmers. The light is
// constructed after the channel is complete, as the device layer does,
// because the RAMP_STOP probe runs at construction.
func newLevelRig(t *testing.T, caps custom.LightCapabilities, rampStop bool) (*Light, *levelRecWriter) {
	t.Helper()
	const address = "LVL0001:1"
	w := &levelRecWriter{}
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "LVL0001"})
	ch := d.AddChannel(address, 1, "DIMMER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	if rampStop {
		ch.Put(generic.NewAction(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterRampStop),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeAction,
				Operations: hmenum.OperationsWrite,
			},
			Writer: w,
		}))
	}
	l := New(Config{Channel: ch, Writer: w, Capabilities: caps})
	return l, w
}

func invokeLevel(t *testing.T, l *Light, cmd uint32, fields any) {
	t.Helper()
	if _, err := levelServer(t, l).MatterInvoke(context.Background(), cmd, fields); err != nil {
		t.Fatalf("MatterInvoke(0x%02X, %+v): %v", cmd, fields, err)
	}
}

func currentMatterLevel(t *testing.T, l *Light) uint8 {
	t.Helper()
	v, ok := levelServer(t, l).MatterRead(levelcontrol.AttrCurrentLevel)
	lvl, isU8 := v.(uint8)
	if !ok || !isU8 {
		t.Fatalf("CurrentLevel = (%v, %v), want an observed uint8", v, ok)
	}
	return lvl
}

// singleLevelSet asserts exactly one write went out, an instant SetValue
// of LEVEL, and returns its value.
func singleLevelSet(t *testing.T, calls []levelCall) float64 {
	t.Helper()
	if len(calls) != 1 || calls[0].put != nil || calls[0].param != hmenum.ParameterLevel {
		t.Fatalf("CCU writes = %+v, want exactly one instant LEVEL SetValue", calls)
	}
	f, _ := calls[0].value.(float64)
	return f
}

// singleRampPut asserts exactly one write went out, the atomic
// put_paramset of a device-side ramp, and returns LEVEL and RAMP_TIME.
func singleRampPut(t *testing.T, calls []levelCall) (level, ramp float64) {
	t.Helper()
	if len(calls) != 1 || calls[0].put == nil {
		t.Fatalf("CCU writes = %+v, want exactly one {LEVEL, RAMP_TIME} put_paramset", calls)
	}
	p := calls[0].put
	level, _ = p[string(hmenum.ParameterLevel)].(float64)
	r, ok := p[string(hmenum.ParameterRampTime)].(float64)
	if !ok {
		t.Fatalf("put_paramset %v carries no RAMP_TIME", p)
	}
	if on, _ := p[string(hmenum.ParameterOnTime)].(float64); on != custom.TimerNotUsed {
		t.Errorf("ON_TIME=%v, want the TimerNotUsed sentinel %v", p[string(hmenum.ParameterOnTime)], custom.TimerNotUsed)
	}
	return level, r
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func u8(v uint8) *uint8    { return &v }
func u16(v uint16) *uint16 { return &v }

// ─── ExecuteIfOff gate on the plain commands ───────────────────────────────

// TestLevelPlainCommandsWhileOffAreSilentSuccess pins the ExecuteIfOff
// gate on each of the four plain commands: while the light is off and no
// ExecuteIfOff is in effect, the command answers Success and nothing
// reaches the CCU (matter.js LevelControlServer.ts:729-736
// #optionsAllowExecution). The light is observed off with a known level,
// so Step and a rated Move would otherwise have a level to work from.
func TestLevelPlainCommandsWhileOffAreSilentSuccess(t *testing.T) {
	cases := []struct {
		name   string
		cmd    uint32
		fields any
	}{
		{"MoveToLevel", levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 100}},
		{"Move", levelcontrol.CmdMove, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, Rate: u8(10)}},
		{"Step", levelcontrol.CmdStep, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 20}},
		{"Stop", levelcontrol.CmdStop, levelcontrol.StopRequest{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true, Transition: true}, true)
			l.OnLevel(0)
			invokeLevel(t, l, tc.cmd, tc.fields)
			if calls := w.snapshot(); len(calls) != 0 {
				t.Fatalf("%s while off reached the CCU: %+v", tc.name, calls)
			}
		})
	}
}

// TestLevelPlainCommandsWhileOffExecuteWithExecuteIfOff pins the other
// side of the gate: ExecuteIfOff in effect — through the command's
// OptionsMask/OptionsOverride or through the stored Options attribute —
// lets each plain command reach the CCU while the light is off.
func TestLevelPlainCommandsWhileOffExecuteWithExecuteIfOff(t *testing.T) {
	const eio = levelcontrol.OptionExecuteIfOff
	cases := []struct {
		name   string
		cmd    uint32
		fields any
		want   hmenum.Parameter
	}{
		{"MoveToLevel", levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 100, OptionsMask: eio, OptionsOverride: eio}, hmenum.ParameterLevel},
		{"Move", levelcontrol.CmdMove, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, OptionsMask: eio, OptionsOverride: eio}, hmenum.ParameterLevel},
		{"Step", levelcontrol.CmdStep, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 20, OptionsMask: eio, OptionsOverride: eio}, hmenum.ParameterLevel},
		{"Stop", levelcontrol.CmdStop, levelcontrol.StopRequest{OptionsMask: eio, OptionsOverride: eio}, hmenum.ParameterRampStop},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/request override", func(t *testing.T) {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, true)
			l.OnLevel(0)
			invokeLevel(t, l, tc.cmd, tc.fields)
			calls := w.snapshot()
			if len(calls) != 1 || calls[0].param != tc.want {
				t.Fatalf("%s with ExecuteIfOff wrote %+v, want one %s write", tc.name, calls, tc.want)
			}
		})
	}

	t.Run("MoveToLevel/stored Options", func(t *testing.T) {
		l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
		l.OnLevel(0)
		if err := levelServer(t, l).MatterWrite(context.Background(), levelcontrol.AttrOptions, uint64(eio)); err != nil {
			t.Fatalf("MatterWrite(Options=ExecuteIfOff): %v", err)
		}
		invokeLevel(t, l, levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 100})
		if got := singleLevelSet(t, w.snapshot()); !near(got, matterLevelToHM(100)) {
			t.Fatalf("MoveToLevel(100) under stored ExecuteIfOff wrote %v, want %v", got, matterLevelToHM(100))
		}
	})

	t.Run("MoveToLevel/override clears stored Options", func(t *testing.T) {
		l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
		l.OnLevel(0)
		if err := levelServer(t, l).MatterWrite(context.Background(), levelcontrol.AttrOptions, uint64(eio)); err != nil {
			t.Fatalf("MatterWrite(Options=ExecuteIfOff): %v", err)
		}
		invokeLevel(t, l, levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 100, OptionsMask: eio, OptionsOverride: 0})
		if calls := w.snapshot(); len(calls) != 0 {
			t.Fatalf("a mask that overrides ExecuteIfOff to 0 still reached the CCU: %+v", calls)
		}
	})
}

// ─── With On/Off coupling ──────────────────────────────────────────────────

// TestLevelWithOnOffToMinLevelTurnsOffInOneWrite pins the coupling of
// every "with On/Off" variant that lands on MinLevel: LEVEL=0 goes out
// as the only CCU write, the OnOff attribute follows to false without a
// second write, and none of them is gated while the light is on.
func TestLevelWithOnOffToMinLevelTurnsOffInOneWrite(t *testing.T) {
	cases := []struct {
		name   string
		cmd    uint32
		fields any
	}{
		{"MoveToLevelWithOnOff MinLevel", levelcontrol.CmdMoveToLevelWithOnOff, levelcontrol.MoveToLevelRequest{Level: matterLevelMinDefault}},
		{"MoveToLevelWithOnOff 0 crops to MinLevel", levelcontrol.CmdMoveToLevelWithOnOff, levelcontrol.MoveToLevelRequest{Level: 0}},
		{"MoveWithOnOff down", levelcontrol.CmdMoveWithOnOff, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeDown}},
		{"StepWithOnOff down clamps to MinLevel", levelcontrol.CmdStepWithOnOff, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeDown, StepSize: 200}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
			l.OnLevel(0.5)
			oo := onOffServer(t, l)
			if v, _ := oo.MatterRead(onoff.AttrOnOff); v != true {
				t.Fatalf("precondition: OnOff = %v, want true", v)
			}
			invokeLevel(t, l, tc.cmd, tc.fields)
			if got := singleLevelSet(t, w.snapshot()); got != 0 {
				t.Fatalf("%s wrote LEVEL=%v, want 0 (MinLevel couples to off)", tc.name, got)
			}
			if v, _ := oo.MatterRead(onoff.AttrOnOff); v != false {
				t.Fatalf("OnOff after %s = %v, want false", tc.name, v)
			}
		})
	}
}

// TestLevelWithOnOffWhileOffTurnsOnInOneWrite pins the other direction:
// a coupled command above MinLevel executes on an off light without any
// ExecuteIfOff, and the OnOff attribute follows to true.
func TestLevelWithOnOffWhileOffTurnsOnInOneWrite(t *testing.T) {
	cases := []struct {
		name   string
		cmd    uint32
		fields any
		want   float64
	}{
		{"MoveToLevelWithOnOff", levelcontrol.CmdMoveToLevelWithOnOff, levelcontrol.MoveToLevelRequest{Level: 100}, matterLevelToHM(100)},
		{"MoveWithOnOff up", levelcontrol.CmdMoveWithOnOff, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp}, 1.0},
		{"StepWithOnOff up", levelcontrol.CmdStepWithOnOff, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 20}, matterLevelToHM(20)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
			l.OnLevel(0)
			oo := onOffServer(t, l)
			invokeLevel(t, l, tc.cmd, tc.fields)
			if got := singleLevelSet(t, w.snapshot()); !near(got, tc.want) {
				t.Fatalf("%s wrote LEVEL=%v, want %v", tc.name, got, tc.want)
			}
			if v, _ := oo.MatterRead(onoff.AttrOnOff); v != true {
				t.Fatalf("OnOff after %s = %v, want true", tc.name, v)
			}
		})
	}
}

// TestLevelPlainCommandsLeaveTheOnOffAttributeAlone pins that only the
// coupled variants store into OnOff: a plain MoveToLevel to MinLevel
// keeps the light on at LEVEL=1/254 and does not touch the attribute.
func TestLevelPlainCommandsLeaveTheOnOffAttributeAlone(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
	l.OnLevel(0.5)
	oo := onOffServer(t, l)
	invokeLevel(t, l, levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 0})
	if got := singleLevelSet(t, w.snapshot()); !near(got, matterLevelToHM(matterLevelMinDefault)) {
		t.Fatalf("plain MoveToLevel(0) wrote %v, want MinLevel %v (still on)", got, matterLevelToHM(matterLevelMinDefault))
	}
	if v, _ := oo.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after a plain MoveToLevel = %v, want true", v)
	}
}

// ─── Ramp vs instant ───────────────────────────────────────────────────────

// TestLevelTransitionTimeRampsOnlyWhenTheDeviceRamps pins the transition
// rule for MoveToLevel and Step: a positive TransitionTime on a device
// with Capabilities.Transition becomes one {LEVEL, RAMP_TIME} put_paramset
// with RAMP_TIME = tenths / 10 s; without the capability, or with a null
// or zero TransitionTime, the level is set at once.
func TestLevelTransitionTimeRampsOnlyWhenTheDeviceRamps(t *testing.T) {
	cases := []struct {
		name     string
		cmd      uint32
		fields   any
		ramps    bool
		wantLvl  float64
		wantRamp float64
	}{
		{"MoveToLevel 25 tenths", levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 200, TransitionTime: u16(25)}, true, matterLevelToHM(200), 2.5},
		{"MoveToLevelWithOnOff MinLevel 30 tenths", levelcontrol.CmdMoveToLevelWithOnOff, levelcontrol.MoveToLevelRequest{Level: 1, TransitionTime: u16(30)}, true, 0, 3.0},
		{"Step up 15 tenths", levelcontrol.CmdStep, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 20, TransitionTime: u16(15)}, true, matterLevelToHM(147), 1.5},
		// A Step cropped at a bound runs the cropped distance at the rate
		// the full step gives: matter.js stepLogic
		// (packages/node/src/behaviors/level-control/LevelControlServer.ts:464-479)
		// derives rate = stepSize / transitionTime * 10 = 250 levels/s, so
		// 127 -> MinLevel 1 is 126 levels, 0.504 s, carried as 0.5 s.
		{"StepWithOnOff down to off 10 tenths", levelcontrol.CmdStepWithOnOff, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeDown, StepSize: 250, TransitionTime: u16(10)}, true, 0, 0.5},
		{"MoveToLevel null TransitionTime", levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 200}, false, matterLevelToHM(200), 0},
		{"MoveToLevel zero TransitionTime", levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 200, TransitionTime: u16(0)}, false, matterLevelToHM(200), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true, Transition: true}, false)
			l.OnLevel(0.5) // CurrentLevel 127
			invokeLevel(t, l, tc.cmd, tc.fields)
			calls := w.snapshot()
			if !tc.ramps {
				if got := singleLevelSet(t, calls); !near(got, tc.wantLvl) {
					t.Fatalf("instant LEVEL=%v, want %v", got, tc.wantLvl)
				}
				return
			}
			lvl, ramp := singleRampPut(t, calls)
			if !near(lvl, tc.wantLvl) || !near(ramp, tc.wantRamp) {
				t.Fatalf("ramp put LEVEL=%v RAMP_TIME=%v, want LEVEL=%v RAMP_TIME=%v", lvl, ramp, tc.wantLvl, tc.wantRamp)
			}
		})
	}

	t.Run("no Transition capability", func(t *testing.T) {
		l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
		l.OnLevel(0.5)
		invokeLevel(t, l, levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 200, TransitionTime: u16(25)})
		if got := singleLevelSet(t, w.snapshot()); !near(got, matterLevelToHM(200)) {
			t.Fatalf("instant LEVEL=%v, want %v", got, matterLevelToHM(200))
		}
	})
}

// ─── Move ─────────────────────────────────────────────────────────────────

// TestLevelMoveRunsToTheEndOfTheRange pins Move as a movement rather than
// a no-op: up ends at MaxLevel (LEVEL 1.0), down at MinLevel (1/254 on
// the plain command); with a Rate and a known current level on a device
// that ramps, RAMP_TIME is distance / rate, rounded to tenths.
func TestLevelMoveRunsToTheEndOfTheRange(t *testing.T) {
	cases := []struct {
		name     string
		cmd      uint32
		req      levelcontrol.MoveRequest
		caps     custom.LightCapabilities
		wantLvl  float64
		ramps    bool
		wantRamp func(current uint8) float64
	}{
		{"up without rate is instant", levelcontrol.CmdMove, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp}, custom.LightCapabilities{Dimmable: true, Transition: true}, 1.0, false, nil},
		{"down without rate is instant", levelcontrol.CmdMove, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeDown}, custom.LightCapabilities{Dimmable: true, Transition: true}, matterLevelToHM(1), false, nil},
		{
			"up with rate ramps", levelcontrol.CmdMove,
			levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, Rate: u8(10)},
			custom.LightCapabilities{Dimmable: true, Transition: true},
			1.0, true,
			func(c uint8) float64 { return math.Round(float64(254-int(c))/10*10) / 10 },
		},
		{
			"down with rate ramps", levelcontrol.CmdMove,
			levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeDown, Rate: u8(40)},
			custom.LightCapabilities{Dimmable: true, Transition: true},
			matterLevelToHM(1), true,
			func(c uint8) float64 { return math.Round(float64(int(c)-1)/40*10) / 10 },
		},
		{
			"WithOnOff down with rate ramps off", levelcontrol.CmdMoveWithOnOff,
			levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeDown, Rate: u8(40)},
			custom.LightCapabilities{Dimmable: true, Transition: true},
			0, true,
			func(c uint8) float64 { return math.Round(float64(int(c)-1)/40*10) / 10 },
		},
		{"rate without Transition capability is instant", levelcontrol.CmdMove, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, Rate: u8(10)}, custom.LightCapabilities{Dimmable: true}, 1.0, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, w := newLevelRig(t, tc.caps, false)
			l.OnLevel(0.5)
			current := currentMatterLevel(t, l)
			invokeLevel(t, l, tc.cmd, tc.req)
			calls := w.snapshot()
			if !tc.ramps {
				if got := singleLevelSet(t, calls); !near(got, tc.wantLvl) {
					t.Fatalf("instant LEVEL=%v, want %v", got, tc.wantLvl)
				}
				return
			}
			lvl, ramp := singleRampPut(t, calls)
			if want := tc.wantRamp(current); !near(lvl, tc.wantLvl) || !near(ramp, want) {
				t.Fatalf("Move from %d: LEVEL=%v RAMP_TIME=%v, want LEVEL=%v RAMP_TIME=%v", current, lvl, ramp, tc.wantLvl, want)
			}
		})
	}
}

// TestLevelMoveRateFromHalfIsDistanceOverRate pins the ramp arithmetic
// on concrete numbers: from CurrentLevel 127 a Move up at 10 levels/s
// covers 127 levels, RAMP_TIME 12.7 s.
func TestLevelMoveRateFromHalfIsDistanceOverRate(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true, Transition: true}, false)
	l.OnLevel(0.5)
	if c := currentMatterLevel(t, l); c != 127 {
		t.Fatalf("precondition: CurrentLevel = %d, want 127", c)
	}
	invokeLevel(t, l, levelcontrol.CmdMove, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, Rate: u8(10)})
	if lvl, ramp := singleRampPut(t, w.snapshot()); lvl != 1.0 || !near(ramp, 12.7) {
		t.Fatalf("Move up at 10/s from 127: LEVEL=%v RAMP_TIME=%v, want 1.0 / 12.7", lvl, ramp)
	}
}

// TestLevelRatedMoveWithUnknownLevelAppliesWithoutRamp pins that a rated
// Move with no observed level has no distance to time, so it applies at
// once instead of inventing a ramp.
func TestLevelRatedMoveWithUnknownLevelAppliesWithoutRamp(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true, Transition: true}, false)
	invokeLevel(t, l, levelcontrol.CmdMoveWithOnOff, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, Rate: u8(10)})
	if got := singleLevelSet(t, w.snapshot()); got != 1.0 {
		t.Fatalf("rated MoveWithOnOff with unknown level wrote %v, want an instant 1.0", got)
	}
}

// TestLevelMoveZeroRateIsInvalidCommand pins that a Rate of 0 is refused
// as InvalidCommand (matter.js LevelControlServer.ts:305-308
// #assertRateValue) before anything reaches the CCU, and the refusal
// does not bump the data version.
func TestLevelMoveZeroRateIsInvalidCommand(t *testing.T) {
	for _, cmd := range []uint32{levelcontrol.CmdMove, levelcontrol.CmdMoveWithOnOff} {
		l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
		l.OnLevel(0.5)
		srv := levelServer(t, l)
		before := l.MatterDataVersion()
		_, err := srv.MatterInvoke(context.Background(), cmd, levelcontrol.MoveRequest{MoveMode: levelcontrol.MoveModeUp, Rate: u8(0)})
		var sc im.StatusCodeError
		if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusInvalidCommand {
			t.Fatalf("cmd 0x%02X Rate=0: err = %v, want InvalidCommand", cmd, err)
		}
		if calls := w.snapshot(); len(calls) != 0 {
			t.Fatalf("cmd 0x%02X Rate=0 reached the CCU: %+v", cmd, calls)
		}
		if l.MatterDataVersion() != before {
			t.Fatalf("cmd 0x%02X Rate=0 bumped the data version", cmd)
		}
	}
}

// ─── Step ─────────────────────────────────────────────────────────────────

// TestLevelStepClampsToTheLevelRange pins Step's clamp to [1, 254]: a
// plain Step down past MinLevel stays on at 1/254, a Step up past
// MaxLevel saturates at 1.0.
func TestLevelStepClampsToTheLevelRange(t *testing.T) {
	cases := []struct {
		name  string
		start float64
		req   levelcontrol.StepRequest
		want  float64
	}{
		{"down floors at MinLevel", 0.1, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeDown, StepSize: 200}, matterLevelToHM(1)},
		{"up saturates at MaxLevel", 0.9, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 200}, 1.0},
		{"up inside the range", 0.5, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 20}, matterLevelToHM(147)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
			l.OnLevel(tc.start)
			invokeLevel(t, l, levelcontrol.CmdStep, tc.req)
			if got := singleLevelSet(t, w.snapshot()); !near(got, tc.want) {
				t.Fatalf("Step wrote %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLevelStepWithUnknownLevelFails pins that a Step with no observed
// level has nothing to step from: the command answers Failure and no
// CCU write goes out. matter.js stepLogic reads the currentLevel getter
// (packages/node/src/behaviors/level-control/LevelControlServer.ts:478),
// which throws Status.Failure while CurrentLevel is null (:103-111).
func TestLevelStepWithUnknownLevelFails(t *testing.T) {
	for _, cmd := range []uint32{levelcontrol.CmdStep, levelcontrol.CmdStepWithOnOff} {
		l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
		eio := levelcontrol.OptionExecuteIfOff
		_, err := levelServer(t, l).MatterInvoke(context.Background(), cmd, levelcontrol.StepRequest{StepMode: levelcontrol.StepModeUp, StepSize: 20, OptionsMask: eio, OptionsOverride: eio})
		var sc im.StatusCodeError
		if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusFailure {
			t.Fatalf("cmd 0x%02X with unknown level: err = %v, want status Failure", cmd, err)
		}
		if calls := w.snapshot(); len(calls) != 0 {
			t.Fatalf("cmd 0x%02X with unknown level reached the CCU: %+v", cmd, calls)
		}
	}
}

// ─── Stop ─────────────────────────────────────────────────────────────────

// TestLevelStopWritesRampStopOnlyWhereTheChannelCarriesIt pins Stop and
// StopWithOnOff: a channel with the RAMP_STOP action (HM-LC-Dim*) gets
// RAMP_STOP=true at High priority; a channel without it (HmIP dimmers)
// gets nothing, and the command still answers Success.
func TestLevelStopWritesRampStopOnlyWhereTheChannelCarriesIt(t *testing.T) {
	for _, cmd := range []uint32{levelcontrol.CmdStop, levelcontrol.CmdStopWithOnOff} {
		for _, rampStop := range []bool{true, false} {
			l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true, Transition: true}, rampStop)
			l.OnLevel(0.5)
			invokeLevel(t, l, cmd, levelcontrol.StopRequest{})
			calls := w.snapshot()
			if !rampStop {
				if len(calls) != 0 {
					t.Fatalf("cmd 0x%02X without RAMP_STOP wrote %+v, want nothing", cmd, calls)
				}
				continue
			}
			if len(calls) != 1 || calls[0].param != hmenum.ParameterRampStop || calls[0].value != true || calls[0].prio != hmenum.CommandPriorityHigh {
				t.Fatalf("cmd 0x%02X with RAMP_STOP wrote %+v, want one RAMP_STOP=true at High", cmd, calls)
			}
		}
	}
}

// TestLevelStopWithOnOffIsNotGatedWhileOff pins that the coupled Stop
// halts a ramp even while the light reads off (a ramp down to off is
// still running), where the plain Stop is gated.
func TestLevelStopWithOnOffIsNotGatedWhileOff(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, true)
	l.OnLevel(0)
	invokeLevel(t, l, levelcontrol.CmdStopWithOnOff, levelcontrol.StopRequest{})
	if calls := w.snapshot(); len(calls) != 1 || calls[0].param != hmenum.ParameterRampStop {
		t.Fatalf("StopWithOnOff while off wrote %+v, want one RAMP_STOP", calls)
	}
}

// ─── Attributes ───────────────────────────────────────────────────────────

// TestLevelCurrentLevelReadsTheObservedBrightness pins CurrentLevel as a
// reading of the port: null until the CCU reported a level, then the
// brightness on the 0..254 scale.
func TestLevelCurrentLevelReadsTheObservedBrightness(t *testing.T) {
	l, _ := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
	srv := levelServer(t, l)
	if v, ok := srv.MatterRead(levelcontrol.AttrCurrentLevel); !ok || v != nil {
		t.Fatalf("CurrentLevel unobserved = (%v, %v), want (null, true)", v, ok)
	}
	l.OnLevel(1.0)
	if v, ok := srv.MatterRead(levelcontrol.AttrCurrentLevel); !ok || v != uint8(254) {
		t.Fatalf("CurrentLevel at LEVEL 1.0 = (%v, %v), want (254, true)", v, ok)
	}
}

// TestLevelCurrentLevelIsNotWritable pins CurrentLevel as "R V"
// (matter.js level-control.element.ts): a write is refused, nothing
// reaches the CCU and the data version stays.
func TestLevelCurrentLevelIsNotWritable(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
	l.OnLevel(0.5)
	srv := levelServer(t, l)
	before := l.MatterDataVersion()
	if err := srv.MatterWrite(context.Background(), levelcontrol.AttrCurrentLevel, uint64(200)); err == nil {
		t.Fatal("MatterWrite(CurrentLevel) succeeded, want a refusal")
	}
	if calls := w.snapshot(); len(calls) != 0 {
		t.Fatalf("a refused CurrentLevel write reached the CCU: %+v", calls)
	}
	if l.MatterDataVersion() != before {
		t.Fatal("a refused CurrentLevel write bumped the data version")
	}
}

// TestLevelOptionsAndOnLevelRoundTrip pins the two writable cluster
// attributes the light keeps: Options and OnLevel read back what was
// written, a null OnLevel unsets it, an Options bit the OptionsBitmap
// does not define (0x04) is refused, and none of it reaches the CCU.
func TestLevelOptionsAndOnLevelRoundTrip(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
	srv := levelServer(t, l)
	ctx := context.Background()

	if v, _ := srv.MatterRead(levelcontrol.AttrOptions); v != uint8(0) {
		t.Fatalf("Options before any write = %v, want 0", v)
	}
	if v, ok := srv.MatterRead(levelcontrol.AttrOnLevel); !ok || v != nil {
		t.Fatalf("OnLevel before any write = (%v, %v), want (null, true)", v, ok)
	}

	if err := srv.MatterWrite(ctx, levelcontrol.AttrOptions, uint64(levelcontrol.OptionExecuteIfOff)); err != nil {
		t.Fatalf("MatterWrite(Options=ExecuteIfOff): %v", err)
	}
	if v, _ := srv.MatterRead(levelcontrol.AttrOptions); v != levelcontrol.OptionExecuteIfOff {
		t.Fatalf("Options after write = %v, want %v", v, levelcontrol.OptionExecuteIfOff)
	}
	if err := srv.MatterWrite(ctx, levelcontrol.AttrOptions, uint64(0x04)); err == nil {
		t.Fatal("MatterWrite(Options=0x04) succeeded, want a refusal")
	}
	if v, _ := srv.MatterRead(levelcontrol.AttrOptions); v != levelcontrol.OptionExecuteIfOff {
		t.Fatalf("Options after a refused write = %v, want it kept at %v", v, levelcontrol.OptionExecuteIfOff)
	}

	if err := srv.MatterWrite(ctx, levelcontrol.AttrOnLevel, uint64(100)); err != nil {
		t.Fatalf("MatterWrite(OnLevel=100): %v", err)
	}
	if v, _ := srv.MatterRead(levelcontrol.AttrOnLevel); v != uint8(100) {
		t.Fatalf("OnLevel after write = %v, want 100", v)
	}
	if err := srv.MatterWrite(ctx, levelcontrol.AttrOnLevel, nil); err != nil {
		t.Fatalf("MatterWrite(OnLevel=null): %v", err)
	}
	if v, ok := srv.MatterRead(levelcontrol.AttrOnLevel); !ok || v != nil {
		t.Fatalf("OnLevel after a null write = (%v, %v), want (null, true)", v, ok)
	}

	if calls := w.snapshot(); len(calls) != 0 {
		t.Fatalf("attribute writes reached the CCU: %+v", calls)
	}
}

// TestLevelAttributeStateSurvivesAServerLookup pins that Options and
// OnLevel live on the light, not on a server instance: every
// MatterClusterServers call hands out the same LevelControl server, so a
// reassembly reads back what a controller wrote.
func TestLevelAttributeStateSurvivesAServerLookup(t *testing.T) {
	l, _ := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
	first := levelServer(t, l)
	if err := first.MatterWrite(context.Background(), levelcontrol.AttrOnLevel, uint64(80)); err != nil {
		t.Fatalf("MatterWrite(OnLevel=80): %v", err)
	}
	second := levelServer(t, l)
	if second != first {
		t.Fatal("MatterClusterServers built a second LevelControl server for the same light")
	}
	if v, _ := second.MatterRead(levelcontrol.AttrOnLevel); v != uint8(80) {
		t.Fatalf("OnLevel through a second lookup = %v, want 80", v)
	}
}

// TestLevelMoveToLevelAboveMaxIsAConstraintError pins that Level 255
// violates "max 254" and is refused before the CCU.
func TestLevelMoveToLevelAboveMaxIsAConstraintError(t *testing.T) {
	l, w := newLevelRig(t, custom.LightCapabilities{Dimmable: true}, false)
	l.OnLevel(0.5)
	_, err := levelServer(t, l).MatterInvoke(context.Background(), levelcontrol.CmdMoveToLevel, levelcontrol.MoveToLevelRequest{Level: 255})
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusConstraintError {
		t.Fatalf("MoveToLevel(255): err = %v, want ConstraintError", err)
	}
	if calls := w.snapshot(); len(calls) != 0 {
		t.Fatalf("MoveToLevel(255) reached the CCU: %+v", calls)
	}
}
