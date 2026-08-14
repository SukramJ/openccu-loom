// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// icSetterLike is the narrow contract ValueWriter needs from an
// InterfaceClient to route writes through the full reliability stack
// (throttle + circuit breaker + retrier) when [WriteOptions.SkipRetry]
// is set. Storing this as an interface keeps value_writer.go decoupled
// from the concrete *InterfaceClient type.
//
// Both methods mirror the corresponding InterfaceClient signatures.
type icSetterLike interface {
	// SetValue routes a single-parameter write through the IC's
	// reliability stack. skipRetry=true calls Retrier.DoOnce instead
	// of Retrier.Do.
	SetValue(ctx context.Context, b backends.Operations, channelAddress string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode, skipRetry bool) error
	// PutParamset routes a paramset write through the IC's reliability
	// stack. skipRetry=true calls Retrier.DoOnce instead of Retrier.Do.
	PutParamset(ctx context.Context, b backends.Operations, channelAddress, paramsetKeyOrLinkAddress string, values map[string]any, priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode, skipRetry bool) error
}

// ValueWriter dispatches SetValue calls to the right backend.
// Coordinators register (centralName, interfaceID) → backend pairs;
// the north-bound adapters call [ValueWriter.SetValue] without
// needing to know which transport backs the interface.
//
// SetValueWithOptions / PutParamsetWithOptions also need:
//
// - the central event bus to subscribe to DataPointValueChangedEvent
// when [WriteOptions.WaitForCallback] is true;
// - the per-interface [reliability.Retrier] to cancel queued retries
// when [WriteOptions.PurgeAddresses] is non-empty;
// - an [icSetterLike] (the [InterfaceClient]) to route writes through
// the full reliability stack when [WriteOptions.SkipRetry] is true.
//
// All three are wired after the central + reliability stack have been
// constructed. Nil is the safe default — WaitForCallback returns
// immediately, PurgeAddresses is a no-op, and SkipRetry falls through
// to the direct backend path (no retry anyway since the backend
// bypasses the Retrier).
type ValueWriter struct {
	mu             sync.RWMutex
	backends       map[valueWriterKey]backends.Operations
	icSetters      map[valueWriterKey]icSetterLike
	bus            eventBusLike
	busResolver    BusResolver
	retrier        retrierLike
	commandTracker CommandTrackerFn
	inFlight       *reliability.InFlightTracker
}

// eventBusLike is the narrow contract ValueWriter needs from the
// central event bus. Mirrors the surface
// [WaitForStateChangeOrTimeout] consumes — keeping this internal so
// the value writer does not become coupled to the full
// `internal/central/events` package.
type eventBusLike any

// BusResolver maps a Unit name to its event bus. Multi-CCU
// deployments use this to route [WaitForStateChangeOrTimeout]
// subscriptions to the correct bus per write call. The daemon
// installs a closure that consults the [central.Registry].
//
// Returning nil disables the wait path for that central — the call
// returns immediately as if [WriteOptions.WaitForCallback] were false.
type BusResolver func(centralName string) (bus any, ok bool)

// CommandTrackerFn is the optimistic-update hook the daemon installs so
// [ValueWriter.SetValueWithOptions] can immediately record a sent value
// in the [reliability.CommandTracker] of the matching
// [InterfaceClient]. The function receives the interfaceID plus the
// (channelAddress, parameter, paramsetKey, value) tuple and should call
// [InterfaceClient.WriteUnconfirmedValue] on the correct IC.
//
// Returning false means "no tracker available for this interface"
// the writer silently skips optimistic-update tracking. Nil function
// value disables the feature entirely (default until the daemon wires
// a resolver via [ValueWriter.SetCommandTrackerFn]).
type CommandTrackerFn func(interfaceID, channelAddress string, parameter hmenum.Parameter, paramsetKey hmenum.ParamsetKey, value any)

// retrierLike is the narrow contract ValueWriter needs from the
// per-interface [reliability.Retrier] used by [PurgeAddresses] and
// [CancelInterface].
type retrierLike interface {
	CancelDevice(deviceAddress string) int
	CancelInterface() int
}

type valueWriterKey struct {
	Central   string
	Interface string
}

// NewValueWriter returns an empty registry.
func NewValueWriter() *ValueWriter {
	return &ValueWriter{
		backends:  make(map[valueWriterKey]backends.Operations),
		icSetters: make(map[valueWriterKey]icSetterLike),
		inFlight:  reliability.NewInFlightTracker(),
	}
}

// Register binds a backend for (central, interface).
func (w *ValueWriter) Register(centralName, interfaceID string, b backends.Operations) {
	if b == nil {
		return
	}
	w.mu.Lock()
	w.backends[valueWriterKey{Central: centralName, Interface: interfaceID}] = b
	w.mu.Unlock()
}

// RegisterIC binds an [icSetterLike] (the [InterfaceClient]) for
// (central, interface). When non-nil, [SetValueWithOptions] and
// [PutParamsetWithOptions] route through the IC's full reliability
// stack whenever [WriteOptions.SkipRetry] is set, so the Retrier uses
// DoOnce instead of Do. Call after [Register] — the backend must
// always be registered first.
//
// Passing nil removes the IC binding; subsequent calls with
// [WriteOptions.SkipRetry] fall through to the direct backend path
// (no retrier is involved either way).
func (w *ValueWriter) RegisterIC(centralName, interfaceID string, ic icSetterLike) {
	key := valueWriterKey{Central: centralName, Interface: interfaceID}
	w.mu.Lock()
	if ic == nil {
		delete(w.icSetters, key)
	} else {
		w.icSetters[key] = ic
	}
	w.mu.Unlock()
}

