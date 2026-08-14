// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ParameterDataPoint is the type-agnostic view of a data point that
// the Device/Channel layer needs. Every `*generic.DataPoint[T]`
// satisfies this interface automatically, so consumers can treat a
// heterogeneous parameter map uniformly.
type ParameterDataPoint interface {
	DataPointKey() hmtypes.DataPointKey
	Parameter() hmenum.Parameter
	ParameterData() hmproto.ParameterData
	RawValue() (any, bool)
	ModifiedAt() time.Time

	// OnAnyUpdate registers a type-erased change handler. Returns an
	// idempotent unsubscribe closure. Used by [SubscribingDataPoint]
	// implementations to react to wire-level parameter changes
	// without coupling to the underlying generic[T].
	OnAnyUpdate(fn func(old, next any)) func()
}

// ChannelNumberDevice is the synthetic channel number used for the
// device-root pseudo-channel that holds parameters living on the device
// address itself (no `:N` suffix).
//
// Production channels use 0..n; -1 is unique to the root pseudo- channel and
// lets adapters filter it out via `ch.Number == ChannelNumberDevice` when
// they only want real channels.
const ChannelNumberDevice = -1

// Channel is a single CCU channel of a device.
//
// `Type` is the CCU-reported CHANNEL_TYPE (e.g. "SHUTTER_TRANSMITTER"
// for a BidCos-RF shutter remote). It is set by the ingest pipeline
// from the TYPE field of the per-channel listDevices entry; most
// MASTER-paramset metadata (easymode groupings, translations, cross-
// validations) is keyed by this value, **not** by the parent
// device's model.
//
// A Channel holds the data points for two CCU paramsets side-by-side:
//
// - VALUES (runtime state: STATE, LEVEL, TEMPERATURE, …)
// - MASTER (channel configuration: e.g. SHORT_ON_TIME, TRANSMIT_TRY_MAX)
//
// Most callers only touch VALUES and use [Put], [Parameter],
// [DataPoints] and [Len] (unchanged surface). MASTER mirrors that
// surface under [PutMaster] / [MasterParameter] / [MasterDataPoints]
// / [MasterLen] so the UI can render configuration pages without
// colliding with runtime state that happens to share a name.
//
// # Locking discipline
//
// Channel uses three independent mutexes. They are NEVER held simultaneously,
// so there is no lock-ordering constraint and no deadlock risk between them:
//
//   - mu (sync.RWMutex) — guards all data-point maps (valuePoints,
//     masterPoints), the writer/refresher pointers, calculatedDPs,
//     customDP, genericEvents, eventGroups, weekProfile,
//     masterRefreshHook, centralName, typeTranslation, and the
//     operator-assigned identity (name, rooms, functions, iseID).
//   - linkPeersMu (sync.RWMutex) — guards linkPeers only. Kept
//     separate from mu so topology updates do not stall concurrent
//     data-point reads on a busy channel.
//   - linkPeerMu (sync.Mutex) — guards linkPeerHandlers (the
//     subscriber slice for OnLinkPeerChanged). Released before
//     invoking any handler in NotifyLinkPeerChanged; never held while
//     calling back into the channel or acquiring mu / linkPeersMu.
//
// Remove() releases mu before acquiring linkPeerMu (step 6), which is
// the only method that touches two mutexes across its lifetime — and
// it does so sequentially, not simultaneously.
type Channel struct {
	Address    string
	Number     int
	Type       string
	ParamsetIn hmenum.ParamsetKey

	// name is the CCU-assigned human-readable channel name (e.g.
	// "Wohnzimmer Licht") when the operator configured one. Empty when the
	// operator configured none — the UI then renders "Kanal N".
	//
	// rooms / functions are populated by the Rega-script ingest
	// (set_device_rooms.fn / set_device_functions.fn) and used by the
	// north-bound adapters to render group dashboards. Empty when the CCU
	// operator has not assigned any. The singular [Channel.Room] method
	// derives the unambiguous case (exactly one room, including the
	// group-master fallback) — north-bound code that wants the single
	// canonical label should call that rather than index into Rooms()[0].
	//
	// iseID is the CCU-internal numeric identifier for this channel, stamped
	// by the ingest pipeline from the device-details response
	// (`get_address_id` on the channel address).
	//
	// All four carry live operator state that is rewritten long after the
	// channel went live: the ingest pipeline re-materialises them on every
	// reconnect and hot-plug, and the rename path writes them from the REST
	// handler goroutine. They are therefore guarded by mu and reachable only
	// through the accessors — as plain exported fields they were a torn read
	// for every unlocked north-bound reader (REST device list, MQTT
	// discovery, alarm candidate scan).
	name      string
	rooms     []string
	functions []string
	iseID     int

	// GroupNo is the channel-group number this channel belongs to. Zero means
	// "no group". When non-zero, the master channel of the group has Number ==
	// GroupNo.
	GroupNo int

	// typeTranslation is the CCU/OCCU translation for the channel type (e.g.
	// "Heizungsregler Transceiver" for HEATING_CLIMATECONTROL_TRANSCEIVER). Set
	// by the ingest pipeline from the CCU translations catalogue. Guarded by mu.
	typeTranslation string

	// linkPeerSourceCategories holds the DP-category strings that describe what
	// this channel can act as a source for in a central link. Set by the ingest
	// pipeline.
	linkPeerSourceCategories []string

	// linkPeerTargetCategories holds the DP-category strings that describe what
	// this channel can act as a target for in a central link. Set by the ingest
	// pipeline.
	linkPeerTargetCategories []string

	// linkSourceRoles / linkTargetRoles hold the raw CCU LINK_SOURCE_ROLES /
	// LINK_TARGET_ROLES tokens of this channel (space-separated on the wire).
	// They drive the direct-link role-matching filter: a sender source
	// intersects its LinkSourceRoles against a candidate's LinkTargetRoles,
	// and vice versa. Set by the ingest pipeline. Guarded by mu.
	linkSourceRoles []string
	linkTargetRoles []string

	// operatorHidden / operatorLocked are daemon-owned per-channel overrides
	// (G12) applied by the ingest pipeline from the persistent channel_flags
	// overlay: hidden drops the channel from operation lists / MQTT discovery /
	// Matter exposure, locked blocks control writes (VALUES paramset) while
	// leaving reads intact. Guarded by mu.
	operatorHidden bool
	operatorLocked bool

	// linkPeers caches the most recently observed link peer addresses for
	// this channel. Set by WireClimateLinkPeerRefresh when it processes a
	// LinkPeerChangedEvent, so the recovery path can immediately re-wire
	// climate activity subscriptions without waiting for the next topology
	// push. Guarded by linkPeersMu (separate from mu to avoid holding the
	// heavy data-point lock during topology updates).
	linkPeersMu sync.RWMutex // guards linkPeers only; never held with mu or linkPeerMu
	linkPeers   []string

	device *Device

	mu           sync.RWMutex // guards data-point maps, writer/refresher, and most fields; see locking discipline above
	valuePoints  map[hmenum.Parameter]ParameterDataPoint
	masterPoints map[hmenum.Parameter]ParameterDataPoint

	// writer and refresher are installed by the device pipeline after
	// hydration via [Channel.SetWriter] / [Channel.SetRefresher]. Both
	// are guarded by mu and may be nil (read-only mode).
	writer    ChannelWriter
	refresher ChannelRefresher

	calculatedDPs    map[hmtypes.DataPointKey]AttachableDataPoint
	calculatedUnsubs map[hmtypes.DataPointKey]func()
	customDP         AttachableDataPoint
	customUnsub      func()
	genericEvents    map[hmtypes.DataPointKey]AttachableEvent
	eventGroups      map[string]*modevent.Group // keyed by event.Kind string; guarded by mu

	// weekProfile is the channel-level schedule descriptor, attached during
	// pipeline hydration when the MASTER paramset reveals per-slot week-profile
	// parameters (P1_*..P6_*). Nil means "no schedule on this channel". The slot
	// parameters themselves are filtered out of the hydrated MASTER DPs (they
	// would surface as ~84 ghost MQTT topics per thermostat); this single DP is
	// the canonical schedule entity instead.
	weekProfile *weekprofile.ProfileDataPoint

	linkPeerMu       sync.Mutex // guards linkPeerHandlers only; never held with mu or linkPeersMu
	linkPeerHandlers []func()

	// masterRefreshHook is called after a successful MASTER paramset write
	// (via Set or SetMany with ParamsetKeyMaster). Classic HM devices use
	// this to schedule a MasterPoller read-back. HmIP devices ignore it
	// because they rely on CONFIG_PENDING instead. Guarded by mu.
	masterRefreshHook func(addr string, key hmenum.ParamsetKey)

	// centralName is the owning Unit's name. Set by the device
	// pipeline after channel hydration so that custom-DP constructors
	// that allocate new generic data points can propagate the correct
	// CentralName into [generic.Spec]. Empty is valid in test fixtures
	// where no real CCU is involved. Guarded by mu.
	centralName string

	// ServiceRegistry implements the write-half of [payload.Source]:
	// ServiceMethodNames + Invoke. The zero value is ready to use;
	// channel-level service methods are rare (most channel-scoped
	// writes go through MASTER-paramset PUT in the config session,
	// which has its own contract), so for most channels this stays
	// empty and simply makes Channel satisfy [payload.Source].
	payload.ServiceRegistry
}

