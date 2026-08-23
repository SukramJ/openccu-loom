// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/central/statemachine"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/internal/store/patches"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// Config configures a [*Unit]. Required fields: Name.
type Config struct {
	// Name is the operator-assigned identifier of the central.
	// Appears in every log and metric label.
	Name string

	// InstanceName is the daemon's own identity, advertised to the CCU
	// as the leading component of the wire interface_id
	// (`<instance_name>-<central_name>-<interface>`) so two daemons against
	// the same CCU do not overwrite each other's callback registration.
	// Daemon-global (same for every central); defaults to the OS
	// hostname. Empty falls back to the legacy `<central_name>-<interface>`
	// form. See ADR-0024.
	InstanceName string

	// DB is the shared SQLite handle. May be nil for tests that run
	// without persistence.
	DB *sql.DB

	// Logger for structured events. Defaults to slog.Default().
	Logger *slog.Logger
}

// Unit is the per-CCU domain orchestrator. The name mirrors
// SPECIFICATION §11.1 — renaming it to remove the stutter would
// diverge from spec and from recorded session names.
//
//nolint:revive // deliberate: spec-aligned name
type Unit struct {
	cfg Config

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	StateMachine   *statemachine.Central
	EventBus       *events.Bus
	DeviceRegistry *registry.DeviceRegistry
	ModelRegistry  *registry.ModelRegistry
	HubModel       *hub.Hub
	DescRegistry   *registry.DeviceDescriptionRegistry
	ParamsetReg    *registry.ParamsetRegistry
	Cache          *coordinators.CacheCoordinator
	Clients        *coordinators.ClientCoordinator
	Configuration  *coordinators.ConfigurationCoordinator
	Events         *coordinators.EventCoordinator
	Devices        *coordinators.DeviceCoordinator
	Hub            *coordinators.HubCoordinator
	Recovery       *coordinators.ConnectionRecoveryCoordinator
	// Link is the LinkCoordinator that brokers LINK-paramset operations
	// (AddLink, RemoveLink, GetLinks, GetLinkInfo). It becomes useful once a
	// [coordinators.ClientResolver] is installed via
	// [Unit.SetLinkResolver] — without one the coordinator is present but
	// its methods short-circuit with `no client` errors.
	Link      *coordinators.LinkCoordinator
	Scheduler *scheduler.Scheduler
	Health    *health.Tracker
	// Reconciler drives the slow-cadence connectivity/health reconciliation pass.
	// Wired at daemon boot via RegisterStandardJobs; nil until then.
	Reconciler *coordinators.Reconciler

	// DeviceDetails holds the runtime metadata cache populated during hub
	// bootstrap (operator-assigned names, ISE-IDs, interface tags, room and
	// function assignments). The cache coordinator's Load() call fills it once
	// after login and refreshes it every 5 minutes via the scheduler. Consumers
	// (MQTT-Discovery, REST list view, NameData builder) read it on every
	// render.
	DeviceDetails *devicedetails.Cache

	// Recorder captures CCU command/response sessions for diagnostics +
	// golden-file replay. Default config keeps the recorder inactive
	// (StartSession-gated) so the daemon pays no overhead in normal operation.
	// Disk-persistence is wired via [Unit.WireSessionRecorderPersistence]
	// when a SQLite store is available.
	Recorder *session.Recorder

	// recorderPersistUnsub stops the auto-persist ticker installed by
	// [WireSessionRecorderPersistence]. Nil when persistence is disabled.
	// recorderStore + recorderSlug retain the persistence target so a
	// recording resumed after a restart can reload what it captured before.
	recorderPersistMu    sync.Mutex
	recorderPersistUnsub func()
	recorderStore        session.PersistStore
	recorderSlug         string

	// MetricsClients collects every InterfaceClient registered for this
	// central so the metrics aggregator can read per-client RPC counters.
	// Populated by the southbound wiring adapters as clients are created;
	// the daemon wires it into the Aggregator at boot time.
	MetricsClients *clientpkg.MetricsClientProvider

	// Aggregator is the per-CCU metrics aggregator. Nil until the daemon
	// wires it at boot via [SetAggregator], so every reader checks for
	// nil before dereferencing. Read by the diagnostics introspection
	// adapter, which renders the snapshot into the `metrics` block of
	// `GET /api/v1/diagnostics`.
	Aggregator *metrics.Aggregator

	logger *slog.Logger

	// systemInfoMu guards the SystemInformation cache. Populated by
	// the hub-wiring adapter after Login + get_backend_info; read by
	// north-bound `/info` and `system.info` WS handlers.
	systemInfoMu sync.RWMutex
	systemInfo   SystemInfo

	// ccuInterfacesMu guards the CCU-reported interface list. Kept out of
	// [SystemInfo] because that struct feeds the MQTT-Discovery hub block
	// through [internal/payload], which flattens scalars only.
	ccuInterfacesMu sync.RWMutex
	ccuInterfaces   []CCUInterface

	// services bundles the runtime service-dispatch closures populated by
	// the hub-wiring adapter once the JSON-RPC session is up; Unit.Service*
	// methods delegate through them to the actual transport.
	// See [serviceBundle] in service_bundle.go.
	services serviceBundle

	// devicesCreatedMu guards devicesCreated below.
	devicesCreatedMu sync.RWMutex
	// devicesCreated is set to true when the first DeviceCreatedEvent fires
	// after startup. Hub jobs (sysvar/program/alarm refresh etc.) optionally
	// gate on this flag via [gatedRunWithDevicesCreatedGate].
	devicesCreated bool
	// devicesCreatedUnsub holds the event-bus unsub function for the
	// DeviceCreatedEvent subscription installed by [WireDevicesCreatedGate].
	devicesCreatedUnsub func()

	// southboundReadyMu guards southboundReady below.
	southboundReadyMu sync.RWMutex
	// southboundReady latches once the initial southbound bring-up (the
	// readiness-gated CCU device load) has completed. It is the queryable
	// twin of [hmevent.CentralSouthboundReadyEvent]: the event only reaches
	// subscribers that exist when it fires, while this flag lets a late
	// subscriber (e.g. the Matter bridge wiring, which starts after the
	// bring-up goroutines) seed its view instead of waiting for an event
	// that will never re-fire. Set by the southbound adapter exactly where
	// it publishes the event; never cleared for the unit's lifetime — the
	// loaded model survives mid-life CCU reconnects, so the registry view
	// stays authoritative once the initial load has completed.
	southboundReady bool

	// readinessMu guards readiness below.
	readinessMu sync.RWMutex
	// readiness tracks the central's live readiness-gated bring-up phase and
	// per-interface device-load counts. It is the queryable twin of
	// [hmevent.CentralReadinessChangedEvent]; north-bound adapters read it to
	// distinguish "still initializing" from "offline", per central.
	readiness Readiness

	// stopHooksMu guards stopHooks.
	stopHooksMu sync.Mutex
	// stopHooks holds teardown functions grouped by shutdown tier (see
	// [StopTier]). Stop fires the tiers in order so an adapter can run its
	// teardown while the coordinators it depends on are still live.
	// Callers register via [AddStopHook] (tier-aware) or [AddOnStopHook]
	// (back-compat, External tier).
	stopHooks [stopTierCount][]func()
}

