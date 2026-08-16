// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// centralScopedValuesCache returns deps.ValuesCache when the filter
// (if any) allows the named central; nil otherwise. Keeps the
// per-central decision out of the pipeline wiring itself.
func centralScopedValuesCache(deps WireDeps, centralName string) *sqlite.ValuesCacheStore {
	if deps.ValuesCache == nil {
		return nil
	}
	if deps.ValuesCacheCentralFilter != nil && !deps.ValuesCacheCentralFilter(centralName) {
		return nil
	}
	return deps.ValuesCache
}

// connectionCheckerInterval is the cadence at which each interface's
// per-client probe goroutine fires (detection only — actual recovery
// is owned by ConnectionRecoveryCoordinator). 15s is the established
// upstream value: short enough that a CCU reboot is noticed within a
// scheduler tick, long enough that the CCU's RPC surface is not
// hammered.
const connectionCheckerInterval = 15 * time.Second

// WireDeps bundles the shared infrastructure [WireCentrals] needs
// beyond the config + registry pair. Fields marked optional can be
// nil — the daemon degrades gracefully (no callbacks wired).
type WireDeps struct {
	Writer       *client.ValueWriter
	Translations *ccudata.Translations
	// Catalogs provides the daemon i18n translations used to produce
	// human-readable display names for alarm and service message codes.
	// May be nil — callers that omit it get raw code strings as display names.
	Catalogs       *i18n.Catalogs
	CallbackServer *rpcserver.XMLRPCServer // optional
	// CallbackPort is the effective XML-RPC callback port. Required when
	// CallbackServer != nil. The host is resolved per-central via
	// CallbackHostFor, so a daemon serving a local and an external CCU
	// advertises a reachable address to each.
	CallbackPort int
	// CallbackHostFor returns the host the given central should push
	// callbacks to (loopback for a co-located CCU, the LAN IP for an
	// external one, or an explicit PublicHost override). Required when
	// CallbackServer or BINRPCCallbackServer is non-nil. Returning "" for
	// a central skips callback registration for it — that central still
	// works, just without push events.
	CallbackHostFor func(cc *config.CentralConfig) string

	// BINRPCCallbackServer is the shared BIN-RPC TCP callback listener
	// for CUxD. Optional — CUxD interfaces are skipped when nil.
	BINRPCCallbackServer *rpcserver.BINRPCServer
	// BINRPCCallbackPort is the effective port of BINRPCCallbackServer.
	// The host is resolved per-central via CallbackHostFor. Required when
	// BINRPCCallbackServer is non-nil.
	BINRPCCallbackPort int

	// Backup, when non-nil, gets one [HTTPBackupRestorer] wired per
	// central via [BackupAdapter.SetRestorerForCentral] as each central's
	// bring-up resolves its JSON-RPC session — so a fleet with several
	// registered centrals restores every backup to the CCU that produced
	// it (see ADR 0002; [BackupAdapter.Restore] resolves the owner from
	// the backup id and never falls back to a different central's
	// restorer). The unscoped [BackupAdapter.TriggerBackup] still targets
	// only the first registered central for backward compatibility;
	// callers that need a specific central use
	// [BackupAdapter.TriggerBackupForCentral].
	Backup *BackupAdapter

	// Visibility, when non-nil, is installed on every per-central
	// [DevicePipeline] via [DevicePipeline.WithVisibility] so the
	// southbound paramset hydration consults the visibility decider
	// before creating each generic data point. The required-parameter
	// whitelist must be pre-populated by the caller via
	// [visibility.Registry.SetRequiredParameters] (see daemon
	// composition root).
	//
	// nil disables the gate (all parameters pass through) — used in
	// tests and tooling that drive [WireCentrals] without a daemon
	// composition ( E.13).
	Visibility *visibility.Registry

	// MasterValues, when non-nil, is installed on every per-central
	// [DevicePipeline] via [DevicePipeline.WithMasterValuesStore]. The
	// pipeline then prefers the persisted MASTER snapshot over a fresh
	// getParamset(MASTER) call at hydration time, which is the only way
	// to avoid the CCU duty-cycle burst on a cold CCU+daemon-reboot.
	//
	// nil disables the cache (every channel hits the CCU at hydration).
	MasterValues *sqlite.MasterValuesStore

	// ChannelFlags, when non-nil, is the operator per-channel override
	// overlay (G12) re-applied onto every rebuilt channel by each
	// per-central [DevicePipeline] via [DevicePipeline.WithChannelFlags].
	ChannelFlags *channelflags.Overlay

	// ValuesCache, when non-nil, is installed on every per-central
	// [DevicePipeline] via [DevicePipeline.WithValuesCacheStore]. The
	// pipeline applies the persisted wire-VALUES snapshot between
	// hydration and the live seedValues round so the SPA / MQTT /
	// Matter surfaces have the last known values immediately on boot.
	//
	// nil disables the cache (every cold boot starts with unobserved
	// data points until the first push event or fetch_all_device_data
	// round fills them).
	ValuesCache *sqlite.ValuesCacheStore

	// ValuesCacheCentralFilter, when non-nil, is consulted per
	// central before the [sqlite.ValuesCacheStore] is wired into
	// that central's [DevicePipeline]. Returns false to skip the
	// cache for that central — useful for excluding test-rig
	// centrals from the persistence path in multi-CCU setups. The
	// daemon's composition root builds this from the
	// `persistence.values_cache.disabled_centrals` config key.
	ValuesCacheCentralFilter func(centralName string) bool

	// PersistSerial, when non-nil, is invoked once per central after its
	// hub bring-up resolves the CCU serial. The composition root wires it to
	// persist the serial into the centrals store (backfilling rows that
	// predate serial capture), so SSDP discovery can recognise a configured
	// central by serial regardless of its host. Best-effort: errors are the
	// callee's concern and must not fail the bring-up.
	PersistSerial func(ctx context.Context, centralName, serial string)

	// Descriptors, when populated, enables the persistent
	// device-description + paramset-description caches: each central's
	// registries are hydrated from SQLite before its bring-up starts
	// (see [WireDescriptorPersistence]) and mirror every later mutation
	// back. The zero value disables the feature.
	Descriptors DescriptorStores
}

// WireCentrals performs the full southbound bootstrap: per central it
// wires the hub (JSON-RPC), every configured interface (XML-RPC +
// backend + ingest + value seeding), and — when [WireDeps.CallbackServer]
// is set — registers the callback handler + announces the daemon's
// callback URL to the CCU so live events start flowing.
func WireCentrals(
	ctx context.Context,
	cfg *config.Config,
	reg *central.Registry,
	deps WireDeps,
	logger *slog.Logger,
) (*BringUpManager, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Each central's southbound bring-up runs in its own background goroutine,
	// gated on CCU readiness (checkrega.cgi == OK). WireCentrals returns
	// immediately so the daemon's north-bound surface (REST/SPA/health) is up
	// while a co-booting CCU is still warming — the central then brings itself
	// up, fully and once, the moment its CCU is ready (so devices are created
	// already carrying their names). bringUpCtx is cancelled by teardown; wg
	// drains the goroutines on shutdown. The closers slice is shared across the
	// concurrent goroutines, so it is mutex-guarded. bringUpCtx is derived from
	// the daemon-lifetime ctx (NOT a short wiring timeout) so a co-booting CCU
	// is waited on indefinitely; teardown cancels it explicitly on shutdown.
	bringUpCtx, cancelBringUp := context.WithCancel(ctx)
	mgr := newBringUpManager()
	mgr.parentCancel = cancelBringUp
	// Capture the shared wiring inputs so a single central can be built + started
	// at runtime (BringUpManager.AddCentral) exactly as at boot.
	mgr.parentCtx = bringUpCtx
	mgr.cfg = cfg
	mgr.deps = deps
	mgr.logger = logger

	// Callback-handler routing is local (no CCU I/O), registered synchronously up
	// front inside buildAndStart; the actual init()/announce to the CCU happens
	// inside the gated bring-up when the interface backends come up. The routing
	// is permanent for the central's life — it survives a re-init (only the gated
	// bring-up generation is cycled).
	for i := range cfg.Centrals {
		cc := &cfg.Centrals[i]
		unit, ok := reg.Get(cc.Name)
		if !ok {
			logger.Warn("wire.central.not_registered", slog.String("central", cc.Name))
			continue
		}
		//nolint:contextcheck // buildAndStart→start runs the gated bring-up on the handle's teardown-bounded parent ctx, not the short-lived wiring ctx
		mgr.add(mgr.buildAndStart(cc, unit))
	}

	// Bring-up is asynchronous: there is no synchronous aggregate error to
	// return. Per-central failures surface via logs + the "waiting for CCU"
	// health state and are retried by the gate.
	return mgr, nil
}

