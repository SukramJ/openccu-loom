// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package events implements the daemon's in-process typed event bus.
//
// Handlers are registered per concrete event type — the generic
// [Subscribe] function gives compile-time type safety with no runtime
// reflection. Publishing is synchronous by default; handlers run on the
// publisher's goroutine in priority order (higher priority first, FIFO
// within the same priority).
//
// The bus refuses re-entrant [Publish] calls from within a handler:
// events emitted from a handler are buffered and flushed after the
// current dispatch completes. That guarantees handler execution order
// stays causal and prevents infinite recursion loops through cross-
// wired subscribers.
package events

import (
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Priority orders handlers for the same event type. Higher values run first.
type Priority int

// Priority values.
const (
	PriorityLow      Priority = -10
	PriorityNormal   Priority = 0
	PriorityHigh     Priority = 10
	PriorityCritical Priority = 20 // Closes
)

// DeferredHighWaterAlert is the threshold at which the bus starts
// emitting slog.Error records about a growing deferred buffer. The
// buffer itself is unbounded — the deferred path is shared between
// true handler re-entrancy and normal concurrent fan-out across
// goroutines, so a hard cap would drop legitimate work. The alert
// surfaces pathological handler recursion (a handler that re-publishes
// faster than the bus can drain) before the daemon runs out of memory.
//
// Operators should alert on DeferredHighWater() exceeding this value.
const DeferredHighWaterAlert = 4096

// HandlerOption configures a subscription.
type HandlerOption func(*handlerOptions)

// WithPriority sets the priority of the registered handler.
func WithPriority(p Priority) HandlerOption {
	return func(o *handlerOptions) { o.priority = p }
}

// WithKey installs a per-handler filter so the handler only fires when the
// event's [hmevent.Event.Key] matches `key`. Pass an empty string to disable
// filtering (default).
func WithKey(key string) HandlerOption {
	return func(o *handlerOptions) { o.key = key }
}

// WithName attaches a stable, human-readable name to the handler that
// shows up in [Bus.HandlerStats]. Useful for diagnostics. When unset
// the bus falls back to a generated id.
//
// loom:reachable:reason="called by north-bound adapters and diagnostic tooling to annotate subscriptions"
func WithName(name string) HandlerOption {
	return func(o *handlerOptions) { o.name = name }
}

// WithExternal marks the handler as an "external" subscription — a
// subscription registered by a north-bound adapter (REST, MQTT, UI)
// that may need to be cleared as a group when the adapter shuts down
// or reloads. Internal coordinators (recovery, health, events) should
// NOT use this option.
//
// loom:reachable:reason="called by MQTT and REST adapters to tag external subscriptions for grouped teardown"
func WithExternal() HandlerOption {
	return func(o *handlerOptions) { o.external = true }
}

type handlerOptions struct {
	priority Priority
	key      string
	name     string
	external bool
}

// Bus dispatches typed events to registered handlers. The zero value is
// not ready for use — construct with [NewBus].
type Bus struct {
	mu        sync.Mutex
	handlers  map[hmevent.EventType][]*registered
	nextID    atomic.Uint64
	dispatch  sync.Mutex // held only while Publish is actually running
	deferred  []func()   // re-entrant publishes land here until dispatch unwinds
	activeTyp hmevent.EventType

	// dispatchGID is the goroutine id of the goroutine that currently owns
	// b.dispatch (0 when no dispatch is in progress). The unsubscribe barrier
	// reads it to detect a handler unsubscribing itself on the dispatch
	// goroutine, where waiting on the in-flight count would deadlock on the
	// current invocation.
	dispatchGID atomic.Int64

	// statsMu guards published — incremented on every dispatchNow call
	// regardless of subscriber count. Snapshot via [EventStats].
	statsMu   sync.RWMutex
	published map[hmevent.EventType]int

	// deferredHighWater tracks the largest len(deferred) ever observed
	// inside Publish. Surfaces pathological handler recursion as a
	// monotonic high-water gauge — operators alert on it crossing
	// [DeferredHighWaterAlert].
	deferredHighWater atomic.Uint64
	// alertedHighWater is set the first time we logged a slog.Error
	// for crossing the alert threshold; suppresses log spam thereafter.
	alertedHighWater atomic.Bool
}

type registered struct {
	id       uint64
	priority Priority
	key      string // empty == match all
	name     string
	external bool // set via WithExternal()
	fn       func(any)

	// dead is set the moment the unsubscribe closure removes this handler
	// from the registry. A dispatch that already snapshotted the handler
	// slice re-checks the flag immediately before invoking, so a handler
	// unsubscribed mid-pass never fires. Pairs with inflight to give the
	// unsubscribe closure barrier semantics.
	dead atomic.Bool
	// inflight counts the dispatch passes that captured this handler in
	// their snapshot but have not yet finished with it. The unsubscribe
	// closure waits on it, so a caller can free resources the handler
	// touches once unsubscribe returns without racing an in-flight
	// invocation.
	inflight sync.WaitGroup

	calls   atomic.Uint64
	matches atomic.Uint64

	// Duration and error tracking — mirrors
	// total_duration_ms is accumulated in microseconds (atomic int64, divided
	// by 1000 on read) to avoid float64 atomics.
	totalDurationUs atomic.Int64 // microseconds; divide by 1000.0 for ms
	totalErrors     atomic.Uint64
}

// NewBus returns a ready-to-use [Bus].
func NewBus() *Bus {
	return &Bus{
		handlers:  make(map[hmevent.EventType][]*registered),
		published: make(map[hmevent.EventType]int),
	}
}

// Subscribe registers a handler for events of type T and returns an
// unsubscribe closure. Calling the closure is idempotent.
//
// Use [WithKey] to install an event-key filter so the handler fires
// only when the event's [hmevent.Event.Key] matches the configured
// key.
func Subscribe[T hmevent.Event](b *Bus, fn func(T), opts ...HandlerOption) func() {
	options := handlerOptions{priority: PriorityNormal}
	for _, o := range opts {
		o(&options)
	}

	var zero T
	typ := zero.Type()

	id := b.nextID.Add(1)
	wrapper := func(e any) {
		ev, ok := e.(T)
		if !ok {
			return
		}
		fn(ev)
	}

	entry := &registered{
		id:       id,
		priority: options.priority,
		key:      options.key,
		name:     options.name,
		external: options.external,
		fn:       wrapper,
	}

	b.mu.Lock()
	b.handlers[typ] = insertSorted(b.handlers[typ], entry)
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			list := b.handlers[typ]
			for i, h := range list {
				if h == entry {
					b.handlers[typ] = append(list[:i], list[i+1:]...)
					break
				}
			}
			// Flip dead under b.mu so a dispatch pass that snapshots after
			// this point either misses the handler entirely or observes dead
			// and skips it (see dispatchNow). Ordered before the barrier Wait.
			entry.dead.Store(true)
			b.mu.Unlock()

			// Barrier: block until any dispatch pass that already captured
			// this handler in its snapshot has finished with it. After this
			// returns the handler is guaranteed not to be executing, so the
			// caller may safely free resources the handler references without
			// racing a still-running invocation.
			//
			// Exception: when we are the dispatch goroutine (a handler
			// unsubscribing itself), waiting on the in-flight count would
			// deadlock on the current invocation — which is our own caller on
			// this stack, so there is nothing to race against anyway. The dead
			// flag set above already stops the handler from firing on any later
			// pass. Only cross-goroutine unsubscribers block.
			if goid() != b.dispatchGID.Load() {
				entry.inflight.Wait()
			}
		})
	}
}

