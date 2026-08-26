// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// Two-phase debounce for GoToLiftPercentage / GoToTiltPercentage slider
// gestures. Apple Home / Google Home slider drags emit 5-10 position
// commands in quick succession; HM cover actuators sit behind a
// duty-cycle-limited radio, and forwarding every intermediate value as
// its own LEVEL write makes the motor stutter and burns duty cycle. The
// client-side coalescer only folds identical in-flight calls, so
// distinct slider values all reach the wire without this layer.
//
// matter.js has no debounce here — its WindowCoveringServer drives a
// native motor that consumes every target write. The coalescing is
// bridge-domain radio protection layered onto the same
// accepted-before-written command contract matter.js already has:
// goToLiftPercentage stores the target and returns while the movement
// itself runs as a detached worker (WindowCoveringServer.ts:574-589;
// :379-383 "this method returns before actual movement completes").
const (
	// goToDebounceGestureStart delays the first command of a gesture. A
	// quick swipe fires an unwanted intermediate step value first; the
	// longer window lets the real destination replace it before any
	// radio write happens.
	goToDebounceGestureStart = 400 * time.Millisecond
	// goToDebounceActiveDrag delays follow-up commands while the slider
	// is still moving — each new value replaces the pending one, so
	// only the value the drag settles on reaches the CCU.
	goToDebounceActiveDrag = 150 * time.Millisecond
	// goToGestureGap is the idle time after which the next command
	// counts as the start of a new gesture rather than a continuation
	// of the previous drag.
	goToGestureGap = 600 * time.Millisecond
	// goToAtTargetTolerance (percent100ths) is the "already there"
	// band: a command whose destination is within 1 % of the current
	// position is acknowledged without any radio write.
	goToAtTargetTolerance = 100
)

// goToAxis names the per-device debounce slots. Lift and tilt debounce
// independently — a tilt adjustment must not swallow a pending lift
// write and vice versa.
type goToAxis int

const (
	goToAxisLift goToAxis = iota
	goToAxisTilt

	goToAxisCount // number of slots; keep last
)

// String labels the axis for log enrichment.
func (a goToAxis) String() string {
	if a == goToAxisTilt {
		return "tilt"
	}
	return "lift"
}

// goToDebouncer holds one pending deferred CCU write per movement axis.
// The zero value is ready to use; state is owned by the custom data
// point (not the per-call cluster-server value) so pending writes
// survive cluster-server reconstruction and can be cancelled when the
// data point is detached.
type goToDebouncer struct {
	mu    sync.Mutex
	slots [goToAxisCount]goToSlot

	// now and afterFunc are test seams; nil selects the real clock and
	// time.AfterFunc. An afterFunc implementation must not invoke the
	// callback synchronously — schedule holds the mutex while arming.
	now       func() time.Time
	afterFunc func(time.Duration, func()) *time.Timer
}

// goToSlot is the pending-command state of one axis. gen invalidates
// stale timer callbacks: schedule and cancel bump it, and a callback
// that fired for an older generation returns without running the
// (already replaced or dropped) write.
type goToSlot struct {
	gen     uint64
	timer   *time.Timer
	write   func()
	lastCmd time.Time
}

// schedule (re-)arms the axis slot with a deferred write, replacing any
// pending one. The delay is two-phase: the first command after an idle
// gap starts a fresh gesture and waits goToDebounceGestureStart; a
// command arriving within goToGestureGap of the previous one is part of
// an active drag and waits only goToDebounceActiveDrag.
//
// write must have copied every value it needs before schedule is
// called — it runs on a timer goroutine long after the Matter invoke
// returned and must not read live command state.
func (d *goToDebouncer) schedule(axis goToAxis, write func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	slot := &d.slots[axis]

	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	delay := goToDebounceGestureStart
	if !slot.lastCmd.IsZero() && now.Sub(slot.lastCmd) <= goToGestureGap {
		delay = goToDebounceActiveDrag
	}
	slot.lastCmd = now

	if slot.timer != nil {
		slot.timer.Stop()
	}
	slot.gen++
	slot.write = write

	after := time.AfterFunc
	if d.afterFunc != nil {
		after = d.afterFunc
	}
	gen := slot.gen
	slot.timer = after(delay, func() {
		d.fire(axis, gen)
	})
}

