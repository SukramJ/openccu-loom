// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// DeviceEntry is the registry's light-weight view of an active device.
// The full [model.Device] lives in the domain model; the registry only
// tracks identity and classification so coordinators don't need to
// import the model package to route events.
type DeviceEntry struct {
	Interface    hmenum.Interface
	Address      string
	Model        string
	Manufacturer hmenum.Manufacturer
	ProductGroup hmenum.ProductGroup
}

// Key returns the registry lookup key.
func (e DeviceEntry) Key() DeviceKey {
	return DeviceKey{Interface: e.Interface, Address: e.Address}
}

// DeviceKey locates a device in the registry.
type DeviceKey struct {
	Interface hmenum.Interface
	Address   string
}

// DeviceRegistry tracks the currently-known devices for one central.
type DeviceRegistry struct {
	mu    sync.RWMutex
	items map[DeviceKey]DeviceEntry
}

// NewDeviceRegistry returns an empty registry.
func NewDeviceRegistry() *DeviceRegistry {
	return &DeviceRegistry{items: make(map[DeviceKey]DeviceEntry)}
}

// Put inserts or replaces an entry.
func (r *DeviceRegistry) Put(e DeviceEntry) {
	r.mu.Lock()
	r.items[e.Key()] = e
	r.mu.Unlock()
}

// Get returns the entry for (iface, address).
func (r *DeviceRegistry) Get(iface hmenum.Interface, address string) (DeviceEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.items[DeviceKey{Interface: iface, Address: address}]
	return e, ok
}

// Remove deletes the entry and reports whether it was present.
func (r *DeviceRegistry) Remove(iface hmenum.Interface, address string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := DeviceKey{Interface: iface, Address: address}
	if _, ok := r.items[key]; !ok {
		return false
	}
	delete(r.items, key)
	return true
}

// List returns entries sorted by (interface, address).
func (r *DeviceRegistry) List() []DeviceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DeviceEntry, 0, len(r.items))
	for _, e := range r.items {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Interface != out[j].Interface {
			return out[i].Interface < out[j].Interface
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// Len reports the total device count.
func (r *DeviceRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Clear drops every entry from the registry. Used during shutdown and
// integration-test reset.
func (r *DeviceRegistry) Clear() {
	r.mu.Lock()
	r.items = make(map[DeviceKey]DeviceEntry)
	r.mu.Unlock()
}

// Has reports whether a device is registered for (iface, address).
func (r *DeviceRegistry) Has(iface hmenum.Interface, address string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.items[DeviceKey{Interface: iface, Address: address}]
	return ok
}

// Models returns the set of unique device model strings across all registered
// entries.
func (r *DeviceRegistry) Models() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{}, len(r.items))
	for _, e := range r.items {
		if e.Model != "" {
			seen[e.Model] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Addresses returns all known device addresses registered for iface, sorted.
func (r *DeviceRegistry) Addresses(iface hmenum.Interface) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for k := range r.items {
		if k.Interface == iface {
			out = append(out, k.Address)
		}
	}
	sort.Strings(out)
	return out
}
