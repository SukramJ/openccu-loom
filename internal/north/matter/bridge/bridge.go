// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package bridge orchestrates the Matter bridge runtime: endpoint
// topology assembly, IM dispatcher wiring, UDP listener, and mDNS
// advertisement. The bridge is the single composition unit the
// daemon's bootstrap turns on when matter.enabled is set in config.
//
// Layering: bridge depends on every other Matter sub-package
// (endpoint, transport/udp, mdns, im) but on no daemon-internal
// types — the model snapshot is supplied via the [Snapshotter]
// callback so this package stays test-friendly without importing
// internal/central.
//
// Lifecycle: [New] → [Start] → (optional) [Reassemble] → [Stop].
// Start is idempotent (returns ErrAlreadyStarted on a second call).
// Stop is idempotent and safe to call after a failed Start.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/udp"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Errors.
var (
	// ErrNotStarted surfaces when methods that require a running bridge
	// are called before [Start] (or after [Stop]).
	ErrNotStarted = errors.New("bridge: not started")
	// ErrAlreadyStarted surfaces from a second [Start] call on the
	// same bridge instance.
	ErrAlreadyStarted = errors.New("bridge: already started")
)

// Snapshotter returns the current model snapshots for endpoint
// topology assembly. Typically wraps the central registry walk:
//
//	func(ctx context.Context) []endpoint.Snapshot {
//	    var out []endpoint.Snapshot
//	    for _, c := range reg.List() {
//	        out = append(out, endpoint.Snapshot{
//	            CentralName: c.Name(),
//	            Devices:     c.ModelRegistry.List(),
//	        })
//	    }
//	    return out
//	}
//
// The bridge calls Snapshotter once at [Start] and on every
// [Reassemble]; it never caches the result — every call gets a
// fresh snapshot.
type Snapshotter func(ctx context.Context) []endpoint.Snapshot

// Config bundles the bridge's identity and listener parameters. All
// fields are validated at [New] time.
type Config struct {
	// Listen is the UDP bind address. Empty defaults to ":5540" via
	// the udp package's MatterPort default.
	Listen string

	// PreferIPv4 forces an IPv4-only socket. Defaults to dual-stack
	// IPv6 (which also accepts IPv4 traffic).
	PreferIPv4 bool

	// VendorID is the bridge's IANA-assigned vendor identifier
	// (BasicInformation.VendorID, Matter §11.1.5.2). Mandatory.
	VendorID uint16

	// ProductID is the bridge's vendor-assigned product identifier
	// (BasicInformation.ProductID, Matter §11.1.5.4). Mandatory.
	ProductID uint16

	// NodeLabel is the bridge's user-visible label
	// (BasicInformation.NodeLabel, Matter §11.1.5.6). Mandatory.
	NodeLabel string

	// Discriminator is the 12-bit Matter commissioning discriminator
	// (Matter §5.1.3.1). Surfaces in the commissionable mDNS record;
	// commissioner UIs use it to pre-filter candidates. Bits above
	// the 12-bit limit are masked off at advertise time.
	Discriminator uint16

	// AdvertiseTimeout caps the per-call timeout for mDNS publish /
	// withdraw operations. Defaults to 5 seconds when zero. Each
	// individual call is given its own context derived from the
	// caller's; no global ticker is involved.
	AdvertiseTimeout time.Duration

	// IncludeMeasurements passes [endpoint.Config.IncludeMeasurements]
	// through to the assembler so standalone sensor endpoints (Temperature,
	// Humidity, …) are created from [interfaces.MatterMeasurementSource]
	// DPs. Off by default; operators turn it on with
	// `north.matter.include_measurements`.
	//
	// The eligibility surface reports these DPs as mappable whatever this
	// flag says, because it answers what the model can project rather than
	// what the current configuration assembles. The two must not be
	// conflated: for one release the flag had no config key at all, so
	// every derived sensor an operator allowlisted was offered, accepted
	// and then silently dropped here.
	IncludeMeasurements bool

	// ExposeSecondaryChannels passes [endpoint.Config.ExposeSecondaryChannels]
	// through to the assembler. Off by default: a multi-channel HmIP actor
	// (switch / dimmer / cover / lock / siren / valve) projects a single
	// Matter endpoint from its primary channel instead of duplicating the
	// physical device across its status transmitter and virtual-receiver
	// actor channels. Sourced from [config.NorthMatter.ExposeSecondaryChannels].
	ExposeSecondaryChannels bool

	// Labels passes [endpoint.Config.Labels] through to the assembler:
	// the daemon-locale-bound parameter translator used for the
	// NodeLabel suffix of measurement sub-endpoints. Nil is tolerated
	// (title-cased parameter fallback).
	Labels device.ParameterTranslator
	// Locale passes [endpoint.Config.Locale] through: the language of the
	// NodeLabel channel-number fallback ("Channel N" / "Kanal N").
	Locale string
}

// validate reports whether the config carries every required field.
// Mirrors endpoint.Config.Validate so callers see one consistent
// failure mode.
func (c Config) validate() error {
	if c.VendorID == 0 {
		return errors.New("bridge: Config.VendorID must be non-zero")
	}
	if c.ProductID == 0 {
		return errors.New("bridge: Config.ProductID must be non-zero")
	}
	if c.NodeLabel == "" {
		return errors.New("bridge: Config.NodeLabel must be non-empty")
	}
	return nil
}

