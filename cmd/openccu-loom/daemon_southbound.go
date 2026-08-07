// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/history"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
)

// southboundWiringDeps carries the already-constructed subsystems the
// southbound wiring phase reads. They are produced by earlier phases of
// the composition root (callback servers, shared stores, the EventBridge)
// and threaded through unchanged.
type southboundWiringDeps struct {
	cfg                 *config.Config
	reg                 *central.Registry
	logger              *slog.Logger
	valueWriter         *clientpkg.ValueWriter
	translations        *ccudata.Translations
	callbackSrv         *rpcserver.XMLRPCServer
	callbackPort        int
	callbackHost        func(*config.CentralConfig) string
	binRPCSrv           *rpcserver.BINRPCServer
	binRPCPort          int
	catalogs            *i18n.Catalogs
	visReg              *visibility.Registry
	masterValuesStore   *sqlite.MasterValuesStore
	valuesCacheStore    *sqlite.ValuesCacheStore
	channelFlagsOverlay *channelflags.Overlay
	descriptorStores    adapter.DescriptorStores
	// sqCentrals persists per-central serials backfilled at bring-up so SSDP
	// discovery recognises configured centrals by serial. Nil disables backfill.
	sqCentrals              *sqlite.CentralsStore
	historyStore            *sqlite.MeasurementStore
	recordingOverrides      *history.RecordingOverrides
	recordingStore          *sqlite.RecordingOverrideStore
	healthTracker           *health.Tracker
	visibilityUnIgnoreStore *sqlite.VisibilityUnIgnoreStore
	mqttWiring              *mqtt.Wiring
	bridge                  *adapter.EventBridge
	// hubMQTT is re-started on each central's CentralSouthboundReadyEvent so
	// hub discovery payloads — skipped while the CCU serial that gates them is
	// still unresolved during the async bring-up — are published once the serial
	// lands. Nil when MQTT is not configured.
	hubMQTT *adapter.HubMQTTPublisher
	// postHubReady runs after each debounced hub-discovery re-Start
	// (serial resolved / live adopt). Nil-safe; currently refreshes the
	// daemon-discovery mDNS TXT record (ADR 0058).
	postHubReady func()
}

// southboundWiring is the result of the southbound wiring phase. It
// surfaces the values that later phases of the composition root read.
type southboundWiring struct {
	// backupAdapter is constructed up-front so WireCentrals can inject
	// the live HTTPBackupRestorer after the first hub handshake; the
	// REST backup handler reads it later.
	backupAdapter *adapter.BackupAdapter
	// bringUpManager exposes per-central re-initialization (clear caches +
	// readiness-gated re-pull) to the cache-reset service. ADR 0042.
	bringUpManager *adapter.BringUpManager
	// hubReadyTrigger fires a debounced hub-publisher re-Start once a central's
	// southbound bring-up (and thus its serial) is ready. The live-adopt path
	// subscribes runtime-added centrals onto the same pipeline. Nil when MQTT is
	// not configured.
	hubReadyTrigger func()
}

// serialBackfiller returns the WireDeps.PersistSerial callback: it records a
// central's resolved canonical serial into the store (best-effort), so SSDP
// discovery recognises a host-configured central by serial. A nil store yields
// a no-op callback; store / log errors never propagate to the bring-up.
func serialBackfiller(store *sqlite.CentralsStore, logger *slog.Logger) func(ctx context.Context, centralName, serial string) {
	return func(ctx context.Context, centralName, serial string) {
		if store == nil {
			return
		}
		updated, err := store.BackfillSerial(ctx, centralName, serial)
		switch {
		case err != nil:
			logger.Warn("central.serial.backfill_failed",
				slog.String("central", centralName), slog.String("err", err.Error()))
		case updated:
			logger.Info("central.serial.backfilled",
				slog.String("central", centralName), slog.String("serial", serial))
		}
	}
}