// fire runs the pending write of axis when gen still names the current
// generation; a replaced or cancelled slot no-ops. The write itself
// runs outside the mutex — it performs a (potentially slow) CCU call.
func (d *goToDebouncer) fire(axis goToAxis, gen uint64) {
	d.mu.Lock()
	slot := &d.slots[axis]
	if slot.gen != gen || slot.write == nil {
		d.mu.Unlock()
		return
	}
	write := slot.write
	slot.write = nil
	slot.timer = nil
	d.mu.Unlock()
	write()
}

// scheduleWrite arms the axis slot with a deferred CCU write. The write
// receives the invoke context detached from its cancellation
// (context.WithoutCancel): the Matter invoke context is cancelled when
// the command handler returns, long before the timer fires, but its
// values (request enrichment) stay useful. A failing deferred write is
// logged — the command was already acknowledged
// (accepted-before-written; see the package comment above), and the
// position mismatch surfaces through the normal CCU value-event echo.
func (d *goToDebouncer) scheduleWrite(ctx context.Context, axis goToAxis, address string, write func(context.Context) error) {
	detached := context.WithoutCancel(custom.EnsureContext(ctx))
	d.schedule(axis, func() {
		if err := write(detached); err != nil {
			slog.Warn("cover: deferred window covering position write failed",
				slog.String("address", address),
				slog.String("axis", axis.String()),
				slog.Any("error", err))
		}
	})
}

// cancel drops the pending write of one axis, if any. Called when a
// newer command supersedes the staged value without needing it
// (StopMotion, UpOrOpen, DownOrClose, or a GoTo that is already at
// target).
func (d *goToDebouncer) cancel(axis goToAxis) {
	d.mu.Lock()
	slot := &d.slots[axis]
	slot.gen++
	if slot.timer != nil {
		slot.timer.Stop()
		slot.timer = nil
	}
	slot.write = nil
	d.mu.Unlock()
}

// cancelAll stops every pending write. Wired into the data point's
// unsubscribe path so a detached cover (channel teardown, central
// shutdown) never fires a timer that writes to the CCU afterwards.
func (d *goToDebouncer) cancelAll() {
	for axis := goToAxis(0); axis < goToAxisCount; axis++ {
		d.cancel(axis)
	}
}

// goToAtTarget reports whether the commanded percent100ths destination
// is within goToAtTargetTolerance of the currently observed position.
// An unobserved position is never "at target" — the write must go out.
func goToAtTarget(pct uint16, position func() (custom.Position, bool)) bool {
	pos, ok := position()
	if !ok {
		return false
	}
	current := hmLevelToMatterPct100ths(pos.Level())
	diff := int(pct) - int(current)
	if diff < 0 {
		diff = -diff
	}
	return diff <= goToAtTargetTolerance
}

// dispatchGoToPercentage is the shared acceptance path for
// GoToLiftPercentage / GoToTiltPercentage across the Cover, Blind, and
// Garage projections:
//
//   - already at target (within 1 %): acknowledge without writing. Any
//     pending intermediate drag value is dropped too — the freshest
//     intent is "stay where you are".
//   - otherwise: store the commanded target immediately (matter.js sets
//     TargetPosition*Percent100ths before triggering motion,
//     WindowCoveringServer.ts:578/:600) so attribute reads and the
//     motion-direction inference see it at once, then defer the CCU
//     write through the two-phase debouncer.
//
// write receives the HM domain-level converted from pct — captured
// here, before scheduling, so the timer callback reads no live state.
func dispatchGoToPercentage(
	ctx context.Context,
	deb *goToDebouncer,
	axis goToAxis,
	address string,
	pct uint16,
	position func() (custom.Position, bool),
	storeTarget func(uint16),
	write func(ctx context.Context, hmLevel float64) error,
) {
	storeTarget(pct)
	if goToAtTarget(pct, position) {
		deb.cancel(axis)
		return
	}
	level := matterPct100thsToHMLevel(pct)
	deb.scheduleWrite(ctx, axis, address, func(ctx context.Context) error {
		return write(ctx, level)
	})
}