// Publish dispatches e to every subscribed handler. Re-entrant publishes
// (calls made from within a handler) are buffered and run once the
// outer dispatch completes.
func Publish[T hmevent.Event](b *Bus, e T) {
	if b.dispatch.TryLock() {
		// Record which goroutine owns the dispatch so a self-unsubscribing
		// handler can skip the barrier wait (see the unsubscribe closure).
		b.dispatchGID.Store(goid())
		// flushDeferred releases b.dispatch (under b.mu) once the queue
		// drains — see the handoff note there. Do NOT defer-unlock here.
		b.dispatchNow(e)
		b.flushDeferred()
		return
	}
	// TryLock failed — either we are inside a handler frame on this
	// goroutine (true re-entrancy) or another goroutine is currently
	// dispatching. Both share the same deferred queue, which is
	// drained iteratively in [flushDeferred].
	captured := e
	b.mu.Lock()
	b.deferred = append(b.deferred, func() { b.dispatchNow(captured) })
	depth := uint64(len(b.deferred))
	// Close the dispatcher-handoff race: the current dispatcher releases
	// b.dispatch only while holding b.mu (in flushDeferred). Attempting the
	// take-over under the same b.mu makes release and re-acquisition mutually
	// exclusive — so our just-enqueued event is either still visible to the
	// outgoing dispatcher's empty-check (it drains it) or the lock is now free
	// for us to take and drain it ourselves. Without this an event enqueued in
	// the window between the dispatcher's last empty-check and its lock release
	// would sit undrained until some future Publish (a lost event → e.g. a
	// HandlerStat match counted as 999/1000).
	tookOver := b.dispatch.TryLock()
	b.mu.Unlock()
	b.observeDeferredDepth(depth, e.Type())
	if tookOver {
		b.dispatchGID.Store(goid())
		b.flushDeferred()
	}
}

