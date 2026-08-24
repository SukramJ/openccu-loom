// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	paramvalidate "github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// WriteOptions carries the per-call directives for a SetValue or
// PutParamset write. Mirrors the keyword arguments.
// `InterfaceClient.set_value` / `put_paramset`
// (`client/interface_client.py:1125,861`):
//
//	set_value(*, channel_address, paramset_key, parameter, value,
//	 rx_mode=LEVEL, wait_for_callback=None,
//	 check_against_pd=False, priority=Low, retry=Default)
//
// openccu-loom's typed Set* paths on `*generic.DataPoint[T]` already
// validate against the descriptor on input — the [WriteOptions.CheckAgainstPD]
// flag is therefore *opt-in* and re-runs the validation explicitly.
// Callers that want the strictest semantics (e.g. an MQTT command-topic
// handler that received a string from a third-party publisher) set
// `CheckAgainstPD=true`; the typed-DP path leaves it false.
type WriteOptions struct {
	// Priority is the [hmenum.CommandPriority] surface that the
	// reliability stack uses to schedule this call against other
	// pending traffic.
	Priority hmenum.CommandPriority

	// RxMode is the wire-level CCU receive-mode hint
	// ([hmenum.CommandRxModeUnset], `LEVEL`, `WAKEUP`, `BURST`). Empty falls
	// through to the backend default.
	RxMode hmenum.CommandRxMode

	// CheckAgainstPD asks the writer to validate the value against the parameter
	// descriptor before invoking the backend.
	//
	// When the write originates from a typed DP setter the validation has
	// already happened; the flag is opt-in so external entry points (REST PUT,
	// MQTT command, WS write) can request a second pass.
	CheckAgainstPD bool

	// Descriptor carries the parameter's hmproto.ParameterData when
	// CheckAgainstPD is true. The writer cannot resolve the descriptor
	// itself — the caller (which has access to the DP) must pass it.
	// Empty descriptor with CheckAgainstPD=true is a programming error
	// and yields ErrMissingDescriptor.
	Descriptor hmproto.ParameterData

	// WaitForCallback opts the caller into waiting for the CCU's confirmation
	// event before the SetValue / PutParamset call returns.
	//
	//   - false (default) — return as soon as the write is on the wire,
	//     without waiting for the CCU to echo it back.
	//   - true — wait until the CCU confirms the new value by push callback,
	//     [WriteOptions.WaitForCallbackTimeout] elapses, or the context is
	//     cancelled.
	//
	// WaitForCallbackTimeout: zero selects the 60s default; a positive value
	// waits exactly that long and then returns [ErrStateChangeTimeout].
	//
	// No production caller sets this today — every site that does is a test —
	// so the resolver requirement below is currently unexercised outside them.
	//
	// Requires a bus resolver installed via
	// [ValueWriter.SetBusResolver]. Without one the wait path is
	// silently skipped (caller behaves as if WaitForCallback were false).
	WaitForCallback bool

	// WaitForCallbackTimeout bounds the wait. Zero falls back to 60 s
	// Ignored when WaitForCallback is false.
	WaitForCallbackTimeout time.Duration

	// SkipRetry, when true, bypasses the per-key retry tracking in the
	// [reliability.Retrier]. The command is still attempted once through the
	// circuit breaker; it is simply not registered for automatic re-sending on
	// transient failures.
	//
	// Typical use: one-shot fire-and-forget commands (e.g. virtual key presses)
	// where a retry would cause a duplicate action.
	SkipRetry bool
}

// ErrMissingDescriptor is returned by SetValueWithOptions
// PutParamsetWithOptions when CheckAgainstPD is true but the
// WriteOptions does not carry a descriptor.
var ErrMissingDescriptor = errors.New("client: WriteOptions.CheckAgainstPD requires a Descriptor")

