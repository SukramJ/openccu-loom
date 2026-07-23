// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/history"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
)

// sharedInfra bundles the daemon-global stores, registries and adapters
// constructed in the "shared infrastructure" phase of the composition
// root. Every field is read further down daemonServeWithDeps; the
// per-field call-site aliases keep the downstream wiring unchanged.
type sharedInfra struct {
	metricsReg    *metrics.Registry
	healthTracker *health.Tracker
	catalogs      *i18n.Catalogs

	visReg            *visibility.Registry
	visFilter         *filter.Adapter
	visibilityStore   *sqlite.VisibilityUnIgnoreStore
	visibilityAdapter *visibilityAdapter

	masterValuesStore  *sqlite.MasterValuesStore
	valuesCacheStore   *sqlite.ValuesCacheStore
	historyStore       *sqlite.MeasurementStore
	recordingOverrides *history.RecordingOverrides
	recordingStore     *sqlite.RecordingOverrideStore
	descriptorStores   adapter.DescriptorStores
	descriptorDB       *sql.DB

	wsHub       *ws.Hub
	wsHandler   http.Handler
	valueWriter *clientpkg.ValueWriter

	mqttCollector *metrics.MqttCollector
	mqttSup       *mqttSupervisor
	mqttWiring    *mqtt.Wiring
}