// gatedCentralBringUp waits until the CCU reports ready (checkrega.cgi == OK),
// then runs the central's full southbound bring-up once and signals north-bound
// adapters to publish it. It loops: if the hub load fails right after the gate
// (the CCU dropped again), it returns to waiting and re-probes. Returns when the
// bring-up succeeds or ctx is cancelled (teardown). Waits indefinitely for a
// never-ready CCU rather than bringing the central up half-loaded.
func gatedCentralBringUp(
	ctx context.Context,
	cfg *config.Config,
	cc *config.CentralConfig,
	unit *central.Unit,
	deps WireDeps,
	callbackURL, binRPCCallbackAddr string,
	cbHandlers *CallbackHandlers,
	addCloser func(func()),
	logger *slog.Logger,
) {
	const reGateBackoff = 5 * time.Second
	recordCentralWaiting(unit)
	for {
		if !WaitForCCUReady(ctx, *cc, CCUReadinessConfig{Timeout: -1}, logger) {
			return // teardown
		}
		if err := bringUpCentral(ctx, cfg, cc, unit, deps, callbackURL, binRPCCallbackAddr, cbHandlers, addCloser, logger); err == nil {
			// Bring-up done: clear the transient "waiting for CCU" component so it
			// does not linger and decay to UNKNOWN (which would drag the overall
			// health verdict down forever even though the central is now healthy).
			// The interface + central components take over from here.
			resolveCentralWaiting(unit)
			// Latch the queryable ready flag BEFORE publishing the event:
			// subscribers that exist see the event; late subscribers seed
			// from [central.Unit.IsSouthboundReady]. A subscriber wired
			// between the latch and the publish observes readiness through
			// both paths, so no interleaving can lose the transition.
			unit.MarkSouthboundReady()
			events.Publish(unit.EventBus, hmevent.CentralSouthboundReadyEvent{
				Base:        hmevent.NewBase(),
				CentralName: cc.Name,
			})
			recordCentralReadiness(unit, hmenum.ReadinessReady, len(cc.Interfaces), len(cc.Interfaces))
			// A regression detector, not a routine check: both backends
			// wire the sysvar write path during hub bring-up, so this
			// line should never appear. It exists because the omission it
			// reports was invisible for a whole release — the alarm
			// sysvar mirror created its variable, never wrote a value,
			// and reported success while doing so.
			if unit.Hub != nil && !unit.Hub.HasSysvarValueWriter() {
				logger.Warn("wire.hub.sysvar_writer.missing",
					slog.String("central", cc.Name),
					slog.String("impact", "alarm sysvar mirror and every sysvar write will fail"))
			}
			logger.Info("wire.central.ready", slog.String("central", cc.Name))
			return
		}
		// Hub load failed even though the CCU just reported ready — most likely
		// the CCU dropped again between the probe and the load. Back to waiting.
		recordCentralWaiting(unit)
		select {
		case <-ctx.Done():
			return
		case <-time.After(reGateBackoff):
		}
	}
}

// startupHealthComponent names the transient health component that carries a
// central's "waiting for its CCU to boot" state. Kept in one place so the
// record and resolve sides never drift.
func startupHealthComponent(centralName string) string { return "startup." + centralName }

// recordCentralWaiting marks a central as waiting for its CCU to finish
// booting. Recorded via RecordQuality (capped at DEGRADED) on a dedicated
// `startup.<central>` component so the wait is visible in diagnostics without
// tripping ServiceAvailability to 503 — a co-booting CCU is a startup state,
// not a hard failure.
func recordCentralWaiting(unit *central.Unit) {
	if unit == nil || unit.Health == nil {
		return
	}
	unit.Health.RecordQuality(startupHealthComponent(unit.Name()), "waiting for CCU to become ready")
	recordCentralReadiness(unit, hmenum.ReadinessWaitingForCCU, 0, 0)
}

// resolveCentralWaiting removes the transient "waiting for CCU" component once
// the central's bring-up has succeeded. Without this the last DEGRADED sample
// goes stale after the tracker's StaleAfter window and decays to UNKNOWN,
// pinning the overall health verdict at "unknown" even though the central and
// its interfaces are healthy.
func resolveCentralWaiting(unit *central.Unit) {
	if unit == nil || unit.Health == nil {
		return
	}
	unit.Health.Unregister(startupHealthComponent(unit.Name()))
}

// recordCentralReadiness updates the queryable per-central readiness phase and
// publishes the change so north-bound adapters (REST/WS) reflect bring-up live.
func recordCentralReadiness(unit *central.Unit, phase hmenum.ReadinessPhase, loaded, total int) {
	if unit == nil {
		return
	}
	unit.SetReadiness(phase, loaded, total)
	if unit.EventBus != nil {
		events.Publish(unit.EventBus, hmevent.CentralReadinessChangedEvent{
			Base:             hmevent.NewBase(),
			CentralName:      unit.Name(),
			Phase:            phase,
			InterfacesLoaded: loaded,
			InterfacesTotal:  total,
		})
	}
}

