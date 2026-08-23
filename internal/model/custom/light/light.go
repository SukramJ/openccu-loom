// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package light implements the dimmable-light custom data point. The
// LEVEL value is held as a typed reference to the channel's existing
// *generic.Float — the embedded pointer is the channel's instance,
// not a duplicate. Light layers the brightness abstraction on top.
package light

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// lastSentTTL caps how long a recorded send target lingers in the
// `lastSentLevel` slot before it ages out, so a forgotten write cannot
// shadow `effectiveLevel` forever. 60 s matches the upstream
// `LAST_COMMAND_SEND_STORE_TIMEOUT` constant.
const lastSentTTL = 60 * time.Second

// levelEchoEpsilon is the wire-precision tolerance used to decide whether
// an incoming LEVEL echo confirms the sent target. Matches the two-decimal
// rounding the optimistic tracker applies to floats.
const levelEchoEpsilon = 0.005

// Writer is an alias for [custom.Writer].
type Writer = custom.Writer

// Config is the constructor record. Channel must already carry the
// LEVEL data point. Writer is currently unused at this level — the
// embedded *generic.Float carries its own writer through the channel-
// side construction — but is kept on Config for symmetry with other
// custom DPs and so future commands (TURN_ON-only switches, etc.)
// have a place to land.
type Config struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.LightCapabilities
	// Group is the rebased channel-group schema of the profile that
	// materialised this light. Every composed field resolves through it —
	// see [custom.ResolveSlotOr]. The zero value is valid: each binding
	// falls back to the parameter named at the call site.
	Group custom.RebasedChannelGroupConfig
}

// Light is a single dimmable light. The LEVEL value is the channel's
// *generic.Float, embedded as a typed reference. Light adds the
// [custom.Brightness] view.
//
// Last-level tracking: every time the CCU reports a non-zero LEVEL, Light
// remembers it as `lastLevel`. A subsequent [TurnOn] call without an explicit
// level uses lastLevel as target so that an On/Off toggle returns the dimmer
// to its previous brightness instead of jumping to 100 %.
type Light struct {
	*generic.Float
	// baseDP carries the observability timestamps and in-flight write counter.
	// Named rather than embedded to avoid ambiguity with the struct's own mu field.
	baseDP custom.BaseDP

	Capabilities custom.LightCapabilities

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Shared by all cluster servers that project this Light
	// (OnOff, LevelControl, ColorControl). Bumped on every successful
	// MatterWrite / MatterInvoke so DataVersionFilter evaluation
	// correctly detects cluster changes.
	dataVersion hmtypes.DataVersionTracker

	// timed is the OnOff cluster LT timed-command engine (OnTime /
	// OffWaitTime countdowns + StartUpOnOff store). Owned here so the
	// countdowns survive cluster-server reconstruction.
	timed timedOnOffState

	mu        sync.RWMutex
	lastLevel float64
	// groupLevel is the optional group-channel level slot, bound by the
	// profile's GROUP_LEVEL field. Its shape differs per family — LEVEL
	// (read+write) on an HmIP state channel, LEVEL_REAL (read-only) on an
	// RF action channel — so it is held by capability, not by type.
	groupLevel custom.GroupLevelDataPoint

	// enableLastBrightness controls a plain turn-on. When true (the
	// default, set from the device's per-central behavior in [New]) a
	// turn-on without an explicit level restores [LastLevel]; when
	// false it turns on at full (1.0). Reference stack key:
	// enable_light_last_brightness.
	enableLastBrightness bool

	// lastSentLevel holds the most recent LEVEL the daemon wrote to the CCU
	// that has not yet been echoed back with a matching value. Survives
	// mismatching intermediate echoes (e.g. ramp values on RF dimmers where
	// LEVEL_REAL only updates after the ramp finishes) so `effectiveLevel`
	// keeps the user's target authoritative until the CCU confirms a
	// matching final value. Cleared by [Light.New]'s LEVEL OnUpdate
	// subscription on a matching echo, by an explicit `clearLastSent`, or
	// by TTL.
	lastSentLevel *float64
	lastSentAt    time.Time

	timerMu     sync.Mutex
	pendingOn   *time.Duration // deferred ON_TIME for next TurnOn
	pendingRamp *time.Duration // deferred RAMP_TIME for next TurnOn

	// hasOnTimeUnit is true when the channel's on-time timer resolves to a
	// value/unit pair (DURATION_VALUE + DURATION_UNIT — see
	// [resolveOnTimeParams]; no device carries the literal ON_TIME_UNIT wire
	// name), meaning the device interprets the reset as a timed shutdown. When
	// true, TurnOn without an explicit on-time sends the NotUsed sentinel to
	// cancel any previously running timer rather than leaving the old value
	// active.
	hasOnTimeUnit bool

	// onTimeValueParam / onTimeUnitParam are the wire parameter(s)
	// [Light.SetOnTime] writes, resolved once at construction from the
	// channel's own paramset. See [resolveOnTimeParams].
	onTimeValueParam hmenum.Parameter
	onTimeUnitParam  hmenum.Parameter

	// resetsOnTimeOnTurnOn gates whether a plain TurnOn (no explicit
	// on-time/timer) emits the NotUsed sentinel on a channel that carries
	// ON_TIME_UNIT. Only signal lights (FixedColorLight) require this reset.
	// For HmIP-RGBW / HmIP-DRG-DALI the device interprets the sentinel duration
	// on a plain turn_on and switches off again immediately (briefly flashes,
	// then off), so the default is false and only LEVEL is sent.
	resetsOnTimeOnTurnOn bool

	unsubLevel func()
}

