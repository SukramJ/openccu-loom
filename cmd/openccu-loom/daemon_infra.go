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
	"github.com/SukramJ/openccu-loom/internal/channelflags"
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
	"github.com/SukramJ/openccu-loom/internal/wiring"
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
//
// channelFlags carries the per-channel operator overrides (G12). It is a
// parameter rather than a post-construction setter because the MQTT bridge
// reads its hidden-channel gate once, at build time: installing the gate after
// Start left the boot-built bridge — the one that lives for the whole daemon
// lifetime — publishing channels the operator had hidden everywhere else. A
// nil overlay leaves the gate off.
func wireSharedInfrastructure(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	reg *central.Registry,
	deps *reloadDeps,
	channelFlags *channelflags.Overlay,
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
	// visibility_wiring.go + notes/concepts/ui/unignore-concept.md). The patterns
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
	var stopHistoryWAL, stopHistoryRetention func()
	if si.historyStore != nil {
		stopHistoryWAL = sqlite.StartWALCheckpointLoop(si.historyStore.DB(), 0, logger) //nolint:contextcheck // StartWALCheckpointLoop creates its own daemon-lifetime context internally
		// Per-datapoint recording overrides live in the same history DB and
		// steer the recorder's hot-path gate (SV10).
		si.recordingStore, si.recordingOverrides = wireRecordingOverrides(si.historyStore, cfg, logger) //nolint:contextcheck // helper bounds its own context internally
	} else {
		// Recording is off, but an earlier run may have left a populated
		// history.db behind. Keep evicting it so switching the feature off
		// releases the disk it was taken for instead of freezing the file
		// at its final size.
		stopHistoryRetention = wireHistoryRetention(cfg, logger) //nolint:contextcheck // wireHistoryRetention bounds its own open context and runs the loop on a daemon-lifetime one
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
		// Client→daemon round-trip, as the daemon measures it from the
		// heartbeat each connection echoes. The median, not the mean: one
		// tab on a slow tunnel is normal and must not move the figure that
		// describes everyone else.
		//
		// This leg is unrelated to the CCU's. A daemon reached through HA
		// Ingress or a public host can show tens of milliseconds here while
		// the CCU link is sub-millisecond, and the reverse on a LAN with an
		// overloaded CCU — which is the whole reason both are reported and
		// neither is summed into the other.
		si.healthTracker.RegisterGauge("ws.heartbeat_rtt_ms",
			func() float64 { return hub.HeartbeatRTTs().MedianMs })
		si.healthTracker.RegisterGauge("ws.heartbeat_rtt_samples",
			func() float64 { return float64(hub.HeartbeatRTTs().Samples) })
	}

	si.valueWriter = clientpkg.NewValueWriter()
	// Stamp the build version into MQTT Discovery payloads so the
	// `origin.sw_version` field reflects the running binary instead of
	// the "dev" default. Set before the supervisor starts emitting
	// Discovery so the very first payload already carries it.
	mqtt.SetOriginVersion(build.Version)
	// One daemon-wide MQTT counter series: the single shared bridge carries
	// every central's traffic, so the counters are not per-central.
	si.mqttCollector = metrics.NewMqttCollector(si.metricsReg)
	// The bridge/health payload names the live fleet, not the boot config: a
	// CCU adopted through the SPA joins the registry without ever reaching
	// cfg.Centrals.
	wireMQTTSupervisor(ctx, cfg, logger, reg, si, channelFlags)
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
		if stopHistoryRetention != nil {
			stopHistoryRetention()
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

// channelHiddenGate turns the per-channel operator overlay into the predicate
// the MQTT bridge consults (G12). A nil overlay yields a nil predicate, which
// disables the gate rather than hiding everything.
func channelHiddenGate(overlay *channelflags.Overlay) func(centralName, channelAddress string) bool {
	if overlay == nil {
		return nil
	}
	return func(centralName, channelAddress string) bool {
		return overlay.Get(centralName, channelAddress).Hidden
	}
}

// wireMQTTSupervisor builds the MQTT supervisor, installs the gates a
// bridge captures at build time, and starts it.
//
// Extracted from [wireSharedInfrastructure] so the one ordering
// constraint here has room to be stated: the channel-hidden gate has no
// setter once a bridge holds it, so it must be installed before Start
// builds the first one.
func wireMQTTSupervisor(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	reg *central.Registry,
	si *sharedInfra,
	channelFlags *channelflags.Overlay,
) {
	// The bridge/health payload names the live fleet, not the boot config: a
	// CCU adopted through the SPA joins the registry without ever reaching
	// cfg.Centrals.
	si.mqttSup = newMQTTSupervisor(logger, si.healthTracker, func() []string {
		return liveCentralNames(cfg, reg)
	})
	si.mqttSup.SetCollector(si.mqttCollector)
	// Let every (re)built MQTT bridge skip operator-hidden channels, so a
	// hidden channel disappears from the MQTT plane like it does from the
	// REST operation list and Matter. The overlay is keyed on
	// (central, address).
	//
	// The gate is captured at bridge-build time and has no setter
	// afterwards, so it has to be installed before Start builds the first
	// bridge. That is a declared constraint rather than a comment now:
	// installing it later compiles, returns nothing, and leaves every
	// hidden channel publishing until the next rebuild.
	reg.Manifest().Attach(wiring.Seam{
		Name:         "mqtt.channel_hidden_gate",
		Collaborator: "channelHiddenGate over *channelflags.Overlay",
		Phase:        wiring.PhaseOrdered,
		Before:       []wiring.Mark{wiring.MarkMQTTSupervisorStarted},
		Why:          "channels the operator hid keep publishing on the MQTT plane, so they reappear in Home Assistant while staying hidden everywhere else",
	}, func() { si.mqttSup.SetChannelHidden(channelHiddenGate(channelFlags)) })
	// A failed first connect is not fatal and not final: the supervisor
	// keeps the stable Wiring every consumer binds to and retries the
	// connect in the background, so a broker that is still booting beside
	// the daemon costs a delay rather than the whole MQTT plane until the
	// next restart.
	// Broker acknowledgement time, read from whichever stack generation is
	// live — a config swap replaces the probe, and a gauge holding the
	// predecessor would report a broker connection that no longer exists.
	//
	// The companion gauge is the cumulative count, not the window occupancy.
	// Occupancy pins to the window size after the first burst of acknowledged
	// publishes and stays there, so it cannot distinguish a median describing
	// the last minute from one describing the bring-up of a daemon that has
	// been running for days. A total that stops advancing says plainly that
	// the median is stale — and a total of zero says the deployment publishes
	// state at QoS 0, where the broker acknowledges nothing and there is
	// nothing to measure.
	if si.healthTracker != nil {
		sup := si.mqttSup
		si.healthTracker.RegisterGauge("mqtt.publish_ack_ms",
			func() float64 { return sup.PublishLatency().MedianMs })
		si.healthTracker.RegisterGauge("mqtt.publish_ack_total",
			func() float64 { return float64(sup.PublishLatency().Total) })
	}
	startErr := si.mqttSup.Start(ctx, cfg)
	reg.Manifest().Mark(wiring.MarkMQTTSupervisorStarted)
	if startErr != nil {
		logger.Warn("mqtt.supervisor.start",
			slog.String("err", startErr.Error()),
			slog.String("effect", "MQTT publishes are dropped until a background retry connects"))
	}
}
