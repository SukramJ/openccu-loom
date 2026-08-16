// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"maps"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ConfigAdapter projects the parsed daemon config onto the sanitized
// [hmapi.ConfigSnapshot] the REST endpoint returns.
type ConfigAdapter struct {
	source   *config.Config
	registry *central.Registry
}

// NewConfigAdapter constructs the adapter. source may be nil; the
// adapter then reports the zero snapshot.
func NewConfigAdapter(source *config.Config, registry *central.Registry) *ConfigAdapter {
	return &ConfigAdapter{source: source, registry: registry}
}

// SanitizedConfig implements interfaces.ConfigReader.
func (a *ConfigAdapter) SanitizedConfig() hmapi.ConfigSnapshot {
	snap := hmapi.ConfigSnapshot{}
	if a.source != nil {
		snap.Locale = a.source.Locale
		snap.CallbackPorts = hmapi.ConfigPorts{
			XMLRPC: a.source.Callback.Port,
			BINRPC: a.source.Callback.BinPort,
		}
		snap.Features = map[string]bool{
			"rest":         a.source.North.REST.IsEnabled(),
			"ui":           a.source.North.UI.IsEnabled(),
			"mqtt":         a.source.North.MQTT.Enabled,
			"raw_topics":   a.source.North.MQTT.RawEnabled,
			"ha_discovery": a.source.North.MQTT.DiscoveryEnabled,
		}
		// Static MVP policies. The daemon does not filter hub content
		// today; marker-based filtering at the consumer side stays a
		// client-side responsibility.
		snap.Policies = map[string]bool{
			"hub.programs.include_all":   true,
			"hub.sysvars.include_all":    true,
			"hub.devices.include_hidden": false,
		}
	}
	snap.Centrals = a.centrals()
	return snap
}

// centrals reports the per-central wiring the daemon actually serves.
//
// The registry is the authority, not the boot config: a CCU adopted or
// removed through the SPA never reaches `cfg.Centrals`, so reading the boot
// slice made this endpoint — documented as the *effective* configuration —
// go stale until the next daemon restart. Each central's host and interface
// list comes from its live client entries, and falls back to the boot row of
// the same name while the central is still waiting for its CCU (registered,
// nothing wired yet). Without a registry the boot list is all there is.
func (a *ConfigAdapter) centrals() []hmapi.ConfigCentral {
	byName := make(map[string]*config.CentralConfig)
	var bootOrder []hmapi.ConfigCentral
	if a.source != nil {
		for i := range a.source.Centrals {
			cc := &a.source.Centrals[i]
			byName[cc.Name] = cc
			bootOrder = append(bootOrder, hmapi.ConfigCentral{
				Name:       cc.Name,
				Host:       cc.Host,
				Interfaces: configuredInterfaceNames(cc),
			})
		}
	}
	if a.registry == nil {
		return bootOrder
	}
	units := a.registry.List()
	out := make([]hmapi.ConfigCentral, 0, len(units))
	for _, u := range units {
		if u == nil {
			continue
		}
		entry := hmapi.ConfigCentral{Name: u.Name()}
		if u.Clients != nil {
			for _, ce := range u.Clients.List() {
				if ce == nil {
					continue
				}
				if entry.Host == "" {
					entry.Host = ce.Host
				}
				entry.Interfaces = append(entry.Interfaces, string(ce.Interface))
			}
		}
		if len(entry.Interfaces) == 0 {
			if cc, ok := byName[entry.Name]; ok {
				entry.Host = cc.Host
				entry.Interfaces = configuredInterfaceNames(cc)
			}
		}
		out = append(out, entry)
	}
	return out
}

// configuredInterfaceNames projects a central's configured interface specs
// onto their bare names.
func configuredInterfaceNames(cc *config.CentralConfig) []string {
	names := make([]string, len(cc.Interfaces))
	for i, spec := range cc.Interfaces {
		names[i] = spec.Name
	}
	return names
}

// HealthAdapter aggregates the daemon-global health tracker plus every
// registered central's tracker into one logical view. Diagnostics
// endpoints (and the SPA) read through this adapter so the daemon-
// global producers (REST 5xx, WS subscribers, SQLite/MQTT/Matter
// probes) and the per-central producers (interface health, audit,
// event-bus, scheduler) appear side by side.
type HealthAdapter struct {
	registry *central.Registry
	fallback *health.Tracker
}

