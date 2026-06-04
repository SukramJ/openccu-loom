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
	disco := mqtt.NewDefaultDiscoveryBuilder(b.Topics(), centralName)

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

	// --- InstallMode (via EventBus) ---
	// InstallMode state arrives as domain events; the topic-owning
	// model object is any InstallMode instance on the hub (all of them
	// resolve to the same canonical central-wide topic).
	_ = b.PublishHubDiscovery(ctx, disco.BuildInstallModeDiscovery(centralName))
	installModeTopic := pickInstallModeTopicSource(hubModel)
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.InstallModeChangedEvent) {
		if e.CentralName != centralName {
			return
		}
		if err := b.PublishInstallMode(ctx, centralName, installModeTopic, e.RemainingS); err != nil {
			p.logger.Warn("mqtt.publish_install_mode",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
	}))

	// --- Connectivity (via EventBus) ---
	// Per-interface reachability changes arrive as ConnectivityChangedEvent
	// from the reconciler and from the callback-driven push path.
	connectivityDiscovered := make(map[string]bool)
	latencyDiscovered := make(map[string]bool)
	p.addUnsub(events.Subscribe(u.EventBus, func(e hmevent.ConnectivityChangedEvent) {
		if e.CentralName != centralName {
			return
		}
		if !connectivityDiscovered[e.InterfaceID] {
			_ = b.PublishHubDiscovery(ctx, disco.BuildConnectivityDiscovery(centralName, e.InterfaceID))
			connectivityDiscovered[e.InterfaceID] = true
		}
		if !latencyDiscovered[e.InterfaceID] {
			_ = b.PublishHubDiscovery(ctx, disco.BuildConnectionLatencyDiscovery(centralName, e.InterfaceID))
			latencyDiscovered[e.InterfaceID] = true
		}
		if err := b.PublishConnectivity(ctx, centralName, hubConnectivityTopics, e.InterfaceID, e.Reachable); err != nil {
			p.logger.Warn("mqtt.publish_connectivity",
				slog.String("central", centralName),
				slog.String("interface", e.InterfaceID),
				slog.String("err", err.Error()))
		}
		if e.LatencyMs > 0 {
			if err := b.PublishHubConnectionLatency(ctx, centralName, e.InterfaceID, e.LatencyMs); err != nil {
				p.logger.Warn("mqtt.publish_connection_latency",
					slog.String("central", centralName),
					slog.String("interface", e.InterfaceID),
					slog.String("err", err.Error()))
			}
		}
	}))

	// --- Metrics (System Health, Connection Latency) ---
	// Discovery is published once at wiring time. State updates are
	// forwarded to the retained metric topics whenever the Metrics
	// aggregate observes a new sample.
	_ = b.PublishHubDiscovery(ctx, disco.BuildSystemHealthDiscovery(centralName))
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
		// Connection latency is per-interface; the reconciler tracks it
		// in ConnectivityChangedEvent.LatencyMs which is picked up by the
		// per-interface discovery block above. No additional subscription
		// needed here.
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
	_ = b.PublishHubDiscovery(ctx, disco.BuildProgramDiscovery(centralName, prog.ID, prog.Name))
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
	spec := mqtt.HubSysvarSpec{
		Name:        sv.Name,
		Description: sv.Description,
		Unit:        sv.Unit,
		ValueList:   sv.ValueList,
		ValueType:   sv.ValueType,
		Writable:    sv.Writer != nil,
		Min:         paramValueAsFloat(sv.Min),
		Max:         paramValueAsFloat(sv.Max),
	}
	_ = b.PublishHubDiscovery(ctx, disco.BuildSysvarDiscovery(centralName, spec))
	if val, observed := sv.Value(); observed {
		w.PublishSysvar(ctx, centralName, sv, sysvarStateForMQTT(sv, val.Unwrap()))
	}
	p.addUnsub(sv.OnUpdate(func(_, next hmtypes.ParamValue) {
		w.PublishSysvar(ctx, centralName, sv, sysvarStateForMQTT(sv, next.Unwrap()))
	}))
}

// pickInstallModeTopicSource returns an InstallMode instance that
// satisfies the bridge's MQTTAddressable contract. The InstallMode
// topic is central-weit (`<base>/<central>/hub/install_mode`), so
// every instance — registered or synthetic — resolves to the same
// topic. The fallback synthetic instance keeps the publish path
// working even when no per-interface InstallMode has been seen yet.
func pickInstallModeTopicSource(hubModel *hub.Hub) payload.MQTTAddressable {
	for _, m := range hubModel.InstallModeDPs() {
		return m
	}
	return hubInstallModeTopics
}

// hubInstallModeTopics is a synthetic InstallMode used purely as a
// topic provider. Its MQTTTopics method is stateless and yields the
// canonical central-weit `<base>/<central>/hub/install_mode`.
var hubInstallModeTopics = hub.NewInstallMode("", nil)

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