// SetAggregator attaches a metrics aggregator to the central. Called
// once at daemon boot, after all providers are wired. Nil is a valid
// value (detaches the current aggregator). The aggregator is exposed
// via the public [Aggregator] field; readers take it directly without
// synchronisation because it is only written once before any request
// is served.
func (u *Unit) SetAggregator(agg *metrics.Aggregator) {
	u.Aggregator = agg
}

// SetObservabilityRecorder fan-outs a recorder into every
// [observability.Recorder]-aware coordinator owned by the central.
// Daemons call this once at boot, after constructing the metrics
// recorder. Passing nil restores the no-op default.
func (u *Unit) SetObservabilityRecorder(rec observability.Recorder) {
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	u.Hub.SetRecorder(rec)
	u.Recovery.SetRecorder(rec)
	// Link was omitted here while AddLink and RemoveLink acquired
	// production callers, so every direct-link operation recorded into
	// the no-op default the constructor installs.
	u.Link.SetRecorder(rec)
}

// SystemInfo carries the CCU-side metadata northbound consumers need
// for status pages (model, version, hostname, openCCU/HA-app flag).
//
// The struct tags drive the per-kind partitioning in [internal/payload]
// the same way [device.Device] does — Kind.INFO surfaces fields HA
// MQTT-Discovery wants in its synthetic hub-device block, Kind.STATE
// is reserved for future state surfaces.
type SystemInfo struct {
	Model    string `payload:"info"`
	Version  string `payload:"info,alt=sw_version"`
	Hostname string `payload:"info"`
	Serial   string `payload:"info,alt=serial_number"`
	URL      string `payload:"info,alt=configuration_url"`
	IsHaApp  bool   `payload:"info,alt=is_ha_app"`

	// AuthEnabled reports whether the CCU requires authentication on its
	// own interfaces, and HTTPSRedirectEnabled whether it redirects plain
	// HTTP to HTTPS. Both are operator-facing security facts about the CCU
	// itself, not about the daemon, and both default to false when the
	// firmware does not implement the query.
	//
	// Deliberately untagged: [internal/payload] skips fields without a
	// `payload:` tag, which keeps these out of the MQTT-Discovery hub
	// block. They are a status-page concern, and adding them to the
	// discovery payload would change a published wire contract.
	AuthEnabled          bool
	HTTPSRedirectEnabled bool

	// Longitude and Latitude are the CCU's astro reference position in
	// decimal degrees, and Timezone its configured IANA zone. Every
	// sunrise/sunset time the CCU computes derives from the position, so
	// surfacing it makes a wrong location visible instead of letting it
	// skew every astro schedule silently.
	//
	// Untagged for the same reason as the two flags above: they are a
	// status-page concern and must not change the published MQTT
	// discovery hub block.
	Longitude float64
	Latitude  float64
	Timezone  string
}

// CCUInterface is one interface adapter the CCU itself reports as
// registered. This is the CCU-side view — it can differ from the
// interfaces the daemon is configured to talk to, which is exactly what
// makes it useful on a status page: an interface the CCU offers but the
// daemon does not manage (or vice versa) shows up as a mismatch.
type CCUInterface struct {
	// Type is the CCU interface type string (e.g. "HmIP-RF").
	Type string
	// Address is the interface identifier the CCU uses in callbacks.
	Address string
	// Port is the XML-RPC port the interface listens on.
	Port int
	// URL is the full XML-RPC endpoint URL the CCU reports.
	URL string
}

