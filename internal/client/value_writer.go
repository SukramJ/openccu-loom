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
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
// - a [BusResolver] that yields the central event bus to subscribe to
// DataPointValueChangedEvent when [WriteOptions.WaitForCallback] is
// true;
// - an [icSetterLike] (the [InterfaceClient]) to route writes through
// the full reliability stack when [WriteOptions.SkipRetry] is true.
//
// Both are wired after the central + reliability stack have been
// constructed. Nil is the safe default — WaitForCallback returns
// immediately and SkipRetry falls through to the direct backend path
// (no retry anyway since the backend bypasses the Retrier).
type ValueWriter struct {
	mu             sync.RWMutex
	backends       map[valueWriterKey]backends.Operations
	icSetters      map[valueWriterKey]icSetterLike
	busResolver    BusResolver
	commandTracker CommandTrackerFn
	inFlight       *reliability.InFlightTracker
}

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

// valueWriterKey is the backend registry's key: a central plus the canonical
// `<central>-<interface>` wire id the CCU echoes and every per-central
// registry uses. The wire id is typed so a lookup cannot be spelled with the
// bare interface name a device also carries — that mistake compiles, finds
// nothing, and surfaces as a write that reports "no backend" for an interface
// that is plainly connected.
type valueWriterKey struct {
	Central   string
	Interface hmtypes.WireInterfaceID
}

// keyFor builds the registry key from the wire id in its string form. The
// write entry points ([ValueWriter.SetValueWithOptions] and friends) take the
// interface as a string because their signature is declared by port interfaces
// in a dozen consumer packages; this is the single place that adopts it, so
// the write path and the registration path cannot key the map differently.
func keyFor(centralName, interfaceID string) valueWriterKey {
	return valueWriterKey{Central: centralName, Interface: hmtypes.ParseWireInterfaceID(interfaceID)}
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
func (w *ValueWriter) Register(centralName string, interfaceID hmtypes.WireInterfaceID, b backends.Operations) {
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
func (w *ValueWriter) RegisterIC(centralName string, interfaceID hmtypes.WireInterfaceID, ic icSetterLike) {
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
func (w *ValueWriter) Deregister(centralName string, interfaceID hmtypes.WireInterfaceID) {
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
// options so both entry points share one write path.
func (w *ValueWriter) SetValue(
	ctx context.Context, centralName, interfaceID, channelAddress string,
	parameter hmenum.Parameter, value any, priority hmenum.CommandPriority,
) error {
	return w.SetValueWithOptions(ctx, centralName, interfaceID, channelAddress, parameter, value, WriteOptions{
		Priority: priority,
		// RxMode left as Unset (CCU default), WaitForCallback false,
		// CheckAgainstPD false — matches the historical SetValue
		// behaviour while still routing through the bus-aware path.
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
func (w *ValueWriter) Backend(centralName string, interfaceID hmtypes.WireInterfaceID) (backends.Operations, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	b, ok := w.backends[valueWriterKey{Central: centralName, Interface: interfaceID}]
	return b, ok
}

// SetBusResolver installs a per-central event bus resolver. nil
// disables the wait path entirely — [WriteOptions.WaitForCallback]
// then behaves as if it were false.
//
// One ValueWriter serves every configured CCU, so the bus a write has
// to wait on is a function of the target central, never a property of
// the writer: resolving per call is what keeps a write to central B
// from waiting on central A's bus. The daemon installs the
// registry-backed closure here.
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

// InFlightTracker returns the shared [reliability.InFlightTracker] so callers
// (e.g. callback coordinators) can check whether a key is currently being
// written to the CCU. The tracker is always non-nil.
func (w *ValueWriter) InFlightTracker() *reliability.InFlightTracker {
	return w.inFlight
}