// wireCentralNorthbound runs the per-central north-bound hooks that must fire
// AFTER a central's southbound bring-up (WireCentrals) has populated its
// InterfaceClient registry and SystemInformation: health subscriptions +
// state re-evaluation, the registry-cleanup on-stop hook, device-availability
// propagation, climate link-peer refresh, and the MQTT hub-info stamp. It
// returns the device-availability and climate closers so the caller can chain
// them into teardown (nil when the corresponding wiring is inactive).
//
// Factored out of wireSouthbound's inline per-central loops so the same wiring
// can be run for a single central added at runtime — the foundation for live
// CCU adopt. The per-central hooks are
// independent across centrals, so running them one-central-at-a-time is
// equivalent to the previous hook-at-a-time loops.
func wireCentralNorthbound(d southboundWiringDeps, u *central.Unit) (availCloser, climateCloser func()) {
	// Health subscriptions must be live before the state re-evaluation:
	// WireCentrals connected the interfaces before this ran, so the startup
	// ClientStateChanged transitions fired with no health subscriber and the
	// central kept the FAILED evaluation taken at boot (before any interface
	// connected) — surfacing as a permanently "degraded" CCU. Re-evaluate now
	// that the InterfaceClient registry reflects the connected clients.
	_ = adapter.WireHealth(u)
	u.EvaluateCentralState("post_wire", true)

	// Clean up the shared registry entry once Stop() transitions to STOPPED so a
	// stopped central is no longer visible to north-bound adapters.
	centralName := u.Name()
	u.AddOnStopHook(func() {
		d.reg.Unregister(centralName)
	})

	// Device-availability propagation: when an InterfaceClient reports
	// CONNECTED / DISCONNECTED / FAILED, every device on that interface gets its
	// forced-availability override flipped so HA / REST / SPA stop showing stale
	// "online" entities after a CCU-side disconnect.
	availCloser = adapter.WireDeviceAvailability(u)

	// Climate link-peer activity-source refresh: on RecoveryCompletedEvent
	// re-subscribe Climate custom DPs on the recovered interface to their linked
	// valve/switch peer channels; LinkPeerChangedEvent re-wires on topology
	// changes.
	climateCloser = adapter.WireClimateLinkPeerRefresh(u)

	// Best-effort early HubInfo stamp onto the MQTT-Discovery builder. WireCentrals
	// only LAUNCHES the async readiness-gated bring-up, so SystemInformation is
	// usually still empty here — the serial (and model/URL) resolve later. The
	// authoritative stamp therefore happens inside the hub publisher, which reads
	// the serial live from the registry on its ready-driven re-Start (see
	// [adapter.HubMQTTPublisher] / hubInfoFromUnit); this stamp only fills what is
	// already known and never gates anything. Multi-CCU: one entry per central.
	// The nil check is on the BRIDGE, not just the Wiring: disabling MQTT at
	// runtime keeps the Wiring alive and points its bridge nowhere. Unlike the
	// hub-discovery re-Start, this call site runs without panic isolation, so
	// an unguarded dereference here does not log — it takes the daemon down.
	if d.mqttWiring != nil {
		if bridge := d.mqttWiring.Bridge(); bridge != nil {
			si := u.SystemInformation()
			bridge.SetHubInfoFor(u.Name(), mqtt.HubInfo{
				Name:    u.Name(),
				Model:   si.Model,
				Version: si.Version,
				Serial:  si.Serial,
				URL:     si.URL,
			})
		}
	}
	return availCloser, climateCloser
}