// observeDeferredDepth updates the high-water gauge and emits one
// slog.Error per bus when the alert threshold is crossed. The alert
// fires once and self-suppresses thereafter so a pathological handler
// graph does not drown the log stream — the gauge stays available for
// metrics scraping.
func (b *Bus) observeDeferredDepth(depth uint64, typ hmevent.EventType) {
	for {
		hw := b.deferredHighWater.Load()
		if depth <= hw {
			break
		}
		if b.deferredHighWater.CompareAndSwap(hw, depth) {
			break
		}
	}
	if depth >= DeferredHighWaterAlert && b.alertedHighWater.CompareAndSwap(false, true) {
		slog.Error(
			"event bus deferred buffer growing past alert threshold — possible handler recursion",
			"event_type", string(typ),
			"depth", depth,
			"alert_at", DeferredHighWaterAlert,
		)
	}
}

// DeferredHighWater returns the largest deferred-buffer length ever
// observed since bus construction. Monotonic — never decreases. Use it
// to alert on pathological handler recursion (a handler that publishes
// faster than dispatchNow drains).
func (b *Bus) DeferredHighWater() uint64 {
	return b.deferredHighWater.Load()
}

// DeferredDepth returns the current deferred-buffer length. Useful as
// a real-time gauge in admin dashboards.
func (b *Bus) DeferredDepth() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.deferred)
}

// PublishSync is an explicit alias of [Publish]. It exists purely for
// API parity with the reference stack's separate publish_sync entry
// point and carries no stronger delivery guarantee than [Publish].
//
// loom:reachable:reason="API-parity alias retained for callers that mirror the reference publish_sync entry point"
//
// It is NOT guaranteed to be synchronous: in the uncontended case
// [Publish] dispatches every handler on the caller's goroutine before
// returning, but when another goroutine already holds the dispatch lock
// (or the caller is inside a handler), the event is buffered and drained
// by the active dispatcher — so handlers may run after this call returns.
// Do not rely on a read-after-publish caller observing the handler's
// side effects; if you need that, dispatch on the same goroutine that
// will read the result. There is no production caller that depends on a
// synchronous-drain contract here.
func PublishSync[T hmevent.Event](b *Bus, e T) {
	Publish(b, e)
}

// dispatchNow runs every handler for e in priority-ordered turn.
// Handlers with a [WithKey] filter are skipped when the event's
// [hmevent.Event.Key] does not match.
func (b *Bus) dispatchNow(e hmevent.Event) {
	b.mu.Lock()
	b.activeTyp = e.Type()
	handlers := append([]*registered(nil), b.handlers[e.Type()]...)
	// Register in-flight interest under b.mu — the same lock the
	// unsubscribe closure takes to detach a handler and flip its dead
	// flag. Serialising "snapshot + mark in-flight" against "detach + mark
	// dead" is what makes the unsubscribe barrier correct: an unsubscribe
	// that wins the lock removes the handler before this snapshot (so it is
	// never invoked below); one that loses observes a pending inflight
	// count and blocks on it until the matching Done runs.
	for _, h := range handlers {
		h.inflight.Add(1)
	}
	b.mu.Unlock()

	// Bump the publish counter even when there are no subscribers
	// The counter mirrors
	// every published event, not every delivery.
	b.statsMu.Lock()
	b.published[e.Type()]++
	b.statsMu.Unlock()

	key := e.EventKey()
	for _, h := range handlers {
		h.calls.Add(1)
		// Skip a handler detached after the snapshot: dead is flipped under
		// b.mu by the unsubscribe closure, so a handler removed mid-pass
		// never fires. Also honour the per-handler key filter.
		if h.dead.Load() || (h.key != "" && h.key != key) {
			h.inflight.Done()
			continue
		}
		h.matches.Add(1)
		b.callHandler(h, e)
		h.inflight.Done()
	}

	b.mu.Lock()
	b.activeTyp = ""
	b.mu.Unlock()
}