// wireSharedInfrastructure constructs the daemon-global stores,
// registries, adapters and the MQTT supervisor that the rest of the
// composition root consumes. It mirrors the original inline phase
// verbatim: the SQLite-backed stores and the MQTT supervisor each
// register a shutdown hook, all of which are folded into the returned
// teardown func and run in the same LIFO order the inline defers used
// (mqtt.Shutdown → valuesCacheStore.Close → masterValuesStore.Close →
// visibilityStore.Close). The caller defers teardown.
func wireSharedInfrastructure(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	reg *central.Registry,
	deps *reloadDeps,
) (si *sharedInfra, teardown func()) {
	si = &sharedInfra{}

	si.metricsReg = metrics.NewRegistry()
	si.healthTracker = health.NewTracker()
	si.catalogs, _ = i18n.NewCatalogs()

	// Outbound visibility filter (ADR 0007): wrap the default registry
	// as a filter.VisibilitySet so adapters never import the full
	// visibility loading machinery. The registry uses built-in rules by
	// default; operators can extend them via un-ignore files once that
	// config knob is wired. A nil adapter is never produced here
	// (NewRegistry always returns non-nil) but the Adapter is nil-safe.
	si.visReg = visibility.NewRegistry()
	// E.13: seed the required-parameter whitelist with every
	// parameter referenced by the generated profile catalogue plus
	// every Extended config. This is what protects required custom-DP
	// parameters (e.g. SET_POINT_TEMPERATURE) from being filtered out
	// by IGNORED_PARAMETERS during paramset hydration.
	si.visReg.SetRequiredParameters(custom.DefaultRegistry().RequiredParameters())
	si.visFilter = filter.NewAdapter(si.visReg)

	// Visibility / un_ignore — SQLite-backed store, bootstrap-seed from
	// config.yaml on first start, then wired into the REST surface via
	// the visibilityAdapter (see cmd/openccu-loom/visibility_adapter.go +
	// visibility_wiring.go + docs/ui/unignore-concept.md). The patterns
	// are applied to visReg after WireCentrals so the suppression marks
	// land on materialised devices.
	si.visibilityStore = wireVisibilityUnIgnoreStore(cfg, logger) //nolint:contextcheck // wireVisibilityUnIgnoreStore has no ctx parameter
	si.visibilityAdapter = newVisibilityAdapter(si.visReg, si.visibilityStore, reg)
	si.masterValuesStore = wireMasterValuesStore(cfg, logger) //nolint:contextcheck // wireMasterValuesStore has no ctx parameter
	si.valuesCacheStore = wireValuesCacheStore(cfg, logger)   //nolint:contextcheck // wireValuesCacheStore has no ctx parameter
	// Persistent device- / paramset-description caches (warm-boot
	// registry hydration + mirror-on-mutation; see
	// adapter.WireDescriptorPersistence). Zero-value stores disable the
	// feature when the DB cannot be opened.
	si.descriptorStores, si.descriptorDB = wireDescriptorStores(cfg, logger) //nolint:contextcheck // wireDescriptorStores has no ctx parameter
	// Start the periodic WAL checkpoint for the values-cache DB. Without
	// this the WAL file can grow unbounded on embedded or busy ARM targets
	// because the values-cache DB is a separate *sql.DB from the audit DB
	// and therefore not covered by the audit-side checkpoint loop wired in
	// daemon.go. The stop function runs one final checkpoint before the
	// store is closed; it is called at the top of teardown so the
	// checkpoint drains the WAL before Close releases the file handle.
	stopValuesCacheWAL := sqlite.StartWALCheckpointLoop(si.valuesCacheStore.DB(), 0, logger) //nolint:contextcheck // StartWALCheckpointLoop creates its own daemon-lifetime context internally

	// Open the opt-in measurement-history DB (its own file + WAL). nil
	// when the feature is off (the default). The append-heavy recorder
	// makes a periodic WAL checkpoint worthwhile on busy ARM targets, so
	// wire one when the store exists; the stop closer drains the WAL
	// before Close at teardown.
	si.historyStore = wireHistoryStore(cfg, logger) //nolint:contextcheck // wireHistoryStore creates its own bounded context internally
	var stopHistoryWAL func()
	if si.historyStore != nil {
		stopHistoryWAL = sqlite.StartWALCheckpointLoop(si.historyStore.DB(), 0, logger) //nolint:contextcheck // StartWALCheckpointLoop creates its own daemon-lifetime context internally
		// Per-datapoint recording overrides live in the same history DB and
		// steer the recorder's hot-path gate (SV10).
		si.recordingStore, si.recordingOverrides = wireRecordingOverrides(si.historyStore, cfg, logger) //nolint:contextcheck // helper bounds its own context internally
	}

	si.wsHub = ws.NewHub()
	if n := cfg.North.REST.WS.ReplayCapacity; n > 0 {
		si.wsHub.SetReplayCapacity(n)
	}
	si.wsHandler = ws.Handler(si.wsHub, logger, wsAllowedOrigins(cfg))
	// WS subscriber-count gauge so the diagnostics dump shows how
	// many SPA clients are currently subscribed for live updates.
	// Registered against every central's tracker because the WS hub
	// is daemon-global; per-central scoping would double-count.
	if si.healthTracker != nil {
		hub := si.wsHub
		si.healthTracker.RegisterGauge("ws.subscribers",
			func() float64 { return float64(hub.ClientCount()) })
	}

	si.valueWriter = clientpkg.NewValueWriter()
	// Stamp the build version into MQTT Discovery payloads so the
	// `origin.sw_version` field reflects the running binary instead of
	// the "dev" default. Set before the supervisor starts emitting
	// Discovery so the very first payload already carries it.
	mqtt.SetOriginVersion(build.Version)
	si.mqttCollector = metrics.NewMqttCollector(si.metricsReg, pickFirstCentral(cfg))
	si.mqttSup = newMQTTSupervisor(logger, si.healthTracker)
	si.mqttSup.SetCollector(si.mqttCollector)
	if err := si.mqttSup.Start(ctx, cfg); err != nil {
		logger.Warn("mqtt.supervisor.start", slog.String("err", err.Error()))
	}
	// Late-bind the supervisor + the live config snapshot into the
	// reload deps bag so the config-watcher's hot-reload handler can
	// issue an MQTT Swap when north.mqtt.* changes and the REST
	// trigger handler can replay the current config on demand. Nil
	// deps (direct daemonServe callers / tests) is fine — Swap simply
	// never fires.
	deps.SetMQTTSupervisor(si.mqttSup)
	deps.SetCurrentConfig(cfg)
	si.mqttWiring = si.mqttSup.Wiring()

	// OTLP span exporter — wired when north.rest.tracing.otlp_endpoint is
	// set. Disabled by default; a non-empty endpoint enables best-effort
	// OTLP/HTTP trace export with no new dependencies (standard library only).
	var otlpExp *observability.OTLPHTTPExporter
	if ep := cfg.North.REST.Tracing.OTLPEndpoint; ep != "" {
		otlpExp = observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{ //nolint:contextcheck // exporter owns its background goroutine lifecycle; ctx is not propagated into the HTTP flush path by design
			Endpoint: ep,
			Logger:   logger.With(slog.String("component", "otlp")),
		})
		observability.SetSpanExporter(otlpExp)
		logger.Info("otlp.trace.enabled", slog.String("endpoint", ep))
	}

	teardown = func() { //nolint:contextcheck // shutdown path must not inherit the cancelled daemon ctx
		// LIFO order, mirroring the original inline defers: the MQTT
		// supervisor was deferred last (runs first), then the three
		// SQLite-backed stores in reverse construction order.
		func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			si.mqttSup.Shutdown(shutCtx)
		}()
		if otlpExp != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = otlpExp.Shutdown(shutCtx)
			observability.SetSpanExporter(nil)
		}
		// Stop the WAL checkpoint loop (which also runs one final checkpoint)
		// before closing the database so the checkpoint drains cleanly.
		stopValuesCacheWAL()
		_ = si.valuesCacheStore.Close()
		if stopHistoryWAL != nil {
			stopHistoryWAL()
		}
		_ = si.historyStore.Close()
		_ = si.masterValuesStore.Close()
		_ = si.visibilityStore.Close()
		if si.descriptorDB != nil {
			_ = si.descriptorDB.Close()
		}
	}

	return si, teardown
}
