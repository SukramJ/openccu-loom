// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CollectorBackend is the wire-level surface a
// [CallParameterCollector] dispatches to. The contract is the value-
// and-paramset operation pair (`set_value` + `put_paramset`).
type CollectorBackend interface {
	// SetValue writes a single parameter.
	SetValue(
		ctx context.Context,
		channelAddress string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error

	// PutParamset writes a batch of parameters atomically. The CCU
	// applies the entire map together; partial application is not
	// possible. Used by the collector when more than one parameter
	// targets the same (channel, paramset, order) tuple.
	PutParamset(
		ctx context.Context,
		channelAddress string,
		paramsetKey hmenum.ParamsetKey,
		values map[string]any,
		priority hmenum.CommandPriority,
	) error
}

// CollectableDataPoint is the contract a sub data point must satisfy
// to participate in collector batching. Every `*DataPoint[T]`
// satisfies it through the public methods below.
type CollectableDataPoint interface {
	DataPointKey() hmtypes.DataPointKey
	// ApplyOptimistic stages a value on the tracker and returns the
	// rollback closure used when the wire call fails. Implementations
	// may return nil to opt out (e.g. OptimisticDisabled, type
	// coercion failure). A burst-skip — where the tracker already
	// holds the same value — returns a non-nil but no-op closure so
	// the caller's bookkeeping stays homogenous.
	ApplyOptimistic(value any) func()
}

// WaitableDataPoint is an optional capability extension to
// [CollectableDataPoint] that lets the collector block on CCU
// confirmation for the first N staged data points after Send.
// Implemented by `*DataPoint[T]` via its own [WaitForConfirmation].
type WaitableDataPoint interface {
	WaitForConfirmation(ctx context.Context) error
}

// CallParameterCollector batches multiple sub-parameter sets that target one
// logical CCU operation (e.g. switching a thermostat into boost mode
// atomically updates SET_POINT_TEMPERATURE + CONTROL_MODE + BOOST_MODE). The
// collector groups by (paramsetKey, order, channelAddress) and emits a single
// PutParamset per group whenever more than one parameter is involved — saving
// round-trip latency and giving the CCU a chance to apply the change
// atomically.
//
// - Add(dp, value, order) buffers the sub-set; `order` controls the dispatch
// sequence within a paramsetKey (lower first). - Send(ctx) applies optimistic
// state to every collected DP, then dispatches the groups in stable order. On
// wire error the rollback closures of every staged DP are fired so the
// user-visible state stays truthful.
//
// A collector is single-shot: once Send returns it is consumed and must not
// be reused (mirrors python's per-collector lifecycle).
type CallParameterCollector struct {
	mu              sync.Mutex
	backend         CollectorBackend
	priority        hmenum.CommandPriority
	items           []collectedItem
	consumed        bool
	waitForCallback int
}

type collectedItem struct {
	dp    CollectableDataPoint
	value any
	// paramsetKey, channelAddress, parameter pre-computed from
	// dp.DataPointKey() so Add holds the dispatch shape.
	paramsetKey    hmenum.ParamsetKey
	channelAddress string
	parameter      string
	order          int
}

// WriterAsBackend wraps a [Writer] as a [CollectorBackend]. When w
// also satisfies [ParamsetWriter] the returned backend forwards
// PutParamset calls to it; otherwise PutParamset falls back to
// sequential SetValue calls (same behaviour as [custom.PutOrSet]).
//
// Use this when a custom data-point method wants to seed a
// [CallParameterCollector] from its own Writer without importing the
// device or channel packages. The resulting backend is safe to pass to
// [NewCollector].
func WriterAsBackend(w Writer) CollectorBackend {
	return &writerBackend{w: w}
}

type writerBackend struct{ w Writer }

func (b *writerBackend) SetValue(
	ctx context.Context,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	return b.w.SetValue(ctx, channelAddress, parameter, value, priority)
}

func (b *writerBackend) PutParamset(
	ctx context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	priority hmenum.CommandPriority,
) error {
	if pw, ok := b.w.(ParamsetWriter); ok {
		return pw.PutParamset(ctx, channelAddress, paramsetKey, values, priority)
	}
	// Fallback: sequential SetValue in deterministic (sorted) order.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := b.w.SetValue(ctx, channelAddress, hmenum.Parameter(k), values[k], priority); err != nil {
			return err
		}
	}
	return nil
}

// CollectorOption configures a [CallParameterCollector].
type CollectorOption func(*CallParameterCollector)