// New constructs a fully-wired Unit. Call [Start] to begin
// operation.
func New(cfg Config) (*Unit, error) {
	if cfg.Name == "" {
		return nil, errors.New("central: Config.Name is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	bus := events.NewBus()
	cache := coordinators.NewCacheCoordinator()
	devReg := registry.NewDeviceRegistry()
	descReg := registry.NewDeviceDescriptionRegistry()
	// Wire the built-in paramset patches (e.g. HM-CC-VG-1 SET_TEMPERATURE
	// MIN/MAX correction). Without this wiring the patches were defined in
	// `internal/store/patches/patches.go` but never applied (HM-CC-VG-1
	// SET_TEMPERATURE MIN/MAX wire-vs-patched drift).
	psReg := registry.NewParamsetRegistryWithPatches(patches.NewRegistry())

	c := &Unit{
		cfg:            cfg,
		logger:         logger,
		StateMachine:   statemachine.NewCentral(cfg.Name, bus),
		EventBus:       bus,
		DeviceRegistry: devReg,
		ModelRegistry:  registry.NewModelRegistry(),
		HubModel:       hub.NewHub(cfg.Name),
		DescRegistry:   descReg,
		ParamsetReg:    psReg,
		Cache:          cache,
		Clients:        coordinators.NewClientCoordinator(),
		Configuration:  coordinators.NewConfigurationCoordinator(descReg, psReg, devReg),
		Events:         coordinators.NewEventCoordinator(bus, cache, logger),
		Devices:        nil, // wired below: the coordinator needs the Unit as its domain model
		Hub:            coordinators.NewHubCoordinator(cfg.Name, bus),
		Recovery:       coordinators.NewConnectionRecoveryCoordinator(cfg.Name, bus),
		// Link starts with a nil resolver — the southbound wiring
		// adapter installs one via [Unit.SetLinkResolver]
		// once the client coordinator has at least one InterfaceClient.
		Link:           coordinators.NewLinkCoordinator(nil),
		Scheduler:      scheduler.New(logger, nil),
		Health:         health.NewTracker(),
		DeviceDetails:  devicedetails.New(),
		MetricsClients: clientpkg.NewMetricsClientProvider(cfg.Name),
		// SessionRecorder is created inactive — operators activate it
		// per-session via REST/WS to capture a CCU-call trace; the
		// daemon pays no overhead in normal operation. Disk-persistence
		// is wired separately via WireSessionRecorderPersistence.
		//
		// TTL = 600 s mirrors the reference SessionRecorder constructor
		// (cache.py:180 ttl_seconds=600). The recorder is a rolling-window
		// trace buffer: when an operator activates it and forgets to stop,
		// 10 minutes of replay capture cap the memory footprint instead of
		// growing unbounded. Entries are evicted lazily on read /
		// purge_expired pass.
		Recorder: session.New(session.Config{Active: false, TTL: 600 * time.Second}),
	}
	// The device coordinator mirrors the domain model in its registries, so
	// it takes the Unit itself as that model: a replaced device is evicted on
	// both sides at once, and a device-created announcement is only made for
	// an address the model can resolve. Constructed here because the Unit has
	// to exist first.
	c.Devices = coordinators.NewDeviceCoordinator(cfg.Name, bus, devReg, descReg, psReg, c, logger)

	c.registerCentralServices()

	// Attach the hub domain model to the hub coordinator. This wires the
	// notifier hooks of every program and system variable the hub scan
	// registers (PutProgram / PutSysvar), so activity flips, executions and
	// value changes reach the event bus — and through it the WebSocket
	// broadcasts (hub.program_changed / hub.program_executed /
	// hub.sysvar_changed). Without this call the hooks stay nil: the model
	// answers REST reads correctly while every bus-driven consumer is
	// silent, and a client that toggles a program never sees the flip
	// confirmed.
	c.Hub.SetHubModel(c.HubModel)

	// Wire the description registries as cache-size providers. Nothing else
	// calls SetSizeProviders, so without this the metrics/diagnostics surface
	// reported a flat 0 for the two largest caches on every central and
	// understated CacheMetricsSnapshot.TotalEntries by the same amount —
	// an operator sizing cache pressure read it as "nothing is cached".
	// The visibility slot stays nil here: that registry is daemon-global and
	// is built long after any central.
	cache.SetSizeProviders(descReg, psReg, nil)

	// Feed the RPC session recorder: the cache coordinator forwards every
	// CCU call/response to it, but only when a recorder is wired AND active.
	// Without this line CacheCoordinator.RecordSession is a no-op and the
	// recorder never sees any traffic. It stays inactive until an operator
	// starts a recording via REST /diagnostics/rpc-recording.
	cache.SetSessionRecorder(c.Recorder)

	// Wire the cache coordinator to the bus so it reacts to
	// [hmevent.DeviceRemovedEvent] / [hmevent.DataFetchCompletedEvent]
	// and emits [hmevent.CacheInvalidatedEvent] from ClearAll. The
	// SetCentralName call ensures emitted events carry the multi-CCU
	// scope.
	cache.SetCentralName(cfg.Name)
	cache.SubscribeToBus(bus)

	// The event coordinator stamps the same scope onto everything it
	// publishes from a raw callback. Nothing called this, so every
	// device-trigger and every raw-parameter event left the bus with an empty
	// central: the health wiring's per-central filter dropped them, and the
	// WebSocket device-trigger plane resolved the CCU serial from "" and
	// emitted unique ids that collide across CCUs.
	c.Events.SetCentralName(cfg.Name)

	return c, nil
}

// WireSessionRecorderPersistence connects the in-memory [session.Recorder]
// to a persistent [session.PersistStore] (typically backed by SQLite via
// `internal/store/sqlite.NewSessionRecorderStore`) and starts an
// auto-persist ticker. Returns a closer that stops the ticker; subsequent
// calls replace the previous wiring.
//
// `slug` scopes the persisted entries (per-central + per-session
// identifier). `interval` <= 0 disables auto-persist (only manual
// `Recorder.Persist` calls reach the store). Default interval is 30s.
//
// closes the production-replay path that was deferred
// in the audit. Without this wiring the recorder works as before
// (in-memory only), so existing callers are not affected.
func (u *Unit) WireSessionRecorderPersistence(ctx context.Context, store session.PersistStore, slug string, interval time.Duration) func() {
	if u == nil || u.Recorder == nil || store == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	u.recorderPersistMu.Lock()
	if u.recorderPersistUnsub != nil {
		u.recorderPersistUnsub()
		u.recorderPersistUnsub = nil
	}
	u.recorderStore = store
	u.recorderSlug = slug
	stop := u.Recorder.SetAutoPersist(ctx, store, u.cfg.Name, slug, interval)
	u.recorderPersistUnsub = stop
	u.recorderPersistMu.Unlock()
	return func() {
		u.recorderPersistMu.Lock()
		if u.recorderPersistUnsub != nil {
			u.recorderPersistUnsub()
			u.recorderPersistUnsub = nil
		}
		u.recorderPersistMu.Unlock()
	}
}

// ReloadRecorderFromPersistence merges any persisted entries back into the
// recorder (best-effort, live data wins) so a recording resumed after a
// restart includes what was captured before it. No-op when persistence is
// unwired or no store is set.
func (u *Unit) ReloadRecorderFromPersistence(ctx context.Context) {
	if u == nil || u.Recorder == nil {
		return
	}
	u.recorderPersistMu.Lock()
	store, slug := u.recorderStore, u.recorderSlug
	u.recorderPersistMu.Unlock()
	if store == nil {
		return
	}
	_ = u.Recorder.Load(ctx, store, u.cfg.Name, slug, session.DefaultMaxLoadEntries)
}

// Name returns the central's identifier.
func (u *Unit) Name() string { return u.cfg.Name }

// InstanceName returns the daemon-global instance identity used as the
// leading component of the wire interface_id. Empty when unset (the
// interface_id then falls back to the legacy `<central_name>-<interface>`
// form). See [Config.InstanceName] and ADR-0024.
func (u *Unit) InstanceName() string { return u.cfg.InstanceName }

// WireDevicesCreatedGate subscribes to the event bus and sets the
// devicesCreated flag on the first [hmevent.DeviceCreatedEvent]. This must be
// called once at boot (before RegisterStandardJobs) if any hub jobs should be
// gated behind device creation. Calling it again removes the previous
// subscription before installing a new one.
func (u *Unit) WireDevicesCreatedGate() {
	u.devicesCreatedMu.Lock()
	if u.devicesCreatedUnsub != nil {
		u.devicesCreatedUnsub()
		u.devicesCreatedUnsub = nil
	}
	u.devicesCreated = false
	u.devicesCreatedMu.Unlock()

	unsub := events.Subscribe(u.EventBus, func(_ hmevent.DeviceCreatedEvent) {
		u.devicesCreatedMu.Lock()
		u.devicesCreated = true
		u.devicesCreatedMu.Unlock()
	})
	u.devicesCreatedMu.Lock()
	u.devicesCreatedUnsub = unsub
	u.devicesCreatedMu.Unlock()
}

// IsDevicesCreated reports whether the device-creation phase has produced
// devices: either a [hmevent.DeviceCreatedEvent] was observed since
// [WireDevicesCreatedGate] was last called, or the domain model already holds
// devices. Returns true unconditionally when [WireDevicesCreatedGate] has not
// been called (no gate = no wait).
//
// The model is consulted because the event is an announcement, not the fact:
// the ingest pipeline materialises a whole interface without publishing one
// per device, so a gate that waited for the event alone would hold every
// gated hub job back on a central whose devices are all present.
func (u *Unit) IsDevicesCreated() bool {
	u.devicesCreatedMu.RLock()
	wired := u.devicesCreatedUnsub != nil
	created := u.devicesCreated
	u.devicesCreatedMu.RUnlock()
	// If the gate was never wired (devicesCreatedUnsub == nil), treat as
	// "already created" so gatedRunWithDevicesCreatedGate is a no-op.
	if !wired || created {
		return true
	}
	return u.ModelRegistry != nil && u.ModelRegistry.Len() > 0
}

// MarkSouthboundReady latches the central's initial southbound bring-up as
// complete. Called by the southbound adapter at the exact point it publishes
// [hmevent.CentralSouthboundReadyEvent] (set BEFORE the publish, so an event
// handler that queries the flag always observes true). Idempotent; the latch
// is never cleared — see the southboundReady field doc.
func (u *Unit) MarkSouthboundReady() {
	u.southboundReadyMu.Lock()
	u.southboundReady = true
	u.southboundReadyMu.Unlock()
}

// IsSouthboundReady reports whether the initial southbound bring-up has
// completed at least once. Late subscribers of
// [hmevent.CentralSouthboundReadyEvent] seed from this accessor to close the
// subscribe-after-fire race.
func (u *Unit) IsSouthboundReady() bool {
	u.southboundReadyMu.RLock()
	defer u.southboundReadyMu.RUnlock()
	return u.southboundReady
}

// Readiness is the queryable view of a central's readiness-gated southbound
// bring-up: the current phase plus the per-interface device-load progress.
type Readiness struct {
	Phase            hmenum.ReadinessPhase
	InterfacesLoaded int
	InterfacesTotal  int
}

// SetReadiness records the central's current bring-up phase and per-interface
// device-load counts. Called by the southbound wiring adapter as it advances
// through the readiness-gated bring-up.
func (u *Unit) SetReadiness(phase hmenum.ReadinessPhase, loaded, total int) {
	u.readinessMu.Lock()
	u.readiness = Readiness{Phase: phase, InterfacesLoaded: loaded, InterfacesTotal: total}
	u.readinessMu.Unlock()
}

// Readiness returns the central's current readiness view. The zero-value
// phase is normalized to [hmenum.ReadinessUnknown].
func (u *Unit) Readiness() Readiness {
	u.readinessMu.RLock()
	defer u.readinessMu.RUnlock()
	r := u.readiness
	if r.Phase == "" {
		r.Phase = hmenum.ReadinessUnknown
	}
	return r
}

// SetLinkResolver installs the [coordinators.ClientResolver] used by
// the [LinkCoordinator] for AddLink / RemoveLink / GetLinks. The
// southbound wiring adapter calls this once the client coordinator
// has at least one InterfaceClient registered. Pass nil to detach
// (the LinkCoordinator falls back to "no client" responses).
func (u *Unit) SetLinkResolver(r coordinators.ClientResolver) {
	if u.Link == nil {
		return
	}
	u.Link.SetResolver(r)
}

// DB returns the shared database handle (may be nil).
func (u *Unit) DB() *sql.DB { return u.cfg.DB }

// QueryFacade returns the read-only aggregate view north-bound
// adapters consume. The facade is built fresh on each call so the
// caller sees the current set of sub-components.
func (u *Unit) QueryFacade() *QueryFacade {
	return NewQueryFacade(u.cfg.Name, u.DeviceRegistry, u.ModelRegistry, u.Health)
}

// Available reports whether the central is currently operational. Returns
// true when the overall health status is not [health.StatusUnknown] or
// [health.StatusUnhealthy].
func (u *Unit) Available() bool {
	if u.Health == nil {
		return false
	}
	s := u.Health.Overall()
	return s == health.StatusHealthy || s == health.StatusDegraded
}

// HasPingPong reports whether at least one registered client supports the
// ping/pong keepalive mechanism.
func (u *Unit) HasPingPong() bool {
	if u.Clients == nil {
		return false
	}
	for _, entry := range u.Clients.List() {
		if entry.Client != nil && entry.Client.Capabilities().PingPong {
			return true
		}
	}
	return false
}

// GetChannel looks up the [*device.Channel] at channelAddress across
// the current device model. Returns nil when the address is unknown.
func (u *Unit) GetChannel(channelAddress string) *device.Channel {
	return u.QueryFacade().GetChannel(channelAddress)
}

// Start moves the central through STARTING → INITIALIZING → RUNNING
// and starts the scheduler. Implementation matches SPECIFICATION §11.2
// "bootstrap" at the orchestrator level; actual southbound client
// startup is performed separately by the client coordinator because
// each client has its own configuration.
//
// After the scheduler is running, Start wires the health tracker's
// SyncCentralState hook so every subsequent client-health change
// automatically re-evaluates the overall central state, and performs
// an initial EvaluateCentralState with fromStart=true to emit the
// first SystemStatusChangedEvent before any client reports in.
func (u *Unit) Start(ctx context.Context) error {
	if err := u.StateMachine.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone); err != nil {
		return err
	}
	if err := u.Scheduler.Start(ctx); err != nil {
		// The reason is what an operator is shown for a central that did
		// not come up. A fixed literal here names the call site rather
		// than the cause — a start aborted by a cancelled boot context
		// reads exactly like a scheduler defect. Anything the classifier
		// cannot place stays internal: the scheduler is the daemon's own
		// component, so an unrecognised failure of it is ours.
		reason := hmerr.ExceptionToFailureReason(err)
		if reason == hmenum.FailureReasonUnknown || reason == hmenum.FailureReasonNone {
			reason = hmenum.FailureReasonInternal
		}
		_ = u.StateMachine.TransitionTo(hmenum.CentralStateFailed, reason)
		return err
	}
	if err := u.StateMachine.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone); err != nil {
		return err
	}

	// Wire the health tracker so client-state changes automatically
	// re-evaluate the central state.
	if u.Health != nil {
		u.Health.OnClientStateChange(func(_ health.Status) {
			u.EvaluateCentralState("client_state_changed", false)
		})
		// Perform the initial sync so the central state reflects the
		// current health picture immediately after the scheduler starts,
		// rather than waiting for the first client-state event.
		u.Health.SyncCentralState(func(health.Status) {
			u.EvaluateCentralState("start", true)
		})
	} else {
		// No health tracker — still emit the initial system-status event.
		u.EvaluateCentralState("start", true)
	}
	return nil
}