// Device returns the parent device.
func (c *Channel) Device() *Device { return c.device }

// Remove tears down the channel's live resources in the same order as the
// Python Channel.remove() method (model/device.py):
//
// 1. Close event groups and release their source subscriptions.
// 2. Fire removal notifications on every generic event source.
// 3. Unsubscribe and clear every calculated DP.
// 4. Clear the custom DP binding and run its unsubscribe hook.
// 5. Collect VALUES and MASTER DPs for removal notifications, then clear them.
// 6. Clear link-peer change handlers to release subscriber closures.
//
// After Remove returns the channel must not be used again. Callers are
// responsible for removing the channel from the parent device's channel map
// (via [Device.RemoveChannel]).
func (c *Channel) Remove() {
	c.mu.Lock()

	// 1. Close event groups.
	for _, g := range c.eventGroups {
		g.Close()
	}
	c.eventGroups = nil

	// 2. Collect generic events for removal notifications.
	events := make([]AttachableEvent, 0, len(c.genericEvents))
	for _, ev := range c.genericEvents {
		events = append(events, ev)
	}
	c.genericEvents = nil

	// 3. Collect + unsubscribe calculated DPs.
	for key, unsub := range c.calculatedUnsubs {
		if unsub != nil {
			unsub()
		}
		delete(c.calculatedUnsubs, key)
	}
	c.calculatedDPs = nil

	// 4. Clear custom DP binding.
	if c.customUnsub != nil {
		c.customUnsub()
		c.customUnsub = nil
	}
	c.customDP = nil

	// 5. Collect VALUES and MASTER DPs before clearing so their
	// NotifyRemoved hooks can fire outside the lock.
	removable := make([]ParameterDataPoint, 0, len(c.valuePoints)+len(c.masterPoints))
	for _, dp := range c.valuePoints {
		removable = append(removable, dp)
	}
	for _, dp := range c.masterPoints {
		removable = append(removable, dp)
	}
	c.valuePoints = nil
	c.masterPoints = nil

	c.mu.Unlock()

	// 6. Clear link-peer change handlers under their own lock. Doing this
	// after releasing mu avoids lock-order issues with callers that hold
	// linkPeerMu when they call back into the channel. Setting the slice
	// to nil releases the subscriber closures and prevents stale callbacks
	// from firing if the channel object is reused or kept alive longer
	// than expected (e.g. during a device-reload cycle).
	c.linkPeerMu.Lock()
	c.linkPeerHandlers = nil
	c.linkPeerMu.Unlock()

	// Fire removal callbacks outside the lock so handlers can safely call
	// back into the channel.
	for _, ev := range events {
		if rd, ok := ev.(interface{ NotifyRemoved() }); ok {
			rd.NotifyRemoved()
		}
	}
	for _, dp := range removable {
		if rd, ok := dp.(interface{ NotifyRemoved() }); ok {
			rd.NotifyRemoved()
		}
	}
}