// SetValueWithOptions is the high-level set_value entry point that
// accepts a [WriteOptions] for fine-grained control. It mirrors the
// Full
//
// - Validates the value against the descriptor when
// [WriteOptions.CheckAgainstPD] is true.
// - Routes through the registered backend's SetValue (which goes
// through the InterfaceClient's reliability stack via
// BackendCaller — coalescer, throttle, circuit, retry).
// - When [WriteOptions.WaitForCallback] is true, currently returns
// immediately (Cluster 4 will wire the subscribe-and-wait logic).
//
// The simpler [ValueWriter.SetValue] remains for callers that don't
// need the option surface; it is implemented in terms of this method.
func (w *ValueWriter) SetValueWithOptions(
	ctx context.Context,
	centralName, interfaceID, channelAddress string,
	parameter hmenum.Parameter,
	value any,
	opts WriteOptions,
) error {
	if opts.CheckAgainstPD {
		if opts.Descriptor.Type == "" {
			return ErrMissingDescriptor
		}
		pv, ok := value.(hmtypes.ParamValue)
		if !ok {
			coerced, err := hmtypes.NewParamValue(value)
			if err != nil {
				return fmt.Errorf("client.SetValueWithOptions: coerce: %w", err)
			}
			pv = coerced
		}
		if err := paramvalidate.Validate(opts.Descriptor, pv); err != nil {
			return fmt.Errorf("client.SetValueWithOptions: validate: %w", err)
		}
	}

	w.mu.RLock()
	key := keyFor(centralName, interfaceID)
	b, bOK := w.backends[key]
	ic := w.icSetters[key]
	resolved := resolveBus(w.busResolver, centralName)
	ctFn := w.commandTracker
	w.mu.RUnlock()
	if !bOK {
		return fmt.Errorf("%w: central=%s interface=%s", ErrNoBackend, centralName, interfaceID)
	}

	// Stage the value as in-flight BEFORE the wire write so that a callback
	// echo arriving concurrently during the call still has a reader fallback.
	// The entry is cleared in the deferred cleanup regardless of outcome.
	if flightKey, err := hmtypes.NewDataPointKey(interfaceID, channelAddress, hmenum.ParamsetKeyValues, string(parameter)); err == nil {
		w.inFlight.Stage(flightKey, value)
		defer w.inFlight.Clear(flightKey)
	}

	// SkipRetry propagation: when an IC is registered and SkipRetry is set,
	// route the write through the IC's reliability stack so the Retrier uses
	// DoOnce instead of Do.
	if opts.SkipRetry && ic != nil {
		if err := ic.SetValue(ctx, b, channelAddress, parameter, value, opts.Priority, opts.RxMode, true); err != nil {
			return err
		}
	} else if err := b.SetValue(ctx, channelAddress, parameter, value, opts.Priority, opts.RxMode); err != nil {
		return err
	}

	// Optimistic-update tracking — record the sent value in the IC's
	// CommandTracker so north-bound adapters can return the new value
	// immediately before the CCU echoes back a callback.
	if ctFn != nil {
		ctFn(interfaceID, channelAddress, parameter, hmenum.ParamsetKeyValues, value)
	}

	if opts.WaitForCallback && resolved != nil {
		dpk, err := hmtypes.NewDataPointKey(interfaceID, channelAddress, hmenum.ParamsetKeyValues, string(parameter))
		if err != nil {
			// Best-effort: the wire write itself succeeded above; an
			// invalid DataPointKey only blocks the callback wait. The
			// caller's contract for SetValueWithOptions is "return nil
			// when the wire write succeeds" — surfacing the key error
			// would falsely report the write itself as failed.
			return nil //nolint:nilerr // best-effort: wire write already succeeded
		}
		timeout := opts.WaitForCallbackTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		if waitErr := WaitForStateChangeOrTimeout(ctx, resolved, []DPKValue{{Key: dpk, Value: value}}, timeout); waitErr != nil {
			return fmt.Errorf("client.SetValueWithOptions: %w", waitErr)
		}
	}
	return nil
}

// resolveBus asks the installed [BusResolver] for the bus that owns
// centralName. Returns nil when no resolver is installed or the
// resolver yields no match — the caller treats nil as "skip wait path".
func resolveBus(resolver BusResolver, centralName string) *events.Bus {
	if resolver == nil {
		return nil
	}
	bus, ok := resolver(centralName)
	if !ok {
		return nil
	}
	// The resolver hands back `any` so the daemon can install it without
	// this package depending on the registry that owns the buses.
	cb, ok := bus.(*events.Bus)
	if !ok {
		return nil
	}
	return cb
}