// New constructs a Light. The channel's LEVEL data point becomes the
// embedded *generic.Float. A subscription to LEVEL updates is wired
// so that lastLevel tracks the latest non-zero CCU value; the
// subscription is cleaned up by [Close] when the custom DP is
// replaced on the channel.
func New(cfg Config) *Light {
	level := custom.FloatField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldLevel, hmenum.ParameterLevel))
	onTimeValueParam, onTimeUnitParam := resolveOnTimeParams(cfg.Channel)
	hasOnTimeUnit := onTimeUnitParam != ""
	l := &Light{
		Float:                level,
		Capabilities:         cfg.Capabilities,
		hasOnTimeUnit:        hasOnTimeUnit,
		onTimeValueParam:     onTimeValueParam,
		onTimeUnitParam:      onTimeUnitParam,
		enableLastBrightness: deviceLightLastBrightness(cfg.Channel),
	}
	// GlobalSceneControl (Matter OnOff attribute 0x4000) defaults to
	// true. matter.js OnOffServer.ts state default; the field is unset
	// (false) otherwise since Go zero-values a fresh timedOnOffState.
	l.timed.globalSceneControl = true
	if level != nil {
		l.unsubLevel = level.OnUpdate(func(_, next float64) {
			// OnUpdate fires both for optimistic notifications (synchronously
			// out of `sendAndObserve.optimistic.Apply`) AND for CCU echoes
			// (out of `OnEvent`). We only treat CCU echoes as confirmations;
			// the optimistic-apply callback carries the same `next` as the
			// just-recorded send target and would otherwise clear the slot
			// before it could shield any intermediate ramp echo.
			if level.IsOptimistic() {
				if next > 0 {
					l.mu.Lock()
					l.lastLevel = next
					l.mu.Unlock()
				}
				return
			}
			l.mu.Lock()
			if next > 0 {
				l.lastLevel = next
			}
			// Matching echo of the sent target → clear the unconfirmed slot
			// (mirrors `last_value_send_tracker.remove_last_value_send` with
			// value match). Mismatching intermediate echoes leave it intact
			// so `effectiveLevel` keeps the user's target authoritative until
			// the CCU confirms the final value.
			if l.lastSentLevel != nil && math.Abs(*l.lastSentLevel-next) < levelEchoEpsilon {
				l.lastSentLevel = nil
				l.lastSentAt = time.Time{}
			}
			l.mu.Unlock()
		})
	}
	if level != nil {
		l.registerLightServices()
		// Matter §10.6.5: DataVersion advances on every CCU-confirmed attribute change.
		_ = level.OnConfirmedUpdate(func(_, _ float64) { l.dataVersion.Bump() })
	}
	return l
}

// LightModifiedAt returns the wall time of the last outbound command sent
// by this Light and whether it has been set. Distinct from the embedded
// generic.Float's ModifiedAt (which tracks individual write operations on
// the LEVEL DP) — this covers any command emitted through Light's write
// path. Delegates to the baseDP observability field.
func (l *Light) LightModifiedAt() (time.Time, bool) { return l.baseDP.ModifiedAt() }

// LightRefreshedAt returns the wall time of the last CCU confirmation
// received for this Light. Delegates to the baseDP observability field.
func (l *Light) LightRefreshedAt() (time.Time, bool) { return l.baseDP.RefreshedAt() }

// LightUnconfirmedLastValuesSend returns the number of in-flight writes.
// Delegates to the baseDP observability field.
func (l *Light) LightUnconfirmedLastValuesSend() int {
	return l.baseDP.UnconfirmedLastValuesSend()
}

// MarkLightModified records the wall time of the most recent outbound
// command. Delegates to the baseDP observability field.
func (l *Light) MarkLightModified() { l.baseDP.MarkModified() }

// MarkLightRefreshed records the wall time of the most recent inbound
// CCU event. Delegates to the baseDP observability field.
func (l *Light) MarkLightRefreshed() { l.baseDP.MarkRefreshed() }

// Close releases the level-update subscription and the Matter timed-OnOff
// tick goroutine. Light callers do not usually invoke this directly: it is
// wired through [Subscribe] / [Channel.SetCustomDataPoint] so replacing the
// custom DP on the channel tears both down automatically.
//
// The tick goroutine has to be named here because nothing else can reach
// it: its stop channel lives inside [timedOnOffState] and is closed only
// when a countdown runs out. A light with an armed OnWithTimedOff whose
// device leaves the model would otherwise keep ticking — and write a
// turn-off to the CCU at expiry — long after the Light was retired.
func (l *Light) Close() {
	if l.unsubLevel != nil {
		l.unsubLevel()
		l.unsubLevel = nil
	}
	l.timed.stopLoop()
}

