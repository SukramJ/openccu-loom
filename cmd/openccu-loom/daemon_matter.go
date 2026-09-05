// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	gosql "database/sql"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	discoverymdns "github.com/SukramJ/openccu-loom/internal/north/discovery/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matter/bootid"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	matterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	matterschema "github.com/SukramJ/openccu-loom/internal/north/matter/schema"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/attestation"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
	mattersetup "github.com/SukramJ/openccu-loom/internal/north/matter/secure/setup"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// bridgeSessionParameters returns the SessionParameters struct the
// CASE responder emits as Sigma2 context-tag 5 (`responderSessionParams`)
// per Matter §4.14.2.3. Mirrors the bridge's operational `_matter._tcp`
// mDNS advertisement so the commissioner's post-CASE MRP timer aligns
// with the discovery-time hint.
//
// The numeric values match the operational SII/SAI/SAT defaults from
// `mdns/service.go`: SII=500ms, SAI=300ms, SAT=4000ms.
func bridgeSessionParameters() *sigma.SessionParameters {
	return &sigma.SessionParameters{
		SessionIdleInterval:    500,
		SessionActiveInterval:  300,
		SessionActiveThreshold: 4000,
	}
}

// buildMatterAdvertiser selects the mDNS advertiser from mc.MDNSAdvertise and
// returns it together with the closer that releases what it owns beyond the
// Advertiser interface. "noop" is an explicit opt-out; empty or "zeroconf"
// yields the default multicast advertiser.
//
// The closer exists for the subtype responder: it holds two multicast sockets
// and a receive goroutine per address family, and the Zeroconf close path only
// retires the subtype NAMES it registered. Every caller therefore has to
// release it explicitly — on shutdown and on every early return, or a bridge
// that failed to bind its UDP port leaves both sockets and both goroutines
// alive for the process lifetime.
func buildMatterAdvertiser(mc config.NorthMatter, logger *slog.Logger) (adv mdns.Advertiser, closeAdv func()) {
	noClose := func() {}
	switch mc.MDNSAdvertise {
	case "noop":
		// Explicit opt-out only. A commissioner can never discover a
		// bridge that does not advertise, so "quiet" must be a conscious
		// choice, not the unset default.
		return mdns.NewNoop(), noClose
	case "", "zeroconf":
		z := mdns.NewZeroconf()
		closeAdv = noClose
		// Side-car responder so service-subtype browses
		// (`_L<long>._sub`, `_S<short>._sub`, `_V<vid>._sub`,
		// `_CM._sub`) hit a PTR pointing at the primary instance —
		// Apple Home and Google Home filter the mDNS browse via
		// these subtypes and `grandcat/zeroconf` v1.0.0 has no
		// first-class subtype API.
		if r, err := mdns.NewSubtypeResponder(logger); err != nil {
			logger.Warn("matter.bridge.mdns.subtype_init_failed",
				slog.String("err", err.Error()),
				slog.String("hint", "primary records will publish, but Apple Home / Google Home subtype-filtered discovery may fail"))
		} else {
			r.Start(context.Background()) //nolint:contextcheck // the responder serves for the advertiser's lifetime, which is longer than any caller ctx; closeAdv ends it
			z.AttachSubtypeResponder(r)
			closeAdv = func() {
				if err := r.Close(); err != nil {
					logger.Debug("matter.bridge.mdns.subtype_close",
						slog.String("err", err.Error()))
				}
			}
			logger.Info("matter.bridge.mdns.subtype_responder_started",
				slog.String("hint", "PTR responder for `_L*._sub`, `_S*._sub`, `_V*._sub`, `_CM._sub` queries active"))
		}
		logger.Info("matter.bridge.mdns.zeroconf",
			slog.String("hint", "primary record via grandcat/zeroconf, subtypes via side-car responder"))
		return z, closeAdv
	default:
		logger.Warn("matter.bridge.mdns.unknown",
			slog.String("value", mc.MDNSAdvertise),
			slog.String("hint", "falling back to noop; valid: noop|zeroconf"))
		return mdns.NewNoop(), noClose
	}
}

// matterBridgeBundle is the full set of artefacts the daemon needs
// from a started Matter bridge: the bridge itself, a stop closure,
// the underlying matter store, and the inner runtime objects the
// REST/UI layer wires into the [matterbridge.CommissioningWindowOpener]
// when ephemeral-window mode is enabled.
type matterBridgeBundle struct {
	bridge *matterbridge.Bridge
	stop   func()
	store  *matterstore.Store
	opMgr  *operational.Manager
	// subMgr is the IM subscription manager. Carried so the REST
	// diagnostics surface can report how many subscriptions ride on each
	// session — a commissioned controller holding none looks identical
	// to a healthy one from every other angle.
	subMgr *subscription.Manager
	// advertiser is the live mDNS advertiser. Carried so the diagnostics
	// surface can report what is actually announced rather than what the
	// config asked for — the two diverge exactly when discovery fails.
	advertiser     mdns.Advertiser
	opCreds        *mattercore.OperationalCredentials
	configuredPase *matterbridge.PaseAdapter // nil when ConcurrentPairings is enabled or no passcode is configured
	rootRefs       rootClusterRefs           // typed handles for daemon-side lifecycle wiring
	// fabricTeardown runs every runtime consequence of a fabric leaving.
	// Exposed so the REST revoke / factory-reset path runs the SAME fan-out
	// the OperationalCredentials wire command triggers instead of deleting
	// the row and leaving the controller's session, subscription and
	// operational advertisement alive.
	fabricTeardown func(ctx context.Context, fabricIndex uint8)
	// withdrawFabric retires one fabric's operational mDNS instance and
	// republishes the remaining set. The wire command reaches it through the
	// cluster's own hooks; the REST path has no cluster to fire them.
	withdrawFabric func(ctx context.Context, compressedID [8]byte, nodeID uint64)
}

