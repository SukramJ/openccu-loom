// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	metricswiring "github.com/SukramJ/openccu-loom/internal/metrics/wiring"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seedCentralHealthAndMetrics primes every registered central's health tracker
// and wires its metrics aggregator. It seeds a synthetic "started" sample so
// `/health` reports green from t=0, pins the operator-configured primary
// interface, registers the event-bus / audit-durability / scheduler gauges,
// and stands up a per-central metrics Observer + Aggregator.
//
// It returns the per-central seed the live-adopt orchestrator runs on a
// runtime-added central: the loop walks the registry exactly once, so an
// adopted CCU reported no `central` health sample, none of its event-bus /
// audit / scheduler gauges, and no observability recorder — its section of
// `/health` and the diagnostics dump simply had nothing in it, which reads
// like a CCU that is idle rather than one nobody is watching.
//
// The seed takes the primary interface as an argument rather than resolving it
// from cfg: a runtime-adopted central is described by its row in the centrals
// table, which cfg.Centrals never gains, so a by-name lookup finds nothing and
// silently drops the operator's pin.
func seedCentralHealthAndMetrics(
	reg *central.Registry, cfg *config.Config, auditDurableStats *audit.DurableSinkStats, logger *slog.Logger,
) (centralSeed func(u *central.Unit, primaryInterface string)) {
	obsRecorder := observability.LogRecorder{Logger: logger.With(slog.String("component", "observability"))}
	centralSeed = func(u *central.Unit, primaryInterface string) {
		seedCentralHealthAndMetricsFor(u, primaryInterface, auditDurableStats, obsRecorder, logger)
	}
	for _, u := range reg.List() {
		centralSeed(u, configuredPrimaryInterface(cfg, u.Name()))
	}
	return centralSeed
}

// configuredPrimaryInterface returns the `primary_interface` the boot config
// pins for name, or "" when the operator left it at its default.
func configuredPrimaryInterface(cfg *config.Config, name string) string {
	if cfg == nil {
		return ""
	}
	for i := range cfg.Centrals {
		if cfg.Centrals[i].Name == name {
			return cfg.Centrals[i].PrimaryInterface
		}
	}
	return ""
}

// seedCentralHealthAndMetricsFor primes one central. Split out of
// [seedCentralHealthAndMetrics] so boot and live adopt share one fork.
//
// SetAggregator / SetObservabilityRecorder write unsynchronised fields the
// serving handlers read, so the adopt path calls this BEFORE the unit enters
// the shared registry — the point at which anything else can observe it.
func seedCentralHealthAndMetricsFor(
	u *central.Unit,
	primaryInterface string,
	auditDurableStats *audit.DurableSinkStats,
	obsRecorder observability.Recorder,
	logger *slog.Logger,
) {
	if u == nil {
		return
	}
	u.Health.Record("central", health.Sample{Healthy: true, Note: "started"})
	u.SetObservabilityRecorder(obsRecorder)

	// Pin the primary interface explicitly when the operator
	// configured `primary_interface` for this central. Empty
	// (default) keeps the built-in HmIP-RF substring heuristic.
	// Multi-CCU setups with HmIP-Wired-only or BidCos-only
	// installations rely on this to score the right interface
	// as the central's primary.
	if primaryInterface != "" {
		u.Health.SetPrimaryInterface(primaryInterface)
		logger.Info("health.primary_interface.pinned",
			slog.String("central", u.Name()),
			slog.String("interface", primaryInterface))
	}

	// Surface the event-bus deferred high-water gauge through the
	// health tracker so admin endpoints can alert on pathological
	// handler recursion without owning the events package directly.
	bus := u.EventBus
	u.Health.RegisterGauge("event_bus.deferred_depth",
		func() float64 { return float64(bus.DeferredDepth()) })
	u.Health.RegisterGauge("event_bus.deferred_high_water",
		func() float64 { return float64(bus.DeferredHighWater()) })
	// Audit durability telemetry. Surfaces the durable-sink overflow
	// counter so admin endpoints can alert on audit-row loss before the
	// database falls behind. Skipped when the durable sink was not wired
	// (in-memory-only audit).
	if auditDurableStats != nil {
		s := auditDurableStats
		u.Health.RegisterGauge("audit.dropped",
			func() float64 { return float64(s.Dropped()) })
		u.Health.RegisterGauge("audit.sink_errors",
			func() float64 { return float64(s.SinkErrors()) })
	}
	// Scheduler coverage: job-count + cumulative failure counter
	// (errors + recovered panics). Per-job breakdown is reachable
	// via the diagnostics-dump component map; the gauge gives the
	// SPA a single number for the at-a-glance tile.
	if u.Scheduler != nil {
		scheduler := u.Scheduler
		u.Health.RegisterGauge("scheduler.jobs",
			func() float64 { return float64(len(scheduler.Jobs())) })
		u.Health.RegisterGauge("scheduler.failures",
			func() float64 { return float64(scheduler.TotalFailures()) })
	}

	// Wire a per-central metrics aggregator. All providers are wired
	// with the components owned by this central; nil providers are
	// safe — Aggregator degrades to zero-value sections.
	obs := metrics.NewObserver()
	agg := metrics.NewAggregator(
		u.Name(), obs,
		metrics.WithClientProvider(metricswiring.NewClientProvider(u.MetricsClients)),
		metrics.WithCacheProvider(metricswiring.NewCacheProvider(u.Cache)),
		metrics.WithRecoveryProvider(metricswiring.NewRecoveryProvider(u.Recovery)),
		metrics.WithEventBus(metricswiring.NewEventBusProvider(u.EventBus)),
		metrics.WithHealthTracker(metricswiring.NewHealthProvider(u.Health, u.Recovery)),
		metrics.WithDeviceProvider(metricswiring.NewDeviceProvider(u.ModelRegistry)),
		metrics.WithHubManager(metricswiring.NewHubProvider(u.Hub)),
	)
	u.SetAggregator(agg)
}