// RemoveDataPoint removes a single generic data point (VALUES or MASTER) from
// the channel. If the DP implements a NotifyRemoved() hook it is called after
// removal. This is the Go counterpart to Channel._remove_data_point
// (model/device.py) for the single-parameter case.
func (c *Channel) RemoveDataPoint(p hmenum.Parameter, paramsetKey hmenum.ParamsetKey) ParameterDataPoint {
	c.mu.Lock()
	var dp ParameterDataPoint
	switch paramsetKey { //nolint:exhaustive // only VALUES + MASTER are stored on channels
	case hmenum.ParamsetKeyMaster:
		dp = c.masterPoints[p]
		delete(c.masterPoints, p)
	default:
		dp = c.valuePoints[p]
		delete(c.valuePoints, p)
	}
	c.mu.Unlock()
	if dp == nil {
		return nil
	}
	if rd, ok := dp.(interface{ NotifyRemoved() }); ok {
		rd.NotifyRemoved()
	}
	return dp
}

// ChannelName satisfies the north-bound `ChannelNamer` contract by
// surfacing the CCU-operator-assigned channel name (`Channel.Name`).
// Defined as a method (not just the field) so adapters that consume
// the channel via the narrow [ChannelInspector] interface can read
// it without importing the full device package.
func (c *Channel) ChannelName() string {
	if c == nil {
		return ""
	}
	return c.Name()
}

// SetCentralName records the owning Unit's name so custom-DP
// constructors can populate [generic.Spec.CentralName] correctly.
// Called by the device pipeline during channel hydration.
func (c *Channel) SetCentralName(name string) {
	c.mu.Lock()
	c.centralName = name
	c.mu.Unlock()
}

// CentralName returns the owning Unit's name, or the empty
// string when the channel was constructed in a test fixture where no
// real CCU is involved.
func (c *Channel) CentralName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.centralName
}

// --- VALUES --------------------------------------------------------

// Put stores (or replaces) a VALUES data point under its parameter
// name.
func (c *Channel) Put(dp ParameterDataPoint) {
	if dp == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valuePoints == nil {
		c.valuePoints = make(map[hmenum.Parameter]ParameterDataPoint)
	}
	c.valuePoints[dp.Parameter()] = dp
}

// Parameter looks up a VALUES data point by wire parameter name.
// Returns nil when the channel does not carry the parameter in its
// VALUES paramset.
func (c *Channel) Parameter(p hmenum.Parameter) ParameterDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.valuePoints[p]
}

// DataPoints returns a stable snapshot of the VALUES data points.
func (c *Channel) DataPoints() []ParameterDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshotSorted(c.valuePoints)
}

// Len reports the number of VALUES data points.
func (c *Channel) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.valuePoints)
}

// --- MASTER --------------------------------------------------------

// PutMaster stores a MASTER-paramset data point. MASTER parameters
// are channel-configuration items — the CCU typically writes them
// as a batch via putParamset.
func (c *Channel) PutMaster(dp ParameterDataPoint) {
	if dp == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.masterPoints == nil {
		c.masterPoints = make(map[hmenum.Parameter]ParameterDataPoint)
	}
	c.masterPoints[dp.Parameter()] = dp
}

// MasterParameter looks up a MASTER-paramset data point by name.
func (c *Channel) MasterParameter(p hmenum.Parameter) ParameterDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.masterPoints[p]
}

// HasMasterParameter reports whether the channel exposes a MASTER-paramset data
// point with the given parameter name. Satisfies the calculated-data-point
// relevance probe (e.g. OperatingVoltageLevel's LOW_BAT_LIMIT requirement).
func (c *Channel) HasMasterParameter(name string) bool {
	return c.MasterParameter(hmenum.Parameter(name)) != nil
}

// HasDeviceMasterParameter reports whether the device-root channel exposes a
// MASTER-paramset data point with the given parameter name. Mirrors the
// reference channel.device.get_generic_data_point(channel_address=device.address,
// paramset_key=MASTER, …) lookup used by OperatingVoltageLevel's BATTERY_STATE
// branch.
func (c *Channel) HasDeviceMasterParameter(name string) bool {
	if c.device == nil {
		return false
	}
	root := c.device.RootChannel()
	if root == nil {
		return false
	}
	return root.MasterParameter(hmenum.Parameter(name)) != nil
}

// MasterDataPoints returns a stable snapshot of the MASTER-paramset
// data points.
func (c *Channel) MasterDataPoints() []ParameterDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshotSorted(c.masterPoints)
}

// MasterLen reports the number of MASTER data points.
func (c *Channel) MasterLen() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.masterPoints)
}

// --- dispatch by paramset -----------------------------------------

// ParamsetDataPoints returns the data points registered under key.
// Unknown keys return nil.
func (c *Channel) ParamsetDataPoints(key hmenum.ParamsetKey) []ParameterDataPoint {
	switch key { //nolint:exhaustive // only VALUES + MASTER are stored on channels today
	case hmenum.ParamsetKeyValues:
		return c.DataPoints()
	case hmenum.ParamsetKeyMaster:
		return c.MasterDataPoints()
	}
	return nil
}

// HasParameter reports whether the channel exposes a VALUES-paramset
// data point with the given parameter name. Used by north-bound
// adapters (MQTT discovery aggregator) to decide whether auxiliary
// topics like LEVEL_2 / HUMIDITY are worth declaring.
func (c *Channel) HasParameter(name string) bool {
	if c == nil {
		return false
	}
	return c.Parameter(hmenum.Parameter(name)) != nil
}

// ParameterFloatRange returns the descriptor's MIN / MAX bounds for the named
// VALUES-paramset parameter, parsed as float64. The third return value is
// true when both bounds were present and parseable.
//
// Used by the MQTT climate discovery builder to derive the HA `min_temp` /
// `max_temp` values from the SET_POINT_TEMPERATURE descriptor instead of
// hardcoding 4.5 / 30.5.
func (c *Channel) ParameterFloatRange(name string) (lo, hi float64, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	dp := c.Parameter(hmenum.Parameter(name))
	if dp == nil {
		return 0, 0, false
	}
	desc := dp.ParameterData()
	loV, loOK := parseRawFloat(desc.Min)
	hiV, hiOK := parseRawFloat(desc.Max)
	if !loOK || !hiOK {
		return 0, 0, false
	}
	return loV, hiV, true
}