// startMatterBridge constructs and starts the Matter bridge when
// matter.enabled is set. Returns the bridge and a stop function the caller
// defers; both are nil when the feature flag is off or the bridge cannot
// stand up. Errors are logged at warn level but never abort the daemon —
// the bridge is feature-flagged and failing to start it must not take
// REST / MQTT down with it.
//
// Defaults applied here mirror the [config.NorthMatter] doc strings:
// VendorID 0xFFF1 (test vendor block — never ship), ProductID 0x8000,
// NodeLabel "openccu-loom", Discriminator 0xF00, Listen ":5540".
//
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; startMatterBridge never opens or
// closes it — a nil db degrades the bridge to disabled (same as a
// disabled cfg.North.Matter.Enabled), and every early-return path below
// leaves the handle untouched for the caller to keep using.
func startMatterBridge(ctx context.Context, cfg *config.Config, reg *central.Registry, db *gosql.DB, healthTracker *health.Tracker, labels device.ParameterTranslator, logger *slog.Logger) *matterBridgeBundle { //nolint:gocognit,gocyclo,funlen // composition/wiring: long sequential setup
	if cfg == nil || !cfg.North.Matter.Enabled {
		return nil
	}
	if db == nil {
		logger.Warn("matter.bridge.db_unavailable", slog.String("hint", "shared openccu-loom.db handle is nil; Matter bridge disabled"))
		return nil
	}

	store := matterstore.New(db)

	// Single defaulting point: the SAME defaulted view feeds the bridge
	// core here and the opener / advertisement / REST setup-payload in
	// wireMatterRuntime + mountRESTRouter. See config.NorthMatter.WithDefaults.
	mc := cfg.North.Matter.WithDefaults()
	if mc.DevRotateUniqueIDs {
		// Dev-mode: rotate every bridged endpoint's UniqueID at boot so
		// pair iteration cycles (chip-tool brief T11, Apple HMHome cache
		// recovery) see a fresh identity each daemon start. Disabled by
		// default — production Apple Home / Google Home accessory
		// recognition expects a stable UniqueID across restarts.
		bootid.EnableRotation()
		logger.Info("matter.bridge.bootid_rotation_enabled",
			slog.String("hint", "per-boot UniqueID salt active — accessory recognition will fail across restarts; toggle north.matter.dev_rotate_unique_ids=false for production"))
	}

	// Latch per-central southbound readiness BEFORE the first assembly
	// (Bridge.Start below) so the snapshotter can stamp
	// [endpoint.Snapshot.ModelComplete]. The boot-time assembly runs before
	// the readiness-gated CCU device load, so every registered central
	// briefly presents an empty ModelRegistry; without the latch the
	// assembler's vanished-source GC would read that as "all devices
	// removed" and wipe the persisted endpoint-ID rows on every boot,
	// renumbering the bridged fleet for paired controllers.
	readiness, unwireReadiness := wireMatterCentralReadiness(reg)
	snap := matterSnapshotter(reg, readiness)

	advertiser, closeAdvertiser := buildMatterAdvertiser(mc, logger) //nolint:contextcheck // buildMatterAdvertiser has no ctx; the subtype responder runs for the advertiser's lifetime and is released through closeAdvertiser
	// The Matter subtree resolves no translation catalogues of its own, so
	// the channel word is translated here and handed down finished. A
	// catalogue that fails to load answers a lookup with the key itself,
	// which would surface as "channel.title" in a NodeLabel, so fall back
	// to the English word the assembler used before.
	channelLabel := "Channel"
	if catalogs, cErr := i18n.NewCatalogs(); cErr == nil {
		if w := catalogs.T(cfg.Locale, "channel.title"); w != "" && w != "channel.title" {
			channelLabel = w
		}
	}
	bridge, err := matterbridge.New(store, snap, advertiser, matterbridge.Config{
		Listen:                  mc.Listen,
		PreferIPv4:              mc.PreferIPv4,
		VendorID:                mc.VendorID,
		ProductID:               mc.ProductID,
		NodeLabel:               mc.NodeLabel,
		Discriminator:           mc.Discriminator,
		ExposeSecondaryChannels: mc.ExposeSecondaryChannels,
		IncludeMeasurements:     mc.IncludeMeasurements,
		Labels:                  labels,
		ChannelLabel:            channelLabel,
	}, logger)
	if err != nil {
		logger.Warn("matter.bridge.new", slog.String("err", err.Error()))
		unwireReadiness()
		closeAdvertiser()
		return nil
	}

	// Enforce the AccessControl cluster's ACL on operational (CASE) IM
	// requests (Matter §9.10). Wired before Start so the first assembled
	// dispatcher already gates reads / writes / invokes.
	bridge.AttachACLLister(store)

	// The pairing trace has to be attached before the bridge starts serving:
	// the moments worth recording begin with the first commissioner that
	// reaches it, and the receive path reads the ring without the bridge lock
	// — attaching after Start would both lose those moments and race the
	// serve goroutine.
	bridge.AttachDiagnosticEvents(diagevent.NewRing(matterDiagEventCapacity))

	if err := bridge.Start(ctx); err != nil {
		logger.Warn("matter.bridge.start", slog.String("err", err.Error()))
		unwireReadiness()
		closeAdvertiser()
		return nil
	}
	// mDNS Re-Announce-Loop: grandcat/zeroconf only Probe+Announce on
	// initial Register; commissioners that miss the announce window
	// (transient WiFi loss, late join, mDNS cache eviction past TTL)
	// never see the bridge again. Periodic re-publish at 30 min keeps
	// the records on the wire (Apple's mDNSResponder caches at TTL=4500
	// = 75 min; re-emit at less than half-TTL).
	var stopReannounce func()
	if z, ok := advertiser.(*mdns.Zeroconf); ok {
		stopReannounce, _ = z.StartReannounceLoop(ctx, 30*time.Minute)
		logger.Info("matter.bridge.mdns.reannounce_loop_started",
			slog.String("interval", "30m"))
	}
	logger.Info("matter.bridge.up",
		slog.String("addr", bridge.LocalAddr()),
		slog.Bool("ipv4_only", mc.PreferIPv4))

	// Wire the bridge into the health tracker. The probe reports the
	// `matter` component alongside `mqtt` and `sqlite` so the
	// Diagnostics surface shows every long-running subsystem on one
	// row each. Best-effort: a nil tracker disables the probe.
	if healthTracker != nil {
		_ = matterbridge.StartHealthProbe(ctx, bridge, matterHealthRecorder{healthTracker}, matterbridge.DefaultProbeInterval)
		// Controller round-trip: how long a paired Apple/Google hub takes to
		// ACK a reliable message this bridge sent. It separates a controller
		// that has gone unreachable from one that is merely slow — both look
		// like "subscriptions alive" everywhere else.
		//
		// The companion gauge is the cumulative count rather than the window
		// occupancy, for the reason spelled out at the MQTT pair in
		// daemon_infra.go: occupancy saturates and then cannot tell a live
		// median from one frozen at the last successful exchange. A total of
		// zero is the correct steady state of a bridge nobody has paired.
		mb := bridge
		healthTracker.RegisterGauge("matter.controller_rtt_ms",
			func() float64 { return mb.ControllerRTT().MedianMs })
		healthTracker.RegisterGauge("matter.controller_rtt_max_ms",
			func() float64 { return mb.ControllerRTT().MaxMs })
		healthTracker.RegisterGauge("matter.controller_rtt_total",
			func() float64 { return float64(mb.ControllerRTT().Total) })
	}

	// Boot-time fabric announce: if a fabric is already persisted
	// (commissioning happened in a previous run), publish the
	// operational mDNS record under that identity so commissioners
	// can resolve us via DNS-SD without having to re-pair.
	announcePersistedFabric(ctx, store, bridge, logger)

	// caseIdentityHolder is the live, swap-able operational identity
	// the per-exchange CASE provider seeds every fresh responder
	// from. AddNOC's OnFabricInstalled hook (registered inside
	// buildRootClusters) calls caseRefresh below to rebuild the
	// identity from the freshly-persisted store row; subsequent
	// Sigma1 arrivals from any peer (iPhone over IPv6, HomePod over
	// IPv4) get a fresh CaseAdapter wrapping a fresh sigma.Responder
	// seeded from this identity. A singleton CaseAdapter would land
	// in `Finished` after the first Sigma3 and reject every
	// subsequent Sigma1 with `ProcessSigma1 already called`.
	// Multi-fabric case identity registry. Apple Home's Multi-Admin pair
	// installs the primary Hub fabric AND the iCloud system commissioner
	// fabric within seconds; afterwards the iPhone reconnects via fabric
	// #1 while the iCloud Hub uses fabric #2 — both targeting the same
	// bridge over a shared UDP listener. A singleton holder that the
	// LAST `OnFabricInstalled` hook overwrites would force every fresh
	// Sigma1 onto the latest fabric's NOC; the iPhone's Sigma1 (which
	// targets fabric #1's destinationId) would then receive a Sigma2
	// signed under fabric #2's NOC and reject the exchange with
	// StatusReport(SecureChannel, INVALID_PARAMETER) — observed in
	// production logs as "MTRDevice getSessionForNode error 172" and
	// surfaces in Home as an unsupported-device error. The registry holds every
	// installed identity so [sigma.IdentityResolver] can pick the right
	// one per inbound Sigma1.DestinationID. Type definition is package-
	// level so the [caseDestinationResolver] (also package-level) can
	// share the same shape without an interface indirection.
	var (
		caseIdentityMu  sync.RWMutex
		caseFabrics     = make(map[uint8]*caseFabricEntry)
		caseIdentity    *sigma.Identity // last-installed; baseline + pre-AddNOC fallback
		caseVerifier    sigma.PeerVerifier
		caseFabricIndex uint8
	)
	caseRefresh := func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, _ []byte) {
		if store == nil {
			return
		}
		fabric, ferr := store.GetFabric(ctx, fabricIndex)
		if ferr != nil {
			logger.Warn("matter.bridge.case.refresh_get_fabric",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", ferr.Error()))
			return
		}
		id, ierr := store.GetIdentity(ctx, fabricIndex)
		if ierr != nil {
			logger.Warn("matter.bridge.case.refresh_get_identity",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", ierr.Error()))
			return
		}
		priv, perr := privKeyFromScalar(id.PrivateKey)
		if perr != nil {
			logger.Warn("matter.bridge.case.refresh_priv",
				slog.String("err", perr.Error()))
			return
		}
		ver, verr := mattercert.NewVerifier(fabric.RootPublicKey, mattercert.SystemTime{})
		if verr != nil {
			logger.Warn("matter.bridge.case.refresh_verifier",
				slog.String("err", verr.Error()))
			return
		}
		ipkCandidates, ipkErr := ipkOperationalCandidates(ctx, store, fabricIndex, fabric.CompressedID, id.IPK)
		if ipkErr != nil {
			logger.Warn("matter.bridge.case.refresh_ipk",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", ipkErr.Error()))
			return
		}
		newID := &sigma.Identity{
			NOC:                id.NOC,
			ICAC:               id.ICAC,
			PrivateKey:         priv,
			NodeID:             fabric.NodeID,
			FabricID:           fabric.FabricID,
			CompressedFabricID: fabric.CompressedID,
			IPK:                ipkCandidates[0],
			FabricIndex:        fabricIndex,
		}
		caseIdentityMu.Lock()
		caseFabrics[fabricIndex] = &caseFabricEntry{
			identity:      newID,
			verifier:      ver,
			rootPublicKey: append([]byte(nil), fabric.RootPublicKey...),
			fabricIndex:   fabricIndex,
			ipkCandidates: ipkCandidates,
		}
		// Keep `caseIdentity` as the most-recently-installed identity so
		// fresh responders still have a sane default before the resolver
		// gets a chance to pick (e.g. malformed Sigma1 that fails to
		// parse the DestinationID).
		caseIdentity = newID
		caseVerifier = ver
		caseFabricIndex = fabricIndex
		caseIdentityMu.Unlock()
		logger.Info("matter.bridge.case.identity_swapped",
			slog.Int("fabric_index", int(fabricIndex)),
			slog.Uint64("fabric_id", fabricID),
			slog.Uint64("node_id", nodeID),
			slog.Int("noc_bytes", len(id.NOC)),
			slog.Int("icac_bytes", len(id.ICAC)),
			slog.Int("total_fabrics", len(caseFabrics)))
	}

	// Sigma1 destination resolver — picks the per-fabric identity whose
	// computed destinationID matches the inbound Sigma1 envelope.
	// Mirrors matter.js FabricManager.findFabricFromDestinationId.
	caseResolver := caseDestinationResolver{
		mu:      &caseIdentityMu,
		fabrics: &caseFabrics,
		logger:  logger,
	}

	// Construct + attach the root-endpoint cluster servers
	// (BasicInformation, GeneralCommissioning, …). Without these
	// chip-tool's ReadCommissioningInfo gets `UnsupportedCluster`
	// (0xC3) for every read and aborts before we can install a fabric.
	// Bind opMgr into the AdoptFabric closure via a deferred-slot
	// pattern: opMgr is constructed below (after buildRootClusters) but
	// the closure only fires when AddNOC is invoked — long after daemon
	// startup completes — so the late assignment is safe. After AddNOC
	// the PASE session's FabricIndex must be rewritten to the new fabric,
	// otherwise the follow-up CommissioningComplete lands on
	// FabricIndex=0 (PASE) and Apple aborts the pair.
	var opMgrSlot *operational.Manager
	adoptSessionForFabric := func(ctx context.Context, fabricIndex uint8) {
		sessionID := mattercore.InvokeSessionIDFromContext(ctx)
		if sessionID == 0 || opMgrSlot == nil {
			return
		}
		if err := opMgrSlot.AdoptFabricIndex(sessionID, fabricIndex); err != nil {
			logger.Debug("matter.bridge.session.adopt_fabric",
				slog.Int("session_id", int(sessionID)),
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", err.Error()))
		} else {
			logger.Debug("matter.bridge.session.adopt_fabric_ok",
				slog.Int("session_id", int(sessionID)),
				slog.Int("fabric_index", int(fabricIndex)))
		}
	}
	// closePaseSessions late-binds opMgr like adoptSessionForFabric —
	// CommissioningComplete / fail-safe-expiry hooks (wired inside
	// buildRootClusters, before opMgr exists) call it to clear any
	// still-established PASE session per Matter §11.10.6.6 step 4.
	closePaseSessions := func() int {
		if opMgrSlot == nil {
			return 0
		}
		return opMgrSlot.ClosePASESessions()
	}
	// Seed the EventNumber counter from the persisted ceiling and wire
	// the ceiling persistence BEFORE any event (StartUp / BootReason)
	// is emitted. Matter §7.14.2.1: event numbers SHALL NOT reset on
	// reboot — controllers filter reads/subscribes with EventMin keyed
	// on the last number they saw, so a reset makes them drop every
	// fresh event silently.
	if store != nil {
		if ceiling, ok, gerr := store.GetMetadataCounter(ctx, matterstore.MetadataKeyEventNumber); gerr == nil && ok {
			bridge.EventLog().SeedNumber(ceiling)
		}
		bridge.EventLog().SetCounterPersistence(func(ceiling uint64) { //nolint:contextcheck // fires from the event-emit hot path with no caller ctx; a background ctx is correct for the fire-and-forget ceiling write
			if perr := store.SetMetadataCounter(context.Background(), matterstore.MetadataKeyEventNumber, ceiling); perr != nil {
				logger.Warn("matter.bridge.eventlog.persist_ceiling", slog.String("err", perr.Error()))
			}
		}, 0)
	}
	rootServers, opCreds, rootRefs, err := buildRootClusters(ctx, mc, store, bridge, advertiser, logger, caseRefresh, adoptSessionForFabric, closePaseSessions)
	if err != nil {
		logger.Warn("matter.bridge.root_clusters.build", slog.String("err", err.Error()))
	} else {
		bridge.AttachRootClusters(rootServers)
		logger.Info("matter.bridge.root_clusters.attached",
			slog.Int("count", len(rootServers)))
	}

	// Build + attach the Aggregator endpoint (EP 1) cluster servers.
	// Bug C topology fix: matter.js's bridge pattern always places the
	// Aggregator on a dedicated endpoint with its own Descriptor whose
	// PartsList enumerates the bridged children. Apple Home's HAP
	// service mapper walks RootNode.PartsList → Aggregator →
	// Aggregator.Descriptor.PartsList to find bridged devices; without
	// this layer Apple Home renders the bridge as empty.
	aggregatorServers, aerr := buildAggregatorClusters()
	if aerr != nil {
		logger.Warn("matter.bridge.aggregator_clusters.build", slog.String("err", aerr.Error()))
	} else {
		bridge.AttachAggregatorClusters(aggregatorServers)
		logger.Info("matter.bridge.aggregator_clusters.attached",
			slog.Int("count", len(aggregatorServers)))
	}

	// Seed GeneralDiagnostics with persisted counters: load the row,
	// bump RebootCount, persist back so the next boot starts at the
	// new value, then hand the seed to the cluster. The cluster adds
	// the live process uptime to BaseOperationalHours on every read.
	// The shutdown closure (further down) writes the final value.
	if rootRefs.GeneralDiagnostics != nil && store != nil {
		seedCtx, seedCancel := context.WithTimeout(ctx, 2*time.Second)
		if rec, lerr := store.LoadDiagnostics(seedCtx); lerr == nil {
			rec.RebootCount++
			if serr := store.SaveDiagnostics(seedCtx, rec); serr != nil {
				logger.Warn("matter.bridge.diagnostics.save_seed", slog.String("err", serr.Error()))
			}
			rootRefs.GeneralDiagnostics.SetPersistedCounters(rec.RebootCount, rec.BaseOperationalHours)
			logger.Info("matter.bridge.diagnostics.seeded",
				slog.Int("reboot_count", int(rec.RebootCount)),
				slog.Int("base_op_hours", int(rec.BaseOperationalHours)))
		} else {
			logger.Warn("matter.bridge.diagnostics.load", slog.String("err", lerr.Error()))
		}
		seedCancel()
	}

	// --- Adapter wiring ----------------------------------------
	// Every Matter bridge lifecycle starts with the operational
	// session manager + MRP ack tracker — both feed the receive
	// pipeline regardless of whether commissioning is active. PASE
	// is conditional on a configured passcode; CASE wiring is
	// deferred until fabric-identity persistence is plumbed.
	opMgr := operational.NewManager(store)
	opMgrSlot = opMgr // late-bind into the AdoptFabric closure above
	sessionLookup := matterbridge.NewOperationalSessionLookup(
		func(id uint16) (*channel.Session, bool) {
			entry, err := opMgr.Get(id)
			if err != nil || entry == nil {
				return nil, false
			}
			return entry.Session, true
		},
	).WithFabricResolver(func(id uint16) (uint8, bool) {
		entry, err := opMgr.Get(id)
		if err != nil || entry == nil {
			return 0, false
		}
		return entry.FabricIndex(), true
	}).WithSubjectResolver(func(id uint16) (uint64, []uint32, bool) {
		// Resolves (peerNodeID, peerCATs) for the IM dispatcher's
		// per-subject ACL gate (Matter §9.10.5.6). Apple Home Multi-
		// Admin pairs install per-resident ACEs keyed on the resident's
		// operational NodeID; without this the ACEs collapse onto the
		// fabric-wide wildcard.
		entry, err := opMgr.Get(id)
		if err != nil || entry == nil || entry.Session == nil {
			return 0, nil, false
		}
		return entry.Session.PeerNodeID(), entry.Session.PeerCATs(), true
	}).WithRetransmitIntervalResolver(func(id uint16, now time.Time) (time.Duration, bool) {
		// Resolves the peer-appropriate MRP base interval so outbound
		// retransmissions honour the peer's advertised session
		// parameters (matter.js MRP.ts:129 retransmissionIntervalOf).
		entry, err := opMgr.Get(id)
		if err != nil || entry == nil {
			return 0, false
		}
		return entry.RetransmitBaseInterval(now), true
	}).WithActivityMarkers(
		// Session activity feeds the idle reaper (StartReaper below) and
		// the MRP peer-active determination. Mirrors matter.js
		// packages/protocol/src/session/Session.ts:127 notifyActivity —
		// invoked by the bridge's receive path for every authenticated
		// inbound message and by its outbound seal path for every secure
		// send (MessageExchange.ts:429 / :562).
		func(id uint16) {
			if entry, err := opMgr.Get(id); err == nil && entry != nil {
				entry.MarkActiveRx()
			}
		},
		func(id uint16) {
			if entry, err := opMgr.Get(id); err == nil && entry != nil {
				entry.MarkActiveTx()
			}
		},
	)
	bridge.AttachSessionLookup(sessionLookup)

	ackTracker := mrp.NewAckTracker(mrp.DefaultStandaloneAckDelay)
	bridge.AttachAckTracker(ackTracker) // wires both Discharge handler + Owe-pump goroutine

	// Subscription manager: tracks active subscriptions + enforces
	// per-fabric quota and cadence floor/ceiling. The Reporter
	// re-reads the dirty paths through the bridge's IM dispatcher
	// and ships a fresh ReportData via the per-subscription src
	// captured in [Bridge.captureSubTarget]. Best-effort delivery
	// (no MRP retransmit on ongoing reports) — controllers tolerate
	// the occasional drop; the next dirty tick re-emits.
	subMgr := subscription.NewManager(subscription.Config{}, bridge.SubscriptionReporter(), logger)
	subMgr.Start(ctx)
	bridge.AttachSubscriptionManager(subMgr)

	// Wire the secure-session manager so it cascades subscription
	// cleanup whenever it tears down a session. Without this hook,
	// stale subscriptions tied to a closed CASE session linger in the
	// IM layer — Apple Home accumulates one ghost subscription per
	// reconnect, the engine then ships duplicate ReportData to a peer
	// whose decryption keys are already gone, and Apple deprioritizes
	// the burst as redundant noise. Mirrors matter.js
	// packages/protocol/src/session/SessionManager.ts — close
	// callbacks chain into SubscriptionHandler cleanup.
	opMgr.SetOnSessionClose(subMgr.CloseSession)

	// Wire the session manager into the Secure-Channel path so the
	// bridge (a) closes sessions on inbound CloseSession StatusReports,
	// (b) ships a best-effort outbound CloseSession StatusReport before
	// zeroising keys on reap/eviction/shutdown, and (c) resumes mDNS
	// broadcast once a peer's last session is gone. Mirrors matter.js
	// packages/protocol/src/protocol/ExchangeManager.ts #sendCloseSession
	// and SecureChannelProtocol.ts inbound-close handling.
	bridge.AttachSessionRegistry(opMgr)

	// Idle-session reaper: evict operational sessions with no traffic for
	// [operational.SessionIdleTimeout] (5 min), sweeping every
	// [matterSessionReapInterval]. The TTL must stay comfortably above the
	// subscription publisher's heartbeat cadence, which the IM subscription
	// package caps at two minutes for exactly this reason — so a
	// live but quiet subscription is never reaped: controllers ack every
	// heartbeat report, and each ack refreshes the session's Rx activity
	// through the receive path (WithActivityMarkers above). Reaped sessions
	// ship the same graceful CloseSession farewell as shutdown.
	opMgr.StartReaper(ctx, operational.SessionIdleTimeout, matterSessionReapInterval)

	// Initial value warm-up REMOVED. A previous per-device LoadAllValues
	// sweep ran here so Apple Home's HAP-mapper would see observed
	// values before the first Subscribe-Initial-Report. On a non-trivial
	// CCU (124 devices × multiple readable DPs each) the sweep fanned
	// out hundreds of `getValue` radio calls within a second or two and
	// drove the CCU DutyCycle into the warning band on every restart.
	// The persistent VALUES cache (ADR 0019) + push events already
	// hydrate the model for the vast majority of accessories; Apple
	// renders the remaining DPs as `unavailable` until the next wire
	// event populates them — the same trade-off MQTT / REST already
	// take. The reference design takes the same trade-off and does not
	// pre-load every DP on boot either.
	// Wire the symmetrical event-drain hook so cluster-emitted events
	// (e.g. GenericSwitch InitialPress / LongPress) ride the same
	// per-subscription [subTarget] machinery as attribute reports.
	subMgr.SetEventReporter(bridge.SubscriptionEventReporter())

	// Wire fabric-removed cleanup AFTER both managers exist. Without
	// this Apple's RemoveFabric (sent at the end of every aborted pair
	// chain) leaves the operational session and ongoing subscription
	// alive — the next pair retry collides on session-id 1 with
	// `aesccm: authentication failed` and Apple gives up.
	fabricTeardown := matterFabricTeardown(bridge, rootRefs.BasicInformation, rootRefs.AccessControl, opMgr, subMgr, store, logger)
	if opCreds != nil {
		opCreds.SetOnFabricRemoved(func(_ context.Context, fabricIndex uint8) {
			fabricTeardown(context.Background(), fabricIndex) //nolint:contextcheck // the teardown outlives the invoking exchange by design; see matterFabricTeardown
		})

		// Wire fabric-updated cleanup. After UpdateNOC installs a new
		// NOC for an existing fabric, every OTHER CASE session bound to
		// that fabric is cryptographically stale — the commissioner must
		// re-CASE with the new NOC. OperationalCredentials fires this hook
		// (its AbortAllOtherCommunicationOnFabric mirror after UpdateNOC)
		// but it stays inert unless wired here. Without it the stale CASE
		// sessions linger and the commissioner's first post-UpdateNOC
		// message decrypts against the wrong identity.
		// Mirrors chip FabricTable::AbortAllOtherCommunicationOnFabric.
		opCreds.SetOnFabricUpdated(func(ctx context.Context, fabricIndex uint8) {
			// Preserve the session that invoked UpdateNOC: it still has to
			// carry the NOCResponse, and the commissioner re-CASEs on it.
			// A plain CloseFabric would also delete the invoking session, so
			// its own response could not be sent and the commissioner would
			// time out. Mirrors chip AbortAllOtherCommunicationOnFabric,
			// which pins the invoking exchange's session.
			keepSession := mattercore.InvokeSessionIDFromContext(ctx)
			opMgr.CloseFabricExcept(fabricIndex, keepSession)
			subMgr.CloseFabricExcept(fabricIndex, keepSession)
			if store != nil {
				// Resumption records embed the old session's crypto +
				// identity; the spec (§11.18.6.9) and matter.js
				// FailsafeContext.ts:168-171 (replaceFabric →
				// deleteResumptionRecordsForFabric) invalidate them on
				// UpdateNOC so no peer can resume into the pre-update
				// identity.
				if err := store.RemoveResumptionsByFabric(context.Background(), fabricIndex); err != nil { //nolint:contextcheck // callback ctx may carry the invoking exchange's deadline; cleanup must complete regardless
					logger.Debug("matter.bridge.fabric.update_remove_resumptions",
						slog.Int("fabric_index", int(fabricIndex)),
						slog.String("err", err.Error()))
				}
				// Swap the CASE identity for this fabric to the freshly
				// persisted NOC (new NodeID included) and announce the
				// new operational instance. The stale instance was
				// already withdrawn by the cluster's OnFabricWithdraw.
				// Mirrors matter.js DeviceAdvertiser.ts:65-76 (fabric
				// update → close old advertisement, re-advertise).
				if fab, ferr := store.GetFabric(ctx, fabricIndex); ferr == nil {
					caseRefresh(ctx, fabricIndex, fab.FabricID, fab.NodeID, fab.RootPublicKey)
					bridge.AnnounceFabric(ctx, fab.CompressedID, fab.NodeID)
				}
			}
			logger.Info("matter.bridge.fabric.updated",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.Int("kept_session", int(keepSession)))
		})
	}

	var configuredPase *matterbridge.PaseAdapter
	if mc.Commissioning.Passcode != 0 {
		if mc.Commissioning.ConcurrentPairings {
			provider := matterbridge.NewPerExchangePaseProvider(func() *matterbridge.PaseAdapter { //nolint:contextcheck // factory signature is fixed; buildPaseAdapter has no ctx parameter
				a, err := buildPaseAdapter(mc.Commissioning, opMgr, opCreds, rootRefs.GeneralCommissioning, logger)
				if err != nil {
					logger.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
					return nil
				}
				return a
			})
			// Reap stale per-exchange adapters every 30s with a 60s
			// TTL so a daemon serving thousands of distinct
			// commissioners doesn't grow the entries map unboundedly.
			// PASE exchanges normally finish < 5s; 60s comfortably
			// covers chip-tool retransmits.
			provider.StartReaper(ctx, 30*time.Second, 60*time.Second)
			bridge.AttachPaseHandlerProvider(provider.Resolve)
			logger.Info("matter.bridge.pase.armed",
				slog.Int("iterations", mc.Commissioning.Iterations),
				slog.String("mode", "per-exchange"))
		} else {
			paseAdapter, err := buildPaseAdapter(mc.Commissioning, opMgr, opCreds, rootRefs.GeneralCommissioning, logger) //nolint:contextcheck // buildPaseAdapter/buildPaseAdapterFromCreds have no ctx parameter
			if err != nil {
				logger.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
			} else {
				bridge.AttachPaseHandler(paseAdapter)
				configuredPase = paseAdapter
				logger.Info("matter.bridge.pase.armed",
					slog.Int("iterations", mc.Commissioning.Iterations),
					slog.String("mode", "singleton"))
			}
		}
	}

	// CASE provider is wired unconditionally when the Matter bridge is
	// up: Sigma1 has to be answered by any daemon that ever wants to
	// serve operational reads, and the only thing the commissioner does
	// before sending Sigma1 is run AddNOC over PASE — at which point
	// `caseRefresh` swaps the freshly persisted identity into the
	// provider's factory. Gating this block on `mc.CASE.NodeID != 0`
	// (the pre-2026-05 behaviour) silently broke commissioning: PASE
	// completed, fabric installed, then every Sigma1 dropped with
	// `ErrCaseHandlerMissing` at DEBUG level and the commissioner timed
	// out. mc.CASE.NodeID / FabricID survive as soft preferences for
	// `loadPersistentCaseIdentity` (multi-fabric pin) and as the
	// fallback identity stamp on the pre-AddNOC ephemeral stub adapter.
	{
		// Seed caseIdentity / caseVerifier from a persisted fabric (if
		// any). caseRefresh updates them on every AddNOC; the
		// per-exchange provider's factory below reads them under the
		// lock when allocating each fresh adapter.
		seedID, seedVer, seedIdx, persisted, err := loadPersistentCaseIdentity(ctx, mc.CASE, store, logger)
		if err != nil {
			logger.Warn("matter.bridge.case.load_identity", slog.String("err", err.Error()))
		}
		if persisted {
			caseIdentityMu.Lock()
			caseIdentity = seedID
			caseVerifier = seedVer
			caseFabricIndex = seedIdx
			// Also stamp the seed into the multi-fabric registry so the
			// destination resolver can match it post-reboot before any
			// AddNOC fires.
			if rootPub, rpErr := loadFabricRootPublicKey(ctx, store, seedIdx); rpErr == nil {
				// Re-derive the IPK rotation candidate set (Matter
				// §11.2.10.6) for the seed fabric so a reboot doesn't
				// lose a KeySetWrite-rotated IPK; falls back to the
				// identity's already-derived IPK when the raw record
				// can't be re-read.
				ipkCandidates := [][16]byte{seedID.IPK}
				if id, idErr := store.GetIdentity(ctx, seedIdx); idErr == nil {
					if cands, candErr := ipkOperationalCandidates(ctx, store, seedIdx, seedID.CompressedFabricID, id.IPK); candErr == nil {
						ipkCandidates = cands
					} else {
						logger.Warn("matter.bridge.case.persistent_ipk_candidates",
							slog.Int("fabric_index", int(seedIdx)),
							slog.String("err", candErr.Error()))
					}
				}
				caseFabrics[seedIdx] = &caseFabricEntry{
					identity:      seedID,
					verifier:      seedVer,
					rootPublicKey: rootPub,
					fabricIndex:   seedIdx,
					ipkCandidates: ipkCandidates,
				}
			} else {
				logger.Warn("matter.bridge.case.persistent_rootpub",
					slog.Int("fabric_index", int(seedIdx)),
					slog.String("err", rpErr.Error()))
			}
			caseIdentityMu.Unlock()
			logger.Info("matter.bridge.case.persistent_identity",
				slog.Int("fabric_index", int(seedIdx)),
				slog.Uint64("node_id", seedID.NodeID),
				slog.Uint64("fabric_id", seedID.FabricID))

			// Multi-Fabric loading: until now only the lowest-index ("seed")
			// fabric was rehydrated on boot. Apple's Multi-Admin pair
			// flow installs two fabrics — iPhone (Apple Home) +
			// HomePod/Apple TV (Apple Home Hub). After a daemon restart
			// the second fabric's CASE-reconnect would land Sigma1
			// against an identity-less branch in the resolver and Apple's
			// MTRDevice for that fabric went `MTRDeviceStateUnreachable`.
			// Load every persisted fabric into `caseFabrics` so the
			// resolver has identities for both controllers from the
			// first packet onwards.
			rehydratedExtra := loadAdditionalFabricsForCase(ctx, store, seedIdx, caseFabrics, &caseIdentityMu, logger)
			if rehydratedExtra > 0 {
				logger.Info("matter.bridge.case.persistent_identities_extra",
					slog.Int("seed_fabric_index", int(seedIdx)),
					slog.Int("additional_fabrics_loaded", rehydratedExtra))
			}
		} else {
			logger.Info("matter.bridge.case.awaiting_addnoc",
				slog.String("hint", "no persisted fabric yet; first Sigma1 after AddNOC will use the fabric-driven identity"))
		}

		caseProvider := matterbridge.NewPerExchangeCaseProvider(func() *matterbridge.CaseAdapter { //nolint:contextcheck // factory signature is fixed; SetOnSessionEstablished callback has no ctx parameter
			caseIdentityMu.RLock()
			id := caseIdentity
			ver := caseVerifier
			fIdx := caseFabricIndex
			caseIdentityMu.RUnlock()
			if id == nil {
				// Pre-AddNOC traffic: build a stub adapter on an
				// ephemeral key so Sigma3 fails cleanly instead of
				// panicking. Real CASE handshakes need the persisted
				// fabric to land first.
				ephem, ephemErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if ephemErr != nil {
					logger.Warn("matter.bridge.case.factory_ephemeral",
						slog.String("err", ephemErr.Error()))
					return nil
				}
				id = &sigma.Identity{PrivateKey: ephem, NodeID: mc.CASE.NodeID, FabricID: mc.CASE.FabricID}
				ver = trustAnyPeerVerifier{}
			}
			// Pre-allocate the operational session id BEFORE the
			// responder is constructed: Sigma2.responderSessionID is
			// what the commissioner echoes back as `dest.SessionID`
			// on every operational packet, and our session lookup is
			// keyed on that value. A hard-coded "1" makes every
			// parallel CASE session collide on the same lookup slot —
			// the iPhone's reads and the HomePod's reads end up in
			// the same bucket and only one of them wins.
			sessID, allocErr := opMgr.AllocateID()
			if allocErr != nil {
				logger.Warn("matter.bridge.case.factory_alloc",
					slog.String("err", allocErr.Error()))
				return nil
			}
			responder := sigma.NewResponder(id, ver, sessID)
			// One CaseAdapter serves every handshake that arrives on its
			// exchange, and Apple Home grafts a second CASE session (the
			// iCloud Hub Companion fabric) onto the exchange it already
			// used. Without a fresh id per handshake the second session
			// would be registered in the slot the first peer's session
			// occupies. matter.js takes the id inside each Sigma1
			// handling instead
			// (packages/protocol/src/session/case/CaseServer.ts:266).
			responder.SetSessionIDRenewer(func(previous uint16) (uint16, bool) {
				next, renewErr := opMgr.AllocateID()
				if renewErr != nil {
					logger.Warn("matter.bridge.case.session_id_renew",
						slog.Int("previous_session_id", int(previous)),
						slog.String("err", renewErr.Error()))
					return 0, false
				}
				// Allocate before releasing so the new handshake can
				// never be handed the id it is replacing. ReleaseID only
				// drops placeholders, so a previous id that already
				// carries an established session stays untouched.
				opMgr.ReleaseID(previous)
				return next, true
			})
			// Wire the multi-fabric destination resolver so the inbound
			// Sigma1's DestinationID picks the correct identity at
			// Sigma2-sign time — Bug G fix for Apple Multi-Admin pairing.
			responder.SetIdentityResolver(caseResolver)
			// Emit responderSessionParams (Sigma2 tag 5) so the commissioner
			// learns the bridge's preferred MRP intervals post-CASE — matches
			// the operational mDNS SII/SAI/SAT defaults. Mirrors chip
			// CASESession.cpp:1326 EncodeSessionParameters.
			responder.SetSessionParameters(bridgeSessionParameters())
			// Wire the resumption-record lookup so a fresh Sigma1 that
			// carries a known ResumptionID can fast-path through
			// Sigma2_Resume instead of the full handshake. Mirrors
			// matter.js CaseServer.ts and chip CASESession.cpp resumption
			// branch.
			responder.SetResumptionStore(caseResumptionStoreAdapter{mgr: opMgr})
			adapter := matterbridge.NewCaseAdapter(responder)
			adapter.SetOnSessionEstablished(func(keys sigma.SessionKeys, peerSessionID uint16) error {
				// fabricIndex / nodeID are read from the responder
				// AFTER ProcessSigma1 so the resolver-chosen identity
				// is reflected — using the factory-time `fIdx` / `id`
				// values would mismatch the actually-signed Sigma2
				// when the destination resolver picked a different
				// fabric. peerSessionID is the Sigma1.InitiatorSessionID
				// — outbound replies stamp it into Header.SessionID.
				// peerNodeID comes from the verified NOC (sigma.Responder
				// lifts it via PeerNodeIDExtractor) and feeds the AES-CCM
				// nonce for inbound packets — Matter §4.5.1.4 builds the
				// nonce from the *source* NodeID, which on the peer's
				// outbound is the peer's own NodeID.
				resolvedFabric := fIdx
				resolvedNode := id.NodeID
				peerNodeID := uint64(0)
				// The session id is read back from the responder rather
				// than taken from the factory-time allocation: a second
				// Sigma1 on this exchange renews it, and registering the
				// session under the id the FIRST handshake announced
				// would displace that peer's live session.
				currentSessID := sessID
				var peerCATs []uint32
				var resumeInfo sigma.ResumeInfo
				if resp := adapter.SnapshotResponder(); resp != nil {
					peerNodeID = resp.PeerNodeID()
					peerCATs = resp.PeerCATs()
					currentSessID = resp.SessionID()
					resumeInfo = resp.ResumeInfo()
					// SessionFabricIndex returns the fabric the resolver
					// landed on for this exchange — see [Responder.SessionFabricIndex].
					if fi, nid, ok := resp.SessionIdentity(); ok {
						resolvedFabric = fi
						resolvedNode = nid
					}
				}
				// What a resume displaces can only be read before the
				// install performs it. Guarded by the level check so a
				// bridge running at info never walks the session table on
				// the handshake path.
				var displacedByResume []uint16
				logResume := resumeInfo.Resumed && logger.Enabled(ctx, slog.LevelDebug)
				if logResume {
					displacedByResume = opMgr.SessionIDsForPeer(resolvedFabric, peerNodeID)
				}
				entry, openErr := opMgr.OpenFromSigmaWithID(currentSessID, resolvedFabric, resolvedNode, peerNodeID, peerSessionID, peerCATs, keys)
				if openErr != nil {
					opMgr.ReleaseID(currentSessID)
					return openErr
				}
				// Carry the initiator's Sigma1 MRP hints onto the session
				// so outbound retransmissions honour the peer's intervals
				// (matter.js MRP.ts:129).
				if resp := adapter.SnapshotResponder(); resp != nil {
					if sp, ok := resp.PeerSessionParameters(); ok {
						entry.SetPeerMRPIntervals(sp.SessionIdleInterval, sp.SessionActiveInterval, uint32(sp.SessionActiveThreshold))
					}
				}
				logger.Info("matter.bridge.case.session_established",
					slog.Int("session_id", int(entry.SessionID)),
					slog.Int("fabric_index", int(resolvedFabric)),
					slog.Uint64("node_id", resolvedNode),
					slog.Uint64("peer_node_id", peerNodeID),
					slog.Int("peer_session_id", int(peerSessionID)))
				if logResume {
					logCaseResume(logger, resumeInfo, resolvedFabric, peerNodeID, displacedByResume, opMgr.Occupancy())
				}
				// Persist ECDH shared secret + resumption ID so
				// Sigma2_Resume can short-circuit the full Sigma1-3
				// handshake on the peer's next reconnect (matter.js
				// CaseServer.ts:210). The accessors return defensive
				// copies; both are nil-safe if the handshake didn't
				// surface the values.
				if resp := adapter.SnapshotResponder(); resp != nil {
					rid := resp.ResumptionID()
					secret := resp.ECDHSharedSecret()
					if len(rid) > 0 && len(secret) > 0 {
						// A fresh context on purpose: the record is what lets
						// the peer's next Sigma1 fast-path, and dropping the
						// write because the daemon context happened to be
						// cancelled mid-handshake would cost that peer a full
						// handshake on every later reconnect.
						if persistErr := opMgr.PersistResumption(context.Background(), resolvedFabric, peerNodeID, rid, secret, peerCATs); persistErr != nil { //nolint:contextcheck // deliberate: see comment above
							logger.Debug("matter.bridge.case.resumption_persist_failed",
								slog.Int("session_id", int(entry.SessionID)),
								slog.String("err", persistErr.Error()))
						}
					}
				}
				return nil
			})
			return adapter
		})
		// Reaper: purge stale per-exchange CASE adapters every 30s
		// with a 60s TTL. CASE handshakes normally finish in well
		// under a second; 60s comfortably covers Apple's MRP
		// retransmits without leaking adapters across the daemon's
		// lifetime.
		// Route the reaper's per-exchange eviction into the bridge's
		// sigma1Replied dedupe map. The Sigma3 success path forgets the
		// entry inline, but an ABORTED CASE handshake (Sigma1 received,
		// Sigma3 never) would otherwise leak one entry permanently. Wiring
		// the evict hook lets the TTL reaper clean those up — without this
		// the dedupe map grows without bound on a daemon that sees many
		// half-completed CASE attempts.
		caseProvider.SetOnEvict(bridge.ForgetSigma1Replied)
		caseProvider.StartReaper(ctx, 30*time.Second, 60*time.Second)
		bridge.AttachCaseHandlerProvider(caseProvider.Resolve)
		logger.Info("matter.bridge.case.armed",
			slog.Uint64("node_id", mc.CASE.NodeID),
			slog.Uint64("fabric_id", mc.CASE.FabricID),
			slog.String("mode", "per-exchange"))
	}

	stop := func() { //nolint:contextcheck // shutdown path must not inherit the (potentially cancelled) daemon ctx
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		unwireReadiness()
		if stopReannounce != nil {
			stopReannounce()
		}
		// Persist the diagnostics counters before tearing down the
		// store. RebootCount was already bumped at boot; here we add
		// the just-elapsed process uptime to BaseOperationalHours so
		// the next boot's TotalOperationalHours read picks up where we
		// left off.
		if rootRefs.GeneralDiagnostics != nil && store != nil {
			if rec, lerr := store.LoadDiagnostics(stopCtx); lerr == nil {
				delta := uint32(rootRefs.GeneralDiagnostics.UpTimeSeconds()/3600) & 0xFFFFFFFF
				rec.BaseOperationalHours += delta
				if serr := store.SaveDiagnostics(stopCtx, rec); serr != nil {
					logger.Warn("matter.bridge.diagnostics.save_shutdown", slog.String("err", serr.Error()))
				}
			}
		}
		if err := bridge.Stop(stopCtx); err != nil {
			logger.Warn("matter.bridge.stop", slog.String("err", err.Error()))
		}
		// On this normal shutdown path bridge.Stop has already withdrawn the
		// mDNS records (goodbyes on the wire) and closed the advertiser, and
		// closing the Zeroconf advertiser closes the subtype responder attached
		// to it — so this call is an idempotent second close. It stays because
		// it is the ONLY responder cleanup on the New / Start failure early
		// returns above, where bridge.Stop (and thus the advertiser Close that
		// owns the responder) never runs; running it here too is harmless.
		closeAdvertiser()
		// Stop the subscription engine goroutine. The DB handle itself is
		// shared (owned by the composition root, see openLoomDB) and is
		// closed by its opener, not here.
		subMgr.Stop()
	}
	return &matterBridgeBundle{
		bridge:         bridge,
		stop:           stop,
		store:          store,
		opMgr:          opMgr,
		subMgr:         subMgr,
		advertiser:     advertiser,
		opCreds:        opCreds,
		configuredPase: configuredPase,
		rootRefs:       rootRefs,
		fabricTeardown: fabricTeardown,
		withdrawFabric: func(ctx context.Context, compressedID [8]byte, nodeID uint64) {
			// Retire the removed identity's operational instance, then
			// republish what is left. The wire command reaches both through
			// the cluster's OnFabricWithdraw / OnMDNSReannounce hooks; the
			// operator paths have no cluster invocation to fire them, so an
			// unpaired controller's `_matter._tcp` record would keep being
			// advertised until the next daemon restart.
			bridge.WithdrawFabric(ctx, compressedID, nodeID)
			if z, ok := advertiser.(*mdns.Zeroconf); ok {
				z.TriggerReannounce(ctx)
			}
		},
	}
}

// matterFabricTeardown builds the runtime fan-out every fabric removal owes,
// whichever surface removed it: the OperationalCredentials RemoveFabric wire
// command, the REST revoke and the factory reset all run this one closure.
// Deleting the persisted row alone leaves the removed controller's CASE
// session and its subscription live — keepalive reports keep flowing, the
// session slot stays occupied, and the next pair retry collides on the same
// session id with `aesccm: authentication failed`.
//
// The operational + subscription close is deferred so an in-flight
// RemoveFabric NOCResponse can still ride out on the just-removed CASE
// session before its keys are dropped. matter.js's CaseServer fires the
// equivalent "fabricRemoved" event AFTER the IM reply ships
// (`InteractionMessenger` flushes the reply, then the behaviors pipeline
// closes the session). With a synchronous CloseFabric we would close session
// N inline, the reply encoder would find session N gone, and Apple would see
// a missing NOCResponse and retry until the pair times out. 100ms is enough
// for the synchronous reply path to drain (the IM dispatcher returns
// NOCResponse immediately, the bridge encrypts + sends in < 5ms in practice).
//
// The ctx argument is deliberately unused: the teardown outlives whatever
// invoked it — an exchange whose reply has shipped, or an HTTP request that
// has already answered — so cancelling that caller must not cut the cleanup
// short.
func matterFabricTeardown(
	bridge *matterbridge.Bridge,
	basicInfo *mattercore.BasicInformation,
	accessControl *mattercore.AccessControl,
	opMgr *operational.Manager,
	subMgr *subscription.Manager,
	store *matterstore.Store,
	logger *slog.Logger,
) func(ctx context.Context, fabricIndex uint8) {
	return func(_ context.Context, fabricIndex uint8) {
		if bridge != nil {
			bridge.EmitFabricRemoved(fabricIndex)
		}
		if basicInfo != nil {
			basicInfo.EmitLeave(fabricIndex)
		}
		if accessControl != nil {
			// Purge the removed fabric's in-memory Extension entry
			// (attribute 0x0001) synchronously — a plain map delete, no
			// wire race with the in-flight NOCResponse the deferred
			// session/subscription close below waits out. Without this a
			// fabric index reused by a later commissioning inherits the
			// removed controller's Extension metadata.
			accessControl.RemoveFabricExtension(fabricIndex)
		}
		logger.Info("matter.bridge.fabric.removed",
			slog.Int("fabric_index", int(fabricIndex)))
		go func() { //nolint:contextcheck // delayed teardown goroutine uses a background ctx by design; the caller that triggered the removal has already returned
			time.Sleep(100 * time.Millisecond)
			if opMgr != nil {
				opMgr.CloseFabric(fabricIndex)
			}
			if subMgr != nil {
				subMgr.CloseFabric(fabricIndex)
			}
			// Explicit resumption-record cleanup. SQLite FK CASCADE would
			// also catch this once the matter_fabrics row is deleted, but
			// the store API decouples the two tables — call
			// RemoveResumptionsByFabric directly so the teardown is
			// defense-in-depth and visible in logs. Mirrors chip
			// src/credentials/FabricTable.cpp Delete().
			if store != nil {
				if err := store.RemoveResumptionsByFabric(context.Background(), fabricIndex); err != nil {
					logger.Debug("matter.bridge.fabric.remove_resumptions",
						slog.Int("fabric_index", int(fabricIndex)),
						slog.String("err", err.Error()))
				}
			}
		}()
	}
}

// buildCaseAdapter constructs a sigma.Responder. When a fabric +
// node identity have been persisted (commissioner ran AddNOC +
// AddTrustedRootCertificate against the OperationalCredentials
// cluster) the responder uses the stored NOC + private key + root
// public key — peer-verification runs through [mattercert.Verifier]
// rooted at the stored RootPublicKey. When no fabric is persisted
// (first-boot / pre-commissioning) it falls back to an EPHEMERAL
// development identity with [trustAnyPeerVerifier]; CASE will reject
// every Sigma3 in that mode (the verifier hard-fails) but the path
// stays wired so PASE can still install a fabric and complete the
// flow on the next restart.
//
// `store` is the matter store that persists fabrics + identities.
// Pass nil to force the ephemeral fallback.
func buildCaseAdapter(ctx context.Context, cfg config.NorthMatterCASE, mgr *operational.Manager, store *matterstore.Store, logger *slog.Logger) (*matterbridge.CaseAdapter, error) {
	identity, verifier, fabricIndex, persisted, err := loadPersistentCaseIdentity(ctx, cfg, store, logger)
	if err != nil {
		return nil, err
	}
	if !persisted {
		ephem, ephemErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if ephemErr != nil {
			return nil, fmt.Errorf("ephemeral case key: %w", ephemErr)
		}
		identity = &sigma.Identity{
			PrivateKey: ephem,
			NodeID:     cfg.NodeID,
			FabricID:   cfg.FabricID,
			// NOC + ICAC + CompressedFabricID intentionally empty —
			// commissioner-side cert validation will reject. Acceptable
			// for development-only loop-back test rigs.
		}
		verifier = trustAnyPeerVerifier{}
		logger.Warn("matter.bridge.case.ephemeral_identity",
			slog.String("hint", "no persisted fabric — Sigma3 will reject; commissioner must run AddNOC first"))
	} else {
		logger.Info("matter.bridge.case.persistent_identity",
			slog.Int("fabric_index", int(fabricIndex)),
			slog.Uint64("node_id", identity.NodeID),
			slog.Uint64("fabric_id", identity.FabricID))
	}

	// SessionID 0 is reserved (unsecured), so allocate from 1.
	const initialSessionID uint16 = 1
	responder := sigma.NewResponder(identity, verifier, initialSessionID)
	// Emit responderSessionParams — see bridgeSessionParameters.
	responder.SetSessionParameters(bridgeSessionParameters())
	caseAdapter := matterbridge.NewCaseAdapter(responder)
	nodeID := identity.NodeID
	caseAdapter.SetOnSessionEstablished(func(keys sigma.SessionKeys, _ uint16) error { //nolint:contextcheck // callback signature is fixed; PersistResumption uses context.Background() since no ctx is available in the callback
		// Snapshot the responder so the post-Sigma3 peerNodeID flows into
		// the operational session entry (used as AES-CCM source NodeID for
		// inbound packets per Matter §4.5.1.4) and the resumption persist
		// below has access to the ECDH shared secret + resumption ID.
		// Mirrors the per-exchange CASE provider's onEstablished closure.
		peerNodeID := uint64(0)
		if resp := caseAdapter.SnapshotResponder(); resp != nil {
			peerNodeID = resp.PeerNodeID()
		}
		entry, err := mgr.OpenFromSigma(fabricIndex, nodeID, peerNodeID, keys)
		if err != nil {
			return err
		}
		// Carry the initiator's Sigma1 MRP hints onto the session so
		// outbound retransmissions honour the peer's intervals
		// (matter.js MRP.ts:129).
		if resp := caseAdapter.SnapshotResponder(); resp != nil {
			if sp, ok := resp.PeerSessionParameters(); ok {
				entry.SetPeerMRPIntervals(sp.SessionIdleInterval, sp.SessionActiveInterval, uint32(sp.SessionActiveThreshold))
			}
		}
		logger.Info("matter.bridge.case.session_established",
			slog.Int("session_id", int(entry.SessionID)),
			slog.Uint64("node_id", nodeID),
			slog.Uint64("peer_node_id", peerNodeID))
		// Persist ECDH shared secret + resumption ID so Sigma2_Resume can
		// short-circuit the full Sigma1-3 handshake on the peer's next
		// reconnect (matter.js CaseServer.ts:210). Defensive guards: nil
		// snapshot or empty rid/secret skip the persist.
		if resp := caseAdapter.SnapshotResponder(); resp != nil {
			rid := resp.ResumptionID()
			secret := resp.ECDHSharedSecret()
			if len(rid) > 0 && len(secret) > 0 {
				if persistErr := mgr.PersistResumption(context.Background(), fabricIndex, peerNodeID, rid, secret, resp.PeerCATs()); persistErr != nil {
					logger.Debug("matter.bridge.case.resumption_persist_failed",
						slog.Int("session_id", int(entry.SessionID)),
						slog.String("err", persistErr.Error()))
				}
			}
		}
		return nil
	})
	return caseAdapter, nil
}

// loadPersistentCaseIdentity attempts to construct a sigma.Identity
// + PeerVerifier from the matter store. The strategy:
//
//   - List fabrics; pick the one whose FabricID matches cfg.FabricID
//     (so multi-fabric daemons stay deterministic), else the lowest
//     FabricIndex when cfg.FabricID is 0.
//   - Load the matching IdentityRecord; convert the raw 32-byte
//     P-256 scalar back into an *ecdsa.PrivateKey.
//   - Build a [mattercert.Verifier] rooted at the fabric's
//     RootPublicKey so inbound NOCs are validated against the trust
//     anchor stored alongside the bridge's own identity.
//
// Returns persisted=false when no fabric matches; caller falls back
// to the ephemeral path.
func loadPersistentCaseIdentity(ctx context.Context, cfg config.NorthMatterCASE, store *matterstore.Store, logger *slog.Logger) (identity *sigma.Identity, verifier sigma.PeerVerifier, fabricIndex uint8, persisted bool, err error) {
	if store == nil {
		return nil, nil, 0, false, nil
	}
	fabrics, err := store.ListFabrics(ctx)
	if err != nil {
		logger.Warn("matter.bridge.case.list_fabrics", slog.String("err", err.Error()))
		return nil, nil, 0, false, nil
	}
	if len(fabrics) == 0 {
		return nil, nil, 0, false, nil
	}
	chosen := pickFabric(fabrics, cfg.FabricID)
	if chosen == nil {
		return nil, nil, 0, false, nil
	}
	id, err := store.GetIdentity(ctx, chosen.FabricIndex)
	if err != nil {
		logger.Warn("matter.bridge.case.get_identity",
			slog.Int("fabric_index", int(chosen.FabricIndex)),
			slog.String("err", err.Error()))
		return nil, nil, 0, false, nil
	}
	priv, err := privKeyFromScalar(id.PrivateKey)
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("matter case identity: %w", err)
	}
	verifier, err = mattercert.NewVerifier(chosen.RootPublicKey, mattercert.SystemTime{})
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("matter case verifier: %w", err)
	}
	opIPK, err := deriveOperationalIPK(id.IPK, chosen.CompressedID)
	if err != nil {
		logger.Warn("matter.bridge.case.load_ipk",
			slog.Int("fabric_index", int(chosen.FabricIndex)),
			slog.String("err", err.Error()))
		return nil, nil, 0, false, nil
	}
	identity = &sigma.Identity{
		NOC:                id.NOC,
		ICAC:               id.ICAC,
		PrivateKey:         priv,
		NodeID:             chosen.NodeID,
		FabricID:           chosen.FabricID,
		CompressedFabricID: chosen.CompressedID,
		IPK:                opIPK,
		FabricIndex:        chosen.FabricIndex,
	}
	return identity, verifier, chosen.FabricIndex, true, nil
}

// pickFabric chooses the fabric to back CASE: explicit FabricID match
// when cfg specifies one (and a row exists), else the lowest
// FabricIndex (deterministic for single-fabric daemons).
//
// The config-supplied `fabric_id` is a soft preference — every real
// fabric is installed via AddNOC with the *commissioner's* chosen
// FabricID (e.g. Apple Home picks a random 64-bit value), so pinning
// the daemon to `fabric_id: 1` would strand a freshly-paired ecosystem
// in the ephemeral path on the next restart. When the preference does
// not match any persisted fabric, fall back to the deterministic
// lowest-index row instead of returning nil.
func pickFabric(fabrics []matterstore.FabricRecord, wantFabricID uint64) *matterstore.FabricRecord {
	if wantFabricID != 0 {
		for i := range fabrics {
			if fabrics[i].FabricID == wantFabricID {
				return &fabrics[i]
			}
		}
		// Fall through to lowest-index fallback below.
	}
	lowest := &fabrics[0]
	for i := range fabrics[1:] {
		f := &fabrics[i+1]
		if f.FabricIndex < lowest.FabricIndex {
			lowest = f
		}
	}
	return lowest
}

// caseFabricEntry tracks one installed CASE fabric for the
// multi-fabric destination resolver — see the long comment at the
// `caseFabrics` declaration inside runDaemon.
type caseFabricEntry struct {
	identity      *sigma.Identity
	verifier      sigma.PeerVerifier
	rootPublicKey []byte // raw 0x04||X||Y, 65 bytes — input to the destinationID HMAC
	fabricIndex   uint8
	// ipkCandidates holds every operational IPK derived from
	// GroupKeySetID 0's epoch keys (Matter §11.2.10.6, the reserved
	// IPK key set). A KeySetWrite that rotates the IPK can leave up
	// to three keys simultaneously valid during the grace window;
	// [ipkOperationalCandidates] populates one entry per present
	// EpochKeyN so [caseDestinationResolver.ResolveSigma1Destination]
	// can match a Sigma1 signed with any of them. Always has at least
	// one element.
	ipkCandidates [][16]byte
}

// loadFabricRootPublicKey reads the raw P-256 root public key (65
// bytes 0x04||X||Y) for the given fabric_index from the matter store.
// Used by the daemon to populate the case-fabric registry on daemon
// start so [caseDestinationResolver] can match Sigma1's DestinationID
// before any AddNOC fires — required so a reboot doesn't lose the
// post-pair multi-fabric routing.
func loadFabricRootPublicKey(ctx context.Context, store *matterstore.Store, fabricIndex uint8) ([]byte, error) {
	if store == nil {
		return nil, errors.New("matter store: nil")
	}
	fabric, err := store.GetFabric(ctx, fabricIndex)
	if err != nil {
		return nil, fmt.Errorf("matter store: get fabric %d: %w", fabricIndex, err)
	}
	return append([]byte(nil), fabric.RootPublicKey...), nil
}

// loadAdditionalFabricsForCase rehydrates every persisted fabric apart
// from the seed (already loaded by the caller) into the
// `caseFabrics` registry so the multi-fabric destination resolver can
// satisfy a Sigma1 from ANY commissioned controller after a daemon
// restart. Apple's Multi-Admin pair installs two fabrics (iPhone Home
// + HomePod/Apple TV Hub) — without this, only the lowest-index one
// would have an identity available, and the other's MTRDevice would
// land in `MTRDeviceStateUnreachable` on first reconnect.
//
// Errors per row are logged at warn; the function never fails — a
// partial rehydration is strictly better than none. Returns the
// number of additional fabrics that were loaded.
func loadAdditionalFabricsForCase(
	ctx context.Context,
	store *matterstore.Store,
	seedIdx uint8,
	caseFabrics map[uint8]*caseFabricEntry,
	mu *sync.RWMutex,
	logger *slog.Logger,
) int {
	if store == nil {
		return 0
	}
	fabrics, err := store.ListFabrics(ctx)
	if err != nil {
		logger.Warn("matter.bridge.case.multi_fabric_list", slog.String("err", err.Error()))
		return 0
	}
	loaded := 0
	for i := range fabrics {
		f := fabrics[i]
		if f.FabricIndex == seedIdx {
			continue // already loaded by caller
		}
		id, idErr := store.GetIdentity(ctx, f.FabricIndex)
		if idErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_identity",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", idErr.Error()))
			continue
		}
		priv, pkErr := privKeyFromScalar(id.PrivateKey)
		if pkErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_privkey",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", pkErr.Error()))
			continue
		}
		verifier, verErr := mattercert.NewVerifier(f.RootPublicKey, mattercert.SystemTime{})
		if verErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_verifier",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", verErr.Error()))
			continue
		}
		ipkCandidates, ipkErr := ipkOperationalCandidates(ctx, store, f.FabricIndex, f.CompressedID, id.IPK)
		if ipkErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_ipk",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", ipkErr.Error()))
			continue
		}
		identity := &sigma.Identity{
			NOC:                id.NOC,
			ICAC:               id.ICAC,
			PrivateKey:         priv,
			NodeID:             f.NodeID,
			FabricID:           f.FabricID,
			CompressedFabricID: f.CompressedID,
			IPK:                ipkCandidates[0],
			FabricIndex:        f.FabricIndex,
		}
		entry := &caseFabricEntry{
			identity:      identity,
			verifier:      verifier,
			rootPublicKey: append([]byte(nil), f.RootPublicKey...),
			fabricIndex:   f.FabricIndex,
			ipkCandidates: ipkCandidates,
		}
		mu.Lock()
		caseFabrics[f.FabricIndex] = entry
		mu.Unlock()
		logger.Info("matter.bridge.case.multi_fabric_loaded",
			slog.Int("fabric_index", int(f.FabricIndex)),
			slog.Uint64("node_id", f.NodeID),
			slog.Uint64("fabric_id", f.FabricID))
		loaded++
	}
	return loaded
}

