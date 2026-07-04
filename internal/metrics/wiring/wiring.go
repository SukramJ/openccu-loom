// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package wiring connects the production providers in `internal/client`,
// `internal/central/coordinators`, `internal/health`, and the bus in
// `internal/central/events` to the aggregator and observer in
// `internal/metrics`.
//
// The wiring lives in its own package to avoid import cycles: the
// metrics aggregator only depends on the small, pure provider
// interfaces in `internal/metrics/protocols.go`; concrete adapters
// that import both metrics and the production packages live here.
//
// This package has no exported types beyond the adapter functions —
// callers wire it up at central-construction time and never reference
// the adapters again.
package wiring

import (
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// ClientProvider adapts a *client.MetricsClientProvider to the
// metrics.ClientForMetrics protocol. The adapter is a thin shim that
// re-exports the per-client snapshot under the metrics package's
// type system without forcing internal/client to import
// internal/metrics.
type ClientProvider struct {
	src *client.MetricsClientProvider
}

// NewClientProvider returns a new ClientProvider scoped to the given
// MetricsClientProvider. Pass nil to get a provider that returns an
// empty client list.
func NewClientProvider(src *client.MetricsClientProvider) *ClientProvider {
	return &ClientProvider{src: src}
}

// Clients implements metrics.ClientForMetrics.
func (p *ClientProvider) Clients() []metrics.InterfaceClientMetrics {
	if p.src == nil {
		return nil
	}
	snap := p.src.Snapshot()
	out := make([]metrics.InterfaceClientMetrics, 0, len(snap))
	for _, ic := range snap {
		out = append(out, clientAdapter{ic: ic})
	}
	return out
}

// clientAdapter wraps an *client.InterfaceClient and surfaces its
// Metrics* accessors as metrics.InterfaceClientMetrics.
type clientAdapter struct {
	ic *client.InterfaceClient
}

// TotalRequests implements metrics.InterfaceClientMetrics.
func (c clientAdapter) TotalRequests() int { return c.ic.MetricsTotalRequests() }

// PendingRequests implements metrics.InterfaceClientMetrics.
func (c clientAdapter) PendingRequests() int { return c.ic.MetricsPendingRequests() }

// ExecutedRequests implements metrics.InterfaceClientMetrics.
func (c clientAdapter) ExecutedRequests() int { return c.ic.MetricsExecutedRequests() }

// CircuitState implements metrics.InterfaceClientMetrics.
func (c clientAdapter) CircuitState() int { return c.ic.MetricsCircuitState() }

// LastFailureTime implements metrics.InterfaceClientMetrics. The
// metrics protocol intentionally types the return as *interface{} to
// avoid importing time; the aggregator type-asserts back to
// time.Time. Returns nil when no failure has been recorded.
func (c clientAdapter) LastFailureTime() *any { //nolint:gocritic // *interface{} is the protocol contract; see metrics.InterfaceClientMetrics
	t, ok := c.ic.MetricsLastFailureTime()
	if !ok {
		return nil
	}
	var v any = t
	return &v
}

// CacheProvider adapts a *coordinators.CacheCoordinator to the
// metrics.CacheProviderForMetrics protocol.
type CacheProvider struct {
	src *coordinators.CacheCoordinator
}

// NewCacheProvider returns a CacheProvider for src. Nil src yields a
// provider that reports zeros.
func NewCacheProvider(src *coordinators.CacheCoordinator) *CacheProvider {
	return &CacheProvider{src: src}
}

// DataCacheSize implements metrics.CacheProviderForMetrics.
func (p *CacheProvider) DataCacheSize() int {
	if p.src == nil {
		return 0
	}
	return p.src.MetricsDataCacheSize()
}

// DataCacheStats implements metrics.CacheProviderForMetrics.
func (p *CacheProvider) DataCacheStats() metrics.CacheStatsSnapshot {
	if p.src == nil {
		return metrics.CacheStatsSnapshot{}
	}
	return metrics.CacheStatsSnapshot{
		Hits:      p.src.MetricsDataCacheHits(),
		Misses:    p.src.MetricsDataCacheMisses(),
		Size:      p.src.MetricsDataCacheSize(),
		Evictions: p.src.MetricsDataCacheEvictions(),
	}
}

// DeviceDescriptionsSize implements metrics.CacheProviderForMetrics.
func (p *CacheProvider) DeviceDescriptionsSize() int {
	if p.src == nil {
		return 0
	}
	return p.src.MetricsDeviceDescriptionsSize()
}

// ParamsetDescriptionsSize implements metrics.CacheProviderForMetrics.
func (p *CacheProvider) ParamsetDescriptionsSize() int {
	if p.src == nil {
		return 0
	}
	return p.src.MetricsParamsetDescriptionsSize()
}

// VisibilityCacheSize implements metrics.CacheProviderForMetrics.
func (p *CacheProvider) VisibilityCacheSize() int {
	if p.src == nil {
		return 0
	}
	return p.src.MetricsVisibilityCacheSize()
}

// RecoveryProvider adapts a *coordinators.ConnectionRecoveryCoordinator
// to the metrics.RecoveryProviderForMetrics protocol.
type RecoveryProvider struct {
	src *coordinators.ConnectionRecoveryCoordinator
}

// NewRecoveryProvider returns a RecoveryProvider for src.
func NewRecoveryProvider(src *coordinators.ConnectionRecoveryCoordinator) *RecoveryProvider {
	return &RecoveryProvider{src: src}
}

// InRecovery implements metrics.RecoveryProviderForMetrics.
func (p *RecoveryProvider) InRecovery() bool {
	if p.src == nil {
		return false
	}
	return p.src.MetricsInRecovery()
}

// RecoveryStates implements metrics.RecoveryProviderForMetrics.
func (p *RecoveryProvider) RecoveryStates() map[string]metrics.RecoveryStateMetrics {
	if p.src == nil {
		return nil
	}
	src := p.src.MetricsRecoveryStates()
	out := make(map[string]metrics.RecoveryStateMetrics, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// EventBusProvider adapts an *events.Bus to the
// metrics.EventBusForMetrics protocol.
type EventBusProvider struct {
	src *events.Bus
}

// NewEventBusProvider returns an EventBusProvider for src.
func NewEventBusProvider(src *events.Bus) *EventBusProvider {
	return &EventBusProvider{src: src}
}

// EventStats implements metrics.EventBusForMetrics.
func (p *EventBusProvider) EventStats() map[string]int {
	if p.src == nil {
		return nil
	}
	return p.src.EventStats()
}

// TotalSubscriptionCount implements metrics.EventBusForMetrics.
func (p *EventBusProvider) TotalSubscriptionCount() int {
	if p.src == nil {
		return 0
	}
	return p.src.TotalSubscriptionCount()
}

// HandlerStats implements metrics.EventBusForMetrics.
//
// It aggregates the per-handler [events.HandlerStat] list returned by the
// bus into a per-event-type [metrics.HandlerStatSnapshot] map. The
// Executed field is set to the sum of Matches (handlers that actually
// fired after key filtering). Duration and error fields are intentionally
// zero: those are observer-sourced and the Aggregator merges them from
// the Observer via its fallback path in Aggregator.Events().
func (p *EventBusProvider) HandlerStats() map[string]metrics.HandlerStatSnapshot {
	if p.src == nil {
		return map[string]metrics.HandlerStatSnapshot{}
	}
	raw := p.src.HandlerStats()
	out := make(map[string]metrics.HandlerStatSnapshot, len(raw))
	for _, hs := range raw {
		key := string(hs.EventType)
		snap := out[key]
		// hs.Matches is uint64; cap at math.MaxInt32 to avoid overflow on
		// 32-bit targets. In practice match counts never approach this limit.
		matches := min(hs.Matches, 1<<31-1)
		snap.Executed += int(matches) //nolint:gosec // overflow guarded above; see #20
		out[key] = snap
	}
	return out
}

// HealthProvider adapts a *health.Tracker to the
// metrics.HealthTrackerForMetrics protocol. Optionally combines the
// tracker's view with a recovery-attempt counter so the resulting
// HealthSummary reports the same data
type HealthProvider struct {
	src      *health.Tracker
	recovery *coordinators.ConnectionRecoveryCoordinator
}

// NewHealthProvider returns a HealthProvider that reads from
// `tracker`. Pass `recovery` to roll up reconnect attempts into the
// summary; nil leaves ReconnectAttempts at zero.
func NewHealthProvider(tracker *health.Tracker, recovery *coordinators.ConnectionRecoveryCoordinator) *HealthProvider {
	return &HealthProvider{src: tracker, recovery: recovery}
}

// HealthSummary implements metrics.HealthTrackerForMetrics.
func (p *HealthProvider) HealthSummary() metrics.HealthSummary {
	if p.src == nil {
		return metrics.HealthSummary{OverallScore: 1.0}
	}
	view := p.src.MetricsHealthSummary()
	out := metrics.HealthSummary{
		OverallScore:    view.OverallScore,
		ClientsHealthy:  view.ClientsHealthy,
		ClientsDegraded: view.ClientsDegraded,
		ClientsFailed:   view.ClientsFailed,
	}
	if p.recovery != nil {
		// Sum recovery attempts across every interface.
		var total int
		for _, st := range p.recovery.MetricsRecoveryStates() {
			total += st.AttemptCount()
		}
		out.ReconnectAttempts = total
	}
	return out
}

// DeviceProvider adapts a central's ModelRegistry to
// [metrics.DeviceForMetrics] so the aggregator's model section can
// report device / channel / data-point counts.
type DeviceProvider struct {
	reg *registry.ModelRegistry
}

// NewDeviceProvider wraps reg. A nil registry is safe and yields no
// devices (the aggregator degrades to a zero-value model section).
func NewDeviceProvider(reg *registry.ModelRegistry) *DeviceProvider {
	return &DeviceProvider{reg: reg}
}

// Devices implements [metrics.DeviceForMetrics].
func (p *DeviceProvider) Devices() []metrics.DeviceMetrics {
	if p == nil || p.reg == nil {
		return nil
	}
	devs := p.reg.List()
	out := make([]metrics.DeviceMetrics, 0, len(devs))
	for _, d := range devs {
		out = append(out, deviceMetrics{d: d})
	}
	return out
}

// deviceMetrics adapts one model device to [metrics.DeviceMetrics].
type deviceMetrics struct {
	d *device.Device
}

// Available implements [metrics.DeviceMetrics].
func (m deviceMetrics) Available() bool { return m.d.Available() }

// ChannelCount implements [metrics.DeviceMetrics].
func (m deviceMetrics) ChannelCount() int { return len(m.d.Channels()) }

// DataPointCounts implements [metrics.DeviceMetrics].
func (m deviceMetrics) DataPointCounts() (generic, custom, calculated int) {
	return len(m.d.AllDataPoints()), len(m.d.CustomDataPoints()), len(m.d.CalculatedDataPoints())
}

// DataPointsByCategory implements [metrics.DeviceMetrics]. Categories
// exist only on attachables satisfying [device.CategorisedDataPoint]
// (custom / calculated); generic wire-level data points carry no
// category on the model surface and are covered by DataPointCounts.
func (m deviceMetrics) DataPointsByCategory() map[string]int {
	out := make(map[string]int)
	count := func(dps []device.AttachableDataPoint) {
		for _, dp := range dps {
			if cdp, ok := dp.(device.CategorisedDataPoint); ok {
				out[string(cdp.Category())]++
			}
		}
	}
	count(m.d.CustomDataPoints())
	count(m.d.CalculatedDataPoints())
	return out
}

// HubProvider adapts the HubCoordinator to
// [metrics.HubDataPointManagerForMetrics] so the aggregator's model
// section can report CCU program / system-variable counts.
type HubProvider struct {
	hub *coordinators.HubCoordinator
}

// NewHubProvider wraps h. A nil coordinator is safe and yields zero
// counts.
func NewHubProvider(h *coordinators.HubCoordinator) *HubProvider {
	return &HubProvider{hub: h}
}

// ProgramCount implements [metrics.HubDataPointManagerForMetrics].
func (p *HubProvider) ProgramCount() int {
	if p == nil || p.hub == nil {
		return 0
	}
	return len(p.hub.ProgramDataPoints())
}

// SysvarCount implements [metrics.HubDataPointManagerForMetrics].
func (p *HubProvider) SysvarCount() int {
	if p == nil || p.hub == nil {
		return 0
	}
	return len(p.hub.Sysvars())
}

// Compile-time assertions: every wiring adapter implements the
// matching metrics provider protocol.
var (
	_ metrics.ClientForMetrics              = (*ClientProvider)(nil)
	_ metrics.CacheProviderForMetrics       = (*CacheProvider)(nil)
	_ metrics.RecoveryProviderForMetrics    = (*RecoveryProvider)(nil)
	_ metrics.EventBusForMetrics            = (*EventBusProvider)(nil)
	_ metrics.HealthTrackerForMetrics       = (*HealthProvider)(nil)
	_ metrics.DeviceForMetrics              = (*DeviceProvider)(nil)
	_ metrics.HubDataPointManagerForMetrics = (*HubProvider)(nil)
)
