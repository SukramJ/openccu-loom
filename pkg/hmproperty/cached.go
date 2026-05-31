// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmproperty provides the Cached[T] generic type — a lightweight,
// concurrency-safe cached property with explicit invalidation.
//
// # Background
//
// expensive computed properties (enabled_default, dpk, quantity,
// value_behavior, _enabled_by_channel_operation_mode, requires_polling,
// service_method_names, service_methods).  Invalidation happens implicitly
// whenever a setter is called or UpdateDescriptor / MarkForcedSensor /
// MarkUnIgnored runs.
//
// This package offers the same pattern for Go without reflection.  Every
// cached property on a DataPoint[T] or BaseDataPointFields can be declared
// as a Cached[T] field; the owning struct calls Invalidate() in its
// mutating methods and Get(loader) in its readers.
//
// # Example usage
//
// A DataPoint[T] that wants to cache EnabledByDefault:
//
//	type DataPoint[T any] struct {
//	    BaseDataPointFields
//	    enabledByDefaultCache hmproperty.Cached[bool]
//	    // ...
//	}
//
//	func (d *DataPoint[T]) EnabledByDefault() bool {
//	    return d.enabledByDefaultCache.Get(func() bool {
//	        return computeEnabledByDefault(d.BaseDataPointFields)
//	    })
//	}
//
//	func (d *DataPoint[T]) SetForcedUsage(u hmenum.Usage) {
//	    d.BaseDataPointFields.SetForcedUsage(u)
//	    d.enabledByDefaultCache.Invalidate()
//	}
//
// # Concurrency
//
// Cached[T] is safe for concurrent use.  Multiple goroutines may call Get
// simultaneously; the loader is called exactly once per valid epoch.  An
// Invalidate concurrent with Get is safe: the invalidation may or may not
// be visible to an in-flight Get — the contract is eventual consistency,
// Not strict serialization. This matches
// semantics, which also does not guard against concurrent access.
package hmproperty

import "sync"

// Cached is a generic cached property. The zero value is valid and starts
// in the invalid (not-yet-loaded) state.
//
// T may be any type, including interfaces and pointers.
type Cached[T any] struct {
	mu    sync.RWMutex
	value T
	valid bool
}

// Get returns the cached value. If the cache is invalid (after construction
// or after Invalidate), loader is called exactly once to populate it.
// Subsequent calls return the cached value until the next Invalidate.
//
// loader must not call Get on the same Cached[T] instance (deadlock).
func (c *Cached[T]) Get(loader func() T) T {
	// Fast path: already valid.
	c.mu.RLock()
	if c.valid {
		v := c.value
		c.mu.RUnlock()

		return v
	}
	c.mu.RUnlock()

	// Slow path: promote to write lock and compute.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-check after acquiring write lock (another goroutine may have
	// populated the cache between our RUnlock and Lock).
	if c.valid {
		return c.value
	}

	c.value = loader()
	c.valid = true

	return c.value
}

// Set stores v as the cached value and marks the cache as valid.
// The loader passed to future Get calls will not be invoked until the
// next Invalidate.
func (c *Cached[T]) Set(v T) {
	c.mu.Lock()
	c.value = v
	c.valid = true
	c.mu.Unlock()
}

// Invalidate marks the cache as invalid. The next Get call will invoke its
// loader to recompute the value.
func (c *Cached[T]) Invalidate() {
	c.mu.Lock()
	c.valid = false
	var zero T
	c.value = zero
	c.mu.Unlock()
}

// IsValid reports whether the cache currently holds a valid value.
// This is a point-in-time snapshot; the state may change immediately after
// this call returns.
func (c *Cached[T]) IsValid() bool {
	c.mu.RLock()
	v := c.valid
	c.mu.RUnlock()

	return v
}
