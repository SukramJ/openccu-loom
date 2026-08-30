// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package event

import (
	"slices"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Group aggregates every [Source] of the same [Kind] on a single
// channel. Home-Assistant-style consumers subscribe to the group
// once and receive every individual parameter's event.
//
// Group embeds [datapoint.BaseDataPointFields] so it satisfies
// [datapoint.BaseDataPoint] — mirroring
// which inherits from CallbackDataPoint (model/event.py:179). The
// promoted [datapoint.BaseDataPointFields.UniqueID]
// [datapoint.BaseDataPointFields.MarkRegistered]
// [datapoint.BaseDataPointFields.IsRegistered] surfaces are therefore
// available directly on Group. The canonical UniqueID shape is
// `<central>:<channelAddress>:event_group/<kind.Short>`.
type Group struct {
	datapoint.BaseDataPointFields

	ChannelAddress string
	Kind           Kind

	mu          sync.RWMutex
	sources     map[hmenum.Parameter]*Source
	callbacks   []func(Event)
	unsubs      []func()
	lastSource  *Source     // last source that fired; guarded by mu
	availableFn func() bool // optional availability delegate; guarded by mu
}

// NewGroup constructs an empty group bound to a channel and kind.
// The embedded [datapoint.BaseDataPointFields] is initialised with an
// empty central name (single-CCU-safe for tests and early wiring); callers
// that need multi-CCU scoping should use [NewGroupWithCentral].
//
// loom:reachable:reason="convenience wrapper for tests; production uses NewGroupWithCentral"
func NewGroup(channelAddress string, k Kind) *Group {
	return NewGroupWithCentral("", channelAddress, k)
}

// NewGroupWithCentral is the multi-CCU-safe constructor. The embedded
// [datapoint.BaseDataPointFields] carries the `central` scope so that
// the resulting [UniqueID] is `<central>:<channelAddress>:event_group/<kind>`.
// ADR 0002 requires production callers to set `central`.
func NewGroupWithCentral(centralName, channelAddress string, k Kind) *Group {
	return &Group{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(
			centralName,
			channelAddress,
			"event_group/"+string(k),
		),
		ChannelAddress: channelAddress,
		Kind:           k,
		sources:        make(map[hmenum.Parameter]*Source),
	}
}

// Category returns [hmenum.DataPointCategoryEventGroup].
func (g *Group) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategoryEventGroup
}

// CanonicalUniqueID returns the loom-namespaced routing key for this event
// group — the external HA-entity identity:
//
//	loom_event_group_<kind>_<channel>
//
// The serialSuffix (the CCU serial's last-10 lower suffix) is supplied by the
// north boundary and lands inside the channel slot for the address families
// that need it, not in front of the whole key.
//
// This is the reference layout, deliberately. The key used to be built from
// the internal key name — `loom_<channel>_event_group/homematic.keypress`,
// with a slash and the unshortened kind — which no consumer could use: the
// Python client recomputed the reference spelling itself rather than read the
// value this method feeds into `EventGroupSummary.unique_id`. A field that
// invites use and is wrong when used is worse than an absent one.
//
// Returns "" for a nil group or an unresolved serial.
func (g *Group) CanonicalUniqueID(serialSuffix string) string {
	if g == nil || serialSuffix == "" {
		return ""
	}
	return routingkey.EventGroupUniqueID(serialSuffix, g.ChannelAddress, g.TranslationKey())
}

// TranslationKey returns the slugified i18n lookup key for this group. It is
// derived from the group's [Kind] using the same rule as [Source.TranslationKey]
// so both event sources and groups resolve to the same display-name key in the
// translation catalogue.
//
// Examples: KindKeypress → "keypress", KindDeviceError → "device_error".
func (g *Group) TranslationKey() string {
	return GenerateTranslationKey(g.Kind)
}

// Usage returns the canonical [hmenum.DataPointUsage] for the group.
// A Group's usage mirrors that of its member sources: when at least one member
// is explicitly enabled by the channel-operation-mode gate the group as a whole
// exposes EventGroup usage. When no sources have been added yet, the default is
// also EventGroup — the group is visible until a membership update overrides it.
func (g *Group) Usage() hmenum.DataPointUsage {
	return hmenum.DataPointUsageEvent
}

