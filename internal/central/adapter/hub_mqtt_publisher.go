// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// HubMQTTPublisher subscribes to every hub entity's OnUpdate hook and
// publishes the change to the configured [mqtt.Wiring].
//
// Programs and Sysvars are wired via the hub model's per-entity
// OnUpdate callbacks so the publisher reacts to every value change
// without polling. AlarmMessages and ServiceMessages use the same
// aggregate-replace hook on the hub model. InstallMode and
// Connectivity changes arrive as domain events on each central's
// EventBus (InstallModeChangedEvent / ConnectivityChangedEvent).
//
// Every broker interaction the publisher performs — wiring-time discovery,
// initial state, model callbacks and bus events alike — is handed to a single
// fan-out worker instead of running inline. Both of the goroutines that drive
// this publisher are shared: the event bus dispatches serially for ALL
// centrals, and the hub model mutates on the refresh goroutine. A broker
// publish that blocks on either of them stalls far more than the hub plane.
// See [HubMQTTPublisher.publish].
//
// Lifecycle: NewHubMQTTPublisher → Start → Stop. Start is idempotent:
// existing subscriptions are released and the previous worker is stopped
// before new ones are attached.
type HubMQTTPublisher struct {
	registry *central.Registry
	wiring   *mqtt.Wiring
	logger   *slog.Logger

	mu     sync.Mutex
	unsubs []func()

	// fanout drains every hub-plane publish on one worker goroutine. Created
	// by Start, torn down by Stop, held behind an atomic pointer because the
	// enqueue side runs on bus-dispatch and model-mutation goroutines. Nil
	// before the first Start, which makes [HubMQTTPublisher.publish] fall back
	// to an inline publish so a unit test can drive an internal wiring helper
	// without a lifecycle.
	fanout atomic.Pointer[mqttFanout]

	// rega holds the per-CCU ReGa liveness conclusion that [ccuReachable]
	// conjoins with the interface states. Created once, by the constructor,
	// and deliberately NOT rebuilt by Start: Start runs again on every broker
	// reconnect, and a tracker that came back empty would forget a ReGa
	// already known to be dead — the gate's first write after a
	// [mqtt.Bridge.ResetRuntimeGates] is exempt from the dwell, so that
	// forgetting would immediately republish a retained `online` for a CCU
	// whose sysvars are frozen.
	rega *regaLivenessTracker

	// regaTargets is the BOOT snapshot of how each central is probed, keyed by
	// central name. Guarded by mu; written by
	// [HubMQTTPublisher.SetRegaLivenessTargets] and read when a poller starts.
	// It is the fallback, not the source of truth — see regaConfig.
	regaTargets map[string]*regaLivenessTarget

	// regaConfig resolves one central's connection config from the LIVE fleet.
	// Guarded by mu; set by [HubMQTTPublisher.SetRegaLivenessConfigSupplier]
	// and asked every time a poller starts, never captured into a snapshot.
	//
	// A CCU adopted at runtime is in no boot-time list, so a target map filled
	// once from the boot config has no entry for it and its poller never
	// starts at all — leaving the very defect the ReGa half exists to fix
	// (a hung ReGaHss with `rfd` still serving, folding to `online`) unfixed
	// for every runtime-adopted CCU. Same trap, and the same shape of fix, as
	// [mqtt.Bridge.cleanupCentralNames]'s per-sweep supplier.
	regaConfig func(centralName string) (config.CentralConfig, bool)

	// regaPollers holds one stop closer per central whose ReGa poller is
	// running, keyed by central name. Guarded by mu.
	//
	// The closers are also in unsubs, which is what Stop drains; this index
	// exists so ONE central's poller can be stopped on its own, which is what
	// [HubMQTTPublisher.RetractCentral] needs. A central removed at runtime
	// never reaches Stop, and a poller that outlives its central keeps
	// probing a decommissioned host and re-seeds a retained `online` on the
	// gate topic that was just retracted.
	regaPollers map[string]func()
}

// NewHubMQTTPublisher constructs the publisher. No subscriptions are
// attached until [Start] is called.
func NewHubMQTTPublisher(reg *central.Registry, w *mqtt.Wiring, logger *slog.Logger) *HubMQTTPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &HubMQTTPublisher{
		registry:    reg,
		wiring:      w,
		logger:      logger,
		rega:        newRegaLivenessTracker(),
		regaTargets: map[string]*regaLivenessTarget{},
		regaPollers: map[string]func(){},
	}
}

// SetRegaLivenessTargets tells the publisher how to reach each central's
// `/ise/checkrega.cgi`, which is the second input of the per-CCU
// reachability gate. Centrals without a host are skipped; a central with no
// target is never probed and its liveness stays unknown, which folds exactly
// as the gate did before this signal existed.
//
// Separate from the constructor because the publisher is built from the
// registry and the MQTT wiring, neither of which carries the CCU's
// connection config. Call it before [HubMQTTPublisher.Start]; a later call
// takes effect on the next Start.
//
// It is the BOOT half only. A central adopted at runtime never reaches the
// list this is called with, so wire
// [HubMQTTPublisher.SetRegaLivenessConfigSupplier] as well — that one is
// asked per poller start and is what covers the live fleet.
func (p *HubMQTTPublisher) SetRegaLivenessTargets(centrals []config.CentralConfig) {
	targets := make(map[string]*regaLivenessTarget, len(centrals))
	// Indexed rather than ranged by value: config.CentralConfig is a large
	// struct and copying one per iteration is what gocritic's rangeValCopy
	// flags.
	for i := range centrals {
		if t := newRegaLivenessTarget(&centrals[i]); t != nil {
			targets[centrals[i].Name] = t
		}
	}
	p.mu.Lock()
	p.regaTargets = targets
	p.mu.Unlock()
}

// SetRegaLivenessConfigSupplier hands the publisher a resolver for one
// central's connection config, asked every time a ReGa poller starts.
//
// This is the live-fleet half of the probe target, and it is a function
// rather than a list for the same reason [mqtt.Bridge.cleanupCentralNames]
// takes a supplier: a CCU adopted at runtime is in no boot-time list. With
// only the boot snapshot, [HubMQTTPublisher.regaTargetFor] returns nil for
// such a CCU, [HubMQTTPublisher.startRegaLivenessPoll] returns before it
// starts anything, its liveness stays [regaLivenessUnknown] forever and the
// gate folds it `online` — so a hung ReGaHss with `rfd` still serving
// publishes no `offline` at all for that class of central, which is the
// exact defect the ReGa half was added to fix.
//
// resolve reports false for a central it does not know, and only then is the
// boot snapshot consulted: an `ok` settles the question, including an `ok`
// carrying a config with no host, which resolves to "known, nothing to
// probe" rather than to the boot address. Safe to call at any time, including after
// [HubMQTTPublisher.Start] — it takes effect on the next poller start, which
// is what every re-Start performs.
func (p *HubMQTTPublisher) SetRegaLivenessConfigSupplier(resolve func(centralName string) (config.CentralConfig, bool)) {
	p.mu.Lock()
	p.regaConfig = resolve
	p.mu.Unlock()
}

// regaTargetFor returns the probe to run for one central, or nil when there
// is nothing to probe (no known config, or a config with no host).
//
// Resolved HERE, at poller-start time, rather than read out of a map filled
// at boot: see [HubMQTTPublisher.SetRegaLivenessConfigSupplier] for the
// runtime-adopted central this ordering is about.
//
// THE LIVE FLEET SETTLES IT. An `ok` from the supplier ends the resolution
// even when the config it answers with carries no host and therefore yields
// no target: the answer is "this central is known, and there is nothing to
// probe", not "ask the boot snapshot". Falling through on a hostless live
// config would probe the BOOT host of a central the live fleet has since
// moved — the same wrong-address probe that
// TestRegaLivenessTargetsPreferTheLiveFleetOverTheBootSnapshot exists to
// keep out, arriving through the one branch that test does not drive. No
// path is known today that produces an adopted config with an empty host
// (config validation requires one), so this is the doc and the code being
// made to agree on the safe reading rather than a live defect being closed.
func (p *HubMQTTPublisher) regaTargetFor(centralName string) *regaLivenessTarget {
	p.mu.Lock()
	resolve := p.regaConfig
	snapshot := p.regaTargets[centralName]
	p.mu.Unlock()
	if resolve != nil {
		if cc, ok := resolve(centralName); ok {
			return newRegaLivenessTarget(&cc)
		}
	}
	return snapshot
}