// dataPointMultiplierReader is an internal adapter the Multiplier
// helper uses to fish a per-DP scaling factor out of a DataPoint
// without forcing every consumer to re-import generic. Every
// `*generic.DataPoint[T]` satisfies it (`unit_cleanup.go:111`).
type dataPointMultiplierReader interface {
	Multiplier() float64
}

// ParameterMultiplier returns the multiplier the named parameter's data point
// reports for unit cleanup. The second return value is true when the
// parameter exists and the multiplier differs from 1.0; consumers (sensor /
// number Discovery builders) skip the scaling step otherwise.
func (c *Channel) ParameterMultiplier(name string) (float64, bool) {
	if c == nil {
		return 0, false
	}
	dp := c.Parameter(hmenum.Parameter(name))
	if dp == nil {
		return 0, false
	}
	r, ok := dp.(dataPointMultiplierReader)
	if !ok {
		return 0, false
	}
	m := r.Multiplier()
	if m == 0 || m == 1.0 {
		return m, false
	}
	return m, true
}

// ParameterFloatValue returns the most recently observed value of the
// named VALUES-paramset parameter, coerced to float64. The second
// return value is true when the parameter exists, has been observed
// at least once, and the raw value is numeric. Used by the climate
// discovery builder to read the operator-configured
// TEMPERATURE_MINIMUM / TEMPERATURE_MAXIMUM bounds — these override
// the SET_POINT_TEMPERATURE descriptor range when set.
func (c *Channel) ParameterFloatValue(name string) (float64, bool) {
	if c == nil {
		return 0, false
	}
	dp := c.Parameter(hmenum.Parameter(name))
	if dp == nil {
		return 0, false
	}
	raw, observed := dp.RawValue()
	if !observed {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// parseRawFloat decodes a json.RawMessage as float64. Mirrors the
// `parseFloat` helper in [generic/bounds.go] — duplicated here to
// avoid an import cycle (device→generic would inverse the existing
// generic→device dependency).
func parseRawFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return f, true
}

// IsParameterInMultipleChannels reports whether more than one channel of the
// parent device exposes the given VALUES parameter.
func (c *Channel) IsParameterInMultipleChannels(parameter string) bool {
	if c == nil || c.device == nil {
		return false
	}
	count := 0
	for _, sibling := range c.device.Channels() {
		if sibling.HasParameter(parameter) {
			count++
			if count > 1 {
				return true
			}
		}
	}
	return false
}

// --- channel grouping & operation mode ----------------------------

// OperationMode returns the value of the CHANNEL_OPERATION_MODE data point as
// a string, or empty when the channel either does not expose the parameter or
// has not yet observed a value. Multi-mode channels (HmIP-MIO16-PCB,
// HmIP-Wired-Multi-IO, HmIP-FCI1/FCI6, …) use this to tell adapters which
// custom-DP variant to expose and which VALUES parameters the visibility gate
// must hide.
//
// Resolution order matches the Python fallback chain in
// `Channel.get_generic_data_point` (no paramset_key argument): 1. VALUES
// paramset — some firmware variants surface the mode live; if it carries an
// observed string we return it. 2. MASTER paramset — the canonical home of
// CHANNEL_OPERATION_MODE on standard CCU paramset descriptions.
//
// Returning the empty string is the "no observation yet" signal callers that
// need to gate visibility skip the gating step in that case.
func (c *Channel) OperationMode() string {
	// Read the value regardless of whether the DP carries a
	// forced_usage=NoCreate mark — the mark only governs whether the
	// DP surfaces in the UI / MQTT layer; the operation-mode gate
	// logic still needs the live value.
	if v := readChannelOperationModeFrom(c.Parameter(hmenum.ParameterChannelOperationMode)); v != "" {
		return v
	}
	return readChannelOperationModeFrom(c.MasterParameter(hmenum.ParameterChannelOperationMode))
}

// readChannelOperationModeFrom unwraps a [ParameterDataPoint]'s raw
// value into a string. CCU firmwares disagree on the wire format —
// some send the enum label ("KEY_BEHAVIOR"), some the enum index
// (1). The latter is resolved through the descriptor's VALUE_LIST so
// callers always see the canonical label. Mirrors the Python
// reference implementation's `channel.operation_mode` which routes
// the observed value through `data_point.value`. Returns the empty
// string when no value has been observed yet — the Python reference
// implementation does not fall back to the descriptor's DEFAULT here,
// and neither do we.
func readChannelOperationModeFrom(dp ParameterDataPoint) string {
	if dp == nil {
		return ""
	}
	v, observed := dp.RawValue()
	if !observed || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return resolveValueListIndex(dp.ParameterData(), int(x))
	case int:
		return resolveValueListIndex(dp.ParameterData(), x)
	case int32:
		return resolveValueListIndex(dp.ParameterData(), int(x))
	case int64:
		return resolveValueListIndex(dp.ParameterData(), int(x))
	}
	return ""
}

// resolveValueListIndex returns the enum label at index i in the
// descriptor's VALUE_LIST, or the empty string when the index is out
// of range or the list is missing.
func resolveValueListIndex(desc hmproto.ParameterData, i int) string {
	if i < 0 || i >= len(desc.ValueList) {
		return ""
	}
	return desc.ValueList[i]
}

// IsGroupMaster reports whether this channel is the master of its
// channel group.
// Channels outside any group (GroupNo == 0) report false.
func (c *Channel) IsGroupMaster() bool {
	return c.GroupNo != 0 && c.GroupNo == c.Number
}

// IsCustomDPPrimaryChannel reports whether this channel hosts a custom data
// point AND is the primary (group-master) channel of its channel group. Used
// by the HA-Discovery name builder to decide between "ch<N>" (primary) and
// "vch<N>" (secondary) suffixes.
func (c *Channel) IsCustomDPPrimaryChannel() bool {
	if c == nil || c.CustomDataPoint() == nil {
		return false
	}
	// Channels outside any group (GroupNo == 0) but carrying a
	// custom-DP are still treated as primary — Python's
	// `is_only_primary_channel` would flag them the same way (single
	// CDP, no group structure).
	if c.GroupNo == 0 {
		return true
	}
	return c.GroupNo == c.Number
}

