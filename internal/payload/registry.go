// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// globalScalarArgsMu guards globalScalarArgs.
var globalScalarArgsMu sync.RWMutex

// globalScalarArgs is the package-level aggregate of method→scalar-arg-key
// mappings. Two things write into it, both throughout the whole process
// lifetime, not just at startup:
//
//   - [RegisterGlobalScalarArgKey], called from custom-DP package `init()`
//     blocks so the key is known before any device of that kind exists.
//   - [ServiceRegistry.registerServiceWithArg], called every time a device
//     channel materialises a Source instance (i.e. whenever the CCU
//     inventory is rescanned or a device is hot-added), which mirrors the
//     same method→key pairs onto the shared table.
//
// Both writers are idempotent in practice — the scalar-arg key is a property
// of the method name itself (every "set_temperature" registration carries
// the same "temperature" key), so repeated writes across the process
// lifetime never change an already-observed value. That is what makes a
// single shared, mutex-guarded map safe here despite the "no mutable
// package-level state" rule: there is no safe point at which registration
// could be closed off (new device kinds can appear for as long as the
// daemon runs), so freezing after "startup" would reject legitimate
// runtime device-discovery writes. The map is intentionally global: it is
// the only way north-bound adapters (MQTT bridge) can resolve the key for
// a bare-scalar payload by method name alone, before they hold a concrete
// Source instance.
var globalScalarArgs = make(map[string]string)

// GlobalScalarArgKey returns the canonical scalar-argument key for the
// given service method. It is consulted by the MQTT bridge when the
// incoming payload is a bare scalar (not a JSON object) and the key must
// be inferred from the method name alone — before any Source has been
// resolved.
//
// Returns the registered key, or "value" as a universal fallback for
// methods that were registered without an explicit scalar arg key (legacy
// [ServiceRegistry.RegisterService]) or that are unknown.
func GlobalScalarArgKey(method string) string {
	globalScalarArgsMu.RLock()
	k := globalScalarArgs[method]
	globalScalarArgsMu.RUnlock()
	if k == "" {
		return "value"
	}
	return k
}

// RegisterGlobalScalarArgKey pre-populates the package-level
// [globalScalarArgs] table with method→key mappings ahead of any Source
// construction. Intended for `init()` blocks in custom-DP packages so
// north-bound adapters (MQTT bridge, REST) can resolve the scalar key
// for a method without first instantiating a concrete Source — which
// matters for tests + discovery payloads that fire before any device
// has materialised.
//
// Re-registration with the same key is a no-op; conflicting re-registration
// panics so a typo or copy-paste collision fails loudly at program start.
func RegisterGlobalScalarArgKey(method, key string) {
	if method == "" || key == "" {
		return
	}
	globalScalarArgsMu.Lock()
	defer globalScalarArgsMu.Unlock()
	if existing, ok := globalScalarArgs[method]; ok && existing != key {
		panic(fmt.Sprintf("payload: scalar-arg key conflict for %q (%q vs %q)", method, existing, key))
	}
	globalScalarArgs[method] = key
}

// ErrUnknownServiceMethod is returned by [ServiceRegistry.Invoke] (and
// therefore by [Source.Invoke]) when the requested method name was
// never registered. Callers test with [errors.Is].
var ErrUnknownServiceMethod = errors.New("payload: unknown service method")

// ServiceHandler is the uniform shape of an external service method.
// params is the JSON-decoded request body; the handler validates and
// coerces to its real argument types. priority propagates to the
// south-bound write so callers retain control over queue placement.
type ServiceHandler func(ctx context.Context, params map[string]any,
	priority hmenum.CommandPriority) error

// ServiceRegistry is the embeddable helper that implements the
// write-side half of [Source]: ServiceMethodNames and Invoke.
//
// Embed it in any model type that exposes external operations; call
// [ServiceRegistry.RegisterService] from the constructor for each
// method that should be reachable from outside the daemon. Methods
// that are not registered remain internal — there is no separate
// scope marker.
//
// The zero value is ready to use. RegisterService initialises the
// internal map on first call.
//
// All methods are safe for concurrent use. Registration is expected
// at construction time; re-registering a name panics. Embedding the
// zero value in a Source-bearing struct that has no service methods
// gives that struct correct ServiceMethodNames (nil) and Invoke
// (always returns [ErrUnknownServiceMethod]) implementations for free.
//
// ServiceMethodNames is O(1) after the first call per registry lifetime.
// The cached slice is rebuilt only when [RegisterService] is called.
// Mirrors the Python reference implementation's WeakKeyDictionary-based
// O(1) cache (decorators.py:321).
type ServiceRegistry struct {
	mu         sync.RWMutex
	names      []string
	funcs      map[string]ServiceHandler
	scalarArgs map[string]string // per-instance scalar-arg-key map; also propagated to globalScalarArgs
	cached     []string          // immutable snapshot rebuilt on RegisterService
}

