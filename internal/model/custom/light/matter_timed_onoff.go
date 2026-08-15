// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// matterTimedHold is the reserved "hold indefinitely" value for the
// OnTime / OffWaitTime countdowns (Matter §1.5.8): a countdown parked
// at 0xFFFF does not decrement. Command fields cap at 65534, so the
// hold value can only be reached via an attribute write.
const matterTimedHold uint16 = 0xFFFF

// matterTimedTickInterval is the countdown resolution. OnTime and
// OffWaitTime are expressed in tenths of a second; matter.js drives
// both countdowns with a 100 ms periodic timer
// (OnOffServer.ts:230/:258 Time.getPeriodicTimer(..., Millis(100))).
const matterTimedTickInterval = 100 * time.Millisecond

// timedOnOffState is the OnOff cluster LT (Lighting) timed-command
// engine state: the OnTime / OffWaitTime countdowns driven by
// OnWithTimedOff, the StartUpOnOff attribute store, and the
// GlobalSceneControl attribute. Owned by [Light] (not the
// per-assembly lightOnOffServer projection) so this state survives
// cluster-server reconstruction across MatterClusterServers calls.
//
// Semantics are a transliteration of matter.js OnOffServer.ts: the
// two logical timers (timed-on while the device is on, delayed-off
// guard while it is off) tick at 100 ms and decrement their
// attribute by one tenth-of-a-second step. A single goroutine drives
// both (see ensureLoopLocked for its lifecycle).
type timedOnOffState struct {
	mu          sync.Mutex
	onTime      uint16 // tenths of a second; 0xFFFF holds
	offWaitTime uint16 // tenths of a second; 0xFFFF holds
	// startUpOnOff stores the nullable StartUpOnOff attribute
	// (0=Off, 1=On, 2=Toggle; nil=null "keep last state"). In-memory
	// only — like NodeLabel/Location it does not yet survive a daemon
	// restart. The physical power-on level of an HM device stays
	// governed by its own device configuration.
	startUpOnOff *uint8

	// globalSceneControl mirrors the LT-gated GlobalSceneControl
	// attribute (0x4000): true after On / OnWithTimedOff /
	// OnWithRecallGlobalScene, false after OffWithEffect; a plain Off
	// leaves it unchanged. Defaults to true (set by [New]). matter.js
	// OnOffServer.ts:97-104 (on), :158-169 (offWithEffect).
	globalSceneControl bool

	timedOnActive    bool
	delayedOffActive bool
	// priority carries the CommandPriority of the arming command so
	// the autonomous off at countdown expiry writes to the CCU with
	// the same urgency the controller requested.
	priority hmenum.CommandPriority
	// stop is non-nil while a tick goroutine runs; maybeStopLocked
	// closes it (ending that goroutine) when both countdowns idle.
	// Guarded by mu.
	stop chan struct{}
	// loopDisabled suppresses the wall-clock tick goroutine. Test
	// hook: hermetic tests drive the countdown via matterTimedAdvance
	// directly, so a real 100 ms ticker would race their assertions.
	loopDisabled bool
}

// ensureLoopLocked starts the tick goroutine when none is running.
// Caller holds t.mu and has just armed a countdown.
//
// Goroutine lifecycle: one goroutine per stop channel. It ticks every
// 100 ms and applies matterTimedAdvance until maybeStopLocked closes
// its channel (countdown expiry, a park-write, or an On cancelling
// the delayed-off guard). A later re-arm creates a fresh channel and
// goroutine; a stale goroutine observing its closed channel exits.
func (t *timedOnOffState) ensureLoopLocked(l *Light) {
	if t.stop != nil || t.loopDisabled {
		return
	}
	stop := make(chan struct{})
	t.stop = stop
	go l.matterTimedLoop(stop)
}

// matterTimedLoop is the countdown tick goroutine body. It applies one
// [Light.matterTimedAdvance] step per 100 ms until its stop channel
// closes. The loop is autonomous by design — the arming invoke's ctx
// died with its response, so a countdown expiry writes on a fresh
// background ctx.
func (l *Light) matterTimedLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(matterTimedTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			l.matterTimedAdvance(context.Background())
		}
	}
}

// stopLoop abandons both countdowns and ends the tick goroutine. Called
// from [Light.Close], i.e. when the channel this Light hangs off is torn
// down (device removal, cache clear, custom-DP replacement).
//
// Without it the only way out of the loop is a countdown reaching zero,
// so a light armed by OnWithTimedOff kept a goroutine — and the retired
// Light, Channel and Device graph behind it — alive for up to the
// 65534-tenths maximum, then issued a real CCU write for a device the
// daemon no longer models.
func (t *timedOnOffState) stopLoop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timedOnActive = false
	t.delayedOffActive = false
	t.maybeStopLocked()
}