// IsCustomDPSecondaryChannel reports whether this channel hosts a
// custom data point AND is a secondary channel of its group (not the
// group master). Used by the HA-Discovery name builder to apply the
// `vch<N>` suffix and `enabled_by_default: false` flag — mirroring
// `support.py::get_custom_data_point_name` (model/support.py:466)
// and `data_point.py::EnabledByDefault` (model/data_point.py:399).
func (c *Channel) IsCustomDPSecondaryChannel() bool {
	if c == nil || c.CustomDataPoint() == nil {
		return false
	}
	return c.GroupNo != 0 && c.GroupNo != c.Number
}

// haComponentProvider is the narrow contract every custom-DP that
// participates in HA-Discovery satisfies via its
// `payload.HAEntity.HAComponent` method ("climate" / "cover"
// "lock" / "light" / "switch" / "siren" / "valve" / …). Defined
// here as a private interface so the [device] package stays free
// of an import on `payload` (which would create a cycle through
// the `custom` family).
type haComponentProvider interface {
	HAComponent() string
}

// IgnoreMultipleChannelsForName reports whether the channel's
// custom DP opts out of the multi-primary ch<N> naming suffix.
// Lock returns true so multi-channel locks render as
// "<Lock>" / "<Lock>" instead of "<Lock> ch1" / "<Lock> ch2".
//
// Returns false when the channel has no custom DP or the DP does
// not implement the optional interface.
func (c *Channel) IgnoreMultipleChannelsForName() bool {
	if c == nil {
		return false
	}
	cdp := c.CustomDataPoint()
	if cdp == nil {
		return false
	}
	type ignorer interface {
		IgnoreMultipleChannelsForName() bool
	}
	if ig, ok := cdp.(ignorer); ok {
		return ig.IgnoreMultipleChannelsForName()
	}
	return false
}

// HasSinglePrimaryCustomDP reports whether this channel's parent
// device hosts exactly ONE primary custom-DP of the same HA component.
// This is the criterion that decides between "no channel suffix" (single
// primary in category) and "ch<N> suffix" (multiple primaries sharing
// the category).
//
// Examples:
//   - HmIP-BWTH: one CLIMATE primary on ch1 → true → name="" → renders
//     just the device name.
//   - HmIP-PSM: one SWITCH primary on ch3 → true → name="" → renders
//     "Steckdose"; ch4/ch5 emit vch4/vch5 (secondary path).
//   - HmIP-BSL: TWO LIGHT primaries (ch8 + ch12) → false → both get
//     ch8/ch12 suffixes.
//
// Returns false when the channel has no parent device, no
// custom-DP, or its custom-DP does not expose HAComponent().
func (c *Channel) HasSinglePrimaryCustomDP() bool {
	if c == nil || c.device == nil {
		return false
	}
	cdp := c.CustomDataPoint()
	if cdp == nil {
		return false
	}
	hc, ok := cdp.(haComponentProvider)
	if !ok {
		return false
	}
	target := hc.HAComponent()
	if target == "" {
		return false
	}
	count := 0
	for _, sibling := range c.device.Channels() {
		if !sibling.IsCustomDPPrimaryChannel() {
			continue
		}
		sibCDP := sibling.CustomDataPoint()
		if sibCDP == nil {
			continue
		}
		sibHC, ok := sibCDP.(haComponentProvider)
		if !ok {
			continue
		}
		if sibHC.HAComponent() != target {
			continue
		}
		count++
		if count > 1 {
			return false
		}
	}
	return count == 1
}

// GroupNumber returns this channel's channel-group number (`Channel.GroupNo`)
// as a method so adapters consuming the channel via a narrow inspector
// interface can read it without depending on the struct field directly.
func (c *Channel) GroupNumber() int {
	if c == nil {
		return 0
	}
	return c.GroupNo
}

// IsInMultiGroup reports whether the channel belongs to a channel group that
// carries more than one channel — i.e. the channel will participate in a
// sub-device split. Singleton-member groups return false (their sub-device
// would carry only the single channel's DP, which is the same view the flat
// parent already provides).
func (c *Channel) IsInMultiGroup() bool {
	if c == nil || c.GroupNo == 0 || c.device == nil {
		return false
	}
	return len(c.device.GroupChannels(c.GroupNo)) > 1
}

// SubDeviceName returns the name to use for the channel's logical sub-device
// when the parent device is exposed as multiple sub-devices (MQTT discovery
// `sub_devices_enabled`). Resolution order:
//
//  1. Group master not resolvable → empty string (caller falls back to the
//     parent device name).
//  2. Master.Name is purely numeric → "<device.Name>-<master.Name>"
//     (numeric labels carry no information on their own).
//  3. Master.Name is a non-empty, non-numeric string → master.Name.
//  4. Master.Name is empty → "<device.Name>-<group_no>".
func (c *Channel) SubDeviceName() string {
	if c == nil {
		return ""
	}
	master := c.GroupMaster()
	if master == nil {
		return ""
	}
	deviceName := ""
	if c.device != nil {
		deviceName = c.device.Name
	}
	masterName := master.Name()
	if isNumericString(masterName) {
		if deviceName == "" {
			return masterName
		}
		return deviceName + "-" + masterName
	}
	if masterName != "" {
		return masterName
	}
	if deviceName == "" {
		return itoa(master.GroupNo)
	}
	return deviceName + "-" + itoa(master.GroupNo)
}

// isNumericString reports whether s consists exclusively of ASCII digits and
// is non-empty. Mirrors Python's `str.isnumeric()` for the common case
// (CCU channel names are pure ASCII).
func isNumericString(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// GroupMaster returns the master channel of this channel's group, or nil when
// no group is assigned or the master channel cannot be located on the parent
// device. The receiver itself is returned when it is the group master.
func (c *Channel) GroupMaster() *Channel {
	if c.GroupNo == 0 {
		return nil
	}
	if c.IsGroupMaster() {
		return c
	}
	if c.device == nil {
		return nil
	}
	deviceAddr := c.Address
	if i := indexOfColon(deviceAddr); i >= 0 {
		deviceAddr = deviceAddr[:i]
	}
	masterAddr := deviceAddr + ":" + itoa(c.GroupNo)
	return c.device.Channel(masterAddr)
}

// Room returns the single assigned room name, falling back to the group
// master's room when this channel itself has none. Returns the empty string
// when no unique room can be resolved (multi-room assignments yield empty per
// Python parity).
func (c *Channel) Room() string {
	if rooms := c.Rooms(); len(rooms) == 1 {
		return rooms[0]
	}
	if c.IsGroupMaster() {
		return ""
	}
	if master := c.GroupMaster(); master != nil && master != c {
		return master.Room()
	}
	return ""
}

// OnLinkPeerChanged registers a callback fired by [NotifyLinkPeerChanged]
// whenever this channel's link peer set changes (add or remove). The returned
// closure unsubscribes idempotently.
func (c *Channel) OnLinkPeerChanged(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	c.linkPeerMu.Lock()
	c.linkPeerHandlers = append(c.linkPeerHandlers, fn)
	idx := len(c.linkPeerHandlers) - 1
	c.linkPeerMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.linkPeerMu.Lock()
			defer c.linkPeerMu.Unlock()
			if idx < len(c.linkPeerHandlers) {
				c.linkPeerHandlers[idx] = nil
			}
		})
	}
}

