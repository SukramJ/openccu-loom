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

	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

	// refs holds every taxonomy node an address is directly assigned to, in
	// every enum; deviceRefs aggregates a device's own refs with those of
	// all its channels, as deviceRooms does for room names. tax is the
	// taxonomy the refs point into.
	refs       map[string]map[taxonomy.Ref]struct{}
	deviceRefs map[string]map[taxonomy.Ref]struct{}
	tax        *taxonomy.Taxonomy

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
		refs:         make(map[string]map[taxonomy.Ref]struct{}),
		deviceRefs:   make(map[string]map[taxonomy.Ref]struct{}),
	}
}

// AddRef assigns address to the taxonomy node r. The device the address
// belongs to (or the device itself) aggregates the ref as well. Idempotent.
func (c *Cache) AddRef(address string, r taxonomy.Ref) {
	if address == "" || r.Path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	addRef(c.refs, address, r)
	if dev := hmtypes.DeviceAddress(address); dev != "" {
		addRef(c.deviceRefs, dev, r)
	}
}

func addRef(m map[string]map[taxonomy.Ref]struct{}, key string, r taxonomy.Ref) {
	set, ok := m[key]
	if !ok {
		set = make(map[taxonomy.Ref]struct{})
		m[key] = set
	}
	set[r] = struct{}{}
}

// SetTaxonomy records the taxonomy the refs point into.
func (c *Cache) SetTaxonomy(t *taxonomy.Taxonomy) {
	c.mu.Lock()
	c.tax = t
	c.mu.Unlock()
}

// Taxonomy returns the taxonomy snapshot, or nil before the first load.
func (c *Cache) Taxonomy() *taxonomy.Taxonomy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tax
}

// Refs returns the taxonomy nodes address is directly assigned to, sorted by
// their string form. Empty when none.
func (c *Cache) Refs(address string) []taxonomy.Ref {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sortedRefs(c.refs[address])
}

// DeviceRefs returns the union of the device's own refs and those of every
// channel, sorted by their string form.
func (c *Cache) DeviceRefs(deviceAddress string) []taxonomy.Ref {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sortedRefs(c.deviceRefs[deviceAddress])
}

// Assignments returns the nodes address is directly assigned to with their
// display names, sorted by reference.
func (c *Cache) Assignments(address string) []taxonomy.Assignment {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.assignmentsLocked(sortedRefs(c.refs[address]))
}

// DeviceAssignments is [Cache.Assignments] over [Cache.DeviceRefs].
func (c *Cache) DeviceAssignments(deviceAddress string) []taxonomy.Assignment {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.assignmentsLocked(sortedRefs(c.deviceRefs[deviceAddress]))
}

func (c *Cache) assignmentsLocked(refs []taxonomy.Ref) []taxonomy.Assignment {
	if len(refs) == 0 {
		return nil
	}
	out := make([]taxonomy.Assignment, len(refs))
	for i, r := range refs {
		out[i] = taxonomy.Assignment{Ref: r}
		if n, ok := c.tax.Node(r); ok {
			out[i].Name = n.Name
		}
	}
	return out
}

func sortedRefs(set map[taxonomy.Ref]struct{}) []taxonomy.Ref {
	if len(set) == 0 {
		return nil
	}
	out := make([]taxonomy.Ref, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// FlatEntry is one node of a flat enum together with the member ids the
// system lists for it (on a CCU: channel ISE-IDs).
type FlatEntry struct {
	ID        string
	Name      string
	MemberIDs []string
}

// FlatEnum is one flat enum for [Cache.ApplyFlatTaxonomy].
type FlatEnum struct {
	ID      taxonomy.EnumID
	Names   map[string]string
	Entries []FlatEntry
}

// CCUFlatEnums declares a CCU's two taxonomies — rooms and functions
// ("Gewerke") — as flat enums with the display names the CCU WebUI uses.
func CCUFlatEnums(rooms, functions []FlatEntry) []FlatEnum {
	return []FlatEnum{
		{ID: taxonomy.EnumRoom, Names: map[string]string{"en": "Rooms", "de": "Räume"}, Entries: rooms},
		{ID: taxonomy.EnumFunction, Names: map[string]string{"en": "Functions", "de": "Gewerke"}, Entries: functions},
	}
}

// ApplyFlatTaxonomy records a taxonomy of depth-one enums — how a system
// with flat room and function lists (a CCU) presents itself — and a ref for
// every member resolve maps to an address. An entry without a name is left
// out, as the name-keyed readers leave it out.
func (c *Cache) ApplyFlatTaxonomy(enums []FlatEnum, resolve func(memberID string) (address string, ok bool)) {
	t := &taxonomy.Taxonomy{Enums: make(map[taxonomy.EnumID]*taxonomy.Enum, len(enums))}
	for _, fe := range enums {
		e := &taxonomy.Enum{ID: fe.ID, Names: maps.Clone(fe.Names)}
		for _, entry := range fe.Entries {
			if entry.Name == "" || entry.ID == "" {
				continue
			}
			e.Roots = append(e.Roots, &taxonomy.Node{ID: entry.ID, Name: entry.Name})
			ref := taxonomy.Root(fe.ID, entry.ID)
			for _, member := range entry.MemberIDs {
				if addr, ok := resolve(member); ok {
					c.AddRef(addr, ref)
				}
			}
		}
		t.Enums[fe.ID] = e
	}
	c.SetTaxonomy(t)
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
	deviceAddr := hmtypes.DeviceAddress(channelAddress)
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

// GetInterface returns the cached interface for address. ok reports whether
// an interface tag was actually cached; when it is false the returned value
// is the [hmenum.InterfaceBidCosRF] placeholder and carries no information
// about the device.
//
// The distinction matters because the cache is populated only by the
// periodic [Loader] run, which is the sole writer of the interface tags: a
// caller reading before the first refresh would otherwise receive
// "BidCos-RF" for every address, indistinguishable from a device the loader
// genuinely tagged BidCos-RF, which [Loader.Load] writes for an omitted or
// unrecognised interface token.
func (c *Cache) GetInterface(address string) (iface hmenum.Interface, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if iface, ok := c.interfaces[address]; ok {
		return iface, true
	}
	return hmenum.InterfaceBidCosRF, false
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
	refs, deviceRefs, tax := src.refs, src.deviceRefs, src.tax
	src.mu.Unlock()

	c.mu.Lock()
	c.names, c.iseIDs, c.interfaces = names, iseIDs, interfaces
	c.channelRooms, c.deviceRooms, c.functions = channelRooms, deviceRooms, functions
	c.refs, c.deviceRefs, c.tax = refs, deviceRefs, tax
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
	c.refs = make(map[string]map[taxonomy.Ref]struct{})
	c.deviceRefs = make(map[string]map[taxonomy.Ref]struct{})
	c.tax = nil
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
	delete(c.refs, deviceAddress)
	delete(c.deviceRefs, deviceAddress)
	for _, ch := range channels {
		delete(c.names, ch)
		delete(c.interfaces, ch)
		delete(c.iseIDs, ch)
		delete(c.channelRooms, ch)
		delete(c.functions, ch)
		delete(c.refs, ch)
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