// Stop performs the full shutdown choreography and transitions the central
// to STOPPED. Safe to call multiple times — a second call returns
// immediately when the state machine is already stopped.
//
// Teardown order (mirrors central_unit.py stop()). External hooks fire in
// three ordered tiers interleaved with the internal steps — see [StopTier]:
//   - [StopTierNorthbound] fires first (before step 1), everything still live.
//     1. Save files (best-effort)
//     2. Scheduler
//     3. ConnectionRecovery coordinator
//     4. Client coordinator (stops all InterfaceClients)
//     5. Device coordinator (waits for in-flight consistency-check goroutines)
//     6. Hub JSON-RPC logout (optional hook)
//     7. Hub coordinator clear
//   - [StopTierCoordinator] fires here (clients down, EventBus still live).
//     8. Cache coordinator unsubscribe + ClearOnStop
//     9. Event coordinator clear
//     10. Event-bus external-subscription clear
//
// 11. Event-bus full subscription clear
// 12. Recorder-persistence teardown
// 13. Transition to STOPPED
//   - [StopTierExternal] fires last (post-STOPPED, no coordinator dependency).
func (u *Unit) Stop() {
	if u.StateMachine.State() == hmenum.CentralStateStopped {
		return
	}

	ctx := context.Background()

	// StopTierNorthbound: external north-bound adapters detach while every
	// coordinator is still live (final availability=offline, command flush).
	u.fireStopTier(StopTierNorthbound)

	// 1. Scheduler — bounded drain so a stuck job cannot block daemon
	// shutdown indefinitely.
	if u.Scheduler != nil {
		u.Scheduler.StopWithTimeout(5 * time.Second)
	}

	// 2. ConnectionRecovery coordinator.
	if u.Recovery != nil {
		u.Recovery.Stop()
	}

	// 3. Client coordinator — stops all InterfaceClients.
	if u.Clients != nil {
		if err := u.Clients.StopClients(ctx); err != nil && u.logger != nil {
			u.logger.Warn("stop: stop_clients failed", "error", err)
		}
	}

	// 4. Device coordinator — drains any in-flight goroutine spawned by
	// ScheduleParamsetConsistencyCheck so it cannot outlive the coordinator.
	if u.Devices != nil {
		u.Devices.Stop()
	}

	// 7. Hub coordinator clear.
	if u.Hub != nil {
		u.Hub.Clear()
	}

	// StopTierCoordinator: south-bound clients are down but the EventBus is
	// still addressable — for bus-bridging adapters' teardown.
	u.fireStopTier(StopTierCoordinator)

	// 8. Cache coordinator: unsubscribe bus hooks + clear in-memory caches.
	if u.Cache != nil {
		u.Cache.UnsubscribeAll()
		u.Cache.ClearOnStop()
	}

	// 9. Event coordinator clear.
	if u.Events != nil {
		u.Events.Clear()
	}

	// 10 + 11. EventBus: clear external subscriptions then all subscriptions.
	if u.EventBus != nil {
		u.EventBus.ClearExternalSubscriptions()
		u.EventBus.ClearAllSubscriptions()
	}

	// 12. Recorder-persistence teardown.
	u.recorderPersistMu.Lock()
	if u.recorderPersistUnsub != nil {
		u.recorderPersistUnsub()
		u.recorderPersistUnsub = nil
	}
	u.recorderPersistMu.Unlock()

	// 13. Transition.
	_ = u.StateMachine.TransitionTo(hmenum.CentralStateStopped, hmenum.FailureReasonNone)

	// StopTierExternal: pure external cleanup after STOPPED (registry
	// unregister, tracker cleanup). Back-compat AddOnStopHook lands here.
	u.fireStopTier(StopTierExternal)
}