// NotifyLinkPeerChanged fires every registered link-peer-changed handler.
// Called by the link coordinator after AddLink / RemoveLink resolves
// successfully so subscribers (e.g. group thermostat custom-DPs that need to
// recompute capabilities) can react.
func (c *Channel) NotifyLinkPeerChanged() {
	if c == nil {
		return
	}
	c.linkPeerMu.Lock()
	handlers := make([]func(), len(c.linkPeerHandlers))
	copy(handlers, c.linkPeerHandlers)
	c.linkPeerMu.Unlock()
	for _, h := range handlers {
		if h != nil {
			h()
		}
	}
}

// indexOfColon is a tiny inlined `strings.Index(s, ":")` so we don't
// pull strings into this file.
func indexOfColon(s string) int {
	for i := range len(s) {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// itoa is a tiny inlined `strconv.Itoa` for non-negative ints; the
// channel group numbers we deal with never exceed two digits.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// AttachWeekProfile installs the channel's week-profile descriptor.
// Called by the device pipeline once it has detected schedule slots
// in the MASTER paramset. Replaces any previously attached profile.
// Pass nil to detach (uncommon — channels typically keep their
// profile for the lifetime of the daemon).
func (c *Channel) AttachWeekProfile(wp *weekprofile.ProfileDataPoint) {
	c.mu.Lock()
	c.weekProfile = wp
	c.mu.Unlock()
}

// WeekProfile returns the attached week-profile descriptor or nil
// when the channel has no schedule. North-bound consumers (REST
// MQTT-Discovery / UI) use the returned DP as the single canonical
// schedule entity for this channel — the slot-level MASTER
// parameters (P1_*..P6_*) are deliberately filtered out of the
// hydrated DPs and are only accessible through this descriptor's
// read / write surface (see [internal/model/weekprofile]).
func (c *Channel) WeekProfile() *weekprofile.ProfileDataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.weekProfile
}

// HasWeekProfile reports whether [AttachWeekProfile] has installed a
// non-nil descriptor. The channel-level shortcut for
// [Device.HasWeekProfile].
func (c *Channel) HasWeekProfile() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.weekProfile != nil
}

// IsScheduleChannel reports whether this channel carries a week-profile
// schedule.
//
// The Go equivalent delegates to [HasWeekProfile]: a channel is a schedule
// channel exactly when the pipeline's schedule-detection pass has attached a
// [weekprofile.ProfileDataPoint] to it.
func (c *Channel) IsScheduleChannel() bool {
	return c.HasWeekProfile()
}

// GetEvents returns every generic event source attached to the channel.
//
// return tuple(self._generic_events.values())
func (c *Channel) GetEvents() []AttachableEvent {
	return c.GenericEvents()
}

// configChangedDP is an optional interface implemented by data points that
// need to react when the channel's device configuration is reloaded. This
// covers generic DPs, generic events, calculated DPs, combined DPs, and
// custom DPs — all of which Python's Channel.on_config_changed iterates.
type configChangedDP interface {
	OnConfigChanged(ctx context.Context) error
}

// OnConfigChanged re-binds all derived data points and events after a
// device configuration reload. It is the Go counterpart to Python's
// Channel.on_config_changed (model/device.py):
//
//  1. Re-reads the MASTER paramset description via Channel.Refresh.
//  2. Fires NotifyLinkPeerChanged so WireClimateLinkPeerRefresh can
//     re-fetch link peers for this channel.
//  3. Calls OnConfigChanged on every generic DP, generic event,
//     calculated DP, and custom DP that implements the optional hook.
//
// Steps 1 and 2 are skipped when the corresponding dependency (refresher /
// link-peer handlers) is not wired — test fixtures or pre-bootstrap channels
// remain valid callers.
//
// Returns the first non-nil error; callers should log it but may continue
// reloading remaining channels.
func (c *Channel) OnConfigChanged(ctx context.Context) error {
	// Step 1: reload MASTER paramset description.
	if err := c.Refresh(ctx, hmenum.ParamsetKeyMaster); err != nil {
		if !errors.Is(err, ErrNoChannelRefresher) {
			return err
		}
	}

	// Step 2: trigger link-peer refresh so subscriptions stay current.
	c.NotifyLinkPeerChanged()

	// Step 3: collect all configurable DPs under the read lock, then
	// call their hooks outside the lock to avoid deadlocks.
	c.mu.RLock()
	var dps []configChangedDP
	for _, dp := range c.valuePoints {
		if cd, ok := dp.(configChangedDP); ok {
			dps = append(dps, cd)
		}
	}
	for _, dp := range c.masterPoints {
		if cd, ok := dp.(configChangedDP); ok {
			dps = append(dps, cd)
		}
	}
	for _, ev := range c.genericEvents {
		if cd, ok := ev.(configChangedDP); ok {
			dps = append(dps, cd)
		}
	}
	for _, dp := range c.calculatedDPs {
		if cd, ok := dp.(configChangedDP); ok {
			dps = append(dps, cd)
		}
	}
	var customCD configChangedDP
	if c.customDP != nil {
		customCD, _ = c.customDP.(configChangedDP)
	}
	c.mu.RUnlock()

	for _, dp := range dps {
		if err := dp.OnConfigChanged(ctx); err != nil {
			return err
		}
	}
	if customCD != nil {
		if err := customCD.OnConfigChanged(ctx); err != nil {
			return err
		}
	}
	return nil
}