// Subscribe satisfies [device.SubscribingDataPoint]. Light has no
// auxiliary parameters to wire up beyond LEVEL (which is the embedded
// pointer). Subscribe replays the LEVEL DP's currently observed value
// through the same callback path that OnUpdate uses so the [lastLevel]
// cache is hydrated at boot — without it, the cache reads 0 until the
// first post-Subscribe CCU push, and a TurnOn issued during that
// window restores the wrong brightness (1.0 fallback instead of the
// device's last known on-level). Returned closure tears down the
// OnUpdate registration so a replacement Light on the same channel
// does not leak.
func (l *Light) Subscribe(_ *device.Channel) func() {
	if l.Float != nil {
		if v, observed := l.RawValue(); observed {
			if f, ok := v.(float64); ok && f > 0 {
				l.mu.Lock()
				l.lastLevel = f
				l.mu.Unlock()
			}
		}
	}
	return l.Close
}

// LastLevel returns the most recent non-zero LEVEL the CCU reported,
// or 1.0 when the light has not yet been observed in an "on" state.
// Used by [TurnOn] to restore the previous brightness on toggle.
func (l *Light) LastLevel() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.lastLevel > 0 {
		return l.lastLevel
	}
	return 1.0
}

// turnOnLevel resolves the target LEVEL for a plain turn-on: the
// restored [LastLevel] when last-brightness is enabled (default), or
// full (1.0) when the per-central toggle is off. Reference stack key:
// enable_light_last_brightness.
func (l *Light) turnOnLevel() float64 {
	if l.enableLastBrightness {
		return l.LastLevel()
	}
	return 1.0
}

// deviceLightLastBrightness reads the per-central light-last-brightness
// toggle off the channel's device, defaulting to true when the channel
// or device is absent (test fixtures, pre-pipeline state).
func deviceLightLastBrightness(ch *device.Channel) bool {
	if ch == nil || ch.Device() == nil {
		return true
	}
	return ch.Device().LightLastBrightness()
}

// Address returns the channel address the Light writes to.
func (l *Light) Address() string {
	if l.Float == nil {
		return ""
	}
	return l.DataPointKey().ChannelAddress
}

// IsRefreshed reports whether LEVEL has been observed at least once.
// Color-temp HSV / effect slots are auxiliary; LEVEL is the canonical "is the
// light known?" signal.
func (l *Light) IsRefreshed() bool {
	if l.Float == nil {
		return false
	}
	_, ok := l.RawValue()
	return ok
}

// IsStatusValid reports whether the LEVEL data point has a valid STATUS
// parameter state (no OVERFLOW / ERROR).
func (l *Light) IsStatusValid() bool {
	if l.Float == nil {
		return true
	}
	return l.Float.IsStatusValid()
}

// Category returns the data-point category. When the channel has no LEVEL
// parameter (a "half-formed" channel — e.g. a light loader fired before the
// channel materialised LEVEL), Float is nil and the auto-promoted forwarder
// dispatches to (*DataPoint[float64]).Category, which dereferences its nil
// receiver and panics. Return Undefined in that case, mirroring
// [cover.Cover.Category].
func (l *Light) Category() hmenum.DataPointCategory {
	if l == nil || l.Float == nil {
		return hmenum.DataPointCategoryUndefined
	}
	return l.Float.Category()
}

// SetGroupLevel binds an optional group-channel LEVEL data point. The value
// of this DP is used by [GroupBrightness] and [GroupBrightnessPct] to expose
// the aggregated group brightness to north-bound consumers. Pass nil to
// clear.
func (l *Light) SetGroupLevel(dp custom.GroupLevelDataPoint) {
	l.mu.Lock()
	l.groupLevel = dp
	l.mu.Unlock()
}

// GroupBrightness returns the group brightness as a 0–255 byte and whether it
// has been observed. Returns (0, false) when no group-level DP has been
// installed or its value has not been received yet.
//
// if self._dp_group_level.value is not None: return
// self.level_to_brightness(self._dp_group_level.value)
func (l *Light) GroupBrightness() (uint8, bool) {
	l.mu.RLock()
	gl := l.groupLevel
	l.mu.RUnlock()
	if gl == nil {
		return 0, false
	}
	v, ok := gl.Value()
	if !ok {
		return 0, false
	}
	return custom.NewBrightness(v).Byte(), true
}

// GroupBrightnessPct returns the group brightness as a 0–100 integer
// percentage and whether it has been observed. Returns (0, false) when no
// group-level DP has been installed or its value has not been received.
//
// if self._dp_group_level.value is not None: return
// self.level_to_brightness_pct(self._dp_group_level.value)
func (l *Light) GroupBrightnessPct() (int, bool) {
	l.mu.RLock()
	gl := l.groupLevel
	l.mu.RUnlock()
	if gl == nil {
		return 0, false
	}
	v, ok := gl.Value()
	if !ok {
		return 0, false
	}
	return int(v*100 + 0.5), true
}

