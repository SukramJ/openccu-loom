// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// ErrCoalescerCleared is returned to every caller waiting on an
// in-flight coalesced call when [Coalescer.Clear] abandons it.
var ErrCoalescerCleared = errors.New("reliability: coalesced call cleared")

// ErrCoalescedCallPanicked is returned to every caller of a coalesced
// call whose function panicked.
var ErrCoalescedCallPanicked = errors.New("reliability: coalesced call panicked")

// CoalescerStats is a snapshot of [Coalescer] counters.
type CoalescerStats struct {
	// Total is the total number of [Coalescer.Do] calls.
	Total uint64
	// Executed is the number of calls that actually ran the inner
	// function (i.e. the leader of a coalesce group).
	Executed uint64
	// Coalesced is the number of calls that piggy-backed on an
	// in-flight call (Total - Executed once the in-flight count is
	// drained).
	Coalesced uint64
	// Failed counts inner-function errors.
	Failed uint64
	// InFlight is the current number of distinct keys coalescing.
	InFlight int
}

// CoalesceHook is called whenever a concurrent caller piggy-backs on an
// in-flight request. The leader call (the one that actually runs fn) does NOT
// trigger the hook — only followers do.
//
// Hooks are called synchronously while no internal lock is held, so a hook
// may safely call back into the coalescer.
type CoalesceHook func(key string, waiters int)

// Coalescer deduplicates concurrent calls with the same key. The first
// caller runs the function; concurrent callers with the same key wait
// and receive the same result.
//
// Typical use: read-only paramset descriptions — we only ever need one
// in-flight fetch per (interface, channel, paramset-key) triple even
// if twenty coroutines ask for it.
type Coalescer struct {
	mu    sync.Mutex
	calls map[string]*coalescedCall

	hook      CoalesceHook
	total     uint64
	executed  uint64
	coalesced uint64
	failed    uint64
}

type coalescedCall struct {
	done chan struct{}
	// cancel stops the shared call. It runs when the last participant
	// leaves and when [Coalescer.Clear] abandons the call.
	cancel context.CancelFunc
	// followers counts how many callers piggy-backed on this call. It is
	// the number the [CoalesceHook] reports and never decreases.
	// Guarded by the Coalescer's mutex.
	followers int
	// active counts the participants still waiting for the result, the
	// leader included; the shared call is cancelled when it reaches zero.
	// Guarded by the Coalescer's mutex.
	active int

	mu      sync.Mutex // guards val, err and settled
	val     any
	err     error
	settled bool
}

// settle records the outcome and releases every waiter. The first
// settlement wins: the leader finishing after [Coalescer.Clear] abandoned
// the call (or the other way round) leaves the published result alone and
// must not close the channel twice.
func (call *coalescedCall) settle(v any, err error) {
	call.mu.Lock()
	defer call.mu.Unlock()
	if call.settled {
		return
	}
	call.settled = true
	call.val, call.err = v, err
	close(call.done)
}

// result returns the settled outcome.
func (call *coalescedCall) result() (any, error) {
	call.mu.Lock()
	defer call.mu.Unlock()
	return call.val, call.err
}

// NewCoalescer returns a fresh coalescer.
func NewCoalescer() *Coalescer {
	return &Coalescer{calls: make(map[string]*coalescedCall)}
}

// MakeCoalesceKey builds a canonical deduplication key from an RPC method
// name and its positional arguments. The resulting key is a stable, printable
// string suitable for use as the key argument of [Coalescer.Do].
//
// The function joins method and every argument's string representation with
// ":" separators. Callers that only need to deduplicate by (method,
// first-arg) can safely pass a single-element args slice. Passing nil args
// yields just the method name as the key.
func MakeCoalesceKey(method string, args []any) string {
	if len(args) == 0 {
		return method
	}
	// Pre-size: method + colon + rough estimate per arg.
	var key strings.Builder
	key.WriteString(method)
	for _, a := range args {
		key.WriteString(":" + fmt.Sprint(a))
	}
	return key.String()
}

// SetHook installs a [CoalesceHook] called once per coalesced
// follower. Pass nil to clear. Replacing an existing hook is allowed.
func (c *Coalescer) SetHook(h CoalesceHook) {
	c.mu.Lock()
	c.hook = h
	c.mu.Unlock()
}

// Do runs fn only once per concurrent key. Subsequent callers with the
// same key wait for the first call to complete and get its result.
// Completed calls are purged so a later invocation starts a fresh run.
//
// The shared call belongs to the group, not to the caller that happened
// to start it: it runs under a context detached from that caller's
// cancellation, and is cancelled once every participant has left. A
// leader whose caller disconnects mid-write therefore no longer fails
// the callers that are still waiting — they asked for the same write,
// their own contexts are alive, and the wire call is already in flight.
//
// Each caller still returns as soon as its own context is done, with
// that context's error.
func (c *Coalescer) Do(ctx context.Context, key string, fn func(ctx context.Context) (any, error)) (any, error) {
	c.mu.Lock()
	c.total++
	// An in-flight call with no participants left has been abandoned (its
	// context is cancelled), so joining it would inherit a result nobody
	// wanted. Start a fresh one instead; the abandoned call's own cleanup
	// leaves this entry alone.
	if call, ok := c.calls[key]; ok && call.active > 0 {
		c.coalesced++
		call.followers++
		call.active++
		waiters := call.followers
		hook := c.hook
		c.mu.Unlock()
		if hook != nil {
			hook(key, waiters)
		}
		return c.await(ctx, call)
	}
	call := &coalescedCall{done: make(chan struct{}), active: 1}
	callCtx := c.sharedContext(ctx, call)
	c.calls[key] = call
	c.executed++
	c.mu.Unlock()

	go c.run(callCtx, key, call, fn)
	return c.await(ctx, call)
}