// callHandler invokes a single registered handler with panic recovery and
// duration tracking. A panicking handler is isolated: the panic is caught,
// logged via slog, counted in h.totalErrors, and dispatch continues with the
// next handler.
func (b *Bus) callHandler(h *registered, e hmevent.Event) {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		h.totalDurationUs.Add(elapsed.Microseconds())

		if r := recover(); r != nil {
			h.totalErrors.Add(1)
			name := h.name
			if name == "" {
				name = formatHandlerID(h.id)
			}
			slog.Error(
				"event handler panicked",
				"handler", name,
				"handler_id", h.id,
				"event_type", string(e.Type()),
				"panic", fmt.Sprintf("%v", r),
				// Without the stack a recovered handler panic is
				// undiagnosable in the field — the message alone cannot
				// point at the offending subscriber. Capture it so the
				// log carries the exact call site.
				"stack", string(debug.Stack()),
			)
		}
	}()
	h.fn(e)
}

// flushDeferred runs buffered publishes until the queue is empty, then
// releases b.dispatch. Handlers that publish further re-entrant events cause
// the queue to grow — we drain it iteratively rather than recursively.
//
// The caller MUST hold b.dispatch (acquired via the TryLock in [Publish]).
// The release happens here, while b.mu is held and the queue is observed
// empty, so it is serialised against the slow-path take-over TryLock in
// [Publish] (also under b.mu). That mutual exclusion is what closes the
// handoff race: an event enqueued concurrently is either seen by the
// empty-check below (drained in the next iteration) or lands after the
// release, where the enqueuer's own TryLock succeeds and drains it.
func (b *Bus) flushDeferred() {
	for {
		b.mu.Lock()
		if len(b.deferred) == 0 {
			// Clear the dispatch-owner id before releasing the lock so a later
			// unsubscribe on this goroutine (now no longer dispatching) does not
			// mistake itself for a self-unsubscribe and skip the barrier.
			b.dispatchGID.Store(0)
			b.dispatch.Unlock()
			b.mu.Unlock()
			return
		}
		next := b.deferred[0]
		b.deferred = b.deferred[1:]
		b.mu.Unlock()
		next()
	}
}

// HandlerCount reports how many handlers are registered for an event
// type. Intended for metrics and tests.
func (b *Bus) HandlerCount(typ hmevent.EventType) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[typ])
}

// EventStats returns the per-event-type publish counter as a stable snapshot
// keyed by the [hmevent.EventType] string. The map is a copy — the caller may
// freely mutate it without affecting the bus.
//
// Counters survive handler unsubscription: a type that had ten publishes
// still reports ten even after every subscriber detached.
func (b *Bus) EventStats() map[string]int {
	b.statsMu.RLock()
	defer b.statsMu.RUnlock()
	out := make(map[string]int, len(b.published))
	for t, n := range b.published {
		out[string(t)] = n
	}
	return out
}

// TotalSubscriptionCount sums the active subscriptions across every event
// type.
func (b *Bus) TotalSubscriptionCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, list := range b.handlers {
		total += len(list)
	}
	return total
}

// ClearSubscriptions removes all handlers registered for the given event
// type. If no handlers were registered it is a no-op (idempotent). The
// publish counters in [EventStats] are NOT reset — they survive a clear,
func (b *Bus) ClearSubscriptions(typ hmevent.EventType) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, typ)
}

// ClearAllSubscriptions removes every handler across all event types.
// EventStats counters survive — only the subscriptions are cleared.
func (b *Bus) ClearAllSubscriptions() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = make(map[hmevent.EventType][]*registered)
}