// Start attaches subscriptions to every hub entity of every central and
// queues one immediate publish per entity so retained MQTT topics carry
// the current observed state, not just future changes. Idempotent:
// existing subscriptions are released and the previous worker stopped first.
//
// The publishes are queued, not performed: Start returns once the wiring is in
// place. That matters because Start also runs from the broker's on-connect
// hook and from the ready-driven re-wire, neither of which should sit behind a
// full hub-plane republish.
func (p *HubMQTTPublisher) Start(ctx context.Context) {
	p.Stop()
	if p.registry == nil || p.wiring == nil {
		return
	}

	f := newMQTTFanout()
	f.start(ctx)
	p.fanout.Store(f)

	// Wire against the worker's context, not the caller's: it is the context
	// every queued publish runs under, so Stop aborts in-flight broker I/O
	// instead of waiting it out. It also survives a caller context that ends
	// with the on-connect hook that triggered this Start. f.ctx IS a child of
	// ctx — start derived it — so cancellation still propagates from the caller.
	for _, u := range p.registry.List() {
		//nolint:contextcheck // f.ctx is the child of ctx that start derived; publishes must outlive the caller's
		p.wireOneCentral(f.ctx, u)
	}
}

// Stop releases every subscription registered by Start and stops the fan-out
// worker, cancelling any publish it is blocked in. Safe to call before Start
// (no-op) or multiple times; no goroutine outlives the call.
func (p *HubMQTTPublisher) Stop() {
	p.mu.Lock()
	unsubs := p.unsubs
	p.unsubs = nil
	// The per-central poller index holds the same closers this teardown is
	// about to run. Clearing it is bookkeeping, not safety: the closers are
	// sync.Once-guarded and their `done` channel is already closed by the
	// time Stop returns, so a RetractCentral that called a retained one would
	// simply return at once. What the clear buys is that the map does not
	// accumulate an entry per central per wiring generation, and that it
	// never answers for a poller that no longer exists.
	p.regaPollers = map[string]func(){}
	p.mu.Unlock()
	// Unsubscribe before stopping the worker so no source can enqueue onto a
	// queue nobody drains any more.
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
	if f := p.fanout.Swap(nil); f != nil {
		// stopDraining, not stop: this queue can be holding a RETRACTION, and
		// a retraction is the one job the next Start does not re-issue — it
		// publishes the declare instead. Start begins with Stop, and Start is
		// what the broker's on-connect hook calls, so a reconnect landing in a
		// removal window would otherwise discard the retract for good and
		// leave the removed CCU's discovery configs retained forever. See
		// TestAQueuedRetractSurvivesABrokerReconnect.
		f.stopDraining(fanoutStopGrace)
	}
}

// Flush blocks until the fan-out worker has drained every publish queued
// before the call. It is a test barrier — the publish path is intentionally
// asynchronous — and a no-op before Start.
func (p *HubMQTTPublisher) Flush() {
	if f := p.fanout.Load(); f != nil {
		f.flush()
	}
}

// RetractCentral clears every retained hub-plane discovery config the publisher
// declared for u's central — programs, sysvars, install-mode, connectivity,
// alarm/service messages, the system-health / last-event-age / connection-latency
// sensors, the inbox and the hub update entity — by re-publishing each with an
// empty payload. It is the whole-central counterpart to the per-entity OnRemoved
// retract hooks in wireOne*.
//
// Those hooks only fire for an entity the CCU drops one at a time; a whole
// central removed at runtime never triggers them, and the orphan-topic sweep is
// scoped to registered centrals so it can never reach a central that is leaving
// the registry. Without this the removed CCU's hub entities stay retained on the
// broker — visible and frozen in Home Assistant, and surviving daemon restarts —
// until the topic is cleared by hand.
//
// Call it BEFORE the unit's model is torn down: the hub-plane items are
// rebuilt from the live hub model (programs, sysvars, install-mode DPs), so
// they carry the same unique_ids the declare side published.
//
// connectivityInterfaces is the central's interface list for the
// connectivity binary_sensor retract. It must be captured by the caller
// BEFORE BringUpManager.RemoveCentral runs: that call drains and removes
// every entry from u.Clients as part of its own teardown, so reading
// u.Clients.List() here — after that call already ran — sees an empty
// registry and silently skips every connectivity retract. Pass nil to fall
// back to reading u.Clients.List() live (only correct when the caller is
// certain the client registry has not been torn down yet).
func (p *HubMQTTPublisher) RetractCentral(u *central.Unit, connectivityInterfaces []hmenum.Interface) {
	if p == nil || u == nil || p.wiring == nil {
		return
	}
	// FIRST, and before anything is queued: end this CCU's ReGa poller and
	// wait for it. It is the only source that keeps publishing on a timer
	// rather than on an event, so it is the only one that can enqueue a fold
	// AFTER the retract below and leave a retained `online` on the gate topic
	// of a CCU that is gone. Done ahead of the bridge check too: the poller
	// has to go even when there is nowhere to publish the retract.
	//
	// WHAT THE FIFO FAN-OUT DOES AND DOES NOT GUARANTEE. Stopping the poller
	// first orders the retract after everything that poller queued, and the
	// single FIFO worker keeps that order — WITHIN ONE WIRING GENERATION. The
	// generation is not a given: [HubMQTTPublisher.Start] begins with
	// [HubMQTTPublisher.Stop], and Start is what the broker supervisor's
	// on-connect hook calls, so a reconnect landing between this enqueue and
	// the worker's drain retires the queue the retract is sitting in. That
	// used to discard it outright — a retract is the one job the next Start
	// does not re-issue, since the next Start publishes the DECLARE instead —
	// which is why Stop now drains the durable remainder before cancelling
	// ([mqttFanout.stopDraining]). Reproduced, and pinned, by
	// TestAQueuedRetractSurvivesABrokerReconnect.
	//
	// One exposure of that shape is NOT closed here and is stated rather than
	// implied: the unit stays in the shared registry until Unit.Stop, which
	// removeCentral runs after evictModel, so a re-wire in that window
	// iterates the leaving central too. Its declares are suppressed by the
	// bridge's /config dedup gate for as long as the payloads are unchanged,
	// and its gate topic is debounced, so no re-seeded `online` could be
	// produced in a test; the ordering that would produce one has not been
	// built and is therefore not claimed to exist. Removing the unit from the
	// registry before the retract is the fix if it ever is built, and it is a
	// change to removeCentral's teardown order, not to this function.
	p.stopRegaLivenessPoll(u.Name())
	b := p.wiring.Bridge()
	if b == nil {
		// MQTT disabled at runtime keeps the Wiring alive with no bridge; a
		// retract has nowhere to go and there is nothing retained to clear.
		return
	}
	// Publishes run on the fan-out worker under its own context, so they survive
	// the (possibly request-scoped) removeCentral caller and are cancelled only
	// by the publisher's own Stop. Fall back to Background when no worker runs
	// (a unit test driving the publisher without Start).
	ctx := context.Background()
	if f := p.fanout.Load(); f != nil {
		ctx = f.ctx
	}
	centralName := u.Name()
	disco := b.DefaultBuilder()
	if disco == nil {
		disco = mqtt.NewDefaultDiscoveryBuilder(b.Topics(), centralName)
	}
	// Re-stamp the serial that gates every hub-discovery unique_id so the retract
	// items address the same topics the declare side published. It is normally
	// already present from the central's earlier wiring; re-stamping is cheap and
	// idempotent, and only a resolved serial is written (never an empty one).
	if hi := hubInfoFromUnit(u); hi.Serial != "" {
		disco.SetHubInfoFor(centralName, hi)
	}

	var items []mqtt.DiscoveryItem
	if hubModel := u.HubModel; hubModel != nil {
		for _, prog := range hubModel.Programs() {
			if prog == nil || prog.Internal() {
				continue
			}
			items = append(items, disco.BuildProgramDiscoveryRoles(
				centralName, programSpecFor(prog), b.ProgramRoles(centralName, prog),
			)...)
		}
		for _, sv := range hubModel.Sysvars() {
			if sv == nil {
				continue
			}
			items = append(items, disco.BuildSysvarDiscovery(centralName, sysvarSpecFor(sv)))
		}
		for _, dp := range hubModel.InstallModeDPs() {
			if dp == nil || dp.InterfaceID == "" {
				continue
			}
			items = append(items,
				disco.BuildInstallModeSensorDiscovery(centralName, dp.InterfaceID),
				disco.BuildInstallModeButtonDiscovery(centralName, dp.InterfaceID))
		}
	}
	// Connectivity: one binary_sensor per registered interface, keyed by the same
	// `<central>-<iface>` wire id seedConnectivityDiscovery declares.
	switch {
	case connectivityInterfaces != nil:
		for _, iface := range connectivityInterfaces {
			if iface == "" {
				continue
			}
			items = append(items, disco.BuildConnectivityDiscovery(centralName, WireInterfaceID(centralName, iface)))
		}
	case u.Clients != nil:
		for _, entry := range u.Clients.List() {
			if entry == nil {
				continue
			}
			iface := entry.Interface
			if iface == "" {
				iface = BareInterfaceFromWireID(centralName, entry.InterfaceID)
			}
			if iface == "" {
				continue
			}
			items = append(items, disco.BuildConnectivityDiscovery(centralName, WireInterfaceID(centralName, iface)))
		}
	}
	// Central-wide singletons, published unconditionally in wireOneCentral.
	items = append(
		items,
		disco.BuildAlarmMessagesDiscovery(centralName),
		disco.BuildServiceMessagesDiscovery(centralName),
		disco.BuildDaemonStatusDiscovery(centralName),
		disco.BuildSystemHealthDiscovery(centralName),
		disco.BuildLastEventAgeDiscovery(centralName),
		disco.BuildConnectionLatencyDiscovery(centralName),
		disco.BuildInboxDiscovery(centralName),
		disco.BuildHubUpdateDiscovery(centralName),
	)

	p.publish(func() {
		retractHubDiscoveryItems(ctx, b, items)
		retractHubRawState(ctx, b, u, centralName)
	})
	// The tracked ReGa liveness of that CCU goes with the gate topic that
	// consumed it. Keeping it would leave a conclusion about a central
	// nothing manages any more, and a central re-added under the same name
	// would inherit it instead of seeding from its own first probe.
	//
	// Correct only because the poller for this central was stopped at the top
	// of this function. Forgetting while a poller still runs is worse than
	// keeping the entry: the next tick finds no entry, takes observe's seed
	// branch, and republishes `online` over the retract that just went out.
	p.rega.forget(centralName)
}