// bringUpCentral runs a central's full southbound bring-up against a ready CCU.
// Order matters: the hub load (names/rooms/functions) runs FIRST so every
// device the pipeline then creates already carries its CCU-assigned name — the
// whole point of gating on readiness. It returns an error only when the hub
// load fails (CCU not actually serving) BEFORE any local wiring or closers are
// registered, so the gated caller can re-probe and retry without double-wiring.
// Per-interface load failures (residual RPC lag) are retried thinly inside
// wireInterface and otherwise logged, never surfaced as a bring-up failure.
func bringUpCentral( //nolint:funlen // composition/wiring: long sequential setup
	ctx context.Context,
	cfg *config.Config,
	cc *config.CentralConfig,
	unit *central.Unit,
	deps WireDeps,
	callbackURL, binRPCCallbackAddr string,
	cbHandlers *CallbackHandlers,
	addCloser func(func()),
	logger *slog.Logger,
) error {
	writer := deps.Writer
	translations := deps.Translations

	// Hub first: a failure here means the CCU is not yet serving JSON-RPC.
	// Return before any wiring so the gate retries cleanly with no half-state.
	recordCentralReadiness(unit, hmenum.ReadinessLoadingHub, 0, 0)
	runner, hubData, hubCloser, err := WireHub(ctx, *cc, unit, logger, deps.Catalogs, cfg.Locale)
	if err != nil {
		logger.Warn("wire.hub.failed",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
		return fmt.Errorf("hub: %w", err)
	}
	addCloser(hubCloser)
	// Backfill the central's serial into the store now that the hub bring-up has
	// resolved it (same canonical form SSDP discovery produces), so a central
	// configured by host — e.g. localhost, where a host match against the
	// discovered IP can never succeed — is recognised as already-configured by
	// serial. Best-effort and idempotent; never blocks or fails the bring-up.
	if deps.PersistSerial != nil {
		if serial := unit.SystemInformation().Serial; serial != "" {
			deps.PersistSerial(ctx, cc.Name, serial)
		}
	}
	// Wire this central's own backup restorer every time it comes up
	// successfully. Keyed by cc.Name so each central gets exactly its own
	// restorer — a re-gate after reconnect simply refreshes the wrapped
	// JSON-RPC session, it never lets one central's restorer answer for
	// another's backups.
	if deps.Backup != nil {
		deps.Backup.SetRestorerForCentral(cc.Name, &HTTPBackupRestorer{
			BaseURL:               ccuBaseURLFor(*cc),
			Session:               runner.Client(),
			InsecureSkipTLSVerify: cc.TLSInsecureSkipVerify,
		})
		logger.Info("wire.backup.restorer_ready", slog.String("central", cc.Name))
	}

	// Per-central interface→backend lookup, populated by wireInterface as each
	// iface comes up. The CONFIG_PENDING hook resolves the backend lazily.
	backendsByInterface := newBackendRegistry()
	wireConfigPendingHook(ctx, unit, deps.MasterValues, cc.Name, backendsByInterface.getter, logger)

	// Source-token lifecycle on the central's bus: ConnectionLost → stale,
	// RecoveryCompleted → live.
	addCloser(WireValueSourceLifecycle(unit, logger))

	pipeline := NewDevicePipeline(unit).
		WithTranslations(translations, cfg.Locale).
		WithNames(hubData.Names).
		WithRooms(hubData.Rooms).
		WithFunctions(hubData.Functions).
		WithVisibility(deps.Visibility).
		WithCustomDPBehavior(
			cc.Behavior.LightLastBrightnessEnabled(),
			cc.Behavior.UseGroupChannelForCoverStateEnabled(),
		).
		WithFirmwareCheck(cc.Behavior.EnableDeviceFirmwareCheckEnabled()).
		WithMasterValuesStore(deps.MasterValues, cc.Name).
		WithValuesCacheStore(centralScopedValuesCache(deps, cc.Name), cc.Name).
		WithChannelFlags(deps.ChannelFlags)

	// JSON-RPC Caller adapter so CcuBackend can dispatch JSON-RPC-only ops.
	var jCaller backends.Caller
	if runner != nil {
		jCaller = &jsonrpcCaller{client: runner.Client()}
	}

	total := len(cc.Interfaces)
	loaded := 0
	recordCentralReadiness(unit, hmenum.ReadinessLoadingDevices, loaded, total)
	for _, ifaceSpec := range cc.Interfaces {
		iface := hmenum.Interface(strings.TrimSpace(ifaceSpec.Name))
		closer, err := wireInterface(ctx, *cc, iface, unit, pipeline, writer, runner, callbackURL, cfg.Reliability, deps.MasterValues, backendsByInterface, jCaller, deps.BINRPCCallbackServer, binRPCCallbackAddr, logger)
		if err != nil {
			logger.Warn("wire.interface.failed",
				slog.String("central", cc.Name),
				slog.String("interface", string(iface)),
				slog.String("err", err.Error()))
			continue
		}
		addCloser(closer)
		logger.Info("wire.interface.ok",
			slog.String("central", cc.Name),
			slog.String("interface", string(iface)))
		loaded++
		recordCentralReadiness(unit, hmenum.ReadinessLoadingDevices, loaded, total)
	}

	// Periodic data-refresh handler (the fetch-all-device-data reconciliation
	// safety net). runner is non-nil here — the hub load above succeeded.
	wireLoadAndRefresh(unit, pipeline, cc.Interfaces, runner, logger)

	// Hot-plug: hand freshly announced devices (newDevices callback) to the
	// pipeline so a device paired at runtime is materialised without a
	// daemon restart. Installed only AFTER every interface's bring-up so a
	// callback racing the initial ingest cannot double-hydrate: before this
	// point the ingestor is nil and NewDevices degrades to registry
	// bookkeeping — those devices arrive through the bring-up's own
	// ListDevices anyway. Reset on teardown so a re-init generation never
	// leaves a stale closure (old backends, old pipeline) behind.
	if cbHandlers != nil {
		var ddLoader *devicedetails.Loader
		if runner != nil {
			ddLoader = devicedetails.NewLoaderForJSONRPC(unit.DeviceDetails, runner.Client(), cc.Name, logger)
		}
		unit.SetDeviceIngestFn(newHotplugIngestor(
			unit, pipeline, writer, runner, backendsByInterface.operations, ddLoader, logger,
		))
		addCloser(func() { unit.SetDeviceIngestFn(nil) })
	}

	// Late-binding handlers: resolve the primary client/backend at call time.
	WireSysvarCreator(unit, writer)
	WireBackupAndDownload(unit, writer)
	// Durable service-message suppression: routes the hub coordinator seam
	// and the ServiceMessages aggregate's Disable/Unsuppress path through the
	// per-interface backend's Interface.suppressServiceMessages call.
	WireServiceMessageSuppressor(unit, writer)
	// Per-interface install-mode data points: one per pairing-capable radio,
	// each writing to its own interface backend (no CCU-wide toggle exists).
	WireInstallModeDPs(unit, writer)
	logger.Info("wire.sysvar_creator.ok", slog.String("central", cc.Name))
	return nil
}

// backendRegistry is the central-scoped interface→backend lookup the
// CONFIG_PENDING hook consults at event time. Populated synchronously
// as each interface comes up in wireInterface.
type backendRegistry struct {
	mu sync.RWMutex
	m  map[string]backends.Operations
}

func newBackendRegistry() *backendRegistry {
	return &backendRegistry{m: make(map[string]backends.Operations)}
}

func (r *backendRegistry) put(interfaceID string, b backends.Operations) {
	if r == nil || interfaceID == "" || b == nil {
		return
	}
	r.mu.Lock()
	r.m[interfaceID] = b
	r.mu.Unlock()
}

// operations returns the raw backend registered for interfaceID, or nil
// when the interface has not been wired (yet). Used by the hot-plug
// ingestor to resolve the southbound backend at callback time.
func (r *backendRegistry) operations(interfaceID string) backends.Operations {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m[interfaceID]
}

// getter is a closure suitable for [wireConfigPendingHook]. Resolves
// the backend lazily at event-fire time so the wiring order between
// the hook installer and per-interface wiring stays simple.
func (r *backendRegistry) getter(interfaceID string) backends.MasterGetter {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	b := r.m[interfaceID]
	r.mu.RUnlock()
	if b == nil {
		return nil
	}
	return b
}

// registerCentralCallbacks resolves the host this central should push
// callbacks to (loopback for a co-located CCU, the LAN IP for an external
// one, or an explicit PublicHost override — see [WireDeps.CallbackHostFor]),
// registers the XML-RPC callback handler, and returns the per-central
// XML-RPC callback URL, the BIN-RPC (CUxD) callback address (same host so
// an external CCU's CUxD reaches us too), the registered handler (nil when
// no XML-RPC callback was registered — the bring-up installs the hot-plug
// ingestor on it once the pipeline exists), and a deregister closure (nil
// when no XML-RPC callback was registered). An empty host skips callback
// registration for the central — it still works, just without push events.
func registerCentralCallbacks(deps WireDeps, cc *config.CentralConfig, unit *central.Unit, logger *slog.Logger) (callbackURL, binRPCCallbackAddr string, handlers *CallbackHandlers, deregister func()) {
	callbackHost := ""
	if deps.CallbackHostFor != nil {
		callbackHost = deps.CallbackHostFor(cc)
	}

	switch {
	case deps.CallbackServer != nil && deps.CallbackPort != 0 && callbackHost != "":
		handlers = NewCallbackHandlers(unit, logger)
		if deps.Writer != nil {
			handlers.SetWriter(deps.Writer)
		}
		handlers.SetDelayNewDeviceCreation(cc.Behavior.DelayNewDeviceCreationEnabled())
		deps.CallbackServer.Register(cc.Name, handlers)
		callbackURL = fmt.Sprintf("http://%s:%d/RPC2/%s", callbackHost, deps.CallbackPort, cc.Name)
		centralName := cc.Name
		srv := deps.CallbackServer
		cbHandlers := handlers
		// Deregister the route first so no new callback can be dispatched, then
		// Stop() the handler to cancel its context and drain the in-flight
		// self-reload / device-refresh goroutines. Without the Stop() call those
		// background goroutines would leak past a live RemoveCentral / shutdown.
		deregister = func() {
			srv.Deregister(centralName)
			cbHandlers.Stop()
		}
	case deps.CallbackServer != nil && callbackHost == "":
		logger.Warn("wire.callback.no_host",
			slog.String("central", cc.Name),
			slog.String("detail", "could not resolve a reachable callback host; CCU will not push events"))
	}

	if deps.BINRPCCallbackServer != nil && deps.BINRPCCallbackPort != 0 && callbackHost != "" {
		binRPCCallbackAddr = fmt.Sprintf("%s:%d", callbackHost, deps.BINRPCCallbackPort)
	}
	return callbackURL, binRPCCallbackAddr, handlers, deregister
}

// ingestAttemptContext returns the context for one boot-time ingest
// attempt, carrying what the retry loop knows about the CCU it is
// talking to.
//
// A co-starting CCU answers the first attempts with http 503 while its
// per-interface RPC service trails ReGaHss, and answers slowly for as
// long as it is still booting. The span layer sees only the result, so
// without these markers a boot that recovered on its second attempt
// shipped `level: error` — and every slow bring-up call shipped a
// warning that resolved itself a few seconds later.
//
// The final attempt (attempt == retries) deliberately drops the retry
// marker: nothing follows it, so an interface that never comes up still
// reports its failure as one. Slowness stays tolerated either way — the
// peer is still booting whether or not this attempt is the last.
func ingestAttemptContext(ctx context.Context, attempt, retries int) context.Context {
	ctx = hmlog.WithExpectedSlowness(ctx)
	if attempt < retries {
		ctx = hmlog.WithRetriedFailures(ctx)
	}
	return ctx
}

//nolint:contextcheck,gocognit,gocyclo,funlen // the probe / async consistency-check / deinit contexts are intentionally rooted in a fresh context (cancelled via their own cancel funcs on teardown), not the wiring ctx — see the per-line notes below; composition/wiring: long sequential setup
func wireInterface(
	ctx context.Context,
	cc config.CentralConfig,
	iface hmenum.Interface,
	unit *central.Unit,
	pipeline *DevicePipeline,
	writer *client.ValueWriter,
	runner *rega.Runner,
	callbackURL string,
	relCfg config.ReliabilityConfig,
	masterValues *sqlite.MasterValuesStore,
	backendReg *backendRegistry,
	jsonCaller backends.Caller,
	binrpcCallbackServer *rpcserver.BINRPCServer,
	binrpcCallbackAddr string,
	logger *slog.Logger,
) (func(), error) {
	// CUxD speaks BIN-RPC natively. It gets its own dedicated wiring
	// path: a BIN-RPC client (outbound calls), a BIN-RPC callback
	// registration (inbound push), and no XML-RPC client at all.
	if iface == hmenum.InterfaceCUxD {
		return wireCUxDInterface(ctx, cc, unit, pipeline, writer, runner, relCfg, masterValues, backendReg, binrpcCallbackServer, binrpcCallbackAddr, logger)
	}

	url, err := interfaceURL(cc, iface)
	if err != nil {
		return nil, err
	}

	// wireID is the canonical, host-independent interface identifier used
	// for all daemon-internal wiring (writer, registries, bus, stamping,
	// MQTT/REST surfaces). initID is the wire-boundary triple advertised to
	// the CCU at init()/deinit() — the CCU echoes it back in callbacks and
	// the inbound handler strips it back to wireID. See [WireInterfaceID] /
	// [InitInterfaceID] (ADR-0024).
	wireID := WireInterfaceID(cc.Name, iface)
	initID := InitInterfaceID(unit.InstanceName(), cc.Name, iface)

	xmlClient, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:                url,
		Username:           cc.Username,
		Password:           cc.Password,
		Interface:          initID,
		Host:               cc.Host,
		InsecureSkipVerify: cc.TLSInsecureSkipVerify,
		Logger:             logger.With(slog.String("interface", wireID)),
		Observer: observer.NewMulti(
			observer.NewLogging(observer.WithLogger(logger), observer.WithSlowThreshold(2*time.Second)),
			observer.NewHealth(unit.Health),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("xmlrpc client: %w", err)
	}

	xmlCaller := &xmlrpcCaller{client: xmlClient}
	announcer := newXMLRPCAnnouncer(xmlClient)

	backendKind := backends.KindFor(iface)

	// W5/W6: create an InterfaceClient that wraps the transport caller
	// with the reliability stack (circuit breaker, retry, throttle,
	// coalescer, ping-pong). The BackendCaller bridges the IC's Call()
	// into the backends.Caller interface expected by the factory.
	//
	// client.Caller uses a []any params slice; xmlrpcCaller uses ...any
	// (backends.Caller convention). Bridge with CallerFunc so both
	// interfaces are satisfied without duplicating the transport.
	xmlSliceCaller := client.CallerFunc(func(ctx context.Context, method string, params []any) (any, error) {
		return xmlCaller.Call(ctx, method, params...)
	})
	// Order-preserving sibling used only by the device-definition export, which
	// must reproduce the CCU's wire member order. Same transport, different
	// reply shape (orderedjson value instead of a flattened map).
	xmlOrderedCaller := client.OrderedCallerFunc(func(ctx context.Context, method string, params []any) (any, error) {
		return xmlCaller.CallOrdered(ctx, method, params...)
	})
	// Build the session-recorder hook that forwards SetValue
	// PutParamset call traces to the CacheCoordinator recorder.
	// The hook is nil-safe on both ends: the IC skips the call
	// when nil and CacheCoordinator.RecordSession is a no-op when
	// no session recorder is wired. Closes the Item-2 gap in
	// (RecordSession-Wiring).
	var sessionHook func(rpcType, method string, params, response any)
	if unit.Cache != nil {
		cache := unit.Cache
		sessionHook = func(rpcType, method string, params, response any) {
			rpc := session.RPCTypeXML
			if rpcType == "json-rpc" {
				rpc = session.RPCTypeJSON
			}
			cache.RecordSession(rpc, method, params, response)
		}
	}

	icCfg := client.Config{
		CentralName:         cc.Name,
		Interface:           iface,
		InitInterfaceID:     initID,
		Caller:              xmlSliceCaller,
		OrderedCaller:       xmlOrderedCaller,
		Enabled:             true,
		Logger:              logger.With(slog.String("interface", wireID)),
		SessionRecorderHook: sessionHook,
		// Gate the reconnect re-registration on the same boot marker that
		// gates the initial bring-up. Without it a reconnect racing a
		// rebooting CCU registers a second time (see [client.Config]).
		// Bounded, unlike the boot gate: the reconnect loop retries with
		// backoff, so a long wait here would stall the client state machine
		// instead of letting it cycle.
		WaitCCUReady: newReconnectReadinessGate(cc, logger),
		// Feeds the central's RPC + service metrics sections. See
		// [newRPCOutcomeHook] for why the observer is resolved per call.
		RPCOutcomeHook: newRPCOutcomeHook(unit, wireID),
	}
	// Operator-supplied reliability overrides default to
	// openccu-loom's Go-idiomatic values when zero; a positive
	// duration pins behaviour. See `example.config.yaml`
	// (reliability: section) for the reference values. The retrier is
	// always built here (not defaulted inside client.New) so its
	// exhausted-chain incident sink is installed from the start.
	icCfg.Retrier = newClientRetrier(unit, wireID, relCfg.CommandRetryInitialDelay)
	// Wire independent per-RPC-class throttle pools (read / write / control)
	// instead of a single shared pool, so a backing-off write does not block
	// reads or liveness pings behind one permit. Each pool bounds its waiter
	// queue; the operator-configured inter-command delay paces writes only.
	// See [perClassThrottlePools].
	icCfg.ReadThrottle, icCfg.WriteThrottle, icCfg.ControlThrottle = perClassThrottlePools(relCfg.CommandThrottleInterCommandDelay)
	ic, err := client.New(icCfg)
	if err != nil {
		return nil, fmt.Errorf("interface client: %w", err)
	}
	wireClientReliability(unit, ic, wireID)
	bcaller := client.NewBackendCaller(ic, hmenum.CommandPriorityLow)

	backend, err := backends.FactoryWithKind(iface, backendKind, backends.FactoryInput{
		XMLRPC:    bcaller,
		JSONRPC:   jsonCaller,
		Announcer: announcer,
	})
	if err != nil {
		return nil, fmt.Errorf("backend factory: %w", err)
	}
	// Probe runtime capabilities once before the first operation.
	// Failures are soft: the backend keeps its conservative static defaults.
	if initErr := backends.MaybeInitialize(ctx, backend); initErr != nil {
		logger.Warn(
			"backend.initialize failed; using static capability defaults",
			slog.String("interface", string(iface)),
			slog.Any("err", initErr),
		)
	}

	// Wire the ReGa script runner and HTTP download transport into the CCU
	// backend so operations that require them (e.g. CreateBackupAndDownload,
	// DownloadFirmware) are reachable in production. Both setters are no-ops
	// on non-CCU backends; the type assertion ensures we only call them when
	// the concrete type is *backends.CcuBackend.
	if ccuBackend, ok := backend.(*backends.CcuBackend); ok {
		if runner != nil {
			ccuBackend.SetScriptRunner(runner)
		}
		hc := jsonrpcHTTPClient(cc)
		if hc == nil {
			// No timeout here by design; the transport is ours either way.
			hc = &http.Client{Transport: httpx.NewTransport()}
		}
		jc := runner
		var sessionIDFn func() string
		if jc != nil {
			sessionIDFn = jc.Client().SessionID
			// The backup download (cp_security.cgi) authenticates by session
			// id and serves a login page under HTTP 200 for a stale one, so
			// make sure the session is usable first. EnsureSession renews the
			// live session rather than displacing it: a forced login here
			// would abandon the session the whole central is working with and
			// burn a slot in the CCU's small, WebUI-shared session pool on
			// every backup or firmware download.
			rpcClient := jc.Client()
			ccuBackend.SetSessionRenewer(func(ctx context.Context) (string, error) {
				if err := rpcClient.EnsureSession(ctx); err != nil {
					return "", err
				}
				return rpcClient.SessionID(), nil
			})
		}
		ccuBackend.SetDownloadFirmwareTransport(ccuBaseURLFor(cc), hc, sessionIDFn)

		// Persist device / channel renames to the CCU. The hook resolves
		// the address to its ReGa ISE-ID, then dispatches to Device.setName
		// for a device address or Channel.setName for a channel address
		// (one carrying a ":" channel suffix), both over JSON-RPC. Without
		// this hook a rename would only mutate the in-memory model and be
		// lost on the next device reload. Wired on the CCU backend because
		// the ReGa ISE-ID lookup and setName calls require JSON-RPC.
		renameBackend := ccuBackend
		unit.SetRenameDeviceFn(func(ctx context.Context, address, name string) error {
			iseID, err := renameBackend.GetIseIDByAddress(ctx, address)
			if err != nil {
				return fmt.Errorf("rename: resolve ise-id for %s: %w", address, err)
			}
			if iseID <= 0 {
				return fmt.Errorf("rename: address %s not found on CCU", address)
			}
			if strings.Contains(address, ":") {
				if _, err := renameBackend.RenameChannel(ctx, iseID, name); err != nil {
					return fmt.Errorf("rename channel %s: %w", address, err)
				}
				return nil
			}
			if _, err := renameBackend.RenameDevice(ctx, iseID, name); err != nil {
				return fmt.Errorf("rename device %s: %w", address, err)
			}
			return nil
		})
	}

	// Register the backend so REST / MQTT command paths can dispatch.
	writer.Register(cc.Name, hmtypes.ParseWireInterfaceID(wireID), backend)

	// Also expose the backend through the per-central registry so the
	// CONFIG_PENDING hook can resolve it at event-fire time. The hook
	// was installed before this loop ran, so this is when the lookup
	// becomes usable.
	if backendReg != nil {
		backendReg.put(wireID, backend)
	}

	// Register the IC with the central's client coordinator so the
	// daemon can look up the IC by interface ID (used by, e.g., the
	// W4 CommandTracker hook in the ValueWriter and the metrics aggregator).
	if unit.Clients != nil {
		_ = unit.Clients.Register(&coordinators.ClientEntry{
			InterfaceID: wireID,
			Interface:   iface,
			Host:        cc.Host,
			Client:      ic,
		})
	}
	if unit.MetricsClients != nil {
		unit.MetricsClients.Register(ic)
	}

	// Publish a ClientStateChangedEvent on every state-machine transition
	// so WireHealth (health tracker → central-state re-evaluation) and
	// WireDeviceAvailability learn when the client connects. Keyed by
	// wireID to match the Clients registry + health component names.
	// Without this the startup connect (created→…→connected) is silent:
	// the health tracker never sees the client become healthy and the
	// central stays DEGRADED even though the interface is connected and
	// receiving callbacks.
	if unit.EventBus != nil {
		ic.SetStateChangedBus(unit.EventBus, wireID)
	}

	// W6: wire the IC's PingPong tracker to the central event bus and
	// the connection-recovery coordinator so threshold-crossing events
	// are published and false-alarm PING tracking is suppressed during
	// known outages.
	WirePingPongBus(unit, ic, wireID, unit.Recovery)

	// W5: install the eight-stage recovery pipeline so the coordinator runs
	// Cooldown → TCP_CHECKING → RPC_CHECKING → WARMING_UP →
	// STABILITY_CHECK → RECONNECTING → DATA_LOADING → RECOVERED after
	// every connection loss or circuit-breaker open event. The gates
	// prevent thundering-herd reconnects after power glitches.
	if unit.Recovery != nil {
		captured := ic
		capturedBackend := backend
		capturedWireID := wireID
		capturedInitID := initID
		capturedCallbackURL := callbackURL
		ccForRecovery := cc // captured for the readiness-gated reconnect stage
		// Wire hub refresh into recovery so sysvar/program data is
		// reloaded after a successful reconnect.
		if unit.Hub != nil {
			unit.Recovery.SetHubRefresher(unit.Hub)
		}

		// Resolve the CCU's TCP address from the XML-RPC URL so the
		// TCP-probe stage can dial without knowing the per-interface port.
		ccuTCPAddr := cc.Host + ":2010" // fallback: CCU homematic2 port
		if parsed, parseErr := neturl.Parse(url); parseErr == nil && parsed.Host != "" {
			ccuTCPAddr = parsed.Host // already "host:port"
		}
		ccuTCPAddrCaptured := ccuTCPAddr

		deps := coordinators.RecoveryStageDeps{
			CooldownDuration: 3 * time.Second,
			WarmupDuration:   1 * time.Second,
			TCPProbe: func(ctx context.Context) error {
				conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", ccuTCPAddrCaptured)
				if dialErr != nil {
					return fmt.Errorf("tcp probe %s: %w", ccuTCPAddrCaptured, dialErr)
				}
				_ = conn.Close()
				return nil
			},
			RPCProbe: func(ctx context.Context) error {
				return capturedBackend.Ping(ctx, capturedInitID)
			},
			StabilityProbe: func(ctx context.Context) error {
				return capturedBackend.Ping(ctx, capturedInitID)
			},
			Reconnect: func(rctx context.Context) error {
				// Gate the reconnect on CCU readiness (checkrega.cgi == OK),
				// homogeneously with the boot path: after a CCU reboot ReGaHss +
				// the interface RPC service warm up over ~a minute, so
				// re-initialising before the CCU is serving just churns. A CCU
				// that is merely up answers immediately, so this is a no-op
				// except during an actual reboot. Bounded (default timeout,
				// also cut short by rctx) so a genuinely-down CCU lets the
				// recovery pipeline report failure and back off rather than
				// blocking this stage forever; the pipeline's own retry re-gates.
				if !WaitForCCUReady(rctx, ccForRecovery, CCUReadinessConfig{}, logger) {
					return errors.New("reconnect: CCU not ready (checkrega.cgi != OK)")
				}
				attempts := 0
				ok, err := captured.Reconnect(rctx, capturedBackend, capturedInitID, capturedCallbackURL, nil, &attempts)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("reconnect: CanReconnect returned false")
				}
				return nil
			},
			LoadData: unit.Recovery.RefreshHubDataAfterRecovery(),
		}
		unit.Recovery.WithPipelineFor(capturedWireID, coordinators.DefaultRecoveryPipeline(deps))
		// Wire the daemon logger so recovery.trigger / recovery.started /
		// recovery.completed / recovery.failed surface in the log
		// alongside the existing wire.init.ok / wire.reinit.ok lines.
		unit.Recovery.SetLogger(logger)
		unit.Recovery.Subscribe() //nolint:contextcheck // Subscribe starts a background goroutine; it has no ctx parameter by design
	}

	// Per-interface connection probe — pings the CCU every 30 s so the
	// circuit breaker advances OPEN → HALF_OPEN → CLOSED on its own
	// schedule. Without this loop the breaker only refreshes when an
	// unrelated code path happens to call Do(), which on a quiet daemon
	// can leave it stuck on OPEN for minutes after the CCU recovers.
	//
	// Driven by a standalone time.Ticker goroutine: the central's
	// scheduler is already running by the time WireCentrals fires
	// (reg.StartAll runs before WireCentrals in the daemon bootstrap),
	// so scheduler.Add would reject the late registration.
	probeCentral := cc.Name
	probeWireID := wireID
	probeIC := ic
	probeBus := unit.EventBus
	probeUnit := unit
	//nolint:contextcheck // probe goroutine must outlive the wiring ctx (60s timeout); daemon-lifetime background context is intentional
	probeCtx, probeCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(connectionCheckerInterval)
		defer ticker.Stop()
		publishLost := func() {
			if probeBus == nil {
				return
			}
			events.Publish(probeBus, hmevent.ConnectionLostEvent{
				CentralName: probeCentral,
				InterfaceID: probeWireID,
				Reason:      hmenum.FailureReasonNetwork,
			})
		}
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-ticker.C:
				// Detection only. ConnectionLostEvent triggers the
				// ConnectionRecoveryCoordinator, which owns the
				// actual reconnect pipeline.
				//
				// Three independent signals — any one of them is
				// enough to publish ConnectionLost:
				//
				//  1. Ping fails (non-bypass check_connection — also
				//     drives the circuit breaker on the way through).
				//  2. State lag: client sits on DISCONNECTED / FAILED
				//     while the wire is healthy (previous reconnect
				//     attempt hit a transient 401 during the CCU's
				//     rega-startup window, or the daemon booted
				//     while the CCU was down — without this the
				//     daemon sits stale forever).
				//  3. Silent callback channel (no inbound event
				//     within callbackFreshness). IsCallbackAlive
				//     guards the post-init window so a freshly
				//     initialised client cannot trip the check
				//     before the first push event lands.
				// Until the central latches southbound-ready the CCU is
				// still bringing its interfaces up, and a ping that
				// takes seconds says so rather than reporting a fault.
				// The tolerance ends with the bring-up: once ready, a
				// slow ping is worth a warning again.
				tickCtx := probeCtx
				if probeUnit != nil && !probeUnit.IsSouthboundReady() {
					tickCtx = hmlog.WithExpectedSlowness(probeCtx)
				}
				if !probeIC.CheckConnectionAvailability(tickCtx, false) {
					publishLost()
					continue
				}
				switch probeIC.ClientState() {
				case hmenum.ClientStateDisconnected, hmenum.ClientStateFailed:
					publishLost()
					continue
				case hmenum.ClientStateCreated, hmenum.ClientStateInitializing,
					hmenum.ClientStateInitialized, hmenum.ClientStateConnecting,
					hmenum.ClientStateConnected, hmenum.ClientStateReconnecting,
					hmenum.ClientStateStopping, hmenum.ClientStateStopped:
					// Transient or active states — probe continues to callback-alive check.
				}
				if !probeIC.IsCallbackAlive() {
					publishLost()
				}
			}
		}
	}()
	logger.Info("wire.check_connection.started",
		slog.String("central", probeCentral),
		slog.String("interface", probeWireID),
		slog.Duration("interval", connectionCheckerInterval))

	// For classic HM interfaces (BidCos-RF, BidCos-Wired, VirtualDevices,
	// CUxD), construct a MasterPoller and wire its SchedulePoll as the
	// post-MASTER-write hook on every channel of THIS interface. HmIP
	// interfaces use the CONFIG_PENDING event path instead and register
	// nothing (no polling). The registration is keyed by wireID because the
	// poller reads through this interface's backend and the pipeline is
	// shared by every interface of the central.
	poller := newMasterPollerForInterface(iface, unit, backend, masterValues, wireID, cc.Name, logger) //nolint:contextcheck // poller callback uses context.Background(); outlives the wiring ctx by design
	if poller != nil {
		pipeline.WithMasterRefreshHook(wireID, poller.SchedulePoll)
	} else {
		pipeline.WithMasterRefreshHook(wireID, nil)
	}

	// Pull the device snapshot and hydrate data points, then announce the
	// callback so the CCU pushes live events. Without this the domain stays
	// empty and every `/api/v1/devices` call returns nothing.
	//
	// Wrapped in activate() so a boot-time failure can be retried in the
	// background instead of leaving the interface empty. An add-on that
	// co-starts with the CCU commonly sees the backend answer http 503 /
	// 401 while ReGaHss and the per-interface RPC service warm up; the first
	// listDevices then fails. Without a retry the interface only recovers if
	// an unrelated recovery cycle happens to fire — or never, if the CCU's
	// ping stays responsive while listDevices is still 503. activateCtx is
	// the wiring ctx on the first attempt and a detached, teardown-bounded
	// ctx on every background retry.
	activate := func(activateCtx context.Context) error {
		if err := pipeline.IngestFromBackend(activateCtx, wireID, iface, backend, writer, runner, logger); err != nil {
			return fmt.Errorf("ingest: %w", err)
		}
		logger.Info("wire.ingest.ok",
			slog.String("central", cc.Name),
			slog.String("interface", wireID),
			slog.Int("devices", unit.ModelRegistry.Len()))

		// Devices for this interface are now materialised: (re)establish the
		// device links of every hub data point whose name carries a
		// device/channel identifier. Sysvars/programs were scanned earlier in
		// WireHub (before any device existed), so the association is resolved
		// here as an idempotent post-pass. Cheap and safe to repeat after each
		// interface ingest; the links converge once every interface's devices
		// are present.
		assignHubChannels(unit, logger)

		// Schedule a background paramset-consistency check for the HmIP-RF
		// interface. HmIP-RF and HmIP-Wired devices both arrive through this
		// single service and are affected by the HmIPServer stale-files bug
		// after firmware updates. The check runs asynchronously so it does
		// not block the wiring path; mismatches are logged and can be used
		// to drive re-ingest or alerts.
		if unit.Devices != nil && iface == hmenum.InterfaceHmIPRF {
			var deviceAddrs []string
			for _, d := range unit.ModelRegistry.List() {
				if d.InterfaceID == wireID && !strings.Contains(d.Address, ":") {
					deviceAddrs = append(deviceAddrs, d.Address)
				}
			}
			if len(deviceAddrs) > 0 {
				//nolint:contextcheck // consistency check runs asynchronously and must outlive the wiring ctx (60s timeout)
				unit.Devices.ScheduleParamsetConsistencyCheck(
					context.Background(), iface, hmtypes.ParseWireInterfaceID(wireID), deviceAddrs, backend,
					func(inconsistencies []coordinators.ParamsetInconsistency) {
						for _, inc := range inconsistencies {
							logger.Warn("wire.paramset_inconsistency",
								slog.String("central", cc.Name),
								slog.String("interface", wireID),
								slog.String("device", inc.DeviceAddress),
								slog.Int("missing", len(inc.MissingParameters)))
						}
					},
				)
			}
		}

		// Wire the on-demand value loader on every device that belongs to
		// this interface. The cache + singleflight on the device picks up
		// from here; subsequent `LoadValue` calls (REST / WS reads,
		// reconciler sweeps, RELEVANT_INIT_PARAMETERS bootstrap) coalesce
		// concurrent loads for the same channel/parameter through it.
		for _, d := range unit.ModelRegistry.List() {
			if d.InterfaceID != wireID {
				continue
			}
			d.SetValueLoader(backend)
		}

		// RELEVANT_INIT_PARAMETERS bootstrap
		// `init_base_data_points` (model/device.py:1934-1977) explicitly
		// loads UNREACH / STICKY_UN_REACH / CONFIG_PENDING on channel 0
		// because fetch_all_device_data does not always include them. The
		// daemon's availability tracking depends on these values being
		// present, so we mirror the explicit load here. Errors are logged
		// at debug level — the daemon still works without these (just with
		// availability defaulted to "reachable" until the first push event).
		seedRelevantInitParameters(activateCtx, unit, iface, logger)

		// Readable-events bootstrap
		// (model/device.py:1947-1958) explicitly loads every event DP that
		// reports as readable. fetch_all_device_data only ships DPs with a
		// non-zero timestamp, so events that have not fired since the last
		// CCU restart end up unobserved otherwise — REST/MQTT consumers
		// then see "unknown" until the user actually presses the button.
		seedReadableEvents(activateCtx, unit, iface, logger)

		// Announce the callback URL to the CCU so it starts pushing live
		// events. A non-callback-capable setup (no server, no URL) skips
		// this step and leaves the daemon in read-through mode.
		if callbackURL != "" {
			// Re-confirm CCU readiness immediately before Deinit/Init. The
			// outer gate (gatedCentralBringUp → WaitForCCUReady) only runs
			// once, before this interface's ingest loop starts; activate()
			// itself is retried across the ingestBackoff window below (up
			// to ~33s). A CCU that reboots again inside that window is
			// invisible to the one-time outer gate, and hitting Deinit/Init
			// against it reproduces the exact "deinit fails, init succeeds"
			// race documented on [newReconnectReadinessGate] — there for
			// the reconnect path, here on first bring-up. The probe is
			// short and bounded (unlike the outer gate's unbounded wait) so
			// a genuinely-ready CCU pays only one fast HTTP round trip.
			if !WaitForCCUReady(activateCtx, cc, CCUReadinessConfig{Timeout: activateReadinessProbeTimeout}, logger) {
				return errors.New("ccu not ready for callback registration (checkrega.cgi != OK)")
			}
			// Pre-Init Deinit: tell the CCU to forget any registration
			// previously made for this callback URL before we install
			// the fresh one. Mirrors the recovery pipeline's
			// ReinitProxy (interface_client.go:653) two-step sequence.
			// A previous daemon-run that died without invoking the
			// shutdown closer (SIGKILL, panic, host reboot, pair-test
			// restart) leaves a dangling registration on the CCU; the
			// CCU then fans state-echo events to the orphan URL and our
			// fresh process never receives them — the live-Subscribe
			// path (Matter, WS Hub, MQTT) then reports stale state
			// indefinitely. Best-effort: a Deinit failure does not abort
			// the subsequent Init (the CCU may already have timed the
			// old registration out, or this is a first-ever boot).
			if err := backend.Deinit(activateCtx, callbackURL); err != nil {
				logger.Debug("wire.deinit.pre_init",
					slog.String("central", cc.Name),
					slog.String("interface", initID),
					slog.String("err", err.Error()))
			}
			// Snapshot the last-event monotonic timestamp before the init
			// call. If the CCU's `init` RPC times out — a known
			// VirtualDevices-service-bug pattern — but the listDevices
			// callback was nonetheless dispatched, the event coordinator
			// stamps a fresh time during init. Treating that as success
			// matches the reference init_proxy fallback at
			// interface_client.py:749-781. Best-effort: a missing event
			// coordinator (test fixture) leaves the snapshot at zero, the
			// post-error comparison short-circuits to "no callback seen",
			// and the legacy hard-failure log fires.
			var preInitEventAt time.Time
			if unit.Events != nil {
				if at, ok := unit.Events.LastEventMonotonicForInterface(wireID); ok {
					preInitEventAt = at
				}
			}
			if err := backend.Init(activateCtx, initID, callbackURL); err != nil {
				callbackSeen := false
				if unit.Events != nil {
					if at, ok := unit.Events.LastEventMonotonicForInterface(wireID); ok && at.After(preInitEventAt) {
						callbackSeen = true
					}
				}
				if callbackSeen {
					logger.Info("wire.init.timeout_callback_received",
						slog.String("central", cc.Name),
						slog.String("interface", wireID),
						slog.String("callback", callbackURL),
						slog.String("err", err.Error()),
						slog.String("hint", "CCU processed init() despite RPC timeout; callback received during init window"))
					ensureConnectedClientState(ic, logger)
				} else {
					logger.Warn("wire.init.failed",
						slog.String("central", cc.Name),
						slog.String("interface", wireID),
						slog.String("err", err.Error()))
					// Walk CREATED → INITIALIZING → FAILED → DISCONNECTED so
					// the recovery pipeline finds a CanReconnect-friendly
					// state on the first probe success. Without this the
					// client sits in CREATED forever once the boot-time
					// init() failed, and every subsequent recovery.trigger
					// is rejected with "CanReconnect returned false".
					ensureDisconnectedClientState(ic, err, logger)
				}
			} else {
				logger.Info("wire.init.ok",
					slog.String("central", cc.Name),
					slog.String("interface", wireID),
					slog.String("callback", callbackURL))
				// Walk the client state forward so the recovery pipeline
				// sees a CanReconnect-friendly state on the next CCU
				// outage. Without this the state stays at CREATED, and
				// every recovery.trigger fails immediately with
				// "CanReconnect returned false".
				ensureConnectedClientState(ic, logger)
			}
		}
		return nil
	}

	// Boot-time interface activation. The readiness gate (gatedCentralBringUp)
	// already confirmed the CCU's ReGaHss is serving, so this normally succeeds
	// on the first try; a few short retries cover residual per-interface RPC lag
	// (the XML-RPC service can trail ReGaHss by a few seconds). Inline —
	// wireInterface runs on the central's background bring-up goroutine, so a
	// brief block is fine; ctx-cancel (teardown) aborts the wait. The interface
	// is reported as wired regardless (the closer tracks it for shutdown).
	ingestBackoff := []time.Duration{
		1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second,
	}