// FinalizeInit must be called after all data points, events, and custom DPs
// have been attached to the channel — typically at the end of the channel
// hydration step in the device coordinator. It closes the channel lifecycle by
// building the event groups from the registered generic events.
//
// centralName scopes the resulting group UniqueIDs; pass an empty string in
// tests that do not require multi-CCU scoping.
//
// Mirrors Channel.finalize_init (model/device.py) which calls
// event_group.finalize_init() for each event group after all registrations.
func (c *Channel) FinalizeInit(centralName string) {
	c.BuildEventGroups(centralName)
}

// EventGroups returns every event group built for this channel, sorted by kind.
// Returns nil when no generic events have been attached or
// [BuildEventGroups] has not been called yet.
//
// event_groups: Final = DelegatedProperty[dict[...ChannelEventGroup]](device.py:1093).
func (c *Channel) EventGroups() []*modevent.Group {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.eventGroups) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(c.eventGroups))
	for k := range c.eventGroups {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	out := make([]*modevent.Group, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, c.eventGroups[k])
	}
	return out
}

// EventGroupForKind returns the event group for kind k, or nil when none exists.
func (c *Channel) EventGroupForKind(k modevent.Kind) *modevent.Group {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.eventGroups[string(k)]
}

// GetFilteredEvents returns the generic event sources that match the optional
// filters.
//
// eventKind: when non-empty only sources whose EventKind() equals eventKind
// are included. This mirrors the `event_type` parameter of
// Channel.get_events (device.py:1302).
//
// registered: when non-nil only sources whose IsRegistered() matches
// *registered are included.
func (c *Channel) GetFilteredEvents(eventKind string, registered *bool) []AttachableEvent {
	all := c.GenericEvents()
	out := make([]AttachableEvent, 0, len(all))
	for _, ev := range all {
		if eventKind != "" && ev.EventKind() != eventKind {
			continue
		}
		if registered != nil {
			if rd, ok := ev.(registeredDP); !ok || rd.IsRegistered() != *registered {
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}

// GetChannelDataPoints returns the calculated + custom data points on this
// channel that match the optional category, excludeNoCreate, and registered
// filters.
//
// This mirrors Channel.get_data_points(*, category=None, exclude_no_create=True,
// registered=None) (device.py:1277) — a per-channel counterpart to
// Device.GetDataPoints.
func (c *Channel) GetChannelDataPoints(category hmenum.DataPointCategory, excludeNoCreate bool, registered *bool) []AttachableDataPoint {
	var all []AttachableDataPoint
	if cdp := c.CustomDataPoint(); cdp != nil {
		all = append(all, cdp)
	}
	all = append(all, c.CalculatedDataPoints()...)
	out := make([]AttachableDataPoint, 0, len(all))
	for _, dp := range all {
		if matchesGetDataPointsFilter(dp, category, excludeNoCreate, registered) {
			out = append(out, dp)
		}
	}
	return out
}

// GetGenericDataPoint returns the VALUES-paramset data point for parameter p,
// or nil when not present.
func (c *Channel) GetGenericDataPoint(p hmenum.Parameter) ParameterDataPoint {
	return c.Parameter(p)
}

// GetGenericDataPointByStatePath returns the first VALUES-paramset data point
// whose StatePath() equals path. Returns nil when no match is found.
//
// Mirrors the state_path branch in Channel.get_generic_data_point
// (model/device.py) which looks up via _state_path_to_dpk. The Go
// implementation performs a linear scan because no dedicated path→key map is
// maintained at the channel level.
func (c *Channel) GetGenericDataPointByStatePath(path string) ParameterDataPoint {
	if path == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, dp := range c.valuePoints {
		if pp, ok := dp.(DataPointProvider); ok && pp.StatePath() == path {
			return dp
		}
	}
	return nil
}

// UniqueID returns a stable identifier for this channel in the form
// "<device_address>_<channel_number>".
//
// return generate_channel_unique_id(self.address, self.no)
//
// Used by north-bound adapters (MQTT Discovery, REST) that need a per-channel
// identity token separate from the raw address string.
func (c *Channel) UniqueID() string {
	if c == nil {
		return ""
	}
	// Extract device address (everything before the last ":N" suffix).
	addr := c.Address
	if i := indexOfColon(addr); i >= 0 {
		addr = addr[:i]
	}
	return addr + "_" + itoa(c.Number)
}

// ParamsetParameter looks up a data point in the given paramset.
func (c *Channel) ParamsetParameter(key hmenum.ParamsetKey, p hmenum.Parameter) ParameterDataPoint {
	switch key { //nolint:exhaustive // only VALUES + MASTER are stored on channels today
	case hmenum.ParamsetKeyValues:
		return c.Parameter(p)
	case hmenum.ParamsetKeyMaster:
		return c.MasterParameter(p)
	}
	return nil
}

// ─── FullName ─────────────────────────────────────────────────────────

// FullName returns the device-prefixed full name for this channel. Equivalent
// to the DataPointFullName with an empty parameter.
//
// full_name: Final = DelegatedProperty[str](path="_name_data.full_name")
func (c *Channel) FullName() string {
	return c.DataPointFullName("")
}

// ─── NameData ─────────────────────────────────────────────────────────

// NameData returns the [naming.NameData] for this channel with an empty
// parameter slot — the channel-level name quadruple.
func (c *Channel) NameData() naming.NameData {
	if c == nil {
		return naming.EmptyNameData
	}
	return BuildDataPointName(c, "", "")
}

// ─── TypeTranslation ──────────────────────────────────────────────────

// TypeTranslation returns the CCU/OCCU human-readable translation of the
// channel type, e.g. "Heizungsregler Transceiver" for
// HEATING_CLIMATECONTROL_TRANSCEIVER. Falls back to [Channel.Type] when no
// translation has been loaded.
func (c *Channel) TypeTranslation() string {
	c.mu.RLock()
	t := c.typeTranslation
	c.mu.RUnlock()
	if t == "" {
		return c.Type
	}
	return t
}

// SetTypeTranslation records the CCU-translations label for this
// channel's type. Called by the ingest pipeline after loading the
// translations catalogue.
func (c *Channel) SetTypeTranslation(t string) {
	c.mu.Lock()
	c.typeTranslation = t
	c.mu.Unlock()
}

// ─── LinkPeerSourceCategories ────────────────────────────────────────

// LinkPeerSourceCategories returns the DP-category strings describing what
// this channel can act as a source for in a central link. Returns nil when
// not set.
func (c *Channel) LinkPeerSourceCategories() []string {
	c.mu.RLock()
	cats := c.linkPeerSourceCategories
	c.mu.RUnlock()
	if len(cats) == 0 {
		return nil
	}
	out := make([]string, len(cats))
	copy(out, cats)
	return out
}

// SetLinkPeerSourceCategories records the source categories for central
// link management. Called by the ingest pipeline.
func (c *Channel) SetLinkPeerSourceCategories(cats []string) {
	c.mu.Lock()
	c.linkPeerSourceCategories = append([]string(nil), cats...)
	c.mu.Unlock()
}

// HasLinkSourceCategory reports whether category is listed in the channel's
// link source categories. Returns false when the channel has no source
// categories recorded.
func (c *Channel) HasLinkSourceCategory(category hmenum.DataPointCategory) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	cats := c.linkPeerSourceCategories
	c.mu.RUnlock()
	return slices.Contains(cats, string(category))
}

// ─── LinkPeerTargetCategories ────────────────────────────────────────

// LinkPeerTargetCategories returns the DP-category strings describing what
// this channel can act as a target for in a central link. Returns nil when
// not set.
func (c *Channel) LinkPeerTargetCategories() []string {
	c.mu.RLock()
	cats := c.linkPeerTargetCategories
	c.mu.RUnlock()
	if len(cats) == 0 {
		return nil
	}
	out := make([]string, len(cats))
	copy(out, cats)
	return out
}

// SetLinkPeerTargetCategories records the target categories for central
// link management. Called by the ingest pipeline.
func (c *Channel) SetLinkPeerTargetCategories(cats []string) {
	c.mu.Lock()
	c.linkPeerTargetCategories = append([]string(nil), cats...)
	c.mu.Unlock()
}

// HasLinkTargetCategory reports whether category is listed in the channel's
// link target categories. Returns false when the channel has no target
// categories recorded.
func (c *Channel) HasLinkTargetCategory(category hmenum.DataPointCategory) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	cats := c.linkPeerTargetCategories
	c.mu.RUnlock()
	return slices.Contains(cats, string(category))
}

// ─── LinkRoles (raw CCU LINK_*_ROLES) ────────────────────────────────

// LinkSourceRoles returns the raw CCU LINK_SOURCE_ROLES tokens of this
// channel (what it can act as a source for). Returns nil when not set.
func (c *Channel) LinkSourceRoles() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	roles := c.linkSourceRoles
	c.mu.RUnlock()
	if len(roles) == 0 {
		return nil
	}
	return append([]string(nil), roles...)
}