// Bridge owns the live Matter runtime: the assembled topology, the
// dispatcher routing IM Read/Write/Invoke into cluster servers, the
// UDP listener accepting Matter datagrams, and the mDNS advertiser
// announcing the bridge to commissioners.
//
// Concurrency: Bridge is safe for concurrent use after Start. The
// topology / dispatcher pair is swapped atomically on [Reassemble]
// so in-flight IM dispatches see a consistent view.
type Bridge struct {
	cfg         Config
	store       endpoint.Store
	aclLister   endpoint.ACLLister // ACL source for the dispatcher's CheckACL; nil denies every operational request
	snapshotter Snapshotter
	logger      *slog.Logger
	advertiser  mdns.Advertiser
	assembler   *endpoint.Assembler

	mu         sync.RWMutex
	listener   *udp.Listener
	topology   *endpoint.Topology
	dispatcher *endpoint.TopologyDispatcher

	// rootClusters carry the cluster servers the daemon attached to
	// endpoint 0 (BasicInformation, GeneralCommissioning, …). Held on
	// the bridge so [Bridge.Reassemble] can re-stamp them onto each
	// freshly assembled root endpoint without the daemon re-attaching.
	rootClusters []interfaces.MatterClusterServer
	// aggregatorClusters carry the cluster servers attached to the
	// Aggregator endpoint (EP 1) — Descriptor (mandatory) + optional
	// Identify. Apple-bridge topology fix: EP 1 hosts the
	// Aggregator device type and its Descriptor.PartsList enumerates
	// the bridged children. Mirrors matter.js's
	// `examples/device-bridge-onoff/src/BridgedDevicesNode.ts`.
	aggregatorClusters []interfaces.MatterClusterServer
	sessions           SessionLookup
	paseHandler        PaseHandler
	paseProvider       PaseHandlerProvider // optional; wins over paseHandler when set
	caseHandler        CaseHandler
	caseProvider       CaseHandlerProvider // optional; wins over caseHandler when set
	ackHandler         AckHandler
	ackTracker         *mrp.AckTracker // optional; when set the pump goroutine runs
	// diagEvents records the moments that explain a failed pairing.
	// Optional: a nil ring drops every record, which is what keeps the
	// receive path safe when nothing wired one.
	//
	// Atomic because the record calls sit on the per-datagram receive
	// path, which runs concurrently with every attach: a plain field
	// would be a data race the moment a ring is attached to a serving
	// bridge, and taking b.mu per datagram would put the whole receive
	// path behind the bridge's topology lock.
	diagEvents atomic.Pointer[diagevent.Ring]
	subManager *subscription.Manager // optional; when set Subscribe is fully wired

	// measurementUnsubscribers holds the unsubscribe closures returned
	// by [interfaces.MatterChangeNotifier.OnMatterValueChanged] for
	// every bridged endpoint whose source pushes value changes. Called
	// and cleared at the start of each reassemble + when a fresh
	// subscription manager is attached, then re-populated by
	// [Bridge.wireMeasurementListenersLocked]. Without re-wiring on
	// reassemble the listeners would either leak (no cleanup) or point
	// at stale topology entries (cleanup but no re-bind).
	measurementUnsubscribers []func()
	// switchUnsubscribers holds the unsubscribe closures returned by
	// [generic.ButtonGroup.WireMatterSwitchHandler] (via the
	// matterSwitchSubscribable assertion) for every bridged
	// GenericSwitch endpoint. Drained at the start of each reassemble:
	// the wiring registers update hooks on the underlying press DP(s),
	// and without teardown the hooks would accumulate across
	// reassembles — every physical press would then emit one duplicate
	// Matter event per prior topology rebuild.
	switchUnsubscribers []func()
	handlerCtx          context.Context // cancelled at Stop so in-flight IM handlers unwind
	serveCancel         context.CancelFunc
	serveDone           chan struct{}
	pumpCancel          context.CancelFunc // optional; nil when no AckTracker wired
	pumpDone            chan struct{}
	started             bool

	// routing bundles the exchange-src / timed-deadline / sub-target /
	// status-response-wait tables the receive, ack-pump, and subscribe
	// pipelines share. See [exchangeRouting] for the per-table
	// semantics and lifecycle.
	routing exchangeRouting

	// outboundReliable bookkeeps reliable outbound messages we sent
	// (subscription reports today; future command responses
	// tomorrow). Inbound HasAck=true datagrams clear the matching
	// counter; the ACK pump goroutine ticks the tracker periodically
	// to re-send unacked entries per Matter §4.12.6. nil when no
	// AckTracker is wired (test paths).
	outboundReliable *outboundReliableTracker

	// sigma1Replied tracks the SHA-256 hash of the Sigma1 payload we
	// already produced a Sigma2 reply for, keyed by exchangeID. Apple
	// iOS multicasts the same Sigma1 onto IPv4 + IPv6-LL + IPv6-Global
	// simultaneously, so the same exchange receives 5 identical Sigma1
	// datagrams in rapid succession. Bug A (sigma.Responder mutex +
	// equality cache) ensures every parallel handler invocation returns
	// byte-identical Sigma2 bytes — but `sendReply` still fires for
	// every invocation, putting 5 Sigma2 datagrams on the wire. Apple
	// processes the first, advances to AwaitingStatusReport, sends
	// Sigma3, and then receives our late Sigma2 copies in state 3 with
	// `CASESession.cpp:2507: CHIP Error 0x0000002A: Invalid message
	// type` → commissioning aborts with "General error: 42".
	//
	// This map gates `sendReply` in the SC-router's Sigma1 branch: the
	// first Sigma1 arrival on an exchange records its payload hash and
	// fires the reply; subsequent arrivals on the same exchange with
	// matching hash log + drop. Sigma3 receive forgets the exchange so
	// re-use under the same exchange-id-after-rollover stays correct.
	// Mirrors matter.js's `CaseServer.ts::onSigma1` which serialises +
	// short-circuits replays via `fabric.locked` + per-exchange state.
	sigma1Replied map[uint16][32]byte

	// reportCounterOwner maps an outbound-reliable MessageCounter to
	// the subscription ID that owns it. Populated by
	// reportSubscription right after tracker.Track; consumed when the
	// ACK pump observes [mrp.ErrMaxRetransmissionsReached] so the
	// peer-vanished subscription can be reaped from the manager
	// instead of staying live and burning ticks forever. Cleared on
	// successful Ack via [Bridge.releaseReportCounter].
	reportCounterOwner sync.Map // map[uint32]uint32  (counter → subscriptionID)

	// subSendErrorCount tracks how many consecutive send failures the
	// ongoing report path has accumulated per subscription. matter.js's
	// ServerSubscription.ts retries an ongoing report up to 2 times
	// before cancelling; this counter implements the same back-off so
	// a transient send error (e.g. a momentary socket-write hiccup or
	// fabric reload race) does not immediately reap an otherwise-healthy
	// subscription. Reset to 0 on the first successful send. Subs that
	// never reach 0 again — peer truly gone — exceed the cap and get
	// closed via the existing eviction path.
	subSendErrorCount sync.Map // map[uint32]int  (subscriptionID → consecutive failures)

	// eventLog is the persistent (in-process lifetime) event buffer.
	// Every call to [Bridge.EmitEvent] appends to this log in addition
	// to fanning out to live subscriptions. Controllers that send a
	// ReadRequest with EventRequests (e.g. chip-tool `read-event-by-id`,
	// Apple MTRDevice liveness checks) get their answers from here.
	// Allocated at New time; never nil.
	eventLog *im.EventLog

	// commissioningWindow tracks the live AdministratorCommissioning
	// (0x003C) window state. Optional; nil means the cluster on the
	// root endpoint reports BUSY for any incoming Open/Revoke command.
	// Wired via [AttachCommissioningWindow].
	commissioningWindow *CommissioningWindow

	// paseFailures counts consecutive real PASE pairing errors so the
	// bridge can abort the commissioning window after too many, mirroring
	// matter.js PaseServer's PASE_COMMISSIONING_MAX_ERRORS brute-force cap.
	// Reset when a new PASE acceptor is installed (a window boundary) and
	// when the cap fires. See [Bridge.recordPaseFailure].
	paseFailures atomic.Int32

	// paseLockoutUntil / paseLockoutStreak implement the timed half of
	// the brute-force cap: reaching [paseMaxErrors] refuses PASE until
	// paseLockoutUntil, and each further lockout without an operator
	// intervention doubles the cooldown up to [paseLockoutMaxCooldown].
	// The cooldown is what makes the refusal recoverable — an
	// uncommissioned bridge keeps its configured-passcode acceptor armed
	// with no window open, so a permanent latch would let any LAN host
	// disable pairing for the daemon's lifetime. Guarded by b.mu; a zero
	// paseLockoutUntil means "never locked out". See
	// [Bridge.recordPaseFailure] and [Bridge.paseLockedOut].
	paseLockoutUntil  time.Time
	paseLockoutStreak int

	// preserveLockoutOnReset makes [Bridge.resetPaseFailures] keep an
	// in-force lockout instead of clearing it. It is raised only while
	// [Bridge.recordPaseFailure] drives the cap's own RevokeWindow — that
	// revoke runs the window's restore closure synchronously, which
	// re-attaches the configured PASE acceptor and would otherwise clear
	// the lockout microseconds after it was engaged (the guessing then
	// continues at full rate). An operator-initiated fresh window open
	// leaves the flag clear and still starts with a clean budget.
	preserveLockoutOnReset atomic.Bool

	// nowFn is the wall-clock seam for the PASE lockout bookkeeping.
	// Nil (the production value) means [time.Now]; tests replace it
	// before any traffic is dispatched so the cooldown can be crossed
	// without sleeping. See [Bridge.now].
	nowFn func() time.Time

	// paseInFlightExchange / paseInFlightSince implement the
	// single-active-PASE invariant (Matter §4.13.1): while one PASE
	// handshake is in progress the bridge SHALL NOT accept another —
	// otherwise a second commissioner's PBKDFParamRequest silently
	// replaces the first one's in-flight verifier state. Guarded by
	// b.mu; zero exchange + zero time = idle. An abandoned handshake
	// self-expires after [pasePairingTimeout] so a crashed
	// commissioner cannot lock pairing out for the whole window.
	// Mirrors matter.js PaseServer.ts:80-86 onNewExchange (reject
	// while a pairing messenger / timer is active) + :127 the 60 s
	// pairing timer.
	paseInFlightExchange uint16
	paseInFlightSince    time.Time

	// unsecuredWindows holds a per-source-node-id [mrp.Window] duplicate
	// detector for unsecured (SessionID==0, PASE) traffic, so a
	// retransmitted Pake1/Pake3 is acked without re-invoking the handshake
	// handler. Keyed by SourceNodeID (uint64) → *mrp.Window. Cleared on
	// each PASE-acceptor swap (a commissioning-window boundary) so it stays
	// bounded to the current window's transient commissioners. Mirrors
	// matter.js UnsecuredSession's MessageReceptionState.
	unsecuredWindows sync.Map
	// unsecuredWindowCount tracks the live entry count of unsecuredWindows so
	// creation can be capped at [maxUnsecuredWindows]: SourceNodeID is
	// attacker-controlled in unsecured PASE traffic, and within a single open
	// commissioning window a spoofed-source flood would otherwise grow the map
	// without bound. Reset to 0 alongside the Clear at each window boundary.
	unsecuredWindowCount atomic.Int64

	// commissioningInstanceName remembers the mDNS instance name the
	// bridge published via [Bridge.AnnounceCommissioning] so the
	// matching [Bridge.WithdrawCommissioning] call can target the
	// right record. Empty means "no commissioning record outstanding".
	commissioningInstanceName string

	// onReassembled, when non-nil, is invoked at the end of every
	// successful Reassemble with the bridged-endpoint count (excluding
	// the root). Wired by the daemon to publish
	// `matter.endpoint_assembled` over the WebSocket hub. Nil safe.
	onReassembled func(endpointCount int)

	// onFabricAdded, when non-nil, fires after the
	// OperationalCredentials cluster successfully installs a fabric
	// (post-AddNOC). Wired by the daemon to publish
	// `matter.fabric_added` over the WebSocket hub. Nil safe.
	onFabricAdded func(fabricIndex uint8)

	// onFabricRemoved, when non-nil, fires after the
	// OperationalCredentials cluster successfully drops a fabric
	// (post-RemoveFabric). Wired by the daemon to publish
	// `matter.fabric_removed` over the WebSocket hub. Nil safe.
	onFabricRemoved func(fabricIndex uint8)

	// unsecuredCounter generates monotonic MessageCounter values for
	// SessionID==0 replies (PASE / commissioning chatter pre-CASE).
	// Encrypted sessions get their counters from the underlying
	// channel.Session. Atomic so the receive goroutine can fan out
	// without blocking on the bridge's mutex.
	unsecuredCounter atomic.Uint32

	// outboundExchangeID allocates fresh 15-bit exchange identifiers
	// for server-initiated exchanges (subscribe ongoing reports,
	// future timed-write / spontaneous-event paths). The high bit
	// (0x8000) is reserved by Matter §4.13 as the Initiator flag —
	// our exchange IDs therefore stay inside [1, 0x7FFF]. Mirrors
	// matter.js packages/protocol/src/protocol/MessageExchange.ts
	// where the ExchangeManager hands out a 15-bit counter via
	// `ExchangeManager.#getNextExchangeId`.
	outboundExchangeID atomic.Uint32

	// startClaim is the race-free entry guard for [Bridge.Start].
	// CAS-claimed at entry; reset on failure paths. Distinct from
	// [Bridge.started] (which signals "fully started" under b.mu)
	// because the start sequence opens a UDP listener — if we held
	// b.mu across that, concurrent reads (Topology, Dispatcher)
	// would block on the listener's bind latency.
	startClaim atomic.Bool

	// sessionMissTracker counts decrypt-side session-id lookup misses
	// per source-address so the receive path can surface a single
	// operator-actionable warning (with the "iPhone reboot" remediation
	// hint) instead of N debug rows. The Apple iPhone holds a stale
	// CHIP SecureSession in MTRDeviceController after a RemoveFabric;
	// subsequent pair attempts from the same iPhone re-emit the old
	// session-id and only an iPhone reboot clears the cache. matter.js
	// and chip both drop these datagrams silently — openccu-loom does
	// the same on the wire, but a once-per-burst INFO log makes the
	// failure mode self-diagnosing.
	sessionMissTracker sessionMissBurst

	// sessionRegistry is the optional seam to the operational session
	// manager, wired via [Bridge.AttachSessionRegistry]. The
	// Secure-Channel path uses it to close a session on an inbound
	// CloseSession StatusReport, and [Bridge.Stop] uses it to drain
	// every session with a graceful CloseSession farewell before the
	// listener goes away. nil when the daemon has not wired one (test
	// fixtures) — both paths degrade to no-ops.
	sessionRegistry SessionRegistry

	// sessionPeerAddrs remembers the last authenticated UDP source
	// address per secure session (sessionID → *net.UDPAddr), fed by
	// the Secure-Channel receive router. The outbound CloseSession
	// sender routes on it; matter.js keeps the equivalent association
	// on the session itself via its MessageChannel
	// (packages/protocol/src/session/Session.ts, `get channel()`).
	// Entries are dropped on an inbound CloseSession and on every
	// graceful close the operational manager notifies. Teardown paths
	// that skip that notifier (fabric removal, NOC rotation, manager
	// shutdown) leave the entry behind until the id is reused: the
	// id space is uint16 and the next session that draws the same id
	// overwrites it on its first authenticated datagram, so the map
	// stays bounded and cannot outlive a reuse.
	sessionPeerAddrs sync.Map
}

