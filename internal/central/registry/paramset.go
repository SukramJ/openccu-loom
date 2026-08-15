// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/store/patches"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ParamsetKey locates a paramset inside the registry.
type ParamsetKey struct {
	Interface      hmtypes.WireInterfaceID
	ChannelAddress string
	ParamsetKey    hmenum.ParamsetKey
}

// ParamsetSink mirrors registry mutations into a persistence backend
// so paramset descriptions survive a daemon restart. Implementations
// must be safe for concurrent use; they run synchronously on the
// mutating goroutine, outside the registry lock, and must treat
// persistence as best-effort.
type ParamsetSink interface {
	// PutParamset persists the normalised (and patched) paramset.
	PutParamset(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey, ps hmproto.Paramset)
	// DeleteChannelParamsets removes every persisted paramset of the channel.
	DeleteChannelParamsets(iface hmtypes.WireInterfaceID, channelAddress string)
}

// ParamsetRegistry caches paramset descriptions per channel/key.
//
// Add() applies normalisation + patches at ingestion time.
// Secondary index (addressParamCache) enables
// get_channel_addresses_by_paramset_key, is_in_multiple_channels, etc.
type ParamsetRegistry struct {
	mu    sync.RWMutex
	items map[ParamsetKey]hmproto.Paramset
	sink  ParamsetSink

	// patchRegistry applies device-specific corrections at ingestion.
	// When nil the old normalise-only path is used (backward compat).
	patchRegistry *patches.Registry
}

// SetSink installs the persistence sink; nil detaches it. Install the
// sink AFTER hydrating the registry from the persistent store —
// loading through Put with an attached sink would write every row
// straight back.
func (r *ParamsetRegistry) SetSink(s ParamsetSink) {
	r.mu.Lock()
	r.sink = s
	r.mu.Unlock()
}

// NewParamsetRegistry returns an empty registry without a patch registry.
//
// loom:reachable:reason="called by NewParamsetRegistryWithPatches and directly in tests; production always uses the WithPatches variant"
func NewParamsetRegistry() *ParamsetRegistry {
	return &ParamsetRegistry{
		items: make(map[ParamsetKey]hmproto.Paramset),
	}
}

// NewParamsetRegistryWithPatches returns a registry that applies patches at
// ingestion time via Add().
func NewParamsetRegistryWithPatches(pr *patches.Registry) *ParamsetRegistry {
	r := NewParamsetRegistry()
	r.patchRegistry = pr
	return r
}

// ApplyPatches runs the configured paramset patches over `ps` in place. Used
// by the device-hydration pipeline to make sure DP construction sees patched
// MIN/MAX/etc. values even when the paramset is not stored in the registry.
// No-op when no patch registry is wired.
func (r *ParamsetRegistry) ApplyPatches(deviceType, channelAddress string, psKey hmenum.ParamsetKey, ps hmproto.Paramset) int {
	if r.patchRegistry == nil || ps == nil {
		return 0
	}
	return r.patchRegistry.ApplyParamset(deviceType, channelAddress, psKey, ps)
}

// Add stores ps under the composite key, normalises it, applies any
// device-type–specific patches, and updates the secondary index.
// The deviceType parameter carries the device TYPE field from
// DeviceDescription (e.g. "HM-CC-VG-1") for patch matching.
func (r *ParamsetRegistry) Add(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey, ps hmproto.Paramset, deviceType string) {
	normalised := hmproto.NormalizeParamset(ps)
	if r.patchRegistry != nil {
		r.patchRegistry.ApplyParamset(deviceType, channelAddress, psKey, normalised)
	}
	key := ParamsetKey{Interface: iface, ChannelAddress: channelAddress, ParamsetKey: psKey}
	r.mu.Lock()
	r.items[key] = normalised
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink.PutParamset(iface, channelAddress, psKey, normalised)
	}
}

// Put stores ps under the composite key. Paramset is normalised before
// storage so hashes computed after Put stay stable.
// For new call sites prefer Add() which also applies patches.
func (r *ParamsetRegistry) Put(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey, ps hmproto.Paramset) {
	normalised := hmproto.NormalizeParamset(ps)
	r.mu.Lock()
	r.items[ParamsetKey{Interface: iface, ChannelAddress: channelAddress, ParamsetKey: psKey}] = normalised
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink.PutParamset(iface, channelAddress, psKey, normalised)
	}
}

// Get returns the paramset and reports whether it exists.
func (r *ParamsetRegistry) Get(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey) (hmproto.Paramset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps, ok := r.items[ParamsetKey{Interface: iface, ChannelAddress: channelAddress, ParamsetKey: psKey}]
	return ps, ok
}

