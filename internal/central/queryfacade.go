// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"fmt"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// InstallModeProvider is the minimal contract the [QueryFacade] uses
// to retrieve install-mode data points. Implemented by
// [*coordinators.HubCoordinator].
type InstallModeProvider interface {
	// InstallModeDPs returns all registered install-mode data points.
	InstallModeDPs() []*hub.InstallMode
}

// QueryFacade is the read-only aggregate view north-bound adapters
// use instead of reaching into every sub-package. Handlers depend on
// narrow interfaces; this facade is the concrete implementation a
// [*Unit] returns.
type QueryFacade struct {
	name        string
	devices     *registry.DeviceRegistry
	model       *registry.ModelRegistry
	health      *health.Tracker
	installMode InstallModeProvider
}

// NewQueryFacade constructs a facade bound to the given Unit
// components. Designed to be cheap — the caller may recreate it per
// request if the dependency graph evolves.
//
// loom:reachable:reason="used by REST/WS handler wiring in daemon.go to expose the per-central query surface"
func NewQueryFacade(name string, devices *registry.DeviceRegistry, model *registry.ModelRegistry, h *health.Tracker) *QueryFacade {
	return &QueryFacade{name: name, devices: devices, model: model, health: h}
}

// Name returns the central identifier.
func (q *QueryFacade) Name() string { return q.name }

// DeviceCount reports how many devices are registered.
func (q *QueryFacade) DeviceCount() int {
	if q.devices == nil {
		return 0
	}
	return q.devices.Len()
}

// Devices returns a lightweight device summary list.
func (q *QueryFacade) Devices() []registry.DeviceEntry {
	if q.devices == nil {
		return nil
	}
	return q.devices.List()
}

// ModelDevices returns every device of the domain model. The slice is
// a fresh snapshot; the devices themselves are shared live objects.
func (q *QueryFacade) ModelDevices() []*device.Device {
	if q.model == nil {
		return nil
	}
	return q.model.List()
}

// HealthSnapshot returns the tracker's current sample set.
func (q *QueryFacade) HealthSnapshot() []health.Component {
	if q.health == nil {
		return nil
	}
	return q.health.Snapshot()
}

// OverallHealth returns the composite verdict.
func (q *QueryFacade) OverallHealth() health.Status {
	if q.health == nil {
		return health.StatusUnknown
	}
	return q.health.Overall()
}

// ScheduleInfo holds schedule capability metadata for one device.
type ScheduleInfo struct {
	DeviceAddress          string
	DeviceName             string
	ScheduleChannelAddress string // empty when no schedule channel found
	HasSchedule            bool
}

// GetDataPoints returns all data points that can be created/registered,
// optionally filtered by interface. Pass an empty string to skip interface
// filtering.
func (q *QueryFacade) GetDataPoints(iface hmenum.Interface) []device.ParameterDataPoint {
	if q.model == nil {
		return nil
	}
	devs := q.model.List()
	out := make([]device.ParameterDataPoint, 0)
	for _, d := range devs {
		if iface != "" && d.Interface != iface {
			continue
		}
		out = append(out, d.AllDataPoints()...)
	}
	return out
}

// GetDataPointsByCategory returns data points matching a specific category.
func (q *QueryFacade) GetDataPointsByCategory(category hmenum.DataPointCategory) []device.AttachableDataPoint {
	if q.model == nil {
		return nil
	}
	devs := q.model.List()
	out := make([]device.AttachableDataPoint, 0)
	for _, d := range devs {
		out = append(out, d.DataPointsByCategory(category)...)
	}
	return out
}

// GetCustomDataPoint returns the custom DP for a device address and channel
// number.
func (q *QueryFacade) GetCustomDataPoint(deviceAddress string, channelNo int) device.AttachableDataPoint {
	if q.model == nil {
		return nil
	}
	d, ok := q.model.Get(deviceAddress)
	if !ok || d == nil {
		return nil
	}
	for _, ch := range d.Channels() {
		if ch.Number == channelNo {
			return ch.CustomDataPoint()
		}
	}
	return nil
}

// GetGenericDataPoint returns the generic data point identified by channel
// address and parameter, or nil when not found.
func (q *QueryFacade) GetGenericDataPoint(channelAddress string, p hmenum.Parameter) device.ParameterDataPoint {
	if q.model == nil || channelAddress == "" {
		return nil
	}
	// Derive device address from channel address (strip ":N" suffix).
	devAddr := deviceAddress(channelAddress)
	d, ok := q.model.Get(devAddr)
	if !ok || d == nil {
		return nil
	}
	return d.DataPoint(channelAddress, p)
}