ingestLoop:
	for attempt := 0; ; attempt++ {
		err := activate(ingestAttemptContext(ctx, attempt, len(ingestBackoff)))
		if err == nil {
			break
		}
		if attempt >= len(ingestBackoff) {
			// Every retry is spent and the interface stayed empty: no
			// devices, no callbacks, nothing north-bound. That is an
			// outcome, not a transient.
			logger.Error("wire.interface.ingest_failed",
				slog.String("central", cc.Name),
				slog.String("interface", wireID),
				slog.String("err", err.Error()))
			break
		}
		logger.Debug("wire.interface.ingest_retry",
			slog.String("central", cc.Name),
			slog.String("interface", wireID),
			slog.Int("attempt", attempt+1),
			slog.String("err", err.Error()))
		t := time.NewTimer(ingestBackoff[attempt])
		select {
		case <-ctx.Done():
			t.Stop()
			break ingestLoop
		case <-t.C:
		}
	}

	// Closer deregisters the callback + unregisters the backend writer
	// on daemon shutdown. The XML-RPC client itself is stateless.
	centralName := cc.Name
	ifaceID := wireID
	deinitID := initID
	//nolint:contextcheck // teardown closure: deinit runs on a fresh short-timeout ctx, not the already-cancelled wiring ctx
	closer := func() {
		// Stop the connection-probe goroutine first so the next tick
		// does not race against the backend being torn down.
		probeCancel()
		// Stop the MasterPoller before deregistering so in-flight polls
		// do not race against the backend being torn down.
		if poller != nil {
			poller.Close()
		}
		deinitOnShutdown(backend, callbackURL, centralName, deinitID, logger)
		writer.Deregister(centralName, hmtypes.ParseWireInterfaceID(ifaceID))
		if unit.Clients != nil {
			unit.Clients.Remove(ifaceID)
		}
		if unit.MetricsClients != nil {
			unit.MetricsClients.Deregister(iface)
		}
	}
	return closer, nil
}

