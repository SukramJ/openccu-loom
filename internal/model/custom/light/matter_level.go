// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// lightLevelSource is the host port of go-fabric's LevelControl server
// over a [Light]. It is a separate type because the port's OnLevel()
// reading and the light's OnLevel(float64) event hook share a name.
type lightLevelSource struct{ l *Light }

var _ levelcontrol.LevelSource = lightLevelSource{}

// lightLevelState carries the two LevelControl attributes that describe
// the cluster rather than the device — Options and OnLevel — which
// matter.js keeps in cluster state. They live on the Light so they
// survive the server reconstruction a topology assembly performs.
type lightLevelState struct {
	mu      sync.RWMutex
	options uint8
	// onLevel is nil for the spec's null reading, "OnLevel has no effect".
	onLevel *uint8
}

// matterLevelServer builds the light's LevelControl server once, on the
// hand-off path of go-fabric's server (Transitions nil): the eight
// commands reach [Light] through [levelcontrol.LevelSource], and the
// device ramps natively through RAMP_TIME. The transition engine is not
// used on purpose — it would write one LEVEL per 100 ms step over the
// radio for the whole transition, where one atomic {LEVEL, RAMP_TIME}
// put_paramset lets the dimmer do the same. RemainingTime therefore
// reads 0 (notes/parity/by_design.md BD-Matter-LevelControl-NativeRamp).
func (l *Light) matterLevelServer() *levelcontrol.Server {
	l.matterLevelOnce.Do(func() {
		l.matterLevelSrv = levelcontrol.NewServer(levelcontrol.Config{
			Source:      lightLevelSource{l: l},
			DataVersion: &l.dataVersion,
			Lighting:    true,
		})
	})
	return l.matterLevelSrv
}

// CurrentLevel implements [levelcontrol.LevelSource]: the observed
// brightness on the 0..254 scale, unknown until the CCU answered.
func (s lightLevelSource) CurrentLevel() (uint8, bool) {
	b, observed := s.l.Brightness()
	if !observed {
		return 0, false
	}
	return brightnessToMatter(b), true
}

// Options implements [levelcontrol.LevelSource].
func (s lightLevelSource) Options() uint8 {
	s.l.matterLevel.mu.RLock()
	defer s.l.matterLevel.mu.RUnlock()
	return s.l.matterLevel.options
}

// SetOptions implements [levelcontrol.LevelSource]. The server has
// already checked the bitmap against the advertised FeatureMap.
func (s lightLevelSource) SetOptions(_ context.Context, options uint8) error {
	s.l.matterLevel.mu.Lock()
	defer s.l.matterLevel.mu.Unlock()
	s.l.matterLevel.options = options
	return nil
}

// OnLevel implements [levelcontrol.LevelSource]. Unset means null: the
// device returns to its own last level when it turns on, which is what
// HM dimmers do with LEVEL=1.0 after an off (and what
// [Light.TurnOn] restores through LastLevel).
func (s lightLevelSource) OnLevel() (uint8, bool) {
	s.l.matterLevel.mu.RLock()
	defer s.l.matterLevel.mu.RUnlock()
	if s.l.matterLevel.onLevel == nil {
		return 0, false
	}
	return *s.l.matterLevel.onLevel, true
}

// SetOnLevel implements [levelcontrol.LevelSource]. The value is stored
// so a controller reads back what it wrote; the single LEVEL knob of an
// HM dimmer has no separate on-level to apply it to.
func (s lightLevelSource) SetOnLevel(_ context.Context, level *uint8) error {
	s.l.matterLevel.mu.Lock()
	defer s.l.matterLevel.mu.Unlock()
	if level == nil {
		s.l.matterLevel.onLevel = nil
		return nil
	}
	v := *level
	s.l.matterLevel.onLevel = &v
	return nil
}

// matterLevelExecutes applies the ExecuteIfOff gate of the plain (non
// On/Off) commands: the effective options allow execution while off, or
// the device is on (matter.js LevelControlServer.ts:729-736
// #optionsAllowExecution). The bitmap arithmetic is the server's
// [levelcontrol.EffectiveOptions]; the OnOff state it is weighed against
// is the light's.
func (l *Light) matterLevelExecutes(mask, override uint8) bool {
	if levelcontrol.EffectiveOptions((lightLevelSource{l: l}).Options(), mask, override)&levelcontrol.OptionExecuteIfOff != 0 {
		return true
	}
	on, _ := l.IsOn()
	return on
}

