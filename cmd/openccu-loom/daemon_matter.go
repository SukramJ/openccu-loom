// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	gosql "database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/go-fabric/bootid"
	matterbridge "github.com/SukramJ/go-fabric/bridge"
	mattercore "github.com/SukramJ/go-fabric/cluster/core"
	"github.com/SukramJ/go-fabric/eligibility"
	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
	mattergroups "github.com/SukramJ/go-fabric/groups"
	"github.com/SukramJ/go-fabric/im/subscription"
	"github.com/SukramJ/go-fabric/mdns"
	"github.com/SukramJ/go-fabric/node"
	matterschema "github.com/SukramJ/go-fabric/schema"
	"github.com/SukramJ/go-fabric/secure/attestation"
	"github.com/SukramJ/go-fabric/secure/operational"
	mattersetup "github.com/SukramJ/go-fabric/secure/setup"
	"github.com/SukramJ/go-fabric/secure/spake2"
	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/deployment"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	discoverymdns "github.com/SukramJ/openccu-loom/internal/north/discovery/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// matterTCPConfig maps config.NorthMatter.TCP onto the bridge's TCP
// roles. The validator restricts the value to the four words; the zero
// value (off) is what an unset key means.
func matterTCPConfig(mode string) matterbridge.TCPConfig {
	switch mode {
	case config.MatterTCPIncoming:
		return matterbridge.TCPConfig{Incoming: true}
	case config.MatterTCPOutgoing:
		return matterbridge.TCPConfig{Outgoing: true}
	case config.MatterTCPBoth:
		return matterbridge.TCPConfig{Incoming: true, Outgoing: true}
	}
	return matterbridge.TCPConfig{}
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

// matterBridgeBundle is what startMatterBridge hands wireMatterRuntime:
// the node facade that runs the bridge, and the daemon-side collaborators
// the REST / WebSocket adapters are built over.
type matterBridgeBundle struct {
	// node is go-fabric's node facade (ADR 0084 phase 3): it performs the
	// stack-generic bring-up — stores, root and Aggregator servers,
	// sessions, PASE, CASE, the commissioning window, the fabric hooks,
	// the event-number persistence — and owns their lifecycle. What the
	// daemon reaches through it is named by the fields below.
	node   *node.Node
	bridge *matterbridge.Bridge
	stop   func()
	store  *matterstore.Store
	// identity persists endpoint ids and the exposure allowlist — the two
	// tables keyed by this daemon's source 5-tuple rather than by anything
	// Matter defines. See [matterendpoint].
	identity *matterendpoint.Store
	// assembler turns this daemon's device model into the endpoint
	// topology the bridge serves. The facade consumes a topology through
	// [node.Config.Topology] and never builds one, so the assembler and
	// everything that configures it — the exposure allowlist above all —
	// live on this side of the boundary and are reached through here.
	assembler *matteradapter.Assembler
	// opMgr and subMgr are the facade's session and subscription managers
	// ([node.Node.Sessions], [node.Node.Subscriptions]). Carried so the
	// REST diagnostics surface reports which peers hold a session and how
	// many subscriptions ride on each — a commissioned controller holding
	// none looks identical to a healthy one from every other angle.
	opMgr  *operational.Manager
	subMgr *subscription.Manager
	// advertiser is the live mDNS advertiser. Carried so the diagnostics
	// surface can report what is actually announced rather than what the
	// config asked for — the two diverge exactly when discovery fails.
	advertiser mdns.Advertiser
	opCreds    *mattercore.OperationalCredentials
	root       *node.RootClusters
	// groups is the one group-state manager both sides share: the
	// assembler's endpoint Groups / ScenesManagement servers and, through
	// node.Config.Groups, the facade's root GroupKeyManagement and the
	// bridge's group-message routing.
	groups *mattergroups.Manager
	// fabricEvents is where the facade's fabric observers deliver. The
	// WebSocket publisher attaches in wireMatterRuntime, once the hub
	// exists; startMatterBridge runs before it.
	fabricEvents *matterFabricEvents
	// configuredCommissioning reports whether the operator configured a
	// valid setup passcode. Without one the REST opener refuses the
	// configured-credentials window, as the daemon did before the facade;
	// the node itself always has credentials ([matterCommissioningCredentials]).
	configuredCommissioning bool
}

// matterFabricEvents relays the facade's fabric observers to the WebSocket
// publisher, which is built after the node: the observers are fixed at
// [node.New], the hub reaches wireMatterRuntime later. An event before the
// publisher is attached is dropped — the window is the few milliseconds
// between [node.Node.Start] and the attach, and a controller cannot have
// commissioned in it.
type matterFabricEvents struct {
	mu      sync.RWMutex
	added   func(fabricIndex uint8)
	removed func(fabricIndex uint8)
}

func (e *matterFabricEvents) set(added, removed func(fabricIndex uint8)) {
	e.mu.Lock()
	e.added, e.removed = added, removed
	e.mu.Unlock()
}

func (e *matterFabricEvents) fabricAdded(info node.FabricInfo) error {
	e.mu.RLock()
	fn := e.added
	e.mu.RUnlock()
	if fn != nil {
		fn(info.FabricIndex)
	}
	return nil
}

func (e *matterFabricEvents) fabricRemoved(info node.FabricInfo) error {
	e.mu.RLock()
	fn := e.removed
	e.mu.RUnlock()
	if fn != nil {
		fn(info.FabricIndex)
	}
	return nil
}

// matterRootNodeDeviceType is the device type the commissionable record's
// DT key advertises (Matter Device Library §2.1 RootNode). matter.js's
// bridge sample advertises the Aggregator's (0x000E); measured against an
// iPhone, DT=0x000E made Apple skip its SystemCommissioner pair and the
// Multi-Admin flow worse, so the daemon keeps RootNode.
const matterRootNodeDeviceType = 0x0016

// matterSupportedFabrics is OperationalCredentials' SupportedFabrics. The
// daemon served the cluster's own default before the facade — the 254
// matter.js advertises (OperationalCredentialsServer.ts:84-88) — and the
// facade's default is 5, so the value is stated to keep the wire.
const matterSupportedFabrics = 254

// matterSetupPasscodeMax is the Matter §5.1.1.6 upper bound of a setup
// passcode.
const matterSetupPasscodeMax = 99999998

// matterRootSerial is the root BasicInformation SerialNumber: a stable
// 16-char hex digest of VID|PID|NodeLabel. ha-bridge derives its serial
// from a hash of the bridge id, and Apple Home's HAP-service rebuild
// reads SerialNumber for accessory deduplication, logging `could not find
// cached attribute values` when it is absent. Distinct from UniqueID
// (the full SHA-256 digest) so the matter.js invariant
// `uniqueId !== serialNumber` (basic-information-validators.ts:26) holds.
func matterRootSerial(mc config.NorthMatter) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%04X|%04X|%s|serial", mc.VendorID, mc.ProductID, mc.NodeLabel))
	return hex.EncodeToString(h[:8])
}