// Add registers a Source with the group. The group re-emits the
// source's events to its own subscribers; sources not matching the
// group's Kind are rejected.
func (g *Group) Add(s *Source) bool {
	if s == nil || s.Kind != g.Kind || s.ChannelAddress != g.ChannelAddress {
		return false
	}
	g.mu.Lock()
	if _, exists := g.sources[s.Parameter]; exists {
		g.mu.Unlock()
		return false
	}
	g.sources[s.Parameter] = s
	unsub := s.OnFire(func(ev Event) {
		g.mu.Lock()
		g.lastSource = s
		g.mu.Unlock()
		g.dispatch(ev)
	})
	g.unsubs = append(g.unsubs, unsub)
	g.mu.Unlock()
	return true
}

// SetAvailableFunc installs an availability delegate called by
// [Group.Available]. When nil, [Available] always returns true. Typically
// installed by the device pipeline to delegate to [device.Device.Available].
//
// available: Final =
// DelegatedProperty[bool](path="_channel.device.available")
func (g *Group) SetAvailableFunc(fn func() bool) {
	g.mu.Lock()
	g.availableFn = fn
	g.mu.Unlock()
}

// Available reports whether the parent device is reachable. Delegates
// to the function registered by [SetAvailableFunc]; returns true when
// no function has been registered (test / detached mode).
func (g *Group) Available() bool {
	g.mu.RLock()
	fn := g.availableFn
	g.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn()
}

// LastTriggeredEvent returns the last Source that fired an event in this
// group, or nil when no event has been received yet.
//
// last_triggered_event: Final =
// DelegatedProperty[...](path="_last_triggered_event")
func (g *Group) LastTriggeredEvent() *Source {
	g.mu.RLock()
	s := g.lastSource
	g.mu.RUnlock()
	return s
}

// Parameters returns the registered source parameters sorted
// alphabetically.
func (g *Group) Parameters() []hmenum.Parameter {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]hmenum.Parameter, 0, len(g.sources))
	for p := range g.sources {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// EventTypes returns the lowercased parameter names of all registered sources
// in sorted order.
//
// This mirrors the `event_types` DelegatedProperty on ChannelEventGroup
// (model/event.py:257): `tuple(event.parameter.lower() for event in events)`.
// Home-Assistant consumers use this list as the canonical set of event type
// identifiers for an EventEntity.
func (g *Group) EventTypes() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.sources))
	for p := range g.sources {
		s := string(p)
		lower := make([]byte, len(s))
		for i := range len(s) {
			if s[i] >= 'A' && s[i] <= 'Z' {
				lower[i] = s[i] + 32
			} else {
				lower[i] = s[i]
			}
		}
		out = append(out, string(lower))
	}
	sort.Strings(out)
	return out
}

// Sources returns all registered [Source] values in this group, sorted by
// parameter name.
//
// events: Final = DelegatedProperty[tuple[GenericEventProtocolAny,
// ...]](path="_events")
//
// Returns nil when the group has no sources.
func (g *Group) Sources() []*Source {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.sources) == 0 {
		return nil
	}
	out := make([]*Source, 0, len(g.sources))
	for _, s := range g.sources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Parameter < out[j].Parameter })
	return out
}

// OnFire subscribes to every source's events. Returns an idempotent
// unsubscribe.
func (g *Group) OnFire(fn func(Event)) func() {
	g.mu.Lock()
	g.callbacks = append(g.callbacks, fn)
	idx := len(g.callbacks) - 1
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if idx < len(g.callbacks) {
				g.callbacks[idx] = nil
			}
		})
	}
}

// Close unsubscribes every attached source. The group may still be
// used — Add re-subscribes — but outstanding subscriptions are
// dropped to release references.
func (g *Group) Close() {
	g.mu.Lock()
	unsubs := g.unsubs
	g.unsubs = nil
	g.mu.Unlock()
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
}

func (g *Group) dispatch(ev Event) {
	g.mu.RLock()
	cbs := make([]func(Event), len(g.callbacks))
	copy(cbs, g.callbacks)
	g.mu.RUnlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(ev)
		}
	}
}