// caseDestinationResolver implements [sigma.IdentityResolver] by
// walking the daemon's `caseFabrics` map and matching every installed
// fabric's computed destinationID against the inbound Sigma1.
// Mirrors matter.js packages/protocol/src/fabric/FabricManager.ts:
// `findFabricFromDestinationId`. Returning (_, _, false) lets the
// responder fall through to its baseline identity (pre-AddNOC /
// single-fabric / test paths).
type caseDestinationResolver struct {
	mu      *sync.RWMutex
	fabrics *map[uint8]*caseFabricEntry
	logger  *slog.Logger
}

func (r caseDestinationResolver) ResolveSigma1Destination(destinationID [32]byte, initiatorRandom [sigma.RandomSize]byte) (*sigma.Identity, sigma.PeerVerifier, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.fabrics == nil {
		return nil, nil, false
	}
	for _, entry := range *r.fabrics {
		if entry == nil || entry.identity == nil {
			continue
		}
		// Try every IPK candidate for this fabric's GroupKeySetID 0
		// (Matter §11.2.10.6 IPK rotation grace window). Mirrors chip
		// CASESession.cpp:958-976 (`FindLocalNodeFromDestinationId`),
		// which loops `ipkKeySet.epoch_keys` and caches whichever key
		// produced the matching candidate — the winning key, not
		// necessarily the newest one, seeds the rest of the exchange
		// because the initiator's Sigma2/Sigma3 salts are keyed on it.
		candidates := entry.ipkCandidates
		if len(candidates) == 0 {
			candidates = [][16]byte{entry.identity.IPK}
		}
		for _, opIPK := range candidates {
			cand := sigma.ComputeDestinationID(
				opIPK,
				initiatorRandom,
				entry.rootPublicKey,
				entry.identity.FabricID,
				entry.identity.NodeID,
			)
			if cand != destinationID {
				continue
			}
			if r.logger != nil {
				r.logger.Debug("matter.bridge.case.identity_resolved",
					slog.Int("fabric_index", int(entry.fabricIndex)),
					slog.Uint64("fabric_id", entry.identity.FabricID),
					slog.Uint64("node_id", entry.identity.NodeID))
			}
			resolved := *entry.identity
			resolved.IPK = opIPK
			return &resolved, entry.verifier, true
		}
	}
	if r.logger != nil {
		r.logger.Debug("matter.bridge.case.identity_unresolved",
			slog.Int("installed_fabrics", len(*r.fabrics)))
	}
	return nil, nil, false
}

// ResolveFabricIndex implements [sigma.FabricIndexResolver] for the
// Sigma2_Resume path: the resumption record names the fabric directly
// (there is no Sigma3 / DestinationID on resume), so the lookup is a
// plain map hit. A miss means the fabric was removed after the record
// was written; the responder then falls through to Full Sigma.
// Mirrors matter.js CaseServer.ts:151 taking `fabric` from the record.
func (r caseDestinationResolver) ResolveFabricIndex(fabricIndex uint8) (*sigma.Identity, sigma.PeerVerifier, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.fabrics == nil {
		return nil, nil, false
	}
	entry := (*r.fabrics)[fabricIndex]
	if entry == nil || entry.identity == nil {
		return nil, nil, false
	}
	return entry.identity, entry.verifier, true
}

