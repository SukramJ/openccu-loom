// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"sort"
	"sync"

	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// AttachableDataPoint is the minimal contract a derived data point
// (calculated formula sensor, custom-profile data point) must satisfy
// to be attached to a [Channel]. Any `*generic.DataPoint[T]` already
// implements it through its `DataPointKey()` method.
//
// Mirrors the Python `CalculatedDataPointProtocol`
// `CustomDataPointProtocol` slice the Channel/Device aggregators in
type AttachableDataPoint interface {
	DataPointKey() hmtypes.DataPointKey
}

// AttachableEvent is the minimal contract for Channel-attached generic event
// sources (keypress / impulse / device-error).
type AttachableEvent interface {
	DataPointKey() hmtypes.DataPointKey
	EventKind() string
}

// SubscribingDataPoint is an opt-in extension implemented by
// calculated / custom data points that need to react to changes on
// the channel's wire-level (VALUES) parameters. The implementation
// reads the parameters it depends on via [Channel.Parameter] and
// hooks into their `OnAnyUpdate` so a sub-DP value change pushes the
// new derived value or aggregate state.
//
// Subscribe returns an unsubscribe closure the channel calls when
// the DP is detached. Returning nil is allowed — the channel then
// has no cleanup obligation.
type SubscribingDataPoint interface {
	AttachableDataPoint
	Subscribe(c *Channel) func()
}

// AttachCalculatedDataPoint registers a calculated DP under its key.
// Re-registering the same key replaces the prior entry. When the DP
// implements [SubscribingDataPoint] its `Subscribe` is invoked so the
// DP can wire its own per-parameter listeners; the returned
// unsubscribe is stored and fires on a subsequent re-attach.
func (c *Channel) AttachCalculatedDataPoint(dp AttachableDataPoint) {
	if dp == nil {
		return
	}
	c.mu.Lock()
	if c.calculatedDPs == nil {
		c.calculatedDPs = make(map[hmtypes.DataPointKey]AttachableDataPoint)
	}
	if c.calculatedUnsubs == nil {
		c.calculatedUnsubs = make(map[hmtypes.DataPointKey]func())
	}
	key := dp.DataPointKey()
	if prev, ok := c.calculatedUnsubs[key]; ok && prev != nil {
		prev()
	}
	c.calculatedDPs[key] = dp
	c.mu.Unlock()

	if sub, ok := dp.(SubscribingDataPoint); ok {
		unsub := sub.Subscribe(c)
		c.mu.Lock()
		c.calculatedUnsubs[key] = unsub
		c.mu.Unlock()
	}
}

// CalculatedDataPoints returns a stable snapshot of every calculated
// data point attached to the channel, sorted by key.
func (c *Channel) CalculatedDataPoints() []AttachableDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshotByKey(c.calculatedDPs)
}

// SetCustomDataPoint binds a custom-profile data point (e.g. a Cover,
// Climate, Light) to the channel. A channel carries at most one
// custom DP at a time. Passing nil clears the binding. When the DP
// implements [SubscribingDataPoint] its `Subscribe` is invoked so the
// DP can wire its sub-DP listeners through the channel's wire-level
// parameters.
func (c *Channel) SetCustomDataPoint(dp AttachableDataPoint) {
	c.mu.Lock()
	if c.customUnsub != nil {
		c.customUnsub()
		c.customUnsub = nil
	}
	c.customDP = dp
	c.mu.Unlock()

	if dp == nil {
		return
	}
	if sub, ok := dp.(SubscribingDataPoint); ok {
		unsub := sub.Subscribe(c)
		c.mu.Lock()
		c.customUnsub = unsub
		c.mu.Unlock()
	}
}

// CustomDataPoint returns the bound custom DP, or nil when none is
// attached.
func (c *Channel) CustomDataPoint() AttachableDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.customDP
}