// New constructs a Bridge. The store and snapshotter are required;
// when advertiser is nil the bridge falls back to [mdns.NewNoop],
// useful for tests and headless boot phases. When logger is nil the
// bridge uses [slog.Default].
//
// New does NOT touch the network — call [Start] for that.
func New(s endpoint.Store, snap Snapshotter, advertiser mdns.Advertiser, cfg Config, logger *slog.Logger) (*Bridge, error) {
	if s == nil {
		return nil, errors.New("bridge: store is required")
	}
	if snap == nil {
		return nil, errors.New("bridge: snapshotter is required")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.AdvertiseTimeout == 0 {
		cfg.AdvertiseTimeout = 5 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	if advertiser == nil {
		advertiser = mdns.NewNoop()
	}

	asm, err := endpoint.New(s, endpoint.Config{
		ExposeSecondaryChannels: cfg.ExposeSecondaryChannels,
		VendorID:                cfg.VendorID,
		ProductID:               cfg.ProductID,
		NodeLabel:               cfg.NodeLabel,
		IncludeMeasurements:     cfg.IncludeMeasurements,
		Labels:                  cfg.Labels,
		Locale:                  cfg.Locale,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bridge: assembler: %w", err)
	}

	br := &Bridge{
		cfg:           cfg,
		store:         s,
		snapshotter:   snap,
		logger:        logger.With(slog.String("subsystem", "matter.bridge")),
		advertiser:    advertiser,
		assembler:     asm,
		sessions:      noopSessionLookup{},
		paseHandler:   noopPaseHandler{},
		caseHandler:   noopCaseHandler{},
		ackHandler:    noopAckHandler{},
		eventLog:      im.NewEventLog(),
		sigma1Replied: make(map[uint16][32]byte),
	}
	return br, nil
}

// Start brings the bridge online: assembles the initial topology,
// constructs the dispatcher, opens the UDP listener, and publishes
// the operational mDNS record (when the topology has at least one
// fabric — for v1.1 GA the operational record is emitted
// unconditionally; commissioning-window logic lands in v1.1.x).
//
// Start blocks only on the synchronous setup steps; the receive loop
// runs in a background goroutine. Returns immediately when the
// listener is bound and the goroutine is running. Cancel ctx (or
// call [Stop]) to tear everything down.
//
// Calling Start a second time on the same instance returns
// [ErrAlreadyStarted].
//
//nolint:contextcheck // serve/ack-pump goroutines run in a fresh context torn down explicitly via the stored serveCancel (Stop); rooting them in the caller ctx would race listener teardown
func (b *Bridge) Start(ctx context.Context) error {
	// Claim the started flag atomically so two concurrent Starts
	// can't both pass the check + race ahead to bind the same UDP
	// port. If any downstream step fails, the deferred reset gives
	// the caller a chance to retry without a stale flag blocking it.
	if !b.startClaim.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	committed := false
	defer func() {
		if !committed {
			b.startClaim.Store(false)
		}
	}()

	// --- Topology assembly ---
	if err := b.reassembleLocked(ctx); err != nil {
		return fmt.Errorf("bridge: initial topology: %w", err)
	}

	// --- UDP listener ---
	listener, err := udp.New(udp.Config{
		LocalAddr:  b.cfg.Listen,
		PreferIPv4: b.cfg.PreferIPv4,
	})
	if err != nil {
		return fmt.Errorf("bridge: udp: %w", err)
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := listener.Serve(serveCtx, b.handleDatagram); err != nil {
			// Closed-listener errors during shutdown are expected; log
			// at debug level. Real bind / IO errors come through
			// before serveCancel fires, so they remain at warn.
			b.logger.Debug("matter.udp.serve.exit", slog.String("err", err.Error()))
		}
	}()

	b.mu.Lock()
	b.listener = listener
	b.serveCancel = cancel
	b.serveDone = done
	b.handlerCtx = serveCtx
	b.started = true
	committed = true
	// Spawn the ACK pump only when an AckTracker has been wired
	// (daemon's [Bridge.AttachAckTracker] call). Test setups that
	// skip the wiring leave the pump dormant, which is fine — the
	// bridge still answers individual datagrams; peers just see
	// retransmits instead of timely standalone ACKs.
	pumpTracker := b.ackTracker
	b.mu.Unlock()
	if pumpTracker != nil {
		pumpCtx, pumpCancel := context.WithCancel(context.Background())
		pumpDone := make(chan struct{})
		go func() {
			defer close(pumpDone)
			b.runAckPump(pumpCtx)
		}()
		b.mu.Lock()
		b.pumpCancel = pumpCancel
		b.pumpDone = pumpDone
		b.mu.Unlock()
	}

	b.logger.Info(
		"matter.bridge.started",
		slog.String("addr", listener.LocalAddr().String()),
		slog.Int("endpoints", b.endpointCount()),
	)

	// --- mDNS advertise ---
	if err := b.publishOperationalRecord(ctx); err != nil {
		// Advertise failure is non-fatal — the bridge is live and a
		// commissioner can still find it via direct addressing. Log
		// loudly so operators know to investigate.
		b.logger.Warn("matter.mdns.publish",
			slog.String("err", err.Error()),
			slog.String("hint", "bridge is operational; commissioners may need a direct address until DNS-SD recovers"))
	}

	return nil
}

// Reassemble re-runs the topology assembly against a fresh snapshot.
// Call after the model registry sees changes (device added / removed,
// channel configuration changed, …). The new topology and a fresh
// dispatcher swap in atomically so in-flight IM dispatches see a
// consistent view at every point.
//
// Returns [ErrNotStarted] when the bridge has not been [Start]ed.
func (b *Bridge) Reassemble(ctx context.Context) error {
	b.mu.RLock()
	started := b.started
	b.mu.RUnlock()
	if !started {
		return ErrNotStarted
	}
	return b.reassembleLocked(ctx)
}

// reassembleLocked is the shared implementation behind initial
// assembly (Start) and re-assembly (Reassemble). The locking is
// intentionally narrow — the snapshot+assemble work happens off-lock,
// and only the swap into b.topology / b.dispatcher takes the write
// lock. Concurrent Reassembles serialise harmlessly: each finishes
// independently, and the last writer wins (subsequent reads see one
// of the two assembled topologies, never a torn intermediate).
func (b *Bridge) reassembleLocked(ctx context.Context) error { //nolint:gocognit,funlen // single-purpose bridge topology reassembly with many endpoint/cluster branches
	snapshots := b.snapshotter(ctx)
	topology, err := b.assembler.Assemble(ctx, snapshots)
	if err != nil {
		return fmt.Errorf("bridge: assemble: %w", err)
	}
	// Reattach the daemon-supplied root cluster servers to the fresh
	// root endpoint so reads on endpoint 0 (BasicInformation,
	// GeneralCommissioning, OperationalCredentials, …) keep resolving
	// after a Reassemble. Without this every Reassemble would clear
	// the root attribute surface and any in-flight commissioning
	// would fall through to UnsupportedCluster.
	//
	// Both sets are read under one lock so the fresh topology cannot mix
	// a root set from before an [Bridge.AttachRootClusters] with an
	// aggregator set from after the paired
	// [Bridge.AttachAggregatorClusters].
	b.mu.RLock()
	rootClusters := append([]interfaces.MatterClusterServer(nil), b.rootClusters...)
	aggregatorClusters := append([]interfaces.MatterClusterServer(nil), b.aggregatorClusters...)
	b.mu.RUnlock()
	if root := topology.FindByID(0); root != nil && len(rootClusters) > 0 {
		root.PublishClusterServers(rootClusters)
	}
	// Reattach the daemon-supplied Aggregator cluster servers to the
	// fresh EP 1 so reads on endpoint 1 (Descriptor) keep resolving
	// after a Reassemble.
	if agg := topology.FindByID(1); agg != nil && len(aggregatorClusters) > 0 {
		agg.PublishClusterServers(aggregatorClusters)
	}
	dispatcher := endpoint.NewTopologyDispatcher(topology)
	// Wire ACL enforcement onto the fresh dispatcher (Matter §9.10). A nil
	// lister leaves CheckACL denying every operational request — production
	// daemons attach a store-backed lister via AttachACLLister.
	b.mu.RLock()
	aclLister := b.aclLister
	b.mu.RUnlock()
	dispatcher.SetACLLister(aclLister)

	// Wire MatterEventReceiver-aware cluster servers so they can fire
	// events through the bridge's event emitter pipeline. The
	// materialised cluster slice is the authoritative set; walk every
	// endpoint (root + bridged) and inject ourselves as the emitter
	// on each receiver. Sources that implement the model-side wiring
	// helper (the per-channel button group's WireMatterSwitchHandler)
	// are subscribed to the cluster's Fire* surface so HM-pushed press
	// events flow through to subscribed Matter commissioners.
	//
	// Each wiring below registers an update hook on the underlying
	// press DP(s). The PREVIOUS topology's hooks are exchanged and
	// drained inside the swap section further down (under b.mu), not
	// here: two concurrent reassembles both wire fresh hooks off-lock,
	// and only the swap decides whose list survives — draining the
	// swapped-out list there guarantees every superseded hook set is
	// torn down exactly once (the loser's hooks are removed by the
	// winner's swap), so no stale hook keeps firing through its old
	// cluster instance and no physical press emits duplicate events.
	var switchUnsubs []func()
	for _, ep := range topology.Endpoints {
		if ep == nil {
			continue
		}
		// Root endpoint carries singleton clusters (BasicInformation,
		// GeneralDiagnostics, AccessControl) that emit StartUp /
		// ShutDown / BootReason / AccessControlEntryChanged events.
		// Bridged endpoints carry per-device clusters (GenericSwitch,
		// BridgedDeviceBasicInformation) that emit press / Reachable
		// events.
		//
		// ClusterServers resolves all three cases: the published set on
		// the root / aggregator, the materialised set everywhere else.
		for _, srv := range endpoint.ClusterServers(ep) {
			recv, ok := srv.(interfaces.MatterEventReceiver)
			if !ok {
				continue
			}
			recv.SetMatterEventEmitter(b)
			if endpointSetter, ok := srv.(matterEndpointReceiver); ok {
				endpointSetter.SetEndpoint(ep.ID)
			}
			// If the cluster server exposes the GenericSwitch Fire*
			// surface and the endpoint's measurement source implements
			// the model-side wiring helper, subscribe so HM press
			// updates fan out as Matter events. The unsubscribe is
			// retained so the next reassemble tears the hook down.
			if !ep.IsRoot() {
				if emitter, ok := srv.(generic.MatterSwitchEventEmitter); ok {
					if subscriber, ok := ep.Measurement.(matterSwitchSubscribable); ok {
						switchUnsubs = append(switchUnsubs, subscriber.WireMatterSwitchHandler(emitter))
					}
				}
			}
		}
	}

	b.mu.Lock()
	prevTopology := b.topology
	b.topology = topology
	b.dispatcher = dispatcher
	staleSwitchUnsubs := b.switchUnsubscribers
	b.switchUnsubscribers = switchUnsubs
	b.wireMeasurementListenersLocked()
	hook := b.onReassembled
	b.mu.Unlock()

	// Drain the swapped-out press-DP hooks now that the new topology
	// is live. Doing this after the swap (instead of before wiring)
	// keeps concurrent reassembles safe: whichever swap runs last
	// drains its predecessor's list, so superseded hooks never leak
	// and never double-fire.
	for _, unsub := range staleSwitchUnsubs {
		if unsub != nil {
			unsub()
		}
	}

	// Reap subscriptions for endpoints that no longer exist in the new
	// topology. Mirrors matter.js packages/node/src/node/ServerNode.ts
	// / BridgedDeviceBasicInformation lifecycle where removing an endpoint
	// tears down its active subscriptions via
	// `endpoint.lifecycle.remove()` →
	// packages/node/src/node/server/ServerSubscription.ts close().
	if prevTopology != nil {
		newIDs := make(map[uint16]bool, len(topology.Endpoints))
		for _, ep := range topology.Endpoints {
			if ep != nil {
				newIDs[ep.ID] = true
			}
		}
		if m := b.subscriptionManagerLocked(); m != nil {
			for _, ep := range prevTopology.Endpoints {
				if ep == nil || newIDs[ep.ID] {
					continue
				}
				if reaped := m.CloseEndpoint(ep.ID); reaped > 0 {
					b.logger.Info("matter.bridge.reassemble.endpoint_removed",
						slog.Int("endpoint_id", int(ep.ID)),
						slog.Int("subscriptions_reaped", reaped))
				}
			}
		}
	}

	// Notify active subscribers that Descriptor.PartsList has changed
	// on both the Root (EP 0) and Aggregator (EP 1) endpoints. Mirrors
	// matter.js packages/node/src/behaviors/descriptor/
	// DescriptorServer.ts:27 — `this.reactTo(this.agent.get(IndexBehavior)
	// .events.change, this.#updatePartsList)` which writes
	// `this.state.partsList = numbers` on every topology mutation and
	// the reactive state engine automatically marks the attribute dirty
	// for all active subscriptions.
	//
	// In openccu-loom the state is not reactive (Go structs have no
	// observable fields), so we call OnAttributeChanged explicitly after
	// the topology swap. The manager propagates the dirty mark to every
	// wildcard or concrete subscription that covers
	// (endpoint, 0x001D, 0x0003); the next Tick flushes the report.
	// Covers both the ADD case (new endpoint appeared) and the REMOVE case
	// (endpoint vanished) — any topology change triggers the notification.
	// chip equivalent: src/app/clusters/descriptor/descriptor.cpp +
	// emberAfSetDynamicEndpoint which calls
	// MatterReportingAttributeChangeCallback for Descriptor.PartsList.
	if m := b.subscriptionManagerLocked(); m != nil {
		for _, epID := range []uint16{0, 1} {
			m.OnAttributeChanged(im.ConcreteAttributePath{
				Endpoint:     epID,
				Cluster:      0x001D, // Descriptor
				Attribute:    0x0003, // PartsList
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
			})
		}
	}

	// Topology.Bridged() is the authoritative bridged set: the assembler
	// always seeds root (EP 0) AND aggregator (EP 1), so any arithmetic
	// on len(Endpoints) drifts the moment a structural tier is added or
	// removed. The count leaves the daemon as the SPA-facing
	// `matter.endpoint_assembled` payload, so an off-by-one shows up as
	// a bridged device that does not exist.
	count := len(topology.Bridged())
	b.logger.Info("matter.bridge.reassembled",
		slog.Int("bridged_endpoints", count),
		slog.Int("total_endpoints", len(topology.Endpoints)))

	if hook != nil {
		hook(count)
	}
	return nil
}

// SetOnReassembled wires the post-Reassemble hook. The bridge calls
// the supplied closure with the freshly-assembled bridged-endpoint
// count (excluding the root and the aggregator) after every successful
// reassembly — initial Start, manual Reassemble, and any
// allowlist-driven re-build.
// Pass nil to detach.
func (b *Bridge) SetOnReassembled(fn func(endpointCount int)) {
	b.mu.Lock()
	b.onReassembled = fn
	b.mu.Unlock()
}

// matterEndpointReceiver is the optional capability a cluster server
// implements to learn the endpoint id it lives on. Required for any
// cluster that emits events (the event payload carries the endpoint
// id as the routing key for subscribers). Today: BasicInformation,
// GeneralDiagnostics, AccessControl, BridgedDeviceBasicInformation.
type matterEndpointReceiver interface {
	SetEndpoint(uint16)
}

// matterSwitchSubscribable is the model-side surface of a press
// source (the per-channel [generic.ButtonGroup], or a lone Button /
// Action DP) that translates its CCU press updates into Matter §1.13
// switch events. The signature deliberately names the model-side
// [generic.MatterSwitchEventEmitter] interface: Go interface
// satisfaction requires identical parameter types, so a bridge-local
// emitter interface in this position can never be satisfied by the
// model types' method set — the assertion in the reassemble wiring
// would then silently never match and no press event would reach a
// commissioner. The returned unsubscribe closure is retained in
// [Bridge.switchUnsubscribers] and drained on the next reassemble.
type matterSwitchSubscribable interface {
	WireMatterSwitchHandler(generic.MatterSwitchEventEmitter) func()
}

// Compile-time guard: the consolidated button group — the shape the
// endpoint assembler mounts as ep.Measurement on every GenericSwitch
// endpoint — must satisfy the reassemble wiring assertion. Fails the
// build if the two interfaces drift apart again.
var _ matterSwitchSubscribable = (*generic.ButtonGroup)(nil)

// SetOnFabricAdded wires the post-AddNOC hook. The bridge does not
// install fabrics itself — the OperationalCredentials cluster does —
// but the OpCreds wiring inside the daemon delegates here so a
// downstream listener (typically the WS event publisher) can fan the
// event out without taking a direct dependency on the cluster.
// Pass nil to detach.
func (b *Bridge) SetOnFabricAdded(fn func(fabricIndex uint8)) {
	b.mu.Lock()
	b.onFabricAdded = fn
	b.mu.Unlock()
}

// EmitFabricAdded is the bridge-side dispatch helper the
// OperationalCredentials cluster's `OnFabricInstalled` closure calls
// on a successful AddNOC. Forwards to whatever closure the daemon
// wired via [SetOnFabricAdded]; nil-safe.
func (b *Bridge) EmitFabricAdded(fabricIndex uint8) {
	b.mu.RLock()
	hook := b.onFabricAdded
	b.mu.RUnlock()
	if hook != nil {
		hook(fabricIndex)
	}
}

// SetOnFabricRemoved wires the post-RemoveFabric hook. Mirrors
// [SetOnFabricAdded]'s nil-safe semantics. Used by the daemon's
// WS-event publisher; the operational + subscription manager
// cleanup runs separately on [core.OperationalCredentials.SetOnFabricRemoved].
func (b *Bridge) SetOnFabricRemoved(fn func(fabricIndex uint8)) {
	b.mu.Lock()
	b.onFabricRemoved = fn
	b.mu.Unlock()
}

// EmitFabricRemoved is the bridge-side dispatch helper the daemon
// invokes inside the [core.OperationalCredentials.SetOnFabricRemoved]
// closure. Forwards to whatever closure the daemon wired via
// [SetOnFabricRemoved]; nil-safe.
func (b *Bridge) EmitFabricRemoved(fabricIndex uint8) {
	b.mu.RLock()
	hook := b.onFabricRemoved
	b.mu.RUnlock()
	if hook != nil {
		hook(fabricIndex)
	}
}

// AttachExposureChecker wires the allowlist gate the assembler
// consults before bridging a model source. The default checker
// permits everything (back-compat for tests + dev setups). Production
// daemons should pass a `matter/store.Store`-backed checker so the
// `matter_exposures` allowlist is enforced.
//
// Pass nil to revert to the allow-all default.
func (b *Bridge) AttachExposureChecker(c endpoint.ExposureChecker) {
	if b == nil || b.assembler == nil {
		return
	}
	b.assembler.SetExposureChecker(c)
}

// AttachACLLister wires the Matter ACL source the IM dispatcher enforces
// against (Matter §9.10). Production daemons MUST pass a
// `matter/store.Store`-backed lister so operational (CASE) reads / writes /
// invokes are gated by the AccessControl cluster's stored entries; without
// one the dispatcher's CheckACL denies every operational request, so a
// bridge left unwired answers nothing rather than answering everything. The
// lister is applied to the dispatcher on the next [Reassemble] and on every
// subsequent rebuild.
//
// A setup that means to run without stored entries passes
// [endpoint.UnenforcedACL].
func (b *Bridge) AttachACLLister(l endpoint.ACLLister) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.aclLister = l
	b.mu.Unlock()
}

// AttachCommissioningWindow wires the bridge-side
// [CommissioningWindow] tracker. The bridge stores the reference for
// future re-emit on Reassemble; callers should also attach the
// AdministratorCommissioning cluster server (with the window as its
// WindowController) to the root endpoint via [AttachRootClusters].
//
// Pass nil to detach.
func (b *Bridge) AttachCommissioningWindow(w *CommissioningWindow) {
	b.mu.Lock()
	b.commissioningWindow = w
	b.mu.Unlock()
}

// CommissioningWindow returns the attached window tracker or nil
// when none was wired. Useful for tests + the daemon's
// AdministratorCommissioning cluster construction.
func (b *Bridge) CommissioningWindow() *CommissioningWindow {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.commissioningWindow
}

// AttachRootClusters wires the cluster servers the dispatcher answers
// for endpoint 0. Pass nil to clear (root endpoint reverts to no
// cluster servers — every read returns UnsupportedCluster). The
// supplied slice is defensively copied; subsequent mutations by the
// caller do not affect the bridge.
//
// Calling this after [Bridge.Start] swaps in the new servers on the
// next [Bridge.Reassemble] *and* on the live topology immediately —
// chip-tool that has already established a session and started
// reading must see the cluster set without waiting for a topology
// re-roll.
func (b *Bridge) AttachRootClusters(servers []interfaces.MatterClusterServer) {
	cp := append([]interfaces.MatterClusterServer(nil), servers...)
	b.mu.Lock()
	b.rootClusters = cp
	if b.topology != nil {
		if root := b.topology.FindByID(0); root != nil {
			root.PublishClusterServers(cp)
		}
	}
	b.mu.Unlock()
}

// PartsListProviderSetter is the duck-typed surface a root-endpoint
// Descriptor cluster (`0:0x001D`) implements so the daemon can wire a
// closure that returns the live bridged-endpoint set on every read.
// Defined here (instead of importing the cluster/core package
// directly) to avoid a bridge → core import cycle.
type PartsListProviderSetter interface {
	SetPartsListProvider(func() []uint16)
}

// AttachRootPartsListProvider walks the attached root clusters and
// installs the live PartsList provider on the Descriptor cluster
// (cluster ID 0x001D). Returns true when the provider was wired,
// false when no Descriptor is mounted.
func (b *Bridge) AttachRootPartsListProvider(provider func() []uint16) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.rootClusters {
		if s.MatterClusterID() == 0x001D {
			if setter, ok := s.(PartsListProviderSetter); ok {
				setter.SetPartsListProvider(provider)
				return true
			}
		}
	}
	return false
}

// AttachAggregatorClusters wires the cluster servers the dispatcher
// answers for endpoint 1 (Aggregator). Bug C topology fix: matter.js's
// bridge pattern always lands the Aggregator on its own endpoint with
// Descriptor (mandatory) + optionally Identify. Apple Home walks
// RootNode.PartsList → Aggregator → Aggregator.PartsList → bridged
// devices; without a dedicated Aggregator endpoint the HAP service
// mapper renders the bridge as empty.
//
// Pass nil to clear. Calling this after [Bridge.Start] swaps in the
// new servers on the next [Bridge.Reassemble] and on the live topology
// immediately.
func (b *Bridge) AttachAggregatorClusters(servers []interfaces.MatterClusterServer) {
	cp := append([]interfaces.MatterClusterServer(nil), servers...)
	b.mu.Lock()
	b.aggregatorClusters = cp
	if b.topology != nil {
		if agg := b.topology.FindByID(1); agg != nil {
			agg.PublishClusterServers(cp)
		}
	}
	b.mu.Unlock()
}

// AttachAggregatorPartsListProvider walks the attached aggregator
// clusters and installs the live PartsList provider on the Aggregator's
// Descriptor cluster (cluster ID 0x001D on endpoint 1). Returns true
// when the provider was wired, false when no Descriptor is mounted on
// the Aggregator.
func (b *Bridge) AttachAggregatorPartsListProvider(provider func() []uint16) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.aggregatorClusters {
		if s.MatterClusterID() == 0x001D {
			if setter, ok := s.(PartsListProviderSetter); ok {
				setter.SetPartsListProvider(provider)
				return true
			}
		}
	}
	return false
}