// retractHubRawState clears the raw-plane retained state topics the
// discovery half above has no reach into: program state and execute-
// availability, sysvar state, and the hub firmware-update singleton.
// retractHubDiscoveryItems only ever re-publishes `.../config` topics — it
// never touches the state topics those configs point at — so without this a
// removed central's raw-plane consumers (anything subscribing outside HA
// discovery) kept reading the last-known program/sysvar/update state
// forever.
//
// Built from the same live hub model the discovery items were, using the
// identical [payload.MQTTAddressable] resolvers the publish side calls
// (Bridge.RetractSysvarState / RetractProgramTopics / RetractHubUpdate
// mirror PublishSysvar / PublishProgram / PublishHubUpdate) so the retract
// side can never drift from what was actually published.
func retractHubRawState(ctx context.Context, b *mqtt.Bridge, u *central.Unit, centralName string) {
	hubModel := u.HubModel
	if hubModel == nil {
		return
	}
	for _, prog := range hubModel.Programs() {
		if prog == nil || prog.Internal() {
			continue
		}
		_ = b.RetractProgramTopics(ctx, centralName, prog)
	}
	for _, sv := range hubModel.Sysvars() {
		if sv == nil {
			continue
		}
		_ = b.RetractSysvarState(ctx, centralName, sv)
	}
	_ = b.RetractHubUpdate(ctx, centralName)
	// The per-CCU reachability gate. `offline` would be a claim about a CCU
	// that no longer exists; the removal retracts it instead.
	_ = b.RetractHubStatus(ctx, centralName)
}

// publish hands one hub-plane broker interaction to the fan-out worker.
//
// Every job is enqueued as durable: the hub plane carries discovery configs
// and aggregate replacements (alarm/service messages, inbox, update info,
// program and sysvar state) whose loss does not self-heal — nothing re-sends
// them, so a dropped payload leaves an entity missing or frozen in Home
// Assistant until the daemon restarts. Their arrival rate is bounded by the
// CCU refresh cadence, not by device event traffic, so the queue has no
// realistic way to grow without bound. See [fanoutJob].
//
// Because the worker is single and the queue is FIFO, every job also runs
// serialised in enqueue order. Handler-owned state — the connectivity
// discovery-dedup map above all — is therefore touched by exactly one
// goroutine and needs no lock of its own.
func (p *HubMQTTPublisher) publish(job func()) {
	if f := p.fanout.Load(); f != nil {
		f.enqueueDurable(job)
		return
	}
	job()
}

func (p *HubMQTTPublisher) addUnsub(u func()) {
	p.mu.Lock()
	p.unsubs = append(p.unsubs, u)
	p.mu.Unlock()
}