// deriveOperationalIPK turns the raw IPKValue persisted from
// AddNOC into the per-fabric "Operational Identity Protection Key"
// per Matter Core §11.2.4 (Group Identity Protection Key derivation):
//
//	operationalIPK = HKDF-SHA256(
//	    ikm  = rawIPK,                    // 16 bytes from AddNOC.IPKValue
//	    salt = compressedFabricID,        // 8 bytes
//	    info = "GroupKey v1.0",
//	    L    = 16,
//	)
//
// CASE Sigma2 / Sigma3 / secure-session salts use this derived key
// as their leading prefix; matter.js's `Fabric.create` does the same
// derivation. Skipping it (i.e. passing the raw IPKValue directly to
// the Sigma layer) makes Apple Home reject Sigma2 with
// SecureChannel/INVALID_PARAMETER because the responder produces a
// different S2K than the initiator.
func deriveOperationalIPK(rawIPK []byte, compressedFabricID [8]byte) ([16]byte, error) {
	var out [16]byte
	if len(rawIPK) != 16 {
		return out, fmt.Errorf("matter case ipk: raw IPK length %d, want 16", len(rawIPK))
	}
	derived, err := hkdf.Key(sha256.New, rawIPK, compressedFabricID[:], "GroupKey v1.0", 16)
	if err != nil {
		return out, fmt.Errorf("matter case ipk: hkdf: %w", err)
	}
	copy(out[:], derived)
	return out, nil
}

// ipkOperationalCandidates derives every operational-IPK destination-ID
// input that is currently valid for a fabric's GroupKeySetID 0 (the
// reserved IPK key set, Matter §11.2.10.6). A KeySetWrite that rotates
// the IPK carries up to three epoch keys so the previous key stays
// valid during the transition; matter.js
// packages/protocol/src/groups/FabricGroups.ts:125-156
// (`setFromGroupKeySet`) HKDF-derives an operational key for every
// present EpochKeyN, and
// packages/protocol/src/fabric/FabricManager.ts:302-317
// (`findFabricFromDestinationId`) tries every one when matching an
// inbound Sigma1 — chip mirrors this in
// CASESession.cpp:958-976 (`FindLocalNodeFromDestinationId`), iterating
// `ipkKeySet.epoch_keys` and caching whichever key produced the
// matching candidate for the rest of the exchange.
//
// operational_credentials.go's AddNOC handler seeds a GroupKeySetID=0
// row with EpochKey0=IPKValue on every successful pair, so the
// rawIPKFallback branch only fires for identities loaded before that
// write path existed (or when the store is unavailable).
func ipkOperationalCandidates(ctx context.Context, st *matterstore.Store, fabricIndex uint8, compressedFabricID [8]byte, rawIPKFallback []byte) ([][16]byte, error) {
	if st != nil {
		gks, err := st.GetGroupKeySet(ctx, fabricIndex, 0)
		switch {
		case err == nil:
			var out [][16]byte
			for _, epochKey := range [][]byte{gks.EpochKey0, gks.EpochKey1, gks.EpochKey2} {
				if len(epochKey) == 0 {
					continue
				}
				opIPK, derr := deriveOperationalIPK(epochKey, compressedFabricID)
				if derr != nil {
					return nil, derr
				}
				out = append(out, opIPK)
			}
			if len(out) > 0 {
				return out, nil
			}
		case !errors.Is(err, matterstore.ErrGroupKeySetNotFound):
			return nil, err
		}
	}
	opIPK, err := deriveOperationalIPK(rawIPKFallback, compressedFabricID)
	if err != nil {
		return nil, err
	}
	return [][16]byte{opIPK}, nil
}

// privKeyFromScalar reconstructs an *ecdsa.PrivateKey from a 32-byte
// raw P-256 scalar (the storage format used by the matter store). The
// public key is computed via ScalarBaseMult.
func privKeyFromScalar(scalar []byte) (*ecdsa.PrivateKey, error) {
	if len(scalar) != 32 {
		return nil, fmt.Errorf("matter case identity: private key length %d, want 32", len(scalar))
	}
	// ParseRawPrivateKey derives the public half and rejects a scalar
	// outside [1, n-1]. The hand-rolled predecessor accepted zero and
	// n itself, both of which yield the identity as the public point.
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), scalar)
	if err != nil {
		return nil, fmt.Errorf("matter case identity: %w", err)
	}
	return priv, nil
}

// trustAnyPeerVerifier is the explicit "CASE is not production-ready"
// sentinel sigma.PeerVerifier. It always returns an error so any
// real Sigma3 attempt fails loudly with "production verifier
// missing" — far better than the previous behaviour of returning a
// random ephemeral key (which caused Sigma3 signature validation to
// fail with an opaque crypto error and no clear signal that the
// fabric-identity wiring was missing).
//
// Replace with [mattercert.NewVerifier] once persistent fabric
// identity (NOC + ICAC + RootCert) is wired.
type trustAnyPeerVerifier struct{}

func (trustAnyPeerVerifier) VerifyAndExtractPubKey(_, _ []byte) (*ecdsa.PublicKey, error) {
	return nil, errors.New("trustAnyPeerVerifier: production verifier missing — wire mattercert.Verifier with persistent fabric identity")
}

// caseResumptionStoreAdapter bridges [sigma.ResumptionStore] to the
// operational [Manager]'s persistent record. Hands the responder a
// stable read-side so an inbound Sigma1 carrying a known
// ResumptionID can fast-path through Sigma2_Resume. Mirrors matter.js
// CaseServer.ts and chip CASESession.cpp resumption branch.
type caseResumptionStoreAdapter struct {
	mgr *operational.Manager
}

// GetByID implements [sigma.ResumptionStore].
func (a caseResumptionStoreAdapter) GetByID(resumptionID []byte) (*sigma.ResumptionRecord, error) {
	if a.mgr == nil {
		return nil, nil
	}
	rec, err := a.mgr.LookupResumption(context.Background(), resumptionID)
	if err != nil {
		// Treat any lookup failure as "no record": the responder will
		// fall through to the full Sigma1-3 handshake, which is the
		// correct behaviour when persistence is unavailable. The
		// nilerr suppression is deliberate — Sigma1 ID-not-found is
		// the documented "no record" signal and must not propagate.
		return nil, nil //nolint:nilerr // intentional fallthrough to full handshake
	}
	if len(rec.ResumptionID) == 0 || len(rec.SharedSecret) == 0 {
		return nil, nil
	}
	// Hand the full record through: the resume path has no Sigma3, so
	// FabricIndex / PeerNodeID / CATs are authoritative from persistence
	// alone (matter.js CaseServer.ts:151 destructures fabric, peerNodeId
	// and caseAuthenticatedTags straight from the record).
	return &sigma.ResumptionRecord{
		ResumptionID: rec.ResumptionID,
		SharedSecret: rec.SharedSecret,
		FabricIndex:  rec.FabricIndex,
		PeerNodeID:   rec.PeerNodeID,
		PeerCATs:     rec.CASEAuthTags,
	}, nil
}

// rootClusterRefs carries the typed handles to selected root-endpoint
// cluster servers the daemon needs to drive lifecycle events (StartUp /
// ShutDown / Leave / BootReason) and persistence-seeded counters
// (RebootCount / TotalOperationalHours). The cluster servers are also
// in the slice returned alongside, so the bridge mounts them as usual;
// these refs are exclusively for daemon-side wiring.
type rootClusterRefs struct {
	BasicInformation     *mattercore.BasicInformation
	GeneralDiagnostics   *mattercore.GeneralDiagnostics
	GeneralCommissioning *mattercore.GeneralCommissioning
	AccessControl        *mattercore.AccessControl
}

// resolveBridgeUniqueID returns the root node's stable BasicInformation
// UniqueID. UniqueID carries Matter §11.1.5.13 quality F — it must not change
// once a controller has commissioned the bridge — so the value is persisted on
// first sight and pinned across every later boot, leaving a bridge rename (a
// node_label change) unable to rotate it. matter.js persists UniqueID through
// its StorageService and chip through GenerateUniqueId()'s persistent storage
// for the same reason.
//
// The seed reproduces exactly what an un-pinned [mattercore.BasicInformation]
// would derive — [mattercore.DeriveUniqueID] over the SAME construction
// NodeLabel and serial the cluster uses (BasicInformation's identityLabel is
// the construction NodeLabel, not the commissioner-restored one) — so the boot
// that first persists it on an already-commissioned bridge keeps that bridge's
// established identity instead of orphaning its pairings.
//
// A nil store (test wiring, no persistence) yields the derived value without
// persisting it — identical to the un-pinned behaviour for that process.
//
// north.matter.dev_rotate_unique_ids bypasses pinning entirely: every boot
// re-derives (with [mattercore.DeriveUniqueID]'s per-boot bootid.Salt(),
// active only while the flag is on) and re-persists, so the knob keeps
// rotating the root identity across restarts instead of freezing on the
// value the first rotated boot happened to persist. The persisted value is
// tagged with [matterstore.SettingUniqueIDRotated] while rotation writes it,
// so a later boot with the flag off can tell a leftover salted value apart
// from a genuinely pinned one and re-derive the deterministic form instead
// of pinning the stale salt forever.
func resolveBridgeUniqueID(ctx context.Context, store *matterstore.Store, mc config.NorthMatter, rootSerial string, logger *slog.Logger) string {
	if store != nil && !mc.DevRotateUniqueIDs {
		v, ok, err := store.GetSetting(ctx, matterstore.SettingUniqueID)
		if err != nil {
			// A failed read is "unknown", not "unset": deriving and
			// persisting below would silently overwrite a pinned value the
			// daemon simply could not read this boot, reassigning the
			// bridge's Matter identity under every paired controller.
			// Derive an in-memory value for THIS boot only — its inputs are
			// identical to the pinned derivation whenever the identity
			// hasn't changed, so this degrades to the same value in the
			// common case — and leave the store untouched; the next boot
			// retries the read.
			logger.Warn("matter.bridge.basicinfo.read_unique_id", slog.String("err", err.Error()))
			return mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, rootSerial)
		}
		if ok && v != "" {
			rotated, rOK, rErr := store.GetSetting(ctx, matterstore.SettingUniqueIDRotated)
			if rErr != nil || !rOK || rotated != "1" {
				return v
			}
			// The pinned value was salted by a boot that had
			// dev_rotate_unique_ids enabled; rotation is off now, so this
			// is a stale one-shot salt, not a real pinned identity. Fall
			// through to re-derive and persist the deterministic form,
			// which also clears the rotated marker below.
		}
	}
	uid := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, rootSerial)
	if store != nil {
		if err := store.SetSetting(ctx, matterstore.SettingUniqueID, uid); err != nil {
			logger.Warn("matter.bridge.basicinfo.persist_unique_id", slog.String("err", err.Error()))
		}
		rotatedFlag := ""
		if mc.DevRotateUniqueIDs {
			rotatedFlag = "1"
		}
		if err := store.SetSetting(ctx, matterstore.SettingUniqueIDRotated, rotatedFlag); err != nil {
			logger.Warn("matter.bridge.basicinfo.persist_unique_id_rotated_marker", slog.String("err", err.Error()))
		}
	}
	return uid
}