// GetEventSources returns all [device.AttachableEvent] sources across all
// devices, optionally filtered by interface. Pass an empty string to skip
// interface filtering.
func (q *QueryFacade) GetEventSources(iface hmenum.Interface) []device.AttachableEvent {
	if q.model == nil {
		return nil
	}
	devs := q.model.List()
	out := make([]device.AttachableEvent, 0)
	for _, d := range devs {
		if iface != "" && d.Interface != iface {
			continue
		}
		for _, ch := range d.Channels() {
			out = append(out, ch.GenericEvents()...)
		}
	}
	return out
}

// GetEventGroup returns the first [device.AttachableEvent] matching the given
// channel address and optional parameter, or nil.
func (q *QueryFacade) GetEventGroup(channelAddress string, p hmenum.Parameter) device.AttachableEvent {
	if q.model == nil {
		return nil
	}
	devAddr := deviceAddress(channelAddress)
	d, ok := q.model.Get(devAddr)
	if !ok || d == nil {
		return nil
	}
	ch := d.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	for _, ev := range ch.GenericEvents() {
		if p == "" {
			return ev
		}
		// Match by the Parameter field in the DataPointKey struct.
		if hmenum.Parameter(ev.DataPointKey().Parameter) == p {
			return ev
		}
	}
	return nil
}

// GetEvents is an alias for [GetEventSources], matching the Python name
// `DeviceQueryFacade.get_events` (query_facade.py:249). Pass an empty
// string to skip interface filtering.
//
// Note: the Python implementation groups events by channel and accepts
// a DeviceTriggerEventType filter; the Go model does not yet have a
// ChannelEventGroup / DeviceTriggerEventType abstraction, so this alias
// returns the flat event list. The grouping dimension will be added when
// the model is extended with event-group semantics.
func (q *QueryFacade) GetEvents(iface hmenum.Interface) []device.AttachableEvent {
	return q.GetEventSources(iface)
}

// GetEventGroups is an alias for [GetEventGroup], matching the Python name
// `DeviceQueryFacade.get_event_groups` (query_facade.py:218). Returns a
// single matching event rather than a slice until ChannelEventGroup
// semantics are added to the model.
func (q *QueryFacade) GetEventGroups(channelAddress string, p hmenum.Parameter) device.AttachableEvent {
	return q.GetEventGroup(channelAddress, p)
}

// GetScheduleCapableDevices returns ScheduleInfo for every device that has a
// week profile.
func (q *QueryFacade) GetScheduleCapableDevices() []ScheduleInfo {
	if q.model == nil {
		return nil
	}
	devs := q.model.List()
	out := make([]ScheduleInfo, 0)
	for _, d := range devs {
		if !d.HasWeekProfile() {
			continue
		}
		info := ScheduleInfo{
			DeviceAddress: d.Address,
			DeviceName:    d.Name(),
		}
		// Find the first channel carrying a week profile.
		for _, ch := range d.Channels() {
			if ch.HasWeekProfile() {
				wp := ch.WeekProfile()
				info.ScheduleChannelAddress = ch.Address
				if wp != nil {
					info.HasSchedule = wp.Climate() != nil || wp.Simple() != nil
				}
				break
			}
		}
		// Also check the root channel.
		if info.ScheduleChannelAddress == "" {
			if root := d.RootChannel(); root != nil && root.HasWeekProfile() {
				wp := root.WeekProfile()
				info.ScheduleChannelAddress = root.Address
				if wp != nil {
					info.HasSchedule = wp.Climate() != nil || wp.Simple() != nil
				}
			}
		}
		out = append(out, info)
	}
	return out
}

// GetChannel returns the channel at channelAddress, searching across all
// registered devices. Returns nil when the address is unknown.
func (q *QueryFacade) GetChannel(channelAddress string) *device.Channel {
	if q.model == nil || channelAddress == "" {
		return nil
	}
	devAddr := deviceAddress(channelAddress)
	d, ok := q.model.Get(devAddr)
	if !ok || d == nil {
		return nil
	}
	return d.Channel(channelAddress)
}