// matterLocales is the root LocalizationConfiguration's SupportedLocales,
// the first of them its ActiveLocale: the daemon's locale, and en-US
// always.
func matterLocales(locale string) []string {
	switch strings.ToLower(locale) {
	case "de", "de-de":
		return []string{"de-DE", "en-US"}
	default:
		return []string{"en-US"}
	}
}

// matterCommissioningCredentials resolves the PASE credentials the node is
// built with. The facade refuses to build without a valid passcode and a
// salt of spake2's size, whereas the daemon runs without a configured
// passcode — the operator pairs through the ephemeral window, or not at
// all. A missing or invalid passcode becomes a random one for this
// process: the node's own window at first boot then runs on credentials
// nobody holds, and the REST opener refuses the configured-credentials
// window as it did before (configured is false). A salt outside spake2's
// bounds becomes a random one, which changes nothing a commissioner sees:
// the salt travels in PBKDFParamResponse, and the verifier is recomputed
// from passcode and salt at every boot.
func matterCommissioningCredentials(cm config.NorthMatterCommissioning, logger *slog.Logger) (passcode uint32, salt []byte, iterations int, configured bool, err error) {
	passcode, configured = cm.Passcode, true
	if passcode == 0 || passcode > matterSetupPasscodeMax || !mattersetup.IsValidSetupPIN(passcode) {
		if cm.Passcode != 0 {
			logger.Warn("matter.bridge.pase.passcode_invalid",
				slog.String("hint", "north.matter.commissioning.passcode is out of range or on the trivially-guessable list; the configured-credentials window is disabled until it is fixed"))
		}
		passcode, err = matterbridge.RandomPasscode()
		if err != nil {
			return 0, nil, 0, false, fmt.Errorf("random passcode: %w", err)
		}
		configured = false
	}
	salt = []byte(cm.Salt)
	if len(salt) < spake2.PBKDFMinSaltSize || len(salt) > spake2.PBKDFMaxSaltSize {
		if cm.Salt != "" {
			logger.Warn("matter.bridge.pase.salt_invalid",
				slog.Int("len", len(salt)),
				slog.Int("min", spake2.PBKDFMinSaltSize),
				slog.Int("max", spake2.PBKDFMaxSaltSize),
				slog.String("hint", "a random salt is used for this process; the commissioner receives it in PBKDFParamResponse"))
		}
		salt, err = matterbridge.RandomSalt()
		if err != nil {
			return 0, nil, 0, false, fmt.Errorf("random salt: %w", err)
		}
	}
	return passcode, salt, cm.Iterations, configured, nil
}