// DataPointKey returns the LEVEL data point key. When the channel
// has no LEVEL parameter (a "half-formed" channel — e.g. a light
// loader fired before the channel materialised LEVEL), Float is nil
// and the auto-promoted method on the embedded *generic.Float would
// crash. Return the zero key so callers in
// [custom.lookupProfileForCustomDP] can fall through the
// channelAddr == "" early-return per its docstring contract.
func (l *Light) DataPointKey() hmtypes.DataPointKey {
	if l == nil || l.Float == nil {
		return hmtypes.DataPointKey{}
	}
	return l.Float.DataPointKey()
}

// SubDataPointKeys returns the wire identifier of the LEVEL slot.
func (l *Light) SubDataPointKeys() []hmtypes.DataPointKey {
	if l.Float == nil {
		return nil
	}
	return []hmtypes.DataPointKey{l.DataPointKey()}
}

// effectiveLevel returns the level used for state readings.
//
// For HmIP dimmers (`groupLevel.Parameter() != LEVEL_REAL`, i.e. the
// state-channel parameter is LEVEL) the action channel is the
// authoritative source; the state channel's semantics are device-
// specific (e.g. HmIP-FDT echoes a section summary on channel 1, not
// a 1:1 mirror). The HmIP path is therefore the plain action-channel
// lookup — [Light.Value] already applies the optimistic value while a
// command is pending.
//
// For RF dimmers (`groupLevel.Parameter() == LEVEL_REAL`) the action
// channel streams intermediate ramp values, so the state channel is
// the stable mirror (#3166). The ramp window is bridged by the
// optimistic value and `lastSentValue` (#3177, #3178, #3179). When the
// action channel was modified more recently than the state channel,
// use it instead — the state channel is about to catch up and the
// short gap would otherwise cause a flicker (#3177 follow-up). The
// state channel is preferred on a tie so the #3166 ramp behaviour
// stays intact.
func (l *Light) effectiveLevel() (float64, bool) {
	if l.Float == nil {
		return 0, false
	}
	l.mu.RLock()
	gl := l.groupLevel
	l.mu.RUnlock()

	// HmIP path (no group level, or group level is LEVEL not LEVEL_REAL):
	// read the action channel directly. Value() applies optimistic first.
	if gl == nil || gl.Parameter() != hmenum.ParameterLevelReal {
		return l.Value()
	}

	// RF dimmer path below (LEVEL_REAL state channel).
	v, ok := l.Value()
	if l.IsOptimistic() {
		return v, ok
	}
	if pending, pok := l.lastSentValue(); pok {
		return pending, true
	}
	gv, gok := gl.Value()
	if ok && gok {
		if l.ModifiedAt().After(gl.ModifiedAt()) {
			return v, true
		}
		return gv, true
	}
	if gok {
		return gv, true
	}
	return v, ok
}

// lastSentValue returns the most recent LEVEL the daemon wrote to the CCU
// that has not yet been echoed back with a matching value, or (0, false)
// when the slot is empty or has aged out past [lastSentTTL].
func (l *Light) lastSentValue() (float64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.lastSentLevel == nil {
		return 0, false
	}
	if time.Since(l.lastSentAt) > lastSentTTL {
		return 0, false
	}
	return *l.lastSentLevel, true
}

// recordLastSent captures the LEVEL the daemon is about to write so
// `effectiveLevel` can fall back to it after a mismatching intermediate
// echo clears the optimistic tracker. Cleared by the LEVEL OnUpdate
// subscription on a matching echo, by [clearLastSent], or by TTL.
func (l *Light) recordLastSent(level float64) {
	l.mu.Lock()
	v := level
	l.lastSentLevel = &v
	l.lastSentAt = time.Now()
	l.mu.Unlock()
}

// clearLastSent drops the recorded send target. Called when a write fails
// synchronously so the optimistic / commanded surfaces do not keep
// reporting a never-sent value.
func (l *Light) clearLastSent() {
	l.mu.Lock()
	l.lastSentLevel = nil
	l.lastSentAt = time.Time{}
	l.mu.Unlock()
}

// Brightness returns the current brightness and whether it has been
// observed yet. Reads from the effective level (LEVEL_REAL / group
// LEVEL when bound, action-channel LEVEL otherwise) so transient
// intermediate values during a ramp do not surface.
func (l *Light) Brightness() (custom.Brightness, bool) {
	if l.Float == nil {
		return custom.Brightness{}, false
	}
	v, ok := l.effectiveLevel()
	return custom.NewBrightness(v), ok
}

// commandedBrightness returns the brightness implied by the most recent
// command. Reads through [effectiveLevel] so the (optimistic →
// last-sent-unconfirmed → group-level → raw) priority stays consistent
// with the state-property reads ([Brightness] / [IsOn]). Used by
// [IsStateChange] and the implicit-brightness fallback in [TurnOn] /
// [TurnOnWith].
func (l *Light) commandedBrightness() (custom.Brightness, bool) {
	if l.Float == nil {
		return custom.Brightness{}, false
	}
	v, ok := l.effectiveLevel()
	return custom.NewBrightness(v), ok
}