// GetChannelParamsetDescriptions returns all paramsets stored for a specific
// channel, keyed by ParamsetKey. Returns an empty map when the channel is
// unknown on the given interface.
//
// get_channel_paramset_descriptions().
func (r *ParamsetRegistry) GetChannelParamsetDescriptions(iface hmtypes.WireInterfaceID, channelAddress string) map[hmenum.ParamsetKey]hmproto.Paramset {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[hmenum.ParamsetKey]hmproto.Paramset)
	for k, ps := range r.items {
		if k.Interface == iface && k.ChannelAddress == channelAddress {
			out[k.ParamsetKey] = ps
		}
	}
	return out
}

// GetParameterData returns the descriptor for a single parameter on a specific
// channel + paramset. ok is false when any part of the path is absent.
//
// get_parameter_data().
func (r *ParamsetRegistry) GetParameterData(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey, parameter string) (hmproto.ParameterData, bool) {
	ps, ok := r.Get(iface, channelAddress, psKey)
	if !ok {
		return hmproto.ParameterData{}, false
	}
	pd, ok := ps[parameter]
	return pd, ok
}

// GetParamsetKeys returns the paramset keys available for a given channel.
// Returns an empty tuple when the channel has no stored paramsets.
//
// get_paramset_keys().
func (r *ParamsetRegistry) GetParamsetKeys(iface hmtypes.WireInterfaceID, channelAddress string) []hmenum.ParamsetKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var keys []hmenum.ParamsetKey
	for k := range r.items {
		if k.Interface == iface && k.ChannelAddress == channelAddress {
			keys = append(keys, k.ParamsetKey)
		}
	}
	return keys
}

// GetChannelAddressesByParamsetKey returns a map of ParamsetKey → channel
// addresses that belong to deviceAddress (prefix match) and expose that
// paramset key on the given interface.
//
// get_channel_addresses_by_paramset_key().
func (r *ParamsetRegistry) GetChannelAddressesByParamsetKey(iface hmtypes.WireInterfaceID, deviceAddress string) map[hmenum.ParamsetKey][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[hmenum.ParamsetKey][]string)
	for k := range r.items {
		if k.Interface != iface {
			continue
		}
		// channel addresses start with deviceAddress followed by ':' or equal it.
		if !isChannelOf(k.ChannelAddress, deviceAddress) {
			continue
		}
		out[k.ParamsetKey] = append(out[k.ParamsetKey], k.ChannelAddress)
	}
	return out
}

// HasInterfaceID reports whether any paramset is stored for interfaceID.
//
// has_interface_id().
func (r *ParamsetRegistry) HasInterfaceID(iface hmtypes.WireInterfaceID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for k := range r.items {
		if k.Interface == iface {
			return true
		}
	}
	return false
}

// HasParameter reports whether parameter is declared on a specific channel +
// paramset. Returns false for any missing part of the path.
//
// has_parameter().
func (r *ParamsetRegistry) HasParameter(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey, parameter string) bool {
	_, ok := r.GetParameterData(iface, channelAddress, psKey, parameter)
	return ok
}

// Delete removes a paramset entry. Returns true when it existed.
func (r *ParamsetRegistry) Delete(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := ParamsetKey{Interface: iface, ChannelAddress: channelAddress, ParamsetKey: psKey}
	if _, ok := r.items[key]; !ok {
		return false
	}
	delete(r.items, key)
	return true
}

// DeleteChannel removes every paramset bound to the given channel and
// cleans up the secondary index entries for that channel.
func (r *ParamsetRegistry) DeleteChannel(iface hmtypes.WireInterfaceID, channelAddress string) {
	r.mu.Lock()
	for k := range r.items {
		if k.Interface == iface && k.ChannelAddress == channelAddress {
			delete(r.items, k)
		}
	}
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink.DeleteChannelParamsets(iface, channelAddress)
	}
}

// Len reports the total number of paramsets stored.
func (r *ParamsetRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// ---------- internal helpers ----------

// isChannelOf reports whether channelAddress belongs to deviceAddress.
// "ABC:1" belongs to "ABC"; "ABC" also belongs to "ABC".
func isChannelOf(channelAddress, deviceAddress string) bool {
	if channelAddress == deviceAddress {
		return true
	}
	return len(channelAddress) > len(deviceAddress)+1 &&
		channelAddress[:len(deviceAddress)] == deviceAddress &&
		channelAddress[len(deviceAddress)] == ':'
}