// wireOneCentral attaches subscriptions for all hub entities belonging
// to c and queues the initial-state publish. Nothing here touches the broker
// directly; every payload goes through [HubMQTTPublisher.publish].
func (p *HubMQTTPublisher) wireOneCentral(ctx context.Context, u *central.Unit) { //nolint:funlen // composition/wiring: long sequential setup
	hubModel := u.HubModel
	centralName := u.Name()
	w := p.wiring
	b := w.Bridge()
	if b == nil {
		// A nil bridge is a designed state, not an anomaly: disabling MQTT
		// at runtime keeps the Wiring alive and points its bridge nowhere
		// (see the supervisor's config-swap path), so every Wiring method
		// treats a publish as a no-op. This wiring pass reaches through to
		// the bridge for the discovery builder, so it has to make the same
		// check — without it the ready-driven re-Start dereferenced nil and
		// took the hub-discovery goroutine down with it.
		//
		// Returning is complete, not a partial repair: the supervisor calls
		// Start again on the next broker connect, and Start re-wires every
		// central from scratch.
		return
	}
	// Use the BRIDGE's discovery builder so the per-central HubInfo the
	// daemon registers via [mqtt.Bridge.SetHubInfoFor] — most importantly
	// the CCU serial that disambiguates hub unique_ids across centrals —
	// is visible to every hub discovery payload built here. A fresh
	// builder would never see the serials: every central's hub entities
	// would collide on identical unique_ids (`loom__alarm_messages`) and
	// HA would silently drop all but one CCU's hub plane. The hub
	// builders skip publishing entirely while the serial is unknown; the
	// daemon re-runs [HubMQTTPublisher.Start] after stamping HubInfo.
	disco := b.DefaultBuilder()
	if disco == nil {
		// Bridge runs a custom (non-default) builder — typically tests.
		// Fall back to a local instance; hub discovery then publishes
		// only once a serial is stamped onto it.
		disco = mqtt.NewDefaultDiscoveryBuilder(b.Topics(), centralName)
	}

	// Stamp this central's CCU metadata (serial, model, version, URL) onto the
	// discovery builder from the registry's SystemInformation — the single
	// source of truth — before building any discovery. The serial gates the
	// whole hub-discovery plane (hubSerial); it is resolved during the async,
	// readiness-gated bring-up, so a builder stamped by the composition root
	// eagerly (before bring-up finished) still carries an empty serial and skips
	// every hub payload while raw state keeps flowing. Reading it here means
	// each (re-)wire — including the ready-driven re-Start — publishes with the
	// central's actual serial. Only stamp once the serial has resolved so we
	// never clobber a serial another path already stamped with an empty one.
	//
	// The stamp is queued rather than applied here so it is ordered
	// BEFORE the discovery builds that read it back: every build below
	// runs on the worker, so a stamp applied inline could land after a
	// payload that already read the empty serial. (The builder's map is
	// itself synchronised — other goroutines stamp it too — so this
	// queueing is about ordering, not about data-race safety.)
	hi := hubInfoFromUnit(u)
	if hi.Serial != "" {
		p.publish(func() { disco.SetHubInfoFor(centralName, hi) })
	}

	// --- Per-CCU reachability gate ---
	// Queued here, ahead of every discovery build below, because Home
	// Assistant holds an entity unavailable until EVERY topic in its
	// `availability` list has reported. Every CCU-scoped hub entity built
	// below now lists `<base>/<central>/hub/status`, so a config that
	// reached HA before the gate's first retained byte would grey the whole
	// hub plane out until the next reachability change — which on a healthy
	// CCU may never come. The worker is FIFO, so queueing the seed first is
	// what orders the byte before the configs that name it.
	//
	// Gated on the SERIAL, like every discovery build below it, and for a
	// reason the gate's own fold depends on: an unobserved connectivity
	// tracker folds to REACHABLE, which is only defensible once the daemon
	// has demonstrated it can talk to this CCU. Before the serial resolves
	// it has demonstrated nothing, so an ungated seed put a retained
	// `online` on the broker for a CCU that is merely CONFIGURED — one that
	// may have been unreachable since boot. There is nothing to gate at that
	// point either: no hub entity of this central exists until the serial
	// stamps its unique ids, and the daemon re-runs Start once it does.
	p.queueCCUReachabilitySeed(ctx, b, centralName, hi.Serial, hubModel)
	// The gate's SECOND input. The seed above and every re-fold below read
	// the interface states, which are not the signal the entities behind this
	// gate depend on: sysvars, programs, the system scores and the message
	// aggregates all come from ReGa. A ReGaHss that dies or hangs while
	// `rfd`/`HMIPServer` keep serving leaves every interface reachable and
	// every one of those values frozen. The poller is what gives that case an
	// edge to publish; it is gated on the serial for the same reason the seed
	// is, and it feeds the SAME debounce rather than one of its own.
	p.startRegaLivenessPoll(ctx, b, centralName, hi.Serial, hubModel)

	// --- Programs ---
	// Subscribe to PutProgram FIRST so programs registered between the
	// snapshot read and the observer attach are not lost. Observer-fires
	// for programs the snapshot below also reads are deduped by ID —
	// the wiring is idempotent on the program object (OnUpdate adds a
	// new callback slot; double-subscribe leaks a slot but does not
	// double-publish state). The publisher is started BEFORE
	// WireCentrals, so when the first ReGa refresh lands later the
	// observer is the only path to discovery.
	p.addUnsub(hubModel.OnProgramRegistered(func(prog *hub.Program) {
		p.wireOneProgram(ctx, centralName, prog, disco, b, w)
	}))
	for _, prog := range hubModel.Programs() {
		p.wireOneProgram(ctx, centralName, prog, disco, b, w)
	}

	// --- Sysvars ---
	p.addUnsub(hubModel.OnSysvarRegistered(func(sv *hub.Sysvar) {
		p.wireOneSysvar(ctx, centralName, sv, disco, b, w)
	}))
	for _, sv := range hubModel.Sysvars() {
		p.wireOneSysvar(ctx, centralName, sv, disco, b, w)
	}

	// --- Device-link changes (sysvar/program → device) ---
	// The southbound assignHubChannels pass runs after devices materialise and
	// after every hub refresh; when it changes a device link it publishes
	// HubChannelsAssignedEvent. Re-publish the affected discovery so linked
	// entities move onto the correct device card. Discovery only — state and
	// the OnUpdate subscriptions above are left intact.
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.HubChannelsAssignedEvent) {
		if e.CentralName != centralName {
			return
		}
		p.publish(func() {
			p.republishHubEntityDiscovery(ctx, centralName, hubModel, disco, b)
		})
	}))

	// --- AlarmMessages ---
	// PublishAlarmMessages is on the Bridge (the Wiring wrapper is not yet
	// generated); call through w.Bridge() so we keep the same error-
	// suppression contract as the other Wiring helpers.
	p.publish(func() {
		_ = b.PublishHubDiscovery(ctx, disco.BuildAlarmMessagesDiscovery(centralName))
	})
	publishAlarm := func(msgs []hub.AlarmMessage) {
		p.publish(func() {
			if err := b.PublishAlarmMessages(ctx, centralName, hubModel.Messages, msgs); err != nil {
				p.logger.Warn("mqtt.publish_alarm_messages",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		})
	}
	if hubModel.Messages.Observed() {
		publishAlarm(hubModel.Messages.List())
	}
	p.addUnsub(hubModel.Messages.OnUpdate(func(msgs []hub.AlarmMessage) {
		publishAlarm(msgs)
	}))

	// --- ServiceMessages ---
	p.publish(func() {
		_ = b.PublishHubDiscovery(ctx, disco.BuildServiceMessagesDiscovery(centralName))
	})
	publishSvc := func(msgs []hub.ServiceMessage) {
		p.publish(func() {
			if err := b.PublishServiceMessages(ctx, centralName, hubModel.ServiceMessages, msgs); err != nil {
				p.logger.Warn("mqtt.publish_service_messages",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		})
	}
	if hubModel.ServiceMessages.Observed() {
		publishSvc(hubModel.ServiceMessages.List())
	}
	p.addUnsub(hubModel.ServiceMessages.OnUpdate(func(msgs []hub.ServiceMessage) {
		publishSvc(msgs)
	}))

	// --- InstallMode (per interface, via EventBus) ---
	p.wireInstallMode(ctx, u, centralName, hubModel, disco, b)

	// --- Connectivity (via EventBus) ---
	// Per-interface reachability changes arrive as ConnectivityChangedEvent
	// from the reconciler and from the callback-driven push path. The
	// connectivity binary_sensor stays per-interface (reference parity);
	// connection-latency is aggregated central-wide and wired from the
	// Metrics block below.
	//
	// connectivityDiscovered is owned by the fan-out worker: both the seed
	// below and the event handler touch it from inside a queued job, so the
	// single worker is the only goroutine that reads or writes it. Publishing
	// inline from the event handler instead would put the map on the bus
	// dispatch goroutine and the seed on the Start goroutine — a data race the
	// serialised dispatch happens to hide today.
	connectivityDiscovered := make(map[string]bool)
	// Eagerly publish connectivity discovery for every registered
	// interface at wiring time. The reference stack creates a
	// connectivity binary_sensor per interface at setup; relying on the
	// first ConnectivityChangedEvent alone left these entities absent
	// until a reachability change happened to fire post-boot. The state
	// still rides the event path below; only the discovery is seeded here.
	// Queued before the subscription is attached, so FIFO order guarantees the
	// seed runs before any event-driven state publish.
	p.publish(func() {
		seedConnectivityDiscovery(ctx, u, centralName, disco, b, connectivityDiscovered)
	})
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.ConnectivityChangedEvent) {
		if e.CentralName != centralName {
			return
		}
		p.publish(func() {
			if !connectivityDiscovered[e.InterfaceID] {
				_ = b.PublishHubDiscovery(ctx, disco.BuildConnectivityDiscovery(centralName, e.InterfaceID))
				connectivityDiscovered[e.InterfaceID] = true
			}
			conn := connectivityTopicProvider(hubModel)
			if err := b.PublishConnectivity(ctx, centralName, conn, e.InterfaceID, e.Reachable); err != nil {
				p.logger.Warn("mqtt.publish_connectivity",
					slog.String("central", centralName),
					slog.String("interface", e.InterfaceID),
					slog.String("err", err.Error()))
			}
			// Re-fold every interface into the per-CCU gate. The event's own
			// flag is deliberately NOT the input: one interface going down
			// is not the CCU going away, and the fold has to see the others
			// to tell the two apart. The bridge debounces the result.
			p.publishCCUReachability(ctx, b, centralName, hubModel)
		})
	}))

	// --- Metrics (System Health, Connection Latency) ---
	// Discovery is published once at wiring time. State updates are
	// forwarded to the retained metric topics whenever the Metrics
	// aggregate observes a new sample.
	p.publish(func() {
		// Daemon status: the one entity that reports the daemon itself being
		// gone. Its state topic is the retained bridge status the broker
		// carries the last will on, so it needs no state publisher here —
		// AnnounceOnline/AnnounceOffline and the will already write it.
		_ = b.PublishHubDiscovery(ctx, disco.BuildDaemonStatusDiscovery(centralName))
		_ = b.PublishHubDiscovery(ctx, disco.BuildSystemHealthDiscovery(centralName))
		// Last-Event-Age: a central-wide liveness sensor (seconds since the
		// newest backend event). Reference parity (hub_last-event-age). The
		// discovery is published once at wiring time; state updates follow the
		// MetricLastEventAgeSecs aggregate.
		_ = b.PublishHubDiscovery(ctx, disco.BuildLastEventAgeDiscovery(centralName))
		// Connection-Latency: ONE central-wide sensor (reference parity —
		// hub_connection-latency) fed from the aggregated ping/pong metric,
		// not per-interface samples. Discovery once at wiring time; state
		// follows the MetricConnectionLatMs aggregate.
		_ = b.PublishHubDiscovery(ctx, disco.BuildConnectionLatencyDiscovery(centralName))
	})
	if hubModel.Metrics != nil {
		// Publish any already-observed system-health value immediately.
		if sample, ok := hubModel.Metrics.Value(hub.MetricSystemHealth); ok {
			p.publish(func() { _ = b.PublishHubSystemHealthScore(ctx, centralName, sample.Value) })
		}
		p.addUnsub(hubModel.Metrics.OnUpdate(hub.MetricSystemHealth, func(s hub.MetricSample) {
			p.publish(func() {
				if err := b.PublishHubSystemHealthScore(ctx, centralName, s.Value); err != nil {
					p.logger.Warn("mqtt.publish_hub_health_score",
						slog.String("central", centralName),
						slog.String("err", err.Error()))
				}
			})
		}))
		// Last-Event-Age state — same observe-then-subscribe pattern as
		// system-health.
		if sample, ok := hubModel.Metrics.Value(hub.MetricLastEventAgeSecs); ok {
			p.publish(func() { _ = b.PublishHubLastEventAge(ctx, centralName, sample.Value) })
		}
		p.addUnsub(hubModel.Metrics.OnUpdate(hub.MetricLastEventAgeSecs, func(s hub.MetricSample) {
			p.publish(func() {
				if err := b.PublishHubLastEventAge(ctx, centralName, s.Value); err != nil {
					p.logger.Warn("mqtt.publish_hub_last_event_age",
						slog.String("central", centralName),
						slog.String("err", err.Error()))
				}
			})
		}))
		// Connection-Latency state — same observe-then-subscribe pattern.
		// The aggregated ping/pong latency lives on the MetricConnectionLatMs
		// sample; the publisher pushes it to the single central-wide topic.
		if sample, ok := hubModel.Metrics.Value(hub.MetricConnectionLatMs); ok {
			p.publish(func() { _ = b.PublishHubConnectionLatency(ctx, centralName, sample.Value) })
		}
		p.addUnsub(hubModel.Metrics.OnUpdate(hub.MetricConnectionLatMs, func(s hub.MetricSample) {
			p.publish(func() {
				if err := b.PublishHubConnectionLatency(ctx, centralName, s.Value); err != nil {
					p.logger.Warn("mqtt.publish_connection_latency",
						slog.String("central", centralName),
						slog.String("err", err.Error()))
				}
			})
		}))
	}

	// --- Inbox ---
	p.publish(func() {
		_ = b.PublishHubDiscovery(ctx, disco.BuildInboxDiscovery(centralName))
	})
	publishInbox := func(devices []hub.InboxDevice) {
		p.publish(func() {
			if err := b.PublishInbox(ctx, centralName, hubModel.Inbox, devices); err != nil {
				p.logger.Warn("mqtt.publish_inbox",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		})
	}
	if hubModel.Inbox.Observed() {
		publishInbox(hubModel.Inbox.List())
	}
	p.addUnsub(hubModel.Inbox.OnUpdate(func(devices []hub.InboxDevice) {
		publishInbox(devices)
	}))

	// --- System Update ---
	p.publish(func() {
		_ = b.PublishHubDiscovery(ctx, disco.BuildHubUpdateDiscovery(centralName))
	})
	publishUpdate := func(info hub.UpdateInfo) {
		// Read the in-progress flag on the notifying goroutine, so the queued
		// payload is the one the event described rather than whatever the
		// aggregate holds when the worker gets round to it.
		inProgress := hubModel.Update.InProgress()
		p.publish(func() {
			if err := b.PublishHubUpdate(ctx, centralName, info.CurrentFirmware, info.AvailableFirmware, inProgress); err != nil {
				p.logger.Warn("mqtt.publish_hub_update",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		})
	}
	if info, ok := hubModel.Update.UpdateInfo(); ok {
		publishUpdate(info)
	}
	p.addUnsub(hubModel.Update.OnUpdate(func(info hub.UpdateInfo) {
		publishUpdate(info)
	}))

	p.declareHubPlane(u, b, centralName)
}

// declareHubPlane tells the retained-orphan sweep that this central's hub
// plane has spoken. The mark is queued like every other job, so the single
// worker runs it only after all of the pass's publishes have returned — at
// which point each of them is recorded on the bridge and the sweep can tell
// a leftover from a live entity.
//
// Gated on the same resolved serial that gates the payloads themselves:
// with no serial every hub builder returns an empty item, so the pass
// declared nothing, and claiming otherwise would let the sweep retract the
// previous boot's hub entities moments before the re-wire (which the daemon
// runs once the serial lands) re-announces them.
func (p *HubMQTTPublisher) declareHubPlane(u *central.Unit, b *mqtt.Bridge, centralName string) {
	if hi := hubInfoFromUnit(u); hi.Serial == "" {
		return
	}
	p.publish(func() { b.MarkHubPlaneDeclared(centralName) })
}

// hubInfoFromUnit projects a central's resolved CCU metadata onto the MQTT
// discovery HubInfo. It is read live from the registry unit so it reflects the
// serial the async readiness-gated bring-up resolved — not a point-in-time
// snapshot taken by the composition root before bring-up finished. The name
// falls back to the central name (SystemInfo carries no name of its own).
func hubInfoFromUnit(u *central.Unit) mqtt.HubInfo {
	si := u.SystemInformation()
	return mqtt.HubInfo{
		Name:    u.Name(),
		Model:   si.Model,
		Version: si.Version,
		Serial:  si.Serial,
		URL:     si.URL,
	}
}

// wireInstallMode seeds per-interface install-mode discovery (one
// remaining-seconds sensor and one activation button per interface) and
// subscribes to InstallModeChangedEvent so each interface's countdown
// rides its own retained topic. The reference stack renders these
// entities per interface (HmIP-RF, BidCos-RF) rather than as a single
// central-wide aggregate.
func (p *HubMQTTPublisher) wireInstallMode(
	ctx context.Context,
	u *central.Unit,
	centralName string,
	hubModel *hub.Hub,
	disco *mqtt.DefaultDiscoveryBuilder,
	b *mqtt.Bridge,
) {
	for _, dp := range hubModel.InstallModeDPs() {
		if dp == nil || dp.InterfaceID == "" {
			continue
		}
		iface := dp.InterfaceID
		_, remaining, observed := dp.InstallState()
		p.publish(func() {
			_ = b.PublishHubDiscovery(ctx, disco.BuildInstallModeSensorDiscovery(centralName, iface))
			_ = b.PublishHubDiscovery(ctx, disco.BuildInstallModeButtonDiscovery(centralName, iface))
			// Publish the current observed countdown immediately so the
			// retained sensor topic is seeded before the first event.
			if !observed {
				return
			}
			if err := b.PublishInstallMode(ctx, centralName, iface, int(remaining.Seconds())); err != nil {
				p.logger.Warn("mqtt.publish_install_mode",
					slog.String("central", centralName),
					slog.String("interface", iface),
					slog.String("err", err.Error()))
			}
		})
	}
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.InstallModeChangedEvent) {
		if e.CentralName != centralName || e.InterfaceID == "" {
			return
		}
		p.publish(func() {
			if err := b.PublishInstallMode(ctx, centralName, e.InterfaceID, e.RemainingS); err != nil {
				p.logger.Warn("mqtt.publish_install_mode",
					slog.String("central", centralName),
					slog.String("interface", e.InterfaceID),
					slog.String("err", err.Error()))
			}
		})
	}))
}

// seedConnectivityDiscovery publishes the connectivity binary_sensor
// discovery for every registered interface of the central. It runs as the
// first queued job of the wiring pass, on the fan-out worker — the
// `connectivityDiscovered` map it fills is shared with the live event
// subscription, which touches it from the same worker, so each interface is
// announced at most once without a lock. Reference
// parity: the connectivity binary_sensor exists per interface at setup,
// not only after the first reachability change. Connection-latency is
// aggregated central-wide and seeded from the Metrics block instead.
//
// The seed keys on the `<central>-<iface>` wire id, exactly as the state half
// does: ConnectivityChangedEvent.InterfaceID carries the wire id because
// stampWireInterfaceIDs (hub_wiring.go) stamps it there before the reconciler
// publishes — the same id GET /interfaces reports and the client looks each
// sensor's value up by. Seeding under the bare interface name instead declared
// a state topic nothing ever writes (a permanently unavailable entity per
// radio) while the first reachability change added a second, live pair under
// the wire id. Seeding under the wire id and recording it in
// connectivityDiscovered keeps the seed and the event path on one entity.
func seedConnectivityDiscovery(
	ctx context.Context,
	u *central.Unit,
	centralName string,
	disco *mqtt.DefaultDiscoveryBuilder,
	b *mqtt.Bridge,
	connectivityDiscovered map[string]bool,
) {
	if u == nil || u.Clients == nil {
		return
	}
	for _, entry := range u.Clients.List() {
		if entry == nil {
			continue
		}
		iface := entry.Interface
		if iface == "" {
			iface = BareInterfaceFromWireID(centralName, entry.InterfaceID)
		}
		if iface == "" {
			continue
		}
		// The wire id, built the same way stampWireInterfaceIDs builds the id it
		// stamps onto ConnectivityChangedEvent, so the seed's discovery topic
		// and unique_id match the state the event path later publishes.
		wireID := WireInterfaceID(centralName, iface)
		if !connectivityDiscovered[wireID] {
			_ = b.PublishHubDiscovery(ctx, disco.BuildConnectivityDiscovery(centralName, wireID))
			connectivityDiscovered[wireID] = true
		}
	}
}

// wireOneProgram queues discovery + the current state, subscribes
// to future executions, and is safe to call on the same program more
// than once (the OnUpdate slot leaks but does not double-publish —
// see comment in wireOneCentral on the snapshot+observer interleave).
// Internal CCU programs (Tmp_*) are skipped because they are not
// user-visible. Operates on (centralName, disco, b, w) captured from
// the parent so the per-entity wiring stays decoupled from the
// daemon-level wiring container.
func (p *HubMQTTPublisher) wireOneProgram(
	ctx context.Context,
	centralName string,
	prog *hub.Program,
	disco *mqtt.DefaultDiscoveryBuilder,
	b *mqtt.Bridge,
	w *mqtt.Wiring,
) {
	if prog == nil || prog.Internal() {
		return
	}
	active, _ := prog.Active()
	// The model declares which controls the program surfaces; the bridge
	// transcribes them (ADR 0011). Roles are resolved against the bridge's
	// own topic base, which is runtime context the model does not hold.
	roles := b.ProgramRoles(centralName, prog)
	// Discovery, state and availability travel as one queued job so the
	// entity's config always precedes its first state on the wire.
	p.publish(func() {
		for _, item := range disco.BuildProgramDiscoveryRoles(centralName, programSpecFor(prog), roles) {
			_ = b.PublishHubDiscovery(ctx, item)
		}
		w.PublishProgramState(ctx, centralName, prog, active)
		p.publishProgramExecuteAvailability(ctx, b, roles, prog)
	})
	p.addUnsub(prog.OnUpdate(func(e hub.ProgramEvent) {
		p.publish(func() {
			w.PublishProgramState(ctx, centralName, prog, e.Active)
			p.publishProgramExecuteAvailability(ctx, b, roles, prog)
		})
	}))
	// A program the operator deleted in the CCU WebUI is dropped from the
	// model by the next refresh, but its retained discovery config keeps the
	// entity alive in every consumer — frozen at its last state, and across
	// daemon restarts, because nothing ever clears a retained topic that the
	// model no longer knows about. Retract the configs this program declared
	// the moment the model drops it.
	p.addUnsub(prog.OnRemoved(func() {
		p.publish(func() {
			retractHubDiscoveryItems(ctx, b,
				disco.BuildProgramDiscoveryRoles(centralName, programSpecFor(prog), roles))
		})
	}))
}

// retractHubDiscoveryItems clears the retained HA-Discovery config of every
// item by re-publishing it with an empty payload, which is how Home Assistant
// (and every other consumer of the discovery plane) is told the entity is
// gone. Items the builder refused (`OK == false`) are skipped, exactly as on
// the declare side.
func retractHubDiscoveryItems(ctx context.Context, b *mqtt.Bridge, items []mqtt.DiscoveryItem) {
	for _, item := range items {
		if !item.OK {
			continue
		}
		item.Payload = nil
		_ = b.PublishHubDiscovery(ctx, item)
	}
}

// wireOneSysvar queues discovery + the current state if observed,
// and subscribes to future value updates. Same idempotency caveat as
// wireOneProgram.
func (p *HubMQTTPublisher) wireOneSysvar(
	ctx context.Context,
	centralName string,
	sv *hub.Sysvar,
	disco *mqtt.DefaultDiscoveryBuilder,
	b *mqtt.Bridge,
	w *mqtt.Wiring,
) {
	if sv == nil {
		return
	}
	val, observed := sv.Value()
	p.publish(func() {
		_ = b.PublishHubDiscovery(ctx, disco.BuildSysvarDiscovery(centralName, sysvarSpecFor(sv)))
		if observed {
			w.PublishSysvar(ctx, centralName, sv, sysvarStateForMQTT(sv, val.Unwrap()))
		}
	})
	unsubUpdate := sv.OnUpdate(func(_, next hmtypes.ParamValue) {
		p.publish(func() {
			w.PublishSysvar(ctx, centralName, sv, sysvarStateForMQTT(sv, next.Unwrap()))
		})
	})
	p.addUnsub(unsubUpdate)
	// A system variable the operator deleted in the CCU WebUI is dropped
	// from the model by the next refresh, but its retained discovery config
	// keeps the entity alive in every consumer — frozen at its last value,
	// and across daemon restarts, because nothing ever clears a retained
	// topic the model no longer knows about. Retract the config this sysvar
	// declared the moment the model drops it, exactly as wireOneProgram
	// does for programs. A CCU-side rename reaches this same hook (the model
	// retracts the old identity before re-announcing the new one via the
	// registration observer), so the state subscription is released here too
	// — otherwise the re-wire would leave the pre-rename OnUpdate slot live and
	// every value change would publish to both the old (retracted) and the new
	// state topic.
	p.addUnsub(sv.OnRemoved(func() {
		unsubUpdate()
		// Build the retract item synchronously, on the notifying goroutine, so
		// its topic is derived from the identity's CURRENT (pre-rename) name. A
		// rename renames the live object immediately after this hook returns;
		// deferring the build to the async worker would read the new name and
		// retract the wrong topic, leaving the old entity stranded.
		item := disco.BuildSysvarDiscovery(centralName, sysvarSpecFor(sv))
		p.publish(func() {
			retractHubDiscoveryItems(ctx, b, []mqtt.DiscoveryItem{item})
		})
	}))
}

// sysvarSpecFor projects a model sysvar onto the narrow discovery contract,
// including the current device link (DeviceAddress). Shared by wireOneSysvar
// and republishHubEntityDiscovery so both build an identical payload.
func sysvarSpecFor(sv *hub.Sysvar) mqtt.HubSysvarSpec {
	// One guarded snapshot of the mutable descriptor: the hub scan rewrites
	// these fields in place through Sysvar.ApplyMeta while this fan-out runs on
	// the bus-dispatch / model-mutation goroutines.
	m := sv.Meta()
	return mqtt.HubSysvarSpec{
		// The name is mutable — a CCU-side rename rewrites it under the data
		// point's own lock — so it is read through the accessor, never off
		// the field.
		Name: sv.LegacyName(),
		// The unique_id is keyed on this, not on the name — see
		// [mqtt.sysvarUniqueID].
		Vid:         m.Vid,
		Description: m.Description,
		Unit:        m.Unit,
		ValueList:   m.ValueList,
		ValueType:   m.ValueType,
		// Writer is swapped in place by the refresh; Writable takes the lock
		// that swap uses, so a torn interface-header read cannot mis-report it.
		Writable:       sv.Writable(),
		IsExtended:     m.IsExtended,
		EnabledDefault: sv.EnabledByDefault(),
		Min:            hub.SysvarBoundAsFloat(m.Min),
		Max:            hub.SysvarBoundAsFloat(m.Max),
		DeviceAddress:  sv.DeviceAddress(),
	}
}

// programSpecFor projects a model program onto the narrow discovery
// contract. Shared by wireOneProgram and republishHubEntityDiscovery so
// both build an identical payload.
func programSpecFor(prog *hub.Program) mqtt.HubProgramSpec {
	return mqtt.HubProgramSpec{
		ID: prog.ID,
		// Mutable under the data point's own lock (a CCU-side rename lands
		// through UpdateMetadata), so read it through the accessor.
		Name:           prog.LegacyName(),
		DeviceAddress:  prog.DeviceAddress(),
		EnabledDefault: prog.EnabledByDefault(),
	}
}

// republishHubEntityDiscovery re-publishes ONLY the discovery payload — not
// state, not subscriptions — for every program and sysvar of the central. It
// runs when assignHubChannels changes a device link (via
// [hmevent.HubChannelsAssignedEvent]): the entity's `device` block flips to
// the physical device (or back to the hub card), so HA moves it to the right
// device. State topics and the OnUpdate slots wired in wireOne* stay untouched,
// so re-running leaks nothing.
func (p *HubMQTTPublisher) republishHubEntityDiscovery(
	ctx context.Context,
	centralName string,
	hubModel *hub.Hub,
	disco *mqtt.DefaultDiscoveryBuilder,
	b *mqtt.Bridge,
) {
	for _, prog := range hubModel.Programs() {
		if prog == nil || prog.Internal() {
			continue
		}
		for _, item := range disco.BuildProgramDiscoveryRoles(
			centralName, programSpecFor(prog), b.ProgramRoles(centralName, prog),
		) {
			_ = b.PublishHubDiscovery(ctx, item)
		}
	}
	for _, sv := range hubModel.Sysvars() {
		if sv == nil {
			continue
		}
		_ = b.PublishHubDiscovery(ctx, disco.BuildSysvarDiscovery(centralName, sysvarSpecFor(sv)))
	}
}

// connectivityTopicProvider returns the connectivity aggregate the bridge
// should render topics from: the unit's own — the object the reconciler
// writes — rather than a shared throw-away one, so the topic provider is
// the state holder whenever there is one.
//
// MQTTTopicsForInterface reads no field of its receiver today, so a central
// whose aggregate is not wired yet is served by a stand-in of the same type
// and gets the same topic. That fallback is the only reason an unwired
// aggregate is not an error here.

// queueCCUReachabilitySeed queues the per-CCU gate's seeding write, unless
// this central's serial has not resolved yet.
//
// Its own function rather than a branch in [HubMQTTPublisher.wireOneCentral]
// because that one is already at the cognitive-complexity ceiling; the
// reasoning for the guard is on the call site.
func (p *HubMQTTPublisher) queueCCUReachabilitySeed(
	ctx context.Context, b *mqtt.Bridge, centralName, serial string, hubModel *hub.Hub,
) {
	if serial == "" {
		return
	}
	p.publish(func() { p.publishCCUReachability(ctx, b, centralName, hubModel) })
}

// startRegaLivenessPoll runs this central's `/ise/checkrega.cgi` probe on
// its own goroutine and re-folds the per-CCU gate after every result.
//
// Three things about its shape are deliberate.
//
// It polls on its OWN goroutine, not on the fan-out worker: a probe is
// blocking network I/O against a CCU that may be exactly the one that has
// stopped answering, and the worker is the single goroutine every hub-plane
// publish of every central queues behind.
//
// It re-folds and publishes after EVERY tick, not only on a state change.
// The gate already dedups a level the broker holds — re-observing it cancels
// a pending opposite write and does nothing else — so a steady CCU costs no
// broker traffic, while a level whose write FAILED (the gate rolls such a
// level back rather than remembering it) gets another chance on the next
// tick instead of waiting for the next edge, which on a healthy CCU may
// never come.
//
// It stops when the wiring generation does: the closer registered with
// [HubMQTTPublisher.addUnsub] cancels the probe's context — aborting a GET
// in flight — and waits for the goroutine to exit, so Stop leaves no poller
// behind and a re-Start never runs two against the same CCU. The same closer
// is indexed by central name so [HubMQTTPublisher.stopRegaLivenessPoll] can
// end ONE poller: a central removed at runtime never reaches Stop.
func (p *HubMQTTPublisher) startRegaLivenessPoll(
	ctx context.Context, b *mqtt.Bridge, centralName, serial string, hubModel *hub.Hub,
) {
	// Gated on the serial like the seed, and for the same reason: nothing of
	// this central's hub plane exists before it resolves, so there is no gate
	// to feed and no entity to grey out.
	if serial == "" {
		return
	}
	target := p.regaTargetFor(centralName)
	if target == nil {
		// Silence here is what hid the runtime-adopted CCU: no target, no
		// poller, no probe, and the gate quietly folding `online` for a CCU
		// whose ReGa may be dead. Say so once per wiring pass.
		p.logger.Warn("mqtt.rega_liveness_no_target",
			slog.String("central", centralName),
			slog.String("probe", checkRegaPath))
		return
	}
	// The latch survives the wiring generation with the tracker: a CCU that
	// has told the daemon this endpoint may not be asked is not asked again
	// on the next broker reconnect either.
	if p.rega.unsupported(centralName) {
		return
	}
	interval := target.interval
	if interval <= 0 {
		interval = regaLivenessInterval
	}
	pollCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	// Registered twice, on purpose, and idempotent so the second call is
	// free: unsubs is the whole wiring generation's teardown (Stop), and
	// regaPollers is the per-central one (RetractCentral), which is the only
	// teardown a central removed at runtime ever gets.
	//
	// Both registrations under ONE p.mu acquisition rather than through
	// addUnsub and a second lock: a Stop interleaving between two acquisitions
	// drains unsubs (firing this closer) and clears regaPollers, and the
	// second acquisition then re-populates the fresh map with a closer that
	// has already fired. Harmless — the closer is idempotent — but it leaves
	// the index describing a poller that is gone, which is precisely what the
	// index exists not to do.
	p.mu.Lock()
	p.unsubs = append(p.unsubs, stop)
	if p.regaPollers == nil {
		p.regaPollers = map[string]func(){}
	}
	p.regaPollers[centralName] = stop
	p.mu.Unlock()
	SafeGo("hub_mqtt.rega_liveness."+centralName, func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if !p.pollRegaLivenessOnce(pollCtx, b, centralName, hubModel, target) {
				return
			}
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

// stopRegaLivenessPoll ends one central's ReGa poller and waits for its
// goroutine, leaving the others running. A no-op for a central with no
// poller, and safe to call twice.
//
// It exists because [HubMQTTPublisher.Stop] is not reachable for a central
// removed at runtime: Stop is the whole wiring generation's teardown, and a
// removal tears down one central while the daemon keeps serving the rest.
// Without this the poller survived its own CCU — it kept GETting a
// decommissioned host every interval for the life of the process, and each
// tick re-folded the gate whose tracker entry the removal had just dropped,
// so [regaLivenessTracker.observe] took its seed branch and wrote a retained
// `online` to `<base>/<central>/hub/status` for a CCU that no longer exists.
// The orphan sweep is scoped to registered centrals, so nothing would ever
// reach that topic again.
func (p *HubMQTTPublisher) stopRegaLivenessPoll(centralName string) {
	p.mu.Lock()
	stop := p.regaPollers[centralName]
	delete(p.regaPollers, centralName)
	p.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// pollRegaLivenessOnce performs one probe, records it and queues the
// re-fold. It reports whether polling should continue.
//
// It stops for two reasons only. A cancelled context is the wiring
// generation ending. A CCU that has latched the probe UNSUPPORTED — it
// answered 401/403/404, so the endpoint is behind auth or absent on that
// firmware — is told once and never asked again: repeating a question the
// CCU has refused is load with no signal in it, and the tracker keeps that
// CCU at unknown so its gate behaves exactly as it did before.
func (p *HubMQTTPublisher) pollRegaLivenessOnce(
	ctx context.Context, b *mqtt.Bridge, centralName string, hubModel *hub.Hub, target *regaLivenessTarget,
) bool {
	if ctx.Err() != nil {
		return false
	}
	res := target.probe(ctx)
	if ctx.Err() != nil {
		// A probe aborted by teardown says nothing about the CCU; recording
		// it would count a shutdown as a failed probe.
		return false
	}
	before := p.rega.state(centralName)
	after := p.rega.observe(centralName, res)
	if after != before {
		p.logger.Info("mqtt.rega_liveness_changed",
			slog.String("central", centralName),
			slog.String("from", regaLivenessStateName(before)),
			slog.String("to", regaLivenessStateName(after)))
	}
	// Queue the re-fold BEFORE deciding whether to keep polling, and do it on
	// every outcome — the latch included.
	//
	// The latch is the one exit that CHANGES the fold on its way out:
	// [regaLivenessTracker.observe] has just moved this CCU back to unknown,
	// and unknown folds to reachable, but this tick is the last one, so there
	// is no later tick to write it. Returning before the publish left a CCU
	// whose previous verdict was `down` with a retained `offline` on
	// `<base>/<central>/hub/status` for the rest of the process — on hardware
	// that is healthy and answering. The field sequence is exactly the one
	// [TestRegaLivenessLatchesOffAnEndpointTheCCURefuses] models: ReGa hangs
	// (`down`), the CCU reboots into a firmware or reverse proxy that answers
	// 401/403/404 (`unsupported` → unknown), and under Home Assistant's
	// `availability_mode: "all"` every sysvar, program, system-health, message
	// and install-mode entity of that CCU stays permanently unavailable with
	// nothing on the wire naming the cause.
	p.publish(func() { p.publishCCUReachability(ctx, b, centralName, hubModel) })
	if p.rega.unsupported(centralName) {
		p.logger.Warn("mqtt.rega_liveness_unsupported",
			slog.String("central", centralName),
			slog.String("probe", checkRegaPath))
		return false
	}
	return true
}

// regaLivenessStateName renders a tracked state for the log line.
func regaLivenessStateName(s regaLivenessState) string {
	switch s {
	case regaLivenessServing:
		return "serving"
	case regaLivenessDown:
		return "down"
	case regaLivenessUnknown:
		return "unknown"
	}
	return "unknown"
}

// publishCCUReachability folds this CCU's interface states into the per-CCU
// availability gate and hands the result to the bridge, which debounces it.
//
// Both callers go through here — the seed queued ahead of the hub discovery
// builds, and the re-fold on every ConnectivityChangedEvent — so the byte
// that seeds the topic and the byte that updates it can never be computed by
// two different folds.
func (p *HubMQTTPublisher) publishCCUReachability(
	ctx context.Context, b *mqtt.Bridge, centralName string, hubModel *hub.Hub,
) {
	if err := b.PublishHubReachability(ctx, centralName, ccuReachable(hubModel, p.rega.state(centralName))); err != nil {
		p.logger.Warn("mqtt.publish_hub_status",
			slog.String("central", centralName),
			slog.String("err", err.Error()))
	}
}

// ccuReachable is the fold: is this CCU still serving the values behind its
// hub plane.
//
// It is a CONJUNCTION of two signals that answer different halves of that
// question:
//
//	an interface answers   AND   ReGa answers
//
// The first half is the DISJUNCTION over the tracked interfaces — reachable
// when at least one of them is — and the choice is the substantive one, so
// it is stated here rather than left to the reader of
// [hub.Connectivity.AnyReachable].
//
// A CCU that goes away takes every one of its interface processes with it:
// the daemon's XML-RPC calls to all of them time out together and every
// interface flips unreachable in the same sweep, so the disjunction reports
// the CCU gone exactly when it is. The conjunction would report it gone
// much sooner and wrongly — one interface down is a per-interface fault,
// and the common shapes of it (a crashed CUxD, an unplugged HmIP wired
// gateway, a BidCoS radio module that the CCU itself restarts) leave the
// ReGa logic layer answering normally. Sysvars, programs, the system scores
// and the message aggregates are ReGa-scoped, not interface-scoped: greying
// them out because one radio is down would hide a working CCU behind an
// unrelated fault. The per-interface fault has its own entity — the
// connectivity binary_sensor — which is where that signal belongs and is
// read.
//
// # Why the interface half is not enough on its own
//
// The interface inputs and the gated entities are not the same signal: the
// entities are ReGa-scoped. On interface reachability alone the gate answers
// "online" in two cases where the values behind it are stale, and the ReGa
// half exists for exactly those two:
//
//   - ReGaHss dies or hangs while `rfd`/`HMIPServer` keep serving. The
//     XML-RPC clients stay connected, the central never goes FAILED,
//     [coordinators.Reconciler] never emits the not-ready sweep, and every
//     interface stays reachable — while every sysvar, program, system score
//     and message aggregate keeps showing its last ReGa value. The CCU
//     ANSWERS `/ise/checkrega.cgi` with something other than "OK" in this
//     state, which is a definite negative and is acted on at once.
//   - The connectivity probe ERRORS. [JSONRPCConnectivityProbe] documents
//     its own limit — `Interface.listInterfaces` measures MEMBERSHIP, not
//     liveness — and the reconciler's error path changes no tracker entry,
//     so a probe that cannot reach the CCU at all leaves the interface half
//     saying `online`. This half needs no firmware assumption to bite, and
//     it is covered without one: a CCU that has stopped answering the
//     daemon has stopped answering its own web server too, so the ReGa probe
//     stops completing, and [regaLivenessFailureThreshold] consecutive
//     silences fold to down.
//
// What the interface half DOES cover on its own is the total outage: a CCU
// that is gone takes the XML-RPC clients down with it and the not-ready
// sweep flips every interface false. The conjunction keeps that.
//
// # The three cases the conjunction has to answer explicitly
//
// NEVER RUN. A CCU with no probe result — none has happened yet, or no
// target is configured for it, or the CCU answered that the endpoint may not
// be asked — is [regaLivenessUnknown], and unknown folds to REACHABLE. It is
// the same absence-of-evidence rule the unobserved interface tracker gets
// (below) and for a stronger reason: an unknown that folded to `offline`
// would grey out every hub entity of every CCU whose firmware does not serve
// this CGI, on a signal that never arrived.
//
// ERROR AS OPPOSED TO A NEGATIVE ANSWER. They are different evidence and are
// treated differently: an answer that is not "OK" is ReGa saying it is not
// serving and flips the state on the spot, while a probe that does not
// complete holds the previous conclusion until
// [regaLivenessFailureThreshold] consecutive failures have accumulated. A
// single transient therefore changes nothing — which matters because the
// transients that hit one CCU (a saturated network, a DNS blip on the
// daemon's side) tend to hit the whole fleet at once, and a fleet-wide flap
// is the one failure mode a gate like this must not introduce.
//
// DISAGREEMENT. The two halves are conjoined, so either one saying "down"
// makes the CCU unreachable, and neither can veto the other. Interfaces up
// with ReGa down is the defect this half was added for: the values are
// stale, and the gate must say so. Every interface down with ReGa still
// answering is a CCU that has lost its whole radio layer, which the gate has
// reported as unreachable since it existed; ReGa answering does not make its
// device-derived values current.
//
// An unobserved interface tracker folds to REACHABLE, not to unreachable. The
// seed in wireOneCentral is gated on the CCU's serial having been read off it, so
// "no interface state yet" at that point means the daemon has just
// demonstrated it can talk to the CCU and the tracker has not caught up —
// absence of evidence, not evidence of absence. Folding it the other way
// would publish a retained `offline` and grey out every hub entity of a
// healthy CCU on every daemon start, until the first reachability change
// happened to arrive.
//
// The debounce is NOT here and there is not a second one for the new half:
// both signals are folded to one level and handed to
// [mqtt.Bridge.PublishHubReachability], whose existing symmetric dwell
// decides what reaches the broker. A flap that ends where it started still
// puts nothing on the wire, whichever half flapped.
func ccuReachable(hubModel *hub.Hub, rega regaLivenessState) bool {
	conn := connectivityTopicProvider(hubModel)
	reachable, observed := conn.AnyReachable()
	interfacesUp := !observed || reachable
	return interfacesUp && rega != regaLivenessDown
}

func connectivityTopicProvider(hubModel *hub.Hub) *hub.Connectivity {
	if hubModel != nil {
		if conn := hubModel.ConnectivityDataPoints(); conn != nil {
			return conn
		}
	}
	return hub.NewConnectivity()
}

// sysvarStateForMQTT maps the CCU-side value into the payload HA
// expects on the state topic. For List sysvars the CCU reports the
// zero-based index into [Sysvar.ValueList]; the matching HA `select`
// (writable) or enum-sensor advertises the labels themselves, so we
// resolve the index to its label before publishing. Out-of-range
// indices fall back to the raw value so HA still surfaces something
// rather than dropping the update silently.
func sysvarStateForMQTT(sv *hub.Sysvar, raw any) any {
	if sv == nil {
		return raw
	}
	// Snapshot the value list under the lock: the hub scan replaces it in place
	// through Sysvar.ApplyMeta while this publish runs on the fan-out worker.
	valueList := sv.Meta().ValueList
	if len(valueList) == 0 {
		return raw
	}
	var idx int
	switch v := raw.(type) {
	case int:
		idx = v
	case int64:
		idx = int(v)
	case float64:
		idx = int(v)
	default:
		return raw
	}
	if idx < 0 || idx >= len(valueList) {
		return raw
	}
	return valueList[idx]
}

// publishProgramExecuteAvailability reports each declared role's usability
// from the model's own answer. The rule — a deactivated program refuses to
// run — lives in [hub.Program.State]; this only transcribes it, so the
// bridge stays free of domain knowledge (ADR 0011).
func (p *HubMQTTPublisher) publishProgramExecuteAvailability(
	ctx context.Context, b *mqtt.Bridge, roles []payload.MQTTRole, prog *hub.Program,
) {
	if len(roles) == 0 || prog == nil {
		return
	}
	state, _ := prog.State().(*payload.ProgramState)
	if state == nil {
		return
	}
	for i := range roles {
		if roles[i].Topics.Availability == "" {
			continue
		}
		_ = b.PublishRoleAvailability(ctx, &roles[i], state.ExecuteAvailable)
	}
}
