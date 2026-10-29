// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"crypto/sha256"
	gosql "database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SukramJ/go-fabric-ui/fabricui"
	"github.com/SukramJ/go-fabric/node"
	matterstore "github.com/SukramJ/go-fabric/store"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterTCPConfig maps config.NorthMatter.TCP onto the bridge's TCP
// roles. The validator restricts the value to the four words; the zero
// value (off) is what an unset key means.
func matterTCPConfig(mode string) node.TCPConfig {
	switch mode {
	case config.MatterTCPIncoming:
		return node.TCPConfig{Incoming: true}
	case config.MatterTCPOutgoing:
		return node.TCPConfig{Outgoing: true}
	case config.MatterTCPBoth:
		return node.TCPConfig{Incoming: true, Outgoing: true}
	}
	return node.TCPConfig{}
}

// matterMDNS selects the node's DNS-SD from mc.MDNSAdvertise. "noop" is an
// explicit opt-out; empty or "zeroconf" leaves the advertiser to the node
// facade, which builds Zeroconf with the subtype responder — the side-car
// that answers the `_L<long>._sub`, `_S<short>._sub`, `_V<vid>._sub` and
// `_CM._sub` browses Apple Home and Google Home filter by — and closes both
// at Stop. An unknown value falls back to the opt-out and says so.
func matterMDNS(mc config.NorthMatter, logger *slog.Logger) node.MDNS {
	switch mc.MDNSAdvertise {
	case "noop":
		// Explicit opt-out only. A commissioner can never discover a
		// bridge that does not advertise, so "quiet" must be a conscious
		// choice, not the unset default.
		return node.MDNS{Disabled: true}
	case "", "zeroconf":
		logger.Info("matter.bridge.mdns.zeroconf",
			slog.String("hint", "primary record via grandcat/zeroconf, subtypes via the facade's side-car responder"))
		return node.MDNS{}
	default:
		logger.Warn("matter.bridge.mdns.unknown",
			slog.String("value", mc.MDNSAdvertise),
			slog.String("hint", "falling back to noop; valid: noop|zeroconf"))
		return node.MDNS{Disabled: true}
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
	// daemon reaches through it is named by the fields below; the bridge
	// it runs on (Node.Bridge), its root servers (Node.Root) and its group
	// state (Node.Groups) are reached through it directly.
	node  *node.Node
	stop  func()
	store *matterstore.Store
	// identity persists endpoint ids and the exposure allowlist — the two
	// tables keyed by this daemon's source 5-tuple rather than by anything
	// Matter defines. See [matterendpoint].
	identity *matterendpoint.Store
	// assembler describes this daemon's device model as the endpoints the
	// node's Aggregator assembles; everything that configures the walk —
	// the exposure allowlist above all — lives on this side of the
	// boundary and is reached through here. sync describes the model and
	// applies the changes to the Aggregator (matteradapter.Reconciler);
	// retire collects a removed central's endpoint numbers; forget queues a
	// device the CCU deleted, so the next sync frees its numbers.
	assembler *matteradapter.Assembler
	sync      func(context.Context) error
	retire    func(ctx context.Context, centralName string) error
	forget    func(centralName, address string)
	// build is what every device source builds its Matter device under;
	// the assembler and the eligibility listing take the same value,
	// since a source keeps the device it built first.
	build interfaces.MatterBuildOptions
	// events is go-fabric-ui's event source: the daemon publishes its own
	// reassemblies into it, and the handler's SSE stream and the WebSocket
	// bridge (wireMatterNodeEvents) read from it. The node's fabric and
	// window transitions do not pass through it: each SSE stream and the
	// bridge observe the node themselves (node.Node.Observe).
	events *fabricui.Events
	// configuredCommissioning reports whether the operator configured a
	// valid setup passcode. Without one the operator's window runs on
	// credentials generated for it alone (fabricui.Options.ConfiguredWindow
	// false); the node itself always has credentials
	// ([matterCommissioningCredentials]).
	configuredCommissioning bool
	// commissioningBlocked is why the bridge admits no new controller, nil
	// when it does. A configured attestation chain that does not load sets
	// it (Node.AttestationError), and the node carries the same block:
	// the bridge keeps serving every controller already paired — the chain
	// is checked only while commissioning — but must not pair a new one as
	// the CSA test vendor while the operator believes the production chain
	// is served.
	commissioningBlocked error
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
// `uniqueId !== serialNumber` (basic-information-validators.ts:42) holds.
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
// salt of the PBKDF size, whereas the daemon runs without a configured
// passcode — the operator pairs through the ephemeral window, or not at
// all. A missing or invalid passcode becomes a random one for this
// process: the node's own window at first boot then runs on credentials
// nobody holds, and the operator's window runs on fresh credentials of its
// own (configured is false). A salt outside the PBKDF
// bounds becomes a random one, which changes nothing a commissioner sees:
// the salt travels in PBKDFParamResponse, and the verifier is recomputed
// from passcode and salt at every boot.
func matterCommissioningCredentials(cm config.NorthMatterCommissioning, logger *slog.Logger) (passcode uint32, salt []byte, iterations int, configured bool, err error) {
	passcode, configured = cm.Passcode, true
	salt = []byte(cm.Salt)
	validPasscode := passcode != 0 && passcode <= matterSetupPasscodeMax && node.ValidateSetupPIN(passcode) == nil
	validSalt := len(salt) >= node.PBKDFSaltMin && len(salt) <= node.PBKDFSaltMax
	if validPasscode && validSalt {
		return passcode, salt, cm.Iterations, configured, nil
	}
	fresh, err := node.NewCommissioningCredentials()
	if err != nil {
		return 0, nil, 0, false, fmt.Errorf("random credentials: %w", err)
	}
	if !validPasscode {
		if cm.Passcode != 0 {
			logger.Warn("matter.bridge.pase.passcode_invalid",
				slog.String("hint", "north.matter.commissioning.passcode is out of range or on the trivially-guessable list; the configured-credentials window is disabled until it is fixed"))
		}
		passcode, configured = fresh.Passcode, false
	}
	if !validSalt {
		if cm.Salt != "" {
			logger.Warn("matter.bridge.pase.salt_invalid",
				slog.Int("len", len(salt)),
				slog.Int("min", node.PBKDFSaltMin),
				slog.Int("max", node.PBKDFSaltMax),
				slog.String("hint", "a random salt is used for this process; the commissioner receives it in PBKDFParamResponse"))
		}
		salt = fresh.Salt
	}
	return passcode, salt, cm.Iterations, configured, nil
}

// matterAttestation is the device attestation the node is built with:
// the operator's DAC / PAI / CD files when configured, else the facade's
// CSA test chain for the vendor and product — pairable with every
// commissioner's test PAA (Apple Home, Google Home and chip-tool accept it
// without --bypass-attestation-verifier), and not shippable.
//
// The node loads the files (node.Attestation.Files). A configured chain
// that does not load — a path missing, a file unreadable, a key that is
// not the DAC's — keeps the node running on the test chain with every
// commissioning refused (BlockCommissioningOnError), and the error is
// Node.AttestationError: attestation is checked only while a controller
// commissions, so every controller already paired keeps working, and no
// new one is paired as the CSA test vendor while the operator believes the
// production chain is served. Files that name nothing select the test
// chain.
func matterAttestation(mc config.NorthMatter, logger *slog.Logger) node.Attestation {
	a := mc.Attestation
	if a.DACPath == "" && a.DACKeyPath == "" && a.PAIPath == "" && a.CDPath == "" {
		logger.Info("matter.bridge.attestation.csa_test",
			slog.String("hint", "CSA Test PAA chain (PAA→PAI→DAC) + signed CD — Apple Home / Google Home / chip-tool accept this without --bypass-attestation-verifier"),
			slog.String("vendor_id", fmt.Sprintf("0x%04X", mc.VendorID)),
			slog.String("product_id", fmt.Sprintf("0x%04X", mc.ProductID)))
		return node.Attestation{}
	}
	return node.Attestation{
		Files: &node.AttestationFiles{
			DACPath:    a.DACPath,
			DACKeyPath: a.DACKeyPath,
			PAIPath:    a.PAIPath,
			CDPath:     a.CDPath,
		},
		BlockCommissioningOnError: true,
	}
}

// startMatterBridge constructs and starts the Matter bridge when
// matter.enabled is set: the daemon's own responsibilities — the enable
// and database gate, the endpoint-identity and exposure store, the
// localized channel word, the topology assembler and its reconciler, the
// attestation material, the health
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
func startMatterBridge(ctx context.Context, cfg *config.Config, reg *central.Registry, db *gosql.DB, healthTracker *health.Tracker, labels device.ParameterTranslator, logger *slog.Logger, schedules interfaces.ClimateScheduleWriter) *matterBridgeBundle { //nolint:funlen // composition/wiring: long sequential setup
	if cfg == nil || !cfg.North.Matter.Enabled {
		return nil
	}
	if db == nil {
		logger.Warn("matter.bridge.db_unavailable", slog.String("hint", "shared openccu-loom.db handle is nil; Matter bridge disabled"))
		return nil
	}

	// The credential store is built on the daemon's shared handle: the
	// daemon carries go-fabric's schema in its own migrations, so the node
	// is handed the store rather than the handle it would apply the schema
	// to (go-fabric guides "Advanced seams", store).
	store := matterstore.New(db)
	// Endpoint identity and the exposure allowlist are keyed by this
	// daemon's own 5-tuple, so they are persisted here rather than by the
	// bridge module — which sees only an opaque key. Same database handle,
	// same migrations; two access surfaces because two owners.
	identity := matterendpoint.New(db)

	// Single defaulting point: the SAME defaulted view feeds the facade
	// here and the status reader in wireMatterRuntime. See
	// config.NorthMatter.WithDefaults.
	mc := cfg.North.Matter.WithDefaults()
	if mc.DevRotateUniqueIDs {
		// Dev-mode: rotate every bridged endpoint's UniqueID at boot so
		// pair iteration cycles (chip-tool brief T11, Apple HMHome cache
		// recovery) see a fresh identity each daemon start. Disabled by
		// default — production Apple Home / Google Home accessory
		// recognition expects a stable UniqueID across restarts. The node
		// enables the rotation (node.Config.BootID) before anything
		// derives a UniqueID.
		logger.Info("matter.bridge.bootid_rotation_enabled",
			slog.String("hint", "per-boot UniqueID salt active — accessory recognition will fail across restarts; toggle north.matter.dev_rotate_unique_ids=false for production"))
	}

	// The boot-time assembly (the facade's Start) runs before the
	// readiness-gated CCU device load, so every registered central first
	// describes no device. That costs no endpoint number: the Aggregator
	// keeps a stored number until the key is forgotten, so the fleet comes
	// back under its numbers once the devices load.
	walk := matterSnapshotter(reg)

	fail := func(event string, err error) *matterBridgeBundle {
		logger.Warn(event, slog.String("err", err.Error()))
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
	rootSerial := matterRootSerial(mc)
	passcode, salt, iterations, configured, err := matterCommissioningCredentials(mc.Commissioning, logger)
	if err != nil {
		return fail("matter.bridge.pase.credentials", err)
	}
	attest := matterAttestation(mc, logger)
	uiEvents := fabricui.NewEvents()
	n, err := node.New(node.Config{
		Network: node.Network{
			Listen:     mc.Listen,
			PreferIPv4: mc.PreferIPv4,
			TCP:        matterTCPConfig(mc.TCP),
			MDNS:       matterMDNS(mc, logger),
		},
		Commissioning: node.Commissioning{
			Passcode:           passcode,
			Discriminator:      mc.Discriminator,
			PBKDFIterations:    iterations,
			Salt:               salt,
			LocationCapability: node.RegulatoryIndoorOutdoor,
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
			// UniqueID stays empty: the facade pins it (quality F) on the
			// first boot of the store and serves the stored value on every
			// later one (Node.UniqueID), so a node_label edit never
			// re-identifies the bridge under a paired controller's cache;
			// under dev_rotate_unique_ids it serves a per-boot value and
			// pins nothing.
			HardwareVersion:       1,
			HardwareVersionString: "1.0",
			SoftwareVersion:       node.SoftwareVersionFromString(build.Version),
			SoftwareVersionString: build.Version,
		},
		Store:                      node.Store{Credentials: store, Endpoints: identity},
		Attestation:                attest,
		BootID:                     node.BootID{RotateUniqueIDs: mc.DevRotateUniqueIDs},
		Health:                     matterHealth(healthTracker),
		Locales:                    matterLocales(cfg.Locale),
		SupportedFabrics:           matterSupportedFabrics,
		DisableTimeSynchronization: !mc.TimeSyncEnabled(),
		Logger:                     logger,
	})
	if err != nil {
		return fail("matter.bridge.node", err)
	}
	// The daemon owns the device walk: it knows the device model, the
	// operator's exposure allowlist and the locale, none of which the
	// Matter subtree may see. The node assembles what the walk describes:
	// its Aggregator holds the endpoints, one partition per central, and
	// the reconciler keeps it in step with the model. A device the CCU
	// deleted is forgotten through the endpoint store, which finds its
	// stored keys by address. Group membership, scene tables and
	// commissioner-written NodeLabels are the node's own state there.
	// A thermostat's week programs reach the CCU through the schedules
	// domain the REST schedule editor writes through.
	matterBuild := interfaces.MatterBuildOptions{
		ThermostatHoldAndRunningState: mc.ThermostatHoldAndRunningState,
		ThermostatSchedules:           mc.ThermostatSchedules,
		ClimateSchedules:              schedules,
	}
	assembler := matteradapter.New(matteradapter.Config{
		ExposeSecondaryChannels: mc.ExposeSecondaryChannels,
		IncludeMeasurements:     mc.IncludeMeasurements,
		Labels:                  labels,
		ChannelLabel:            channelLabel,
		Build:                   matterBuild,
	}, logger)
	reconciler := matteradapter.NewReconciler(n.Aggregator(), assembler, identity, logger)
	syncModel := func(ctx context.Context) error { return reconciler.Sync(ctx, walk(ctx)) }
	// The allowlist checker is wired before the first sync, so the
	// bridge only ever serves sources the operator has explicitly
	// enabled. Default = empty allowlist = empty topology. The
	// exposure selection (go-fabric-ui's `/api/v1/node/exposure` over
	// loom's exposure provider) drives the matter_exposures table the
	// checker reads through.
	// Without it Apple Home would see every CCU device as a Matter
	// endpoint (1000+), the initial Subscribe would expand to 60+ KB,
	// the post-CASE phase would exceed Apple's pairing-UI timeout and
	// the user would see a generic add-failed error even though the
	// cryptographic handshake completed.
	assembler.SetExposureChecker(identity)
	// The boot-time set: every central the registry holds now; one whose
	// devices have not loaded yet describes nothing and keeps its stored
	// numbers. Start assembles the set; later syncs ride the reassembly
	// triggers.
	if err := syncModel(ctx); err != nil {
		logger.Warn("matter.bridge.sync.boot", slog.String("err", err.Error()))
	}

	if mc.TimeSyncEnabled() {
		logger.Warn("matter.bridge.time_sync.mounted — TimeSynchronization (0x0038) advertised on RootNode by operator opt-in; some controllers (e.g. Apple Home) may reject pairing")
	}
	bridge := n.Bridge()
	attestErr := n.AttestationError()
	if attestErr != nil {
		// The bridge still starts on the test chain with commissioning
		// refused — every window: the node's own, the operator's and a
		// paired controller's AdministratorCommissioning (multi-admin).
		logger.Error("matter.bridge.attestation",
			slog.String("err", attestErr.Error()),
			slog.String("hint", "the configured north.matter.attestation chain does not load; paired controllers keep working, new commissioning is disabled until the files are fixed and the daemon restarts"))
	} else if attest.Files != nil {
		logger.Info("matter.bridge.attestation.vendor",
			slog.String("hint", "production DAC/PAI/CD loaded from config — chip-tool can validate without --bypass-attestation-verifier"))
	}
	if err := n.Start(ctx); err != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = n.Stop(stopCtx)
		cancel()
		return fail("matter.bridge.start", err)
	}
	logger.Info("matter.bridge.up",
		slog.String("addr", n.LocalAddr()),
		slog.Bool("ipv4_only", mc.PreferIPv4),
		slog.Bool("configured_passcode", configured))

	// The node's health probe (node.Config.Health, matterHealth) reports
	// the `matter` component alongside `mqtt` and `sqlite` so the
	// Diagnostics surface shows every long-running subsystem on one row
	// each. Best-effort: a nil tracker runs no probe and no gauges.
	if healthTracker != nil {
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
		// The facade's Stop runs the daemon's shutdown order: the
		// operational time stored once more, BasicInformation ShutDown,
		// the PASE providers, the CASE reaper, the subscription engine,
		// the DNS-SD re-announce loop, the bridge (mDNS goodbyes on the
		// wire) and the advertiser with its subtype responder.
		if err := n.Stop(stopCtx); err != nil {
			logger.Warn("matter.bridge.stop", slog.String("err", err.Error()))
		}
	}
	return &matterBridgeBundle{
		node:                    n,
		stop:                    stop,
		store:                   store,
		identity:                identity,
		assembler:               assembler,
		sync:                    syncModel,
		retire:                  reconciler.Retire,
		forget:                  reconciler.QueueForget,
		build:                   matterBuild,
		events:                  uiEvents,
		configuredCommissioning: configured,
		commissioningBlocked:    attestErr,
	}
}

// matterWiring carries what wireMatterRuntime hands the rest of the
// composition root. All fields are zero when the bridge is disabled.
type matterWiring struct {
	// nodeUI are go-fabric-ui's options for the node page family's API
	// (rest.Deps.NodeUI): the node, loom's exposure provider, the
	// reassemble hook and the event source. The router sets Prefix and
	// Admin.
	nodeUI fabricui.Options
	// status is the daemon-side status seam the MCP tool and the health
	// probe read; the REST status comes from go-fabric-ui over the node.
	status *matterStatusReaderAdapter
	// exposureStore is the concrete allowlist store: the live-adopt
	// orchestrator needs DeleteForCentral (matterExposureStore /
	// setMatterExposureStore in central_adopt.go), the exposure provider
	// reads and writes rows through it.
	exposureStore *matterendpoint.Store
	// retire collects a removed central's Matter endpoint numbers
	// (matteradapter.Reconciler.Retire); the live-remove orchestrator
	// calls it after the central's rows are purged.
	retire func(ctx context.Context, centralName string) error
	// centralHook wires a runtime-adopted central into the running bridge
	// (reassemble-on-ready, hot-plug lifecycle with the forget of a deleted
	// device, reachable forward). Nil when the bridge is disabled; the
	// live-adopt orchestrator skips it then.
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
// removal and the PartsList subscription notification. Events arriving
// before the central is southbound-ready are skipped: the boot ingest
// fires one DeviceCreatedEvent per device and the ready trigger already
// covers that batch with a single reassemble. Create events are
// at-least-once (the CCU re-announces its inventory on reconnect); the
// debounce coalesces such bursts and a no-change reassemble is cheap
// in-memory work.
//
// A device the CCU deleted is also handed to forget
// ([matteradapter.Reconciler.QueueForget]) before the trigger fires, so
// the sync it causes frees the device's endpoint numbers; a device that
// is merely not described keeps them. The cache-clear re-init's teardown
// removes every device without the operator deleting any
// ([hmevent.DeviceRemovedEvent.ModelTeardown]) and forgets none. forget
// may be nil. Returns the unsubscribe closers (empty when nothing to
// wire).
func subscribeMatterDeviceLifecycleTrigger(u *central.Unit, trigger func(), forget func(centralName, address string)) []func() {
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
		u.EventBus.Subscribe(func(e hmevent.DeviceRemovedEvent) {
			if forget != nil && !e.ModelTeardown {
				forget(e.CentralName, e.Address)
			}
			fire()
		}),
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

// matterReassembler is the node page family's topology reassembly
// (fabricui.Options.Reassemble, run by POST /node/maintenance/reassemble
// and after every exposure change): a sync of the Aggregator with the
// model, as every reassembly trigger runs, followed by an audit entry
// naming the request's principal. After an exposure change the ledger
// therefore shows the exposure batch and the reassembly it caused, in
// that order.
func matterReassembler(syncModel func(context.Context) error, rec audit.Recorder) func(context.Context) error {
	return func(ctx context.Context) error {
		err := syncModel(ctx)
		if rec != nil {
			if id, ok := auth.IdentityFrom(ctx); ok {
				note := "ok"
				if err != nil {
					note = "error: " + err.Error()
				}
				rec.Record(audit.Entry{User: id.Subject, Action: audit.ActionMatterForceSync, Note: note})
			}
		}
		return err
	}
}

// matterSnapshotter builds the model walk behind the reconciler: one
// [matteradapter.DeviceSnapshot] per registered central, read live from
// the central registry so runtime-added centrals surface on the next
// sync.
func matterSnapshotter(reg *central.Registry) func(context.Context) []matteradapter.DeviceSnapshot {
	return func(_ context.Context) []matteradapter.DeviceSnapshot {
		var out []matteradapter.DeviceSnapshot
		for _, u := range reg.List() {
			if u == nil || u.ModelRegistry == nil {
				continue
			}
			out = append(out, matteradapter.DeviceSnapshot{
				CentralName: u.Name(),
				Devices:     releasedDevicesOf(u),
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
type matterCentralHook func(u *central.Unit) (unwire func())

// newMatterCentralHook builds the per-central Matter wiring hook from the
// bridge-start artefacts.
func newMatterCentralHook(
	reassembleTrigger func(),
	forget func(centralName, address string),
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
		unsubs = append(unsubs, subscribeMatterDeviceLifecycleTrigger(u, reassembleTrigger, forget)...)
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
// the daemon's adapters over it: go-fabric-ui's options for the node page
// family's API, the WebSocket bridge of its events, the MCP / health
// status seam, the reassemble-on-ready and hot-plug triggers, the
// reachability forward and the live-adopt hook. What the node facade wires
// itself — the commissioning window with its fail-safe, verifier installer
// and CM=2 announcement, the PartsList providers, the StartUp / BootReason
// events, the fabric fan-out — is not repeated here (ADR 0084 phase 3).
func wireMatterRuntime(ctx context.Context, cfg *config.Config, reg *central.Registry, db *gosql.DB, healthTracker *health.Tracker, labels device.ParameterTranslator, catalogs *i18n.Catalogs, auditRec audit.Recorder, logger *slog.Logger, wsHub *ws.Hub, schedules interfaces.ClimateScheduleWriter) (wiring matterWiring, closers []func(), teardown func()) {
	teardown = func() {}
	bundle := startMatterBridge(ctx, cfg, reg, db, healthTracker, labels, logger, schedules)
	if bundle == nil {
		return wiring, closers, teardown
	}
	// Defaulted view of the Matter config — the identical view
	// startMatterBridge fed the facade. The status reader below MUST
	// consume the same values; mixing raw and defaulted fields used to
	// publish discriminator 0 while the bridge core held the 0xF00 default.
	mcfg := cfg.North.Matter.WithDefaults()
	mb := bundle.node.Bridge()
	mids := bundle.identity
	teardown = bundle.stop
	wiring.exposureStore = mids
	wiring.retire = bundle.retire

	// Wire CCU device-availability → BridgedDeviceBasicInformation
	// Reachable. WireDeviceAvailability (above) publishes a
	// DeviceLifecycleEvent{AvailabilityChanged} on each central's bus
	// whenever a device's effective availability flips (interface
	// disconnect, STICKY_UNREACH, reconnect). Forward that to the
	// bridge so the matching bridged endpoint fires the §9.13.6
	// ReachableChanged event — without this Apple/Google always see
	// every bridged device as reachable even when the CCU device is
	// dead. The Reachable attribute itself reads the endpoint's
	// Availability hook — the assembler hands it dev.Available — live
	// per dispatch (go-fabric endpoint/materialize.go:198); this supplies the push
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
	reassembleClosers, reassembleTrigger := wireMatterReassembleOnReady(ctx, readyBuses, bundle.sync, matterReassembleReadyDebounce, logger)
	closers = append(closers, reassembleClosers...)
	// Hot-plug: device create/remove events on a ready central feed the
	// same debounced reassemble so runtime-paired (or deleted) CCU
	// devices appear as (or vanish from) bridged endpoints live.
	for _, u := range reg.List() {
		closers = append(closers, subscribeMatterDeviceLifecycleTrigger(u, reassembleTrigger, bundle.forget)...)
	}
	// Per-central hook for the live-adopt orchestrator: a runtime-added
	// central gets the same reassemble-on-ready and reachable-forward
	// wiring as the boot-time centrals above.
	wiring.centralHook = newMatterCentralHook(reassembleTrigger, bundle.forget, mb.NotifyDeviceReachable)

	// Every reassembly — the ready trigger, hot-plug, the operator's
	// selection, the maintenance route — ends in the event source, so the
	// node pages reload their endpoint list and the WebSocket hub carries
	// `node.endpoints_changed`. The StartUp / BootReason events are the
	// facade's (emitted by its Start).
	mb.SetOnReassembled(func(int) {
		bundle.events.Publish(fabricui.Event{Type: fabricui.EventEndpointsChanged, Data: struct{}{}})
	})
	closers = append(closers, wireMatterNodeEvents(bundle.events, bundle.node, wsHub, auditRec))

	wiring.status = &matterStatusReaderAdapter{
		enabled:   mcfg.Enabled,
		node:      bundle.node,
		exposures: mids,
		window:    bundle.node.CommissioningWindow(),
		cfg: &matterStatusConfig{
			advertising: mcfg.MDNSAdvertise == "zeroconf",
		},
	}
	if healthTracker != nil {
		_ = startMatterHealthProbe(ctx, wiring.status, bundle.commissioningBlocked, healthTracker, matterHealthProbeInterval)
	}

	// The node page family's API. The exposure provider is loom's: which
	// CCU channels are exposable and how they are named stays here; the
	// pages render what it describes. The commissioning window is the
	// facade's: with a configured passcode the operator's window is the
	// node's own window on the configured credentials, otherwise — and in
	// ephemeral-window mode — a window on credentials generated for it
	// alone (node.OpenBasicWindow), whose codes GET /node/pairing serves
	// while it is open. A broken attestation chain refuses every window
	// through the facade (node.ErrCommissioningBlocked), as it refuses a
	// commissioner's AdministratorCommissioning.
	candidates := &matterCandidateProviderAdapter{reg: reg, cfg: cfg, build: bundle.build}
	wiring.nodeUI = fabricui.Options{
		Node: bundle.node,
		Exposure: matteradapter.NewExposureProvider(matteradapter.ExposureProviderConfig{
			Candidates: candidates.MatterCandidates,
			Store:      mids,
			Labels:     matterParameterLabels{labels},
			Translate:  matterColumnTranslator(catalogs, cfg.Locale),
			Actor: func(ctx context.Context) string {
				if id, ok := auth.IdentityFrom(ctx); ok {
					return id.Subject
				}
				return ""
			},
			Audit: auditRec,
		}),
		Reassemble:       matterReassembler(bundle.sync, auditRec),
		Events:           bundle.events,
		Logger:           logger,
		ConfiguredWindow: bundle.configuredCommissioning && !mcfg.Commissioning.EphemeralWindow,
	}
	return wiring, closers, teardown
}

// matterColumnTranslator renders the exposure provider's column headers
// from the daemon's catalogue in the configured locale; without a
// catalogue (a test wiring) the keys are shown as they are.
func matterColumnTranslator(catalogs *i18n.Catalogs, locale string) func(string) string {
	return func(key string) string {
		if catalogs == nil {
			return key
		}
		return catalogs.T(locale, key)
	}
}

// matterParameterLabels adapts the daemon's parameter translator to the
// exposure provider's labeler: the channel-typed translation, or empty.
type matterParameterLabels struct{ t device.ParameterTranslator }

func (l matterParameterLabels) ChannelTypedParameterLabel(channelType, parameter string) string {
	if l.t == nil {
		return ""
	}
	if s, ok := l.t.ChannelTypedParameterLabelOk(channelType, parameter); ok {
		return s
	}
	return ""
}

// matterHealthRecorder adapts the daemon's health tracker to the slim
// sample type the Matter bridge probe speaks. The probe reports only a
// verdict and a stable English note; the tracker's richer sample (catalogue
// key, timestamp, staleness exemption) is filled in here, on the host side,
// so the Matter subtree needs no dependency on the tracker package.
type matterHealthRecorder struct{ t *health.Tracker }

func (r matterHealthRecorder) Record(name string, s node.HealthSample) {
	r.t.Record(name, health.Sample{Healthy: s.Healthy, Note: s.Note})
}

func (r matterHealthRecorder) RecordUnhealthy(name string, s node.HealthSample) {
	r.t.RecordUnhealthy(name, health.Sample{Healthy: s.Healthy, Note: s.Note})
}

// matterHealthComponent is the tracker component the probe reports under,
// the name the bridge's own probe uses (go-fabric
// bridge/health_probe.go:17, HealthComponentName).
const matterHealthComponent = "matter"

// onSample routes one verdict of the node's probe: an unhealthy one is a
// condition the probe states rather than a sample, recorded at once past
// the tracker's flap damping, as the bridge's probe recorded it
// (bridge/health_probe.go:113-129).
func (r matterHealthRecorder) onSample(s node.HealthSample) {
	if s.Healthy {
		r.Record(matterHealthComponent, s)
		return
	}
	r.RecordUnhealthy(matterHealthComponent, s)
}

// matterHealth configures the node's health probe at the default cadence
// over t; a nil tracker runs none.
func matterHealth(t *health.Tracker) node.Health {
	if t == nil {
		return node.Health{}
	}
	return node.Health{OnSample: matterHealthRecorder{t}.onSample}
}