// wireSouthbound performs the southbound wiring phase of the composition
// root: it builds the backup adapter, runs WireCentrals (per-central
// XML-RPC/BIN-RPC client wiring, device pipeline, paramset hydration),
// starts the persistent VALUES-cache flusher and its health gauges,
// wires per-central health / availability / climate-link refresh
// subscriptions (via wireCentralNorthbound), applies visibility un-ignore
// lists, stamps HubInfo and runs the boot-time MQTT retain/orphan cleanup
// plus the initial snapshot, and starts the periodic unobserved-DP sweep.
//
// It is a behavior-preserving extraction: same operations, order and
// nil-handling as the inline phase. The returned teardown folds the
// phase's inline defers (LIFO) for the caller to defer at the same
// position.
//
// availClosers is the caller-owned slice the Matter phase also appends
// to; this phase appends its device-availability closers to it and the
// returned teardown drains it at the same LIFO position as the inline
// defer did. The slice is passed by pointer and read at teardown time,
// so closers the Matter phase appends after wireSouthbound returns are
// still run. The caller defers teardown (which is the single drain
// point); the inline consuming defer in daemonServeWithDeps is removed.
//
//nolint:funlen // sequential southbound wiring phase of the composition root
func wireSouthbound(ctx context.Context, d southboundWiringDeps, availClosers *[]func()) (result southboundWiring, teardown func()) {
	cfg := d.cfg
	reg := d.reg
	logger := d.logger

	// Teardown collects the phase's inline defers; the caller defers it
	// once so they run in the same LIFO order as the original code.
	var teardowns []func()
	teardown = func() {
		for i := len(teardowns) - 1; i >= 0; i-- {
			teardowns[i]()
		}
	}

	// hubReadyTriggerFn is set when MQTT is configured so the live-adopt path can
	// subscribe runtime-added centrals onto the hub-discovery ready pipeline.
	var hubReadyTriggerFn func()

	// Build the XML-RPC client per (central, interface), wrap it into
	// a backend, register it with the ValueWriter, then pull the device
	// snapshot through the DevicePipeline and load per-channel VALUES
	// paramset descriptions so channels carry their data points.
	//
	// The backup adapter is constructed up-front because WireCentrals
	// injects the live HTTPBackupRestorer into it after the first
	// successful hub handshake.
	backupAdapter := buildBackupAdapter(cfg, reg, logger)
	// WireCentrals returns immediately: each central's southbound bring-up runs
	// in the background, gated on CCU readiness. No wiring timeout here — a
	// co-booting CCU is waited on indefinitely (the bring-up is bounded by the
	// daemon-lifetime ctx + the teardown closer, not a fixed window).
	bringUpMgr, wireErr := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{
		Writer:               d.valueWriter,
		Translations:         d.translations,
		Catalogs:             d.catalogs,
		CallbackServer:       d.callbackSrv,
		CallbackPort:         d.callbackPort,
		CallbackHostFor:      d.callbackHost,
		BINRPCCallbackServer: d.binRPCSrv,
		BINRPCCallbackPort:   d.binRPCPort,
		Backup:               backupAdapter,
		Visibility:           d.visReg,
		MasterValues:         d.masterValuesStore,
		ChannelFlags:         d.channelFlagsOverlay,
		ValuesCache:          d.valuesCacheStore,
		Descriptors:          d.descriptorStores,
		ValuesCacheCentralFilter: func(centralName string) bool {
			return cfg.Persistence.ValuesCache.ValuesCacheEnabled(centralName)
		},
		PersistSerial: serialBackfiller(d.sqCentrals, d.logger),
	}, logger)
	// Background flusher for the persistent VALUES cache. Runs every
	// flush_interval (default 60 s; override via
	// persistence.values_cache.flush_interval) and once more on
	// graceful shutdown. nil-store / nil-registry guards make this a
	// no-op when the feature is disabled.
	flushInterval := cfg.Persistence.ValuesCache.FlushInterval
	if flushInterval <= 0 {
		flushInterval = adapter.DefaultValuesCacheFlushInterval
	}
	if stopFlusher := adapter.WireValuesCacheFlusher(reg, d.valuesCacheStore, flushInterval, logger); stopFlusher != nil { //nolint:contextcheck // WireValuesCacheFlusher has no ctx parameter; it creates its own daemon-lifetime context internally
		teardowns = append(teardowns, stopFlusher)
	}
	// Evict a device's persisted cache rows when it is removed, so an
	// unpaired device does not leave orphaned values_cache rows behind;
	// and wire the opt-in measurement-history recorder, which subscribes
	// to genuine live wire value changes and persists a numeric
	// time-series for SPA charts (no-op when history is off — nil store).
	// See ADR 0040. Both helpers manage their own daemon-lifetime context.
	//nolint:contextcheck // these wiring helpers take no ctx; they bound their own internal contexts
	teardowns = append(
		teardowns,
		adapter.WireValuesCacheEviction(reg, d.valuesCacheStore, logger),
		wireHistoryRecorder(cfg, reg, d.historyStore, d.recordingOverrides, d.healthTracker, logger),
	)
	// Surface the values-cache counters as health gauges so the
	// /diagnostics surface and any Prometheus scraper see how many
	// rows survived the last restart, how many got cast-rejected,
	// and how the periodic flusher is doing.
	if d.healthTracker != nil && d.valuesCacheStore != nil {
		store := d.valuesCacheStore
		d.healthTracker.RegisterGauge("values_cache.restored_rows",
			func() float64 { return float64(store.MetricsSnapshot().RestoredRows) })
		d.healthTracker.RegisterGauge("values_cache.cast_failures",
			func() float64 { return float64(store.MetricsSnapshot().CastFailures) })
		d.healthTracker.RegisterGauge("values_cache.gc_rows_deleted",
			func() float64 { return float64(store.MetricsSnapshot().GCRowsDeleted) })
		d.healthTracker.RegisterGauge("values_cache.flush_batches",
			func() float64 { return float64(store.MetricsSnapshot().FlushBatches) })
		d.healthTracker.RegisterGauge("values_cache.flushed_entries",
			func() float64 { return float64(store.MetricsSnapshot().FlushedEntries) })
		d.healthTracker.RegisterGauge("values_cache.row_count",
			func() float64 { //nolint:contextcheck // gauge callback fires on demand; must not inherit the (cancelled) daemon ctx
				gaugeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				stats, err := store.Stats(gaugeCtx)
				if err != nil {
					return 0
				}
				return float64(stats.Rows)
			})
		d.healthTracker.RegisterGauge("values_cache.value_json_bytes",
			func() float64 { //nolint:contextcheck // gauge callback fires on demand; must not inherit the (cancelled) daemon ctx
				gaugeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				stats, err := store.Stats(gaugeCtx)
				if err != nil {
					return 0
				}
				return float64(stats.ValueJSONSize)
			})
	}
	if wireErr != nil {
		logger.Warn("wire.partial", slog.String("err", wireErr.Error()))
	}
	if bringUpMgr != nil {
		teardowns = append(teardowns, bringUpMgr.Teardown)
	}

	// Wire the per-central north-bound hooks AFTER WireCentrals so the
	// InterfaceClient registry + SystemInformation are populated. Each central
	// is wired independently via wireCentralNorthbound (also used by the
	// live-adopt path); the device-availability + climate closers are chained
	// into teardown. Availability closers go through the caller-owned
	// *availClosers pointer so the later Matter-phase appends are included.
	var climateClosers []func()
	for _, u := range reg.List() {
		availCloser, climateCloser := wireCentralNorthbound(d, u)
		if availCloser != nil {
			*availClosers = append(*availClosers, availCloser)
		}
		if climateCloser != nil {
			climateClosers = append(climateClosers, climateCloser)
		}
	}
	// Appended in this order so shutdown (LIFO) drains climate closers before
	// availability closers — matching the original two inline defers.
	teardowns = append(
		teardowns,
		func() {
			for _, close := range *availClosers {
				close()
			}
		},
		func() {
			for _, close := range climateClosers {
				close()
			}
		},
	)

	// Apply the per-central un_ignore lists from SQLite (seeded from
	// config.yaml when the table is empty) onto the shared visibility
	// registry. Runs after WireCentrals so every central's
	// ModelRegistry is populated with materialised devices that the
	// suppression-mark pass can flip. See docs/ui/unignore-concept.md
	// and visibility_wiring.go.
	applyVisibilityUnIgnore(ctx, cfg, reg, d.visibilityUnIgnoreStore, d.visReg, logger)

	// Re-run the hub publisher once each central's serial resolves. WireCentrals
	// only LAUNCHES the readiness-gated bring-up goroutines and returns before
	// they finish, so at this point SystemInformation (and the serial that gates
	// the whole hub-discovery plane) is still empty for every central — an eager
	// Start here would skip all hub discovery, leaving HA with an "unknown
	// device" parent and no sysvar entities. Drive the (idempotent) re-Start off
	// CentralSouthboundReadyEvent instead; the debounce coalesces a staggered
	// multi-CCU boot into a single re-wire. The seed below covers a central that
	// became ready between the subscribe and this loop.
	if d.mqttWiring != nil && d.hubMQTT != nil {
		hubReadyClosers, hubReadyTrigger := wireHubReadyRestart(ctx, d, reg, logger)
		teardowns = append(teardowns, hubReadyClosers...)
		hubReadyTriggerFn = hubReadyTrigger
	}

	// Boot-time stale cleanup — clear retained channel-aggregate
	// state topics from the previous build before the inventory wave.
	// Necessary when the discovery payload structure changes and
	// brokers still hold incompatible JSON: HA refuses to bind the
	// stale message and the entity stays unavailable until the
	// retained content is replaced. Runs against the legacy aggregate
	// shape (`<base>/<central>/<iface>/<addr>/<ch>/state`) — the
	// follow-up PublishInitialSnapshot republishes the current view
	// for every observed DP.
	//
	// Best-effort: a broker that doesn't support the cleanup
	// subscription path returns errCleanupClientLacksSubscribe; the
	// daemon logs and proceeds.
	if d.mqttWiring != nil {
		if mqttBridge := d.mqttWiring.Bridge(); mqttBridge != nil {
			cleanupWindow := cfg.North.MQTT.EffectiveRetainCleanupWindow()
			cleanupCtx, cleanupCancel := context.WithTimeout(ctx, cleanupWindow+8*time.Second)
			n, cleanupErr := mqttBridge.RunRetainCleanupOnce(cleanupCtx, cleanupWindow)
			cleanupCancel()
			if cleanupErr != nil {
				logger.Warn("mqtt.retain_cleanup", slog.String("err", cleanupErr.Error()))
			} else if n > 0 {
				logger.Info("mqtt.retain_cleanup", slog.Int("evicted", n))
			}
		}
	}

	wireRetainedOrphanSweeps(ctx, d, cfg, logger)

	// Push the post-hydration snapshot of every observed VALUES data
	// point through the EventBridge so the broker carries retained
	// state (and HA Discovery configs) for every device immediately
	// after start, not just after the first CCU-driven change. Centrals
	// whose readiness-gated bring-up is still running are skipped — their
	// snapshot (and orphan sweep) rides on CentralSouthboundReadyEvent,
	// after finishIngest applied the visibility marks. Snapshotting a
	// mid-ingest central published its entire MASTER paramsets retained.
	d.bridge.PublishInitialSnapshot(ctx)

	// Periodic unobserved-DP sweep — retries LoadValue for the
	// RELEVANT_INIT + readable-event whitelist on a slow cadence.
	// Catches stragglers that fetch_all_device_data omitted (event
	// DPs that have not fired) and fills any holes that opened
	// during a brief CCU outage between push events. 5-minute
	// cadence (DefaultUnobservedSweepInterval) — long enough that
	// steady-state operation costs nothing extra on the CCU.
	unobservedSweep := adapter.NewUnobservedSweep(reg, logger)
	stopSweep := adapter.StartUnobservedSweepLoop(
		ctx, unobservedSweep, 0, logger,
	)
	teardowns = append(teardowns, stopSweep)

	result = southboundWiring{
		backupAdapter:   backupAdapter,
		bringUpManager:  bringUpMgr,
		hubReadyTrigger: hubReadyTriggerFn,
	}
	return result, teardown
}