// wireValueWriterHooks attaches the per-central LinkCoordinator and the two
// ValueWriter resolver hooks (bus resolver + optimistic CommandTracker) onto
// every registered central. Grouping these wirings keeps the composition
// root's branch-count down without changing behaviour.
//
// LinkCoordinator wiring is UNCONDITIONAL — it runs even when REST is off.
// Both REST (internal/north/rest/handlers/links.go) and the WS link commands
// (cmd/openccu-loom/ws_adapters.go, via wsLinkQuery) call
// *adapter.LinksDomain directly and never read central.Unit.Link; MQTT has
// no link.* commands at all. WireLinkCoordinator's resolver is set but has
// no production reader today — kept unconditional because a resolver that
// exists only when REST happens to be enabled is a trap for the next
// consumer that reaches for central.Unit.Link expecting it to be populated.
//
// The bus resolver lets WriteOptions.WaitForCallback subscribe to the right
// central's EventBus per call (multi-CCU deployments hit different busses).
//
// The CommandTracker hook fires after each successful SetValue: it calls
// WriteUnconfirmedValue on the matching InterfaceClient so north-bound adapters
// can return the new value immediately before the CCU echoes back a callback.
func wireValueWriterHooks(reg *central.Registry, valueWriter *clientpkg.ValueWriter, linksDomain *adapter.LinksDomain, logger *slog.Logger) {
	for _, u := range reg.List() {
		if err := adapter.WireLinkCoordinator(u, linksDomain); err != nil {
			logger.Warn("wire link coordinator", slog.String("central", u.Name()), slog.String("err", err.Error()))
		}
	}

	reg.Manifest().Attach(wiring.Seam{
		Name:         "client.value_writer_hooks",
		Collaborator: "*client.ValueWriter bus resolver and command tracker",
		Phase:        wiring.PhaseOnce,
		Why:          "a value write reaches the CCU and is recorded by no command tracker, so the in-flight gauge under-reports and the callback path has no entry to clear when the CCU echoes the value back",
	}, func() { wireValueWriterHookFns(reg, valueWriter) })
}

// wireValueWriterHookFns installs the hooks themselves. Split out so the
// seam above wraps exactly the handover and nothing else.
//
// The two hooks are not equally live, and the seam's Why covers only the
// tracker for that reason. The bus resolver feeds the wait-for-callback
// path in [client.SetValueWithOptions], and no production site asks for
// that wait: WriteOptions.WaitForCallback is set only in tests, and
// generic.WithWaitForCallback has no caller outside them either. Removing
// the resolver would therefore change nothing observable today — which is
// precisely why it is worth writing down, rather than leaving a future
// reader to infer from the seam that both halves carry traffic.
func wireValueWriterHookFns(reg *central.Registry, valueWriter *clientpkg.ValueWriter) {
	valueWriter.SetBusResolver(func(centralName string) (any, bool) {
		c, ok := reg.Get(centralName)
		if !ok || c == nil {
			return nil, false
		}
		return c.EventBus, true
	})

	valueWriter.SetCommandTrackerFn(func(interfaceID, channelAddress string, parameter hmenum.Parameter, paramsetKey hmenum.ParamsetKey, value any) {
		for _, u := range reg.List() {
			if entry, ok := u.Clients.Get(interfaceID); ok && entry != nil && entry.Client != nil {
				entry.Client.WriteUnconfirmedValue(channelAddress, parameter, paramsetKey, value)
				return
			}
		}
	})
}

// wireDevicesCreatedGates installs the device-creation gate on every
// registered central. Split out so the seam wraps exactly the handover and
// nothing else — and so an effect test can drive it the way boot does.
//
// Before the gate is installed, Unit.IsDevicesCreated answers true
// unconditionally: with no gate there is nothing to wait on, so every
// gated hub job is free to run. That is the state the seam exists to leave
// behind, and it is why the gate has to be in place before the standard
// jobs are registered.
func wireDevicesCreatedGates(reg *central.Registry) {
	for _, u := range reg.List() {
		u.WireDevicesCreatedGate()
	}
}