// ensureDisconnectedClientState walks the InterfaceClient's state machine
// from CREATED → INITIALIZING → FAILED → DISCONNECTED so the recovery
// coordinator can subsequently transition into RECONNECTING. Used when
// the boot-time init() failed (CCU unreachable at daemon start).
//
// cause is the failure that ended the bring-up; it decides the reason the
// FAILED state carries, so an operator reading the interface state sees
// rejected credentials as "auth" rather than "network". A cause the
// classifier cannot place stays "network", because from the operator's side
// an interface that never came up is the interface not being on the wire.
func ensureDisconnectedClientState(ic *client.InterfaceClient, cause error, logger *slog.Logger) {
	if ic == nil {
		return
	}
	reason := hmerr.ExceptionToFailureReason(cause)
	if reason == hmenum.FailureReasonUnknown || reason == hmenum.FailureReasonNone {
		reason = hmenum.FailureReasonNetwork
	}
	transitions := []struct {
		target hmenum.ClientState
		reason string
	}{
		{hmenum.ClientStateInitializing, "wire.init.failed: created→initializing"},
		{hmenum.ClientStateFailed, "wire.init.failed: initializing→failed"},
		{hmenum.ClientStateDisconnected, "wire.init.failed: failed→disconnected (ready for reconnect)"},
	}
	for _, t := range transitions {
		if err := ic.TransitionTo(t.target, t.reason, false, reason); err != nil {
			logger.Debug("wire.init.state_transition_skipped",
				slog.String("target", string(t.target)),
				slog.String("err", err.Error()))
		}
	}
}