// MoveToLevel implements [levelcontrol.LevelSource]: LEVEL to the
// requested level, ramped over TransitionTime where the device carries
// RAMP_TIME. A plain MoveToLevel while off is a silent Success unless
// ExecuteIfOff is in effect (LevelControlServer.ts:729-736).
func (s lightLevelSource) MoveToLevel(ctx context.Context, req levelcontrol.MoveToLevelRequest) error {
	if !s.l.matterLevelExecutes(req.OptionsMask, req.OptionsOverride) {
		return nil
	}
	return s.l.matterMoveToLevel(ctx, cropMatterLevel(req.Level), req.TransitionTime, false)
}

// MoveToLevelWithOnOff implements [levelcontrol.LevelSource]: the
// coupled variant. A target at MinLevel turns the device off
// (LevelControlServer.ts:557 couple, chip LevelControlCluster.cpp:344;
// Matter §1.6.4.1.2); HM dimmers carry a single LEVEL knob, so
// "CurrentLevel=min + OnOff=false" is LEVEL=0.
func (s lightLevelSource) MoveToLevelWithOnOff(ctx context.Context, req levelcontrol.MoveToLevelRequest) error {
	return s.l.matterMoveToLevel(ctx, cropMatterLevel(req.Level), req.TransitionTime, true)
}

// matterMoveToLevel drives LEVEL to level (0..254; with withOnOff a
// MinLevel target is off), over the transition where the device ramps,
// and mirrors a coupled command onto the OnOff attribute.
func (l *Light) matterMoveToLevel(ctx context.Context, level uint8, transition *uint16, withOnOff bool) error {
	target := matterLevelToHM(level)
	if withOnOff && level == matterLevelMinDefault {
		target = 0
	}
	if ramp, ramped := transitionRampDuration(transition); ramped && l.Capabilities.Transition {
		// A positive transition time becomes the device-side ramp:
		// matter.js LevelControlServer.ts:326 (moveToLevelLogic)
		// turns it into a rate towards the target level; HM dimmers
		// ramp natively when RAMP_TIME accompanies LEVEL in one atomic
		// put_paramset, so the whole transition is the device's.
		if err := l.setLevelRamped(ctx, target, ramp, matterDispatchPriority); err != nil {
			return err
		}
	} else if err := l.SetLevel(ctx, target, matterDispatchPriority); err != nil {
		return err
	}
	l.syncMatterOnOff(withOnOff, target)
	return nil
}

// Move implements [levelcontrol.LevelSource]: a continuous move towards
// MaxLevel or MinLevel. matter.js runs it at Rate levels per second
// (Transitions.ts changePerS); an HM dimmer runs the same movement as
// one ramp to the end of the range over distance / rate seconds, so the
// Rate becomes RAMP_TIME where the device carries it. Without a Rate the
// move applies at once, as matter.js reads an unset DefaultMoveRate
// (package doc of cluster/levelcontrol). A plain Move while off is a
// silent Success unless ExecuteIfOff is in effect.
func (s lightLevelSource) Move(ctx context.Context, req levelcontrol.MoveRequest) error {
	if !s.l.matterLevelExecutes(req.OptionsMask, req.OptionsOverride) {
		return nil
	}
	return s.l.matterMove(ctx, req, false)
}

// MoveWithOnOff implements [levelcontrol.LevelSource]: the coupled
// variant; a move down ends at MinLevel and turns the device off.
func (s lightLevelSource) MoveWithOnOff(ctx context.Context, req levelcontrol.MoveRequest) error {
	return s.l.matterMove(ctx, req, true)
}

func (l *Light) matterMove(ctx context.Context, req levelcontrol.MoveRequest, withOnOff bool) error {
	level := matterLevelMax
	if req.MoveMode == levelcontrol.MoveModeDown {
		level = matterLevelMinDefault
	}
	var transition *uint16
	if req.Rate != nil && *req.Rate > 0 {
		if current, known := (lightLevelSource{l: l}).CurrentLevel(); known {
			distance := int(level) - int(current)
			if distance < 0 {
				distance = -distance
			}
			// tenths of a second, the unit TransitionTime carries.
			tenths := (distance*10 + int(*req.Rate)/2) / int(*req.Rate)
			if tenths > 0xFFFE {
				tenths = 0xFFFE
			}
			t := uint16(tenths) //nolint:gosec // bounded above
			transition = &t
		}
	}
	return l.matterMoveToLevel(ctx, level, transition, withOnOff)
}

