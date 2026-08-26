// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package devicedetails carries the per-CCU runtime metadata that
// enriches devices and channels with operator-assigned names, room
// memberships, function tags, ISE-IDs, and interface mapping. It is
// The openccu-loom equivalent.
// `DeviceDetailsCache` (`store/dynamic/details.py`).
//
// The cache is populated by the periodic device-details load:
// names from `Device.listAllDetail`, rooms from `Room.getChannelIDs`
// (resolved via Room.getName), functions from
// `Function.getChannelIDs`, ISE-IDs from the same listAllDetail
// payload. Lookups are constant-time and locked with a single
// RWMutex. It is read at ingest time by the device pipeline, which
// bakes the result into naming.NameData; the north-bound surfaces read
// that baked model rather than this cache.
package devicedetails

import (
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Cache holds the runtime device-detail metadata for one Unit.
//
// - `names`        — channel/device address → operator-assigned name. -
// `iseIDs`       — channel/device address → CCU ISE-ID (used for
// Channel.setName / Device.setName JSON-RPC calls). - `interfaces`   —
// channel address → interface (HmIP-RF, BidCos-RF, …). - `channelRooms` —
// channel address → set of room labels. - `deviceRooms`  — device address →
// aggregated set of room labels (union of all channel-room sets per device).
// - `functions`    — channel/device address → set of function tags
// ("Heizung", "Sicherheit", …).
//
// All fields are addressed by string keys (CCU addresses use upper-case
// alphanumeric prefixes; treat them as opaque).
type Cache struct {
	mu sync.RWMutex

	names        map[string]string
	iseIDs       map[string]int
	interfaces   map[string]hmenum.Interface
	channelRooms map[string]map[string]struct{} // channel → set
	deviceRooms  map[string]map[string]struct{} // device  → set
	functions    map[string]map[string]struct{} // address → set

	refreshedAt time.Time
}

// New returns an empty cache.
func New() *Cache {
	return &Cache{
		names:        make(map[string]string),
		iseIDs:       make(map[string]int),
		interfaces:   make(map[string]hmenum.Interface),
		channelRooms: make(map[string]map[string]struct{}),
		deviceRooms:  make(map[string]map[string]struct{}),
		functions:    make(map[string]map[string]struct{}),
	}
}

// AddName registers the operator-assigned name for an address.
func (c *Cache) AddName(address, name string) {
	c.mu.Lock()
	c.names[address] = name
	c.mu.Unlock()
}

// AddAddressISEID registers the CCU ISE-ID for an address.
func (c *Cache) AddAddressISEID(address string, iseID int) {
	c.mu.Lock()
	c.iseIDs[address] = iseID
	c.mu.Unlock()
}

// AddInterface registers the interface tag for a channel address.
func (c *Cache) AddInterface(address string, iface hmenum.Interface) {
	c.mu.Lock()
	c.interfaces[address] = iface
	c.mu.Unlock()
}

// AddChannelRoom adds `room` to the channel's room set. Mirrors the
// Aggregation step
// (`details.py:169-176`). Idempotent — repeated calls with the same
// pair are no-ops.
func (c *Cache) AddChannelRoom(channelAddress, room string) {
	if room == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	set, ok := c.channelRooms[channelAddress]
	if !ok {
		set = make(map[string]struct{})
		c.channelRooms[channelAddress] = set
	}
	set[room] = struct{}{}
	// Maintain the device-rooms aggregate. Mirrors
	// `_prepare_device_rooms` (`details.py:178-198`): a device is in
	// every room any of its channels is in.
	deviceAddr := deviceAddressFromChannel(channelAddress)
	if deviceAddr != "" {
		dset, ok := c.deviceRooms[deviceAddr]
		if !ok {
			dset = make(map[string]struct{})
			c.deviceRooms[deviceAddr] = dset
		}
		dset[room] = struct{}{}
	}
}

// AddFunction adds `function` to the address's function set.
func (c *Cache) AddFunction(address, function string) {
	if function == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	set, ok := c.functions[address]
	if !ok {
		set = make(map[string]struct{})
		c.functions[address] = set
	}
	set[function] = struct{}{}
}

// MarkRefreshed stamps the cache's `refreshedAt` timestamp. Callers
// invoke this once a successful load pass completes — readers consult
// [Cache.RefreshedAt] to compute cache-age decisions.
func (c *Cache) MarkRefreshed(at time.Time) {
	c.mu.Lock()
	c.refreshedAt = at
	c.mu.Unlock()
}

// RefreshedAt returns the timestamp of the most recent [MarkRefreshed]
// call, or the zero [time.Time] when the cache has never been loaded.
func (c *Cache) RefreshedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.refreshedAt
}

// GetName returns the operator-assigned name or the empty string when
// none is cached. Mirrors `details.py:119-121`.
func (c *Cache) GetName(address string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.names[address]
}

// GetAddressID returns the cached ISE-ID or 0 when none is cached.
// Mirrors `details.py:97-99`.
func (c *Cache) GetAddressID(address string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.iseIDs[address]
}

// GetInterface returns the cached interface or [hmenum.InterfaceBidCosRF]
// as a fallback.
func (c *Cache) GetInterface(address string) hmenum.Interface {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if iface, ok := c.interfaces[address]; ok {
		return iface
	}
	return hmenum.InterfaceBidCosRF
}