// SystemInformation returns the cached CCU-side metadata. The hub- wiring
// adapter populates the cache after Login + `get_backend_info`; before then
// the zero value is returned.
func (u *Unit) SystemInformation() SystemInfo {
	u.systemInfoMu.RLock()
	defer u.systemInfoMu.RUnlock()
	return u.systemInfo
}

// PatchSystemPosition updates only the cached astro position, leaving
// every other field untouched. Used after a successful position write so
// the status surfaces reflect it immediately, without re-running the
// whole hub-wiring pass just to refresh two numbers.
func (u *Unit) PatchSystemPosition(longitude, latitude float64) {
	u.systemInfoMu.Lock()
	u.systemInfo.Longitude = longitude
	u.systemInfo.Latitude = latitude
	u.systemInfoMu.Unlock()
}

// SetSystemInformation overwrites the cached metadata. Called from
// the hub-wiring adapter once `get_backend_info` returns.
func (u *Unit) SetSystemInformation(info SystemInfo) {
	u.systemInfoMu.Lock()
	u.systemInfo = info
	u.systemInfoMu.Unlock()
}

// CCUInterfaces returns the interface list the CCU reports for itself.
// The hub-wiring adapter populates it after Login; before then the slice
// is empty. The returned slice is a copy — callers may retain or sort it
// without racing the next refresh.
func (u *Unit) CCUInterfaces() []CCUInterface {
	u.ccuInterfacesMu.RLock()
	defer u.ccuInterfacesMu.RUnlock()
	return slices.Clone(u.ccuInterfaces)
}

// SetCCUInterfaces replaces the cached CCU-reported interface list.
// Called from the hub-wiring adapter once `Interface.listInterfaces`
// returns. Stores a copy so a caller mutating its own slice afterwards
// cannot reach into the cache.
func (u *Unit) SetCCUInterfaces(ifaces []CCUInterface) {
	u.ccuInterfacesMu.Lock()
	u.ccuInterfaces = slices.Clone(ifaces)
	u.ccuInterfacesMu.Unlock()
}

// Model returns the CCU model string from the cached system info. Empty
// string when system info has not been observed yet.
func (u *Unit) Model() string {
	return u.SystemInformation().Model
}

// Version returns the openccu-loom build version. Use [SystemInformation] for
// the CCU-reported firmware version.
func (u *Unit) Version() string {
	return build.Version
}

// CCUVersion returns the CCU firmware version string from the cached
// system information. Returns an empty string when system info has not
// been populated yet (before the hub adapter performs Login +
// get_backend_info). This is an alias for SystemInformation().Version
// so callers that explicitly want the CCU version have an unambiguous
// name.
func (u *Unit) CCUVersion() string {
	return u.SystemInformation().Version
}

// OnStateTransition registers a handler that fires every time the central
// state machine transitions to the given `to` state. Pass the zero value ("")
// for `to` to receive all transitions regardless of the destination state.
//
// Returns an unsubscribe function; calling it is idempotent.
func (u *Unit) OnStateTransition(to, from hmenum.CentralState, handler func(to, from hmenum.CentralState)) func() {
	if u.EventBus == nil {
		return func() {}
	}
	return events.Subscribe(u.EventBus, func(e hmevent.CentralStateChangedEvent) {
		if e.CentralName != u.cfg.Name {
			return
		}
		if to != "" && e.To != to {
			return
		}
		if from != "" && e.From != from {
			return
		}
		handler(e.To, e.From)
	})
}