// PutParamsetWithOptions is the high-level put_paramset entry point.
// Same option surface as [ValueWriter.SetValueWithOptions].
//
// `values` is the wire-level paramset map; CheckAgainstPD here would
// require per-key descriptors — that variant is not yet wired. When
// [WriteOptions.CheckAgainstPD] is true and Descriptor is empty,
// returns [ErrMissingDescriptor]; when Descriptor is non-empty it is
// applied to every value uniformly which is rarely what callers want
// (paramsets typically span heterogeneous parameter types). For
// per-key validation, callers should validate before invoking this
// method.
func (w *ValueWriter) PutParamsetWithOptions(
	ctx context.Context,
	centralName, interfaceID, channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	opts WriteOptions,
) error {
	if opts.CheckAgainstPD {
		if opts.Descriptor.Type == "" {
			return ErrMissingDescriptor
		}
		for _, v := range values {
			pv, ok := v.(hmtypes.ParamValue)
			if !ok {
				coerced, err := hmtypes.NewParamValue(v)
				if err != nil {
					return fmt.Errorf("client.PutParamsetWithOptions: coerce: %w", err)
				}
				pv = coerced
			}
			if err := paramvalidate.Validate(opts.Descriptor, pv); err != nil {
				return fmt.Errorf("client.PutParamsetWithOptions: validate: %w", err)
			}
		}
	}

	w.mu.RLock()
	ppKey := keyFor(centralName, interfaceID)
	b, bOK := w.backends[ppKey]
	ic := w.icSetters[ppKey]
	resolved := resolveBus(w.busResolver, centralName)
	w.mu.RUnlock()
	if !bOK {
		return fmt.Errorf("%w: central=%s interface=%s", ErrNoBackend, centralName, interfaceID)
	}
	// Stage all paramset values as in-flight BEFORE the wire write so that
	// callback echoes arriving concurrently still have a reader fallback.
	// All entries are cleared when the function returns, regardless of outcome.
	// The defer-in-loop is intentional: each iteration captures a distinct fkCopy
	// so every staged key is unconditionally cleared at function exit.
	for name, v := range values {
		if fk, err := hmtypes.NewDataPointKey(interfaceID, channelAddress, paramsetKey, name); err == nil {
			fkCopy := fk
			vCopy := v
			w.inFlight.Stage(fkCopy, vCopy)
			//nolint:gocritic // deferInLoop intentional: each fkCopy is distinct; every staged key must be cleared at function exit
			defer w.inFlight.Clear(fkCopy)
		}
	}

	// SkipRetry propagation: route through IC's reliability stack
	// (Retrier.DoOnce) when both SkipRetry and an IC are set. Without an IC the
	// direct backend path has no retry to skip.
	if opts.SkipRetry && ic != nil {
		if err := ic.PutParamset(ctx, b, channelAddress, string(paramsetKey), values, opts.Priority, opts.RxMode, true); err != nil {
			return err
		}
	} else if err := b.PutParamset(ctx, channelAddress, paramsetKey, values, opts.Priority, opts.RxMode); err != nil {
		return err
	}
	if opts.WaitForCallback && resolved != nil {
		// Build a DPKValue per paramset entry. The CCU echoes one
		// DataPointValueChangedEvent per parameter — we wait until
		// all of them confirm or the timeout elapses.
		dpkValues := make([]DPKValue, 0, len(values))
		for name, v := range values {
			dpk, err := hmtypes.NewDataPointKey(interfaceID, channelAddress, paramsetKey, name)
			if err != nil {
				continue
			}
			dpkValues = append(dpkValues, DPKValue{Key: dpk, Value: v})
		}
		if len(dpkValues) > 0 {
			timeout := opts.WaitForCallbackTimeout
			if timeout <= 0 {
				timeout = 60 * time.Second
			}
			if waitErr := WaitForStateChangeOrTimeout(ctx, resolved, dpkValues, timeout); waitErr != nil {
				return fmt.Errorf("client.PutParamsetWithOptions: %w", waitErr)
			}
		}
	}
	return nil
}

// Compile-time guard so refactoring catches a renamed
// backends.Operations method.
var _ backends.Operations = backends.Operations(nil)