// buildRootClusters constructs the cluster servers the bridge mounts
// on endpoint 0. This is the surface chip-tool's
// `ReadCommissioningInfo` queries during commissioning — without
// these clusters the read returns `UnsupportedCluster` for every
// attribute and chip-tool aborts before installing a fabric.
//
// Returned set (Matter §11):
//
//   - 0x0028 BasicInformation         — VendorID, ProductID, NodeLabel, …
//   - 0x0030 GeneralCommissioning     — ArmFailSafe, RegulatoryConfig, …
//   - 0x0031 NetworkCommissioning     — wire interface info
//   - 0x0033 GeneralDiagnostics       — boot reason, network interfaces
//   - 0x003E OperationalCredentials   — NOC / fabric install (PASE → CASE)
//   - 0x003F GroupKeyManagement       — group quotas
//
// DiagnosticLogs (0x0032) and OTASoftwareUpdateRequestor (0x002A) are
// deliberately absent from the returned set; the reasoning is at their
// respective decision points below.
//
// Operators that disable Matter via cfg.Enabled never reach this
// function. Construction errors are surfaced individually so a
// single misconfigured cluster cannot block the rest.
func buildRootClusters(ctx context.Context, mc config.NorthMatter, store *matterstore.Store, bridge *matterbridge.Bridge, adv mdns.Advertiser, logger *slog.Logger, onFabricInstalledExtra func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, rootPub []byte), adoptSessionForFabric func(ctx context.Context, fabricIndex uint8), closePaseSessions func() int) ([]interfaces.MatterClusterServer, *mattercore.OperationalCredentials, rootClusterRefs, error) { //nolint:gocognit,funlen,gocyclo // composition/wiring: long sequential setup
	out := make([]interfaces.MatterClusterServer, 0, 8)
	var opCreds *mattercore.OperationalCredentials
	var refs rootClusterRefs

	// SerialNumber is a human-readable bridge identifier. ha-bridge
	// (`packages/backend/src/utils/json/create-bridge-server-config.ts`)
	// derives it from an MD5 hash of the bridge id; Apple Home's HAP-
	// service rebuild reads SerialNumber for accessory deduplication and
	// logs `could not find cached attribute values` when it is absent.
	// We synthesise a stable 16-char hex digest of
	// `VID|PID|NodeLabel` — distinct from UniqueID (full SHA-256
	// digest) so the matter.js
	// `basic-information-validators.ts:26` invariant
	// (`uniqueId !== serialNumber`) holds for the Root cluster too.
	rootSerial := func() string {
		h := sha256.Sum256(fmt.Appendf(nil, "%04X|%04X|%s|serial",
			mc.VendorID, mc.ProductID, mc.NodeLabel))
		return hex.EncodeToString(h[:8])
	}()
	// UniqueID carries Matter §11.1.5.13 quality F (fixed for the lifetime of
	// the device): once a controller has commissioned the bridge the value must
	// never change, or Apple Home / Google Home treat every bridged accessory
	// as new and force a re-pair. The un-pinned derivation depends on NodeLabel,
	// so a bridge rename (a node_label edit) would otherwise rotate it. Resolve
	// it to a persisted, pinned value here and hand it to the cluster.
	uniqueID := resolveBridgeUniqueID(ctx, store, mc, rootSerial, logger)
	bi, err := mattercore.NewBasicInformation(mattercore.Config{
		VendorID:    mc.VendorID,
		ProductID:   mc.ProductID,
		NodeLabel:   mc.NodeLabel,
		UniqueID:    uniqueID,
		VendorName:  "openccu-loom",
		ProductName: "openccu-loom Matter Bridge",
		// SoftwareVersion is derived from the same build string that feeds
		// SoftwareVersionStr so the two attributes always describe the
		// same release — matter.js keeps the pair consistent by deriving
		// one from the other (BasicInformationServer.ts:71), and a
		// divergent pair (the previous hard-coded 1 next to "0.32.1")
		// crashes at least one ecosystem hub during bridge sync.
		SoftwareVersion: mattercore.SoftwareVersionFromString(build.Version),
		// SoftwareVersionStr carries the human-readable daemon build so
		// controllers display a real version. HardwareVersionStr is a
		// deliberate constant — a software bridge has no hardware revision,
		// but Matter mandates a non-empty string (constraint "1 to 64").
		SoftwareVersionStr: build.Version,
		HardwareVersion:    1,
		HardwareVersionStr: "1.0",
		SerialNumber:       rootSerial,
	})
	if err != nil {
		return nil, nil, refs, fmt.Errorf("BasicInformation: %w", err)
	}
	if store != nil {
		// Restore commissioner-written NodeLabel / Location — both carry
		// Matter §11.1.6 "N" (non-volatile) quality, so a value a
		// controller wrote must survive the restart and override the
		// config default. matter.js restores them via its persistent
		// behavior state.
		if v, ok, gerr := store.GetSetting(ctx, matterstore.SettingNodeLabel); gerr == nil && ok {
			if serr := bi.SetNodeLabel(v); serr != nil {
				logger.Warn("matter.bridge.basicinfo.restore_node_label", slog.String("err", serr.Error()))
			}
		}
		if v, ok, gerr := store.GetSetting(ctx, matterstore.SettingLocation); gerr == nil && ok {
			if serr := bi.SetLocation(v); serr != nil {
				logger.Warn("matter.bridge.basicinfo.restore_location", slog.String("err", serr.Error()))
			}
		}
		bi.SetOnPersistentWrite(func(nodeLabel, location string) { //nolint:contextcheck // fires from an inbound Matter write with no caller ctx; a background ctx is correct for the fire-and-forget persist
			persistCtx := context.Background()
			if serr := store.SetSetting(persistCtx, matterstore.SettingNodeLabel, nodeLabel); serr != nil {
				logger.Warn("matter.bridge.basicinfo.persist_node_label", slog.String("err", serr.Error()))
			}
			if location != "" {
				if serr := store.SetSetting(persistCtx, matterstore.SettingLocation, location); serr != nil {
					logger.Warn("matter.bridge.basicinfo.persist_location", slog.String("err", serr.Error()))
				}
			}
		})
	}
	out = append(out, bi)
	refs.BasicInformation = bi

	gc, err := mattercore.NewGeneralCommissioning(mattercore.GeneralCommissioningConfig{
		LocationCapability:           mattercore.RegulatoryIndoorOutdoor,
		SupportsConcurrentConnection: true,
		// L10-G fix: on a successful CommissioningComplete, revoke the
		// open enhanced-commissioning window — chip's
		// CommissioningWindowManager does this automatically. Lazy-resolves
		// the window through the bridge accessor to avoid the initialisation
		// ordering issue (the window is attached AFTER buildRootClusters
		// returns).
		OnCommissioningComplete: func(ctx context.Context, fabricIndex uint8) {
			w := bridge.CommissioningWindow()
			if w == nil {
				return
			}
			if err := w.RevokeWindow(ctx); err != nil {
				logger.Debug("matter.bridge.commissioning_window.revoke_on_complete",
					slog.Int("fabric_index", int(fabricIndex)),
					slog.String("err", err.Error()))
			}
		},
		// On FailSafe expiry (commissioner crashed mid-pair between
		// AddTrustedRootCertificate + CSRRequest + AddNOC and
		// CommissioningComplete) revoke the open commissioning window AND
		// let the OpCreds cluster clear its pending state
		// (pendingPrivKey, pendingTrustRoot, pendingCSRSessionID). Without
		// this hook a partial pair leaves the bridge in a state where the
		// next pair attempt collides with the orphan fabric row and Apple
		// Home aborts the second commissioning with FabricConflict.
		// OpCreds.handleRemoveFabric already clears pending state for the
		// active fabric; the FailSafe-expiry path catches the pre-AddNOC
		// race where no fabric exists yet.
		OnFailSafeExpired: func(ctx context.Context, fabricIndex uint8) {
			if w := bridge.CommissioningWindow(); w != nil {
				_ = w.RevokeWindow(ctx)
			}
			logger.Info("matter.bridge.failsafe.expired",
				slog.Int("fabric_index", int(fabricIndex)))
		},
	})
	if err != nil {
		return nil, nil, refs, fmt.Errorf("GeneralCommissioning: %w", err)
	}
	out = append(out, gc)
	refs.GeneralCommissioning = gc

	// NetworkCommissioning (0x0031) is MANDATORY on RootEndpoint per
	// matter.js HEAD `packages/model/src/standard/elements/root-node.element.ts:34`
	// (`conformance: "!CustomNetworkConfig"` = mandatory unless the
	// device exposes its own CustomNetworkConfig feature, which a
	// stationary Matter bridge never does). Without it Apple Home's
	// HMMTRAccessoryServerBrowser cannot determine "supported link
	// layer types" from Descriptor.ServerList and aborts the HAP
	// service rebuild with `Error retrieving supported link layers -
	// no topology` + `Nil supported link layer types`, leaving the
	// pair hanging at "Adding to Home" until the iOS Home UI gives
	// up with the "accessory could not be added" dialog.
	// FeatureMap = ETH (bit 2 = 0x4) — openccu-loom is a stationary
	// bridge running on an Ethernet/WiFi host; Ethernet-class is the
	// matter.js examples/device-onoff stack's choice too.
	netComm := mattercore.NewNetworkCommissioning(mattercore.NetworkCommissioningConfig{})
	out = append(out, netComm)

	// DiagnosticLogs (0x0032) is optional on RootEndpoint
	// (matter.js root-node.element.ts:35 `conformance: "O"`). Not
	// mounted here — the bridge has no diagnostic-log surface yet.
	gd := mattercore.NewGeneralDiagnostics(mattercore.BootReasonPowerOnReboot)
	out = append(out, gd)
	refs.GeneralDiagnostics = gd
	// OTASoftwareUpdateRequestor (0x002A) is intentionally NOT mounted
	// on the Root endpoint. matter.js's RootEndpoint
	// (packages/node/src/endpoints/root.ts:248-277) does not list it in
	// `optional` or `mandatory`; it is meant for a separate OTA-update
	// device-type composition. home-assistant-matter-bridge also does
	// not mount it. Apple Home's HAP service mapper reads
	// Descriptor.ServerList and rejects an unknown cluster on the
	// RootNode device type as schematic inconsistency.
	//
	// TimeSynchronization (0x0038) is NOT mounted by default. matter.js's
	// RootEndpoint lists it in `optional` only (root.ts:215), meant for
	// hub/coordinator devices; home-assistant-matter-bridge omits it and
	// Apple's HAP mapper may reject the extra cluster on RootNode. It is
	// mounted only behind the explicit, default-off operator opt-in
	// (north.matter.enable_time_sync) for controllers that genuinely need a
	// time-sync surface — see notes/parity/by_design.md (BD-Matter-TimeSync-NotMounted).
	if mc.TimeSyncEnabled() {
		out = append(out, mattercore.NewTimeSynchronization())
		logger.Warn("matter.bridge.time_sync.mounted — TimeSynchronization (0x0038) advertised on RootNode by operator opt-in; some controllers (e.g. Apple Home) may reject pairing")
	}
	//
	// Re-add the OTA cluster only if the bridge ever takes on the OTA-provider
	// device-class role.
	// IcdManagement (0x0046) is intentionally NOT mounted on the Root
	// endpoint. Mirrors matter.js RootEndpoint
	// (packages/node/src/endpoints/root.ts:248-277): IcdManagement is
	// in `optional`, not `mandatory`. Strict controllers detect ICD-
	// status via `rootServerList.includes(IcdManagement.id)`
	// (NodePhysicalProperties.ts:33). Mounting the cluster makes them
	// treat the bridge as an Intermittently-Connected Device and
	// expect CheckIn-Protocol, StayActiveRequest, LIT/SIT features —
	// none of which a Matter-Bridge needs. Re-add only if the bridge
	// ever becomes a real ICD (battery-powered, long-idle).

	// Descriptor (0x001D) — MANDATORY on every Matter endpoint per
	// §9.5. Apple Home reads `DeviceTypeList`, `ServerList`, and
	// `PartsList` immediately after CommissioningComplete to confirm
	// the device type advertised matches a known Matter device-type
	// (RootNode 0x0016 here). Missing Descriptor surfaces as
	// UnsupportedCluster on the controller and the iCloud-Heim sync
	// step times out with a generic add-failed error.
	//
	// ServerList is the static set of clusters mounted on Endpoint 0;
	// PartsList enumerates the bridged dynamic endpoints (populated
	// after Reassemble via the bridge's endpoint registry, but the
	// initial value exposed here is empty — Apple re-reads after the
	// PartsList becomes non-empty).
	rootDescriptor, err := mattercore.NewDescriptor(
		[]mattercore.DeviceTypeStruct{
			// Matter Device Library §2.1 + matter.js's
			// `RootNodeDt.id = 0x16, revision = 4` (per
			// `packages/model/src/standard/elements/root-node.element.ts`
			// in matter.js HEAD). Every root endpoint MUST advertise the
			// RootNode device type. The schema table in
			// `internal/north/matter/schema/devicetypes.go` already
			// carries `0x0016: 4` — older hardcoded `Revision: 3` value
			// here drifted behind matter.js HEAD; Apple Home's HAP
			// mapper logs `Unable to find HAP service type for
			// deviceType 22` when an unexpected revision flows through.
			//
			// Apple-bridge topology: the Aggregator device type (0x000E)
			// is NOT advertised here — it lives on its own endpoint
			// (EP 1), exactly the way matter.js's
			// `examples/device-bridge-onoff/src/BridgedDevicesNode.ts`
			// composes a Matter bridge. Apple Home's
			// HMMTRAccessoryServerBrowser refuses to render bridged
			// devices when EP 0 carries both RootNode + Aggregator
			// simultaneously: it cannot disambiguate which endpoint to
			// crawl for the bridged-children PartsList and falls back to
			// "empty bridge" UX (pair lands, no devices visible).
			{DeviceType: 0x0016, Revision: matterschema.DeviceTypeRevisions[0x0016]},
		},
		// ServerList is derived AFTER the full endpoint composition via
		// SetServerListProvider (Bug K fix — see end of this function).
		// Mirrors matter.js DescriptorServer.#serverList
		// (DescriptorServer.ts:236-244): the advertised ServerList is
		// always the set of mounted behaviors, never a hardcoded list
		// that can diverge when a cluster is added without updating the
		// list. Apple's HAP mapper rejects an endpoint as schematically
		// inconsistent when the Subscribe stream contains AttributeReports
		// for a cluster not in ServerList (or vice versa).
		nil,
		nil, // ClientList — empty; we are not a controller
		nil, // PartsList — bridge dynamically populates after Reassemble
	)
	if err != nil {
		return nil, nil, refs, fmt.Errorf("descriptor: %w", err)
	}
	out = append(out, rootDescriptor)

	// AdministratorCommissioning (0x003C). Wires the bridge's
	// commissioning-window controller (set via
	// AttachCommissioningWindow earlier in the boot sequence) so
	// controllers can read WindowStatus + drive
	// OpenCommissioningWindow / RevokeCommissioning. Without a
	// window controller the cluster reports BUSY for every command,
	// which is the spec-correct behaviour.
	admComm := matterwire.NewAdministratorCommissioning()
	if w := bridge.CommissioningWindow(); w != nil {
		admComm.SetController(w)
	}
	if store != nil {
		admComm.SetVendorIDResolver(func(ctx context.Context, fabricIndex uint8) uint16 {
			rec, err := store.GetFabric(ctx, fabricIndex)
			if err != nil {
				return 0
			}
			return rec.VendorID
		})
		// Wire the fabric counter so the cluster can detect an
		// uncommissioned (factory) bridge. When FabricCount == 0 the
		// §11.19.8.1 OpenCommissioningWindow upper bound extends from
		// 900 s to 172 800 s (48 h) so a first-pairing window can outlast
		// the standard cap. Without this hook IsUncommissioned stays
		// false, the 900 s cap always applies, and a fresh bridge can
		// never open the long first-pairing window. Mirrors chip
		// CommissioningWindowManager::MaxCommissioningTimeout (= the 48h
		// bound when no fabrics are installed).
		admComm.SetFabricCounter(func(ctx context.Context) (int, error) {
			recs, err := store.ListFabrics(ctx)
			if err != nil {
				return 0, err
			}
			return len(recs), nil
		})
	}
	// Wire the FailSafe-armed accessor so OpenCommissioningWindow enforces
	// that no FailSafe window is active before opening a new one.
	// Mirrors chip CommissioningWindowManager + AdministratorCommissioningCluster
	// VerifyOrExit(IsFailSafeFullyDisarmed, ...).
	admComm.SetIsFailSafeArmed(gc.FailSafeArmed)
	out = append(out, admComm)

	if store != nil {
		// AccessControl (0x001F) is mandatory on the Root endpoint per
		// Matter Core §1.3.6.6. Apple Home reads `acl` immediately
		// after CASE; an absent cluster surfaces as
		// `UnsupportedCluster` for every subsequent fabric-scoped
		// read and Apple's UI tears the new fabric down via
		// RemoveFabric. AddNOC's default-entry path
		// (operational_credentials.handleAddNOC) populates the store
		// row this cluster reads back.
		if ac, err := mattercore.NewAccessControl(store); err == nil {
			out = append(out, ac)
			refs.AccessControl = ac
		} else {
			logger.Warn("matter.bridge.access_control.build", slog.String("err", err.Error()))
		}
	}

	if store != nil {
		// DAC / PAI / CD bytes the bridge presents to commissioners.
		// Vendor-supplied bundle (mc.Attestation.*) wins when all
		// four files load + the DAC key matches the cert; otherwise
		// fall back to an ephemeral self-signed DAC that requires
		// chip-tool's `--bypass-attestation-verifier true` flag.
		var (
			dacKey *ecdsa.PrivateKey
			dac    []byte
			pai    []byte
			cd     []byte
		)
		if k, certBytes, paiBytes, cdBytes, vendorOK := loadVendorAttestation(mc.Attestation, logger); vendorOK {
			dacKey, dac, pai, cd = k, certBytes, paiBytes, cdBytes
			logger.Info("matter.bridge.attestation.vendor",
				slog.String("hint", "production DAC/PAI/CD loaded from config — chip-tool can validate without --bypass-attestation-verifier"))
		} else if chain, cdBytes, err := buildTestAttestation(mc.VendorID, mc.ProductID); err == nil {
			dacKey, dac, pai, cd = chain.DACKey, chain.DAC, chain.PAI, cdBytes
			logger.Info("matter.bridge.attestation.csa_test",
				slog.String("hint", "CSA Test PAA chain (PAA→PAI→DAC) + signed CD — Apple Home / Google Home / chip-tool accept this without --bypass-attestation-verifier"),
				slog.String("vendor_id", fmt.Sprintf("0x%04X", mc.VendorID)),
				slog.String("product_id", fmt.Sprintf("0x%04X", mc.ProductID)))
		} else {
			logger.Warn("matter.bridge.attestation.csa_test_failed", slog.String("err", err.Error()))
			var derr error
			dacKey, dac, pai, cd, derr = buildDevAttestation(mc.VendorID, mc.ProductID)
			if derr != nil {
				logger.Warn("matter.bridge.attestation.build", slog.String("err", derr.Error()))
			}
			logger.Warn("matter.bridge.attestation.dev",
				slog.String("hint", "ephemeral self-signed DAC fallback — chip-tool must run with --bypass-attestation-verifier true; iPhone / Google Home will reject"))
		}
		opcreds, err := mattercore.NewOperationalCredentials(store, mattercore.OpcredsConfig{
			// SupportedFabrics=0 → OpcredsConfig defaults to spec
			// maximum 254 per Matter §11.18.4.1. The previous cap of 5
			// rejected the 6th admin's AddNOC with TableFull and broke
			// Apple Multi-Admin chains that pair iPad + Apple Home Hub +
			// further controllers.
			SupportedFabrics:         0,
			DACPrivateKey:            dacKey,
			DAC:                      dac,
			PAI:                      pai,
			CertificationDeclaration: cd,
			OnMDNSReannounce: func(ctx context.Context) {
				// Republish the remaining fabric set after RemoveFabric.
				// The stale instance itself is retired by OnFabricWithdraw
				// below — republish alone cannot do that. Mirrors matter.js
				// Fabric.remove() triggering MdnsServer.reannounceInstance.
				if z, ok := adv.(*mdns.Zeroconf); ok {
					z.TriggerReannounce(ctx)
				}
			},
			OnFabricWithdraw: func(ctx context.Context, compressedID [8]byte, nodeID uint64) {
				// Retire the operational instance of a removed fabric (or
				// the old-NodeID instance after UpdateNOC). Mirrors
				// matter.js DeviceAdvertiser.ts:76-86.
				bridge.WithdrawFabric(ctx, compressedID, nodeID)
			},
			OnFabricInstalled: func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, rootPub []byte) {
				// Adopt-Fabric on the invoking session FIRST so the
				// subsequent CommissioningComplete (and any ACL read /
				// group-key write) lands with the right accessing
				// fabric. chip OperationalCredentialsCluster.cpp:510-514
				// calls secureSession->AdoptFabricIndex(newFabricIndex)
				// BEFORE the ACL replace; we mirror the ordering. Apple
				// otherwise aborts the pair with HMMTRErrorDomain Code 9
				// "System Commissioner Pairing — Completing" because
				// CommissioningComplete arrives on the PASE session
				// (FabricIndex=0) and the access check fails.
				if adoptSessionForFabric != nil {
					adoptSessionForFabric(ctx, fabricIndex)
				}
				// Re-arm the GeneralCommissioning fail-safe to point at
				// the freshly-installed fabric. Matter Core §11.18.6.16:
				//
				//   "Successful operation of [AddNOC] SHALL also re-arm
				//   the fail-safe in such a way that the new fabric is
				//   the failsafe fabric. The remaining fail-safe expiry
				//   duration SHALL stay the same."
				//
				// Without this Apple's Multi-Admin pairing fails: Apple
				// arms FailSafe on the primary Hub's CASE session
				// (fabric=1), then AddNOC installs the system-commissioner
				// fabric (fabric=2), then Apple's iCloud Hub opens its
				// own CASE session on fabric 2 and calls
				// CommissioningComplete — our spec-correct fabric-match
				// check (handleCommissioningComplete:409) rejects with
				// InvalidAuthentication "session fabric 2 != failsafe
				// fabric 1" → Apple aborts with HMMTRErrorDomain Code 9
				// ("System Commissioner Pairing — Completing").
				// Mirrors matter.js packages/node/src/behaviors/
				// operational-credentials/OperationalCredentialsServer.ts
				// `#addNoc` → `failSafeContext.assignFabric(...)`.
				gc.SetCurrentFabric(fabricIndex)

				// Re-publish the operational mDNS record under the
				// just-installed fabric+node identity so commissioners
				// can resolve us via DNS-SD for fresh CASE handshakes.
				cid, derr := computeFabricCompressedID(rootPub, fabricID)
				if derr != nil {
					logger.Debug("matter.bridge.fabric.compressed_id",
						slog.Int("fabric_index", int(fabricIndex)),
						slog.String("err", derr.Error()))
					return
				}
				bridge.AnnounceFabric(ctx, cid, nodeID)
				logger.Info("matter.bridge.fabric.installed",
					slog.Int("fabric_index", int(fabricIndex)),
					slog.Uint64("fabric_id", fabricID),
					slog.Uint64("node_id", nodeID))
				// Fan the event out via the bridge so the WS event
				// publisher (wired post-buildRootClusters) sees it.
				bridge.EmitFabricAdded(fabricIndex)
				// Outer scope swaps the CASE responder to the freshly
				// persisted identity so Apple's follow-up Sigma1 lands
				// on a responder that signs Sigma2 with the real NOC.
				if onFabricInstalledExtra != nil {
					onFabricInstalledExtra(ctx, fabricIndex, fabricID, nodeID, rootPub)
				}
			},
		})
		if err != nil {
			logger.Warn("matter.bridge.opcreds.build", slog.String("err", err.Error()))
		} else {
			// Wire the FailSafe accessor so AddNOC, UpdateNOC, and
			// AddTrustedRootCertificate can enforce
			// Status::FailsafeRequired per Matter §11.18.6.4/.8/.9.
			// Without this any CASE session can mutate fabric state
			// without first arming a fail-safe window — chip + matter.js
			// both gate on `failSafeContext.IsFailSafeArmed(...)`.
			opcreds.SetIsFailSafeArmed(gc.FailSafeArmed)
			// Wire the ArmFailSafe-arming hook so every new arm (or
			// re-arm) wipes pending NOC / trust-root state on OpCreds.
			// Mirrors matter.js OperationalCredentialsServer.ts: a
			// FailSafeContext is freshly constructed on each arm, which
			// surfaces a clean `rootCertSet` / `fabricIndex`. Required
			// for Apple's multi-admin "SystemCommissioner" flow — the
			// second pair sequence (CSRRequest → AddTrustedRootCertificate
			// → AddNOC on the same CASE session after the iPhone fabric
			// is installed) needs the `nocWasInvoked` flag reset, else
			// AddTrustedRootCertificate fails with CONSTRAINT_ERROR
			// ("NOC command already invoked in this FailSafe window")
			// and Apple aborts with HMMTRAccessoryPairingStep_System
			// CommissionerPairing_FetchingRootCertificate.
			gcOpCreds := opcreds
			gc.SetOnFailSafeArmed(func(ctx context.Context, _ uint8) {
				gcOpCreds.ClearPendingState()
				_ = ctx
			})
			// Replace the constructor-time OnFailSafeExpired (window-revoke
			// only) with the production-correct hook that also clears
			// OpCreds pending state when the commissioner crashes mid-pair
			// (after ArmFailSafe + AddTrustedRootCertificate + CSRRequest
			// but before AddNOC, OR after AddNOC but before
			// CommissioningComplete). Without ClearPendingState the next
			// pair attempt trips on stale pendingPrivKey / pendingTrustRoot
			// / pendingCSRSessionID / nocWasInvoked and Apple Home aborts
			// the second commissioning with FabricConflict.
			//
			// Mirrors matter.js packages/node/src/behaviors/operational-
			// credentials/OperationalCredentialsServer.ts:#handleFailsafeClosed
			// which fires from fabricManager.events.failsafeClosed after the
			// FailSafeContext is torn down. chip's equivalent is
			// FailSafeContext::Reset() invoked from the expiry timer.
			gc.SetOnFailSafeExpired(func(ctx context.Context, fabricIndex uint8) {
				// OnFailSafeExpiry clears pending commissioning state AND
				// rolls back any half-paired fabric (pendingInstallFabricIndex)
				// that AddNOC committed but CommissioningComplete never confirmed.
				// Mirrors chip CommissioningWindowManager::OnFailSafeTimerExpired.
				gcOpCreds.OnFailSafeExpiry(ctx, fabricIndex)
				if w := bridge.CommissioningWindow(); w != nil {
					_ = w.RevokeWindow(ctx)
				}
				// Clear any still-established PASE session — the failed
				// commissioner's channel must not outlive its fail-safe.
				// Mirrors matter.js FailsafeContext.ts:291 (fail-safe
				// expired → closePaseSession(FailsafeExpiredError)).
				if closePaseSessions != nil {
					if n := closePaseSessions(); n > 0 {
						logger.Info("matter.bridge.failsafe.pase_sessions_closed",
							slog.Int("count", n))
					}
				}
				logger.Info("matter.bridge.failsafe.expired",
					slog.Int("fabric_index", int(fabricIndex)))
			})
			// Replace the constructor-time OnCommissioningComplete (window-revoke
			// only) with a hook that also clears OpCreds pending state — most
			// importantly pendingInstallFabricIndex. Without this reset a
			// subsequent ArmFailSafe → expiry cycle would call revertAddNOC
			// on the already-confirmed fabric and delete its ACL / GroupKey /
			// Fabric row. Mirrors chip FailSafeContext::Reset() on the success
			// path of CommissioningWindowManager::OnCommissioningComplete.
			gc.SetOnCommissioningComplete(func(ctx context.Context, fabricIndex uint8) {
				gcOpCreds.ClearPendingState()
				// Matter §11.10.6.6 step 4: "The Secure Session Context of
				// any PASE session still established at the Server SHALL be
				// cleared." Deferred by a grace period because Apple sends
				// CommissioningComplete over the fabric-adopted PASE session
				// (see AdoptFabricIndex) — an immediate close would drop the
				// CommissioningCompleteResponse before it reaches the wire.
				// Same delayed-teardown pattern as the RemoveFabric session
				// eviction. Mirrors matter.js FailsafeContext.ts:154
				// completeCommission → closePaseSession.
				if closePaseSessions != nil {
					go func() {
						time.Sleep(100 * time.Millisecond)
						if n := closePaseSessions(); n > 0 {
							logger.Info("matter.bridge.commissioning_complete.pase_sessions_closed",
								slog.Int("count", n))
						}
					}()
				}
				w := bridge.CommissioningWindow()
				if w == nil {
					return
				}
				if err := w.RevokeWindow(ctx); err != nil {
					logger.Debug("matter.bridge.commissioning_window.revoke_on_complete",
						slog.Int("fabric_index", int(fabricIndex)),
						slog.String("err", err.Error()))
				}
			})
			// Wire the commissioning-window predicate so ArmFailSafe can
			// distinguish a genuine CASE-steal attempt (FailSafe armed AND
			// window open) from a legitimate re-arm (FailSafe armed, no
			// window). Without this hook the nil-predicate defaults to true
			// and every CASE-fabric re-arm is rejected while another fabric's
			// FailSafe is active — even if no commissioning window is open.
			// Mirrors chip GeneralCommissioningCluster.cpp:419-427.
			gc.SetIsCommissioningWindowOpen(func() bool {
				w := bridge.CommissioningWindow()
				if w == nil {
					return false
				}
				return w.CurrentWindow().Status != matterwire.WindowStatusClosed
			})
			out = append(out, opcreds)
			opCreds = opcreds
		}
		gkm, err := mattercore.NewGroupKeyManagement(store, mattercore.GroupKeyMgmtConfig{})
		if err != nil {
			logger.Warn("matter.bridge.gkm.build", slog.String("err", err.Error()))
		} else {
			out = append(out, gkm)
		}
	}

	// Wire the Root Descriptor's ServerList provider to the assembled
	// cluster-server set. Mirrors matter.js DescriptorServer.#serverList
	// (DescriptorServer.ts:236-244) — the advertised ServerList is the
	// set of mounted behaviors. Closure captures `out` by reference so
	// any conditional append above is reflected. After this point `out`
	// must not be mutated; the bridge consumes the slice as-is.
	mounted := out
	rootDescriptor.SetServerListProvider(func() []uint32 {
		ids := make([]uint32, 0, len(mounted))
		seen := make(map[uint32]struct{}, len(mounted))
		for _, srv := range mounted {
			if srv == nil {
				continue
			}
			id := srv.MatterClusterID()
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids
	})

	return out, opCreds, refs, nil
}

