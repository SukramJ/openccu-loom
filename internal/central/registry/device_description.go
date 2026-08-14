// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package registry hosts the in-memory registries every central keeps:
// device descriptions, paramsets, and the map of active devices. All
// types are safe for concurrent use.
package registry

import (
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DeviceDescriptionKey locates a description inside the registry.
type DeviceDescriptionKey struct {
	Interface hmtypes.WireInterfaceID
	Address   string
}

// DescriptionSink mirrors registry mutations into a persistence
// backend so descriptions survive a daemon restart. Implementations
// must be safe for concurrent use; they are invoked synchronously on
// the mutating goroutine (coordinator callback paths), outside the
// registry lock, and must treat persistence as best-effort.
type DescriptionSink interface {
	// PutDescription persists the normalised description.
	PutDescription(iface hmtypes.WireInterfaceID, desc hmproto.DeviceDescription)
	// DeleteDescription removes the persisted description.
	DeleteDescription(iface hmtypes.WireInterfaceID, address string)
}

// DeviceDescriptionRegistry caches device descriptions per interface.
// It stores the normalised form so callers can hash without re-work.
type DeviceDescriptionRegistry struct {
	mu    sync.RWMutex
	items map[DeviceDescriptionKey]hmproto.DeviceDescription
	sink  DescriptionSink
}

// NewDeviceDescriptionRegistry returns an empty registry.
func NewDeviceDescriptionRegistry() *DeviceDescriptionRegistry {
	return &DeviceDescriptionRegistry{items: make(map[DeviceDescriptionKey]hmproto.DeviceDescription)}
}

// SetSink installs the persistence sink; nil detaches it. Install the
// sink AFTER hydrating the registry from the persistent store —
// loading through Put with an attached sink would write every row
// straight back.
func (r *DeviceDescriptionRegistry) SetSink(s DescriptionSink) {
	r.mu.Lock()
	r.sink = s
	r.mu.Unlock()
}

// Put stores desc under (iface, desc.Address). The description is
// normalised before storage.
func (r *DeviceDescriptionRegistry) Put(iface hmtypes.WireInterfaceID, desc hmproto.DeviceDescription) {
	normalised := hmproto.NormalizeDevice(desc)
	r.mu.Lock()
	r.items[DeviceDescriptionKey{Interface: iface, Address: normalised.Address}] = normalised
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink.PutDescription(iface, normalised)
	}
}

// Get returns the stored description and reports whether it exists.
func (r *DeviceDescriptionRegistry) Get(iface hmtypes.WireInterfaceID, address string) (hmproto.DeviceDescription, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[DeviceDescriptionKey{Interface: iface, Address: address}]
	return d, ok
}

// Delete removes a description. Returns true when the entry was present.
func (r *DeviceDescriptionRegistry) Delete(iface hmtypes.WireInterfaceID, address string) bool {
	r.mu.Lock()
	key := DeviceDescriptionKey{Interface: iface, Address: address}
	_, ok := r.items[key]
	if ok {
		delete(r.items, key)
	}
	sink := r.sink
	r.mu.Unlock()
	if ok && sink != nil {
		sink.DeleteDescription(iface, address)
	}
	return ok
}

// All returns a snapshot of descriptions for iface, ordered by address.
func (r *DeviceDescriptionRegistry) All(iface hmtypes.WireInterfaceID) []hmproto.DeviceDescription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]hmproto.DeviceDescription, 0, len(r.items))
	for k := range r.items {
		if k.Interface == iface {
			out = append(out, r.items[k])
		}
	}
	return out
}

// Len returns the total number of stored descriptions across all
// interfaces.
func (r *DeviceDescriptionRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// =============================================================================
// DeviceDescriptionCache access methods
//
// These methods mirror the
// query surface (store/persistent/device.py:116-172).
// =============================================================================

// GetAddresses returns the set of all cached addresses for iface,
// or all addresses when iface is the zero value.
func (r *DeviceDescriptionRegistry) GetAddresses(iface hmtypes.WireInterfaceID) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for k := range r.items {
		if iface == "" || k.Interface == iface {
			out = append(out, k.Address)
		}
	}
	return out
}

// GetDeviceWithChannels returns the device description plus all channel
// descriptions for (iface, deviceAddress). If the device description
// is not found the returned map is empty.
func (r *DeviceDescriptionRegistry) GetDeviceWithChannels(iface hmtypes.WireInterfaceID, deviceAddress string) map[string]hmproto.DeviceDescription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]hmproto.DeviceDescription)
	dev, ok := r.items[DeviceDescriptionKey{Interface: iface, Address: deviceAddress}]
	if !ok {
		return out
	}
	out[deviceAddress] = dev
	for _, chAddr := range dev.Children {
		if chAddr == "" {
			continue
		}
		if ch, ok := r.items[DeviceDescriptionKey{Interface: iface, Address: chAddr}]; ok {
			out[chAddr] = ch
		}
	}
	return out
}

// GetInterfaceIDs returns the distinct interface values that have at
// least one cached description.
func (r *DeviceDescriptionRegistry) GetInterfaceIDs() []hmtypes.WireInterfaceID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[hmtypes.WireInterfaceID]struct{})
	for k := range r.items {
		seen[k.Interface] = struct{}{}
	}
	out := make([]hmtypes.WireInterfaceID, 0, len(seen))
	for iface := range seen {
		out = append(out, iface)
	}
	return out
}

// GetModel returns the TYPE field of the device description for
// deviceAddress, searching across all interfaces.
// Returns the empty string when not found.
func (r *DeviceDescriptionRegistry) GetModel(deviceAddress string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for k := range r.items {
		if k.Address == deviceAddress && r.items[k].Parent == "" {
			// Parent is empty only for device-level descriptions.
			return r.items[k].Type
		}
	}
	return ""
}

// HasDeviceDescriptions reports whether any descriptions are cached for iface.
func (r *DeviceDescriptionRegistry) HasDeviceDescriptions(iface hmtypes.WireInterfaceID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for k := range r.items {
		if k.Interface == iface {
			return true
		}
	}
	return false
}