// Stop tears down the bridge: gracefully closes every secure session
// (best-effort CloseSession StatusReport per peer, capped by
// [stopSessionCloseBudget]), closes the UDP listener, withdraws
// every published mDNS record, and waits up to ctx's deadline for
// the serve goroutine to exit. Idempotent — a second call is a
// no-op. Safe to call after a failed [Start].
func (b *Bridge) Stop(ctx context.Context) error {
	// Session farewell FIRST, while the listener can still ship the
	// CloseSession StatusReports — without it controllers keep stale
	// CASE sessions and show the bridge unresponsive for minutes after
	// a restart. Mirrors matter.js's shutdown ordering: sessions close
	// gracefully (Session.ts:248 initiateClose → gracefulClose →
	// ExchangeManager.ts:658 #sendCloseSession) before the transport
	// is torn down (ServerNetworkRuntime.ts:410-447 stop()).
	b.closeSecureSessionsForShutdown()
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return nil
	}
	listener := b.listener
	cancel := b.serveCancel
	done := b.serveDone
	pumpCancel := b.pumpCancel
	pumpDone := b.pumpDone
	b.listener = nil
	b.serveCancel = nil
	b.serveDone = nil
	b.handlerCtx = nil
	b.pumpCancel = nil
	b.pumpDone = nil
	b.started = false
	b.mu.Unlock()
	// Release the start-claim so a subsequent [Bridge.Start] can
	// re-arm. Keep this AFTER the b.mu critical section so a
	// concurrent Start cannot CAS-claim before we've cleared the
	// per-instance state above.
	b.startClaim.Store(false)

	// Cancel first so the serve loop unwinds; Close as belt-and-braces
	// in case the listener wraps the context-cancel in its own way.
	if cancel != nil {
		cancel()
	}
	if pumpCancel != nil {
		pumpCancel()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			b.logger.Warn("matter.bridge.stop.serve_timeout", slog.String("err", ctx.Err().Error()))
		}
	}
	if pumpDone != nil {
		// Give the pump its own bounded budget. If `ctx` already
		// expired waiting for the serve loop, the pump still gets a
		// fresh window — pumpCancel fired above so the goroutine
		// should already be unwinding. Without this, a slow serve-
		// loop teardown burns the entire ctx and the pump select
		// falls through immediately while the goroutine is still
		// alive — that's the leak the original "Same context" branch
		// was hiding.
		pumpStopCtx, pumpStopCancel := context.WithTimeout(context.Background(), pumpStopGrace)
		select {
		case <-pumpDone:
		case <-pumpStopCtx.Done():
			b.logger.Warn("matter.bridge.stop.pump_timeout", slog.Duration("grace", pumpStopGrace))
		}
		pumpStopCancel()
	}

	// Best-effort mDNS withdraw + close. Errors here are logged but
	// don't block teardown — an mDNS that cannot be reached on shutdown
	// is no worse than one that was never published.
	if b.advertiser != nil {
		active := b.advertiser.Active()
		for i := range active {
			svc := &active[i]
			withdrawCtx, withdrawCancel := context.WithTimeout(context.Background(), b.cfg.AdvertiseTimeout)
			//nolint:contextcheck // shutdown path: mDNS withdraw must run on a fresh timeout ctx, not the cancelled serve ctx
			if err := b.advertiser.Withdraw(withdrawCtx, svc.InstanceName, svc.ServiceType); err != nil {
				b.logger.Debug("matter.mdns.withdraw",
					slog.String("instance", svc.InstanceName),
					slog.String("err", err.Error()))
			}
			withdrawCancel()
		}
		if err := b.advertiser.Close(); err != nil {
			b.logger.Debug("matter.mdns.close", slog.String("err", err.Error()))
		}
	}

	return nil
}

