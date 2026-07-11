// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// ModelRegistry holds the fully-instantiated [*device.Device] graph
// keyed by address. It is separate from [DeviceRegistry] (which is
// the light-weight routing table) so the model layer can evolve
// without churning the low-level entry struct.
type ModelRegistry struct {
	mu    sync.RWMutex
	items map[string]*device.Device
}

// NewModelRegistry returns an empty registry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{items: make(map[string]*device.Device)}
}

// Put stores d under its device address.
func (r *ModelRegistry) Put(d *device.Device) {
	if d == nil || d.Address == "" {
		return
	}
	r.mu.Lock()
	r.items[d.Address] = d
	r.mu.Unlock()
}

// Get returns the device for address.
func (r *ModelRegistry) Get(address string) (*device.Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[address]
	return d, ok
}

// List returns every registered device sorted by address.
func (r *ModelRegistry) List() []*device.Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*device.Device, 0, len(r.items))
	for _, d := range r.items {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// IdentifyChannel scans every registered device for the channel referenced by
// text — a CCU system-variable or program name — and returns the owning device
// and channel. Devices are visited in sorted-address order (via List) so the
// result is deterministic when more than one device could match. Returns
// (nil, nil, false) when text matches no channel.
//
// Mirrors the Python reference's `central/device_registry.py:187-203`
// (DeviceRegistry.identify_channel): the per-device decision is delegated to
// [device.Device.IdentifyChannel].
func (r *ModelRegistry) IdentifyChannel(text string) (*device.Device, *device.Channel, bool) {
	if text == "" {
		return nil, nil, false
	}
	for _, d := range r.List() {
		if ch := d.IdentifyChannel(text); ch != nil {
			return d, ch, true
		}
	}
	return nil, nil, false
}

// Remove drops the device and reports whether one existed.
func (r *ModelRegistry) Remove(address string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.items[address]; ok {
		delete(r.items, address)
		d.NotifyRemoved()
		return true
	}
	return false
}

// Len reports the total device count.
func (r *ModelRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}