// sharedContext derives the context the shared call runs under from the
// context of the caller that starts it: values and deadline are kept —
// the deadline is the only bound known when the call starts — while
// cancellation is dropped, because the group outlives that one caller.
// The returned cancel is stored on call.
func (c *Coalescer) sharedContext(ctx context.Context, call *coalescedCall) context.Context {
	base := context.WithoutCancel(ctx)
	var (
		callCtx context.Context
		cancel  context.CancelFunc
	)
	if deadline, ok := ctx.Deadline(); ok {
		callCtx, cancel = context.WithDeadline(base, deadline)
	} else {
		callCtx, cancel = context.WithCancel(base)
	}
	call.cancel = cancel
	return callCtx
}

// run executes the shared call and publishes its outcome to everyone
// waiting on it.
//
// The map entry is dropped BEFORE the call settles, not after: settle
// closes call.done and every parked [Coalescer.await] returns the moment
// that happens, so a caller can observe its own Do() returning while the
// map still (briefly) carries the now-finished entry if the purge ran
// second. That window makes [Coalescer.InFlight] and [Coalescer.Stats]
// report a call in flight when none is, and lets a brand-new caller for
// the same key join the already-settled call instead of starting a fresh
// one. Purging first closes the window by construction: the entry is gone
// before anyone can be woken to look for it.
func (c *Coalescer) run(ctx context.Context, key string, call *coalescedCall, fn func(ctx context.Context) (any, error)) {
	defer call.cancel()
	v, err := invokeCoalesced(ctx, fn)

	c.mu.Lock()
	// Only drop the entry while it is still this call: [Coalescer.Clear]
	// may have removed it and a later caller registered a new one.
	if cur, ok := c.calls[key]; ok && cur == call {
		delete(c.calls, key)
	}
	if err != nil {
		c.failed++
	}
	c.mu.Unlock()

	call.settle(v, err)
}

// invokeCoalesced runs fn and turns a panicking call into an error. The
// shared call runs on its own goroutine, where an escaping panic would
// take the daemon down instead of unwinding into the caller that could
// have handled it.
func invokeCoalesced(ctx context.Context, fn func(ctx context.Context) (any, error)) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("reliability.coalesced_call_panic", slog.String("panic", fmt.Sprintf("%v", r)))
			v, err = nil, fmt.Errorf("%w: %v", ErrCoalescedCallPanicked, r)
		}
	}()
	return fn(ctx)
}

// await blocks until the shared call settles or the caller's own context
// is done, and drops the caller from the group either way.
func (c *Coalescer) await(ctx context.Context, call *coalescedCall) (any, error) {
	select {
	case <-call.done:
		c.leave(call)
		return call.result()
	case <-ctx.Done():
		c.leave(call)
		return nil, ctx.Err()
	}
}

// leave drops one participant and cancels the shared call once the last
// one is gone: with nobody waiting for the result, keeping the wire busy
// buys nothing.
func (c *Coalescer) leave(call *coalescedCall) {
	c.mu.Lock()
	call.active--
	last := call.active <= 0
	c.mu.Unlock()
	if last {
		call.cancel()
	}
}

// Clear abandons every in-flight coalesced call: the shared calls are
// cancelled, removed from the map, and every waiter is released with
// [ErrCoalescerCleared]. The error is the point — a waiter released
// without a result has not had its call carried out, and reporting that
// as a successful empty result would turn an abandoned write into a
// silent success.
//
// It runs during [InterfaceClient.Close] so no caller stays parked on a
// call whose transport is gone.
func (c *Coalescer) Clear() {
	c.mu.Lock()
	abandoned := make([]*coalescedCall, 0, len(c.calls))
	for key, call := range c.calls {
		abandoned = append(abandoned, call)
		delete(c.calls, key)
	}
	c.mu.Unlock()
	for _, call := range abandoned {
		call.settle(nil, ErrCoalescerCleared)
		if call.cancel != nil {
			call.cancel()
		}
	}
}

// InFlight reports how many distinct keys are currently coalescing.
// Intended for metrics and tests.
func (c *Coalescer) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// Stats snapshots the coalescer counters.
func (c *Coalescer) Stats() CoalescerStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CoalescerStats{
		Total:     c.total,
		Executed:  c.executed,
		Coalesced: c.coalesced,
		Failed:    c.failed,
		InFlight:  len(c.calls),
	}
}