// Topology returns the currently assembled topology. Returns nil
// when the bridge has not been started; safe to call concurrently
// with [Reassemble].
func (b *Bridge) Topology() *endpoint.Topology {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.topology
}

// Dispatcher returns the currently active IM dispatcher. Returns nil
// when the bridge has not been started.
func (b *Bridge) Dispatcher() im.Dispatcher {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.dispatcher == nil {
		return nil
	}
	return b.dispatcher
}

// EventLog returns the bridge's persistent event buffer. Callers
// (e.g. the IM ReadRequest handler) query it to answer historical
// event read requests (Matter §10.6.6, chip-tool `read-event-by-id`).
// Never nil.
func (b *Bridge) EventLog() *im.EventLog {
	return b.eventLog
}

// LocalAddr returns the effective UDP bind address — useful when
// Listen=":0" lets the OS pick the port. Returns nil when not started.
func (b *Bridge) LocalAddr() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.listener == nil {
		return ""
	}
	return b.listener.LocalAddr().String()
}

// effectiveUDPPort returns the port the UDP listener is actually bound
// to, falling back to the configured (or default) port before Start.
//
// `north.matter.listen: ":0"` is a valid configuration — the OS then
// assigns an ephemeral port, and advertising the 5540 default instead
// points every commissioner's SRV lookup at a port nothing listens on.
// Mirrors matter.js packages/node/src/behavior/system/network/
// ServerNetworkRuntime.ts:234 (`addTransports` writes the bound
// `ipv6Interface.port` into `network.operationalPort`) which
// ServerNetworkRuntime.ts:89 then reads when it constructs the
// MdnsAdvertiser.
func (b *Bridge) effectiveUDPPort() int {
	b.mu.RLock()
	listener := b.listener
	b.mu.RUnlock()
	if listener != nil {
		if addr := listener.LocalAddr(); addr != nil && addr.Port > 0 {
			return addr.Port
		}
	}
	return udpPort(b.cfg.Listen)
}