// maybeStopLocked ends the tick goroutine when both countdowns are
// idle. Caller holds t.mu.
func (t *timedOnOffState) maybeStopLocked() {
	if t.stop != nil && !t.timedOnActive && !t.delayedOffActive {
		close(t.stop)
		t.stop = nil
	}
}

// matterTimedHandleOn applies the LT bookkeeping of a successful On
// command path (On, Toggle-on, OnWithRecallGlobalScene, and the
// turn-on tail of OnWithTimedOff). Mirrors matter.js
// OnOffServer.ts:97-112 on(): GlobalSceneControl is unconditionally
// set true; while no timed-on phase runs (OnTime == 0) an On cancels
// the delayed-off guard and clears OffWaitTime; during a timed-on
// phase (including the 0xFFFF hold) OffWaitTime is retained.
func (l *Light) matterTimedHandleOn() {
	t := &l.timed
	t.mu.Lock()
	defer t.mu.Unlock()
	t.globalSceneControl = true
	if t.onTime == 0 {
		t.delayedOffActive = false
		t.offWaitTime = 0
		t.maybeStopLocked()
	}
}

// matterTimedHandleOff applies the LT bookkeeping of a successful Off
// command path (Off, OffWithEffect, Toggle-off). Mirrors matter.js
// OnOffServer.ts:119-139 off(): the timed-on countdown stops and
// OnTime resets to 0; an Off while OffWaitTime > 0 enters the
// delayed-off guard period (Matter §1.5.7.6.4) unless parked at the
// 0xFFFF hold.
func (l *Light) matterTimedHandleOff() {
	t := &l.timed
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timedOnActive = false
	t.onTime = 0
	if t.offWaitTime > 0 && t.offWaitTime != matterTimedHold && !t.delayedOffActive {
		t.delayedOffActive = true
		t.ensureLoopLocked(l)
	}
	t.maybeStopLocked()
}

// matterOnWithTimedOff executes the OnWithTimedOff command (0x42).
// Mirrors matter.js OnOffServer.ts:198-225 onWithTimedOff():
//
//   - AcceptOnlyWhenOn (OnOffControl bit 0) set while the device is
//     off → silent Success no-op.
//   - Device off inside a running delayed-off guard → only lower
//     OffWaitTime to the requested value, stay off.
//   - Otherwise OnTime rises to max(request, current), OffWaitTime is
//     replaced, the timed-on countdown arms (unless OnTime is 0 or the
//     0xFFFF hold) and the device turns on.
func (l *Light) matterOnWithTimedOff(ctx context.Context, onOffControl uint8, onTime, offWaitTime uint16, priority hmenum.CommandPriority) error {
	const acceptOnlyWhenOnBit = 0x01
	on, _ := l.IsOn()
	if onOffControl&acceptOnlyWhenOnBit != 0 && !on {
		return nil
	}

	t := &l.timed
	t.mu.Lock()
	if t.offWaitTime > 0 && !on {
		// Delayed-off guard period: only lower the wait, stay off.
		// matter.js OnOffServer.ts:203-214.
		t.offWaitTime = min(offWaitTime, t.offWaitTime)
		if !t.delayedOffActive && t.offWaitTime > 0 && t.offWaitTime != matterTimedHold {
			t.delayedOffActive = true
			t.ensureLoopLocked(l)
		}
		t.maybeStopLocked()
		t.mu.Unlock()
		return nil
	}
	prevOnTime, prevOffWait := t.onTime, t.offWaitTime
	prevTimedOn, prevDelayedOff := t.timedOnActive, t.delayedOffActive
	t.onTime = max(onTime, t.onTime)
	t.offWaitTime = offWaitTime
	t.priority = priority
	if t.onTime != 0 && t.onTime != matterTimedHold {
		t.timedOnActive = true
		t.ensureLoopLocked(l)
	} else {
		t.timedOnActive = false
	}
	t.maybeStopLocked()
	t.mu.Unlock()

	if err := l.TurnOn(ctx, priority); err != nil {
		// Roll the armed countdown back — matter.js state mutations are
		// transactional and revert when on() fails, so a rejected CCU
		// write must not leave a ticking timed-on phase behind.
		t.mu.Lock()
		t.onTime, t.offWaitTime = prevOnTime, prevOffWait
		t.timedOnActive, t.delayedOffActive = prevTimedOn, prevDelayedOff
		if t.timedOnActive || t.delayedOffActive {
			t.ensureLoopLocked(l)
		}
		t.maybeStopLocked()
		t.mu.Unlock()
		return err
	}
	l.matterTimedHandleOn()
	return nil
}