// NewHealthAdapter constructs the adapter. fallback carries the
// daemon-global tracker — its components and gauges are merged into
// every Snapshot / Score / Gauges call alongside the per-central
// trackers reachable through registry.
func NewHealthAdapter(r *central.Registry, fallback *health.Tracker) *HealthAdapter {
	if fallback == nil {
		fallback = health.NewTracker()
	}
	return &HealthAdapter{registry: r, fallback: fallback}
}

// scopedTracker pairs a tracker with the central it belongs to. The
// central name is empty for the daemon-global fallback tracker —
// its components (REST 5xx, WS subscribers, SQLite/MQTT probes) are
// daemon-wide resources and stay unscoped in the aggregated view.
type scopedTracker struct {
	central string
	tracker *health.Tracker
}

// scopedTrackers returns every tracker the adapter should consult, in
// the order daemon-global first → centrals in registry order. The
// resulting slice is fresh per call so a concurrent registry mutation
// cannot leak into the iteration.
func (a *HealthAdapter) scopedTrackers() []scopedTracker {
	out := make([]scopedTracker, 0, 1)
	if a.fallback != nil {
		out = append(out, scopedTracker{tracker: a.fallback})
	}
	if a.registry != nil {
		for _, u := range a.registry.List() {
			if u.Health != nil {
				out = append(out, scopedTracker{central: u.Name(), tracker: u.Health})
			}
		}
	}
	return out
}

// trackers returns the bare tracker list for callers that aggregate
// without per-component naming (Overall, PrimaryClientHealthy,
// CentralScoreInt, Gauges).
func (a *HealthAdapter) trackers() []*health.Tracker {
	scoped := a.scopedTrackers()
	out := make([]*health.Tracker, 0, len(scoped))
	for _, st := range scoped {
		out = append(out, st.tracker)
	}
	return out
}

// Overall implements restapi.HealthReader. Returns the worst status
// observed across every consulted tracker; unknown beats healthy when
// no degraded / unhealthy is in flight so a half-booted daemon does
// not advertise itself as green. Trackers with no components do NOT
// contribute an "unknown" vote — a freshly-constructed daemon-global
// fallback that has not seen a single Record yet would otherwise
// permanently drag the aggregate to unknown.
func (a *HealthAdapter) Overall() health.Status {
	worst := health.StatusHealthy
	hasUnknown := false
	hasAny := false
	for _, t := range a.trackers() {
		snap := t.Snapshot()
		if len(snap) == 0 {
			continue
		}
		hasAny = true
		s := t.Overall()
		switch s {
		case health.StatusUnhealthy:
			return health.StatusUnhealthy
		case health.StatusDegraded:
			worst = health.StatusDegraded
		case health.StatusUnknown:
			hasUnknown = true
		case health.StatusHealthy:
			// no escalation
		}
	}
	if !hasAny {
		return health.StatusUnknown
	}
	if worst == health.StatusHealthy && hasUnknown {
		return health.StatusUnknown
	}
	return worst
}