// Step implements [levelcontrol.LevelSource]: one discrete step, the
// target clamped to [MinLevel, MaxLevel] = [1, 254] as matter.js
// Transitions.ts:139 clamps — a plain Step can never turn the device
// off. A plain Step while off is a silent Success unless ExecuteIfOff is
// in effect (LevelControlServer.ts:388).
func (s lightLevelSource) Step(ctx context.Context, req levelcontrol.StepRequest) error {
	if !s.l.matterLevelExecutes(req.OptionsMask, req.OptionsOverride) {
		return nil
	}
	next, ok := s.l.matterSteppedLevel(req)
	if !ok {
		return nil
	}
	return s.l.matterMoveToLevel(ctx, next, req.TransitionTime, false)
}

// StepWithOnOff implements [levelcontrol.LevelSource]: the coupled
// variant. A step clamped to MinLevel turns the device off (Matter
// §1.6.7.6; chip LevelControlCluster.cpp:508 compares the post-clamp
// target, which matter.js couple() does before the clamp — chip and
// the spec win here).
func (s lightLevelSource) StepWithOnOff(ctx context.Context, req levelcontrol.StepRequest) error {
	next, ok := s.l.matterSteppedLevel(req)
	if !ok {
		return nil
	}
	return s.l.matterMoveToLevel(ctx, next, req.TransitionTime, true)
}

// matterSteppedLevel is the clamped target of a Step; unknown while the
// brightness has not been observed, in which case the step is a silent
// Success — there is no level to step from.
func (l *Light) matterSteppedLevel(req levelcontrol.StepRequest) (uint8, bool) {
	current, known := (lightLevelSource{l: l}).CurrentLevel()
	if !known {
		return 0, false
	}
	if req.StepMode == levelcontrol.StepModeDown {
		if int(current)-int(req.StepSize) < int(matterLevelMinDefault) {
			return matterLevelMinDefault, true
		}
		return current - req.StepSize, true
	}
	if int(current)+int(req.StepSize) > int(matterLevelMax) {
		return matterLevelMax, true
	}
	return current + req.StepSize, true
}

// Stop implements [levelcontrol.LevelSource]: the classic HM dimmers
// (HM-LC-Dim*) carry a RAMP_STOP action that halts a running ramp at the
// current level; HmIP dimmers carry none, and a Stop there is a Success
// that changes nothing, as before. A plain Stop while off is a silent
// Success unless ExecuteIfOff is in effect.
func (s lightLevelSource) Stop(ctx context.Context, req levelcontrol.StopRequest) error {
	if !s.l.matterLevelExecutes(req.OptionsMask, req.OptionsOverride) {
		return nil
	}
	return s.l.matterStop(ctx)
}

// StopWithOnOff implements [levelcontrol.LevelSource]: the coupled
// variant stops the same way; the OnOff state stays what the level
// confirmation says it is.
func (s lightLevelSource) StopWithOnOff(ctx context.Context, _ levelcontrol.StopRequest) error {
	return s.l.matterStop(ctx)
}

func (l *Light) matterStop(ctx context.Context) error {
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

// syncMatterOnOff mirrors a "with On/Off" level command onto the OnOff
// attribute the moment the CCU accepted the level write, as matter.js's
// LevelControlServer couples the two clusters: a coupled command to
// MinLevel turns the light off, one above it turns it on. It stores
// through the OnOff server's Set — the host's own change — so the OnOff
// Sink is not asked for a second CCU write; the LEVEL confirmation
// corrects the value if the device answered otherwise.
func (l *Light) syncMatterOnOff(withOnOff bool, target float64) {
	if !withOnOff {
		return
	}
	l.matterOnOffServer().Set(target > 0)
}

// cropMatterLevel crops a requested level into [MinLevel, MaxLevel] =
// [1, 254]. Mirrors matter.js LevelControlServer.ts:274
// cropValueRange(level, this.minLevel, this.maxLevel) with the LT
// feature's minLevel=1 / maxLevel=254 defaults (LevelControlServer.ts:115-119).
func cropMatterLevel(level uint8) uint8 {
	if level < matterLevelMinDefault {
		return matterLevelMinDefault
	}
	if level > matterLevelMax {
		return matterLevelMax
	}
	return level
}

// transitionRampDuration converts a MoveToLevel / Step TransitionTime
// (nullable uint16, tenths of a second per Matter §1.6.7.1) into the
// RAMP_TIME duration handed to the CCU. Null/absent and 0 both report
// ramped=false so the caller keeps the instant SetLevel path: matter.js
// LevelControlServer.ts:326 (moveToLevelLogic) only computes an
// effectiveRate for a truthy transition time, and transition()'s
// changePerS contract (LevelControlServer.ts:525) documents "0 or
// nullish means transition instantly".
func transitionRampDuration(tenths *uint16) (ramp time.Duration, ramped bool) {
	if tenths == nil || *tenths == 0 {
		return 0, false
	}
	return time.Duration(*tenths) * 100 * time.Millisecond, true
}
