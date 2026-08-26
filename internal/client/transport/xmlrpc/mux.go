// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// MethodHandler processes one XML-RPC method invocation. Params are
// passed as decoded [Value]s; the return [Value] is serialised into the
// response. Returning a non-nil [*hmerr.XMLRPCFault] or any other error
// causes a <fault> response; the dispatcher translates non-fault errors
// into an opaque -1/err.Error() fault, matching CCU convention.
type MethodHandler func(ctx context.Context, params []Value) (Value, error)

// Mux routes XML-RPC method invocations to handlers by method name.
// Safe for concurrent registration and dispatch.
//
// Mux does not implement http.Handler — that lives in [Handler], which
// wraps a Mux and handles the HTTP framing.
type Mux struct {
	mu       sync.RWMutex
	methods  map[string]MethodHandler
	fallback MethodHandler
}

// NewMux returns an empty Mux.
func NewMux() *Mux {
	return &Mux{methods: make(map[string]MethodHandler)}
}

// Handle registers fn for the given method name, replacing any prior
// registration.
func (m *Mux) Handle(method string, fn MethodHandler) {
	if method == "" {
		panic("xmlrpc: Mux.Handle called with empty method name")
	}
	if fn == nil {
		panic("xmlrpc: Mux.Handle called with nil handler")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.methods[method] = fn
}

// HandleFallback registers a catch-all handler used when no specific
// method has been registered. If unset, unknown methods produce a
// fault with code -32601 ("method not found") to match JSON-RPC's
// convention as the closest standardised codepoint.
func (m *Mux) HandleFallback(fn MethodHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallback = fn
}

// Methods returns the currently registered method names in undefined order.
func (m *Mux) Methods() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.methods))
	for n := range m.methods {
		out = append(out, n)
	}
	return out
}

// Dispatch invokes the handler registered for method and returns its
// result. Unregistered methods use the fallback handler; if no
// fallback is set, a fault with code -32601 is returned (as an error).
func (m *Mux) Dispatch(ctx context.Context, method string, params []Value) (Value, error) {
	m.mu.RLock()
	fn, ok := m.methods[method]
	fallback := m.fallback
	m.mu.RUnlock()

	if !ok {
		if fallback != nil {
			return fallback(ctx, params)
		}
		return nil, &hmerr.XMLRPCFault{Code: -32601, Message: "method not found: " + method}
	}
	return fn(ctx, params)
}

// RegisterSystemMethods wires system.listMethods, system.methodHelp and
// system.multicall into the mux. Called by the CCU during callback
// registration; handler responses are unopinionated.
func (m *Mux) RegisterSystemMethods() {
	m.Handle("system.listMethods", func(_ context.Context, _ []Value) (Value, error) {
		names := m.Methods()
		out := make(ArrayValue, len(names))
		for i, n := range names {
			out[i] = StringValue(n)
		}
		return out, nil
	})

	m.Handle("system.methodHelp", func(_ context.Context, _ []Value) (Value, error) {
		// Intentionally empty: the CCU only checks existence, not content.
		return StringValue(""), nil
	})

	m.Handle("system.methodSignature", func(_ context.Context, _ []Value) (Value, error) {
		// Returns "undef" — the CCU treats an absent signature as "any types
		// accepted" and never inspects the contents.
		return ArrayValue{StringValue("undef")}, nil
	})

	m.Handle("system.multicall", func(ctx context.Context, params []Value) (Value, error) {
		if len(params) != 1 {
			return nil, fmt.Errorf("system.multicall: expected 1 param, got %d", len(params))
		}
		calls, err := AsArray(params[0])
		if err != nil {
			return nil, fmt.Errorf("system.multicall: %w", err)
		}
		results := make(ArrayValue, 0, len(calls))
		for i, c := range calls {
			s, err := AsStruct(c)
			if err != nil {
				return nil, fmt.Errorf("system.multicall call %d: %w", i, err)
			}
			name, err := StructField[StringValue](s, "methodName")
			if err != nil {
				return nil, fmt.Errorf("system.multicall call %d: %w", i, err)
			}
			innerParams, err := StructField[ArrayValue](s, "params")
			if err != nil {
				return nil, fmt.Errorf("system.multicall call %d: %w", i, err)
			}
			sub, err := m.Dispatch(ctx, string(name), innerParams)
			if err != nil {
				var fault *hmerr.XMLRPCFault
				if !errors.As(err, &fault) {
					fault = &hmerr.XMLRPCFault{Code: -1, Message: err.Error()}
				}
				results = append(results, StructValue{Members: []Member{
					{Name: "faultCode", Value: IntValue(int32(fault.Code))}, //nolint:gosec // fault codes fit int32; see #20
					{Name: "faultString", Value: StringValue(fault.Message)},
				}})
				continue
			}
			// A successful sub-call wraps its return in a single-element
			// array, per the XML-RPC multicall convention.
			results = append(results, ArrayValue{sub})
		}
		return results, nil
	})
}