// matterAttestation is the device attestation material: the operator's
// DAC / PAI / CD when configured and consistent, else the facade's CSA
// test chain for the vendor and product — pairable with every
// commissioner's test PAA (Apple Home, Google Home and chip-tool accept
// it without --bypass-attestation-verifier), and not shippable.
func matterAttestation(mc config.NorthMatter, logger *slog.Logger) node.Attestation {
	if key, dac, pai, cd, ok := loadVendorAttestation(mc.Attestation, logger); ok {
		logger.Info("matter.bridge.attestation.vendor",
			slog.String("hint", "production DAC/PAI/CD loaded from config — chip-tool can validate without --bypass-attestation-verifier"))
		return node.Attestation{
			Chain:                    &attestation.Chain{DACKey: key, DAC: dac, PAI: pai},
			CertificationDeclaration: cd,
		}
	}
	logger.Info("matter.bridge.attestation.csa_test",
		slog.String("hint", "CSA Test PAA chain (PAA→PAI→DAC) + signed CD — Apple Home / Google Home / chip-tool accept this without --bypass-attestation-verifier"),
		slog.String("vendor_id", fmt.Sprintf("0x%04X", mc.VendorID)),
		slog.String("product_id", fmt.Sprintf("0x%04X", mc.ProductID)))
	return node.Attestation{}
}

