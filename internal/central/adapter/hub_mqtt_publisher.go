// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
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
// Lifecycle: NewHubMQTTPublisher → Start → Stop. Start is idempotent:
// existing subscriptions are released before new ones are attached.
type HubMQTTPublisher struct {
	registry *central.Registry
	wiring   *mqtt.Wiring
	logger   *slog.Logger

	mu     sync.Mutex
	unsubs []func()
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
// fires one immediate publish per entity so retained MQTT topics carry
// the current observed state, not just future changes. Idempotent:
// existing subscriptions are released first.
func (p *HubMQTTPublisher) Start(ctx context.Context) {
	p.Stop()
	if p.registry == nil || p.wiring == nil {
		return
	}
	for _, u := range p.registry.List() {
		p.wireOneCentral(ctx, u)
	}
}

// Stop releases every subscription registered by Start. Safe to call
// before Start (no-op) or multiple times.
func (p *HubMQTTPublisher) Stop() {
	p.mu.Lock()
	unsubs := p.unsubs
	p.unsubs = nil
	p.mu.Unlock()
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
}

func (p *HubMQTTPublisher) addUnsub(u func()) {
	p.mu.Lock()
	p.unsubs = append(p.unsubs, u)
	p.mu.Unlock()
}

// wireOneCentral attaches subscriptions for all hub entities belonging
// to c and performs the initial-state publish.
func (p *HubMQTTPublisher) wireOneCentral(ctx context.Context, u *central.Unit) { //nolint:funlen // composition/wiring: long sequential setup
	hubModel := u.HubModel
	centralName := u.Name()
	w := p.wiring
	b := w.Bridge()
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
	if hi := hubInfoFromUnit(u); hi.Serial != "" {
		disco.SetHubInfoFor(centralName, hi)
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
		p.republishHubEntityDiscovery(ctx, centralName, hubModel, disco, b)
	}))

	// --- AlarmMessages ---
	// PublishAlarmMessages is on the Bridge (the Wiring wrapper is not yet
	// generated); call through w.Bridge() so we keep the same error-
	// suppression contract as the other Wiring helpers.
	_ = b.PublishHubDiscovery(ctx, disco.BuildAlarmMessagesDiscovery(centralName))
	publishAlarm := func(msgs []hub.AlarmMessage) {
		if err := b.PublishAlarmMessages(ctx, centralName, hubModel.Messages, msgs); err != nil {
			p.logger.Warn("mqtt.publish_alarm_messages",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
	}
	if hubModel.Messages.Observed() {
		publishAlarm(hubModel.Messages.List())
	}
	p.addUnsub(hubModel.Messages.OnUpdate(func(msgs []hub.AlarmMessage) {
		publishAlarm(msgs)
	}))

	// --- ServiceMessages ---
	_ = b.PublishHubDiscovery(ctx, disco.BuildServiceMessagesDiscovery(centralName))
	publishSvc := func(msgs []hub.ServiceMessage) {
		if err := b.PublishServiceMessages(ctx, centralName, hubModel.ServiceMessages, msgs); err != nil {
			p.logger.Warn("mqtt.publish_service_messages",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
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
	connectivityDiscovered := make(map[string]bool)
	// Eagerly publish connectivity discovery for every registered
	// interface at wiring time. The reference stack creates a
	// connectivity binary_sensor per interface at setup; relying on the
	// first ConnectivityChangedEvent alone left these entities absent
	// until a reachability change happened to fire post-boot. The state
	// still rides the event path below; only the discovery is seeded here.
	seedConnectivityDiscovery(ctx, u, centralName, disco, b, connectivityDiscovered)
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.ConnectivityChangedEvent) {
		if e.CentralName != centralName {
			return
		}
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
	}))

	// --- Metrics (System Health, Connection Latency) ---
	// Discovery is published once at wiring time. State updates are
	// forwarded to the retained metric topics whenever the Metrics
	// aggregate observes a new sample.
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
	if hubModel.Metrics != nil {
		// Publish any already-observed system-health value immediately.
		if sample, ok := hubModel.Metrics.Value(hub.MetricSystemHealth); ok {
			_ = b.PublishHubSystemHealthScore(ctx, centralName, sample.Value)
		}
		p.addUnsub(hubModel.Metrics.OnUpdate(hub.MetricSystemHealth, func(s hub.MetricSample) {
			if err := b.PublishHubSystemHealthScore(ctx, centralName, s.Value); err != nil {
				p.logger.Warn("mqtt.publish_hub_health_score",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		}))
		// Last-Event-Age state — same observe-then-subscribe pattern as
		// system-health.
		if sample, ok := hubModel.Metrics.Value(hub.MetricLastEventAgeSecs); ok {
			_ = b.PublishHubLastEventAge(ctx, centralName, sample.Value)
		}
		p.addUnsub(hubModel.Metrics.OnUpdate(hub.MetricLastEventAgeSecs, func(s hub.MetricSample) {
			if err := b.PublishHubLastEventAge(ctx, centralName, s.Value); err != nil {
				p.logger.Warn("mqtt.publish_hub_last_event_age",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		}))
		// Connection-Latency state — same observe-then-subscribe pattern.
		// The aggregated ping/pong latency lives on the MetricConnectionLatMs
		// sample; the publisher pushes it to the single central-wide topic.
		if sample, ok := hubModel.Metrics.Value(hub.MetricConnectionLatMs); ok {
			_ = b.PublishHubConnectionLatency(ctx, centralName, sample.Value)
		}
		p.addUnsub(hubModel.Metrics.OnUpdate(hub.MetricConnectionLatMs, func(s hub.MetricSample) {
			if err := b.PublishHubConnectionLatency(ctx, centralName, s.Value); err != nil {
				p.logger.Warn("mqtt.publish_connection_latency",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		}))
	}

	// --- Inbox ---
	_ = b.PublishHubDiscovery(ctx, disco.BuildInboxDiscovery(centralName))
	publishInbox := func(devices []hub.InboxDevice) {
		if err := b.PublishInbox(ctx, centralName, hubModel.Inbox, devices); err != nil {
			p.logger.Warn("mqtt.publish_inbox",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
	}
	if hubModel.Inbox.Observed() {
		publishInbox(hubModel.Inbox.List())
	}
	p.addUnsub(hubModel.Inbox.OnUpdate(func(devices []hub.InboxDevice) {
		publishInbox(devices)
	}))

	// --- System Update ---
	_ = b.PublishHubDiscovery(ctx, disco.BuildHubUpdateDiscovery(centralName))
	publishUpdate := func(info hub.UpdateInfo) {
		inProgress := hubModel.Update.InProgress()
		if err := b.PublishHubUpdate(ctx, centralName, info.CurrentFirmware, info.AvailableFirmware, inProgress); err != nil {
			p.logger.Warn("mqtt.publish_hub_update",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
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
		_ = b.PublishHubDiscovery(ctx, disco.BuildInstallModeSensorDiscovery(centralName, dp.InterfaceID))
		_ = b.PublishHubDiscovery(ctx, disco.BuildInstallModeButtonDiscovery(centralName, dp.InterfaceID))
		// Publish the current observed countdown immediately so the
		// retained sensor topic is seeded before the first event.
		if _, remaining, observed := dp.InstallState(); observed {
			if err := b.PublishInstallMode(ctx, centralName, dp.InterfaceID, int(remaining.Seconds())); err != nil {
				p.logger.Warn("mqtt.publish_install_mode",
					slog.String("central", centralName),
					slog.String("interface", dp.InterfaceID),
					slog.String("err", err.Error()))
			}
		}
	}
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.InstallModeChangedEvent) {
		if e.CentralName != centralName || e.InterfaceID == "" {
			return
		}
		if err := b.PublishInstallMode(ctx, centralName, e.InterfaceID, e.RemainingS); err != nil {
			p.logger.Warn("mqtt.publish_install_mode",
				slog.String("central", centralName),
				slog.String("interface", e.InterfaceID),
				slog.String("err", err.Error()))
		}
	}))
}

// seedConnectivityDiscovery publishes the connectivity binary_sensor
// discovery for every registered interface of the central at wiring
// time. The `connectivityDiscovered` map is shared with the live event
// subscription so each interface is announced at most once. Reference
// parity: the connectivity binary_sensor exists per interface at setup,
// not only after the first reachability change. Connection-latency is
// aggregated central-wide and seeded from the Metrics block instead.
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
		iface := entry.InterfaceID
		if iface == "" {
			continue
		}
		if !connectivityDiscovered[iface] {
			_ = b.PublishHubDiscovery(ctx, disco.BuildConnectivityDiscovery(centralName, iface))
			connectivityDiscovered[iface] = true
		}
	}
}

// wireOneProgram publishes discovery + the current state, subscribes
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
	if prog == nil || prog.IsInternal {
		return
	}
	active, _ := prog.Active()
	_ = b.PublishHubDiscovery(ctx, disco.BuildProgramDiscovery(centralName, prog.ID, prog.Name, prog.DeviceAddress()))
	w.PublishProgramState(ctx, centralName, prog, active)
	p.addUnsub(prog.OnUpdate(func(e hub.ProgramEvent) {
		w.PublishProgramState(ctx, centralName, prog, e.Active)
	}))
}

// wireOneSysvar publishes discovery + the current state if observed,
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
	_ = b.PublishHubDiscovery(ctx, disco.BuildSysvarDiscovery(centralName, sysvarSpecFor(sv)))
	if val, observed := sv.Value(); observed {
		w.PublishSysvar(ctx, centralName, sv, sysvarStateForMQTT(sv, val.Unwrap()))
	}
	p.addUnsub(sv.OnUpdate(func(_, next hmtypes.ParamValue) {
		w.PublishSysvar(ctx, centralName, sv, sysvarStateForMQTT(sv, next.Unwrap()))
	}))
}

// sysvarSpecFor projects a model sysvar onto the narrow discovery contract,
// including the current device link (DeviceAddress). Shared by wireOneSysvar
// and republishHubEntityDiscovery so both build an identical payload.
func sysvarSpecFor(sv *hub.Sysvar) mqtt.HubSysvarSpec {
	return mqtt.HubSysvarSpec{
		Name:          sv.Name,
		Description:   sv.Description,
		Unit:          sv.Unit,
		ValueList:     sv.ValueList,
		ValueType:     sv.ValueType,
		Writable:      sv.Writer != nil,
		IsExtended:    sv.IsExtended,
		Min:           paramValueAsFloat(sv.Min),
		Max:           paramValueAsFloat(sv.Max),
		DeviceAddress: sv.DeviceAddress(),
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
		if prog == nil || prog.IsInternal {
			continue
		}
		_ = b.PublishHubDiscovery(ctx, disco.BuildProgramDiscovery(centralName, prog.ID, prog.Name, prog.DeviceAddress()))
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

// paramValueAsFloat coerces a CCU [*hmtypes.ParamValue] bound (Min /
// Max on a Sysvar) to the float64 form HA's number discovery wants.
// Returns nil when the source is nil, absent, or carries a non-numeric
// kind (e.g. a List sysvar's spurious bounds).
func paramValueAsFloat(pv *hmtypes.ParamValue) *float64 {
	if pv == nil || pv.IsNone() {
		return nil
	}
	switch pv.Kind {
	case hmtypes.ValueKindFloat:
		v := pv.Float
		return &v
	case hmtypes.ValueKindInt:
		v := float64(pv.Int)
		return &v
	default:
		return nil
	}
}

// sysvarStateForMQTT maps the CCU-side value into the payload HA
// expects on the state topic. For List sysvars the CCU reports the
// zero-based index into [Sysvar.ValueList]; the matching HA `select`
// (writable) or enum-sensor advertises the labels themselves, so we
// resolve the index to its label before publishing. Out-of-range
// indices fall back to the raw value so HA still surfaces something
// rather than dropping the update silently.
func sysvarStateForMQTT(sv *hub.Sysvar, raw any) any {
	if sv == nil || len(sv.ValueList) == 0 {
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
	if idx < 0 || idx >= len(sv.ValueList) {
		return raw
	}
	return sv.ValueList[idx]
}