// ResolveDeviceName returns a best-effort human-readable name for the device
// at address: the operator-assigned `Device.Name` first, then `Model` as
// fallback, then the address itself.
func (u *Unit) ResolveDeviceName(address string) string {
	if u.ModelRegistry == nil || address == "" {
		return address
	}
	dev, ok := u.ModelRegistry.Get(address)
	if !ok || dev == nil {
		return address
	}
	if name := dev.Name(); name != "" {
		return name
	}
	if dev.Model != "" {
		return dev.Model
	}
	return address
}

// HasDevice reports whether address is materialised in the domain model, the
// question every "may this device be announced?" decision comes down to.
func (u *Unit) HasDevice(address string) bool {
	if u == nil || u.ModelRegistry == nil || address == "" {
		return false
	}
	_, ok := u.ModelRegistry.Get(address)
	return ok
}

// RemoveDevice drops the device from the model registry, tears down each
// channel (event groups, calculated-DP subscriptions, custom-DP bindings),
// clears every EventBus subscription keyed to one of the device's data
// points, and publishes a [hmevent.DeviceRemovedEvent] so north-bound
// adapters (MQTT Discovery cleanup, cache eviction) can react.
//
// The channel teardown is performed first via [device.Device.RemoveChannel]
// so that event-group closers and calculated-DP unsubscribers run before the
// EventBus subscription sweep — matching the order Python's device.remove()
// calls channel.remove() for each channel before clearing the device registry
// entry.
//
// Idempotent: returns false when no device matches the address.
func (u *Unit) RemoveDevice(address string) bool {
	return u.removeDevice(address, false)
}

// RemoveDeviceForTeardown is [Unit.RemoveDevice] for a whole-model teardown:
// the cache-clear re-init drops every device and immediately re-pulls them.
// The removal event it publishes carries
// [hmevent.DeviceRemovedEvent.ModelTeardown], which the persistent
// VALUES/MASTER cache evictors honour by standing down — the operator's
// requested clear scope was already applied by cachereset.Service.Clear, and
// evicting again here would take the whole fleet's cache with it (ADR 0042).
// Every other consumer still reacts, so a device the CCU has genuinely
// dropped disappears from the north-bound planes at teardown rather than
// lingering until the next daemon boot.
func (u *Unit) RemoveDeviceForTeardown(address string) bool {
	return u.removeDevice(address, true)
}

func (u *Unit) removeDevice(address string, teardown bool) bool {
	if u == nil || u.ModelRegistry == nil || address == "" {
		return false
	}
	var interfaceID string
	if dev, ok := u.ModelRegistry.Get(address); ok && dev != nil {
		interfaceID = dev.InterfaceID
		// Tear down each channel first: closes event groups, unsubscribes
		// calculated DPs, and releases custom-DP bindings. The snapshot is
		// taken before removal so the loop does not race the channel map.
		for _, ch := range dev.Channels() {
			dev.RemoveChannel(ch.Address)
		}
		if u.EventBus != nil {
			for _, dp := range dev.AllDataPoints() {
				if idp, ok := dp.(interface{ UniqueID() string }); ok {
					u.EventBus.ClearSubscriptionsByKey(idp.UniqueID())
				}
			}
			for _, dp := range dev.AllMasterDataPoints() {
				if idp, ok := dp.(interface{ UniqueID() string }); ok {
					u.EventBus.ClearSubscriptionsByKey(idp.UniqueID())
				}
			}
		}
	}
	removed := u.ModelRegistry.Remove(address)
	if removed && u.EventBus != nil {
		events.Publish(u.EventBus, hmevent.DeviceRemovedEvent{
			Base:          hmevent.NewBase(),
			CentralName:   u.cfg.Name,
			InterfaceID:   interfaceID,
			Address:       address,
			ModelTeardown: teardown,
		})
	}
	return removed
}

// --- Service Methods ---

// AcceptDeviceInbox dispatches an "accept device from inbox" command to the
// configured hub-side handler. The caller wires `AcceptInboxFn` from the
// corresponding REST/WS adapter once the JSON-RPC session is up.
//
// Returns an error when no handler is wired yet (e.g. before Start).
func (u *Unit) AcceptDeviceInbox(ctx context.Context, address string) error {
	u.services.mu.RLock()
	fn := u.services.acceptInboxFn
	u.services.mu.RUnlock()
	if fn == nil {
		return errors.New("central: AcceptDeviceInbox not wired")
	}
	return fn(ctx, address)
}

// SetAcceptInboxFn wires the inbox-accept handler. Pass nil to
// detach.
func (u *Unit) SetAcceptInboxFn(fn func(ctx context.Context, address string) error) {
	u.services.mu.Lock()
	u.services.acceptInboxFn = fn
	u.services.mu.Unlock()
}

// SetDeviceIngestFn wires the materialiser that turns announced device
// descriptions into domain devices. The wiring installs it once the
// device pipeline and the per-interface backends exist. Pass nil to
// detach.
func (u *Unit) SetDeviceIngestFn(fn func(ctx context.Context, interfaceID string, descriptions []hmproto.DeviceDescription) error) {
	u.services.mu.Lock()
	u.services.deviceIngestFn = fn
	u.services.mu.Unlock()
}

// IngestDevices materialises the given descriptions into the domain
// model. An announcement that arrives before the wiring installed the
// materialiser is silently skipped rather than rejected: the interface
// bring-up materialises those devices anyway. Errors from the
// materialiser itself are propagated.
func (u *Unit) IngestDevices(ctx context.Context, interfaceID string, descriptions []hmproto.DeviceDescription) error {
	u.services.mu.RLock()
	fn := u.services.deviceIngestFn
	u.services.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, interfaceID, descriptions)
}

// CreateBackup triggers a backup on the CCU and returns the downloaded
// archive blob.
func (u *Unit) CreateBackup(ctx context.Context) ([]byte, error) {
	u.services.mu.RLock()
	fn := u.services.createBackupFn
	u.services.mu.RUnlock()
	if fn == nil {
		return nil, errors.New("central: CreateBackup not wired")
	}
	return fn(ctx)
}

// SetCreateBackupFn wires the backup-and-download handler.
func (u *Unit) SetCreateBackupFn(fn func(ctx context.Context) ([]byte, error)) {
	u.services.mu.Lock()
	u.services.createBackupFn = fn
	u.services.mu.Unlock()
}

// RenameDevice changes the operator-visible name of a device.
// Updates both the in-memory model and persists the new name through
// the configured `RenameDeviceFn` hook.
func (u *Unit) RenameDevice(ctx context.Context, address, name string) error {
	if address == "" {
		return errors.New("central: RenameDevice: empty address")
	}
	u.services.mu.RLock()
	fn := u.services.renameDeviceFn
	u.services.mu.RUnlock()
	// In-memory rename — always applied so the UI reflects the change
	// even when no persistent backend is wired (e.g. tests).
	if u.ModelRegistry != nil {
		if dev, ok := u.ModelRegistry.Get(address); ok && dev != nil {
			dev.SetName(name)
			u.PublishDeviceMetadataChanged(dev)
		}
	}
	if fn == nil {
		return nil
	}
	return fn(ctx, address, name)
}