// IsOn reports whether the light is on (brightness > 0). The second
// bool flags whether the value has been observed yet. Reads from the
// effective level (see [Brightness]).
func (l *Light) IsOn() (on, observed bool) {
	b, ok := l.Brightness()
	return b.IsOn(), ok
}

// commandedIsOn reports the on/off state implied by the most recent
// command. See [commandedBrightness].
func (l *Light) commandedIsOn() (on, observed bool) {
	b, ok := l.commandedBrightness()
	return b.IsOn(), ok
}

// OnLevel feeds the CCU-reported LEVEL value (0.0–1.0) onto the
// shared channel-side data point. The lastLevel tracker fires
// through the registered OnUpdate callback.
func (l *Light) OnLevel(level float64) {
	if l.Float == nil {
		return
	}
	l.OnEvent(level)
}

// SetLevel sends a new LEVEL value. Non-dimmable lights accept only
// 0 or 1 and report an error otherwise.
func (l *Light) SetLevel(ctx context.Context, level float64, priority hmenum.CommandPriority) error {
	if !l.Capabilities.Dimmable && level != 0 && level != 1 {
		return errors.New("light: device is not dimmable, level must be 0 or 1")
	}
	b := custom.NewBrightness(level).Byte()
	turnOn := level > 0
	turnOff := level == 0
	if !l.IsStateChangeFull(StateChangeArgsFull{TurnOn: turnOn, TurnOff: turnOff, Brightness: &b}) {
		return nil
	}
	if l.Float == nil {
		return errors.New("light: SET level: channel has no LEVEL data point")
	}
	target := custom.NewBrightness(level).Level()
	l.recordLastSent(target)
	if err := l.Set(custom.EnsureContext(ctx), target, priority); err != nil {
		l.clearLastSent()
		return err
	}
	return nil
}

// TurnOn turns the light on at its previously remembered brightness
// (see [LastLevel]). When the light has never been observed in an "on"
// State, falls back to 100 %.
// `turn_on` without an explicit brightness restores the last known
// non-zero level instead of jumping to full power.
//
// When a deferred timer is pending (set via [SetTimerOnTime]
// [SetTimerRampTime]), the operation is bundled into a single atomic
// put_paramset of {LEVEL[, ON_TIME, RAMP_TIME]} —.
// "LEVEL": 1.0})`). The deferred timer is consumed by every TurnOn
// call.
func (l *Light) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	l.timerMu.Lock()
	on, ramp := l.pendingOn, l.pendingRamp
	l.pendingOn, l.pendingRamp = nil, nil
	l.timerMu.Unlock()
	if on == nil && ramp == nil {
		if !l.IsStateChangeFull(StateChangeArgsFull{TurnOn: true}) {
			return nil
		}
		// Signal lights with ON_TIME_UNIT send ON_TIME=NotUsed to cancel any
		// previously active timer. Without this, the old on-time remains active
		// after a plain TurnOn and the light switches itself off unexpectedly.
		// Other lights (RGBW/DALI) must not — the device reads the sentinel as a
		// shutdown duration and turns off again right away.
		if l.resetsOnTimeOnTurnOn && l.hasOnTimeUnit && l.Writer != nil {
			notUsed := time.Duration(NotUsed * float64(time.Second))
			b := l.turnOnLevel()
			onCfg := OnConfig{
				OnTime:     &notUsed,
				Brightness: &b,
				Writer:     l.Writer,
				Address:    l.DataPointKey().ChannelAddress,
			}
			return l.TurnOnWith(ctx, onCfg, priority)
		}
		return l.SetLevel(ctx, l.turnOnLevel(), priority)
	}
	cfg := OnConfig{}
	if on != nil {
		cfg.OnTime = on
	}
	if ramp != nil {
		cfg.RampTime = ramp
	}
	return l.TurnOnWith(ctx, cfg, priority)
}

// SetTimerOnTime stores `d` for the next [TurnOn] call. The next TurnOn
// merges it into one atomic put_paramset bundle.
// Pass a zero or negative duration to clear the deferred timer.
func (l *Light) SetTimerOnTime(d time.Duration) {
	l.timerMu.Lock()
	defer l.timerMu.Unlock()
	if d <= 0 {
		l.pendingOn = nil
		return
	}
	l.pendingOn = &d
}

// SetTimerRampTime is the RAMP_TIME companion of [SetTimerOnTime].
func (l *Light) SetTimerRampTime(d time.Duration) {
	l.timerMu.Lock()
	defer l.timerMu.Unlock()
	if d <= 0 {
		l.pendingRamp = nil
		return
	}
	l.pendingRamp = &d
}