// ensureConnectedClientState walks the InterfaceClient's state machine
// from its current CREATED state through INITIALIZING → INITIALIZED →
// CONNECTING → CONNECTED so the connection-loss recovery pipeline can
// later transition into DISCONNECTED + RECONNECTING. Invalid moves are
// silently skipped — the state machine validates each step. Logged at
// Debug so the boot path stays readable.
func ensureConnectedClientState(ic *client.InterfaceClient, logger *slog.Logger) {
	if ic == nil {
		return
	}
	transitions := []struct {
		target hmenum.ClientState
		reason string
	}{
		{hmenum.ClientStateInitializing, "wire.init.ok: created→initializing"},
		{hmenum.ClientStateInitialized, "wire.init.ok: initializing→initialized"},
		{hmenum.ClientStateConnecting, "wire.init.ok: initialized→connecting"},
		{hmenum.ClientStateConnected, "wire.init.ok: connecting→connected"},
	}
	for _, t := range transitions {
		if err := ic.TransitionTo(t.target, t.reason, false, hmenum.FailureReasonNone); err != nil {
			logger.Debug("wire.init.state_transition_skipped",
				slog.String("target", string(t.target)),
				slog.String("err", err.Error()))
		}
	}
}

// interfacePortOverride resolves an operator-configured port override for
// iface, in precedence order: the per-interface [config.InterfaceSpec.Port]
// (what the Config UI's CCUs tab writes), the legacy `ports` map, then the
// central-wide `port`. Returns 0 when no override is set, so the caller falls
// back to the interface's detection default.
//
// The per-interface InterfaceSpec.Port must win: the SPA persists the port a
// user enters in the interface row into Interfaces[].Port, and without this
// lookup the override was stored but never applied (the daemon kept connecting
// on the detection-default port).
func interfacePortOverride(cc config.CentralConfig, iface hmenum.Interface) int {
	for _, s := range cc.Interfaces {
		if s.Name == string(iface) && s.Port > 0 {
			return s.Port
		}
	}
	if p, ok := cc.Ports[string(iface)]; ok && p > 0 {
		return p
	}
	if cc.Port > 0 {
		return cc.Port
	}
	return 0
}