// endpointCount returns the count of bridged endpoints (excludes the
// root and the aggregator). Used only for logging.
func (b *Bridge) endpointCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.topology == nil {
		return 0
	}
	return len(b.topology.Bridged())
}

// handleDatagram is the inbound path for every Matter UDP datagram.
// Drives the full receive pipeline (header decode → session lookup
// → decryption → protocol dispatch → IM/SC handler call → reply
// send) via [Bridge.dispatch]. Errors at any layer are logged
// inside dispatch; the handler discards the result so the receive
// loop never stalls.
//
// Cancellation: dispatch runs under [Bridge.handlerCtx] which the
// serve goroutine derives from its own context at Start. A Stop()
// cancels handlerCtx so any blocked IM handler unwinds promptly.
func (b *Bridge) handleDatagram(buf []byte, src *net.UDPAddr) {
	ctx := b.handlerContext()
	_ = b.dispatch(ctx, buf, src)
}

// handlerContext returns the context every IM/SC handler call runs
// under. Returns context.Background when the bridge isn't
// fully-started (the receive loop should not be running in that
// case, but the defensive default avoids a nil deref).
func (b *Bridge) handlerContext() context.Context {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.handlerCtx != nil {
		return b.handlerCtx
	}
	return context.Background()
}

// publishOperationalRecord publishes the operational `_matter._tcp`
// record via the configured advertiser once a real fabric identity
// exists. Apple's MatterSupport reads operational records to detect
// already-paired bridges; emitting a `0000…0000` placeholder
// (CompressedFabricID + NodeID both zero) at boot makes Apple think a
// stale bridge is on the network and silently aborts the new pairing.
//
// The post-AddNOC `AnnounceFabric` path publishes the record with the
// real CompressedFabricID + NodeID so chip-tool's
// `FindOperationalForStayActive` query still resolves once a fabric
// is installed.
//
// Pre-fabric callers receive `nil` and a debug log so the no-op is
// observable in traces.
func (b *Bridge) publishOperationalRecord(ctx context.Context) error { //nolint:unparam // signature reserved for the actual publish path; AnnounceFabric will return non-nil once wired
	_ = ctx
	b.logger.Debug("matter.mdns.operational.deferred",
		slog.String("hint", "no fabric installed yet — skipping `_matter._tcp` publish until AnnounceFabric"))
	return nil
}