// LinkTargetRoles returns the raw CCU LINK_TARGET_ROLES tokens of this
// channel (what it can act as a target for). Returns nil when not set.
func (c *Channel) LinkTargetRoles() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	roles := c.linkTargetRoles
	c.mu.RUnlock()
	if len(roles) == 0 {
		return nil
	}
	return append([]string(nil), roles...)
}

// SetLinkRoles records the raw CCU LINK_SOURCE_ROLES / LINK_TARGET_ROLES
// tokens for direct-link role matching. Called by the ingest pipeline.
func (c *Channel) SetLinkRoles(source, target []string) {
	c.mu.Lock()
	c.linkSourceRoles = append([]string(nil), source...)
	c.linkTargetRoles = append([]string(nil), target...)
	c.mu.Unlock()
}

// SetOperatorFlags records the daemon-owned per-channel overrides (G12).
// Called by the ingest pipeline from the persistent channel_flags overlay
// and by the REST/WS handler after an operator change.
func (c *Channel) SetOperatorFlags(hidden, locked bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.operatorHidden = hidden
	c.operatorLocked = locked
	c.mu.Unlock()
}

// IsHidden reports whether an operator has hidden this channel from the
// operation lists / MQTT / Matter surfaces (G12).
func (c *Channel) IsHidden() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.operatorHidden
}

// IsLocked reports whether an operator has locked this channel against
// control writes (G12).
func (c *Channel) IsLocked() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.operatorLocked
}

// ─── LinkPeers cache ──────────────────────────────────────────────────

// LinkPeers returns a copy of the most recently cached link peer
// addresses for this channel. Returns nil when no peers have been
// observed yet (i.e. before the first [hmevent.LinkPeerChangedEvent] is
// processed). Callers should treat a nil result and an empty slice the
// same way — both mean "no known peers".
//
// The cache is populated by [WireClimateLinkPeerRefresh] whenever it
// handles a [hmevent.LinkPeerChangedEvent], and is consumed by the
// recovery path of that same function so it can immediately re-wire
// climate activity subscriptions after a reconnect without waiting for
// the CCU to re-deliver topology pushes.
func (c *Channel) LinkPeers() []string {
	if c == nil {
		return nil
	}
	c.linkPeersMu.RLock()
	peers := c.linkPeers
	c.linkPeersMu.RUnlock()
	if len(peers) == 0 {
		return nil
	}
	out := make([]string, len(peers))
	copy(out, peers)
	return out
}

// SetLinkPeers caches the link peer addresses for this channel. Passing
// an empty or nil slice clears the cache (models "no peers configured").
// Thread-safe; called from [WireClimateLinkPeerRefresh] on every
// [hmevent.LinkPeerChangedEvent] for this channel's address.
func (c *Channel) SetLinkPeers(peers []string) {
	if c == nil {
		return
	}
	c.linkPeersMu.Lock()
	if len(peers) == 0 {
		c.linkPeers = nil
	} else {
		c.linkPeers = append([]string(nil), peers...)
	}
	c.linkPeersMu.Unlock()
}

// snapshotSorted builds a slice from a paramset map, sorted by
// parameter name for stable rendering.
func snapshotSorted(m map[hmenum.Parameter]ParameterDataPoint) []ParameterDataPoint {
	out := make([]ParameterDataPoint, 0, len(m))
	for _, dp := range m {
		out = append(out, dp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Parameter() < out[j].Parameter() })
	return out
}