// Snapshot implements restapi.HealthReader. Unions the component
// lists across every tracker. Components from a central's tracker are
// scoped as `<central>/<component>` — two CCUs typically run the same
// interface names (HmIP-RF, BidCos-RF, the `central` heartbeat), and
// an unscoped union would collapse them to a single entry, hiding all
// but one CCU from the diagnostics view. Daemon-global fallback
// components stay bare. Same-name duplicates (only possible within
// the fallback after scoping) keep the entry with the newest sample.
// The result is sorted alphabetically so the SPA renders a stable
// order.
func (a *HealthAdapter) Snapshot() []health.Component {
	seen := make(map[string]int)
	var out []health.Component
	for _, st := range a.scopedTrackers() {
		for _, c := range st.tracker.Snapshot() {
			if st.central != "" {
				c.Name = st.central + "/" + c.Name
			}
			if idx, ok := seen[c.Name]; ok {
				if c.LastSample.Timestamp.After(out[idx].LastSample.Timestamp) {
					out[idx] = c
				}
				continue
			}
			seen[c.Name] = len(out)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Score implements ws.HealthSnapshotProvider. Computes the aggregate
// score across the unified Snapshot — every component contributes
// once regardless of which tracker reported it.
func (a *HealthAdapter) Score() float64 {
	snap := a.Snapshot()
	if len(snap) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range snap {
		switch c.Status {
		case health.StatusHealthy:
			total += 1.0
		case health.StatusDegraded:
			total += 0.5
		case health.StatusUnhealthy, health.StatusUnknown:
			// adds 0
		}
	}
	return total / float64(len(snap))
}

// ScoreInt mirrors [(*health.Tracker).ScoreInt] over the aggregated
// view.
func (a *HealthAdapter) ScoreInt() int { return int(a.Score() * 100) }

// IsAvailable reports whether the aggregated Overall is StatusHealthy.
func (a *HealthAdapter) IsAvailable() bool { return a.Overall() == health.StatusHealthy }

// IsDegraded reports whether the aggregated Overall is StatusDegraded.
func (a *HealthAdapter) IsDegraded() bool { return a.Overall() == health.StatusDegraded }

// IsFailed reports whether the aggregated Overall is StatusUnhealthy.
func (a *HealthAdapter) IsFailed() bool { return a.Overall() == health.StatusUnhealthy }

// PrimaryClientHealthy reports true when any tracker considers its
// primary client healthy. In single-central setups this matches the
// per-central tracker exactly; in multi-CCU setups it answers "is at
// least one CCU's hub interface up?".
func (a *HealthAdapter) PrimaryClientHealthy() bool {
	for _, t := range a.trackers() {
		if t.PrimaryClientHealthy() {
			return true
		}
	}
	return false
}

// resolveClientTracker resolves a (possibly scoped) component name to
// the tracker that owns it. `<central>/<component>` routes to exactly
// that central's tracker — the names [HealthAdapter.Snapshot] emits
// round-trip through here. Bare names keep the legacy first-match
// scan so callers predating the scoping do not break.
func (a *HealthAdapter) resolveClientTracker(name string) (*health.Tracker, string, bool) {
	if centralName, comp, scoped := strings.Cut(name, "/"); scoped {
		for _, st := range a.scopedTrackers() {
			if st.central == centralName {
				if _, ok := st.tracker.ClientDetail(comp); ok {
					return st.tracker, comp, true
				}
				return nil, "", false
			}
		}
		// No central by that name — fall through to the bare-name
		// scan: a daemon-global component may legitimately contain
		// a slash.
	}
	for _, t := range a.trackers() {
		if _, ok := t.ClientDetail(name); ok {
			return t, name, true
		}
	}
	return nil, "", false
}

// ClientScore resolves name (scoped or bare) to its owning tracker
// and returns that client's score. Returns 0 when no tracker has
// seen the named client.
func (a *HealthAdapter) ClientScore(name string) float64 {
	if t, comp, ok := a.resolveClientTracker(name); ok {
		return t.ClientScore(comp)
	}
	return 0
}

// ClientDetail resolves name (scoped or bare) to its owning tracker
// and returns that client's detail entry.
func (a *HealthAdapter) ClientDetail(name string) (health.ClientHealth, bool) {
	if t, comp, ok := a.resolveClientTracker(name); ok {
		return t.ClientDetail(comp)
	}
	return health.ClientHealth{}, false
}

// CentralScoreInt returns the score for the named central. Iterates
// every tracker so a single misplaced component (per-central probe
// writing into the daemon-global tracker, or vice versa) still
// contributes to the right CCU's verdict.
func (a *HealthAdapter) CentralScoreInt(name string) int {
	total := 0.0
	count := 0
	for _, t := range a.trackers() {
		s := t.CentralScore(name)
		if s == 0 {
			continue
		}
		total += s
		count++
	}
	if count == 0 {
		return 0
	}
	return int((total / float64(count)) * 100)
}

// Gauges returns the merged pull-gauge map from every consulted
// tracker. Duplicate keys (unlikely — gauge names are subsystem-
// prefixed) keep the value from the last tracker visited.
func (a *HealthAdapter) Gauges() map[string]float64 {
	out := map[string]float64{}
	for _, t := range a.trackers() {
		maps.Copy(out, t.Gauges())
	}
	return out
}