// AnnounceFabric re-publishes the operational mDNS record with the
// supplied CompressedFabricID + NodeID so commissioners can resolve
// the bridge to its post-commissioning Matter identity (chip-tool's
// `FindOperationalForStayActive` queries `<compressed>-<node>._matter._tcp.local`).
//
// Idempotent — calling with the same values overwrites the prior
// publish. Errors fall back to a debug log; the advertiser itself
// reports its failure modes.
func (b *Bridge) AnnounceFabric(ctx context.Context, compressedFabricID [8]byte, nodeID uint64) {
	if b.advertiser == nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, b.cfg.AdvertiseTimeout)
	defer cancel()
	svc := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID: compressedFabricID,
		NodeID:             nodeID,
		Port:               uint16(b.effectiveUDPPort()), //nolint:gosec // the bound / parsed port is ≤ 65535; see #20
		// HostName empty → advertiser uses the OS LocalHostName so the
		// SRV target resolves via macOS Bonjour / Linux avahi A/AAAA.
		HostName: "",
	})
	if err := b.advertiser.Publish(pubCtx, svc); err != nil {
		b.logger.Debug("matter.mdns.fabric_publish",
			slog.String("instance", svc.InstanceName),
			slog.String("err", err.Error()))
		return
	}
	if b.advertiserIsNoop() {
		b.logger.Warn("matter.mdns.fabric_not_advertised",
			slog.String("instance", svc.InstanceName),
			slog.String("hint", "mdns_advertise=noop suppresses the operational record — controllers cannot resolve the bridge after commissioning"))
		return
	}
	b.logger.Info("matter.mdns.fabric_published",
		slog.String("instance", svc.InstanceName))
}

// WithdrawFabric retracts the operational `_matter._tcp` instance for
// the given (compressedFabricID, nodeID) identity — the counterpart to
// [Bridge.AnnounceFabric] for RemoveFabric and for an UpdateNOC that
// changed the NodeID. A republish alone cannot retire the record: the
// advertiser re-announces what it still holds, so the stale
// <compressedID>-<nodeID> instance keeps answering until its TTL and
// commissioners resolve an identity that no longer exists. Mirrors
// matter.js DeviceAdvertiser.ts:76-86 (close the fabric's
// advertisements on update/delete before re-advertising).
func (b *Bridge) WithdrawFabric(ctx context.Context, compressedFabricID [8]byte, nodeID uint64) {
	if b.advertiser == nil {
		return
	}
	wCtx, cancel := context.WithTimeout(ctx, b.cfg.AdvertiseTimeout)
	defer cancel()
	svc := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID: compressedFabricID,
		NodeID:             nodeID,
		Port:               uint16(b.effectiveUDPPort()), //nolint:gosec // the bound / parsed port is ≤ 65535; see #20
		HostName:           "",
	})
	if err := b.advertiser.Withdraw(wCtx, svc.InstanceName, svc.ServiceType); err != nil {
		b.logger.Debug("matter.mdns.fabric_withdraw",
			slog.String("instance", svc.InstanceName),
			slog.String("err", err.Error()))
		return
	}
	b.logger.Info("matter.mdns.fabric_withdrawn",
		slog.String("instance", svc.InstanceName))
}