// OnConfig bundles the optional fields a `TurnOnWith` call accepts. The Light
// (and ColorLight / ColorTempLight / FixedColorLight / RGBWLight EffectLight
// subtypes) interpret the relevant fields and silently ignore the rest.
type OnConfig struct {
	Brightness *float64
	Kelvin     *int32
	Hue        *int32
	Saturation *float64
	FixedColor *FixedColor
	Effect     *int32
	OnTime     *time.Duration
	RampTime   *time.Duration

	// Writer + Address are required for OnTime / RampTime / Effect
	// FixedColor when those fields are not part of the embedded
	// generic primitives. When unset, the relevant fields are ignored.
	Writer  custom.Writer
	Address string
}

// TurnOnWith dispatches every set-operation derived from cfg as a single
// atomic put_paramset bundle when the writer supports it. Without a
// [ParamsetWriter] the call falls back to sequential SetValue.
//
// LEVEL is always part of the payload (defaulting to LastLevel when no
// Brightness is supplied) so the CCU treats the call as a full "turn-on"
// command, not a stand-alone timer update.
//
// A [generic.CallParameterCollector] is attached to ctx so any future routing
// that consults [generic.CollectorFromContext] can batch this operation
// uniformly.
func (l *Light) TurnOnWith(ctx context.Context, cfg OnConfig, priority hmenum.CommandPriority) (err error) {
	ctx = custom.EnsureContext(ctx)
	// Attach a collector for forward-compatible batching. The internal
	// PutOrSet already handles atomicity for current callers. Anything
	// staged on it only reaches the wire in the flush, so the flush
	// error is part of this command's result.
	if l.Float != nil && l.Writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(l.Writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		defer func() {
			if err = generic.FlushCollector(ctx, coll, err); err != nil {
				l.clearLastSent()
			}
		}()
	}
	addr := cfg.Address
	if addr == "" && l.Float != nil {
		addr = l.DataPointKey().ChannelAddress
	}
	w := cfg.Writer
	if w == nil && l.Float != nil {
		w = l.Writer
	}
	if w == nil {
		// No writer at all → fall back to plain SetLevel which uses
		// the embedded *generic.Float's own writer.
		level := l.turnOnLevel()
		if cfg.Brightness != nil {
			level = *cfg.Brightness
		}
		return l.SetLevel(ctx, level, priority)
	}

	level := l.turnOnLevel()
	if cfg.Brightness != nil {
		level = *cfg.Brightness
	}
	if !l.Capabilities.Dimmable && level != 0 && level != 1 {
		return errors.New("light: device is not dimmable, level must be 0 or 1")
	}

	params := map[hmenum.Parameter]any{
		hmenum.ParameterLevel: level,
	}
	if cfg.OnTime != nil {
		params[hmenum.ParameterOnTime] = cfg.OnTime.Seconds()
	} else if cfg.RampTime != nil {
		// Ramp-without-on-time: the CCU otherwise treats ON_TIME as the implicit
		// "back to off" timer, which the operator did not request. Send the NotUsed
		// sentinel so RAMP_TIME runs stand-alone.
		params[hmenum.ParameterOnTime] = NotUsed
	}
	if cfg.RampTime != nil {
		params[hmenum.ParameterRampTime] = cfg.RampTime.Seconds()
	}
	l.recordLastSent(level)
	if err = custom.PutOrSet(ctx, w, addr, hmenum.ParamsetKeyValues, params, priority); err != nil {
		l.clearLastSent()
		return err
	}
	return nil
}

// NotUsed is the sentinel value the CCU honours when an ON_TIME or RAMP_TIME
// parameter must be left "unused" while another timer runs stand-alone.
const NotUsed = 111600.0

// TurnOffWithRamp turns the light off ramping the LEVEL down over
// `ramp` seconds. Sends ON_TIME=NotUsed + RAMP_TIME + LEVEL=0
// atomically when the writer supports put_paramset. The
// ON_TIME=NotUsed sentinel is required by the CCU so the device
// does not silently overlay an implicit off-timer on top of the ramp.
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (l *Light) TurnOffWithRamp(ctx context.Context, ramp time.Duration, priority hmenum.CommandPriority) error {
	if ramp <= 0 {
		return l.TurnOff(ctx, priority)
	}
	addr := ""
	if l.Float != nil {
		addr = l.DataPointKey().ChannelAddress
	}
	var w custom.Writer
	if l.Float != nil {
		w = l.Writer
	}
	if w == nil {
		return l.TurnOff(ctx, priority)
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(w), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	l.recordLastSent(0.0)
	err := custom.PutOrSet(ctx, w, addr, hmenum.ParamsetKeyValues, map[hmenum.Parameter]any{
		hmenum.ParameterOnTime:   NotUsed,
		hmenum.ParameterRampTime: ramp.Seconds(),
		hmenum.ParameterLevel:    0.0,
	}, priority)
	// Anything staged on the collector only reaches the wire in the
	// flush, so its error is part of this command's result.
	if err = generic.FlushCollector(ctx, coll, err); err != nil {
		l.clearLastSent()
		return err
	}
	return nil
}

// TurnOff clears any pending deferred timer before checking for a state
// change, then drives LEVEL to 0. Clearing the timer first ensures that a
// pending set_timer_on_time / set_timer_ramp_time that was queued for a
// subsequent TurnOn does not interfere with the off command and mirrors the
// reset_timer_on_time() pre-guard in the Python reference (light.py:396).
func (l *Light) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	l.timerMu.Lock()
	l.pendingOn = nil
	l.pendingRamp = nil
	l.timerMu.Unlock()
	if !l.IsStateChangeFull(StateChangeArgsFull{TurnOff: true}) {
		return nil
	}
	return l.SetLevel(ctx, 0.0, priority)
}

