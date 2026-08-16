// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
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
}

// NewHubMQTTPublisher constructs the publisher. No subscriptions are
// attached until [Start] is called.
func NewHubMQTTPublisher(reg *central.Registry, w *mqtt.Wiring, logger *slog.Logger) *HubMQTTPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &HubMQTTPublisher{registry: reg, wiring: w, logger: logger}
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
	p.mu.Unlock()
	// Unsubscribe before stopping the worker so no source can enqueue onto a
	// queue nobody drains any more.
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
	if f := p.fanout.Swap(nil); f != nil {
		f.stop()
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
	if hi := hubInfoFromUnit(u); hi.Serial != "" {
		p.publish(func() { disco.SetHubInfoFor(centralName, hi) })
	}

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
			if err := b.PublishConnectivity(ctx, centralName, hubConnectivityTopics, e.InterfaceID, e.Reachable); err != nil {
				p.logger.Warn("mqtt.publish_connectivity",
					slog.String("central", centralName),
					slog.String("interface", e.InterfaceID),
					slog.String("err", err.Error()))
			}
		})
	}))

	// --- Metrics (System Health, Connection Latency) ---
	// Discovery is published once at wiring time. State updates are
	// forwarded to the retained metric topics whenever the Metrics
	// aggregate observes a new sample.
	p.publish(func() {
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
// observeProbeLatency (hub_wiring.go) stamps it there before the reconciler
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
		// The wire id, built the same way observeProbeLatency builds the id it
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
		Name:        sv.LegacyName(),
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

// hubConnectivityTopics is a shared zero-value Connectivity used
// purely as a topic provider — its MQTTTopicsForInterface method
// is stateless and yields the canonical
// `<base>/<central>/hub/connectivity/<iface>`. The reconciler owns
// the real Connectivity state aggregate; here we only need the topic
// shape, which lives on the model type.
var hubConnectivityTopics = hub.NewConnectivity()

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
