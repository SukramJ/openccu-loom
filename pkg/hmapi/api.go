// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmapi provides a high-level HomematicAPI facade that is the
// single convenience entry-point into the openccu-loom daemon.
//
// The facade aggregates a [Registry] of [Unit] instances and
// exposes the most commonly needed operations without requiring
// callers to wire up the individual coordinators and registries.
//
// # Design notes
//
// This package sits in pkg/ so that external programs that embed
// openccu-loom as a library can import it without pulling in the whole
// internal/ tree. All concrete types live in internal/; the facade
// delegates through the [Registrar] and [Connector] interfaces.
//
// # Skipped items
//
// (full RequestContext cross-cutting) are not part of
// this facade in v1.0. The facade carries no per-request context
// injection; callers pass context.Context through [Connect] and the
// individual operation methods.
package hmapi

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ErrNotConnected is returned by methods that require at least one
// registered [Unit] when none has been connected.
var ErrNotConnected = errors.New("hmapi: not connected — call Connect first")

// ErrAlreadyConnected is returned when Connect is called on an API
// instance that has already been started.
var ErrAlreadyConnected = errors.New("hmapi: already connected")

// ErrNotSupported is returned by [HomematicAPI.ReadValue],
// [HomematicAPI.WriteValue] and similar methods when the target
// [CentralHandle] does not implement the corresponding optional interface.
var ErrNotSupported = errors.New("hmapi: operation not supported by this central")

// CentralHandle is the minimal interface that the facade requires of
// each Unit. Using an interface here keeps pkg/hmapi free of
// the internal/central import path.
//
// Implementations: *internal/central.Unit.
type CentralHandle interface {
	// Name returns the operator-assigned identifier of the central.
	Name() string
	// Connect establishes the south-bound connection to the CCU and
	// starts all coordinators. Idempotent: calling Connect on an
	// already-running central is a no-op.
	Connect(ctx context.Context) error
	// Disconnect tears down the south-bound connection and stops all
	// coordinators. Idempotent.
	Disconnect(ctx context.Context) error
}

// ValueReader is an optional interface implemented by [CentralHandle]
// types that support reading a data-point value from the CCU.
//
// ReadValue retrieves the current value for the given channel address
// and parameter name. The concrete return type is one of bool, int,
// float64, or string matching the data-point's ParameterType.
type ValueReader interface {
	ReadValue(ctx context.Context, channelAddress, parameterName string) (any, error)
}

// ValueWriter is an optional interface implemented by [CentralHandle]
// types that support writing a data-point value to the CCU.
//
// WriteValue sets the CCU parameter identified by channelAddress
// parameterName to value. The value must be compatible with the
// data-point's ParameterType.
type ValueWriter interface {
	WriteValue(ctx context.Context, channelAddress, parameterName string, value any) error
}

// UpdateSubscriber is an optional interface implemented by
// [CentralHandle] types that support event-based update subscriptions.
//
// SubscribeToUpdates installs handler as a callback that is invoked
// for every data-point value change. The returned unsubscribe function
// removes the handler. Calling unsubscribe more than once is safe.
//
// (api.py). The Python implementation returns a token that is
// passed to unsubscribe(); Go uses the closure pattern instead.
type UpdateSubscriber interface {
	SubscribeToDataPointUpdates(
		handler func(centralName, channelAddress, parameterName string, value any),
	) (unsubscribe func())
}

// HomematicAPI is the top-level facade over the configured set of
// Units. Callers that want a single entry-point into the
// daemon create one API instance, register their centrals via
// [HomematicAPI.Register], and then call [HomematicAPI.Connect].
type HomematicAPI struct {
	mu        sync.RWMutex
	centrals  map[string]CentralHandle
	connected bool
}

// New returns an empty, disconnected HomematicAPI.
func New() *HomematicAPI {
	return &HomematicAPI{
		centrals: make(map[string]CentralHandle),
	}
}

// Register adds a Unit to the API. Returns an error when a
// central with the same name has already been registered.
func (a *HomematicAPI) Register(c CentralHandle) error {
	if c == nil || c.Name() == "" {
		return errors.New("hmapi: cannot register nil or unnamed central")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.centrals[c.Name()]; ok {
		return errors.New("hmapi: central already registered: " + c.Name())
	}
	a.centrals[c.Name()] = c
	return nil
}

// Central returns the handle for the named central, or false.
func (a *HomematicAPI) Central(name string) (CentralHandle, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.centrals[name]
	return c, ok
}

// Centrals returns every registered CentralHandle in an unspecified
// order.
func (a *HomematicAPI) Centrals() []CentralHandle {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]CentralHandle, 0, len(a.centrals))
	for _, c := range a.centrals {
		out = append(out, c)
	}
	return out
}