// interfaceRemotePathOverride resolves an operator-configured URL-path
// override for iface from the per-interface [config.InterfaceSpec]. Returns
// "" when unset, so the caller keeps the backend default. The value is
// checked for shape at config load ([config.InterfaceSpec.Validate]), so
// anything non-empty arriving here is an absolute path.
func interfaceRemotePathOverride(cc config.CentralConfig, iface hmenum.Interface) string {
	for _, s := range cc.Interfaces {
		if s.Name == string(iface) && s.RemotePath != "" {
			return s.RemotePath
		}
	}
	return ""
}

// interfaceURL composes the XML-RPC endpoint for (central, interface)
// using the SPECIFICATION §7.2 detection ports. CUxD is BIN-RPC only
// and therefore rejected here — callers that want CUxD must wire the
// BIN-RPC caller separately.
func interfaceURL(cc config.CentralConfig, iface hmenum.Interface) (string, error) {
	if iface == hmenum.InterfaceCUxD {
		return "", errors.New("CUxD requires a BIN-RPC caller; XML-RPC wiring is not applicable")
	}
	ports, ok := hmenum.DetectionPorts[iface]
	if !ok {
		return "", fmt.Errorf("no known port for interface %q", iface)
	}
	port := ports.Plain
	scheme := "http"
	if cc.TLS {
		if ports.TLS == 0 {
			return "", fmt.Errorf("interface %q has no TLS port", iface)
		}
		port = ports.TLS
		scheme = "https"
	}
	// Per-interface override takes precedence over the central-wide
	// fallback so operators can pin, e.g., HmIP-RF to a non-standard
	// port without disturbing other interfaces.
	if ov := interfacePortOverride(cc, iface); ov > 0 {
		port = ov
	}
	// Path mirrors the CCU's XML-RPC routing: /RPC2 is the default
	// endpoint, /groups is the VirtualDevices variant. POSTing to the
	// bare "/" path causes the CCU's putParamset handler to crash
	// internally (Vert.x NPE or fault -5) while reads still succeed —
	// keep paths explicit.
	path := "/RPC2"
	if iface == hmenum.InterfaceVirtualDevices {
		path = "/groups"
	}
	// A reverse-proxied or otherwise non-standard-routed CCU is reached
	// through the operator's own path; the value is shape-validated at config
	// load ([config.InterfaceSpec.Validate]).
	if ov := interfaceRemotePathOverride(cc, iface); ov != "" {
		path = ov
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, cc.Host, port, path), nil
}
