// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"errors"
	"sort"
	"sync"
)

// ErrCentralExists is returned when a central is registered twice with
// the same name.
var ErrCentralExists = errors.New("registry: central already registered")

// ErrCentralNotFound is returned on lookup of an unknown central.
var ErrCentralNotFound = errors.New("registry: central not found")

// Central is implemented by the concrete CentralUnit type. Kept here
// as an opaque handle so the registry does not depend on the full
// central package.
type Central any

// CentralRegistry is the daemon-wide lookup table of configured
// centrals, keyed by their operator-assigned name. Every northbound
// adapter reads from this registry; bootstrapping writes once.
type CentralRegistry struct {
	mu    sync.RWMutex
	items map[string]Central
}

// NewCentralRegistry returns an empty registry.
//
// loom:reachable:reason="constructed in daemon.go bootstrap to hold all CentralUnit instances; multi-CCU-safe by design"
func NewCentralRegistry() *CentralRegistry {
	return &CentralRegistry{items: make(map[string]Central)}
}

// Register adds a central under name. Returns [ErrCentralExists] if
// the name is already used.
func (r *CentralRegistry) Register(name string, central Central) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; exists {
		return ErrCentralExists
	}
	r.items[name] = central
	return nil
}

// Get returns the central for name.
func (r *CentralRegistry) Get(name string) (Central, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[name]
	if !ok {
		return nil, ErrCentralNotFound
	}
	return c, nil
}

// Names returns the registered central names in sorted order.
func (r *CentralRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for n := range r.items {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Remove deletes a central. Returns true when the entry existed.
func (r *CentralRegistry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[name]; !ok {
		return false
	}
	delete(r.items, name)
	return true
}

// Values returns every registered central in name-sorted order.
func (r *CentralRegistry) Values() []Central {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.items))
	for n := range r.items {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Central, 0, len(names))
	for _, n := range names {
		out = append(out, r.items[n])
	}
	return out
}

// Contains reports whether a central with the given name is registered.
// Mirrors Python's `name in CENTRAL_REGISTRY` semantics.
func (r *CentralRegistry) Contains(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.items[name]
	return ok
}

// Len returns the number of registered centrals. Mirrors Python's
// `len(CENTRAL_REGISTRY)` semantics.
func (r *CentralRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}