// BrightnessPct returns the current brightness as a percentage (0-100).
// Returns observed=false when the light has not yet been observed.
func (l *Light) BrightnessPct() (pct int, observed bool) {
	b, ok := l.Brightness()
	if !ok {
		return 0, false
	}
	return int(b.Level()*100 + 0.5), true
}

// resolveOnTimeParams determines the wire parameter(s) that carry a
// light's on-time timer. No CCU device carries ON_TIME_VALUE / ON_TIME_UNIT
// — those literal names exist in hmenum but appear on no light's paramset
// in the fleet — so probing for them always misses and the write is
// rejected or dropped. The real shapes are family-specific: plain dimmers
// (HM-LC-Dim*, HmIP-BDT/-PDT/-FDT) carry a single FLOAT ON_TIME in
// seconds; signal lights and RGBW dimmers (HmIP-BSL, -RGBW, -DRG-DALI)
// carry the value/unit pair DURATION_VALUE + DURATION_UNIT, exactly like
// RAMP_TIME_VALUE/RAMP_TIME_UNIT. Mirrors the profile's
// FieldOnTimeValue/FieldOnTimeUnit mapping (generated_profile_configs.go),
// read here directly off the wire so the probe works regardless of which
// profile registered the channel.
func resolveOnTimeParams(ch *device.Channel) (valueParam, unitParam hmenum.Parameter) {
	if ch != nil && ch.Parameter(hmenum.ParameterDurationValue) != nil && ch.Parameter(hmenum.ParameterDurationUnit) != nil {
		return hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit
	}
	return hmenum.ParameterOnTime, ""
}

// SetOnTime sets the light's on-time timer, encoding the duration into
// whichever wire shape [resolveOnTimeParams] resolved at construction: a
// bare seconds value for the plain-dimmer families, or a value/unit pair
// for signal lights and RGBW dimmers.
func (l *Light) SetOnTime(ctx context.Context, w custom.Writer, addr string, d time.Duration, priority hmenum.CommandPriority) error {
	valueParam := l.onTimeValueParam
	if valueParam == "" {
		valueParam = hmenum.ParameterOnTime
	}
	if l.onTimeUnitParam == "" {
		if err := w.SetValue(custom.EnsureContext(ctx), addr, valueParam, d.Seconds(), priority); err != nil {
			return fmt.Errorf("light: ON_TIME: %w", err)
		}
		return nil
	}
	value, unit := custom.EncodeTimerDuration(d)
	if err := stageTimerPair(ctx, w, addr,
		valueParam, value, l.onTimeUnitParam, unit, priority); err != nil {
		return fmt.Errorf("light: ON_TIME: %w", err)
	}
	return nil
}

// SetRampTime sets the RAMP_TIME parameter on the light's channel.
func (l *Light) SetRampTime(ctx context.Context, w custom.Writer, addr string, d time.Duration, priority hmenum.CommandPriority) error {
	value, unit := custom.EncodeTimerDuration(d)
	if err := stageTimerPair(ctx, w, addr,
		hmenum.ParameterRampTimeValue, value, hmenum.ParameterRampTimeUnit, unit, priority); err != nil {
		return fmt.Errorf("light: RAMP_TIME: %w", err)
	}
	return nil
}

// stageTimerPair writes a value/unit parameter pair, through the
// collector when the caller opened one.
//
// These pairs qualify the level they accompany and are meaningless
// apart from it, so they belong in the same wire call: a dimmer that
// receives its level and its ramp time as separate messages ramps from
// whatever the previous command left behind. The pair carries no
// observable state of its own, which is why it is staged as bare
// parameters rather than as data points.
func stageTimerPair(
	ctx context.Context, w custom.Writer, addr string,
	valueParam hmenum.Parameter, value any,
	unitParam hmenum.Parameter, unit any,
	priority hmenum.CommandPriority,
) error {
	ctx = custom.EnsureContext(ctx)
	if coll := generic.CollectorFromContext(ctx); coll != nil {
		errValue := coll.AddParam(addr, hmenum.ParamsetKeyValues, string(valueParam), value, 0)
		errUnit := coll.AddParam(addr, hmenum.ParamsetKeyValues, string(unitParam), unit, 0)
		if errValue == nil && errUnit == nil {
			return nil
		}
		// A consumed collector falls through to direct writes.
	}
	if err := w.SetValue(ctx, addr, valueParam, value, priority); err != nil {
		return err
	}
	return w.SetValue(ctx, addr, unitParam, unit, priority)
}