// WithPriority overrides the default [CommandPriority] used for
// every dispatch issued by the collector.
func WithPriority(p hmenum.CommandPriority) CollectorOption {
	return func(c *CallParameterCollector) { c.priority = p }
}

// WithWaitForCallback configures the collector to block on CCU
// confirmation for the first n staged data points after Send returns
// successfully. Each waited DP must implement [WaitableDataPoint]
// (`*DataPoint[T]` does, via its own WaitForConfirmation). Items
// past the first n stay fire-and-forget so the caller can mix
// high-confidence Lock / Siren writes with lower-confidence Switch /
// Cover writes in one batch. n<=0 disables waiting (default).
func WithWaitForCallback(n int) CollectorOption {
	return func(c *CallParameterCollector) {
		if n > 0 {
			c.waitForCallback = n
		}
	}
}

// NewCollector constructs a fresh [CallParameterCollector] bound to
// backend. Without options the priority defaults to
// [hmenum.CommandPriorityHigh].
func NewCollector(backend CollectorBackend, opts ...CollectorOption) *CallParameterCollector {
	c := &CallParameterCollector{
		backend:  backend,
		priority: hmenum.CommandPriorityHigh,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ErrCollectorConsumed is returned when Add or Send is called on a
// collector whose Send has already completed.
var ErrCollectorConsumed = errors.New("generic: CallParameterCollector already consumed")

// ErrNoBackend is returned by Send when the collector was wired
// without a [CollectorBackend].
var ErrNoBackend = errors.New("generic: CallParameterCollector has no backend")

// Add buffers a sub-DP set. order steers dispatch sequence within a
// paramsetKey — lower orders fire first. Items added with the same
// order on the same channel collapse into one PutParamset.
//
// Repeated Add calls for the same (channel, paramset, parameter)
// overwrite the prior value; the collector remembers only the last
// staged value per parameter.
func (c *CallParameterCollector) Add(dp CollectableDataPoint, value any, order int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.consumed {
		return ErrCollectorConsumed
	}
	key := dp.DataPointKey()
	// Replace existing entry with the same target.
	for i := range c.items {
		it := &c.items[i]
		if it.paramsetKey == key.ParamsetKey &&
			it.channelAddress == key.ChannelAddress &&
			it.parameter == key.Parameter &&
			it.order == order {
			it.dp = dp
			it.value = value
			return nil
		}
	}
	c.items = append(c.items, collectedItem{
		dp:             dp,
		value:          value,
		paramsetKey:    key.ParamsetKey,
		channelAddress: key.ChannelAddress,
		parameter:      key.Parameter,
		order:          order,
	})
	return nil
}

// AddParam buffers a bare (channel, paramset, parameter) write that has
// no data point of its own.
//
// The auto-off and ramp parameters are the reason it exists: ON_TIME,
// RAMP_TIME and their unit companions are write-only side parameters of
// a switch or dimmer, so they carry no state and no data point — yet
// they have to travel in the SAME wire call as the value they qualify.
// Writing them past the collector is what split a bounded switch-on
// into two radio transmissions, each spending duty-cycle budget the
// following stop command needs.
//
// Because such a parameter has no observable state, no optimistic value
// is applied and nothing is rolled back for it on failure.
func (c *CallParameterCollector) AddParam(
	channelAddress string, paramsetKey hmenum.ParamsetKey, parameter string, value any, order int,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.consumed {
		return ErrCollectorConsumed
	}
	for i := range c.items {
		it := &c.items[i]
		if it.paramsetKey == paramsetKey && it.channelAddress == channelAddress &&
			it.parameter == parameter && it.order == order {
			it.value = value
			return nil
		}
	}
	c.items = append(c.items, collectedItem{
		value:          value,
		paramsetKey:    paramsetKey,
		channelAddress: channelAddress,
		parameter:      parameter,
		order:          order,
	})
	return nil
}

// Len reports how many sub-sets are currently buffered.
func (c *CallParameterCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Cancel discards any buffered items and marks the collector
// consumed. Subsequent Add or Send calls return
// [ErrCollectorConsumed].
func (c *CallParameterCollector) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = nil
	c.consumed = true
}

// Send dispatches the buffered items grouped by (paramsetKey,
// order, channelAddress). For groups of size 1 the call is reduced
// to SetValue; multi-parameter groups go through PutParamset. The
// collector applies optimistic state on every DP before the first
// wire call so observers see the user's intent immediately. On the
// first wire error all rollback closures fire and the error is
// returned — partial sends are left as observed by the wire (the
// CCU's state is authoritative once a parameter has been
// transferred).
//
// Returning the wire error to the caller mirrors
// behaviour: the higher-level command-throttle/retry layer decides
// whether to re-issue the collector.
func (c *CallParameterCollector) Send(ctx context.Context) error { //nolint:funlen // single-purpose collector dispatch with many type/error branches
	c.mu.Lock()
	if c.consumed {
		c.mu.Unlock()
		return ErrCollectorConsumed
	}
	if c.backend == nil {
		c.mu.Unlock()
		return ErrNoBackend
	}
	items := append([]collectedItem(nil), c.items...)
	priority := c.priority
	waitN := c.waitForCallback
	c.consumed = true
	c.mu.Unlock()

	if len(items) == 0 {
		return nil
	}

	// Stage optimistic state for every staged DP. nil-rollbacks are
	// dropped (e.g. OptimisticDisabled DPs); we still issue the
	// wire call afterwards.
	rollbacks := make([]func(), 0, len(items))
	for _, it := range items {
		if it.dp == nil {
			// A bare parameter (AddParam) has no observable state to
			// apply or roll back.
			continue
		}
		if rb := it.dp.ApplyOptimistic(it.value); rb != nil {
			rollbacks = append(rollbacks, rb)
		}
	}
	rollbackAll := func() {
		for _, rb := range rollbacks {
			rb()
		}
	}

	// Build the dispatch tree: paramsetKey → order → channelAddress
	// → param → value. Each leaf becomes either a SetValue (single
	// param) or a PutParamset (multi).
	type byChannel = map[string]map[string]any
	type byOrder = map[int]byChannel
	tree := make(map[hmenum.ParamsetKey]byOrder, 2)
	for _, it := range items {
		orders, ok := tree[it.paramsetKey]
		if !ok {
			orders = make(byOrder, 1)
			tree[it.paramsetKey] = orders
		}
		channels, ok := orders[it.order]
		if !ok {
			channels = make(byChannel, 1)
			orders[it.order] = channels
		}
		params, ok := channels[it.channelAddress]
		if !ok {
			params = make(map[string]any, 1)
			channels[it.channelAddress] = params
		}
		params[it.parameter] = it.value
	}

	// Iterate in deterministic order: paramsetKey alphabetically
	// (stable across runs), then ascending order, then channel
	// Addresses sorted. This matches
	// `for _, paramset_no in sorted(paramsets.items())`.
	psKeys := make([]string, 0, len(tree))
	for k := range tree {
		psKeys = append(psKeys, string(k))
	}
	sort.Strings(psKeys)

	for _, ps := range psKeys {
		paramsetKey := hmenum.ParamsetKey(ps)
		orders := tree[paramsetKey]
		orderKeys := make([]int, 0, len(orders))
		for o := range orders {
			orderKeys = append(orderKeys, o)
		}
		sort.Ints(orderKeys)
		for _, ord := range orderKeys {
			channels := orders[ord]
			chanAddrs := make([]string, 0, len(channels))
			for a := range channels {
				chanAddrs = append(chanAddrs, a)
			}
			sort.Strings(chanAddrs)
			for _, addr := range chanAddrs {
				paramset := channels[addr]
				if err := c.dispatch(ctx, addr, paramsetKey, paramset, priority); err != nil {
					rollbackAll()
					return err
				}
			}
		}
	}
	// Optional CCU-confirmation wait. Walks the first `waitN` items
	// in collection order and blocks on each one's
	// WaitForConfirmation. Items that do implement
	// [WaitableDataPoint] are skipped silently so the option degrades
	// gracefully when callers pass fake DPs in tests.
	if waitN > 0 {
		limit := min(waitN, len(items))
		for i := range limit {
			w, ok := items[i].dp.(WaitableDataPoint)
			if !ok {
				continue
			}
			if err := w.WaitForConfirmation(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *CallParameterCollector) dispatch(
	ctx context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	paramset map[string]any,
	priority hmenum.CommandPriority,
) error {
	if len(paramset) == 1 {
		for param, value := range paramset {
			return c.backend.SetValue(ctx, channelAddress, hmenum.Parameter(param), value, priority)
		}
	}
	return c.backend.PutParamset(ctx, channelAddress, paramsetKey, paramset, priority)
}
