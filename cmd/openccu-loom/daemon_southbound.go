// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
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
	"github.com/SukramJ/openccu-loom/internal/wiring"
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
	channelFlagsStore   *sqlite.ChannelFlagsStore
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
	// bootCleanups holds the once-per-process retained-store scrubs. It is
	// shared with the supervisor's connect hook so whichever of the two first
	// sees a live bridge runs them. Nil-safe.
	bootCleanups *bootRetainCleanups
	bridge       *adapter.EventBridge
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
		default:
			// Not an error, but not nothing either: the UPDATE matched no row.
			// Either the serial was already stored (the ordinary case on every
			// boot after the first) or the row does not exist yet, which is
			// what happens on a first boot where the centrals table is seeded
			// after this callback has already run. The silent version of this
			// branch is why that ordering defect survived: the miss looked
			// exactly like success.
			logger.Debug("central.serial.backfill_no_row",
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
	// no-op when the feature is disabled. It carries the same per-central
	// opt-out as the restore half above — otherwise an excluded CCU is kept
	// out of the cache on read and written to it on every tick.
	flushInterval := cfg.Persistence.ValuesCache.FlushInterval
	if flushInterval <= 0 {
		flushInterval = adapter.DefaultValuesCacheFlushInterval
	}
	flusher := adapter.WireValuesCacheFlusher(reg, d.valuesCacheStore, flushInterval, logger, //nolint:contextcheck // WireValuesCacheFlusher has no ctx parameter; it creates its own daemon-lifetime context internally
		adapter.WithValuesCacheCentralFilter(func(centralName string) bool {
			return cfg.Persistence.ValuesCache.ValuesCacheEnabled(centralName)
		}))
	teardowns = append(teardowns, flusher.Stop)
	// Evict a device's persisted cache rows when it is removed, so an
	// unpaired device does not leave orphaned values_cache rows behind;
	// and wire the opt-in measurement-history recorder, which subscribes
	// to genuine live wire value changes and persists a numeric
	// time-series for SPA charts (no-op when history is off — nil store).
	// See ADR 0040. Both helpers manage their own daemon-lifetime context.
	evictor := adapter.WireValuesCacheEviction(reg, d.valuesCacheStore, logger) //nolint:contextcheck // the eviction handler takes no ctx; each DELETE bounds its own
	// Same eviction, for the persisted MASTER-paramset cache: DevicePipeline's
	// MASTER hydration is cache-first, so without this a device re-paired at
	// the same address after removal is seeded from the previous pairing's
	// stale configuration instead of the CCU's current one.
	masterEvictor := adapter.WireMasterValuesEviction(reg, d.masterValuesStore, logger)
	// And the operator's own overrides: a device's Hidden/Locked channel flags
	// outlived the device being unpaired, so a replacement paired into the same
	// address — routine when hardware is swapped, the CCU reuses addresses —
	// silently inherited the previous device's visibility decisions. A
	// whole-model teardown is excluded inside the evictor: the cache-clear
	// re-init removes every device without the operator asking for any of them
	// to go (see hmevent.DeviceRemovedEvent.ModelTeardown).
	channelFlagsEvictor := adapter.WireChannelFlagsEviction(reg, d.channelFlagsStore, d.channelFlagsOverlay, logger)    //nolint:contextcheck // the eviction handler takes no ctx; each DELETE bounds its own
	stopHistoryRecorder := wireHistoryRecorder(cfg, reg, d.historyStore, d.recordingOverrides, d.healthTracker, logger) //nolint:contextcheck // the recorder bounds its own internal contexts; it takes no ctx
	teardowns = append(
		teardowns,
		evictor.Stop,
		masterEvictor.Stop,
		channelFlagsEvictor.Stop,
		stopHistoryRecorder,
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
	//
	// The un_ignore observer joins the same list: it applies the per-central
	// patterns from SQLite (seeded from config.yaml when the table is empty)
	// onto the shared visibility registry. Registered here, after
	// WireCentrals, so every central's ModelRegistry is populated with
	// materialised devices that the suppression-mark pass can flip; it
	// re-applies for every CCU adopted or removed afterwards. See
	// notes/concepts/ui/unignore-concept.md and visibility_wiring.go.
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
		wireVisibilityUnIgnore(ctx, cfg, reg, d.visibilityUnIgnoreStore, d.visReg, logger),
	)

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

	// Boot-time stale cleanup, ahead of the inventory wave. A broker that
	// was not reachable at this point runs it from the first successful
	// (re)connect instead — see [bootRetainCleanups].
	if d.mqttWiring != nil {
		d.bootCleanups.run(ctx, d.mqttWiring.Bridge())
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

// bootRetainCleanups owns the two retained-store scrubs that have to run once
// per process, before the inventory wave publishes anything:
//
//   - the legacy channel-aggregate state topics
//     (`<base>/<central>/<iface>/<addr>/<ch>/state`) a previous build left
//     behind. When the discovery payload structure changes, brokers still hold
//     incompatible JSON, HA refuses to bind it, and the entity stays
//     unavailable until the retained content is replaced.
//   - discovery configs a previous build published with an empty CCU-serial
//     slot (`loom__…`). Those ids are ambiguous across CCUs and the consumer
//     keys its entity registry on them: republishing the corrected payload on
//     the same topic creates a second entity beside the stale one instead of
//     replacing it. An empty retained payload is what makes the consumer
//     forget the old identity — which is why this must precede the snapshot
//     that re-announces the same entities under a serial-carrying id, unlike
//     the orphan sweeps that deliberately run after it.
//
// Both need a live bridge. The boot path offers one as soon as the model is
// hydrated; when the broker was still down then, the first successful
// (re)connect offers one instead. run consumes the first live bridge it is
// given and is a no-op afterwards, so exactly one of those paths pays for it.
//
// Best-effort throughout: a broker that doesn't support the cleanup
// subscription path returns errCleanupClientLacksSubscribe; the daemon logs
// and proceeds.
type bootRetainCleanups struct {
	cfg    *config.Config
	logger *slog.Logger

	mu sync.Mutex
	// done latches only once both scrubs have actually been attempted (each
	// either succeeded or failed for a reason other than a busy snapshot
	// slot). A slot-busy return means "not attempted", so it must NOT latch
	// done — otherwise the scrub is skipped for the rest of the process life.
	done bool
	// running excludes a second, concurrent attempt. run is called from the
	// boot wiring and from every broker (re)connect, which can overlap; two
	// attempts would contend for the bridge's single snapshot slot and turn
	// one of them into a spurious busy return.
	running bool
}

// retainScrubber is the subset of the MQTT bridge the boot-time retained
// scrubs depend on. Narrowed to a local interface so the once-guard's
// retry-on-busy behaviour can be exercised without a live broker.
// [*mqtt.Bridge] satisfies it.
type retainScrubber interface {
	RunRetainCleanupOnce(ctx context.Context, snapshotWindow time.Duration) (int, error)
	RunUnscopedDiscoveryCleanupOnce(ctx context.Context, snapshotWindow time.Duration) (int, error)
}

// retainCleanupBudgetSlack is added to the configured snapshot window to size
// each scrub's context budget. It has to cover the scrub's own teardown plus a
// generous allowance to WAIT for the bridge's snapshot slot when another sweep
// (a concurrent (re)connect pass, a per-central orphan sweep) holds it — so a
// momentarily busy slot is waited out rather than surfaced as
// [mqtt.ErrSweepSlotBusy] on every attempt. It is comfortably larger than the
// former flat 8s margin, which had to cover the wait and the teardown at once
// and could not. When the wait still exceeds this the once-guard is left
// unlatched and the next (re)connect retries.
const retainCleanupBudgetSlack = 25 * time.Second

// newBootRetainCleanups returns the once-guard for the two boot-time scrubs.
func newBootRetainCleanups(cfg *config.Config, logger *slog.Logger) *bootRetainCleanups {
	return &bootRetainCleanups{cfg: cfg, logger: logger}
}

// completed reports whether both scrubs have run to completion (the once-guard
// has latched). Read under the lock so it never straddles a concurrent attempt.
func (c *bootRetainCleanups) completed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

// run performs both scrubs against bridge, unless a previous call already
// completed them or bridge is nil (no broker link yet — a later caller will
// bring one).
func (c *bootRetainCleanups) run(ctx context.Context, bridge *mqtt.Bridge) {
	// bridge is kept a concrete *mqtt.Bridge here so the nil check is a real
	// pointer comparison; a nil bridge boxed into retainScrubber would be a
	// non-nil interface and slip past it.
	if c == nil || bridge == nil {
		return
	}
	c.runScrub(ctx, bridge)
}

// runScrub is the testable core: it serialises attempts, runs both scrubs, and
// latches the once-guard only when neither scrub was skipped for a busy slot.
func (c *bootRetainCleanups) runScrub(ctx context.Context, s retainScrubber) {
	c.mu.Lock()
	if c.done || c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()

	busy := c.scrub(ctx, s)

	c.mu.Lock()
	c.running = false
	if !busy {
		// Both passes were attempted (a non-busy error still counts — it was
		// tried and reported). Latch so no later (re)connect repeats them.
		c.done = true
	}
	c.mu.Unlock()
}

// scrub runs both retained-store passes and reports whether either was skipped
// because the bridge's snapshot slot stayed busy for the whole budget
// ([mqtt.ErrSweepSlotBusy]) — the signal [runScrub] uses to leave the
// once-guard unlatched so a later (re)connect retries.
func (c *bootRetainCleanups) scrub(ctx context.Context, s retainScrubber) (busy bool) {
	window := c.cfg.North.MQTT.EffectiveRetainCleanupWindow()
	budget := window + retainCleanupBudgetSlack

	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, budget)
	n, cleanupErr := s.RunRetainCleanupOnce(cleanupCtx, window)
	cleanupCancel()
	if errors.Is(cleanupErr, mqtt.ErrSweepSlotBusy) {
		busy = true
	}
	if cleanupErr != nil {
		c.logger.Warn("mqtt.retain_cleanup", slog.String("err", cleanupErr.Error()))
	} else if n > 0 {
		c.logger.Info("mqtt.retain_cleanup", slog.Int("evicted", n))
	}

	unscopedCtx, unscopedCancel := context.WithTimeout(ctx, budget)
	cleared, unscopedErr := s.RunUnscopedDiscoveryCleanupOnce(unscopedCtx, window)
	unscopedCancel()
	if errors.Is(unscopedErr, mqtt.ErrSweepSlotBusy) {
		busy = true
	}
	if unscopedErr != nil {
		c.logger.Warn("mqtt.unscoped_discovery_cleanup", slog.String("err", unscopedErr.Error()))
	} else if cleared > 0 {
		c.logger.Info("mqtt.unscoped_discovery_cleanup", slog.Int("cleared", cleared))
	}
	return busy
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
// profiles, removed devices. Once per boot and per central — both sweeps
// alike: each is scoped to the node-id / topic namespace of the central it
// is called with, and running only the default central's would leave every
// other CCU's orphans unreachable forever. Best-effort — a broker without
// subscribe support just skips.
func wireRetainedOrphanSweeps(ctx context.Context, d southboundWiringDeps, cfg *config.Config, logger *slog.Logger) {
	// Only the Wiring has to exist here, never its bridge: the boot connect
	// may still be failing, and the background retry installs a bridge into
	// this same Wiring minutes later. Returning on a nil bridge left the hook
	// uninstalled for the whole process lifetime, so a daemon that started
	// beside a still-booting broker never swept a single retained orphan.
	if d.mqttWiring == nil {
		return
	}
	d.reg.Manifest().Attach(wiring.Seam{
		Name:         "mqtt.retained_orphan_sweep",
		Collaborator: "post-central-snapshot hook on *mqtt.Bridge",
		Phase:        wiring.PhaseOnce,
		Why:          "the post-snapshot sweeps never run: retained discovery configs for devices this build no longer publishes stay on the broker, so Home Assistant keeps entities the daemon has forgotten, and the raw plane keeps its retired MASTER paramsets, suppressed VALUES parameters and dropped calculated data points too. The boot scrubs cover neither class",
	}, func() { wireRetainedOrphanSweepHook(ctx, d, cfg, logger) })
}

// wireRetainedOrphanSweepHook installs the hook itself, so the seam above
// wraps the handover and nothing else.
func wireRetainedOrphanSweepHook(ctx context.Context, d southboundWiringDeps, cfg *config.Config, logger *slog.Logger) {
	cleanupWindow := cfg.North.MQTT.EffectiveRetainCleanupWindow()
	var sweptCentrals sync.Map
	d.bridge.SetPostCentralSnapshotHook(func(_ context.Context, centralName string) {
		// Resolve the bridge BEFORE claiming the central: a snapshot pass that
		// ran while the link was down published nothing, so recording it as
		// swept would retire the central's only sweep against a broker it
		// never reached.
		mqttBridge := d.mqttWiring.Bridge()
		if mqttBridge == nil {
			return
		}
		if _, already := sweptCentrals.LoadOrStore(centralName, struct{}{}); already {
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
			n, err := mqttBridge.RunDiscoveryOrphanCleanupOnce(sweepCtx, centralName, cleanupWindow)
			if err != nil {
				logger.Warn("mqtt.discovery_orphan_cleanup",
					slog.String("central", centralName), slog.String("err", err.Error()))
			} else if n > 0 {
				logger.Info("mqtt.discovery_orphan_cleanup",
					slog.String("central", centralName), slog.Int("evicted", n))
			}
			n, err = mqttBridge.RunRawOrphanCleanupOnce(sweepCtx, centralName, cleanupWindow)
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
	// The subscription is set up INSIDE the Attach closure, not beside it.
	// It used to be attached with an empty `func() {}`, which made the seam
	// declare itself and wrap nothing: deleting the subscription below left
	// /api/v1/diagnostics/wiring still reporting the seam as wired. A
	// declaration that survives the removal of what it declares is the one
	// state ADR 0065 exists to rule out.
	reg.Manifest().Attach(wiring.Seam{
		Name:         "mqtt.hub_ready_restart",
		Collaborator: "hub-MQTT restart on CCU readiness",
		Phase:        wiring.PhaseOnce,
		Why:          "the hub publisher keeps the empty serial it started with, so hub discovery is skipped and no sysvar, program or service-message entity appears in Home Assistant — and neither does the post-ready mDNS TXT refresh (ADR 0058) that rides the same trigger",
	}, func() {
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
	})
	for _, u := range reg.List() {
		if u.IsSouthboundReady() {
			trigger()
			break
		}
	}
	return closers, trigger
}