// matterOnTime returns the current OnTime attribute value (0x4001).
func (l *Light) matterOnTime() uint16 {
	l.timed.mu.Lock()
	defer l.timed.mu.Unlock()
	return l.timed.onTime
}

// matterOffWaitTime returns the current OffWaitTime attribute value (0x4002).
func (l *Light) matterOffWaitTime() uint16 {
	l.timed.mu.Lock()
	defer l.timed.mu.Unlock()
	return l.timed.offWaitTime
}

// matterSetOnTime applies an OnTime attribute write. Writes never
// start a countdown — only OnWithTimedOff arms one — but a write that
// parks the attribute at 0 or the 0xFFFF hold ends a running
// countdown. Mirrors matter.js OnOffServer.ts:66-84 #stopHeldTimer.
func (l *Light) matterSetOnTime(v uint16) {
	t := &l.timed
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onTime = v
	if t.timedOnActive && (v == 0 || v == matterTimedHold) {
		t.timedOnActive = false
	}
	t.maybeStopLocked()
}

// matterSetOffWaitTime applies an OffWaitTime attribute write. Same
// park semantics as [Light.matterSetOnTime] for the delayed-off guard.
func (l *Light) matterSetOffWaitTime(v uint16) {
	t := &l.timed
	t.mu.Lock()
	defer t.mu.Unlock()
	t.offWaitTime = v
	if t.delayedOffActive && (v == 0 || v == matterTimedHold) {
		t.delayedOffActive = false
	}
	t.maybeStopLocked()
}

// matterStartUpOnOff returns the stored nullable StartUpOnOff value
// (nil = TLV null, "keep last state").
func (l *Light) matterStartUpOnOff() *uint8 {
	l.timed.mu.Lock()
	defer l.timed.mu.Unlock()
	return l.timed.startUpOnOff
}

// matterSetStartUpOnOff stores a StartUpOnOff attribute write.
func (l *Light) matterSetStartUpOnOff(v *uint8) {
	l.timed.mu.Lock()
	defer l.timed.mu.Unlock()
	l.timed.startUpOnOff = v
}

// matterGlobalSceneControl returns the current GlobalSceneControl
// attribute value (0x4000).
func (l *Light) matterGlobalSceneControl() bool {
	l.timed.mu.Lock()
	defer l.timed.mu.Unlock()
	return l.timed.globalSceneControl
}

// matterClearGlobalSceneControl forces GlobalSceneControl to false —
// the OffWithEffect side effect. A plain Off never calls this: mirrors
// matter.js OnOffServer.ts:158-169 offWithEffect(), which flips the
// attribute in addition to calling off() (off() itself leaves
// GlobalSceneControl untouched).
func (l *Light) matterClearGlobalSceneControl() {
	l.timed.mu.Lock()
	defer l.timed.mu.Unlock()
	l.timed.globalSceneControl = false
}

// matterTimedAdvance applies one 100 ms countdown step. Exposed as a
// method (rather than buried in the goroutine) so tests drive the
// countdown hermetically without wall-clock waits. ctx scopes the
// CCU write a timed-on expiry issues; the tick goroutine supplies a
// background ctx because the arming invoke's context is gone.
//
// Mirrors matter.js OnOffServer.ts:239-253 #timedOnTick and
// :312-325 #delayedOffTick: a countdown parked at the 0xFFFF hold
// stops without decrementing; the timed-on expiry clears OffWaitTime
// and turns the device off; the delayed-off expiry simply ends the
// guard period.
func (l *Light) matterTimedAdvance(ctx context.Context) {
	t := &l.timed
	t.mu.Lock()
	var turnOff bool
	var priority hmenum.CommandPriority
	if t.timedOnActive {
		switch {
		case t.onTime == matterTimedHold:
			t.timedOnActive = false
		case t.onTime <= 1:
			t.onTime = 0
			t.timedOnActive = false
			t.offWaitTime = 0
			turnOff = true
			priority = t.priority
		default:
			t.onTime--
		}
	}
	if t.delayedOffActive {
		switch {
		case t.offWaitTime == matterTimedHold:
			t.delayedOffActive = false
		case t.offWaitTime <= 1:
			t.offWaitTime = 0
			t.delayedOffActive = false
		default:
			t.offWaitTime--
		}
	}
	t.maybeStopLocked()
	t.mu.Unlock()

	if turnOff {
		// Autonomous off at timed-on expiry. matter.js awaits off()
		// inside the tick; the bridge maps it onto a CCU write.
		_ = l.TurnOff(custom.EnsureContext(ctx), priority)
		l.dataVersion.Bump()
	}
}