// buildAggregatorClusters constructs the cluster servers the bridge
// mounts on endpoint 1 (Aggregator). matter.js's
// `examples/device-bridge-onoff/src/BridgedDevicesNode.ts` places the
// AggregatorEndpoint on its own endpoint; matter.js's
// `packages/node/src/endpoints/aggregator.ts:60-63` lists Identify and
// Actions as optional. chip's reference bridge-app keeps both on the
// Aggregator (`connectedhomeip/examples/bridge-app/bridge-common/
// bridge-app.matter:2636-2647`).
//
// Returned set:
//
//   - 0x0003 Identify — mirrors chip's bridge-app composition. Apple's
//     HAP-Mapper has empirically required Identify on the Aggregator
//     endpoint to recognise it as a real endpoint and proceed to
//     traverse `Descriptor.PartsList` for bridged accessories.
//     Without it the Mapper has been observed to fall back to
//     `endpointDeviceTypes={0=(22)}` (RootNode only) and never
//     enumerate EP 2+. matter.js conformance lists Identify as
//     optional, so this is a conformance-strict addition not a spec
//     change.
//   - 0x001D Descriptor — DeviceTypeList=[{0x000E, rev 2}],
//     ServerList=[0x0003, 0x001D], PartsList=<dynamic provider,
//     populated by [matterbridge.Bridge.AttachAggregatorPartsListProvider]>.
//
// Actions (0x0025) is intentionally NOT mounted yet. chip's bridge-app
// ships it for the TC-BR test plan; we have no scene/action surface
// to model. Add when the bridge exposes Actions semantics.
func buildAggregatorClusters() ([]interfaces.MatterClusterServer, error) {
	aggregatorIdentify := mattercore.NewIdentify()
	aggregatorDescriptor, err := mattercore.NewDescriptor(
		[]mattercore.DeviceTypeStruct{
			// Matter Device Library §13.2 + matter.js's
			// `AggregatorDt.id = 0xe, revision = 2`.
			{DeviceType: 0x000E, Revision: 2},
		},
		// ServerList is wired below via SetServerListProvider so it
		// stays derived from the actually-mounted set, mirroring the
		// Root Descriptor Bug-K fix. A hardcoded static list here
		// would silently drift the moment Actions (0x0025) or another
		// Aggregator-side cluster is added.
		nil,
		nil, // ClientList — empty
		nil, // PartsList — dynamic, populated by AttachAggregatorPartsListProvider
	)
	if err != nil {
		return nil, fmt.Errorf("aggregator Descriptor: %w", err)
	}
	servers := []interfaces.MatterClusterServer{aggregatorIdentify, aggregatorDescriptor}
	mounted := servers
	aggregatorDescriptor.SetServerListProvider(func() []uint32 {
		ids := make([]uint32, 0, len(mounted))
		seen := make(map[uint32]struct{}, len(mounted))
		for _, srv := range mounted {
			id := srv.MatterClusterID()
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids
	})
	return servers, nil
}

// announcePersistedFabric publishes the operational mDNS record for
// every previously-installed fabric on daemon boot. No-op when the
// matter store has no fabrics, or the lookup fails — commissioners
// fall back to direct-IP pairing in that case.
//
// Multi-Fabric advertise: Apple's Multi-Admin pair installs two fabrics
// (iPhone Home + HomePod/Apple TV Hub) — and a re-pair adds a third
// when the first goes stale. Each fabric's MTRDevice looks up its OWN
// `<compressedFabricID>-<nodeID>._matter._tcp.local` record over
// mDNS for CASE-reconnect. Publishing only the lowest-index fabric
// left every other commissioned controller's discovery empty and
// surfaced as "Bridge does not respond" in the Home App. The
// underlying mDNS advertiser (`zeroconf.Publish`) keys each register
// on `InstanceName + ServiceType`, so calling AnnounceFabric for
// every fabric publishes them all in parallel without overwriting
// each other.
func announcePersistedFabric(ctx context.Context, store *matterstore.Store, bridge *matterbridge.Bridge, logger *slog.Logger) {
	if store == nil || bridge == nil {
		return
	}
	fabrics, err := store.ListFabrics(ctx)
	if err != nil || len(fabrics) == 0 {
		return
	}
	for _, f := range fabrics {
		cid, err := computeFabricCompressedID(f.RootPublicKey, f.FabricID)
		if err != nil {
			logger.Debug("matter.bridge.fabric.compressed_id_boot",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", err.Error()))
			continue
		}
		bridge.AnnounceFabric(ctx, cid, f.NodeID)
	}
}

// computeFabricCompressedID returns the 8-byte CompressedFabricID
// per Matter §4.13.2.4 — HKDF-SHA256(IKM=rootPubKey[1:],
// salt=fabricID-BE, info="CompressedFabric", L=8). Used by the
// daemon's OnFabricInstalled hook to compute the DNS-SD instance
// name for the operational mDNS record.
func computeFabricCompressedID(rootPubKey []byte, fabricID uint64) ([8]byte, error) {
	var out [8]byte
	if len(rootPubKey) != 65 || rootPubKey[0] != 0x04 {
		return out, fmt.Errorf("rootPubKey must be 65-byte uncompressed, got %d", len(rootPubKey))
	}
	salt := []byte{
		byte(fabricID>>56) & 0xFF, byte(fabricID>>48) & 0xFF, byte(fabricID>>40) & 0xFF, byte(fabricID>>32) & 0xFF,
		byte(fabricID>>24) & 0xFF, byte(fabricID>>16) & 0xFF, byte(fabricID>>8) & 0xFF, byte(fabricID) & 0xFF,
	}
	der, err := hkdf.Key(sha256.New, rootPubKey[1:], salt, "CompressedFabric", 8)
	if err != nil {
		return out, err
	}
	copy(out[:], der)
	return out, nil
}

// loadVendorAttestation tries to load DAC/PAI/CD bytes plus the DAC
// private key from the operator-supplied paths. Returns
// (nil-everything, false) when any file is missing or unreadable —
// the caller falls back to [buildDevAttestation]. Returns a
// populated bundle + true on full load success.
//
// Format auto-detection: PEM blocks with `BEGIN CERTIFICATE` /
// `BEGIN EC PRIVATE KEY` / `BEGIN PRIVATE KEY` are decoded via
// `encoding/pem` + `crypto/x509`; raw DER bytes are detected by
// `0x30 0x82` ASN.1 sequence prefix. CD bytes are passed through
// verbatim — chip-tool decodes the CMS structure on its side.
//
// Errors at any step are logged at warn and the function returns
// false; the daemon then prints the dev-attestation hint.
func loadVendorAttestation(cfg config.NorthMatterAttestation, logger *slog.Logger) (key *ecdsa.PrivateKey, dac, pai, cd []byte, ok bool) {
	if cfg.DACPath == "" || cfg.DACKeyPath == "" || cfg.PAIPath == "" || cfg.CDPath == "" {
		return nil, nil, nil, nil, false
	}
	dac, err := readCertBytes(cfg.DACPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.dac", slog.String("path", cfg.DACPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	pai, err = readCertBytes(cfg.PAIPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.pai", slog.String("path", cfg.PAIPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	cd, err = os.ReadFile(cfg.CDPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.cd", slog.String("path", cfg.CDPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	key, err = readECDSAPrivateKey(cfg.DACKeyPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.dac_key", slog.String("path", cfg.DACKeyPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	// Sanity-check: the DAC private key must match the DAC cert's
	// public key. Mismatch silently fails commissioning later (chip-tool
	// signature-verifies the AttestationResponse against the cert), so
	// fail-fast here instead.
	cert, err := x509.ParseCertificate(dac)
	if err != nil {
		logger.Warn("matter.bridge.attestation.dac_parse", slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	pub, ok2 := cert.PublicKey.(*ecdsa.PublicKey)
	//nolint:staticcheck // SA1019: pub.X/Y direct big.Int access kept — Matter wire shape (uncompressed P-256 point, 65 bytes) is checked here, not the modern crypto/ecdh `PublicKey.Bytes()` shape. Migration would force a wire-format audit.
	if !ok2 || pub.X.Cmp(key.X) != 0 || pub.Y.Cmp(key.Y) != 0 {
		logger.Warn("matter.bridge.attestation.key_mismatch",
			slog.String("hint", "DAC certificate public key does not match supplied private key — falling back to dev attestation"))
		return nil, nil, nil, nil, false
	}
	return key, dac, pai, cd, true
}

// readCertBytes reads a certificate file in PEM or raw DER format
// and returns the DER bytes. Auto-detects format by the leading
// bytes (`0x30 0x82` for DER ASN.1; otherwise PEM-decoded).
func readCertBytes(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) >= 2 && raw[0] == 0x30 && raw[1] == 0x82 {
		return raw, nil // DER
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return block.Bytes, nil
}

// readECDSAPrivateKey reads a PKCS#8 or SEC1 P-256 ECDSA private
// key from `path`. PEM and DER both accepted via the same auto-
// detect logic as [readCertBytes].
func readECDSAPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	der := raw
	if len(raw) < 2 || raw[0] != 0x30 || raw[1] != 0x82 {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("no PEM block in %s", path)
		}
		der = block.Bytes
	}
	// Try PKCS#8 first (most modern tooling default).
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s: PKCS#8 key is not ECDSA", path)
		}
		return ec, nil
	}
	// Fall back to SEC1 (`BEGIN EC PRIVATE KEY`).
	return x509.ParseECPrivateKey(der)
}

// buildTestAttestation produces a fresh PAI + DAC chain rooted at the
// embedded CSA Test PAA, plus a CMS-signed Certification Declaration
// for the given (vendor, product) pair. This is the default the daemon
// presents when no operator-supplied attestation bundle is configured —
// matter.js follows the exact same pattern, and Apple Home, Google
// Home, and chip-tool all accept it without `--bypass-attestation`.
func buildTestAttestation(vendorID, productID uint16) (*attestation.Chain, []byte, error) {
	chain, err := attestation.BuildTestChain(vendorID, productID)
	if err != nil {
		return nil, nil, fmt.Errorf("build CSA test chain: %w", err)
	}
	cd, err := attestation.BuildTestCertificationDeclaration(vendorID, productID)
	if err != nil {
		return nil, nil, fmt.Errorf("build CSA test CD: %w", err)
	}
	return chain, cd, nil
}

// buildDevAttestation constructs an ephemeral DAC key + a
// self-signed DAC-shaped X.509 certificate. The PAI is the same
// cert (acts as its own intermediate; chip-tool's
// `--bypass-attestation-verifier true` skips chain validation). The
// CD is an empty placeholder — production deployments must wire
// CSA-signed CD bytes via config. Retained as the last-ditch fallback
// when even the CSA test chain fails to build (should never happen in
// practice — keeps the bridge bootable for diagnostic purposes).
//
// Returned material is cryptographically valid (chip-tool's
// AttestationResponse signature decodes), but NOT trusted by
// any production commissioner. Operators that ship a real device
// must replace via configuration.
func buildDevAttestation(vendorID, productID uint16) (dacKey *ecdsa.PrivateKey, dac, pai, cd []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dev DAC key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: fmt.Sprintf("Matter Dev DAC 0x%04X-0x%04X", vendorID, productID),
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(20 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dev DAC cert: %w", err)
	}
	// Use the same cert for DAC + PAI in dev mode; real deployments
	// would generate a chain (PAA → PAI → DAC) per Matter §6.2.
	return priv, der, der, []byte{}, nil
}

// buildPaseAdapter constructs the Spake2+ verifier the bridge's PASE
// port consumes and wires its session-pickup callback to
// [operational.Manager.OpenFromPase] so a successful Pake3 lands a
// fresh session in the operational manager.
//
// The returned adapter wraps one shared Verifier, so concurrent PASE
// attempts would collide on its state. Production does not use it that
// way: matterbridge.PerExchangePaseProvider builds a fresh adapter per
// exchange, and that is what the concurrent-pairings path installs.
// rotatingSerialPart mirrors the SerialNumber derivation in
// [rootSerial] so the Rotating Device Identifier draws on the same
// stable bridge identity tuple — keeps the commissionable `RI` TXT
// key consistent across daemon restarts without an extra
// persistence slot.
func rotatingSerialPart(vendorID, productID uint16, nodeLabel string) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%04X|%04X|%s|serial", vendorID, productID, nodeLabel))
	return hex.EncodeToString(h[:8])
}

func buildPaseAdapter(cfg config.NorthMatterCommissioning, mgr *operational.Manager, opCreds *mattercore.OperationalCredentials, gc *mattercore.GeneralCommissioning, logger *slog.Logger) (*matterbridge.PaseAdapter, error) {
	salt := []byte(cfg.Salt)
	if len(salt) == 0 {
		// Fixed development salt — never ship without setting Salt.
		salt = []byte("openccu-loom-dev0")
	}
	iterations := cfg.Iterations
	if iterations == 0 {
		iterations = 1000
	}
	return buildPaseAdapterFromCreds(cfg.Passcode, salt, iterations, mgr, opCreds, gc, logger)
}

// buildPaseAdapterFromCreds is the shared helper that turns a
// passcode + salt + iteration count into a fully wired
// [matterbridge.PaseAdapter]. Used by both the configured-mode build
// (read from `north.matter.commissioning.*`) and the ephemeral
// per-window provider (random passcode + salt regenerated per
// OpenCommissioningWindow call).
func buildPaseAdapterFromCreds(passcode uint32, salt []byte, iterations int, mgr *operational.Manager, opCreds *mattercore.OperationalCredentials, gc *mattercore.GeneralCommissioning, logger *slog.Logger) (*matterbridge.PaseAdapter, error) {
	// Reject trivially-guessable and out-of-range passcodes before deriving
	// the SPAKE2+ verifier. Mirrors chip src/crypto/CHIPCryptoPAL.cpp
	// IsValidSetupPIN which chip-tool calls at commissioning start.
	if !mattersetup.IsValidSetupPIN(passcode) {
		return nil, fmt.Errorf("matter: PASE passcode %d is invalid or trivially guessable", passcode)
	}
	vc, err := spake2.NewVerifierContext(passcode, salt, iterations)
	if err != nil {
		return nil, fmt.Errorf("spake2 verifier context: %w", err)
	}
	return buildPaseAdapterFromContext(vc, salt, iterations, mgr, opCreds, gc, logger)
}

// buildPaseAdapterFromVerifier builds a PASE adapter from a Matter
// §3.10.5 passcode verifier (w0 || L) instead of a passcode. This is the
// Enhanced Commissioning Window path: a multi-admin commissioner derives
// the verifier from a passcode it chose and supplies only (w0, L) in
// OpenCommissioningWindow, so the bridge accepts PASE against that
// passcode without ever seeing it. Mirrors matter.js
// PaseServer.fromVerificationValue
// (packages/protocol/src/session/pase/PaseServer.ts:52-61).
// gc is intentionally omitted: the Enhanced Commissioning Window is
// driven by a commissioner that sends its own ArmFailSafe, so the
// defensive AutoArmOnPaseEstablished hook (which needs GeneralCommissioning)
// is not wired on this path — matching the ephemeral provider, which also
// passes a nil gc.
func buildPaseAdapterFromVerifier(verifier, salt []byte, iterations int, mgr *operational.Manager, opCreds *mattercore.OperationalCredentials, logger *slog.Logger) (*matterbridge.PaseAdapter, error) {
	const want = spake2.VerifierW0Size + spake2.VerifierLSize // 97 bytes, Matter §3.10.5
	if len(verifier) != want {
		return nil, fmt.Errorf("matter: PAKE passcode verifier length=%d, want %d", len(verifier), want)
	}
	vc, err := spake2.NewVerifierFromValue(verifier[:spake2.VerifierW0Size], verifier[spake2.VerifierW0Size:])
	if err != nil {
		return nil, fmt.Errorf("spake2 verifier from value: %w", err)
	}
	return buildPaseAdapterFromContext(vc, salt, iterations, mgr, opCreds, nil, logger)
}

// buildPaseAdapterFromContext wires a fully-configured [matterbridge.PaseAdapter]
// around a prepared SPAKE2+ verifier context — the shared tail of
// buildPaseAdapterFromCreds (passcode path) and buildPaseAdapterFromVerifier
// (supplied-verifier / Enhanced Commissioning Window path).
func buildPaseAdapterFromContext(vc *spake2.VerifierContext, salt []byte, iterations int, mgr *operational.Manager, opCreds *mattercore.OperationalCredentials, gc *mattercore.GeneralCommissioning, logger *slog.Logger) (*matterbridge.PaseAdapter, error) {
	// Pre-allocate the operational session id BEFORE the adapter is
	// constructed. The id is embedded in PBKDFParamResponse as
	// ResponderSessionID; the commissioner echoes it back as
	// Header.SessionID on every post-PASE-establishment datagram
	// (IM reads, writes, invokes). Without pre-allocation the bridge
	// sends a fixed id (e.g. 1) in the response but registers the
	// actual session under a different dynamically-chosen id — the
	// inbound session lookup then misses, the datagram is dropped,
	// and chip-tool retransmits until max-retries. Mirrors the CASE
	// responder path which calls AllocateID before building Sigma2.
	sessID, err := mgr.AllocateID()
	if err != nil {
		return nil, fmt.Errorf("allocate PASE session id: %w", err)
	}
	// Use the factory variant so each Pake1 starts with a fresh
	// verifier whose context is bound to the actual PBKDFParam wire
	// bytes that crossed the channel (Matter §4.13.4). Empty
	// idA/idB per §3.10.4. The `context` argument the adapter
	// passes here is already the SPAKE2+ context HASH (32 bytes),
	// not the literal "CHIP PAKE V1 Commissioning" string.
	paseAdapter := matterbridge.NewPaseAdapterWithFactory(func(context []byte) *spake2.Verifier {
		return spake2.NewVerifier(vc, nil, nil, context)
	})
	// Wire the PBKDF response config so the adapter can answer
	// commissioner PBKDFParamRequest before Pake1 starts. Use the
	// pre-allocated sessID as ResponderSessionID so the commissioner
	// echoes back the correct id on post-PASE IM traffic.
	paseAdapter.SetPBKDFParams(uint32(iterations), salt, sessID) //nolint:gosec // iterations was validated by NewVerifierContext; see #20
	// Advertise the bridge's MRP profile as PBKDFParamResponse tag 5 —
	// the same idle/active/threshold triplet Sigma2 tag 5 and the mDNS
	// SII/SAI keys carry. Mirrors matter.js PaseServer.ts:151.
	sp := bridgeSessionParameters()
	idle := uint16(sp.SessionIdleInterval)     //nolint:gosec // bridgeSessionParameters values are spec defaults ≤ 4000
	active := uint16(sp.SessionActiveInterval) //nolint:gosec // see above
	thresh := sp.SessionActiveThreshold
	paseAdapter.SetResponderMRPParams(&spake2.MRPParameters{
		IdleRetransTimeoutMs:   &idle,
		ActiveRetransTimeoutMs: &active,
		ActiveThresholdTimeMs:  &thresh,
	})
	paseAdapter.SetOnSessionEstablished(func(sharedSecret []byte, peerSessionID uint16) error {
		// Pickup marker: this fires after a successful Pake3
		// verification but before the session is registered. Paired
		// with the closing session_established log it brackets the
		// pickup so a stall inside is attributable from the log alone.
		logger.Debug("matter.bridge.pase.session_pickup",
			slog.Int("peer_session_id", int(peerSessionID)))
		// PASE pre-dates the operational fabric; both node ids ride
		// as the bridge-allocated PASE-temporary values per Matter
		// §4.13.2 (random ephemerals). Use 0/0 here.
		// Register the session under the pre-allocated sessID so the
		// inbound session lookup finds it when the commissioner sends
		// encrypted IM traffic with Header.SessionID==sessID.
		entry, err := mgr.OpenFromPaseWithID(sessID, 0, 0, peerSessionID, sharedSecret)
		if err != nil {
			mgr.ReleaseID(sessID)
			return err
		}
		logger.Debug("matter.bridge.pase.session_open_ok",
			slog.Int("session_id", int(entry.SessionID)))
		// Honour the commissioner's InitiatorMRPParams (tag 5) so
		// outbound retransmissions on the PASE session use the peer's
		// advertised intervals. Mirrors matter.js PaseServer.ts:155-157
		// `session.timingParameters = initiatorSessionParams`.
		if pm := paseAdapter.PeerMRPParams(); pm != nil {
			var idleMs, activeMs, threshMs uint32
			if pm.IdleRetransTimeoutMs != nil {
				idleMs = uint32(*pm.IdleRetransTimeoutMs)
			}
			if pm.ActiveRetransTimeoutMs != nil {
				activeMs = uint32(*pm.ActiveRetransTimeoutMs)
			}
			if pm.ActiveThresholdTimeMs != nil {
				threshMs = uint32(*pm.ActiveThresholdTimeMs)
			}
			entry.SetPeerMRPIntervals(idleMs, activeMs, threshMs)
		}
		// Plumb the AttestationChallenge into OperationalCredentials
		// so AttestationRequest / CSRRequest signatures bind to the
		// session per Matter §11.18.4.7. opCreds is nil when the
		// matter store is missing; in that case attestation falls
		// through to the all-zero stub.
		if opCreds != nil {
			opCreds.SetAttestationChallenge(entry.AttestationChallenge)
		}
		// Arm a 60-second FailSafe when no prior ArmFailSafe arrived.
		// Matter §11.10.5.2 sets the default expiry floor at 60 s;
		// the auto-arm ensures the fail-safe expiry hook fires and
		// cleans up any uncommitted state if the commissioner disappears
		// without calling CommissioningComplete.
		if gc != nil {
			gc.AutoArmOnPaseEstablished(context.Background())
		}
		logger.Info("matter.bridge.pase.session_established",
			slog.Int("session_id", int(entry.SessionID)))
		return nil
	})
	return paseAdapter, nil
}

// --- commissioning-window hook adapters ----------------------------------

// matterWindowTransitionHook returns the [matterbridge.CommissioningWindow]
// transition callback. On a transition into an Enhanced Commissioning Window
// opened with a commissioner-supplied verifier it advertises the window over
// mDNS with CM=2 + the ECW discriminator (so a second controller can
// discover the multi-admin window); on a transition into the closed state it
// withdraws the commissionable record. An Enhanced window opened via the
// REST opener (its own passcode, CM=1) carries no supplied verifier and is
// left to the opener's own announce. `enhancedAdvert` is the CM=2 template;
// the discriminator is stamped per fire. Mirrors matter.js
// DeviceCommissioner.ts:166 (enterCommissioningMode with Enhanced).
func matterWindowTransitionHook(mb *matterbridge.Bridge, window *matterbridge.CommissioningWindow, enhancedAdvert matterbridge.CommissioningAdvertisement, logger *slog.Logger) func() {
	return func() { //nolint:contextcheck // hook fires asynchronously on window state change; no caller ctx is available
		if mb == nil {
			return
		}
		snap := window.CurrentWindow()
		if snap.Status == matterwire.WindowStatusEnhanced && window.HasSuppliedVerifier() {
			adv := enhancedAdvert
			adv.Discriminator = window.Discriminator()
			if err := mb.AnnounceCommissioning(context.Background(), adv); err != nil {
				logger.Warn("matter.bridge.commissioning.enhanced_announce", slog.String("err", err.Error()))
			}
			return
		}
		if snap.Status != matterwire.WindowStatusEnhanced {
			mb.WithdrawCommissioning(context.Background())
		}
	}
}

// failSafeArmerAdapter wires the Matter fail-safe arm hook to
// GeneralCommissioning. Matter §11.19.6 mandates that
// OpenCommissioningWindow arm the fail-safe for the window duration.
//
// Mirrors matter.js packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:openCommissioningWindow → call to
// GeneralCommissioningBehavior.armFailSafeLogic.
type failSafeArmerAdapter struct {
	gc     *mattercore.GeneralCommissioning
	logger *slog.Logger
}

func (a *failSafeArmerAdapter) ArmFailSafeFor(ctx context.Context, seconds uint32, fabricIndex uint8) error {
	// Delegates to GeneralCommissioning.ArmFailSafeFor which arms the
	// timer per Matter §11.19.6. Mirrors matter.js
	// packages/node/src/behaviors/administrator-commissioning/
	// AdministratorCommissioningServer.ts:openCommissioningWindow →
	// GeneralCommissioningBehavior.armFailSafeLogic(timeoutSeconds) and
	// chip CommissioningWindowManager.cpp ArmFailSafe() call.
	if a.gc == nil {
		a.logger.Warn("failsafe.arm.skipped",
			slog.String("reason", "gc_nil"),
			slog.Int("seconds", int(seconds)),
			slog.Int("fabric_index", int(fabricIndex)))
		return nil
	}
	a.logger.Debug("failsafe.arm",
		slog.Int("seconds", int(seconds)),
		slog.Int("fabric_index", int(fabricIndex)))
	return a.gc.ArmFailSafeFor(ctx, seconds, fabricIndex)
}

// paseSessionCloserAdapter wires the PASE-session eviction hook to the
// operational session manager. Matter §11.19.7.3 step 1 mandates closing
// any open PASE session when the commissioning window is revoked.
//
// PASE sessions are stored with FabricIndex == 0 (pre-fabric, per Matter
// §4.13.2 / operational.Manager.OpenFromPase). CloseFabric(0) terminates
// all of them atomically.
//
// Mirrors matter.js packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:revokeCommissioning → call to
// paseCommissioner.close().
type paseSessionCloserAdapter struct {
	opMgr  *operational.Manager
	logger *slog.Logger
}

func (a *paseSessionCloserAdapter) ClosePaseSessions(_ context.Context) error {
	if a.opMgr == nil {
		a.logger.Debug("pase.close.skipped", slog.String("reason", "no_op_mgr"))
		return nil
	}
	// FabricIndex == 0 is the pre-fabric PASE bucket; CloseFabric closes
	// all sessions registered under that fabric index atomically.
	a.opMgr.CloseFabric(0)
	a.logger.Debug("pase.sessions.closed", slog.String("reason", "window_revoke"))
	return nil
}

// startMDNSAdvertiser parses the REST listen address, builds the
// daemon-discovery TXT bundle, and starts a multicast advertiser.
// Returns (nil, nil) when the listen address has no usable port
// (Unix socket etc.) so the caller can skip without an error path.
// Returns (nil, err) when the port is malformed or zeroconf fails to
// register; the caller is expected to log and continue (mDNS is a
// convenience, not a hard dependency).
func startMDNSAdvertiser(ctx context.Context, cfg *config.Config, reg *central.Registry, logger *slog.Logger) (discoverymdns.Advertiser, error) {
	svc, ok := mdnsServiceFor(cfg, len(reg.Names()), mdnsCCUSerials(reg))
	if !ok {
		return nil, nil
	}
	adv := discoverymdns.NewMulticast(svc)
	if err := adv.Start(ctx); err != nil {
		return nil, err
	}
	logger.Info("discovery.mdns.started",
		slog.String("service_type", discoverymdns.ServiceType),
		slog.Int("port", svc.Port))
	return adv, nil
}

// mdnsServiceFor builds the mDNS service advertised for client
// auto-discovery from the daemon config. Returns ok=false when the
// REST listen address carries no usable port (mDNS is then skipped).
//
// The TXT bundle gives a discovering client everything it needs to
// reach and label the daemon before authenticating: the API mount
// path, the wire-contract version (mirrors GET /info), whether the
// daemon's own listener is TLS (always 0 — TLS is terminated upstream
// by a reverse proxy / HA ingress), the friendly instance label, a
// pre-auth hint of how many CCUs this daemon serves, and — per
// ADR 0058 — the resolved CCU serial suffixes (`ccus=`). Serials
// resolve during the readiness-gated bring-up and change on live
// adopt, so the record is re-announced at runtime via
// [discoverymdns.Advertiser.UpdateTXT]; `GET /api/v1/system/ccu`
// stays the authoritative post-auth source.
func mdnsServiceFor(cfg *config.Config, centralCount int, ccuSerials []string) (discoverymdns.Service, bool) {
	port, ok := splitListenPort(cfg.North.REST.Listen)
	if !ok {
		return discoverymdns.Service{}, false
	}
	return discoverymdns.Service{
		InstanceName: cfg.North.Discovery.MDNS.InstanceName,
		Port:         port,
		TXT:          mdnsTXT(cfg, centralCount, ccuSerials),
	}, true
}

// mdnsTXT assembles the TXT bundle. Shared by the initial Register and
// every runtime re-announce so both paths stay identical.
func mdnsTXT(cfg *config.Config, centralCount int, ccuSerials []string) []string {
	txt := []string{
		"path=/api/v1",
		"api_version=" + handlers.APIVersion,
		"tls=0",
		"instance=" + cfg.North.Discovery.MDNS.ResolveInstanceName(),
		"centrals=" + strconv.Itoa(centralCount),
	}
	if v := mdnsCCUsValue(ccuSerials); v != "" {
		txt = append(txt, "ccus="+v)
	}
	return txt
}

// mdnsCCUsValue joins the resolved serial suffixes sorted and
// comma-separated, dropping whole entries once the TXT string would
// exceed the DNS 255-byte-per-string limit (an invalid record helps
// nobody; the list is best-effort by contract, ADR 0058).
func mdnsCCUsValue(serials []string) string {
	const maxLen = 255 - len("ccus=")
	sorted := make([]string, 0, len(serials))
	for _, sn := range serials {
		if sn != "" {
			sorted = append(sorted, sn)
		}
	}
	sort.Strings(sorted)
	out := ""
	for _, sn := range sorted {
		next := sn
		if out != "" {
			next = out + "," + sn
		}
		if len(next) > maxLen {
			break
		}
		out = next
	}
	return out
}

// mdnsCCUSerials collects the canonical (last-10, case-preserved) serial of
// every registered central; unresolved centrals are skipped and appear on a
// later re-announce.
//
// The value must be the exact string GET /system/ccu reports: the Home
// Assistant integration de-dupes discovery by matching each serial in the
// mDNS ccus= TXT against the config entry's unique_id (the /system/ccu
// serial) with a case-sensitive string compare. The lower-cased routing form
// ([Registry.SerialSuffix]) never matches an upper-case-hex CCU serial, so HA
// re-discovers an already-integrated daemon on every restart.
func mdnsCCUSerials(reg *central.Registry) []string {
	if reg == nil {
		return nil
	}
	names := reg.Names()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if sn := reg.CanonicalSerial(name); sn != "" {
			out = append(out, sn)
		}
	}
	return out
}

// matterWiring carries the Matter REST/WS-facing adapters produced by
// wireMatterRuntime. All fields are nil when the bridge is disabled.
type matterWiring struct {
	fabricStore       handlers.MatterFabricStore
	sessionLister     handlers.MatterSessionLister
	mdnsReporter      handlers.MatterMdnsReporter
	endpointInspector handlers.MatterEndpointInspector
	compatReporter    handlers.MatterCompatibilityReporter
	diagEvents        handlers.MatterDiagnosticEventReporter
	opener            handlers.MatterCommissioningOpener
	statusReader      handlers.MatterStatusReader
	fabricRevoker     handlers.MatterFabricRevoker
	fabricPurger      handlers.MatterFabricPurger
	closer            handlers.MatterCommissioningCloser
	// exposureStore is the concrete allowlist store rather than the narrower
	// handlers.MatterExposureStore port: the live-adopt orchestrator also
	// needs DeleteForCentral (matterExposureStore/setMatterExposureStore in
	// central_adopt.go), which that REST-facing interface does not declare.
	// It still satisfies handlers.MatterExposureStore at every assignment
	// site below.
	exposureStore *matterstore.Store
	candidates    handlers.MatterCandidateProvider
	pub           *matterEventPublisher
	reassembler   handlers.MatterTopologyReassembler
	bi            *mattercore.BasicInformation
	// centralHook wires a runtime-adopted central into the running bridge
	// (reassemble-on-ready, hot-plug lifecycle, reachable forward). Nil when
	// the bridge is disabled; the live-adopt orchestrator skips it then. The
	// model-complete latch is not part of it — that rides a registry
	// observer, which reaches boot-time and adopted centrals alike.
	centralHook matterCentralHook
}

// matterReassembleReadyDebounce coalesces a burst of CentralSouthboundReadyEvents
// (staggered multi-CCU bring-ups) into a single topology reassemble.
const matterReassembleReadyDebounce = 750 * time.Millisecond

// matterSessionReapInterval is the sweep cadence of the operational
// idle-session reaper (see the StartReaper call in startMatterBridge).
// idleTimeout/poll ≈ 5:1 keeps eviction latency bounded at TTL + 60 s
// while the sweep itself stays negligible.
const matterSessionReapInterval = 60 * time.Second

// wireMatterReassembleOnReady rebuilds the Matter bridge topology once each
// central's southbound bring-up has completed. The topology is first assembled
// at daemon start — before the async CCU device load finishes — so without this
// the bridge stays empty of bridged endpoints (and the commissioning window is
// refused) until an operator toggles an exposure, even though exposures are
// already enabled. Subscribing to CentralSouthboundReadyEvent closes that gap:
// the persisted exposures take effect automatically once the devices load.
//
// The event handler only signals (non-blocking) so it never blocks the
// serialized bus dispatch; the actual Reassemble runs on a dedicated debounce
// goroutine that stops when ctx is cancelled. Returns the subscription closers
// plus the non-blocking trigger, so the live-adopt hook can subscribe a
// runtime-added central's bus onto the same debounce pipeline later (see
// [newMatterCentralHook]). The debounce goroutine starts whenever reassemble
// is non-nil — even with zero boot-time buses — because a daemon can boot with
// no configured centrals and adopt its first one at runtime.
func wireMatterReassembleOnReady(
	ctx context.Context,
	buses []*events.Bus,
	reassemble func(context.Context) error,
	debounce time.Duration,
	logger *slog.Logger,
) (closers []func(), trigger func()) {
	if reassemble == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	signal := make(chan struct{}, 1)
	trigger = func() {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	for _, bus := range buses {
		if unsub := subscribeMatterReadyTrigger(bus, trigger); unsub != nil {
			closers = append(closers, unsub)
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-signal:
			}
			// Debounce: wait for a quiet window so a staggered multi-CCU boot
			// coalesces into one reassemble instead of one per central.
			for settling := true; settling; {
				select {
				case <-ctx.Done():
					return
				case <-signal:
				case <-time.After(debounce):
					settling = false
				}
			}
			runMatterReassemble(ctx, reassemble, logger)
		}
	}()
	return closers, trigger
}

// subscribeMatterReadyTrigger subscribes trigger to bus's
// CentralSouthboundReadyEvent. Shared by the boot-time wiring
// ([wireMatterReassembleOnReady]) and the live-adopt hook so both feed the
// same debounce pipeline. Returns nil when there is nothing to wire.
func subscribeMatterReadyTrigger(bus *events.Bus, trigger func()) func() {
	if bus == nil || trigger == nil {
		return nil
	}
	return events.Subscribe(bus, func(hmevent.CentralSouthboundReadyEvent) {
		trigger()
	})
}

// subscribeMatterDeviceLifecycleTrigger feeds device create/remove events
// into the same debounced reassemble pipeline: a device hot-plugged (or
// deleted) after its central's bring-up must surface as a bridged
// endpoint without a daemon restart — Reassemble handles endpoint add,
// GC and the PartsList subscription notification. Events arriving before
// the central is southbound-ready are skipped: the boot ingest fires one
// DeviceCreatedEvent per device and the ready trigger already covers
// that batch with a single reassemble. Create events are at-least-once
// (the CCU re-announces its inventory on reconnect); the debounce
// coalesces such bursts and a no-change reassemble is cheap in-memory
// work. Returns the unsubscribe closers (empty when nothing to wire).
func subscribeMatterDeviceLifecycleTrigger(u *central.Unit, trigger func()) []func() {
	if u == nil || u.EventBus == nil || trigger == nil {
		return nil
	}
	fire := func() {
		if u.IsSouthboundReady() {
			trigger()
		}
	}
	return []func(){
		events.Subscribe(u.EventBus, func(hmevent.DeviceCreatedEvent) { fire() }),
		events.Subscribe(u.EventBus, func(hmevent.DeviceRemovedEvent) { fire() }),
		// A rename is a topology change too, even though the set of devices is
		// unchanged: the accessory's NodeLabel is built from the device's name,
		// so without this a device renamed in the CCU WebUI keeps its old name
		// in Apple Home and Google Home until the daemon restarts. MQTT and the
		// WebSocket already learn about it from the same event.
		events.Subscribe(u.EventBus, func(hmevent.DeviceMetadataChangedEvent) { fire() }),
		// A release adds a device to the bridged set without changing the
		// model: it was materialised long ago and only the wizard's last
		// step made it publishable. Without this it reaches no controller
		// until the daemon restarts.
		events.Subscribe(u.EventBus, func(hmevent.DeviceReleasedEvent) { fire() }),
	}
}

// runMatterReassemble invokes reassemble with panic isolation so a fault in the
// topology build cannot crash the daemon from the debounce goroutine.
func runMatterReassemble(ctx context.Context, reassemble func(context.Context) error, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("matter.reassemble_on_ready.panic", slog.Any("panic", r))
		}
	}()
	if err := reassemble(ctx); err != nil {
		logger.Warn("matter.reassemble_on_ready.failed", slog.String("err", err.Error()))
	}
}