// GetParameters returns all parameter names present in the given paramset key
// across the model registry, optionally filtered by operation flags. A zero
// ops value returns all parameters.
func (q *QueryFacade) GetParameters(paramsetKey hmenum.ParamsetKey, ops hmenum.Operations) []string {
	if q.model == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, d := range q.model.List() {
		for _, ch := range d.Channels() {
			for _, dp := range ch.ParamsetDataPoints(paramsetKey) {
				if ops != 0 {
					pd := dp.ParameterData()
					if pd.Operations&ops == 0 {
						continue
					}
				}
				seen[string(dp.Parameter())] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

// GetUnIgnoreCandidates returns un-ignore candidate pattern strings
// for the given paramset: three formats per parameter for VALUES,
// one full-format tranche for MASTER. See ADR 0015 for the format
// rationale.
//
// Candidates are exactly the DPs whose forced Usage is
// [hmenum.DataPointUsageIgnored] — the visibility-gate marker for
// statically suppressed parameters (IGNORED_PARAMETERS,
// HIDDEN_PARAMETERS, wildcard regex, channel-operation-mode mask).
// `NoCreate` (generic DPs consumed by an aggregating parent) and
// `CDPSecondary` (default-disabled HmIP replica channels surfaced
// via MQTT-Discovery `enabled_by_default: false`) are NOT candidates;
// see ADR 0015 for the rationale.
//
// Transport-scope parameters (CONFIG_PENDING, STICKY_UNREACH,
// UNREACH) are skipped — they exist on every device and toggling
// them per-parameter is meaningless.
//
// Output formats per parameter:
//
//	VALUES paramset (read | event operations):
//	  1. "PARAM"                            — simple name (any device/channel)
//	  2. "PARAM:VALUES@MODEL:all"           — wildcard channel for a model
//	  3. "PARAM:VALUES@MODEL:<channelNo>"   — specific channel
//
//	MASTER paramset (read operations):
//	  4. "PARAM:MASTER@MODEL:<channelNo>"   — specific channel
//
// The wildcard token "all" matches [visibility.UnIgnoreWildcard].
// A facade without a model registry returns nil rather than an empty
// slice, matching every other accessor here — callers distinguish "no
// model loaded yet" from "a model with nothing to offer".
func (q *QueryFacade) GetUnIgnoreCandidates(paramsetKey hmenum.ParamsetKey) []string {
	if q.model == nil {
		return nil
	}
	return q.collectUnIgnoreCandidates(paramsetKey).Patterns()
}

// GetUnIgnoreCandidateGroups returns the same candidate set as
// [GetUnIgnoreCandidates], grouped by (parameter, paramset) with the
// affected models, channels and the rule that hid each one.
//
// The flat form is the cross-product of parameter × model × channel in
// three redundant pattern formats: a 399-device fleet produces ~2800
// strings out of ~45 distinct parameters. The picker needs the 45, plus
// the scopes to drill into, so it groups here rather than re-deriving
// the structure from parsed strings in the browser.
//
// Passing several paramsets walks the model once and returns the groups
// of all of them, ordered by paramset then parameter name.
func (q *QueryFacade) GetUnIgnoreCandidateGroups(paramsetKeys ...hmenum.ParamsetKey) []visibility.CandidateGroup {
	if q.model == nil {
		return nil
	}
	return q.collectUnIgnoreCandidates(paramsetKeys...).Groups()
}

// collectUnIgnoreCandidates walks the model registry once and feeds
// every suppressed, un-ignorable data point into a collector. Both
// public candidate accessors share it so the flat and the grouped shape
// are always the same underlying set.
func (q *QueryFacade) collectUnIgnoreCandidates(paramsetKeys ...hmenum.ParamsetKey) *visibility.CandidateCollector {
	c := visibility.NewCandidateCollector()
	if q.model == nil || len(paramsetKeys) == 0 {
		return c
	}
	for _, d := range q.model.List() {
		for _, ch := range d.Channels() {
			operationMode := ch.OperationMode()
			for _, paramsetKey := range paramsetKeys {
				for _, dp := range ch.ParamsetDataPoints(paramsetKey) {
					if !isIgnoredDataPoint(dp) {
						continue
					}
					p := dp.Parameter()
					if visibility.IsIgnoredForUnIgnore(p) {
						continue
					}
					if !operationsMatchParamset(dp, paramsetKey) {
						continue
					}
					c.Add(visibility.ClassifyInput{
						Model:         d.Model,
						ChannelType:   ch.Type,
						ChannelNo:     ch.Number,
						Paramset:      paramsetKey,
						Parameter:     p,
						ParameterData: dp.ParameterData(),
						OperationMode: operationMode,
					}, d.Address)
				}
			}
		}
	}
	return c
}

// isIgnoredDataPoint reports whether dp carries
// `forcedUsage = Ignored` — i.e. the visibility gate suppressed it
// via one of the static rule sets. Only Ignored DPs are un-ignore
// candidates; see ADR 0015. DPs that do not expose ForcedUsage
// (defensive — every production type embeds BaseDataPointFields
// which does) are treated as not-Ignored.
func isIgnoredDataPoint(dp device.ParameterDataPoint) bool {
	u, ok := dp.(interface {
		ForcedUsage() (hmenum.DataPointUsage, bool)
	})
	if !ok {
		return false
	}
	usage, set := u.ForcedUsage()
	if !set {
		return false
	}
	return usage == hmenum.DataPointUsageIgnored
}

// operationsMatchParamset reports whether dp's OPERATIONS bitmask
// satisfies the per-paramset filter for un-ignore candidates:
//
//   - VALUES: READ | EVENT both set
//   - MASTER: READ set
//
// DPs that fail the filter are skipped — they would never be
// observable / event-emitting if un-ignored, so listing them is
// noise.
func operationsMatchParamset(dp device.ParameterDataPoint, paramsetKey hmenum.ParamsetKey) bool {
	ops := dp.ParameterData().Operations
	switch paramsetKey {
	case hmenum.ParamsetKeyValues:
		return ops.Has(hmenum.OperationsRead | hmenum.OperationsEvent)
	case hmenum.ParamsetKeyMaster:
		return ops.Has(hmenum.OperationsRead)
	default:
		return false
	}
}

// SetInstallModeProvider wires an [InstallModeProvider] so
// [GetInstallMode] can look up cached install-mode data points.
// Returns the receiver for chaining.
func (q *QueryFacade) SetInstallModeProvider(p InstallModeProvider) *QueryFacade {
	q.installMode = p
	return q
}

// InstallModeInfo carries the full install-mode state for one interface.
type InstallModeInfo struct {
	// Active reports whether install mode is currently enabled.
	Active bool
	// Remaining is the time-to-expiry of the pairing window. Zero when
	// install mode is inactive.
	Remaining time.Duration
	// Mode is the string label of the install-mode type..
	// this maps to the interface type (e.g. "HmIP-RF"). In Go we surface
	// the InterfaceID string directly.
	Mode string
}

// GetInstallMode returns the install-mode state for the interface identified
// by iface. Returns a non-nil error when the interface is not known to the
// wired [InstallModeProvider]. Returns (InstallModeInfo{}, nil) with
// Active=false when the interface is known but not in install mode.
func (q *QueryFacade) GetInstallMode(iface hmenum.Interface) (InstallModeInfo, error) {
	if q.installMode == nil {
		return InstallModeInfo{}, fmt.Errorf("install mode: no provider wired for interface %q", iface)
	}
	ifaceID := string(iface)
	dps := q.installMode.InstallModeDPs()
	for _, dp := range dps {
		if dp == nil || dp.InterfaceID != ifaceID {
			continue
		}
		enabled, rem, _ := dp.InstallState()
		return InstallModeInfo{
			Active:    enabled,
			Remaining: rem,
			Mode:      ifaceID,
		}, nil
	}
	return InstallModeInfo{}, fmt.Errorf("install mode: interface %q not registered", iface)
}

// GetInstallModeByID returns the install-mode remaining duration for the
// interface identified by interfaceID string. Returns (0, false) when
// no matching install-mode data point is registered or the interface is
// not in install mode.
//
// This is the original string-keyed variant kept for backward
// compatibility with internal callers that do not yet use the typed
// [GetInstallMode]. New code should prefer [GetInstallMode].
func (q *QueryFacade) GetInstallModeByID(interfaceID string) (remaining time.Duration, ok bool) {
	if q.installMode == nil {
		return 0, false
	}
	dps := q.installMode.InstallModeDPs()
	for _, dp := range dps {
		if dp == nil || dp.InterfaceID != interfaceID {
			continue
		}
		enabled, rem, observed := dp.InstallState()
		if !observed {
			return 0, false
		}
		return rem, enabled
	}
	return 0, false
}

// deviceAddress strips the ":N" channel suffix from a channel address
// to derive the parent device address. Returns the input unchanged when
// no colon is found (i.e. the input is already a device address).
func deviceAddress(channelAddress string) string {
	if idx := strings.LastIndex(channelAddress, ":"); idx >= 0 {
		return channelAddress[:idx]
	}
	return channelAddress
}