// AttachGenericEvent registers a generic event source.
func (c *Channel) AttachGenericEvent(ev AttachableEvent) {
	if ev == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.genericEvents == nil {
		c.genericEvents = make(map[hmtypes.DataPointKey]AttachableEvent)
	}
	c.genericEvents[ev.DataPointKey()] = ev
}

// GenericEvents returns a stable snapshot of every event source
// attached to the channel.
func (c *Channel) GenericEvents() []AttachableEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]hmtypes.DataPointKey, 0, len(c.genericEvents))
	for k := range c.genericEvents {
		keys = append(keys, k)
	}
	sortKeys(keys)
	out := make([]AttachableEvent, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.genericEvents[k])
	}
	return out
}

// BuildEventGroups groups the channel's generic events by kind and
// installs one [modevent.Group] per distinct kind. Each group receives all
// sources of that kind via [modevent.Group.Add]; the availability delegate is
// wired to the parent device so groups report the device's reachability.
//
// The method is idempotent: calling it again after attaching additional events
// rebuilds the groups from scratch, closing and replacing the old ones.
//
// Callers must invoke this method after all generic events have been attached
// to the channel, typically at the end of the channel hydration step. Groups
// remain nil (and [EventGroups] returns nil) until this method is called.
//
// centralName scopes the [modevent.Group.UniqueID]; pass an empty string in
// tests that do not require multi-CCU scoping.
func (c *Channel) BuildEventGroups(centralName string) {
	c.mu.Lock()
	// Close any previously built groups to release source subscriptions.
	old := c.eventGroups
	c.eventGroups = nil
	c.mu.Unlock()

	for _, g := range old {
		g.Close()
	}

	// Gather events and classify by kind. Only concrete *modevent.Source
	// values can be added to a Group; other AttachableEvent implementations
	// (e.g. test doubles) are skipped.
	c.mu.RLock()
	sources := make([]AttachableEvent, 0, len(c.genericEvents))
	for _, ev := range c.genericEvents {
		sources = append(sources, ev)
	}
	addr := c.Address
	c.mu.RUnlock()

	byKind := make(map[modevent.Kind][]*modevent.Source)
	for _, ev := range sources {
		src, ok := ev.(*modevent.Source)
		if !ok {
			continue
		}
		byKind[src.Kind] = append(byKind[src.Kind], src)
	}

	if len(byKind) == 0 {
		return
	}

	newGroups := make(map[string]*modevent.Group, len(byKind))
	for k, srcs := range byKind {
		g := modevent.NewGroupWithCentral(centralName, addr, k)
		for _, src := range srcs {
			g.Add(src)
		}
		// Wire availability to the parent device when available.
		if c.device != nil {
			dev := c.device
			g.SetAvailableFunc(func() bool { return dev.Available() })
		}
		newGroups[string(k)] = g
	}

	c.mu.Lock()
	c.eventGroups = newGroups
	c.mu.Unlock()
}

// CategorisedDataPoint pairs an attached data point with its CCU
// fine-grained category. Used by [Channel.DataPointsByCategory]
// [Device.DataPointsByCategory] so callers can filter heterogeneous
// collections (calculated + custom + sub-DPs) without reflection.
type CategorisedDataPoint interface {
	AttachableDataPoint
	Category() hmenum.DataPointCategory
}

// DataPointsByCategory returns every attached calculated / custom DP of the
// channel that satisfies [CategorisedDataPoint] and reports `category`.
func (c *Channel) DataPointsByCategory(category hmenum.DataPointCategory) []AttachableDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]AttachableDataPoint, 0)
	if c.customDP != nil {
		if cdp, ok := c.customDP.(CategorisedDataPoint); ok && cdp.Category() == category {
			out = append(out, c.customDP)
		}
	}
	keys := make([]hmtypes.DataPointKey, 0, len(c.calculatedDPs))
	for k := range c.calculatedDPs {
		keys = append(keys, k)
	}
	sortKeys(keys)
	for _, k := range keys {
		dp := c.calculatedDPs[k]
		if cdp, ok := dp.(CategorisedDataPoint); ok && cdp.Category() == category {
			out = append(out, dp)
		}
	}
	return out
}