// Connect starts all registered centrals concurrently. Any error from
// a Connect() call is collected and returned as a multi-error;
// successful centrals remain running even when others fail. Calling
// Connect a second time returns [ErrAlreadyConnected].
func (a *HomematicAPI) Connect(ctx context.Context) error {
	a.mu.Lock()
	if a.connected {
		a.mu.Unlock()
		return ErrAlreadyConnected
	}
	snapshot := make([]CentralHandle, 0, len(a.centrals))
	for _, c := range a.centrals {
		snapshot = append(snapshot, c)
	}
	a.connected = true
	a.mu.Unlock()

	if len(snapshot) == 0 {
		return nil
	}

	type result struct {
		name string
		err  error
	}
	ch := make(chan result, len(snapshot))
	for _, c := range snapshot {
		go func() {
			err := c.Connect(ctx)
			ch <- result{name: c.Name(), err: err}
		}()
	}
	var errs []error
	for range snapshot {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) == 1 {
		return errs[0]
	}
	if len(errs) > 1 {
		return &multiError{errs: errs}
	}
	return nil
}

// Disconnect stops all registered centrals concurrently. Errors are
// collected and returned as a multi-error. After Disconnect the API
// may be reused: call [HomematicAPI.Connect] again to restart.
func (a *HomematicAPI) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	snapshot := make([]CentralHandle, 0, len(a.centrals))
	for _, c := range a.centrals {
		snapshot = append(snapshot, c)
	}
	a.connected = false
	a.mu.Unlock()

	type result struct {
		err error
	}
	ch := make(chan result, len(snapshot))
	for _, c := range snapshot {
		go func() {
			ch <- result{err: c.Disconnect(ctx)}
		}()
	}
	var errs []error
	for range snapshot {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) == 1 {
		return errs[0]
	}
	if len(errs) > 1 {
		return &multiError{errs: errs}
	}
	return nil
}

// IsConnected reports whether [Connect] has been called successfully.
func (a *HomematicAPI) IsConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

// ReadValue reads the current value of a data-point from the named
// central. Returns [ErrNotConnected] when no central is registered or
// the API is not connected. Returns [ErrNotSupported] when the central
// does not implement [ValueReader].
func (a *HomematicAPI) ReadValue(ctx context.Context, centralName, channelAddress, parameterName string) (any, error) {
	c, err := a.requireCentral(centralName)
	if err != nil {
		return nil, err
	}
	reader, ok := c.(ValueReader)
	if !ok {
		return nil, ErrNotSupported
	}
	return reader.ReadValue(ctx, channelAddress, parameterName)
}

// WriteValue writes a data-point value to the named central. Returns
// [ErrNotConnected] when no central is registered or the API is not
// connected. Returns [ErrNotSupported] when the central does not
// implement [ValueWriter].
func (a *HomematicAPI) WriteValue(ctx context.Context, centralName, channelAddress, parameterName string, value any) error {
	c, err := a.requireCentral(centralName)
	if err != nil {
		return err
	}
	writer, ok := c.(ValueWriter)
	if !ok {
		return ErrNotSupported
	}
	return writer.WriteValue(ctx, channelAddress, parameterName, value)
}

// SubscribeToUpdates installs handler as a callback that is invoked
// for every data-point value change on any registered central that
// implements [UpdateSubscriber]. Returns a combined unsubscribe
// function that removes the handler from every subscribed central.
//
// (api.py).
func (a *HomematicAPI) SubscribeToUpdates(
	handler func(centralName, channelAddress, parameterName string, value any),
) (unsubscribe func()) {
	a.mu.RLock()
	snapshot := make([]CentralHandle, 0, len(a.centrals))
	for _, c := range a.centrals {
		snapshot = append(snapshot, c)
	}
	a.mu.RUnlock()

	var fns []func()
	for _, c := range snapshot {
		if sub, ok := c.(UpdateSubscriber); ok {
			unsub := sub.SubscribeToDataPointUpdates(handler)
			fns = append(fns, unsub)
		}
	}
	return func() {
		for _, f := range fns {
			f()
		}
	}
}

// requireCentral returns the handle for the named central, or an error
// when not found or the API is not connected.
func (a *HomematicAPI) requireCentral(name string) (CentralHandle, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.connected {
		return nil, ErrNotConnected
	}
	c, ok := a.centrals[name]
	if !ok {
		return nil, errors.New("hmapi: unknown central: " + name)
	}
	return c, nil
}

// --------------------------------------------------------------------------
// internal helpers
// --------------------------------------------------------------------------

type multiError struct{ errs []error }

func (m *multiError) Error() string {
	var s strings.Builder
	for i, e := range m.errs {
		if i > 0 {
			s.WriteString("; ")
		}
		s.WriteString(e.Error())
	}
	return s.String()
}

func (m *multiError) Unwrap() []error { return m.errs }