// ClearExternalSubscriptions removes all handlers that were registered
// with [WithExternal] for the given event types. When no types are
// provided every external handler across all event types is removed.
// Returns the count of removed handlers. Internal subscriptions (those
// without [WithExternal]) are never touched.
func (b *Bus) ClearExternalSubscriptions(types ...hmevent.EventType) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	removed := 0
	filter := func(list []*registered) ([]*registered, int) {
		kept := list[:0]
		n := 0
		for _, h := range list {
			if h.external {
				n++
			} else {
				kept = append(kept, h)
			}
		}
		return kept, n
	}
	if len(types) == 0 {
		for typ, list := range b.handlers {
			kept, n := filter(list)
			removed += n
			if len(kept) == 0 {
				delete(b.handlers, typ)
			} else {
				b.handlers[typ] = kept
			}
		}
		return removed
	}
	for _, typ := range types {
		list, ok := b.handlers[typ]
		if !ok {
			continue
		}
		kept, n := filter(list)
		removed += n
		if len(kept) == 0 {
			delete(b.handlers, typ)
		} else {
			b.handlers[typ] = kept
		}
	}
	return removed
}

// ClearSubscriptionsByKey removes all handlers whose key exactly matches the
// provided key, across every event type. Idempotent when no handler carries
// that key.
func (b *Bus) ClearSubscriptionsByKey(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for typ, list := range b.handlers {
		filtered := list[:0]
		for _, h := range list {
			if h.key != key {
				filtered = append(filtered, h)
			}
		}
		if len(filtered) == 0 {
			delete(b.handlers, typ)
		} else {
			b.handlers[typ] = filtered
		}
	}
}

// ResetEventStats clears all per-event-type publish counters.
func (b *Bus) ResetEventStats() {
	b.statsMu.Lock()
	defer b.statsMu.Unlock()
	b.published = make(map[hmevent.EventType]int)
}

// HandlerStat is a snapshot of one registered handler's counters.
type HandlerStat struct {
	EventType       hmevent.EventType
	Name            string
	Priority        Priority
	Key             string
	Calls           uint64  // events delivered to dispatch (including key-filtered skips)
	Matches         uint64  // events that actually invoked the handler
	TotalDurationMs float64 // cumulative handler execution time in milliseconds
	TotalErrors     uint64  // recovered panics + (future) returned errors
}

// HandlerStats returns a snapshot of the per-handler counters. The
// list order is implementation-defined.
func (b *Bus) HandlerStats() []HandlerStat {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []HandlerStat
	for typ, list := range b.handlers {
		for _, h := range list {
			name := h.name
			if name == "" {
				name = formatHandlerID(h.id)
			}
			out = append(out, HandlerStat{
				EventType:       typ,
				Name:            name,
				Priority:        h.priority,
				Key:             h.key,
				Calls:           h.calls.Load(),
				Matches:         h.matches.Load(),
				TotalDurationMs: float64(h.totalDurationUs.Load()) / 1000.0,
				TotalErrors:     h.totalErrors.Load(),
			})
		}
	}
	return out
}

// LeakedSubscriptions returns the names of every still-registered handler.
// Used during shutdown diagnostics — handlers that should have been
// unsubscribed surface here.
func (b *Bus) LeakedSubscriptions() []string {
	stats := b.HandlerStats()
	if len(stats) == 0 {
		return nil
	}
	out := make([]string, 0, len(stats))
	for _, s := range stats {
		out = append(out, string(s.EventType)+":"+s.Name)
	}
	return out
}

// formatHandlerID renders the auto-generated handler id as a short string.
func formatHandlerID(id uint64) string {
	return "h" + strconv.FormatUint(id, 10)
}

// goid returns the current goroutine's numeric id by parsing the runtime
// stack header ("goroutine <id> [status]:"). It is used only to detect a
// handler unsubscribing itself on the dispatch goroutine, so the unsubscribe
// barrier can skip waiting on the current invocation instead of deadlocking.
// The cost is paid once per dispatch-lock acquisition and on the cold
// unsubscribe path, never per handler call.
func goid() int64 {
	var buf [32]byte
	n := runtime.Stack(buf[:], false)
	const prefix = "goroutine "
	s := buf[:n]
	if len(s) < len(prefix) {
		return 0
	}
	s = s[len(prefix):]
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	id, _ := strconv.ParseInt(string(s[:i]), 10, 64)
	return id
}

func insertSorted(list []*registered, r *registered) []*registered {
	// Higher priority first. Equal priorities preserve registration
	// order, which is FIFO because the new entry goes at the end of the
	// equal-priority run.
	idx := sort.Search(len(list), func(i int) bool { return list[i].priority < r.priority })
	out := make([]*registered, 0, len(list)+1)
	out = append(out, list[:idx]...)
	out = append(out, r)
	out = append(out, list[idx:]...)
	return out
}