// DataPointsByCategory aggregates every channel's [CategorisedDataPoint] of
// `category`.
func (d *Device) DataPointsByCategory(category hmenum.DataPointCategory) []AttachableDataPoint {
	channels := d.Channels()
	out := make([]AttachableDataPoint, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.DataPointsByCategory(category)...)
	}
	return out
}

// CalculatedDataPoints aggregates calculated DPs across every channel.
func (d *Device) CalculatedDataPoints() []AttachableDataPoint {
	channels := d.Channels()
	out := make([]AttachableDataPoint, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.CalculatedDataPoints()...)
	}
	return out
}

// CustomDataPoints aggregates the bound custom DP of every channel, skipping
// channels without a binding.
func (d *Device) CustomDataPoints() []AttachableDataPoint {
	channels := d.Channels()
	out := make([]AttachableDataPoint, 0, len(channels))
	for _, ch := range channels {
		if dp := ch.CustomDataPoint(); dp != nil {
			out = append(out, dp)
		}
	}
	return out
}

// GenericEvents aggregates event sources across every channel.
func (d *Device) GenericEvents() []AttachableEvent {
	channels := d.Channels()
	out := make([]AttachableEvent, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.GenericEvents()...)
	}
	return out
}

// SubscribeToDeviceUpdated registers a callback fired by
// [Device.NotifyUpdated].
//
// Returns an idempotent unsubscribe closure.
func (d *Device) SubscribeToDeviceUpdated(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	d.updatedMu.Lock()
	d.updatedHandlers = append(d.updatedHandlers, fn)
	idx := len(d.updatedHandlers) - 1
	d.updatedMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.updatedMu.Lock()
			defer d.updatedMu.Unlock()
			if idx < len(d.updatedHandlers) {
				d.updatedHandlers[idx] = nil
			}
		})
	}
}

// NotifyUpdated fires every handler registered via
// [Device.SubscribeToDeviceUpdated]. Called by the device-state
// pipeline whenever a high-level transition (reachability, low
// battery, config-pending, …) is observed.
func (d *Device) NotifyUpdated() {
	d.updatedMu.Lock()
	handlers := make([]func(), len(d.updatedHandlers))
	copy(handlers, d.updatedHandlers)
	d.updatedMu.Unlock()
	for _, h := range handlers {
		if h != nil {
			h()
		}
	}
}

// AddChannelToGroup records that channelNo belongs to groupNo on the
// device-level channel-grouping map. The Channel itself already carries its
// own `GroupNo`; this map exists so coordinators can resolve "give me every
// channel of group N" cheaply.
//
// Calling with the same (groupNo, channelNo) twice is a no-op.
func (d *Device) AddChannelToGroup(groupNo, channelNo int) {
	d.groupMu.Lock()
	defer d.groupMu.Unlock()
	if d.groupChannels == nil {
		d.groupChannels = make(map[int]map[int]struct{})
	}
	if d.channelToGroup == nil {
		d.channelToGroup = make(map[int]int)
	}
	members, ok := d.groupChannels[groupNo]
	if !ok {
		members = make(map[int]struct{})
		d.groupChannels[groupNo] = members
	}
	members[channelNo] = struct{}{}
	// Keep the reverse map in sync so GroupForChannel is O(1).
	d.channelToGroup[channelNo] = groupNo
	// The group number itself also maps to its own group (mirrors the Python
	// _channel_to_group initialisation that seeds group_no → group_no).
	if _, exists := d.channelToGroup[groupNo]; !exists {
		d.channelToGroup[groupNo] = groupNo
	}
}