// wireRetainedOrphanSweeps installs the EventBridge post-snapshot hook that
// runs the retained-orphan sweeps. They are deferred until a central's
// snapshot pass has actually published, because both compare the broker's
// retained store against the bridge's bookkeeping (`declared` for
// HA-Discovery configs, rawTopics/configCache for the raw plane) — running
// them against a central the ready gate skipped would classify every
// legitimate topic as an orphan and wipe it. The hook fires from whichever
// path publishes the snapshot first: the boot-time catch-up for a central
// that latched ready early, or the southbound-ready path for the common
// readiness-gated case.
//
// The sweeps evict what previous builds/boots retained but the current model
// no longer publishes: MASTER paramsets that escaped before the visibility
// gate closed the mid-ingest window, suppressed VALUES parameters, retired
// profiles, removed devices. Once per boot and per central; the HA-Discovery
// sweep additionally only for the bridge's default central, whose node_id
// namespace it is scoped to. Best-effort — a broker without subscribe
// support just skips.
func wireRetainedOrphanSweeps(ctx context.Context, d southboundWiringDeps, cfg *config.Config, logger *slog.Logger) {
	if d.mqttWiring == nil || d.mqttWiring.Bridge() == nil {
		return
	}
	defaultCentral := pickFirstCentral(cfg)
	cleanupWindow := cfg.North.MQTT.EffectiveRetainCleanupWindow()
	var sweptCentrals sync.Map
	d.bridge.SetPostCentralSnapshotHook(func(_ context.Context, centralName string) {
		if _, already := sweptCentrals.LoadOrStore(centralName, struct{}{}); already {
			return
		}
		mqttBridge := d.mqttWiring.Bridge()
		if mqttBridge == nil {
			return
		}
		// The sweeps block on a subscribe window; keep them off the
		// snapshot caller (boot path / durable-job worker). The
		// HA-Discovery sweep runs FIRST: its retained-snapshot subscribe
		// must see a quiet broker, and the raw sweep can evict thousands
		// of topics whose in-flight QoS deliveries would otherwise
		// stretch the discovery snapshot past its window (measured: only
		// half the orphaned discovery configs were seen when the raw
		// sweep's evictions preceded the subscribe).
		go func() {
			// Generous budget: the subscribe windows are short, but the
			// eviction bursts publish thousands of retained-clear
			// messages each awaiting its PUBACK — a window-derived
			// timeout expired mid-burst and silently dropped the tail
			// (measured: ~200 orphans per boot survived). Bounded by the
			// daemon ctx for shutdown.
			sweepCtx, sweepCancel := context.WithTimeout(ctx, 5*time.Minute)
			defer sweepCancel()
			if centralName == defaultCentral {
				n, err := mqttBridge.RunDiscoveryOrphanCleanupOnce(sweepCtx, cleanupWindow)
				if err != nil {
					logger.Warn("mqtt.discovery_orphan_cleanup", slog.String("err", err.Error()))
				} else if n > 0 {
					logger.Info("mqtt.discovery_orphan_cleanup", slog.Int("evicted", n))
				}
			}
			n, err := mqttBridge.RunRawOrphanCleanupOnce(sweepCtx, centralName, cleanupWindow)
			if err != nil {
				logger.Warn("mqtt.raw_orphan_cleanup",
					slog.String("central", centralName), slog.String("err", err.Error()))
			} else if n > 0 {
				logger.Info("mqtt.raw_orphan_cleanup",
					slog.String("central", centralName), slog.Int("evicted", n))
			}
		}()
	})
}

// wireHubReadyRestart subscribes every central's bus to the debounced
// hub-discovery re-Start (serial resolution / live adopt), chains the
// post-ready hook (mDNS TXT refresh, ADR 0058), and seeds the trigger
// for centrals that became ready before the subscription.
func wireHubReadyRestart(ctx context.Context, d southboundWiringDeps, reg *central.Registry, logger *slog.Logger) (closers []func(), trigger func()) {
	var readyBuses []*events.Bus
	for _, u := range reg.List() {
		if u.EventBus != nil {
			readyBuses = append(readyBuses, u.EventBus)
		}
	}
	closers, trigger = wireHubDiscoveryOnReady(
		ctx, readyBuses, func(rctx context.Context) {
			d.hubMQTT.Start(rctx)
			if d.postHubReady != nil {
				d.postHubReady()
			}
		},
		hubDiscoveryReadyDebounce, logger,
	)
	for _, u := range reg.List() {
		if u.IsSouthboundReady() {
			trigger()
			break
		}
	}
	return closers, trigger
}