// RegisterService records a service method under name. Order of
// registration is preserved; ServiceMethodNames returns names in the
// same order they were registered, so HA-Discovery emits stable
// `*_command_topic` mappings across restarts.
//
// Panics if name is empty, h is nil, or name was already registered.
// All three conditions are construction-time programming errors — a
// panic is the right signal.
//
// Methods registered via RegisterService get "value" as their scalar-arg
// key (legacy API). Use [RegisterServiceWithArg] when the method expects a
// named scalar argument so the MQTT bridge can wrap bare payloads correctly.
func (r *ServiceRegistry) RegisterService(name string, h ServiceHandler) {
	r.registerServiceWithArg(name, "", h)
}

// RegisterServiceWithArg records a service method under name and associates
// scalarArgKey as the preferred argument name when the MQTT bridge receives a
// bare scalar payload (not a JSON object) for this method. The key is also
// recorded in the package-level [globalScalarArgs] table so the bridge can
// resolve it without a concrete Source instance.
//
// An empty scalarArgKey defaults to "value". Panics under the same conditions
// as [RegisterService].
func (r *ServiceRegistry) RegisterServiceWithArg(name, scalarArgKey string, h ServiceHandler) {
	r.registerServiceWithArg(name, scalarArgKey, h)
}

// OverrideService replaces a previously registered service handler in place.
// Used by composed custom-DPs (e.g. Blind, which embeds Cover) to substitute
// a more specific handler for an inherited operation without tripping the
// duplicate-registration panic in [RegisterService]. Panics when the name
// is empty, the handler is nil, or no prior registration exists.
func (r *ServiceRegistry) OverrideService(name string, h ServiceHandler) {
	if name == "" {
		panic("payload: OverrideService called with empty name")
	}
	if h == nil {
		panic(fmt.Sprintf("payload: OverrideService %q called with nil handler", name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.funcs[name]; !ok {
		panic(fmt.Sprintf("payload: OverrideService %q called for unregistered method", name))
	}
	r.funcs[name] = h
}

// registerServiceWithArg is the shared implementation for RegisterService and
// RegisterServiceWithArg. scalarArgKey="" is treated as "value".
func (r *ServiceRegistry) registerServiceWithArg(name, scalarArgKey string, h ServiceHandler) {
	if name == "" {
		panic("payload: RegisterService called with empty name")
	}
	if h == nil {
		panic(fmt.Sprintf("payload: RegisterService %q called with nil handler", name))
	}
	if scalarArgKey == "" {
		scalarArgKey = "value"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.funcs[name]; dup {
		panic(fmt.Sprintf("payload: duplicate service method %q", name))
	}
	if r.funcs == nil {
		r.funcs = make(map[string]ServiceHandler)
		r.scalarArgs = make(map[string]string)
	}
	r.funcs[name] = h
	r.scalarArgs[name] = scalarArgKey
	r.names = append(r.names, name)
	// Rebuild the cached snapshot so ServiceMethodNames stays O(1).
	snap := make([]string, len(r.names))
	copy(snap, r.names)
	r.cached = snap

	// Propagate to the package-level aggregate so the MQTT bridge can
	// resolve the key by method name alone, without a Source instance.
	// Concurrent writes are safe for the whole process lifetime: the
	// scalar-arg key only ever depends on the method name, so repeated
	// writes across device-discovery cycles are idempotent no-ops after
	// the first one for a given method.
	globalScalarArgsMu.Lock()
	globalScalarArgs[name] = scalarArgKey
	globalScalarArgsMu.Unlock()
}

// ScalarArgKey returns the scalar-argument key registered for the given
// service method on this instance. Returns "value" when the method is
// unknown or was registered without an explicit key.
func (r *ServiceRegistry) ScalarArgKey(method string) string {
	r.mu.RLock()
	k := r.scalarArgs[method]
	r.mu.RUnlock()
	if k == "" {
		return "value"
	}
	return k
}

// ServiceMethodNames returns the registered method names in
// registration order. The snapshot is pre-built at [RegisterService]
// time and returned as a fresh copy — O(n) copy but O(1) preparation,
// avoiding a rebuild on every read call. Mirrors the cached approach in
// Py:321 (WeakKeyDictionary cache).
// Returns nil when no methods have been registered.
func (r *ServiceRegistry) ServiceMethodNames() []string {
	r.mu.RLock()
	snap := r.cached
	r.mu.RUnlock()
	if len(snap) == 0 {
		return nil
	}
	out := make([]string, len(snap))
	copy(out, snap)
	return out
}

// Invoke dispatches name with params and priority. Returns
// [ErrUnknownServiceMethod] (wrapped with the offending name) when
// name is not registered; otherwise returns whatever the handler
// returns.
func (r *ServiceRegistry) Invoke(ctx context.Context, name string,
	params map[string]any, priority hmenum.CommandPriority,
) error {
	r.mu.RLock()
	h, ok := r.funcs[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownServiceMethod, name)
	}
	return h(ctx, params, priority)
}