// GetChannelRooms returns a copy of the room set for a channel
// address. Empty when no rooms are cached. Mirrors
// `details.py:101-103`.
func (c *Cache) GetChannelRooms(channelAddress string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sortedSet(c.channelRooms[channelAddress])
}

// GetDeviceRooms returns the aggregated room set for the device.
// Mirrors `details.py:105-107`.
func (c *Cache) GetDeviceRooms(deviceAddress string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sortedSet(c.deviceRooms[deviceAddress])
}

// GetFunctions returns the function tag set for an address. Mirrors
// the underlying state of `details.py:109-113`'s
// `get_function_text` — callers join with comma if needed.
func (c *Cache) GetFunctions(address string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sortedSet(c.functions[address])
}

// GetFunctionText returns the comma-joined function string, or the
// empty string when none is cached. Direct mirror of
// `details.py:109-113`.
func (c *Cache) GetFunctionText(address string) string {
	tags := c.GetFunctions(address)
	if len(tags) == 0 {
		return ""
	}
	return strings.Join(tags, ",")
}

// DeviceChannelISEIDs returns a copy of the ise-id map. Mirrors
// the `device_channel_ise_ids` DelegatedProperty in
// `details.py:75`. Used by the configui to retrieve IDs for
// Channel.setName / Device.setName calls.
func (c *Cache) DeviceChannelISEIDs() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int, len(c.iseIDs))
	maps.Copy(out, c.iseIDs)
	return out
}

// ReplaceWith swaps in the tables of a freshly built cache and stamps the
// refresh timestamp, in one lock hold. It is the commit step of the loader's
// build-into-a-staging-cache pass: the live cache keeps the previous
// generation for the whole duration of the CCU round-trips, so a reader that
// lands mid-refresh — the device pipeline resolving a name and an ISE-ID for
// a device being ingested, above all — sees the last good generation rather
// than an empty or half-filled one, and a failed round-trip leaves the
// previous generation in place because the commit never runs.
//
// src is consumed: its tables are moved, not copied, so callers must not
// keep using it afterwards.
func (c *Cache) ReplaceWith(src *Cache, at time.Time) {
	if src == nil {
		return
	}
	src.mu.Lock()
	names, iseIDs, interfaces := src.names, src.iseIDs, src.interfaces
	channelRooms, deviceRooms, functions := src.channelRooms, src.deviceRooms, src.functions
	src.mu.Unlock()

	c.mu.Lock()
	c.names, c.iseIDs, c.interfaces = names, iseIDs, interfaces
	c.channelRooms, c.deviceRooms, c.functions = channelRooms, deviceRooms, functions
	c.refreshedAt = at
	c.mu.Unlock()
}

// Clear empties every internal table and resets the refreshed
// timestamp. Mirrors `details.py:89-95`.
func (c *Cache) Clear() {
	c.mu.Lock()
	c.names = make(map[string]string)
	c.iseIDs = make(map[string]int)
	c.interfaces = make(map[string]hmenum.Interface)
	c.channelRooms = make(map[string]map[string]struct{})
	c.deviceRooms = make(map[string]map[string]struct{})
	c.functions = make(map[string]map[string]struct{})
	c.refreshedAt = time.Time{}
	c.mu.Unlock()
}

// RemoveDevice clears every entry tied to the device address (the device row
// plus every channel of the form `<device>:<n>`).
//
// `channels` is the explicit list of channel addresses the caller already
// knows about (typically obtained from device.Device.Channels()). When empty,
// only the bare device row is removed; channel rows belonging to devices not
// in `channels` would stay (the caller should pass the full list at removal
// time).
func (c *Cache) RemoveDevice(deviceAddress string, channels []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.names, deviceAddress)
	delete(c.interfaces, deviceAddress)
	delete(c.iseIDs, deviceAddress)
	delete(c.deviceRooms, deviceAddress)
	delete(c.functions, deviceAddress)
	for _, ch := range channels {
		delete(c.names, ch)
		delete(c.interfaces, ch)
		delete(c.iseIDs, ch)
		delete(c.channelRooms, ch)
		delete(c.functions, ch)
	}
}

// HasName reports whether a name has been cached for the given
// address. Helpful when the caller needs to distinguish "no name set"
// from "empty string set". Used by the auto-generated fallback chain
// in [internal/model/device].
func (c *Cache) HasName(address string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.names[address]
	return ok
}

// IsEmpty reports whether the cache has no entries at all.
func (c *Cache) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.names) == 0 &&
		len(c.iseIDs) == 0 &&
		len(c.interfaces) == 0 &&
		len(c.channelRooms) == 0 &&
		len(c.deviceRooms) == 0 &&
		len(c.functions) == 0
}

// deviceAddressFromChannel extracts "VCU…" from "VCU…:N". Returns the
// input unchanged when no colon is present (the address is already
// device-level).
func deviceAddressFromChannel(addr string) string {
	if i := strings.IndexByte(addr, ':'); i > 0 {
		return addr[:i]
	}
	return addr
}

// sortedSet returns a stable-ordered copy of the string set. Empty or
// nil sets yield nil (so callers can JSON-marshal without surprise).
func sortedSet(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