// matterCentralReadiness latches which centrals have completed their initial
// southbound bring-up (the readiness-gated CCU device load). The Matter
// snapshotter consults it to stamp [endpoint.Snapshot.ModelComplete] so the
// assembler's vanished-source GC only trusts centrals whose ModelRegistry is
// authoritative — a central still waiting on its CCU must keep its persisted
// endpoint-ID rows (see [endpoint.Snapshot.ModelComplete]).
//
// Ready survives mid-life CCU reconnects — the loaded model does too, so the
// registry view stays authoritative once the initial load has completed. It is
// dropped in exactly one place: when a central leaves the registry (see
// [wireMatterCentralReadiness]), because the next unit registered under that
// name starts with an empty model again. Safe for concurrent use — the snapshotter
// reads from the bridge's assembly path while the event-bus dispatch writes.
type matterCentralReadiness struct {
	mu    sync.RWMutex
	ready map[string]struct{}
}

func newMatterCentralReadiness() *matterCentralReadiness {
	return &matterCentralReadiness{ready: make(map[string]struct{})}
}

// markReady latches centralName as model-complete.
func (r *matterCentralReadiness) markReady(centralName string) {
	r.mu.Lock()
	r.ready[centralName] = struct{}{}
	r.mu.Unlock()
}

// clearReady drops centralName's latch, returning it to model-incomplete.
// A nil receiver is a no-op so the teardown path stays unconditional.
func (r *matterCentralReadiness) clearReady(centralName string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.ready, centralName)
	r.mu.Unlock()
}

// isReady reports whether centralName has completed its initial device load.
func (r *matterCentralReadiness) isReady(centralName string) bool {
	r.mu.RLock()
	_, ok := r.ready[centralName]
	r.mu.RUnlock()
	return ok
}

// wireMatterCentralReadiness latches per-central readiness for every central
// the registry carries — the ones already registered and every one registered
// later — and returns the tracker plus the observer removal.
//
// It is a registry observer and not a walk because the latch is the one piece
// of per-central Matter state that does NOT ride the central's own EventBus:
// Unit.Stop drops the subscription, nothing drops the latch. A central removed
// at runtime therefore left it set, and the next unit registered under that
// name starts with an empty ModelRegistry — the stale latch stamps that empty
// snapshot ModelComplete, the assembler reads every persisted endpoint of the
// central as vanished and deletes it, and the refill renumbers the whole fleet
// so controllers lose every accessory of that CCU. [central.Registry.Unregister]
// runs the observer's unwire, so a boot-registered and a runtime-adopted
// central clear the latch through the same path.
//
// Failure direction is deliberate: a central whose ready event is never
// observed stays "model-incomplete", which only defers the assembler's
// vanished-source GC for that central — persisted endpoint IDs are never
// deleted on stale information.
func wireMatterCentralReadiness(reg *central.Registry) (readiness *matterCentralReadiness, remove func()) {
	readiness = newMatterCentralReadiness()
	if reg == nil {
		return readiness, func() {}
	}
	return readiness, reg.OnRegisterDeclared(wiring.Seam{
		Name:         "matter.central_readiness",
		Collaborator: "*matterCentralReadiness",
		Phase:        wiring.PhasePerCentral,
		Why:          "no central is ever latched model-complete, so every bridge snapshot stays incomplete and endpoint garbage collection never runs",
	}, func(u *central.Unit) func() {
		unsub := wireMatterCentralReadinessForUnit(readiness, u)
		name := u.Name()
		return func() {
			if unsub != nil {
				unsub()
			}
			readiness.clearReady(name)
		}
	})
}

// wireMatterCentralReadinessForUnit latches one central's readiness into the
// tracker: it subscribes to the unit's CentralSouthboundReadyEvent for
// go-forward transitions, then seeds from the unit's queryable latched flag
// ([central.Unit.IsSouthboundReady]). The seed closes the boot-window race:
// southbound bring-up goroutines start before the Matter bridge wires its
// subscriptions, so a fast CCU (or a runtime-adopted central) can fire its
// ready event before this subscription exists — the event never re-fires, and
// without the seed the central would stay model-incomplete (GC deferred) for
// the whole process lifetime. Seeding AFTER subscribing means every
// interleaving is covered: a pre-subscription event is caught by the seed, a
// post-seed event by the subscription, and one in between by both.
func wireMatterCentralReadinessForUnit(readiness *matterCentralReadiness, u *central.Unit) func() {
	if readiness == nil || u == nil || u.EventBus == nil {
		return nil
	}
	unsub := events.Subscribe(u.EventBus, func(e hmevent.CentralSouthboundReadyEvent) {
		readiness.markReady(e.CentralName)
	})
	if u.IsSouthboundReady() {
		readiness.markReady(u.Name())
	}
	return unsub
}

// matterSnapshotter builds the bridge's topology snapshotter: one
// [endpoint.DeviceSnapshot] per registered central, read live from the central
// registry so runtime-added centrals surface on the next assembly. Each
// snapshot's ModelComplete flag is stamped from the readiness latch; a nil
// readiness marks every central model-incomplete (the GC-off fail-safe).
func matterSnapshotter(reg *central.Registry, readiness *matterCentralReadiness) matterbridge.Snapshotter {
	return func(_ context.Context) []endpoint.DeviceSnapshot {
		var out []endpoint.DeviceSnapshot
		for _, u := range reg.List() {
			if u == nil || u.ModelRegistry == nil {
				continue
			}
			out = append(out, endpoint.DeviceSnapshot{
				CentralName:   u.Name(),
				Devices:       releasedDevicesOf(u),
				ModelComplete: readiness != nil && readiness.isReady(u.Name()),
			})
		}
		return out
	}
}