// NamePostfix returns the suffix appended to a light's data-point name
// In The base [Light] adds nothing
// subtypes (ColorLight, ColorTempLight, FixedColorLight, EffectLight,
// RGBWLight) override to produce e.g. "_color", "_color_temp",
// "_effect", "_hs". Mirrors `data_point_name_postfix` on
// `model/custom/light.py`.
func (l *Light) NamePostfix() string { return "" }

// IsStateChange reports whether the proposed light command would change the
// device state. Returns true when:
//
// - turnOn is true and the light is currently off (or unobserved). - turnOff
// is true and the light is currently on (or unobserved). - brightness is
// non-nil and differs from the current brightness byte.
//
// Reads through `commandedBrightness` / `commandedIsOn`, which share the
// effective-level priority order with [Brightness] / [IsOn] (optimistic →
// last-sent-unconfirmed → group-level → raw action LEVEL). The fix for the
// "dimmer flips back during ramp" regression routed all four properties
// through the same source so a redundant command is judged against the
// same ramp-aware view the user sees.
//
// Returns true when no current value has been observed (first command always
// goes through). Thread-safe.
func (l *Light) IsStateChange(turnOn, turnOff bool, brightness *uint8) bool {
	if on, ok := l.commandedIsOn(); !ok {
		// Not yet observed — allow the command.
		return true
	} else if turnOn && !on {
		return true
	} else if turnOff && on {
		return true
	}
	if brightness != nil {
		if b, ok := l.commandedBrightness(); !ok || b.Byte() != *brightness {
			return true
		}
	}
	return false
}

// StateChangeArgsFull bundles every field Python's
// `CustomDpDimmer.is_state_change` (light.py:361) compares. Each pointer
// field is optional — only non-nil fields are compared against the
// current commanded state. Mirrors the Python signature one-to-one so
// callers that want full-parity-state-suppression can use it instead
// of the narrower [Light.IsStateChange] (3-arg) form.
type StateChangeArgsFull struct {
	TurnOn          bool
	TurnOff         bool
	Brightness      *uint8
	HSColor         *HSColor // hue (0..359), saturation (0..1)
	ColorTempKelvin *uint16  // colour temperature in Kelvin
	Effect          *string  // active effect name; "" means no effect
	OnTime          *float64 // seconds the light should stay on
	RampTime        *float64 // seconds the dimmer should ramp up/down
}

// HSColor is the (hue, saturation) pair Light commands accept.
type HSColor struct {
	Hue        float64 // 0..359
	Saturation float64 // 0..1
}

// IsStateChangeFull is the eight-key counterpart to [IsStateChange],
// mirroring Python's `CustomDpDimmer.is_state_change` (light.py:361).
// Returns true when any non-nil field of args differs from the
// currently-commanded state — or when no state has been observed yet.
//
// This is the canonical state-change suppression surface for Light
// chains that drive HSColor / ColorTemp / Effect / OnTime / RampTime
// from a single HA service call. Callers that only care about
// on/off/brightness can keep using the narrower [IsStateChange].
func (l *Light) IsStateChangeFull(args StateChangeArgsFull) bool {
	if l.IsStateChange(args.TurnOn, args.TurnOff, args.Brightness) {
		return true
	}
	if args.HSColor != nil {
		if hs, ok := l.commandedHSColor(); !ok || hs != *args.HSColor {
			return true
		}
	}
	if args.ColorTempKelvin != nil {
		if k, ok := l.commandedColorTempKelvin(); !ok || k != *args.ColorTempKelvin {
			return true
		}
	}
	if args.Effect != nil {
		if e, ok := l.commandedEffect(); !ok || e != *args.Effect {
			return true
		}
	}
	if args.OnTime != nil {
		if t, ok := l.commandedOnTime(); !ok || t != *args.OnTime {
			return true
		}
	}
	if args.RampTime != nil {
		if t, ok := l.commandedRampTime(); !ok || t != *args.RampTime {
			return true
		}
	}
	return false
}

// commandedHSColor / commandedColorTempKelvin / commandedEffect /
// commandedOnTime / commandedRampTime are stubs in the base Light
// type. ColorLight / ColorTempLight / EffectLight override the
// relevant accessor; Light itself returns `(_, false)` so a generic
// Light without these capabilities reports "not commanded".
//
// The override pattern is class-level: each subclass overrides only
// the commanded-* keys it owns and the base class returns the
// "no observation yet" tuple for the rest. Since Go has no virtual
// dispatch we expose hook methods; the concrete Light subtypes
// (color.go, effect.go, …) can shadow these when they hold the
// corresponding cached state.
func (l *Light) commandedHSColor() (HSColor, bool)        { _ = l; return HSColor{}, false }
func (l *Light) commandedColorTempKelvin() (uint16, bool) { _ = l; return 0, false }
func (l *Light) commandedEffect() (string, bool)          { _ = l; return "", false }
func (l *Light) commandedOnTime() (float64, bool)         { _ = l; return 0, false }
func (l *Light) commandedRampTime() (float64, bool)       { _ = l; return 0, false }