// startMatterBridge constructs and starts the Matter bridge when
// matter.enabled is set: the daemon's own responsibilities — the enable
// and database gate, the endpoint-identity and exposure store, the
// per-central readiness latch, the localized channel word, the topology
// assembler, the pinned UniqueID, the attestation material, the health
// probe and gauges — around go-fabric's node facade, which performs the
// stack-generic bring-up in its documented order (ADR 0084 phase 3).
// Returns the bundle and a stop function the caller defers; both are nil
// when the feature flag is off or the bridge cannot stand up. Errors are
// logged at warn level but never abort the daemon — the bridge is
// feature-flagged and failing to start it must not take REST / MQTT down
// with it.
//
// Defaults applied here mirror the [config.NorthMatter] doc strings:
// VendorID 0xFFF1 (test vendor block — never ship), ProductID 0x8000,
// NodeLabel "openccu-loom", Discriminator 0xF00, Listen ":5540".
//
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; startMatterBridge never opens or
// closes it — a nil db degrades the bridge to disabled (same as a
// disabled cfg.North.Matter.Enabled), and every early-return path below
// leaves the handle untouched for the caller to keep using. The facade is
// handed the two stores, never the handle ([node.Store.DB] would apply
// go-fabric's schema; the daemon carries it in its own migrations).
func startMatterBridge(ctx context.Context, cfg *config.Config, reg *central.Registry, db *gosql.DB, healthTracker *health.Tracker, labels device.ParameterTranslator, logger *slog.Logger) *matterBridgeBundle { //nolint:funlen // composition/wiring: long sequential setup
	if cfg == nil || !cfg.North.Matter.Enabled {
		return nil
	}
	if db == nil {
		logger.Warn("matter.bridge.db_unavailable", slog.String("hint", "shared openccu-loom.db handle is nil; Matter bridge disabled"))
		return nil
	}

	store := matterstore.New(db)
	// Group membership is stack state, as in matter.js where GroupsServer
	// keeps it in the root's GroupKeyManagementServer (go-fabric ADR
	// 0009): one manager over the store feeds the assembler (the bridged
	// endpoints' Groups and ScenesManagement servers) and, through
	// node.Config.Groups, the facade's root GroupKeyManagement and
	// Groupcast servers and the bridge's group-message routing. Two
	// managers over the same store would diverge in memory: an AddGroup
	// on an endpoint would not be routed until the next load.
	groupMgr, gErr := mattergroups.NewManager(store, logger)
	if gErr != nil {
		logger.Warn("matter.bridge.groups", slog.String("err", gErr.Error()))
		return nil
	}
	// Endpoint identity and the exposure allowlist are keyed by this
	// daemon's own 5-tuple, so they are persisted here rather than by the
	// bridge module — which sees only an opaque key. Same database handle,
	// same migrations; two access surfaces because two owners.
	identity := matterendpoint.New(db)

	// Single defaulting point: the SAME defaulted view feeds the facade
	// here and the opener / status reader in wireMatterRuntime +
	// mountRESTRouter. See config.NorthMatter.WithDefaults.
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
	// (the facade's Start) so the snapshotter can stamp
	// [endpoint.Snapshot.ModelComplete]. The boot-time assembly runs before
	// the readiness-gated CCU device load, so every registered central
	// briefly presents an empty ModelRegistry; without the latch the
	// assembler's vanished-source GC would read that as "all devices
	// removed" and wipe the persisted endpoint-ID rows on every boot,
	// renumbering the bridged fleet for paired controllers.
	readiness, unwireReadiness := wireMatterCentralReadiness(reg)
	walk := matterSnapshotter(reg, readiness)

	advertiser, closeAdvertiser := buildMatterAdvertiser(mc, logger) //nolint:contextcheck // buildMatterAdvertiser has no ctx; the subtype responder runs for the advertiser's lifetime and is released through closeAdvertiser
	fail := func(event string, err error) *matterBridgeBundle {
		logger.Warn(event, slog.String("err", err.Error()))
		unwireReadiness()
		closeAdvertiser()
		return nil
	}

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
	// The daemon owns topology assembly: it knows the device model, the
	// operator's exposure allowlist and the locale, none of which the
	// Matter subtree may see. The facade is handed the finished topology
	// through the snapshotter below.
	assembler, err := matteradapter.New(identity, matteradapter.Config{
		VendorID:                mc.VendorID,
		ProductID:               mc.ProductID,
		NodeLabel:               mc.NodeLabel,
		ExposeSecondaryChannels: mc.ExposeSecondaryChannels,
		IncludeMeasurements:     mc.IncludeMeasurements,
		Labels:                  labels,
		ChannelLabel:            channelLabel,
		Groups:                  groupMgr,
		Scenes:                  matterScenesStore{store: store, logger: logger},
		NodeLabels:              identity,
	}, logger)
	if err != nil {
		return fail("matter.bridge.assembler", err)
	}
	// The allowlist checker is wired before the first assembly, so the
	// bridge only ever serves sources the operator has explicitly
	// enabled. Default = empty allowlist = empty topology. The
	// exposure-management REST endpoints (`/api/v1/matter/exposable`)
	// drive the matter_exposures table the checker reads through.
	// Without it Apple Home would see every CCU device as a Matter
	// endpoint (1000+), the initial Subscribe would expand to 60+ KB,
	// the post-CASE phase would exceed Apple's pairing-UI timeout and
	// the user would see a generic add-failed error even though the
	// cryptographic handshake completed.
	assembler.SetExposureChecker(identity)

	rootSerial := matterRootSerial(mc)
	passcode, salt, iterations, configured, err := matterCommissioningCredentials(mc.Commissioning, logger)
	if err != nil {
		return fail("matter.bridge.pase.credentials", err)
	}
	fabricEvents := &matterFabricEvents{}
	n, err := node.New(node.Config{
		Network: node.Network{
			Listen:     mc.Listen,
			PreferIPv4: mc.PreferIPv4,
			TCP:        matterTCPConfig(mc.TCP),
		},
		Commissioning: node.Commissioning{
			Passcode:           passcode,
			Discriminator:      mc.Discriminator,
			PBKDFIterations:    iterations,
			Salt:               salt,
			LocationCapability: mattercore.RegulatoryIndoorOutdoor,
		},
		ProductDescription: node.ProductDescription{
			Name:       mc.NodeLabel,
			DeviceType: matterRootNodeDeviceType,
			VendorID:   mc.VendorID,
			ProductID:  mc.ProductID,
		},
		BasicInformation: node.BasicInformation{
			VendorName:   "openccu-loom",
			ProductName:  "openccu-loom Matter Bridge",
			NodeLabel:    mc.NodeLabel,
			SerialNumber: rootSerial,
			// UniqueID carries quality F (fixed for the lifetime of the
			// device): the value persisted on the first boot is served
			// verbatim, so a node_label edit never re-identifies the
			// bridge under a paired controller's cache.
			UniqueID:              resolveBridgeUniqueID(ctx, store, mc, rootSerial, logger),
			HardwareVersion:       1,
			HardwareVersionString: "1.0",
			SoftwareVersion:       mattercore.SoftwareVersionFromString(build.Version),
			SoftwareVersionString: build.Version,
		},
		Store:                      node.Store{Credentials: store, Endpoints: identity},
		Advertiser:                 advertiser,
		Attestation:                matterAttestation(mc, logger),
		Groups:                     groupMgr,
		Topology:                   matterTopologySnapshotter(assembler, walk),
		Locales:                    matterLocales(cfg.Locale),
		SupportedFabrics:           matterSupportedFabrics,
		DisableTimeSynchronization: !mc.TimeSyncEnabled(),
		Observers: node.Observers{
			OnFabricAdded:   fabricEvents.fabricAdded,
			OnFabricRemoved: fabricEvents.fabricRemoved,
		},
		Logger: logger,
	})
	if err != nil {
		return fail("matter.bridge.node", err)
	}
	if mc.TimeSyncEnabled() {
		logger.Warn("matter.bridge.time_sync.mounted — TimeSynchronization (0x0038) advertised on RootNode by operator opt-in; some controllers (e.g. Apple Home) may reject pairing")
	}
	bridge := n.Bridge()
	if err := n.Start(ctx); err != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = n.Stop(stopCtx)
		cancel()
		return fail("matter.bridge.start", err)
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
		slog.Bool("ipv4_only", mc.PreferIPv4),
		slog.Bool("configured_passcode", configured))

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

	stop := func() { //nolint:contextcheck // shutdown path must not inherit the (potentially cancelled) daemon ctx
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		unwireReadiness()
		if stopReannounce != nil {
			stopReannounce()
		}
		// The facade's Stop runs the daemon's shutdown order: the
		// operational time stored once more, BasicInformation ShutDown,
		// the PASE providers, the CASE reaper, the subscription engine,
		// the bridge (mDNS goodbyes on the wire) and the advertiser.
		if err := n.Stop(stopCtx); err != nil {
			logger.Warn("matter.bridge.stop", slog.String("err", err.Error()))
		}
		// Closing the Zeroconf advertiser closes the subtype responder
		// attached to it, so this is an idempotent second close on the
		// normal path. It stays because it is the ONLY responder cleanup
		// on the New / Start failure early returns above, where the
		// facade's Stop never runs; running it here too is harmless.
		closeAdvertiser()
	}
	return &matterBridgeBundle{
		node:                    n,
		bridge:                  bridge,
		stop:                    stop,
		store:                   store,
		identity:                identity,
		assembler:               assembler,
		opMgr:                   n.Sessions(),
		subMgr:                  n.Subscriptions(),
		advertiser:              advertiser,
		opCreds:                 n.Root().OperationalCredentials,
		root:                    n.Root(),
		groups:                  groupMgr,
		fabricEvents:            fabricEvents,
		configuredCommissioning: configured,
	}
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
			slog.String("hint", "DAC certificate public key does not match supplied private key — falling back to the CSA test chain"))
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

