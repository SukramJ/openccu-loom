// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty

import "fmt"

// Resolver is implemented by any object that can resolve a named field
// path to a typed value. The most common implementation reads a Go
// struct field by name via reflection.
type Resolver interface {
	// Resolve returns the value for the given field path.
	// Returns (value, true) on success, (zero, false) when the path
	// does not exist.
	Resolve(path string) (any, bool)
}

// DelegatedProperty is a read-through property that fetches its
// value from a [Resolver] (typically another struct) every time Value
// is called. This is the Go equivalent of Python's DelegatedProperty
// descriptor, which attaches a read-only property to a class that
// transparently reads from a referenced object.
//
// Usage:
//
//	dp := hmproperty.Delegated[bool](resolver, "available")
//	available := dp.Value() // reads resolver.Resolve("available")
type DelegatedProperty[T any] struct {
	resolver Resolver
	path     string
}

// Delegated constructs a [DelegatedProperty][T] for the given resolver
// and field path. If resolver is nil every call to Value returns the
// zero value of T.
//
// loom:reachable:reason="used by device-profile paramset-config and MQTT entity descriptions to resolve delegated property paths"
func Delegated[T any](resolver Resolver, path string) DelegatedProperty[T] {
	return DelegatedProperty[T]{resolver: resolver, path: path}
}

// Value resolves the delegated path and returns it as T. When the
// resolver returns no value, or the resolved value is not assignable to
// T, the zero value of T is returned.
func (d DelegatedProperty[T]) Value() T {
	if d.resolver == nil {
		var zero T
		return zero
	}
	raw, ok := d.resolver.Resolve(d.path)
	if !ok || raw == nil {
		var zero T
		return zero
	}
	v, ok := raw.(T)
	if !ok {
		var zero T
		return zero
	}
	return v
}

// IsSet reports whether the delegated resolver currently has a value
// for the path (i.e. Resolve returns ok=true).
func (d DelegatedProperty[T]) IsSet() bool {
	if d.resolver == nil {
		return false
	}
	_, ok := d.resolver.Resolve(d.path)
	return ok
}

// String returns a human-readable representation including the path and
// current value, useful for debugging.
func (d DelegatedProperty[T]) String() string {
	return fmt.Sprintf("DelegatedProperty{path=%q, value=%v}", d.path, d.Value())
}

// --------------------------------------------------------------------------
// ConstResolver — a Resolver that always returns the same constant value.
// Useful in tests and for properties that should always return a fixed value,
// (e.g., "always returns 1.0" or "always returns False").
// --------------------------------------------------------------------------

// ConstResolver implements [Resolver] by returning a fixed value for
// every path. Zero-value fields in the map are returned as the provided
// default.
type ConstResolver map[string]any

// Resolve implements [Resolver]. Returns (value, true) when path exists
// in the map, otherwise (nil, false).
func (r ConstResolver) Resolve(path string) (any, bool) {
	v, ok := r[path]
	return v, ok
}