// GroupChannels returns the channel numbers registered for groupNo,
// sorted ascending. Returns nil when the group is unknown.
func (d *Device) GroupChannels(groupNo int) []int {
	d.groupMu.RLock()
	defer d.groupMu.RUnlock()
	members, ok := d.groupChannels[groupNo]
	if !ok {
		return nil
	}
	out := make([]int, 0, len(members))
	for n := range members {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// ─── ChannelGroups ───────────────────────────────────────────────────

// ChannelGroups returns the device's channel-group membership as a slice of
// [RebasedChannelGroupConfig] values sorted by group number. Each entry
// records one logical sub-device group together with its sorted member
// channel numbers.
//
// Returns nil when no groups have been registered.
func (d *Device) ChannelGroups() []RebasedChannelGroupConfig {
	d.groupMu.RLock()
	defer d.groupMu.RUnlock()
	if len(d.groupChannels) == 0 {
		return nil
	}
	out := make([]RebasedChannelGroupConfig, 0, len(d.groupChannels))
	for groupNo, members := range d.groupChannels {
		nos := make([]int, 0, len(members))
		for n := range members {
			nos = append(nos, n)
		}
		sort.Ints(nos)
		out = append(out, RebasedChannelGroupConfig{
			GroupNumber:    groupNo,
			ChannelNumbers: nos,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupNumber < out[j].GroupNumber })
	return out
}

// GetChannelGroupNo returns the group number for channelNo. Returns 0 when
// the channel has not been assigned to a group.
//
// Note: each channel also carries its GroupNo field directly; this
// device-level convenience method exists so callers with only a channel
// number (not the Channel object) can look up the group association cheaply.
//
// The lookup uses the O(1) reverse map maintained by [AddChannelToGroup].
func (d *Device) GetChannelGroupNo(channelNo int) int {
	d.groupMu.RLock()
	defer d.groupMu.RUnlock()
	return d.channelToGroup[channelNo]
}

// IsInMultiChannelGroup reports whether channelNo belongs to any channel
// group on this device.
func (d *Device) IsInMultiChannelGroup(channelNo int) bool {
	return d.GetChannelGroupNo(channelNo) != 0
}

// ─── DefaultScheduleChannel ─────────────────────────────────────────

// DefaultScheduleChannel returns the first channel that has a week-profile
// attached, or nil when no such channel exists.
//
// return next((c for c in self._channels if c.is_schedule_channel), None)
//
// Channels are sorted by address before scanning so the result is stable.
func (d *Device) DefaultScheduleChannel() *Channel {
	for _, ch := range d.Channels() {
		if ch.HasWeekProfile() {
			return ch
		}
	}
	if root := d.RootChannel(); root != nil && root.HasWeekProfile() {
		return root
	}
	return nil
}

// ─── AllowUndefinedGenericDataPoints ──────────────────────────────────

// AllowUndefinedGenericDataPoints reports whether the device's custom-profile
// data point(s) allow un-mapped generic parameters to remain visible.
//
// return any(cdp.allow_undefined_generic_data_points for cdp in
// self.custom_data_points)
//
// The Go equivalent checks whether any bound custom DP satisfies the
// [UndefinedGenericDataPointAllower] interface.
func (d *Device) AllowUndefinedGenericDataPoints() bool {
	for _, ch := range d.Channels() {
		cdp := ch.CustomDataPoint()
		if cdp == nil {
			continue
		}
		if a, ok := cdp.(UndefinedGenericDataPointAllower); ok && a.AllowUndefinedGenericDataPoints() {
			return true
		}
	}
	return false
}

// UndefinedGenericDataPointAllower is the opt-in interface that
// custom-profile data points implement when they want un-mapped generic
// parameters to remain visible alongside their custom surface.
type UndefinedGenericDataPointAllower interface {
	AllowUndefinedGenericDataPoints() bool
}

// ─── HasSubDevices ───────────────────────────────────────────────────

// HasSubDevices reports whether the device should be split into multiple
// logical sub-devices for northbound presentation (MQTT discovery, REST/WS
// device-info). The device must declare at least two channel groups AND at
// least two of those groups must carry more than one member channel —
// otherwise the grouping is trivial (a single multi-channel group with
// auxiliary singletons, e.g. HmIP-WRC6-230's IP_SWITCH + seven singleton
// LED groups) and producing sub-devices would just yield single-DP children
// with no added structure.
func (d *Device) HasSubDevices() bool {
	d.groupMu.RLock()
	defer d.groupMu.RUnlock()
	if len(d.groupChannels) <= 1 {
		return false
	}
	count := 0
	for _, members := range d.groupChannels {
		if len(members) > 1 {
			count++
		}
		if count > 1 {
			return true
		}
	}
	return false
}

// ─── RelevantForCentralLinkManagement ────────────────────────────────

// virtualRemoteModels enumerates pseudo-device models the CCU exposes as
// "virtual remotes". These are not real radio peers — they only forward
// press events from the WebUI / scripts onto the bus. Including them in
// central-link management would have the CCU attempt to add KEY_*-source
// links onto a device that has no physical button to press, so we skip
// them at the dispatch boundary.
var virtualRemoteModels = map[string]struct{}{
	"HM-RCV-50":   {},
	"HMW-RCV-50":  {},
	"HmIP-RCV-50": {},
}

// RelevantForCentralLinkManagement reports whether this device is a candidate
// for CCU central-link management.
//
// Returns true when the device's interface is one of BidCos-RF, BidCos-Wired
// or HmIP-RF AND the device's model is not a virtual-remote pseudo-device.
// VirtualDevices and CUxD devices do not participate in CCU press-event link
// management — they use a different event dispatch path.
func (d *Device) RelevantForCentralLinkManagement() bool {
	switch d.Interface { //nolint:exhaustive // VirtualDevices and CUxD do not participate in CCU link management
	case hmenum.InterfaceBidCosRF, hmenum.InterfaceBidCosWired, hmenum.InterfaceHmIPRF:
		// fall through to model filter
	default:
		return false
	}
	if _, isVirtualRemote := virtualRemoteModels[d.Model]; isVirtualRemote {
		return false
	}
	return true
}

// ─── GetDataPoints ───────────────────────────────────────────────────

// registeredDP is the minimal interface required to apply the registered
// filter inside GetDataPoints without importing the datapoint package.
type registeredDP interface {
	IsRegistered() bool
}

// noCreateDP is the minimal interface required to apply the excludeNoCreate
// filter inside GetDataPoints without importing the hmenum package directly.
type noCreateDP interface {
	Usage() hmenum.DataPointUsage
}

// GetDataPoints returns the calculated + custom data points across all
// channels that match the optional category filter.
//
// When category is the zero value all data points are returned.
// When excludeNoCreate is true, data points whose Usage() equals
// [hmenum.DataPointUsageNoCreate] are excluded.
// When registered is non-nil, only data points whose IsRegistered()
// result matches *registered are included.
func (d *Device) GetDataPoints(category hmenum.DataPointCategory, excludeNoCreate bool, registered *bool) []AttachableDataPoint {
	channels := d.Channels()
	out := make([]AttachableDataPoint, 0, len(channels)*4)
	for _, ch := range channels {
		// Custom DP
		if cdp := ch.CustomDataPoint(); cdp != nil {
			if matchesGetDataPointsFilter(cdp, category, excludeNoCreate, registered) {
				out = append(out, cdp)
			}
		}
		// Calculated DPs
		for _, cdp := range ch.CalculatedDataPoints() {
			if matchesGetDataPointsFilter(cdp, category, excludeNoCreate, registered) {
				out = append(out, cdp)
			}
		}
	}
	return out
}

// matchesGetDataPointsFilter applies the category / excludeNoCreate / registered
// filters used by GetDataPoints. Returns true when the DP passes all active filters.
func matchesGetDataPointsFilter(dp AttachableDataPoint, category hmenum.DataPointCategory, excludeNoCreate bool, registered *bool) bool {
	if category != "" {
		cd, ok := dp.(CategorisedDataPoint)
		if !ok || cd.Category() != category {
			return false
		}
	}
	if excludeNoCreate {
		if nc, ok := dp.(noCreateDP); ok && nc.Usage() == hmenum.DataPointUsageNoCreate {
			return false
		}
	}
	if registered != nil {
		rd, ok := dp.(registeredDP)
		if !ok || rd.IsRegistered() != *registered {
			return false
		}
	}
	return true
}

// ─── GetEvents ───────────────────────────────────────────────────────

// GetEvents returns all generic event sources across the device's channels.
//
// return tuple(ev for ch in self.channels for ev in ch.generic_events)
func (d *Device) GetEvents() []AttachableEvent {
	return d.GenericEvents()
}

// ─── LinkPeerChannels ────────────────────────────────────────────────

// LinkPeerChannels returns a map of channel address → peer channel addresses
// for every channel on the device that has at least one link peer cached.
// Channels with no known peers are omitted. The returned map and its slices
// are copies; callers may modify them freely.
func (d *Device) LinkPeerChannels() map[string][]string {
	channels := d.Channels()
	out := make(map[string][]string, len(channels))
	for _, ch := range channels {
		if peers := ch.LinkPeers(); len(peers) > 0 {
			out[ch.Address] = peers
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ─── GetGenericDataPoint ─────────────────────────────────────────────

// GetGenericDataPoint returns a VALUES-paramset data point. Lookup order:
//
// 1. If statePath is non-empty, scan every channel for a DP whose StatePath()
// equals statePath and return the first match.
// 2. Otherwise look up by channelAddress + parameter name. When channelAddress
// is empty the search spans every channel; when non-empty only that channel is
// checked.
//
// Returns nil when no matching DP is found.
//
// Mirrors Channel.get_generic_data_point / Device.get_generic_data_point
// (model/device.py) which also performs state-path-first lookup followed by a
// paramset-key-aware parameter lookup.
func (d *Device) GetGenericDataPoint(channelAddress string, p hmenum.Parameter, statePath string) ParameterDataPoint {
	if statePath != "" {
		for _, ch := range d.Channels() {
			if dp := ch.GetGenericDataPointByStatePath(statePath); dp != nil {
				return dp
			}
		}
		return nil
	}
	if channelAddress == "" {
		for _, ch := range d.Channels() {
			if dp := ch.Parameter(p); dp != nil {
				return dp
			}
		}
		return nil
	}
	ch := d.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	return ch.Parameter(p)
}

// ─── GetGenericEvent ─────────────────────────────────────────────────

// GenericEvent is the minimal interface event sources expose for the purpose of
// [Device.GetGenericEvent] — any [AttachableEvent] that also carries a
// parameter name.
type GenericEvent interface {
	AttachableEvent
	EventParameter() hmenum.Parameter
}

// GetGenericEvent returns the first generic event source on channelAddress
// for the given parameter, or nil when absent.
func (d *Device) GetGenericEvent(channelAddress string, p hmenum.Parameter) AttachableEvent {
	ch := d.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	for _, ev := range ch.GenericEvents() {
		if ge, ok := ev.(GenericEvent); ok && ge.EventParameter() == p {
			return ev
		}
	}
	return nil
}

// ─── GetReadableDataPoints ───────────────────────────────────────────

// GetReadableDataPoints returns data points that advertise READ in their
// operations bitmask, optionally restricted to a single paramset.
//
// When paramsetKey is the zero value (empty string) the search spans VALUES
// and MASTER across all channels — the original broad behaviour. When a
// specific key is supplied (e.g. [hmenum.ParamsetKeyMaster]) only DPs from
// that paramset are returned; this matches the Python signature
// get_readable_data_points(*, paramset_key: ParamsetKey) (model/device.py).
func (d *Device) GetReadableDataPoints(paramsetKey hmenum.ParamsetKey) []ParameterDataPoint {
	channels := d.Channels()
	out := make([]ParameterDataPoint, 0, len(channels)*4)
	for _, ch := range channels {
		var dps []ParameterDataPoint
		switch paramsetKey {
		case hmenum.ParamsetKeyMaster:
			dps = ch.MasterDataPoints()
		case hmenum.ParamsetKeyValues, "":
			dps = ch.DataPoints()
			if paramsetKey == "" {
				// No filter: include MASTER too.
				dps = append(dps, ch.MasterDataPoints()...)
			}
		default:
			dps = ch.ParamsetDataPoints(paramsetKey)
		}
		for _, dp := range dps {
			if dp.ParameterData().Operations.IsReadable() {
				out = append(out, dp)
			}
		}
	}
	return out
}

// ─── CombinedDataPoints ──────────────────────────────────────────────

// CombinedDataPoint is the opt-in marker interface implemented by combined-
// package data points (HSColor, Timer, LevelCombined, WeekProfile, …). Any
// [AttachableDataPoint] stored in calculatedDPs that also satisfies this
// interface is surfaced by [Channel.CombinedDataPoints]
// [Device.CombinedDataPoints].
type CombinedDataPoint interface {
	AttachableDataPoint
	// IsCombined is a zero-cost marker method. Combined DPs implement it
	// by returning true; all other calculated / generic DPs do not.
	IsCombined() bool
}

// CombinedDataPoints returns every calculated DP attached to the channel
// that satisfies [CombinedDataPoint] and returns true from IsCombined().
func (c *Channel) CombinedDataPoints() []AttachableDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]hmtypes.DataPointKey, 0, len(c.calculatedDPs))
	for k := range c.calculatedDPs {
		keys = append(keys, k)
	}
	sortKeys(keys)
	out := make([]AttachableDataPoint, 0)
	for _, k := range keys {
		dp := c.calculatedDPs[k]
		if cdp, ok := dp.(CombinedDataPoint); ok && cdp.IsCombined() {
			out = append(out, dp)
		}
	}
	return out
}

// CombinedDataPoints aggregates combined DPs across every channel.
func (d *Device) CombinedDataPoints() []AttachableDataPoint {
	channels := d.Channels()
	out := make([]AttachableDataPoint, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.CombinedDataPoints()...)
	}
	return out
}

// ─── DataPointPaths ───────────────────────────────────────────────────

// DataPointProvider is the opt-in interface for data points that expose
// a state_path string (set by the naming pipeline via SetPathData).
// Any [AttachableDataPoint] that also implements this interface participates
// in [Channel.DataPointPaths] / [Device.DataPointPaths].
type DataPointProvider interface {
	AttachableDataPoint
	StatePath() string
}

// DataPointPaths returns every non-empty StatePath from all data points
// attached to the channel (calculated, custom, generic).
//
// return tuple(self._state_path_to_dpk.keys())
func (c *Channel) DataPointPaths() []string {
	var out []string
	c.mu.RLock()
	for _, dp := range c.valuePoints {
		if pp, ok := dp.(DataPointProvider); ok {
			if p := pp.StatePath(); p != "" {
				out = append(out, p)
			}
		}
	}
	for _, dp := range c.calculatedDPs {
		if pp, ok := dp.(DataPointProvider); ok {
			if p := pp.StatePath(); p != "" {
				out = append(out, p)
			}
		}
	}
	if c.customDP != nil {
		if pp, ok := c.customDP.(DataPointProvider); ok {
			if p := pp.StatePath(); p != "" {
				out = append(out, p)
			}
		}
	}
	c.mu.RUnlock()
	sort.Strings(out)
	return out
}

// DataPointPaths aggregates state-path strings across every channel.
func (d *Device) DataPointPaths() []string {
	channels := d.Channels()
	out := make([]string, 0, len(channels)*4)
	for _, ch := range channels {
		out = append(out, ch.DataPointPaths()...)
	}
	sort.Strings(out)
	return out
}

// ─── GenericDataPoints ───────────────────────────────────────────────

// GenericDataPoints returns every VALUES-paramset data point across all
// channels as a flat slice.
//
// return tuple(dp for ch in self._channels.values() for dp in
// ch.generic_data_points)
//
// This is a convenience alias for [Device.AllDataPoints].
func (d *Device) GenericDataPoints() []ParameterDataPoint {
	return d.AllDataPoints()
}

// ─── Identifier ───────────────────────────────────────────────────────

// Identifier returns the stable device identifier in the form
// "<device_address>::<interface_id>".
//
// return f"{self._address}{IDENTIFIER_SEPARATOR}{self._interface_id}"
//
// Used by north-bound adapters (MQTT Discovery, REST) as the canonical device
// identity token that survives address changes.
func (d *Device) Identifier() string {
	return d.Address + "::" + d.InterfaceID
}

// ─── GetCalculatedDataPoint ──────────────────────────────────────────

// GetCalculatedDataPoint returns a calculated data point on the channel at
// channelAddress identified by its parameter name, or nil when not found.
//
// if channel := self.get_channel(channel_address=channel_address): return
// channel.get_calculated_data_point(parameter=parameter) return None
func (d *Device) GetCalculatedDataPoint(channelAddress, parameter string) AttachableDataPoint {
	ch := d.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	for _, dp := range ch.CalculatedDataPoints() {
		if dp.DataPointKey().Parameter == parameter {
			return dp
		}
	}
	return nil
}

// ─── GetCustomDataPoint ───────────────────────────────────────────────

// GetCustomDataPoint returns the custom-profile data point bound to the
// channel with the given channel number, or nil when the channel has none.
//
// if channel := self.get_channel(channel_address=...): return
// channel.custom_data_point return None
func (d *Device) GetCustomDataPoint(channelNo int) AttachableDataPoint {
	d.mu.RLock()
	var ch *Channel
	for _, c := range d.channels {
		if c.Number == channelNo {
			ch = c
			break
		}
	}
	d.mu.RUnlock()
	if ch == nil {
		return nil
	}
	return ch.CustomDataPoint()
}

// ─── SubscribeToFirmwareUpdated ─────────────────────────────────────

// SubscribeToFirmwareUpdated registers a handler called whenever the device's
// firmware information changes (version, update state). Returns an idempotent
// unsubscribe closure.
//
// return self._firmware.subscribe_to_updated(handler=handler)
func (d *Device) SubscribeToFirmwareUpdated(fn func(FirmwareInfo)) func() {
	return d.firmware.OnChange(fn)
}

func snapshotByKey(m map[hmtypes.DataPointKey]AttachableDataPoint) []AttachableDataPoint {
	keys := make([]hmtypes.DataPointKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortKeys(keys)
	out := make([]AttachableDataPoint, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func sortKeys(keys []hmtypes.DataPointKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ChannelAddress != keys[j].ChannelAddress {
			return keys[i].ChannelAddress < keys[j].ChannelAddress
		}
		if keys[i].ParamsetKey != keys[j].ParamsetKey {
			return keys[i].ParamsetKey < keys[j].ParamsetKey
		}
		return keys[i].Parameter < keys[j].Parameter
	})
}

// ─── CheckChannelIsOnlyPrimaryChannel ─────────────────────────────────

// CheckChannelIsOnlyPrimaryChannel reports whether the given channel is the
// sole primary channel of its device, i.e. it is the primary channel AND the
// device does not expose multiple real channels. Custom-DP naming uses this
// flag to decide whether to suppress the channel-number suffix in generated
// entity names.
//
// Logic: return primary_channel == current_channel_no and device_has_multiple_channels is False.
//
// Pass -1 for primaryChannelNo or currentChannelNo to represent "not set".
//
// loom:reachable:reason="called by MQTT entity-description builder to determine whether the channel-number suffix can be omitted"
func CheckChannelIsOnlyPrimaryChannel(currentChannelNo, primaryChannelNo int, deviceHasMultipleChannels bool) bool {
	if deviceHasMultipleChannels {
		return false
	}
	return primaryChannelNo == currentChannelNo
}
