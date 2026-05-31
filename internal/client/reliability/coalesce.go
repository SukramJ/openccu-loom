// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"fmt"
	"sync"
)

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
	done    chan struct{}
	mu      sync.Mutex // guards val and err; allows followers to read safely after Clear
	val     any
	err     error
	waiters int
	// cleared is set to true by [Coalescer.Clear] to signal the leader
	// goroutine that the channel has already been closed and must not be
	// closed again.
	cleared bool
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
	key := method
	for _, a := range args {
		key += ":" + fmt.Sprint(a)
	}
	return key
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
func (c *Coalescer) Do(ctx context.Context, key string, fn func(ctx context.Context) (any, error)) (any, error) {
	c.mu.Lock()
	c.total++
	if call, ok := c.calls[key]; ok {
		c.coalesced++
		call.waiters++
		waiters := call.waiters
		hook := c.hook
		c.mu.Unlock()
		if hook != nil {
			hook(key, waiters)
		}
		select {
		case <-call.done:
			call.mu.Lock()
			v, e := call.val, call.err
			call.mu.Unlock()
			return v, e
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &coalescedCall{done: make(chan struct{})}
	c.calls[key] = call
	c.executed++
	c.mu.Unlock()

	v, e := fn(ctx)

	call.mu.Lock()
	call.val, call.err = v, e
	call.mu.Unlock()

	c.mu.Lock()
	if !call.cleared {
		close(call.done)
		delete(c.calls, key)
	}
	if e != nil {
		c.failed++
	}
	c.mu.Unlock()

	return v, e
}

// Clear cancels every in-flight coalesced call by closing their done
// channels and removes them from the internal map. All waiters (both
// followers and any goroutine waiting in [Do]) will unblock and
// receive a nil result with a nil error — callers should treat a nil
// return from Do after Clear as an indication that the coalescer was
// shut down, not a success.
//
// Typical use: call Clear during InterfaceClient.Close so pending
// coalescer calls don't hang indefinitely after the transport is gone.
// (client/request_coalescer.py:119-130, C14).
func (c *Coalescer) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, call := range c.calls {
		call.cleared = true
		close(call.done)
		delete(c.calls, key)
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