// Deregister drops the binding for both the backend and the IC.
func (w *ValueWriter) Deregister(centralName, interfaceID string) {
	key := valueWriterKey{Central: centralName, Interface: interfaceID}
	w.mu.Lock()
	delete(w.backends, key)
	delete(w.icSetters, key)
	w.mu.Unlock()
}

// ErrNoBackend is returned when no backend is registered for
// (central, interface).
var ErrNoBackend = errors.New("value_writer: no backend for (central, interface)")

// SetValue routes the call. Delegates to [SetValueWithOptions] with default
// options so the same reliability path (PurgeAddresses guard, retrier hook)
// is used uniformly.
func (w *ValueWriter) SetValue(
	ctx context.Context, centralName, interfaceID, channelAddress string,
	parameter hmenum.Parameter, value any, priority hmenum.CommandPriority,
) error {
	return w.SetValueWithOptions(ctx, centralName, interfaceID, channelAddress, parameter, value, WriteOptions{
		Priority: priority,
		// RxMode left as Unset (CCU default), WaitForCallback false,
		// CheckAgainstPD false — matches the historical SetValue
		// behaviour while still routing through the per-call
		// PurgeAddresses + bus-aware path.
	})
}

// PutParamset writes several parameters of one paramset in a single
// call, with the same defaults [ValueWriter.SetValue] applies.
//
// It exists so a data point whose semantics require one atomic write
// can have it: a bounded switch-on carries its device-side auto-off in
// the same message as the switch-on, rather than spending a second
// radio transmission out of the duty-cycle budget the following stop
// command needs.
func (w *ValueWriter) PutParamset(
	ctx context.Context, centralName, interfaceID, channelAddress string,
	paramsetKey hmenum.ParamsetKey, values map[string]any, priority hmenum.CommandPriority,
) error {
	return w.PutParamsetWithOptions(ctx, centralName, interfaceID, channelAddress, paramsetKey, values,
		WriteOptions{Priority: priority})
}

// Backend returns the operations bound to (central, interface).
func (w *ValueWriter) Backend(centralName, interfaceID string) (backends.Operations, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	b, ok := w.backends[valueWriterKey{Central: centralName, Interface: interfaceID}]
	return b, ok
}

// SetEventBus installs a single central event bus. Convenient for
// single-CCU deployments. For multi-CCU use
// [ValueWriter.SetBusResolver] which routes per central name.
//
// When both are set, [SetBusResolver] takes precedence — the
// resolver is consulted first; if it returns ok=false, the writer
// falls back to this single-bus field.
func (w *ValueWriter) SetEventBus(bus any) {
	w.mu.Lock()
	w.bus = bus
	w.mu.Unlock()
}

// SetBusResolver installs a per-central event bus resolver. nil
// disables the resolver path; the writer then falls back to the
// single bus from [SetEventBus] (if any).
//
// Multi-CCU daemons install the registry-backed closure here so
// each [WaitForStateChangeOrTimeout] call subscribes to the bus
// owning the target central.
func (w *ValueWriter) SetBusResolver(r BusResolver) {
	w.mu.Lock()
	w.busResolver = r
	w.mu.Unlock()
}

// SetCommandTrackerFn installs the optimistic-update hook that is called
// after every successful [SetValueWithOptions] to record the sent value
// in the matching [InterfaceClient]'s [reliability.CommandTracker].
// nil disables optimistic-update tracking (default).
//
// The daemon composition root wires a closure that looks up the IC via
// the central registry and calls [InterfaceClient.WriteUnconfirmedValue].
func (w *ValueWriter) SetCommandTrackerFn(fn CommandTrackerFn) {
	w.mu.Lock()
	w.commandTracker = fn
	w.mu.Unlock()
}

// SetRetrier installs the per-interface [reliability.Retrier] used to
// purge pending retries for addresses listed in
// [WriteOptions.PurgeAddresses]. nil disables purging.
func (w *ValueWriter) SetRetrier(r any) {
	w.mu.Lock()
	if r == nil {
		w.retrier = nil
	} else if rr, ok := r.(retrierLike); ok {
		w.retrier = rr
	}
	w.mu.Unlock()
}

// InFlightTracker returns the shared [reliability.InFlightTracker] so callers
// (e.g. callback coordinators) can check whether a key is currently being
// written to the CCU. The tracker is always non-nil.
func (w *ValueWriter) InFlightTracker() *reliability.InFlightTracker {
	return w.inFlight
}

// CancelInterface aborts all in-flight retry chains for the (central,
// interface) pair by forwarding to the installed retrier's
// [reliability.Retrier.CancelInterface]. Returns the number of chains
// canceled, or 0 when no retrier is installed.
func (w *ValueWriter) CancelInterface(centralName, interfaceID string) int {
	w.mu.RLock()
	r := w.retrier
	w.mu.RUnlock()
	if r == nil {
		return 0
	}
	return r.CancelInterface()
}