// releasedDevicesOf is the central's model minus the devices the
// onboarding wizard has not released yet.
//
// A withheld device is fully materialised — the wizard needs its ise_id
// and channels to configure it — so it would otherwise be assembled into
// a bridged endpoint and appear on every commissioned Matter controller
// under whatever name it was paired with. Endpoint ids are assigned in
// assembly order and persisted, so publishing early and renaming later
// is not a cosmetic mistake: the controller keeps the first identity it
// saw.
func releasedDevicesOf(u *central.Unit) []*device.Device {
	all := u.ModelRegistry.List()
	if u.Devices == nil {
		return all
	}
	out := make([]*device.Device, 0, len(all))
	for _, d := range all {
		if d == nil {
			continue
		}
		if !u.Devices.IsReleased(hmtypes.ParseWireInterfaceID(d.InterfaceID), d.Address) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// wireMatterDeviceReachableForward forwards one central's device-availability
// lifecycle signal into the bridge so the matching bridged endpoints fire the
// §9.13.6 ReachableChanged event (see [matterbridge.Bridge.NotifyDeviceReachable]
// for the full rationale). Shared by the boot-time wiring and the live-adopt
// hook. Returns nil when there is nothing to wire.
func wireMatterDeviceReachableForward(u *central.Unit, notify func(centralName, address string, reachable bool)) func() {
	if u == nil || u.EventBus == nil || notify == nil {
		return nil
	}
	cName := u.Name()
	return events.Subscribe(u.EventBus, func(e hmevent.DeviceLifecycleEvent) {
		if e.Subtype != hmenum.DeviceLifecycleSubtypeAvailabilityChanged {
			return
		}
		notify(cName, e.Address, e.Available)
	})
}

// matterCentralHook wires one central into the running Matter bridge:
// reassemble-on-ready onto the shared debounce pipeline, hot-plug device
// lifecycle, and the device-availability →
// BridgedDeviceBasicInformation.Reachable forward. The live-adopt
// orchestrator invokes it for runtime-added centrals so an adopted central is
// wired into the bridge exactly like a boot-time one; the returned unwire
// drops the subscriptions on live remove. Nil when the Matter bridge is
// disabled.
//
// The readiness latch is deliberately NOT part of this hook: it is the only
// per-central Matter state that outlives the unit's EventBus, so it rides the
// registry observer in [wireMatterCentralReadiness], which covers boot-time
// and runtime-adopted centrals in one place and clears the latch when the
// central leaves the registry.
type matterCentralHook func(u *central.Unit) (unwire func())

// newMatterCentralHook builds the per-central Matter wiring hook from the
// bridge-start artefacts.
func newMatterCentralHook(
	reassembleTrigger func(),
	notifyReachable func(centralName, address string, reachable bool),
) matterCentralHook {
	return func(u *central.Unit) func() {
		if u == nil || u.EventBus == nil {
			return nil
		}
		var unsubs []func()
		if unsub := subscribeMatterReadyTrigger(u.EventBus, reassembleTrigger); unsub != nil {
			unsubs = append(unsubs, unsub)
		}
		// Hot-plugged / removed devices on the adopted central feed the
		// same debounced reassemble as the boot-time centrals.
		unsubs = append(unsubs, subscribeMatterDeviceLifecycleTrigger(u, reassembleTrigger)...)
		// A model already loaded before the hook ran (ready event fired in
		// the adopt window) needs a reassemble kick too — the event that
		// would have triggered it is gone.
		if reassembleTrigger != nil && u.IsSouthboundReady() {
			reassembleTrigger()
		}
		if unsub := wireMatterDeviceReachableForward(u, notifyReachable); unsub != nil {
			unsubs = append(unsubs, unsub)
		}
		if len(unsubs) == 0 {
			return nil
		}
		return func() {
			for _, unsub := range unsubs {
				unsub()
			}
		}
	}
}

// wireMatterRuntime stands up the Matter bridge runtime when enabled and
// returns the REST/WS adapters, any device-availability unsubscribe
// closures to register, and a teardown to defer. Returns a zero
// matterWiring + nil closers + a no-op teardown when the bridge is off.
//
// The named return shadows the internal/wiring package this file imports
// for the seam declaration in [wireMatterCentralReadiness]. The shadow is
// confined to this function, which does not declare a seam; renaming the
// return instead would rename an identifier four wiring pins and a
// WebSocket-emitter guard address by name, which is a worse trade than
// the shadow.
//
//nolint:gocognit,funlen,gocyclo // composition/wiring: long sequential Matter bridge setup
func wireMatterRuntime(ctx context.Context, cfg *config.Config, reg *central.Registry, db *gosql.DB, healthTracker *health.Tracker, labels device.ParameterTranslator, logger *slog.Logger, wsHub *ws.Hub) (wiring matterWiring, closers []func(), teardown func()) { //nolint:gocritic // importShadow: see the note above
	teardown = func() {}
	if bundle := startMatterBridge(ctx, cfg, reg, db, healthTracker, labels, logger); bundle != nil {
		// Defaulted view of the Matter config — the identical view
		// startMatterBridge fed the bridge core. The opener, the mDNS
		// advertisement, and the status reader below MUST consume the
		// same values; mixing raw and defaulted fields used to publish
		// discriminator 0 while the bridge core held the 0xF00 default.
		mcfg := cfg.North.Matter.WithDefaults()
		mb := bundle.bridge
		mfs := bundle.store
		teardown = bundle.stop
		wiring.fabricStore = mfs
		wiring.sessionLister = matterSessionLister{op: bundle.opMgr, sub: bundle.subMgr}
		wiring.mdnsReporter = matterMdnsReporter{adv: bundle.advertiser}
		wiring.endpointInspector = matterEndpointInspector{bridge: mb}
		wiring.compatReporter = matterCompatibilityReporter{
			fabrics:   mfs,
			inspector: wiring.endpointInspector,
		}
		// The ring itself is attached inside startMatterBridge, before the
		// bridge serves its first datagram; here we only expose the bridge as
		// the REST surface's reporter.
		wiring.diagEvents = mb
		wiring.exposureStore = mfs
		// Wire the allowlist checker so the assembler only bridges
		// sources that the operator has explicitly enabled. Default
		// = empty allowlist = empty topology. The exposure-management
		// REST endpoints (`/api/v1/matter/exposable`) drive the
		// matter_exposures table the checker reads through.
		//
		// Bridge.Start has already assembled the initial topology with
		// the allow-all default, so we trigger a reassemble here to
		// discard the over-broad endpoint set and rebuild scoped to
		// the persisted exposures. Without this Apple Home sees every
		// CCU device as a Matter endpoint (1000+), the initial
		// Subscribe expands to 60+ KB, the post-CASE phase exceeds
		// Apple's pairing-UI timeout and the user sees a generic
		// add-failed error even though the cryptographic handshake completed.
		mb.AttachExposureChecker(mfs)
		if err := mb.Reassemble(ctx); err != nil {
			logger.Warn("matter.bridge.reassemble.after_exposure_checker",
				slog.String("err", err.Error()))
		}

		// Wire CCU device-availability → BridgedDeviceBasicInformation
		// Reachable. WireDeviceAvailability (above) publishes a
		// DeviceLifecycleEvent{AvailabilityChanged} on each central's bus
		// whenever a device's effective availability flips (interface
		// disconnect, STICKY_UNREACH, reconnect). Forward that to the
		// bridge so the matching bridged endpoint fires the §9.13.6
		// ReachableChanged event — without this Apple/Google always see
		// every bridged device as reachable even when the CCU device is
		// dead. The Reachable attribute itself reads dev.Available() live
		// per dispatch (endpoint/materialize.go); this supplies the push
		// half so active subscriptions are notified, not just polled reads.
		for _, u := range reg.List() {
			if unsub := wireMatterDeviceReachableForward(u, mb.NotifyDeviceReachable); unsub != nil {
				closers = append(closers, unsub)
			}
		}
		// Reassemble the topology once each central's initial device load
		// completes: the boot-time assembly above runs before the async CCU
		// load, so the persisted exposures would otherwise not take effect
		// (empty bridge → commissioning refused) until an operator toggles one.
		readyBuses := make([]*events.Bus, 0, len(reg.List()))
		for _, u := range reg.List() {
			if u != nil && u.EventBus != nil {
				readyBuses = append(readyBuses, u.EventBus)
			}
		}
		reassembleClosers, reassembleTrigger := wireMatterReassembleOnReady(ctx, readyBuses, mb.Reassemble, matterReassembleReadyDebounce, logger)
		closers = append(closers, reassembleClosers...)
		// Hot-plug: device create/remove events on a ready central feed the
		// same debounced reassemble so runtime-paired (or deleted) CCU
		// devices appear as (or vanish from) bridged endpoints live.
		for _, u := range reg.List() {
			closers = append(closers, subscribeMatterDeviceLifecycleTrigger(u, reassembleTrigger)...)
		}
		// Per-central hook for the live-adopt orchestrator: a runtime-added
		// central gets the same reassemble-on-ready and reachable-forward
		// wiring as the boot-time centrals above.
		wiring.centralHook = newMatterCentralHook(reassembleTrigger, mb.NotifyDeviceReachable)
		// Wire the Root Descriptor's dynamic PartsList provider to
		// the bridge's live topology so `0:0x001D:0x0003` reflects the
		// freshly-assembled bridged endpoints. Apple Home reads
		// PartsList after CommissioningComplete; an empty list makes
		// the bridge look like an empty RootNode and Apple's UI ends
		// the commissioning with a generic add-failed error.
		// Root.Descriptor.PartsList — flat tree of all descendant
		// endpoints (Aggregator + bridged), matching matter.js HEAD's
		// `DescriptorServer.#updatePartsList` (packages/node/src/
		// behaviors/descriptor/DescriptorServer.ts:185-209) when
		// IndexBehavior is mounted on Root. matter.js's
		// `examples/device-bridge-onoff` Sample emits
		// `Root.PartsList=[1,2,3]` (Aggregator + 2 bridged children)
		// and pairs with Apple Home successfully — verified empirically
		// (matter.js byte-dump via InteractionMessenger.ts:681 hook).
		// Earlier `[1]`-only experiments produced the same Apple-Cache
		// `count: 3` symptom, so flat-tree is the matter.js-parity
		// answer, not the workaround.
		mb.AttachRootPartsListProvider(func() []uint16 {
			topo := mb.Topology()
			if topo == nil {
				return nil
			}
			ids := make([]uint16, 0, len(topo.Endpoints))
			for _, ep := range topo.Endpoints {
				if ep.IsRoot() {
					continue
				}
				ids = append(ids, ep.ID)
			}
			slices.Sort(ids)
			return ids
		})
		// Aggregator endpoint (EP 1) PartsList: every bridged endpoint
		// (ID ≥ 2). Mirrors matter.js's `Aggregator.parts = [bridged
		// children]` (AggregatorEndpoint requirements list Parts as
		// mandatory in `packages/node/src/endpoints/aggregator.ts`).
		mb.AttachAggregatorPartsListProvider(func() []uint16 {
			topo := mb.Topology()
			if topo == nil {
				return nil
			}
			ids := make([]uint16, 0, len(topo.Endpoints))
			for _, ep := range topo.Endpoints {
				if ep.IsRoot() || ep.IsAggregator() {
					continue
				}
				ids = append(ids, ep.ID)
			}
			return ids
		})
		// Build the commissioning-window opener using the configured
		// PASE parameters. The opener reuses the bridge's already-open
		// PASE acceptor; it tracks the window state and emits
		// QR + manual codes for the caller (REST handler).
		window := matterbridge.NewCommissioningWindow()
		mb.AttachCommissioningWindow(window)
		// Wire FailSafeArmer and PaseSessionCloser hooks so CommissioningWindow
		// can arm the Matter fail-safe after opening a window (Matter §11.19.6)
		// and evict open PASE sessions when the window is revoked
		// (Matter §11.19.7.3 step 1). Both adapters delegate to their
		// respective production paths — see [failSafeArmerAdapter] and
		// [paseSessionCloserAdapter].
		window.SetFailSafeChecker(bundle.rootRefs.GeneralCommissioning)
		window.SetFailSafeArmer(&failSafeArmerAdapter{
			gc:     bundle.rootRefs.GeneralCommissioning,
			logger: logger,
		})
		window.SetPaseSessionCloser(&paseSessionCloserAdapter{
			opMgr:  bundle.opMgr,
			logger: logger,
		})
		// Wire the Enhanced Commissioning Window PASE-verifier installer: when
		// a Matter commissioner opens a window via the AdministratorCommissioning
		// cluster command with a supplied PAKE verifier (multi-admin), install a
		// PASE acceptor built from that verifier for the window lifetime
		// (Matter §11.19 / §3.10.5). Wired independently of the operator's
		// EphemeralWindow REST preference — the cluster path does not go through
		// the REST opener. In ConcurrentPairings mode the restore rebuilds the
		// configured per-exchange provider; otherwise it re-arms the configured
		// singleton adapter (bundle.configuredPase, nil → noop between windows).
		var verifierConfiguredFactory func() *matterbridge.PaseAdapter
		if mcfg.Commissioning.ConcurrentPairings {
			cmCopy := mcfg.Commissioning
			opMgrLocal := bundle.opMgr
			opCredsLocal := bundle.opCreds
			gcLocal := bundle.rootRefs.GeneralCommissioning
			loggerLocal := logger
			verifierConfiguredFactory = func() *matterbridge.PaseAdapter { //nolint:contextcheck // factory signature is fixed by interface; buildPaseAdapter has no ctx
				a, err := buildPaseAdapter(cmCopy, opMgrLocal, opCredsLocal, gcLocal, loggerLocal)
				if err != nil {
					loggerLocal.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
					return nil
				}
				return a
			}
		}
		verifierInstaller := newMatterVerifierInstaller(
			mb, bundle.opMgr, bundle.opCreds, bundle.configuredPase, verifierConfiguredFactory, logger,
		)
		window.SetPaseVerifierInstaller(verifierInstaller)
		// The installer owns the live per-exchange PASE provider, whose reaper
		// goroutine outlives every window it was opened for unless something
		// stops it. Chain it into the Matter teardown so shutdown ends it.
		stopBridge := teardown
		teardown = func() {
			verifierInstaller.Close()
			stopBridge()
		}
		opener := matterbridge.NewCommissioningWindowOpener(
			window,
			mcfg.Discriminator,
			mcfg.Commissioning.Passcode,
			mcfg.VendorID,
			mcfg.ProductID,
		)
		// Matter §4.3.1.5 requires a random 64-bit hex Instance Name on
		// every commissionable record. A fixed/zero value lets Apple
		// Home cache the lookup result across reboots and reject the
		// pairing as "already known"; rolling a fresh ID per process
		// boot avoids the stale-cache trap.
		var instanceID [8]byte
		if _, err := rand.Read(instanceID[:]); err != nil {
			logger.Warn("matter.bridge.instance_id.rand_failed", slog.String("err", err.Error()))
		}
		// Rotating Device Identifier per Matter §5.4.2.4. UniqueID is
		// derived stably from the bridge identity (VendorID, ProductID,
		// SerialNumber, NodeLabel) so the value survives daemon restarts
		// without an extra persistence slot. LifetimeCounter stays at 0
		// in 0.1.0; future iterations bump it on fabric-change events.
		rotatingUniqueID := mdns.DeriveUniqueIDFromIdentity(
			mcfg.VendorID,
			mcfg.ProductID,
			rotatingSerialPart(mcfg.VendorID, mcfg.ProductID, mcfg.NodeLabel),
			mcfg.NodeLabel,
		)
		rotatingID := mdns.GenerateRotatingID(rotatingUniqueID, 0)

		wiring.opener = &matterCommissioningOpenerAdapter{
			inner:  opener,
			bridge: mb,
			advert: matterbridge.CommissioningAdvertisement{
				InstanceID:        instanceID,
				Discriminator:     mcfg.Discriminator,
				VendorID:          mcfg.VendorID,
				ProductID:         mcfg.ProductID,
				NodeLabel:         mcfg.NodeLabel,
				RotatingID:        rotatingID,
				CommissioningMode: 1, // §4.3.1.4 CM=1: open commissioning window
				// DT TXT-Record advertises the *primary* device-type
				// of the Node. matter.js's bridge sample sets it to
				// AggregatorEndpoint.deviceType (0x000E); we tested
				// 0x000E empirically against an iPhone with all four
				// fix-stack items in place (ACL, NetworkCommissioning,
				// 500ms chunk wait, ArmFailSafe→OpCreds reset) and
				// Apple's HMMTRBridgeDeviceTypeDeterminer still
				// produced endpointDeviceTypes={0=(22)} — the value
				// is not driven by DT. Side-effect of DT=0x000E was
				// that Apple skipped the SystemCommissioner pair
				// (no AddingSystemCommissioner state, no second AddNOC
				// for vendor 0x1384) which removed the second HAP
				// rebuild attempt and made the pair worse rather
				// than better. Kept at RootNode pending a wire-level
				// diff against a matter.js sample bridge.
				DeviceTypeID: 0x0016, // §10.3 RootNode — DT=0x000E empirically worse for Apple Multi-Admin flow
			},
		}
		// Transition hook: advertise an Enhanced Commissioning Window opened
		// with a supplied verifier (CM=2) and withdraw the commissionable
		// record when the window closes. Extracted to keep this function's
		// cyclomatic complexity in check.
		//nolint:contextcheck // the returned hook fires asynchronously on a window state change; there is no caller ctx to thread, so the mDNS announce/withdraw use context.Background()
		window.SetTransitionHook(matterWindowTransitionHook(mb, window, matterbridge.CommissioningAdvertisement{
			InstanceID:        instanceID,
			VendorID:          mcfg.VendorID,
			ProductID:         mcfg.ProductID,
			NodeLabel:         mcfg.NodeLabel,
			RotatingID:        rotatingID,
			CommissioningMode: 2, // §4.3.1.4 CM=2: enhanced commissioning window
			DeviceTypeID:      0x0016,
		}, logger))

		// Ephemeral-window mode: each OpenCommissioningWindow call
		// generates a fresh discriminator + passcode + Spake2+ verifier
		// and swaps it onto the bridge's PASE adapter for the window
		// duration. Works with both `concurrent_pairings=false`
		// (singleton swap) and `concurrent_pairings=true` (per-exchange
		// PaseAdapter factory installed for the window).
		if mcfg.Commissioning.EphemeralWindow {
			var configuredFactory func() *matterbridge.PaseAdapter
			if mcfg.Commissioning.ConcurrentPairings {
				// Capture the operator's configured per-exchange factory
				// so the Restore closure can re-install it after the
				// ephemeral window closes.
				cmCopy := mcfg.Commissioning
				opMgrLocal := bundle.opMgr
				opCredsLocal := bundle.opCreds
				gcLocal := bundle.rootRefs.GeneralCommissioning
				loggerLocal := logger
				configuredFactory = func() *matterbridge.PaseAdapter { //nolint:contextcheck // factory signature is fixed by interface; buildPaseAdapter has no ctx
					a, err := buildPaseAdapter(cmCopy, opMgrLocal, opCredsLocal, gcLocal, loggerLocal)
					if err != nil {
						loggerLocal.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
						return nil
					}
					return a
				}
			}
			ephem := newMatterEphemeralProvider(mb, mcfg.Commissioning, bundle.opMgr, bundle.opCreds, bundle.configuredPase, configuredFactory, logger)
			opener.SetEphemeralProvider(ephem)
			// Same ownership as the verifier installer above: the provider
			// holds the live per-exchange PASE provider and its reaper
			// goroutine, which no window close ends on its own.
			stopWithVerifier := teardown
			teardown = func() {
				ephem.Close()
				stopWithVerifier()
			}
			logger.Info("matter.bridge.pase.ephemeral_armed",
				slog.Bool("configured_fallback", bundle.configuredPase != nil),
				slog.Bool("concurrent_pairings", mcfg.Commissioning.ConcurrentPairings))
		}
		// Wire the Reassemble → WS event emit pipeline so the SPA's
		// allowlist save flow gets a `matter.endpoint_assembled`
		// notification after the topology refresh completes.
		wiring.pub = &matterEventPublisher{hub: wsHub, fabrics: wiring.fabricStore}
		// Composite onReassembled: WS event publish + Matter-spec
		// lifecycle events (BootReason + StartUp) on the FIRST
		// reassemble, when the cluster servers are wired to the
		// emitter pipeline. matter.js fires these on the equivalent
		// "behavior pipeline ready" hook (NodeServer.run).
		var reassembleOnce sync.Once
		biRef := bundle.rootRefs.BasicInformation
		gdRef := bundle.rootRefs.GeneralDiagnostics
		wiring.bi = biRef
		mb.SetOnReassembled(func(count int) { //nolint:contextcheck // callback signature is fixed; publishEndpointAssembled has no ctx
			wiring.pub.publishEndpointAssembled(count)
			reassembleOnce.Do(func() {
				if gdRef != nil {
					gdRef.EmitBootReason()
				}
				if biRef != nil {
					biRef.EmitStartUp()
				}
			})
		})
		// IMPORTANT: SetOnReassembled is wired AFTER mb.Reassemble(ctx)
		// (called earlier in this function) — the bridge's first
		// topology-assembly pass therefore fires the hook with the
		// then-nil closure and the StartUp / BootReason events never
		// land in EventLog. Apple Home's MTRDevice waits for those
		// Critical events as part of its Subscribe-Initial state-
		// machine (verified via byte-diff
		// against matter.js Sample): without them the controller
		// transitions state `Subscribing` → `Unsubscribed` instead of
		// `Subscribing` → `InitialSubscriptionEstablished`, persists
		// only 3 cluster_information records instead of ~21, and
		// surfaces the bridge as "added but not supported".
		// Trigger the same once-only emit path the SetOnReassembled
		// callback would have triggered, now that the hook is wired
		// and the cluster-server emitters are bound via the topology
		// assembler.
		reassembleOnce.Do(func() {
			if gdRef != nil {
				gdRef.EmitBootReason()
			}
			if biRef != nil {
				biRef.EmitStartUp()
			}
		})
		mb.SetOnFabricAdded(wiring.pub.publishFabricAdded)
		mb.SetOnFabricRemoved(wiring.pub.publishFabricRemoved)
		wiring.statusReader = &matterStatusReaderAdapter{
			enabled: mcfg.Enabled,
			bridge:  mb,
			store:   mfs,
			window:  window,
			cfg: &matterStatusConfig{
				advertising: mcfg.MDNSAdvertise == "zeroconf",
			},
		}
		if healthTracker != nil {
			_ = startMatterHealthProbe(ctx, wiring.statusReader, healthTracker, matterHealthProbeInterval)
		}
		revoker := &matterFabricRevokerAdapter{
			store:    mfs,
			opCreds:  bundle.opCreds,
			teardown: bundle.fabricTeardown,
			withdraw: bundle.withdrawFabric,
		}
		wiring.fabricRevoker = revoker
		wiring.fabricPurger = revoker
		wiring.reassembler = mb
		wiring.closer = &matterCommissioningCloserAdapter{window: window}
		wiring.candidates = &matterCandidateProviderAdapter{reg: reg, cfg: cfg}
	}
	return wiring, closers, teardown
}

// matterSessionLister joins the two managers that each hold half of the
// session picture: the operational manager knows which sessions exist
// and when they last carried traffic, the subscription manager knows
// what rides on them.
//
// Neither half is meaningful alone. A session with recent local activity
// and a long-idle peer is a controller that went away without closing;
// a session with no subscriptions is a controller that is connected but
// receiving nothing. Both states are invisible from the ecosystem side,
// where they look like entities that quietly stop updating.
type matterSessionLister struct {
	op  *operational.Manager
	sub *subscription.Manager
}

// MatterSessions implements [handlers.MatterSessionLister].
func (l matterSessionLister) MatterSessions() []handlers.MatterSessionInfo {
	if l.op == nil {
		return nil
	}
	var counts map[uint16]int
	if l.sub != nil {
		counts = l.sub.CountBySession()
	}
	sessions := l.op.Sessions()
	out := make([]handlers.MatterSessionInfo, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, handlers.MatterSessionInfo{
			SessionID:        s.SessionID,
			FabricIndex:      s.FabricIndex,
			PeerNodeID:       s.PeerNodeID,
			LocalNodeID:      s.LocalNodeID,
			IsPASE:           s.IsPASE,
			Subscriptions:    counts[s.SessionID],
			LastActivity:     s.LastActivity,
			LastPeerActivity: s.LastPeerActivity,
		})
	}
	return out
}

// MatterSessionOccupancy implements [handlers.MatterSessionLister]. It
// reads the id space from the allocator itself rather than counting the
// session list: an id staked for a handshake that never completed is not
// a session and would otherwise stay invisible right up to the point
// where the space refuses the next controller.
func (l matterSessionLister) MatterSessionOccupancy() handlers.MatterSessionOccupancy {
	if l.op == nil {
		return handlers.MatterSessionOccupancy{}
	}
	occ := l.op.Occupancy()
	return handlers.MatterSessionOccupancy{
		Live:     occ.Live,
		Reserved: occ.Reserved,
		Capacity: occ.Capacity,
		Free:     occ.Free(),
	}
}

// logCaseResume records one CASE resumption at debug level.
//
// The resume fast path is the one CASE branch whose behaviour cannot be
// validated without a live controller: it establishes a session from a
// cached secret and keeps the session id the responder already
// announced, where matter.js takes a fresh one
// (packages/protocol/src/session/case/CaseServer.ts:#resume calls
// getNextAvailableSessionId). Reusing the id risks conflating the peer's
// previous message counters with the new session; renewing it burns one
// id per MRP retransmit of the resume Sigma1. Which one a certified
// controller actually provokes shows up across a network partition or a
// bridge restart, not in a test — so what ships is the record that lets
// an operator report answer it: the cached record the controller
// resumed from, the session id on both sides of the resume, and the
// sessions the install displaced. matter.js logs a resumed session for
// the same reason (packages/protocol/src/session/NodeSession.ts:412
// logNew(logger, "Resumed", …)); we keep it at debug because it fires
// on the wire path, once per resume.
//
// displaced are the peer's live session ids read immediately before the
// install — every one of them is evicted by it.
func logCaseResume(logger *slog.Logger, info sigma.ResumeInfo, fabricIndex uint8, peerNodeID uint64, displaced []uint16, occ operational.SessionTableOccupancy) {
	if logger == nil || !info.Resumed {
		return
	}
	ids := make([]string, 0, len(displaced))
	for _, id := range displaced {
		ids = append(ids, strconv.Itoa(int(id)))
	}
	logger.Debug("matter.bridge.case.session_resumed",
		slog.String("presented_resumption_id", hex.EncodeToString(info.PresentedResumptionID)),
		slog.String("issued_resumption_id", hex.EncodeToString(info.IssuedResumptionID)),
		slog.Int("session_id_before", int(info.SessionIDBefore)),
		slog.Int("session_id_after", int(info.SessionIDAfter)),
		slog.Bool("session_id_renewed", info.SessionIDBefore != info.SessionIDAfter),
		slog.Int("fabric_index", int(fabricIndex)),
		slog.Uint64("peer_node_id", peerNodeID),
		slog.Int("displaced_session_count", len(displaced)),
		slog.String("displaced_session_ids", strings.Join(ids, ",")),
		slog.Int("sessions_live", occ.Live),
		slog.Int("sessions_reserved", occ.Reserved))
}

// matterMdnsReporter reads the live advertiser and runs the mDNS
// diagnosis over what it is currently announcing.
//
// Reading the advertiser rather than the config is the point: the two
// diverge exactly when discovery fails. A configured address set that
// the host resolved differently, a subtype responder that failed to
// start, a publish that errored after boot — each leaves the config
// looking correct and the announcement wrong.
type matterMdnsReporter struct{ adv mdns.Advertiser }

// MatterMdns implements [handlers.MatterMdnsReporter].
func (r matterMdnsReporter) MatterMdns() handlers.MatterMdnsDiagnostics {
	out := handlers.MatterMdnsDiagnostics{
		Services: []handlers.MatterMdnsService{},
		Findings: []handlers.MatterMdnsFinding{},
	}
	if r.adv == nil {
		return out
	}
	// A no-op advertiser is the explicit "stay quiet" opt-in, not a
	// fault: reporting it as advertising=false lets the surface say so
	// instead of listing an empty announcement as a defect.
	_, isNoop := r.adv.(*mdns.Noop)
	out.Advertising = !isNoop

	services := r.adv.Active()
	for i := range services {
		svc := &services[i]
		addrs := make([]string, 0, len(svc.Addresses))
		for _, ip := range svc.Addresses {
			addrs = append(addrs, ip.String())
		}
		txt := make(map[string]string, len(svc.TXT))
		for _, rec := range svc.TXT {
			txt[rec.Key] = rec.Value
		}
		out.Services = append(out.Services, handlers.MatterMdnsService{
			ServiceType:  svc.ServiceType,
			InstanceName: svc.InstanceName,
			HostName:     svc.HostName,
			Port:         svc.Port,
			Addresses:    addrs,
			Subtypes:     append([]string(nil), svc.Subtypes...),
			TXT:          txt,
		})
	}
	if !out.Advertising {
		return out
	}
	for _, f := range mdns.Diagnose(services) {
		out.Findings = append(out.Findings, handlers.MatterMdnsFinding{
			Severity: string(f.Severity),
			Code:     f.Code,
			Message:  f.Message,
			Service:  f.Service,
		})
	}
	return out
}

// matterEndpointInspector reads the bridge's assembled topology.
//
// It reports the endpoint identity as assigned, never as derived: the
// numbers come from persisted identity rather than from position in the
// device list, so they survive restarts but cannot be inferred from the
// fleet. Anything picking an endpoint by ordinal — "the lowest one with
// cluster X" — reads a different device after any change to the store's
// history.
type matterEndpointInspector struct{ bridge *matterbridge.Bridge }

// MatterEndpoints implements [handlers.MatterEndpointInspector].
func (i matterEndpointInspector) MatterEndpoints() []handlers.MatterEndpointInfo {
	if i.bridge == nil {
		return nil
	}
	topo := i.bridge.Topology()
	if topo == nil {
		return nil
	}
	out := make([]handlers.MatterEndpointInfo, 0, len(topo.Endpoints))
	for _, ep := range topo.Endpoints {
		if ep == nil {
			continue
		}
		info := handlers.MatterEndpointInfo{
			EndpointID:       ep.ID,
			ParentEndpointID: ep.ParentEndpointID,
			DeviceType:       ep.DeviceType,
			Reachable:        ep.Reachable,
			FriendlyName:     ep.FriendlyName,
			Clusters:         []handlers.MatterEndpointCluster{},
		}
		if name, ok := matterschema.DeviceTypeName(uint32(ep.DeviceType)); ok {
			info.DeviceTypeName = name
		}
		if rev, ok := matterschema.DeviceTypeRevision(uint32(ep.DeviceType)); ok {
			info.DeviceTypeRevision = rev
		}
		info.DeviceAddress = ep.SourceKey.DeviceAddress
		info.ChannelAddress = ep.ChannelAddress
		if ep.Source != nil {
			seen := make(map[uint32]struct{})
			for _, cs := range ep.Source.MatterClusterServers() {
				if cs == nil {
					continue
				}
				id := cs.MatterClusterID()
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				cluster := handlers.MatterEndpointCluster{ID: id}
				if name, ok := matterschema.ClusterName(id); ok {
					cluster.Name = name
				}
				if rev, ok := matterschema.ClusterRevision(id); ok {
					cluster.Revision = rev
				}
				info.Clusters = append(info.Clusters, cluster)
			}
			sort.Slice(info.Clusters, func(a, b int) bool { return info.Clusters[a].ID < info.Clusters[b].ID })
		}
		out = append(out, info)
	}
	return out
}

// matterCompatibilityReporter joins the commissioned fabrics with the
// assembled topology, which is the only place both halves are known.
//
// A compatibility problem needs both: the device type alone is fine
// until an ecosystem that refuses it is commissioned, and the fabric
// alone says nothing about what it will be shown.
type matterCompatibilityReporter struct {
	fabrics   handlers.MatterFabricStore
	inspector handlers.MatterEndpointInspector
}

// MatterCompatibility implements [handlers.MatterCompatibilityReporter].
func (r matterCompatibilityReporter) MatterCompatibility() handlers.MatterCompatibility {
	out := handlers.MatterCompatibility{
		Ecosystems: []handlers.MatterEcosystem{},
		Findings:   []handlers.MatterCompatFinding{},
	}

	var vendorIDs []uint16
	if r.fabrics != nil {
		if recs, err := r.fabrics.ListFabrics(context.Background()); err == nil {
			for _, rec := range recs {
				vendorIDs = append(vendorIDs, rec.VendorID)
				out.Ecosystems = append(out.Ecosystems, handlers.MatterEcosystem{
					Ecosystem:   string(eligibility.EcosystemForVendor(rec.VendorID)),
					VendorID:    rec.VendorID,
					FabricIndex: rec.FabricIndex,
					Label:       rec.Label,
				})
			}
		}
	}

	deviceTypes := make(map[uint16]int)
	if r.inspector != nil {
		endpoints := r.inspector.MatterEndpoints()
		// The root and aggregator endpoints are bridge scaffolding, not
		// exposed devices — counting them would push the ecosystem
		// ceiling warning two devices early.
		for _, ep := range endpoints {
			if ep.EndpointID <= 1 {
				continue
			}
			deviceTypes[ep.DeviceType]++
			out.EndpointCount++
		}
	}

	for _, f := range eligibility.Compat(vendorIDs, deviceTypes, out.EndpointCount) {
		out.Findings = append(out.Findings, handlers.MatterCompatFinding{
			Ecosystem:  string(f.Ecosystem),
			Code:       f.Code,
			Message:    f.Message,
			DeviceType: f.DeviceType,
		})
	}
	return out
}

// matterDiagEventCapacity bounds the in-memory pairing trace. Large
// enough to cover a pairing attempt and the minute around it, small
// enough that it never competes with the durable records.
const matterDiagEventCapacity = 200

// matterHealthRecorder adapts the daemon's health tracker to the slim
// sample type the Matter bridge probe speaks. The probe reports only a
// verdict and a stable English note; the tracker's richer sample (catalogue
// key, timestamp, staleness exemption) is filled in here, on the host side,
// so the Matter subtree needs no dependency on the tracker package.
type matterHealthRecorder struct{ t *health.Tracker }

func (r matterHealthRecorder) Record(name string, s matterbridge.HealthSample) {
	r.t.Record(name, health.Sample{Healthy: s.Healthy, Note: s.Note})
}

func (r matterHealthRecorder) RecordUnhealthy(name string, s matterbridge.HealthSample) {
	r.t.RecordUnhealthy(name, health.Sample{Healthy: s.Healthy, Note: s.Note})
}