// CommissioningAdvertisement describes the parameters the bridge
// publishes on `_matterc._udp` while a commissioning window is open.
// All fields are stamped from the configured / ephemeral
// commissioning state held by the [CommissioningWindow] and the
// daemon's setup payload — the bridge itself does not know about
// passcodes.
type CommissioningAdvertisement struct {
	Discriminator      uint16
	VendorID           uint16
	ProductID          uint16
	NodeLabel          string
	CommissioningMode  uint8
	DeviceTypeID       uint32
	PairingHint        uint16
	PairingInstruction string
	InstanceID         [8]byte

	// RotatingID is the optional Matter §5.4.2.4 "Rotating Device
	// Identifier" — 36 uppercase-hex characters when present, empty
	// to suppress the `RI` TXT key. Callers compute it via
	// [mdns.GenerateRotatingID] from a persisted UniqueID +
	// LifetimeCounter pair; the bridge merely forwards the precomputed
	// value into the mDNS service config.
	RotatingID string
}

// AnnounceCommissioning publishes the `_matterc._udp` record (with
// `_L<long>`, `_S<short>`, `_V<vendor>` subtypes per Matter §4.3.1.4)
// so commissioners discover the bridge by service-type filter while
// the window is open. Idempotent — a second call replaces the prior
// publish.
//
// The instance name is remembered on the bridge so
// [Bridge.WithdrawCommissioning] can withdraw the right record on
// window close.
func (b *Bridge) AnnounceCommissioning(ctx context.Context, params CommissioningAdvertisement) error {
	if b == nil || b.advertiser == nil {
		return nil
	}
	pubCtx, cancel := context.WithTimeout(ctx, b.cfg.AdvertiseTimeout)
	defer cancel()
	svc := mdns.BuildCommissionableService(mdns.CommissionableServiceConfig{
		InstanceID:         params.InstanceID,
		Discriminator:      params.Discriminator,
		VendorID:           params.VendorID,
		ProductID:          params.ProductID,
		CommissioningMode:  params.CommissioningMode,
		DeviceTypeID:       params.DeviceTypeID,
		DeviceName:         params.NodeLabel,
		PairingHint:        params.PairingHint,
		PairingInstruction: params.PairingInstruction,
		RotatingID:         params.RotatingID,
		Port:               uint16(b.effectiveUDPPort()), //nolint:gosec // the bound / parsed port is ≤ 65535; see #20
		// HostName empty → advertiser uses the OS LocalHostName so the
		// SRV target resolves via macOS Bonjour / Linux avahi A/AAAA.
		HostName: "",
	})
	if err := b.advertiser.Publish(pubCtx, svc); err != nil {
		b.logger.Warn("matter.mdns.commissioning_publish_err",
			slog.String("instance", svc.InstanceName),
			slog.String("err", err.Error()))
		return fmt.Errorf("matter.mdns.commissioning_publish: %w", err)
	}
	b.mu.Lock()
	b.commissioningInstanceName = svc.InstanceName
	b.mu.Unlock()
	// The noop advertiser accepts every Publish without emitting a
	// single multicast packet. Logging "published" then sends an
	// operator hunting network problems while the record never left the
	// process — commissioners report "device not found" against a QR
	// code that is perfectly valid. Say what actually happened.
	if b.advertiserIsNoop() {
		b.logger.Warn("matter.mdns.commissioning_not_advertised",
			slog.String("instance", svc.InstanceName),
			slog.Int("discriminator", int(params.Discriminator)),
			slog.String("hint", "mdns_advertise=noop suppresses the commissionable record — commissioners cannot discover the bridge; set north.matter.mdns_advertise=zeroconf (or leave it unset) for pairing"))
		return nil
	}
	b.logger.Info("matter.mdns.commissioning_published",
		slog.String("instance", svc.InstanceName),
		slog.String("host", svc.HostName),
		slog.Int("discriminator", int(params.Discriminator)))
	return nil
}

// advertiserIsNoop reports whether the wired advertiser is the
// in-memory noop implementation — i.e. Publish calls succeed without
// putting any record on the network.
func (b *Bridge) advertiserIsNoop() bool {
	_, ok := b.advertiser.(*mdns.Noop)
	return ok
}

// WithdrawCommissioning withdraws the most recent
// [Bridge.AnnounceCommissioning] publish so commissioners no longer
// discover the bridge once the window has closed. No-op when no
// publish is on record.
func (b *Bridge) WithdrawCommissioning(ctx context.Context) {
	b.mu.Lock()
	instance := b.commissioningInstanceName
	b.commissioningInstanceName = ""
	advertiser := b.advertiser
	b.mu.Unlock()
	if instance == "" || advertiser == nil {
		return
	}
	withdrawCtx, cancel := context.WithTimeout(ctx, b.cfg.AdvertiseTimeout)
	defer cancel()
	if err := advertiser.Withdraw(withdrawCtx, instance, mdns.ServiceTypeCommissionable); err != nil {
		b.logger.Debug("matter.mdns.commissioning_withdraw",
			slog.String("instance", instance),
			slog.String("err", err.Error()))
		return
	}
	b.logger.Info("matter.mdns.commissioning_withdrawn",
		slog.String("instance", instance))
}

// udpPort extracts the port from a "host:port" listen string. Returns
// the Matter default (5540) when the string is empty or malformed —
// the listener has already validated the bind, so this fallback only
// affects the advertised SRV record.
func udpPort(listen string) int {
	if listen == "" {
		return udp.MatterPort
	}
	for i := len(listen) - 1; i >= 0; i-- {
		if listen[i] == ':' {
			port := 0
			for _, c := range listen[i+1:] {
				if c < '0' || c > '9' {
					return udp.MatterPort
				}
				port = port*10 + int(c-'0')
			}
			if port == 0 {
				return udp.MatterPort
			}
			return port
		}
	}
	return udp.MatterPort
}

// AttachDiagnosticEvents wires the ring the bridge records pairing and
// session moments into. Passing nil detaches it.
//
// Safe to call while the bridge serves: the receive path loads the ring
// atomically, and a nil ring drops the record rather than panicking, so a
// datagram in flight during an attach or a detach either records into the
// old ring or into the new one.
func (b *Bridge) AttachDiagnosticEvents(ring *diagevent.Ring) {
	if b == nil {
		return
	}
	b.diagEvents.Store(ring)
}

// DiagnosticEvents returns the recorded trace, newest first. Empty when
// no ring is attached.
func (b *Bridge) DiagnosticEvents() []diagevent.Event {
	return b.diagRing().Snapshot()
}

// diagRing loads the attached diagnostics ring. A nil result is usable —
// every [diagevent.Ring] method tolerates a nil receiver — so callers on
// the receive path record unconditionally.
func (b *Bridge) diagRing() *diagevent.Ring {
	return b.diagEvents.Load()
}

// ControllerRTT summarises how long paired Matter controllers are taking to
// acknowledge this bridge's reliable messages. Diagnostic only — see
// [mrpRTTWindow] for why this stays off the cluster surface.
//
// Safe on a nil bridge (Matter disabled), which reports an empty summary.
func (b *Bridge) ControllerRTT() MRPRTTStats {
	if b == nil {
		return MRPRTTStats{}
	}
	return b.outboundReliable.RTTStats()
}
