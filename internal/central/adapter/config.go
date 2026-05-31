// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"sort"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// ConfigAdapter projects the parsed daemon config onto the sanitized
// [handlers.ConfigSnapshot] the REST endpoint returns.
type ConfigAdapter struct {
	source   *config.Config
	registry *central.Registry
}

// NewConfigAdapter constructs the adapter. source may be nil; the
// adapter then reports the zero snapshot.
func NewConfigAdapter(source *config.Config, registry *central.Registry) *ConfigAdapter {
	return &ConfigAdapter{source: source, registry: registry}
}

// SanitizedConfig implements handlers.ConfigReader.
func (a *ConfigAdapter) SanitizedConfig() handlers.ConfigSnapshot {
	snap := handlers.ConfigSnapshot{}
	if a.source != nil {
		snap.Locale = a.source.Locale
		snap.CallbackPorts = handlers.ConfigPorts{
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
		for i := range a.source.Centrals {
			cc := &a.source.Centrals[i]
			ifaceNames := make([]string, len(cc.Interfaces))
			for j, spec := range cc.Interfaces {
				ifaceNames[j] = spec.Name
			}
			snap.Centrals = append(snap.Centrals, handlers.ConfigCentral{
				Name:       cc.Name,
				Host:       cc.Host,
				Interfaces: ifaceNames,
			})
		}
	}
	return snap
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

// trackers returns every tracker the adapter should consult, in the
// order daemon-global first → centrals in registry order. The
// resulting slice is fresh per call so a concurrent registry mutation
// cannot leak into the iteration.
func (a *HealthAdapter) trackers() []*health.Tracker {
	out := make([]*health.Tracker, 0, 1)
	if a.fallback != nil {
		out = append(out, a.fallback)
	}
	if a.registry != nil {
		for _, c := range a.registry.List() {
			if c.Health != nil {
				out = append(out, c.Health)
			}
		}
	}
	return out
}

// Overall implements handlers.HealthReader. Returns the worst status
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

// Snapshot implements handlers.HealthReader. Unions the component
// lists across every tracker; duplicates (same component name in two
// trackers) keep the entry with the newest sample. The result is
// sorted alphabetically so the SPA renders a stable order.
func (a *HealthAdapter) Snapshot() []health.Component {
	seen := make(map[string]int)
	var out []health.Component
	for _, t := range a.trackers() {
		for _, c := range t.Snapshot() {
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

// ClientScore searches every tracker for a registered detail entry
// for name and returns the first match's score. Returns 0 when no
// tracker has seen the named client.
func (a *HealthAdapter) ClientScore(name string) float64 {
	for _, t := range a.trackers() {
		if _, ok := t.ClientDetail(name); ok {
			return t.ClientScore(name)
		}
	}
	return 0
}

// ClientDetail searches every tracker for a registered detail entry
// for name and returns the first match.
func (a *HealthAdapter) ClientDetail(name string) (health.ClientHealth, bool) {
	for _, t := range a.trackers() {
		if d, ok := t.ClientDetail(name); ok {
			return d, true
		}
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
		for k, v := range t.Gauges() {
			out[k] = v
		}
	}
	return out
}