// startMDNSAdvertiser parses the REST listen address, builds the
// daemon-discovery TXT bundle, and starts a multicast advertiser.
// Returns (nil, nil) when the listen address has no usable port
// (Unix socket etc.) so the caller can skip without an error path.
// Returns (nil, err) when the port is malformed or zeroconf fails to
// register; the caller is expected to log and continue (mDNS is a
// convenience, not a hard dependency).
func startMDNSAdvertiser(ctx context.Context, cfg *config.Config, self mdnsSelf, reg *central.Registry, logger *slog.Logger) (discoverymdns.Advertiser, error) {
	svc, ok := mdnsServiceFor(cfg, self, len(reg.Names()), mdnsCCUSerials(reg))
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
func mdnsServiceFor(cfg *config.Config, self mdnsSelf, centralCount int, ccuSerials []string) (discoverymdns.Service, bool) {
	port, ok := splitListenPort(cfg.North.REST.Listen)
	if !ok {
		return discoverymdns.Service{}, false
	}
	return discoverymdns.Service{
		InstanceName: cfg.North.Discovery.MDNS.InstanceName,
		Port:         port,
		TXT:          mdnsTXT(cfg, self, centralCount, ccuSerials),
	}, true
}

// mdnsSelf is the daemon's self-description as the mDNS record carries
// it: the short form of what `/info` says (ADR 0081). It is built from
// the values `/info` serves — the resolved deployment and the capability
// detector's login paths — so the record cannot say something else.
type mdnsSelf struct {
	kind       deployment.Kind
	tls        bool
	loginPaths []string
}

// mdnsTXTVersion versions the record's key set (RFC 6763 section 6.7).
const mdnsTXTVersion = "1"

// mdnsTXT assembles the TXT bundle. Shared by the initial Register and
// every runtime re-announce so both paths stay identical.
func mdnsTXT(cfg *config.Config, self mdnsSelf, centralCount int, ccuSerials []string) []string {
	tls := "0"
	if self.tls {
		tls = "1"
	}
	txt := []string{
		"txtvers=" + mdnsTXTVersion,
		"path=/api/v1",
		"api_version=" + handlers.APIVersion,
		"tls=" + tls,
		"instance=" + cfg.North.Discovery.MDNS.ResolveInstanceName(),
		"centrals=" + strconv.Itoa(centralCount),
		"deploy=" + string(self.kind),
	}
	if p := self.kind.IngressPath(); p != "" {
		txt = append(txt, "ingress="+p)
	}
	if len(self.loginPaths) > 0 {
		txt = append(txt, "auth="+strings.Join(self.loginPaths, ","))
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
	exposureStore *matterendpoint.Store
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
	return bus.Subscribe(func(hmevent.CentralSouthboundReadyEvent) {
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
		u.EventBus.Subscribe(func(hmevent.DeviceCreatedEvent) { fire() }),
		u.EventBus.Subscribe(func(hmevent.DeviceRemovedEvent) { fire() }),
		// A rename is a topology change too, even though the set of devices is
		// unchanged: the accessory's NodeLabel is built from the device's name,
		// so without this a device renamed in the CCU WebUI keeps its old name
		// in Apple Home and Google Home until the daemon restarts. MQTT and the
		// WebSocket already learn about it from the same event.
		u.EventBus.Subscribe(func(hmevent.DeviceMetadataChangedEvent) { fire() }),
		// A release adds a device to the bridged set without changing the
		// model: it was materialised long ago and only the wizard's last
		// step made it publishable. Without this it reaches no controller
		// until the daemon restarts.
		u.EventBus.Subscribe(func(hmevent.DeviceReleasedEvent) { fire() }),
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
	unsub := u.EventBus.Subscribe(func(e hmevent.CentralSouthboundReadyEvent) {
		readiness.markReady(e.CentralName)
	})
	if u.IsSouthboundReady() {
		readiness.markReady(u.Name())
	}
	return unsub
}

// matterTopologySnapshotter composes the model walk with topology
// assembly into the single callback the bridge holds.
//
// The bridge calls this at the top of each (re)assembly, off its own
// lock, exactly where it previously called the walk and the assembler in
// sequence — so a snapshot and the topology built from it stay one
// atomic step from the bridge's point of view, and a reassembly still
// reads the model as late as possible rather than at wiring time.
func matterTopologySnapshotter(asm *matteradapter.Assembler, walk func(context.Context) []matteradapter.DeviceSnapshot) matterbridge.Snapshotter {
	return func(ctx context.Context) (*fabricendpoint.Topology, error) {
		return asm.AssembleDevices(ctx, walk(ctx))
	}
}

// matterSnapshotter builds the model walk behind the bridge's
// snapshotter: one [matteradapter.DeviceSnapshot] per registered central,
// read live from the central registry so runtime-added centrals surface
// on the next assembly. Each snapshot's ModelComplete flag is stamped
// from the readiness latch; a nil readiness marks every central
// model-incomplete (the GC-off fail-safe).
func matterSnapshotter(reg *central.Registry, readiness *matterCentralReadiness) func(context.Context) []matteradapter.DeviceSnapshot {
	return func(_ context.Context) []matteradapter.DeviceSnapshot {
		var out []matteradapter.DeviceSnapshot
		for _, u := range reg.List() {
			if u == nil || u.ModelRegistry == nil {
				continue
			}
			out = append(out, matteradapter.DeviceSnapshot{
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
	return u.EventBus.Subscribe(func(e hmevent.DeviceLifecycleEvent) {
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

// wireMatterRuntime boots the bridge through startMatterBridge and builds
// the daemon's adapters over it: the REST / WebSocket ports, the
// reassemble-on-ready and hot-plug triggers, the reachability forward and
// the live-adopt hook. What the node facade wires itself — the
// commissioning window with its fail-safe, verifier installer and CM=2
// announcement, the PartsList providers, the StartUp / BootReason events,
// the fabric fan-out — is not repeated here (ADR 0084 phase 3).
func wireMatterRuntime(ctx context.Context, cfg *config.Config, reg *central.Registry, db *gosql.DB, healthTracker *health.Tracker, labels device.ParameterTranslator, logger *slog.Logger, wsHub *ws.Hub) (wiring matterWiring, closers []func(), teardown func()) { //nolint:gocritic // importShadow: see the note above
	teardown = func() {}
	bundle := startMatterBridge(ctx, cfg, reg, db, healthTracker, labels, logger)
	if bundle == nil {
		return wiring, closers, teardown
	}
	// Defaulted view of the Matter config — the identical view
	// startMatterBridge fed the facade. The opener and the status reader
	// below MUST consume the same values; mixing raw and defaulted fields
	// used to publish discriminator 0 while the bridge core held the
	// 0xF00 default.
	mcfg := cfg.North.Matter.WithDefaults()
	mb := bundle.bridge
	mfs := bundle.store
	mids := bundle.identity
	teardown = bundle.stop
	wiring.fabricStore = mfs
	wiring.sessionLister = matterSessionLister{op: bundle.opMgr, sub: bundle.subMgr}
	wiring.mdnsReporter = matterMdnsReporter{adv: bundle.advertiser}
	wiring.endpointInspector = matterEndpointInspector{bridge: mb}
	wiring.compatReporter = matterCompatibilityReporter{
		fabrics:   mfs,
		inspector: wiring.endpointInspector,
	}
	// The pairing trace ring is attached by the facade before the bridge
	// serves its first datagram; here we only expose the bridge as the
	// REST surface's reporter.
	wiring.diagEvents = mb
	wiring.exposureStore = mids

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
	// completes: the boot-time assembly (the facade's Start) runs before
	// the async CCU load, so the persisted exposures would otherwise not
	// take effect (empty bridge → commissioning refused) until an
	// operator toggles one.
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

	// The commissioning window is the facade's: the operator's REST
	// window is the node's own window with the configured credentials,
	// or — in ephemeral-window mode — a per-window set of credentials the
	// facade installs for the window's lifetime (node.OpenBasicWindow).
	// A commissioner's OpenCommissioningWindow with a supplied verifier
	// (multi-admin) is served by the facade's verifier installer.
	window := bundle.node.CommissioningWindow()
	wiring.opener = &matterCommissioningOpenerAdapter{
		node:          bundle.node,
		bridge:        mb,
		commissioning: mcfg.Commissioning,
		discriminator: mcfg.Discriminator,
		vendorID:      mcfg.VendorID,
		productID:     mcfg.ProductID,
		configured:    bundle.configuredCommissioning,
	}
	// Wire the Reassemble → WS event emit pipeline so the SPA's
	// allowlist save flow gets a `matter.endpoint_assembled`
	// notification after the topology refresh completes. The StartUp /
	// BootReason events are the facade's (emitted by its Start).
	wiring.pub = &matterEventPublisher{hub: wsHub, fabrics: wiring.fabricStore}
	mb.SetOnReassembled(func(count int) { //nolint:contextcheck // callback signature is fixed; publishEndpointAssembled has no ctx
		wiring.pub.publishEndpointAssembled(count)
	})
	// The facade's fabric observers run after its own handling of AddNOC
	// / RemoveFabric (CASE identity, operational record, sessions); the
	// WebSocket frames follow. The bridge's SetOnFabricAdded is not the
	// seam: the facade never fires it.
	bundle.fabricEvents.set(wiring.pub.publishFabricAdded, wiring.pub.publishFabricRemoved)
	wiring.bi = bundle.root.BasicInformation
	wiring.statusReader = &matterStatusReaderAdapter{
		enabled:   mcfg.Enabled,
		bridge:    mb,
		store:     mfs,
		exposures: mids,
		window:    window,
		cfg: &matterStatusConfig{
			advertising: mcfg.MDNSAdvertise == "zeroconf",
		},
	}
	if healthTracker != nil {
		_ = startMatterHealthProbe(ctx, wiring.statusReader, healthTracker, matterHealthProbeInterval)
	}
	// The REST revoke and the factory reset remove a fabric through the
	// facade (node.RemoveFabric), which runs the RemoveFabric command's
	// own fan-out: the Leave event, the CASE identity and operational
	// record withdrawn, the sessions and subscriptions closed, the
	// node's own window reopened once the last fabric is gone, then the
	// observer above. Deleting the row alone left the removed
	// controller's session and subscription alive and the next pair
	// retry colliding on the same session id.
	revoker := &matterFabricRevokerAdapter{node: bundle.node, store: mfs}
	wiring.fabricRevoker = revoker
	wiring.fabricPurger = revoker
	wiring.reassembler = mb
	wiring.closer = &matterCommissioningCloserAdapter{window: window}
	wiring.candidates = &matterCandidateProviderAdapter{reg: reg, cfg: cfg}
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
		info.DeviceAddress = ep.DeviceAddress
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
