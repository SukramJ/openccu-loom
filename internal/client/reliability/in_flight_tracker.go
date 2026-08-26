// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// InFlightTracker records values that are currently being sent to the CCU but
// for which no confirmation callback has arrived yet. It is distinct from
// [CommandTracker] (which records values after the send completes) so that a
// concurrent callback echo during the wire call still has a fallback reader.
//
// The dedup guarantee: if a second concurrent write arrives for the same key
// while a first write is in-flight, Stage returns the previously staged value
// and ok=true so the caller can optionally skip the duplicate wire call.
//
// Thread-safe: all methods are protected by a mutex.
type InFlightTracker struct {
	mu      sync.Mutex
	entries map[hmtypes.DataPointKey]any
}

// NewInFlightTracker returns an empty tracker.
func NewInFlightTracker() *InFlightTracker {
	return &InFlightTracker{
		entries: make(map[hmtypes.DataPointKey]any),
	}
}

// Stage records value for key as in-flight before the wire write begins.
// If a previous staging for the same key exists, it returns (previousValue,
// true) so the caller can detect a concurrent duplicate; otherwise returns
// (nil, false). The entry stays until [Clear] is called.
func (t *InFlightTracker) Stage(key hmtypes.DataPointKey, value any) (prev any, duplicate bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, duplicate = t.entries[key]
	t.entries[key] = value
	return prev, duplicate
}

// Clear removes the tracking entry for key. Must be called in a defer after
// Stage to guarantee cleanup regardless of wire success or failure.
func (t *InFlightTracker) Clear(key hmtypes.DataPointKey) {
	t.mu.Lock()
	delete(t.entries, key)
	t.mu.Unlock()
}

// IsInFlight reports whether key currently has a staged value.
func (t *InFlightTracker) IsInFlight(key hmtypes.DataPointKey) bool {
	t.mu.Lock()
	_, ok := t.entries[key]
	t.mu.Unlock()
	return ok
}

// Get returns the staged value for key and true when in-flight; nil and false
// when absent.
func (t *InFlightTracker) Get(key hmtypes.DataPointKey) (any, bool) {
	t.mu.Lock()
	v, ok := t.entries[key]
	t.mu.Unlock()
	return v, ok
}

// Size returns the number of currently staged entries.
func (t *InFlightTracker) Size() int {
	t.mu.Lock()
	n := len(t.entries)
	t.mu.Unlock()
	return n
}