// PublishDeviceMetadataChanged notifies north-bound adapters (MQTT,
// Matter) that a device's name, room, or function assignment changed, so
// they re-materialise the device's footprint (HA discovery name /
// suggested_area, Matter NodeLabel) instead of keeping the value observed
// at boot for the rest of the daemon's life. Called after every mutation
// of the fields those surfaces read — see RenameDevice, RenameChannel and
// the room/function setters in the adapter layer's DeviceAdminDomain.
func (u *Unit) PublishDeviceMetadataChanged(dev *device.Device) {
	if u.EventBus == nil || dev == nil {
		return
	}
	events.Publish(u.EventBus, hmevent.DeviceMetadataChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: u.Name(),
		InterfaceID: dev.InterfaceID,
		Address:     dev.Address,
	})
}

// SetRenameDeviceFn wires the persistent-rename handler. Pass nil to
// detach.
func (u *Unit) SetRenameDeviceFn(fn func(ctx context.Context, address, name string) error) {
	u.services.mu.Lock()
	u.services.renameDeviceFn = fn
	u.services.mu.Unlock()
}

// RenameDeviceWithChannels renames a device and, when includeChannels
// is true, renames every channel using the pattern "{name}:{no}" (the
// colon-separated channel-number suffix the CCU WebUI applies). Each
// channel rename is delegated to the same persistent `RenameDeviceFn`
// hook so the store stays consistent.
//
// When includeChannels is false this is equivalent to [RenameDevice].
func (u *Unit) RenameDeviceWithChannels(ctx context.Context, address, name string, includeChannels bool) error {
	if err := u.RenameDevice(ctx, address, name); err != nil {
		return err
	}
	if !includeChannels {
		return nil
	}
	if u.ModelRegistry == nil {
		return nil
	}
	dev, ok := u.ModelRegistry.Get(address)
	if !ok || dev == nil {
		return nil
	}
	u.services.mu.RLock()
	fn := u.services.renameDeviceFn
	u.services.mu.RUnlock()
	var firstErr error
	for _, ch := range dev.Channels() {
		chName := name + ":" + strconv.Itoa(ch.Number)
		ch.SetName(chName)
		if fn != nil {
			if err := fn(ctx, ch.Address, chName); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// RenameChannel changes the operator-visible name of a single channel.
// Updates both the in-memory channel model and persists the new name
// through the configured `RenameDeviceFn` hook (the hook dispatches to
// Channel.setName for a colon-suffixed channel address). channelAddress
// must be a full channel address ("<device>:<no>").
func (u *Unit) RenameChannel(ctx context.Context, channelAddress, name string) error {
	if channelAddress == "" {
		return errors.New("central: RenameChannel: empty address")
	}
	u.services.mu.RLock()
	fn := u.services.renameDeviceFn
	u.services.mu.RUnlock()
	// In-memory rename — always applied so the UI reflects the change
	// even when no persistent backend is wired (e.g. tests).
	if u.ModelRegistry != nil {
		base, _, _ := strings.Cut(channelAddress, ":")
		if dev, ok := u.ModelRegistry.Get(base); ok && dev != nil {
			if ch := dev.Channel(channelAddress); ch != nil {
				ch.SetName(name)
				u.PublishDeviceMetadataChanged(dev)
			}
		}
	}
	if fn == nil {
		return nil
	}
	return fn(ctx, channelAddress, name)
}

// LoadAndRefreshDataPointData triggers a fetch of every readable VALUES data
// point, seeds the data-cache and re-publishes value-changed events for any
// cache miss.
func (u *Unit) LoadAndRefreshDataPointData(ctx context.Context) error {
	u.services.mu.RLock()
	fn := u.services.loadAndRefreshFn
	u.services.mu.RUnlock()
	if fn == nil {
		return errors.New("central: LoadAndRefreshDataPointData not wired")
	}
	return fn(ctx)
}

// SetLoadAndRefreshFn wires the data-point reload handler.
func (u *Unit) SetLoadAndRefreshFn(fn func(ctx context.Context) error) {
	u.services.mu.Lock()
	u.services.loadAndRefreshFn = fn
	u.services.mu.Unlock()
}

// ServiceWiringStatus reports, for each service-method hook on the
// central, whether the hub-wiring adapter has populated it. Its readers
// today are tests that want to assert "the southbound wiring completed"
// without hammering every service method individually; it is shaped to
// be servable from a diagnostics endpoint, but nothing serves it yet.
//
// Service methods are wired asynchronously after Start() — the JSON-RPC
// session must come up first. A status snapshot taken immediately
// after [Start] therefore reports false for every entry; once the hub
// adapter has run the entries flip to true. Each service method
// already returns a clean "not wired" error when invoked early, so
// this map is purely informational.
//
// Only hooks something in production actually wires belong here.
// `accept_inbox` is deliberately absent: it is reachable solely through
// the [payload.ServiceRegistry] registration in [registerCentralServices],
// which no production caller invokes — the live accept path is
// DeviceAdminDomain.AcceptInboxDevice. Listing it would make
// [Unit.ServiceWiringComplete] permanently false, which is what it was
// until the never-wired persistence and config-validation hooks were
// removed.
func (u *Unit) ServiceWiringStatus() map[string]bool {
	u.services.mu.RLock()
	defer u.services.mu.RUnlock()
	return map[string]bool{
		"create_backup":    u.services.createBackupFn != nil,
		"rename_device":    u.services.renameDeviceFn != nil,
		"load_and_refresh": u.services.loadAndRefreshFn != nil,
	}
}

// ServiceWiringComplete is true when every service-method hook has
// been wired. Returns false until the hub-wiring adapter finishes its
// post-Start work. Convenience over [ServiceWiringStatus] for callers
// that only care about the boolean.
func (u *Unit) ServiceWiringComplete() bool {
	for _, wired := range u.ServiceWiringStatus() {
		if !wired {
			return false
		}
	}
	return true
}

// EvaluateCentralState is the single-entry-point that re-evaluates the
// overall central state after any client-state change. The trigger
// parameter is a human-readable label used for logging. When fromStart
// is true the evaluation skips the "was this the last known state?" check
// and always emits a SystemStatusChangedEvent, which is needed at
// daemon startup before the first health record arrives.
//
// Guards applied before transitioning:
//   - RUNNING is suppressed while any interface is actively recovering and
//     demoted to DEGRADED, so an in-flight recovery never reads as
//     all-healthy. The recovery pipeline does not call back here; the next
//     client-state change re-evaluates once the pipeline has settled.
//   - A central without a single registered InterfaceClient holds its
//     current state: connectivity is not observable yet, and reading that
//     as FAILED would announce an outage on every boot.
//   - DEGRADED and FAILED are only allowed from operational states
//     (RUNNING, DEGRADED, RECOVERING, INITIALIZING). The state machine's
//     transition table enforces the exact set; a rejected transition is
//     logged rather than dropped, because the machine then keeps a state
//     the connectivity picture contradicts.
func (u *Unit) EvaluateCentralState(trigger string, fromStart bool) {
	if u.Clients == nil || u.Health == nil {
		return
	}
	entries := u.Clients.List()
	allConnected := u.Clients.Available()
	anyConnected := u.Clients.AnyClientActive()
	allCallbacksAlive := u.Clients.IsAlive()
	current := u.StateMachine.State()

	// Determine the target state based on client connectivity and callback health:
	//   RUNNING   — every client is connected and all callbacks are alive
	//   DEGRADED  — at least one client is connected, but not all; or all
	//               are connected but one or more callbacks have gone stale
	//   FAILED    — no clients are connected
	var newState hmenum.CentralState
	switch {
	case len(entries) == 0:
		// Not a single InterfaceClient is registered yet: the south-bound
		// bring-up registers them only once the CCU reports ready, so there
		// is nothing to judge connectivity by. Hold the current state instead
		// of reading "no connected client" as an outage — otherwise every
		// boot and every runtime adopt would announce FAILED before the first
		// interface has had a chance to come up. The same distinction keeps
		// the `central` health component healthy during the gated wait.
		newState = current
	case allConnected && allCallbacksAlive:
		newState = hmenum.CentralStateRunning
	case allConnected && !allCallbacksAlive:
		// All interfaces report connected but at least one callback is
		// no longer receiving events — treat as degraded rather than
		// fully healthy so north-bound adapters surface the stale
		// callback to operators.
		newState = hmenum.CentralStateDegraded
	case anyConnected:
		newState = hmenum.CentralStateDegraded
	default:
		newState = hmenum.CentralStateFailed
	}

	// Guard: while any interface is in active recovery the central must
	// remain in RECOVERING state. When all clients still report Connected
	// (the recovery pipeline has not yet reached the Reconnect stage), the
	// computed newState would be RUNNING — but emitting an "all-healthy"
	// event while recovery is in flight misleads north-bound consumers.
	// Demote to DEGRADED so the event carries the correct operator-visible
	// state and Healthy=false is surfaced. Without this demotion the
	// recovery pipeline's early-stage failures (TCP_CHECK, RPC_CHECK) would
	// never produce a Healthy=false event because the client remains
	// Connected until the Reconnect stage explicitly transitions it.
	inRecovery := u.Recovery != nil && u.Recovery.InRecovery()
	if newState == hmenum.CentralStateRunning && inRecovery {
		newState = hmenum.CentralStateDegraded
	}

	if !fromStart && current == newState {
		return
	}

	// Collect degraded interface IDs + reasons for structured logging and event payload.
	var degradedIfaces []string
	var degradedReasons map[string]hmenum.FailureReason
	if newState == hmenum.CentralStateDegraded {
		// Pull per-interface failure reasons from the state machine which tracks
		// them via MarkInterfaceDegraded. For interfaces not yet marked, use the
		// catch-all FailureReasonNetwork.
		smReasons := u.StateMachine.DegradedInterfaces()
		for _, e := range entries {
			if !e.Connected() {
				degradedIfaces = append(degradedIfaces, e.InterfaceID)
				if degradedReasons == nil {
					degradedReasons = make(map[string]hmenum.FailureReason)
				}
				if r, ok := smReasons[e.InterfaceID]; ok {
					degradedReasons[e.InterfaceID] = r
				} else {
					degradedReasons[e.InterfaceID] = hmenum.FailureReasonNetwork
				}
			}
		}
	}

	if u.logger != nil {
		u.logger.Debug(
			"evaluate_central_state",
			"trigger", trigger,
			"all_connected", allConnected,
			"any_connected", anyConnected,
			"in_recovery", inRecovery,
			"from", current,
			"to", newState,
		)
	}

	// TransitionTo validates against the allowed-transitions table and sets
	// the degraded-interface map atomically. A rejected transition leaves the
	// machine on its previous state, which means the daemon keeps reporting a
	// state the connectivity picture contradicts — never silent, always
	// logged, so the divergence is diagnosable from the log alone.
	var smOpts []statemachine.CentralTransitionOption
	if len(degradedReasons) > 0 {
		smOpts = append(smOpts, statemachine.WithDegradedInterfaces(degradedReasons))
	}
	if newState != current {
		if err := u.StateMachine.TransitionTo(newState, hmenum.FailureReasonNone, smOpts...); err != nil && u.logger != nil {
			// STOPPED is terminal, so an evaluation racing a shutdown is
			// expected rather than a defect.
			if current == hmenum.CentralStateStopped {
				u.logger.Debug("evaluate_central_state_transition_rejected",
					"trigger", trigger, "from", current, "to", newState, "err", err)
			} else {
				u.logger.Warn("evaluate_central_state_transition_rejected",
					"trigger", trigger, "from", current, "to", newState, "err", err)
			}
		}
	}
	// Report what the machine actually holds, not what we asked for. A
	// rejected transition leaves the machine on its previous state, and the
	// event is the only place that divergence would otherwise surface — a
	// payload announcing a state the machine, /health and the SPA badge all
	// disagree with.
	published := u.StateMachine.State()

	// Healthy reflects the operator-visible connection quality:
	//   - false when no client is active (all disconnected)
	//   - false when recovery is in progress (connection is being restored)
	//   - true when all clients are connected and no recovery is running
	healthy := (allConnected || anyConnected) && !inRecovery

	// Publish a system-status event so north-bound adapters observe the flip.
	if u.EventBus != nil {
		events.Publish(u.EventBus, hmevent.SystemStatusChangedEvent{
			Base:                     hmevent.NewBase(),
			CentralName:              u.cfg.Name,
			Healthy:                  healthy,
			CentralState:             published,
			DegradedInterfaces:       degradedIfaces,
			DegradedInterfaceReasons: degradedReasons,
		})
	}
}

// ReadableGenericDataPoints returns every VALUES-paramset data point across
// every registered device that advertises READ in its operations bitmask.
//
// Order: alphabetical by device address, then by channel address, then by
// parameter name.
func (u *Unit) ReadableGenericDataPoints() []device.ParameterDataPoint {
	if u.ModelRegistry == nil {
		return nil
	}
	devs := u.ModelRegistry.List()
	out := make([]device.ParameterDataPoint, 0)
	for _, d := range devs {
		for _, dp := range d.AllDataPoints() {
			if dp.ParameterData().Operations.IsReadable() {
				out = append(out, dp)
			}
		}
	}
	return out
}
