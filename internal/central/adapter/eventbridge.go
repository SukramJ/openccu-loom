// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// EventBridge subscribes to the domain [events.Bus] of every
// registered central and fans changes out to:
//
// - the WebSocket [*ws.Hub] (if non-nil) — the REST event stream
// - the MQTT [*mqtt.Wiring] (if non-nil) — raw plane + HA discovery
//
// One EventBridge instance is long-lived: Start attaches subscriptions,
// Stop releases them. It is safe to call Start/Stop repeatedly.
//
// The optional [filter.VisibilitySet] (vis) gates MQTT publishes: only
// parameters in the visible-set are forwarded to the broker. The WS
// stream is intentionally left unfiltered — it is the operator-tooling
// channel and operators may want all events for diagnostics. Nil vis
// means "no filter — forward everything" (backward-compatible).
//
// See ADR 0007 for the rationale.
type EventBridge struct {
	registry *central.Registry
	wsHub    *ws.Hub
	mqtt     *mqtt.Wiring
	vis      filter.VisibilitySet
	labels   mqtt.ParameterLabeler

	// postSnapshotHook, when set, runs synchronously after a central's
	// snapshot pass published (see [SetPostCentralSnapshotHook]). Installed
	// once during daemon wiring, before Start — not guarded by a mutex.
	postSnapshotHook func(ctx context.Context, centralName string)

	// translations + locale localize the discovery entity names the bridge
	// authors itself (schedule-switch, combined-sensor and combined-timer
	// labels). Resolved via [EventBridge.tr]; the catalogues are auto-loaded in
	// [NewEventBridge] (immutable embedded data) and the locale is set by
	// [EventBridge.WithLocale]. Empty locale falls back to the catalogue default.
	translations *i18n.Catalogs
	locale       string

	// startMu guards unsubs and started so Start/Stop are safe to call
	// concurrently and Start is idempotent. It is only held around the
	// (bounded) subscription bookkeeping — never while invoking a handler or
	// a broker publish — so it cannot stall dispatch.
	startMu sync.Mutex
	started bool
	// stopping is set while detach() tears the bridge down and cleared by the
	// next Start. It orders [EventBridge.beginBackgroundLoad] against
	// goroutineWG.Wait(): both the flag write and every Add happen under
	// startMu, so an Add can never land on a counter Wait() is already parked
	// on. Separate from started because PublishInitialSnapshot is a supported
	// call on a bridge that was never started, and must still warm schedules
	// up there.
	stopping bool
	unsubs   []func()
	// attached holds the subscriptions of centrals adopted after Start,
	// keyed by central name so a removed central can be detached again.
	// Start-time centrals live in unsubs; both are released by detach.
	attached map[string][]func()
	// liveSubs holds the callbacks the snapshot passes wire onto model
	// objects (week profiles, schedules, firmware trackers, combined data
	// points), keyed by the model slot they belong to and carrying the
	// identity of the object each one sits on. See eventbridge_live_subs.go
	// for why identity — and not the address — decides whether a pass
	// subscribes. Guarded by startMu like the two slices above; released by
	// detach.
	liveSubs map[liveSubKey]liveSub

	// fanout decouples the live value-change MQTT publish work from the bus
	// dispatch goroutine (see mqtt_fanout.go). Created in Start, drained by a
	// single worker, torn down in Stop. Held behind an atomic pointer so the
	// hot dispatch path (dispatchLive) reads it lock-free. Nil before the
	// first Start — the live dispatch path falls back to a synchronous publish
	// in that case so unit tests that invoke handlers without Start keep
	// working.
	fanout atomic.Pointer[mqttFanout]

	// availabilityCache is keyed by `<central>|<iface>|<deviceAddr>` and
	// holds the last availability state we published for that device.
	// Drives idempotent publishing: a reachable device is published
	// "online" once at boot and must not re-trigger an "online" publish
	// per value-change event — only on a reachability transition
	// (UNREACH / STICKY_UNREACH flipping) does the topic change.
	availabilityCache sync.Map

	// goroutineWG tracks background SafeGo goroutines (warm-up schedule
	// loads) so Stop() can wait for them to exit rather than leaving
	// orphaned goroutines after the bridge tears down.
	goroutineWG sync.WaitGroup

	// lifetimeCtx / stopCancel bound the bridge's background goroutines.
	// Stop() calls stopCancel() then waits on goroutineWG so no goroutine
	// outlives the bridge. Goroutines use lifetimeCtx (not context.Background())
	// so a daemon shutdown aborts a long warm-up load promptly.
	lifetimeCtx context.Context //nolint:containedctx // stored for background goroutine lifetime, not for per-call use
	stopCancel  context.CancelFunc
}

// NewEventBridge constructs a bridge. vis may be nil (no MQTT filter).
func NewEventBridge(r *central.Registry, wsHub *ws.Hub, mq *mqtt.Wiring) *EventBridge {
	eb := &EventBridge{registry: r, wsHub: wsHub, mqtt: mq}
	// Pre-set a background lifetime context so callers that invoke
	// PublishInitialSnapshot without calling Start first do not hit a nil
	// lifetimeCtx. Start() replaces this with a cancellable child.
	eb.lifetimeCtx, eb.stopCancel = context.WithCancel(context.Background()) //nolint:contextcheck // pre-init; replaced by Start()
	if cat, err := i18n.NewCatalogs(); err == nil {
		eb.translations = cat
	}
	return eb
}

// WithLocale sets the language used for the discovery entity names the bridge
// synthesises itself. Returns the receiver for fluent wiring.
func (b *EventBridge) WithLocale(locale string) *EventBridge {
	b.locale = locale
	return b
}

// tr resolves an i18n catalogue key in the bridge's locale and substitutes any
// `{placeholder}` occurrences from the alternating placeholder,value pairs in
// subs. Falls back to the raw key when no catalogues are wired.
func (b *EventBridge) tr(key string, subs ...string) string {
	s := key
	if b.translations != nil {
		s = b.translations.T(b.locale, key)
	}
	for i := 0; i+1 < len(subs); i += 2 {
		s = strings.ReplaceAll(s, "{"+subs[i]+"}", subs[i+1])
	}
	return s
}

// WithVisibility wires a [filter.VisibilitySet] that gates MQTT publish.
// Returns the receiver for fluent wiring:
//
//	bridge := adapter.NewEventBridge(reg, hub, mq).WithVisibility(vis)
func (b *EventBridge) WithVisibility(vis filter.VisibilitySet) *EventBridge {
	b.vis = vis
	return b
}

// WithParameterLabels wires the locale-aware parameter labeler used
// to populate the MQTT discovery `name` field. Without it, HA shows
// raw uppercase parameter names ("RSSI_DEVICE") instead of human-
// readable labels ("Signalstärke Gerät" / "Signal Strength").
// A nil labeler keeps the bridge running with title-cased fallbacks
// only.
func (b *EventBridge) WithParameterLabels(l mqtt.ParameterLabeler) *EventBridge {
	b.labels = l
	return b
}

// Start attaches a subscription per central and brings up the MQTT fan-out
// worker. It is idempotent: a second Start first detaches the previous run's
// subscriptions and worker, so it never double-subscribes.
func (b *EventBridge) Start(ctx context.Context) {
	// Idempotent guard: tear down any previous run before re-attaching. detach
	// runs its blocking work (unsub barriers, worker drain) without startMu
	// held, so it composes with the locked section below.
	b.detach()

	b.startMu.Lock()
	defer b.startMu.Unlock()

	// Initialise a bridge-lifetime cancellable context. Background goroutines
	// spawned during PublishInitialSnapshot hold lifetimeCtx so Stop()
	// can cancel them early (via stopCancel) and then drain via goroutineWG.
	//nolint:contextcheck // background goroutines must survive Start's ctx; stopCancel is invoked by Stop()
	b.lifetimeCtx, b.stopCancel = context.WithCancel(ctx)
	// The previous detach() closed the background-load gate; a running bridge
	// re-opens it.
	b.stopping = false
	if b.registry == nil {
		return
	}

	// The MQTT fan-out worker decouples broker I/O from bus dispatch: the live
	// value-change handlers enqueue here instead of publishing inline, so a
	// slow / half-open broker never stalls the dispatch goroutine of any
	// central. See mqtt_fanout.go and dispatchLive.
	f := newMQTTFanout()
	//nolint:contextcheck // worker inherits the bridge-lifetime context, not Start's ctx; stop() cancels it
	f.start(b.lifetimeCtx)
	b.fanout.Store(f)
	b.started = true

	for _, u := range b.registry.List() {
		b.unsubs = append(b.unsubs, b.subscribeUnit(u)...)
	}
}

// beginBackgroundLoad registers one background warm-up goroutine with the
// bridge's wait group and reports whether the caller may start it.
//
// The Add must be ordered against teardown, not merely happen before it: a
// snapshot pass runs on the MQTT lifecycle's reconnect goroutine and on the
// fan-out worker, neither of which detach() stops before it calls
// goroutineWG.Wait(). An Add landing on a zero counter that Wait is parked on
// is "sync: WaitGroup misuse: Add called concurrently with Wait" — a panic
// that takes the daemon down during shutdown; in the interleavings the runtime
// does not catch, the goroutine simply outlives Stop and publishes into a
// torn-down stack. Taking startMu here puts the Add on the same lock as the
// stopping flag, so it either happens before detach closed the gate (and Wait
// waits for it) or not at all. The scheduler solves the identical problem the
// same way (internal/scheduler/scheduler.go).
//
// A caller that gets false must not spawn the goroutine — and must not call
// goroutineWG.Done() either.
func (b *EventBridge) beginBackgroundLoad() bool {
	b.startMu.Lock()
	defer b.startMu.Unlock()
	if b.stopping {
		return false
	}
	b.goroutineWG.Add(1)
	return true
}

// AttachCentral subscribes a central that joined the registry AFTER
// [EventBridge.Start] — a live-adopted CCU. Start snapshots the registry
// once, so without this call the new central's bus reaches no north-bound
// surface at all: neither the MQTT fan-out nor the WebSocket live plane
// sees a single value change until the daemon restarts.
//
// The subscriptions inherit the bridge's lifetime context, never the
// caller's: an adopt runs on an HTTP request context that is cancelled the
// moment the response is written.
//
// A bridge that was never started attaches nothing — Start covers the
// central when it runs. Attaching the same central twice replaces the
// previous subscription set rather than doubling it.
func (b *EventBridge) AttachCentral(u *central.Unit) {
	if b == nil || u == nil {
		return
	}
	b.DetachCentral(u.Name())

	b.startMu.Lock()
	defer b.startMu.Unlock()
	if !b.started {
		return
	}
	if b.attached == nil {
		b.attached = make(map[string][]func())
	}
	b.attached[u.Name()] = b.subscribeUnit(u)
}

// DetachCentral releases the subscriptions [EventBridge.AttachCentral]
// installed for one central, so a removed CCU stops reaching the
// north-bound planes. Unknown names are a no-op.
func (b *EventBridge) DetachCentral(centralName string) {
	if b == nil {
		return
	}
	b.startMu.Lock()
	unsubs := b.attached[centralName]
	delete(b.attached, centralName)
	b.startMu.Unlock()

	// Each unsub is a barrier that waits for an in-flight dispatch of that
	// handler, so it runs without startMu held (same reasoning as detach).
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
}

// subscribeUnit installs the per-central bus subscriptions and returns
// their unsubscribe funcs. Shared by [EventBridge.Start] (boot-time
// centrals) and [EventBridge.AttachCentral] (live-adopted ones) so an
// adopted central is wired exactly like a configured one.
//
// No handler takes a caller context: every publish a handler triggers runs on
// the fan-out worker under the worker's own context, so the bridge lifetime —
// not the goroutine that happened to wire the central — bounds the broker I/O.
func (b *EventBridge) subscribeUnit(u *central.Unit) []func() {
	bus := u.EventBus
	return []func(){
		events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
			b.onValueChanged(u.Name(), e)
		}),
		events.Subscribe(bus, func(e hmevent.CentralStateChangedEvent) {
			b.onCentralState(u.Name(), e)
		}),
		// Per-central southbound bring-up phase transitions so the UI can
		// show "still initializing" vs "offline" while a co-booting CCU
		// loads names then devices.
		events.Subscribe(bus, func(e hmevent.CentralReadinessChangedEvent) {
			b.onCentralReadiness(u.Name(), e)
		}),
		// Wire-DP source-token transitions (cache → live, live →
		// stale, stale → live) republish the same topic even though
		// the value did not change. Without this consumers that gate
		// on value diff (HA without `force_update`) miss freshness
		// flips. ADR 0019.
		events.Subscribe(bus, func(e hmevent.DataPointSourceChangedEvent) {
			b.onSourceChanged(u.Name(), e)
		}),
		// Prune the MQTT bridge's declared map when a device is removed so
		// the dedup gate does not suppress subsequent orphan-cleanup evictions
		// of the same topics, and so snapshot passes do not re-emit discovery
		// configs for a device that no longer exists in the model.
		events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) {
			b.enqueueDurable(func(jobCtx context.Context) { b.onDeviceRemoved(jobCtx, e) })
		}),
		// Per-central southbound-ready: the readiness-gated bring-up loads each
		// central's devices (with names) asynchronously, after this boot-time
		// PublishInitialSnapshot would have run. Publish that central's snapshot
		// when it signals ready so its devices reach the broker without waiting
		// for a restart. Idempotent (the bridge diff-gates on its declared map),
		// so it composes safely with the catch-up PublishInitialSnapshot call.
		events.Subscribe(bus, func(e hmevent.CentralSouthboundReadyEvent) {
			b.enqueueDurable(func(jobCtx context.Context) {
				b.PublishCentralSnapshot(jobCtx, e.CentralName)
			})
		}),
		// Hot-plug: a device materialised AFTER its central's bring-up
		// (newDevices callback) never re-enters a snapshot pass on its
		// own — publish its full per-device footprint when the model
		// announces it, so discovery + state reach the broker without a
		// daemon restart.
		events.Subscribe(bus, func(e hmevent.DeviceCreatedEvent) {
			b.enqueueDurable(func(jobCtx context.Context) { b.onDeviceCreated(jobCtx, u, e) })
		}),
		// A rename or room/function change updates the live model but
		// touches no wire value, so it rides no DataPointValueChangedEvent —
		// without this the HA-Discovery device name / suggested_area kept
		// whatever [publishDeviceSnapshot] last observed until a broker
		// reconnect or daemon restart re-walked the whole model.
		// The release is the first moment a fully-built device may be
		// published. Nothing on the wire changed and the creation event
		// fired long ago, so without this the device stays invisible to
		// Home Assistant until the next daemon restart.
		events.Subscribe(bus, func(e hmevent.DeviceReleasedEvent) {
			b.enqueueDurable(func(jobCtx context.Context) { b.onDeviceReleased(jobCtx, u, e) })
		}),
		events.Subscribe(bus, func(e hmevent.DeviceMetadataChangedEvent) {
			b.publishDeviceMetadataChangedWS(u.Name(), e)
			b.enqueueDurable(func(jobCtx context.Context) { b.onDeviceMetadataChanged(jobCtx, u, e) })
		}),
		// An availability flip that did NOT come from an inbound value —
		// the interface-wide force applied when its client leaves
		// CONNECTED — has no value-change event to ride, so the retained
		// per-device availability topic would keep the value it had when
		// the CCU went silent. Home Assistant reads that topic as one of
		// the entity's availability sources, so a device on a powered-off
		// CCU would stay `online` on the broker for as long as the daemon
		// runs. The shared transition gate in markAvailability makes this
		// a no-op for flips the value path already published, so the two
		// producers cannot double-publish or feed each other.
		events.Subscribe(bus, func(e hmevent.DeviceLifecycleEvent) {
			if e.Subtype != hmenum.DeviceLifecycleSubtypeAvailabilityChanged {
				return
			}
			centralName, iface, addr, online := e.CentralName, e.InterfaceID, e.Address, e.Available
			if centralName == "" {
				centralName = u.Name()
			}
			b.enqueueDurable(func(jobCtx context.Context) {
				b.markAvailability(jobCtx, centralName, iface, addr, online)
			})
		}),
	}
}

// enqueueDurable hands a whole-device / whole-central publish pass to the
// fan-out worker instead of running it on the bus dispatch goroutine. A
// snapshot walks every data point of a device (or every device of a central)
// and blocks in the broker for each one; run inline it froze dispatch for
// every central sharing the bus, which is the defect this indirection exists
// to remove.
//
// Durable, not evictable: these passes carry the discovery configs and
// retracted topics that nothing re-sends. A dropped snapshot leaves a device
// missing from Home Assistant, and a dropped retraction leaves a deleted
// device lingering as a live entity — both until the next daemon restart. Only
// live value-change state publishes, which the next sample overwrites, are
// droppable. See [fanoutJob].
//
// Sharing the queue with the live value plane is deliberate: one FIFO drained
// by one worker is what keeps a device's discovery ahead of its first state
// and its retraction behind the publishes it retracts.
//
// With no worker running (a unit test driving a handler without Start) the job
// runs inline under the bridge lifetime context, preserving the old behaviour.
func (b *EventBridge) enqueueDurable(job func(context.Context)) {
	f := b.fanout.Load()
	if f == nil {
		job(b.lifetimeCtx)
		return
	}
	f.enqueueDurable(func() { job(f.ctx) })
}

// onDeviceCreated publishes the full MQTT snapshot of one freshly
// created device. Boot-time creation events are skipped — the
// southbound-ready snapshot covers them and a device mid-ingest may not
// be fully hydrated yet. The event is at-least-once (the CCU
// re-announces its whole inventory on every reconnect), so a repeat for
// an already-published device only re-emits retained topics.
func (b *EventBridge) onDeviceCreated(ctx context.Context, u *central.Unit, e hmevent.DeviceCreatedEvent) {
	if b == nil || b.mqtt == nil || u == nil {
		return
	}
	if !u.IsSouthboundReady() {
		return
	}
	d, ok := u.ModelRegistry.Get(e.Address)
	if !ok || d == nil {
		return
	}
	b.publishDeviceSnapshot(ctx, u.Name(), d)
}

// onDeviceReleased publishes a device's full MQTT footprint the moment
// the operator finishes onboarding it. It is the same walk a snapshot
// pass does for one device — publishDeviceSnapshot is retained-topic
// idempotent — but it is the FIRST one this device gets, because every
// earlier attempt returned at the release gate.
func (b *EventBridge) onDeviceReleased(ctx context.Context, u *central.Unit, e hmevent.DeviceReleasedEvent) {
	if b == nil || b.mqtt == nil || u == nil {
		return
	}
	if !u.IsSouthboundReady() {
		return
	}
	d, ok := u.ModelRegistry.Get(e.Address)
	if !ok || d == nil {
		return
	}
	b.publishDeviceSnapshot(ctx, u.Name(), d)
}

// onDeviceMetadataChanged re-publishes one device's full MQTT snapshot
// after a rename or room/function change, so the HA-Discovery device
// name / suggested_area stay current instead of keeping the value
// observed at the device's last full snapshot. [publishDeviceSnapshot]
// is retained-topic idempotent, so this only changes the fields that
// actually moved.
func (b *EventBridge) onDeviceMetadataChanged(ctx context.Context, u *central.Unit, e hmevent.DeviceMetadataChangedEvent) {
	if b == nil || b.mqtt == nil || u == nil {
		return
	}
	if !u.IsSouthboundReady() {
		return
	}
	d, ok := u.ModelRegistry.Get(e.Address)
	if !ok || d == nil {
		return
	}
	// The per-DP HA-Discovery label prefers each data point's
	// construction-time cached NameData quadruple over recomputing it live
	// (see datapointNameDataOf in buildPublishEvent), so a rename that only
	// republished the snapshot would keep emitting the pre-rename name on
	// every entity. Re-stamp the cache for every data point of every
	// channel first — restampChannelDataPointNames is the same helper the
	// ingest pipeline uses right after a channel is hydrated.
	for _, ch := range d.Channels() {
		restampChannelDataPointNames(ch, ch.DataPoints())
		restampChannelDataPointNames(ch, ch.MasterDataPoints())
	}
	b.publishDeviceSnapshot(ctx, u.Name(), d)
}

// publishDeviceMetadataChangedWS broadcasts the WebSocket
// `device.metadata_changed` frame for a rename or room/function change,
// alongside the MQTT re-publish [EventBridge.onDeviceMetadataChanged]
// performs on the same event. Unlike the MQTT arm it does not depend on
// b.mqtt: the WS plane keeps no declared-topic bookkeeping a disabled
// bridge would leave inconsistent, so a client holding a device list learns
// about the rename regardless of whether MQTT is wired.
func (b *EventBridge) publishDeviceMetadataChangedWS(centralName string, e hmevent.DeviceMetadataChangedEvent) {
	if b == nil || b.wsHub == nil {
		return
	}
	b.wsHub.Publish(ws.Event{
		Topic: ws.DeviceLifecycleTopic(e.Address),
		Type:  string(hmevent.EventTypeDeviceMetadataChanged),
		When:  e.Timestamp(),
		Payload: ws.DeviceMetadataChangedPayload{
			Central:       centralName,
			InterfaceID:   e.InterfaceID,
			DeviceAddress: e.Address,
		},
	})
}

// detach releases every subscription, stops the MQTT fan-out worker and waits
// for background goroutines to exit. It is shared by Start (idempotent
// re-attach) and Stop. Safe to call when the bridge was never started.
//
// The teardown bookkeeping (snapshot + clear the subscription slice and worker
// handle) runs under startMu, but the blocking teardown steps run without it:
// each unsub is a barrier that waits for any in-flight dispatch of that
// handler, so holding startMu across it could stall a concurrent Start/Stop.
func (b *EventBridge) detach() {
	b.startMu.Lock()
	unsubs := b.unsubs
	b.unsubs = nil
	for name, subs := range b.attached {
		unsubs = append(unsubs, subs...)
		delete(b.attached, name)
	}
	// The model-object callbacks the snapshot passes wired go with them:
	// they publish into this bridge, so a torn-down bridge must not leave
	// them sitting on live week profiles and firmware trackers.
	for key, sub := range b.liveSubs {
		if sub.unsub != nil {
			unsubs = append(unsubs, sub.unsub)
		}
		delete(b.liveSubs, key)
	}
	stopCancel := b.stopCancel
	b.started = false
	// Close the background-load gate before releasing the lock: from here on
	// beginBackgroundLoad refuses to Add, so the Wait() at the end of this
	// function cannot race an Add issued by a snapshot pass running on the
	// MQTT lifecycle goroutine, which detach does not stop.
	b.stopping = true
	b.startMu.Unlock()

	fanout := b.fanout.Swap(nil)

	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
	if stopCancel != nil {
		stopCancel()
	}
	if fanout != nil {
		fanout.stop()
	}
	b.goroutineWG.Wait()
}

// onSourceChanged fans a lifecycle transition out through the same
// dispatch path as a regular value change — WS inline, MQTT via the
// fan-out worker. It synthesises a DataPointValueChangedEvent with
// OldValue == NoneValue so the dedup gate downstream treats it as a
// fresh emission. The value itself comes from the source-changed
// event's Value field, which the DP layer fills with its current
// RawValue at transition time. The MQTT wiring may be nil (MQTT
// disabled); dispatchLive gates only its MQTT arm on that, so the
// WS freshness signal must not be guarded here.
func (b *EventBridge) onSourceChanged(centralName string, e hmevent.DataPointSourceChangedEvent) {
	if b == nil {
		return
	}
	if e.Value == nil {
		return
	}
	newVal, err := hmtypes.NewParamValue(e.Value)
	if err != nil {
		return
	}
	// Preserve the source event's paramset key so the downstream
	// visibility gate and topic-bucket selection classify the refresh
	// like the original value change; empty (pre-field producers)
	// defaults to VALUES.
	psKey := e.ParamsetKey
	if psKey == "" {
		psKey = hmenum.ParamsetKeyValues
	}
	b.dispatchLive(centralName, ws.KindRefresh, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    e.InterfaceID,
			ChannelAddress: e.ChannelAddress,
			ParamsetKey:    psKey,
			Parameter:      e.Parameter,
		},
		OldValue: hmtypes.NoneValue(),
		NewValue: newVal,
	})
}

// onDeviceRemoved retracts the removed device's HA-Discovery configs AND
// raw-plane topics from the broker immediately (empty retained payload),
// so the device's entities disappear from Home Assistant and every other
// MQTT consumer at once instead of lingering as "unavailable" (HA) or
// permanently `available:true` with a stale last value (raw-plane / non-HA
// consumers) until the next boot's orphan-cleanup pass. Called from the
// DeviceRemovedEvent subscription wired in Start.
func (b *EventBridge) onDeviceRemoved(ctx context.Context, e hmevent.DeviceRemovedEvent) {
	if b == nil {
		return
	}
	// The device's week profiles, schedules and firmware tracker are gone
	// from the model, so no later snapshot pass can reach them to take
	// their callbacks over. Release them here or they stay installed for
	// the life of the daemon.
	b.releaseLiveSubsForDevice(e.CentralName, e.Address)
	// Drop the availability transition state together with the topic it
	// describes: RetractRawStateForDevice writes an empty retained payload
	// to the device-availability topic, so a device that comes back under
	// the same address must be able to publish `online` again. A stale
	// cached `true` would classify that as "no transition" and leave every
	// HA entity of the readopted device unavailable for the life of the
	// daemon.
	b.forgetAvailability(e.CentralName, e.InterfaceID, e.Address)
	if b.mqtt == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	// Both retractions are scoped to the removal's own central. Device
	// addresses are unique per CCU but repeat verbatim across CCUs — the
	// virtual remote (HmIP-RCV-1), the BidCoS pseudo devices and the
	// INT000* internal devices carry the identical address everywhere — so
	// an unscoped discovery sweep would clear the other centrals' live
	// entities along with this one's.
	bridge.RetractDiscoveryForCentralDevice(ctx, e.CentralName, e.Address)
	bridge.RetractRawStateForDevice(ctx, e.CentralName, e.InterfaceID, e.Address)
}

// PublishInitialSnapshot walks every registered central's device
// model and publishes the current observed VALUES-paramset value of
// every data point through the same fan-out path as a live
// CCU-driven change. Without this call the broker only receives
// retained topics for parameters that change after daemon start
// HA Discovery configs are never emitted for devices whose values
// happen to be stable, so HA's MQTT integration never picks them
// up.
//
// Intended call site: after the device pipeline (WireCentrals) has
// hydrated and seeded values via fetch_all_device_data. Calling it
// before hydration is a no-op (no DataPoints exist yet). Re-running
// after a reconnect is safe: PublishState is idempotent on the
// MQTT side (retained topic with the same payload is a no-op for
// most brokers).
//
// MASTER-paramset values are deliberately skipped: they are
// configuration parameters, not runtime state, and are surfaced
// through the Config UI / REST paramset endpoints instead of the
// MQTT broker.
func (b *EventBridge) PublishInitialSnapshot(ctx context.Context) {
	if b.registry == nil {
		return
	}
	// This runs on every broker (re)connect, and a broker without a
	// persistent retained store comes back empty. The availability
	// transition gate would otherwise suppress the republish of every
	// device-availability topic — the topic HA's `availability_mode: all`
	// requires — leaving every entity unavailable until the daemon
	// restarts. Clearing the gate makes the snapshot authoritative again;
	// the per-event dedupe resumes from the values this pass publishes.
	b.availabilityCache.Clear()
	for _, u := range b.registry.List() {
		b.publishCentralSnapshot(ctx, u)
	}
}

// SetPostCentralSnapshotHook installs fn, invoked synchronously after a
// central's device snapshot has actually been published — via the boot-time
// [PublishInitialSnapshot], the southbound-ready path, or a broker-reconnect
// reseed alike. A central the ready gate skipped does NOT fire the hook.
//
// The daemon uses it to defer the retained-orphan sweeps until the bridge's
// declared / rawTopics bookkeeping reflects the current model; running them
// earlier classifies every legitimate topic as an orphan.
func (b *EventBridge) SetPostCentralSnapshotHook(fn func(ctx context.Context, centralName string)) {
	b.postSnapshotHook = fn
}

// PublishCentralSnapshot publishes the device snapshot for a single central,
// resolved by name. The per-central southbound-ready subscription uses it so a
// readiness-gated central's devices reach the broker as soon as THAT central
// finishes its bring-up, rather than waiting on a global boot snapshot.
func (b *EventBridge) PublishCentralSnapshot(ctx context.Context, centralName string) {
	if b == nil || b.registry == nil {
		return
	}
	u, ok := b.registry.Get(centralName)
	if !ok || u == nil {
		return
	}
	b.publishCentralSnapshot(ctx, u)
}

// publishCentralSnapshot publishes every device's current snapshot for one
// central — the per-unit body shared by the full boot snapshot
// ([PublishInitialSnapshot]) and the per-central southbound-ready path.
func (b *EventBridge) publishCentralSnapshot(ctx context.Context, u *central.Unit) {
	// A central whose readiness-gated bring-up has not latched ready is
	// still hydrating: its devices already sit in the ModelRegistry with
	// seeded values, but the visibility passes at the end of finishIngest
	// have not run yet, so every suppressed parameter still reads
	// Visible() == true. Snapshotting that window published entire
	// MASTER paramsets (75-slot week programs, router tables) retained to
	// the broker. Skip the central; the CentralSouthboundReadyEvent
	// subscription publishes it the moment the bring-up completes — the
	// ready latch is set before that event fires.
	if !u.IsSouthboundReady() {
		return
	}
	centralName := u.Name()
	// Stamp the CCU serial onto the discovery builder before writing a
	// single payload. A few address classes repeat verbatim across CCUs —
	// the virtual-remote buses, INT000*, the hub pseudo-addresses — and
	// the serial is the only thing that separates their unique_ids. It
	// reaches the builder by a slower route than the devices do: the
	// composition root stamps it while SystemInformation is still empty,
	// and the authoritative stamp rides the hub publisher's debounced
	// ready-restart, which lands after this snapshot. Reading it live from
	// the registry here removes the ordering question instead of timing
	// around it.
	b.stampHubSerial(u)
	ctx, tracker := withSnapshotPublishTracking(ctx)
	for _, d := range u.ModelRegistry.List() {
		b.publishDeviceSnapshot(ctx, centralName, d)
	}
	// The snapshot wrote the whole model to MQTT's retained topics. WS
	// subscribers get one signal instead of one frame per data point:
	// their view is now behind, and reloading over REST is both cheaper
	// and more complete than replaying the walk into a live stream.
	if b.wsHub != nil {
		b.wsHub.SignalResync()
	}
	if tracker.failed.Load() {
		// Arming the sweeps now would hand them a declared-set that is
		// missing exactly the topics the broker rejected, and the sweep
		// deletes every retained config it cannot find there. The next
		// snapshot pass against a healthy broker arms them instead.
		slog.Default().Warn("mqtt.snapshot.incomplete",
			slog.String("central", centralName),
			slog.String("detail", "broker rejected at least one publish; retained-orphan sweeps not armed for this pass"))
		return
	}
	if b.postSnapshotHook != nil {
		b.postSnapshotHook(ctx, centralName)
	}
}

// snapshotPublishTracker records whether any broker publish of one snapshot
// pass was rejected. It exists because the pass is best-effort per topic:
// every publish helper below swallows its error so one unreachable topic
// cannot abort a whole model's snapshot.
//
// The aggregate outcome still matters. The bridge records a discovery topic
// as `declared` only after the broker accepted it, and the retained-orphan
// sweeps delete every retained config that is not in that set. A pass that
// lost publishes therefore presents an incomplete declared-set to the sweep,
// which then evicts exactly the entities that failed to publish — they
// disappear from every consumer until a later boot against a healthy broker.
type snapshotPublishTracker struct{ failed atomic.Bool }

// snapshotPublishTrackerKey is the private context key of the tracker.
type snapshotPublishTrackerKey struct{}

// withSnapshotPublishTracking scopes a fresh tracker to ctx. Only publishes
// made under the returned context are counted, so the live value-change path
// (which carries no tracker) is unaffected.
func withSnapshotPublishTracking(ctx context.Context) (context.Context, *snapshotPublishTracker) {
	t := &snapshotPublishTracker{}
	return context.WithValue(ctx, snapshotPublishTrackerKey{}, t), t
}

// notePublish records the outcome of one broker publish. A call outside a
// tracked snapshot pass, or one that succeeded, is a no-op.
func notePublish(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if t, ok := ctx.Value(snapshotPublishTrackerKey{}).(*snapshotPublishTracker); ok && t != nil {
		t.failed.Store(true)
	}
}

// publishEvent routes one per-data-point event through the MQTT wiring.
// Inside a tracked snapshot pass it publishes through the bridge directly so
// the rejected publish is recorded; the pass logs one summary line instead of
// the wiring's per-topic warning.
func (b *EventBridge) publishEvent(ctx context.Context, ev mqtt.Event) {
	t, tracked := ctx.Value(snapshotPublishTrackerKey{}).(*snapshotPublishTracker)
	if !tracked || t == nil {
		b.mqtt.Publish(ctx, ev)
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	if err := bridge.PublishState(ctx, ev); err != nil {
		t.failed.Store(true)
	}
}

// publishDeviceSnapshot publishes one device's full MQTT footprint —
// availability, info/diagnostics, HA discovery + state for every data
// point, week-profile/schedule/combined/event/custom-DP surfaces, and
// the firmware-update entity. The per-device body shared by the
// snapshot passes above and the hot-plug path ([onDeviceCreated]); all
// publishes are retained-topic idempotent, so repeating the pass for an
// already-published device is safe.
func (b *EventBridge) publishDeviceSnapshot(ctx context.Context, centralName string, d *device.Device) {
	ifaceID := d.InterfaceID
	// A device the onboarding wizard has not released yet is materialised
	// and configurable here, but must not reach Home Assistant: the whole
	// point of the release step is that the operator names and places it
	// BEFORE it shows up in the ecosystem. Publishing first and renaming
	// after would leave HA with the entity ids of the unnamed device.
	if u, ok := b.registry.Get(centralName); ok && u.Devices != nil &&
		!u.Devices.IsReleased(hmtypes.ParseWireInterfaceID(ifaceID), d.Address) {
		return
	}
	// Publish per-device availability FIRST. The HA Discovery
	// payload references the device-availability topic (with
	// `availability_mode: all`) — without an explicit publish
	// HA marks every entity as unavailable and the discovery
	// effectively does nothing.
	//
	// Availability tracks device REACHABILITY (UNREACH /
	// STICKY_UNREACH via Device.Available()), not "has a value
	// been observed yet". A reachable
	// device whose data points have not reported yet is `online`
	// with each entity showing an `unknown` value, which is the
	// Home-Assistant convention. The per-DP snapshot below
	// publishes an explicit `{available:true}` state for every
	// data point so the state topic is never empty (avoiding the
	// empty-template warnings that the previous observed-gating
	// design worked around by leaving entities unavailable).
	online := d.Available()
	b.markAvailability(ctx, centralName, ifaceID, d.Address, online)

	// ADR 0011 phase 1c — device info + diagnostics topics.
	// Both are retained one-shot snapshots; the info topic
	// re-publishes when the model gains channels or
	// firmware-tracker fields update.
	b.publishDeviceInfo(ctx, centralName, ifaceID, d)
	b.publishDeviceDiagnostics(ctx, centralName, ifaceID, d)

	for _, ch := range d.Channels() {
		_, channelNo := parseChannel(ch.Address)
		// VALUES paramset — runtime state.
		for _, dp := range ch.DataPoints() {
			b.registerAndLoadDP(ctx, centralName, ifaceID, d, ch, channelNo, dp, hmenum.ParamsetKeyValues)
		}
		// ADR 0011 phase 1c — also publish MASTER paramset.
		// MASTER values are seeded once via OnWireValue and
		// don't generate normal value-change bus events; the
		// initial-snapshot pass synthesises them so the
		// `channels/<ch>/master/<param>/state` topics actually
		// contain something. Subsequent MASTER edits flow
		// through the configuration coordinator's regular
		// bus events so the runtime case is covered.
		for _, dp := range ch.MasterDataPoints() {
			b.registerAndLoadDP(ctx, centralName, ifaceID, d, ch, channelNo, dp, hmenum.ParamsetKeyMaster)
		}
		// Calculated DPs are written by the calculator's own
		// OnWireValue calls; surface them initially via the
		// same synthesised-event path. publishSlotState routes
		// them to the calculated/ bucket via
		// isCalculatedParameter.
		//
		// Observed calc-DPs (DEW_POINT, ENTHALPY — always
		// computable from the channel's temperature/humidity)
		// take the happy path through onValueChangedKind. Calc
		// binary_sensors (SMOKE_ALARM, INTRUSION_ALARM,
		// WINDOW_OPEN) start unobserved — they only compute a
		// value once the underlying alarm fires — yet the
		// reference stack registers them as HA entities at setup
		// regardless. Mirror the unobserved-DP boot path so they
		// reach HA discovery with an `unknown` slot state instead
		// of silently never surfacing.
		for _, dp := range ch.CalculatedDataPoints() {
			pdp, ok := dp.(interface {
				RawValue() (any, bool)
				Parameter() hmenum.Parameter
			})
			if !ok {
				continue
			}
			raw, observed := pdp.RawValue()
			if !observed {
				b.registerAndLoadUnobservedCalculatedDP(ctx, centralName, ifaceID, d, ch, channelNo, string(pdp.Parameter()))
				continue
			}
			newVal, err := hmtypes.NewParamValue(raw)
			if err != nil {
				continue
			}
			b.publishSnapshotValue(ctx, centralName, hmevent.DataPointValueChangedEvent{
				Base: hmevent.NewBase(),
				Key: hmtypes.DataPointKey{
					InterfaceID:    ifaceID,
					ChannelAddress: ch.Address,
					ParamsetKey:    hmenum.ParamsetKeyValues,
					Parameter:      string(pdp.Parameter()),
				},
				OldValue: hmtypes.NoneValue(),
				NewValue: newVal,
			})
		}
		// Week-profile DP — publish HA-Discovery select entity and
		// initial state, then subscribe to live profile-pointer changes.
		b.publishWeekProfileSnapshot(ctx, centralName, ifaceID, d, ch)

		// Zeitplan sensor — device-level HA `sensor` carrying the
		// active-entry count + rich schedule attributes
		// (schedule_type, max_entries, available_target_channels,
		// schedule_enabled, schedule_data).
		b.publishScheduleEntitySnapshot(ctx, centralName, ifaceID, d, ch)

		// Combined DPs (Timer, HSColor, LevelCombined, …): publish
		// one HA-Discovery entity per attached combined DP for
		// visible CombinedTimerField surfaces. Currently only the
		// Timer surface is wired; HSColor / LevelCombined remain
		// attachable scaffolding.
		b.publishCombinedDPSnapshot(ctx, centralName, ifaceID, d, ch)

		// Press-event entity discovery — emit the HA `event`
		// payload for every channel that exposes PRESS_*
		// parameters even though no value-change event has
		// fired yet. Without this seeding HA never sees the
		// button entity until somebody actually presses the
		// button on a fresh broker, and many physical buttons
		// have no observed value persisted between presses.
		b.publishChannelEventDiscoverySnapshot(ctx, centralName, ifaceID, d, ch)

		// Custom-DP aggregate discovery — write-only custom-DPs
		// (HmIP-WRCD text-display) have no readable parameter, so
		// the register-and-load path never emits their aggregate
		// entity. Publish it directly here so they (and their
		// companion entities, e.g. the text-display `notify`
		// surface) reach HA from boot.
		b.publishCustomDPDiscoverySnapshot(ctx, centralName, ifaceID, d, ch)
	}
	// Device-level firmware-update entity: published once per
	// updatable device. The update entity is not a channel — it
	// maps to the device's Firmware tracker and lives under the
	// device address with no channel suffix. Wires a live
	// OnChange subscription so subsequent firmware-state
	// transitions (CCU push → FirmwareInfo.Set) automatically
	// re-publish the state topic.
	b.publishUpdateSnapshot(ctx, centralName, ifaceID, d)
}

// markAvailability publishes the per-device availability topic, but
// only when the desired state differs from the last published state,
// and reports whether that transition happened.
// Without this gate every value-change event would re-publish "online"
// (and any UNREACH parameter would re-publish whatever it just
// computed) — broker spam plus retained-topic churn. With it the topic
// flips exactly on transitions: boot → offline; first observed DP →
// online; UNREACH true → offline; UNREACH false → online.
//
// The gate runs before the MQTT wiring is consulted so it stays the single
// answer to "did this device's availability just change" for every plane,
// including deployments with no broker at all.
//
// Errors are swallowed at debug level — the broker not having the
// device-availability topic is a degraded-but-not-fatal state.
func (b *EventBridge) markAvailability(ctx context.Context, centralName, iface, deviceAddr string, online bool) bool {
	key := availabilityCacheKey(centralName, iface, deviceAddr)
	if prev, ok := b.availabilityCache.Load(key); ok {
		// availabilityCache only ever stores bool values written by this
		// method, so the assertion cannot fail.
		if prevBool, _ := prev.(bool); prevBool == online {
			return false
		}
	}
	b.availabilityCache.Store(key, online)
	if b.mqtt == nil {
		return true
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return true
	}
	notePublish(ctx, bridge.PublishAvailability(ctx, centralName, iface, deviceAddr, online))
	return true
}

// forgetAvailability drops one device's cached availability state so the
// next [markAvailability] call publishes unconditionally. Used whenever the
// retained topic itself is cleared — the cache must not outlive the topic
// it describes.
func (b *EventBridge) forgetAvailability(centralName, iface, deviceAddr string) {
	if b == nil {
		return
	}
	b.availabilityCache.Delete(availabilityCacheKey(centralName, iface, deviceAddr))
}

// availabilityCacheKey scopes the transition gate by central, interface and
// device address, so the same device address on two CCUs stays distinct.
func availabilityCacheKey(centralName, iface, deviceAddr string) string {
	return centralName + "|" + iface + "|" + deviceAddr
}

// isReachabilityParameter reports whether a parameter change implies
// the per-device availability has flipped, so we should re-publish the
// device-availability topic.
func isReachabilityParameter(p string) bool {
	switch p {
	case "UNREACH", "STICKY_UNREACH", "CONFIG_PENDING":
		return true
	}
	return false
}

// registerAndLoadDP composes a per-DP [mqtt.Event] and publishes it.
// Two outcomes:
//
//  1. The DP has an observed RawValue (persistent VALUES cache hit,
//     fetch_all_device_data hit, or a push event during ingest):
//     publish full state + discovery via onValueChanged.
//  2. The DP is still unobserved: emit an HA discovery payload and an
//     explicit `{available:true}` slot state carrying an `unknown`
//     value, so HA renders the entity as online-but-unknown until the
//     next CCU push delivers a real value.
//
// Availability tracks device REACHABILITY, not value observation:
// a reachable device whose DP has not reported
// yet is online with an `unknown` value. Publishing the available slot
// state (rather than evicting it to empty) is what keeps the entity
// from rendering as `unavailable` under the discovery payload's
// `availability_mode: all` + per-DP availability template.
//
// The function does NOT issue a getValue / LoadValue on the wire.
// Per-DP boot-time loads were observed to fire one radio call per
// unobserved DP (thousands across a non-trivial CCU) and drove the
// DutyCycle into the warning band on every restart. The reference
// design only loads Channel-0 RELEVANT_INIT_PARAMETERS + readable
// events via [seedRelevantInitParameters] / [seedReadableEvents].
func (b *EventBridge) registerAndLoadDP(
	ctx context.Context,
	centralName, ifaceID string,
	d *device.Device,
	ch *device.Channel,
	channelNo int,
	dp interface {
		RawValue() (any, bool)
		Parameter() hmenum.Parameter
	},
	paramsetKey hmenum.ParamsetKey,
) {
	parameter := string(dp.Parameter())
	dpk := hmtypes.DataPointKey{
		InterfaceID:    ifaceID,
		ChannelAddress: ch.Address,
		ParamsetKey:    paramsetKey,
		Parameter:      parameter,
	}

	// Boot-time radio budget: we publish what the model already has
	// (persistent VALUES cache + fetch_all_device_data + push events
	// observed during ingest). DPs that are still unobserved are
	// registered in HA discovery with an `unknown`-value slot state and
	// wait for the next CCU push. The previous "best-effort LoadValue per DP"
	// path fanned out one getValue radio call per unobserved DP and
	// drove the CCU DutyCycle into the warning band on every boot —
	// the reference design only loads Channel-0 RELEVANT_INIT_PARAMETERS
	// + readable events (see `seedRelevantInitParameters` /
	// `seedReadableEvents`).
	raw, observed := dp.RawValue()

	if observed {
		// Happy path — full state + discovery via onValueChanged.
		newVal, err := hmtypes.NewParamValue(raw)
		if err != nil {
			return
		}
		// Visibility gate, ahead of the fan-out rather than inside the
		// MQTT half of it, so both north-bound planes carry the same
		// set. MQTT consulted buildPublishEvent on its way through and
		// dropped what the gate refused; the WebSocket half consulted
		// nothing and broadcast it. A channel with a week program holds
		// hundreds of MASTER slots that MASTER's default-deny whitelist
		// refuses, and the boot snapshot pushed every one of them to
		// every subscriber — enough, on a large installation, to run a
		// session past its buffer before it had finished connecting.
		chAddr, _ := parseChannel(ch.Address)
		if _, _, ok, _ := b.buildPublishEvent(
			centralName, ifaceID, d.Address, chAddr, channelNo,
			d.Model, d.Name(), dpk, raw, paramsetKey,
		); !ok {
			return
		}
		b.publishSnapshotValue(ctx, centralName, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			Key:      dpk,
			OldValue: hmtypes.NoneValue(),
			NewValue: newVal,
		})
		return
	}

	// Unobserved path — register the entity in HA discovery and publish
	// an explicit `{available:true}` slot state with an `unknown` value
	// (NoneValue → JSON `null`, zero Base → no timestamps). This is what
	// keeps the entity online under `availability_mode: all`; a future
	// wire event replaces the `unknown` value with the real one.
	//
	// Visibility-gated by the same rule as the observed path
	// (buildPublishEvent): a suppressed DP publishes neither slot state
	// nor the /config companion — unless the channel hosts a custom DP
	// whose discovery payload references the slot topic. Without this
	// gate every ignored parameter (BOOTED, INSTALL_TEST, *_STATUS, …)
	// landed retained on the broker with a null value.
	chAddr, _ := parseChannel(ch.Address)
	if _, _, ok, _ := b.buildPublishEvent(
		centralName, ifaceID, d.Address, chAddr, channelNo,
		d.Model, d.Name(), dpk, nil, paramsetKey,
	); !ok {
		return
	}
	b.publishSlotState(ctx, centralName, ifaceID, d.Address, channelNo, hmevent.DataPointValueChangedEvent{
		Key:      dpk,
		OldValue: hmtypes.NoneValue(),
		NewValue: hmtypes.NoneValue(),
	}, ch)
	b.publishDiscoveryForUnobservedDP(ctx, centralName, ifaceID, d, ch, channelNo, parameter, paramsetKey)
}

// publishDiscoveryForUnobservedDP composes the per-DP [mqtt.Event]
// for a not-yet-observed DP and routes it through
// [Bridge.PublishDiscoveryOnly]. The Event carries `Value: nil`
// the discovery payload doesn't reference it, but the visibility
// gates and descriptor-metadata propagation in [buildPublishEvent]
// do, so we go through the same construction path as the observed
// case.
//
// Best-effort: a broker hiccup is swallowed silently (matches the
// rest of PublishInitialSnapshot's error semantics).
func (b *EventBridge) publishDiscoveryForUnobservedDP(
	ctx context.Context,
	centralName, ifaceID string,
	d *device.Device,
	ch *device.Channel,
	channelNo int,
	parameter string,
	paramsetKey hmenum.ParamsetKey,
) {
	if b.mqtt == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	model, name := d.Model, d.Name()
	channel, _ := parseChannel(ch.Address)
	key := hmtypes.DataPointKey{
		InterfaceID:    ifaceID,
		ChannelAddress: ch.Address,
		ParamsetKey:    paramsetKey,
		Parameter:      parameter,
	}
	ev, _, ok, discoveryEligible := b.buildPublishEvent(centralName, ifaceID, d.Address, channel, channelNo, model, name, key, nil, paramsetKey)
	if !ok || !discoveryEligible {
		// NoCreate-suppressed DPs (per-DP `Visible() == false`) are
		// referenced by Custom-DP discoveries but should not surface
		// as their own HA entity. Skip the discovery-only publish
		// for them — slot state is still published via the
		// register-and-load path's onValueChanged route.
		return
	}
	// When the channel hosts a custom DP, buildPublishEvent stamps
	// ev.Source so the runtime path emits the aggregate channel discovery
	// alongside the per-parameter one. The unobserved-DP boot path only
	// wants the per-parameter discovery; clear Source so the discovery
	// builder routes through the per-parameter classifier (otherwise the
	// siren aggregate would be republished and the standalone select /
	// number entity for whitelisted action DPs never lands).
	ev.Source = nil
	notePublish(ctx, bridge.PublishDiscoveryOnly(ctx, ev))
}

// registerAndLoadUnobservedCalculatedDP is the calculated-DP counterpart
// of [registerAndLoadDP]'s unobserved branch. Calc binary_sensors
// (SMOKE_ALARM, INTRUSION_ALARM, WINDOW_OPEN) carry no value until the
// underlying alarm fires, but the reference stack still registers them
// as HA entities at setup. This publishes an explicit `{available:true}`
// slot state with an `unknown` value on the calculated bucket plus the
// per-DP HA discovery payload, so the entity exists in HA from boot and
// a later calculator update replaces the `unknown` value with the real
// one.
func (b *EventBridge) registerAndLoadUnobservedCalculatedDP(
	ctx context.Context,
	centralName, ifaceID string,
	d *device.Device,
	ch *device.Channel,
	channelNo int,
	parameter string,
) {
	dpk := hmtypes.DataPointKey{
		InterfaceID:    ifaceID,
		ChannelAddress: ch.Address,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
	// publishSlotState routes to the calculated/ bucket via
	// isCalculatedParameter — NoneValue → JSON `null` value with no
	// timestamps keeps the entity online under availability_mode: all.
	b.publishSlotState(ctx, centralName, ifaceID, d.Address, channelNo, hmevent.DataPointValueChangedEvent{
		Key:      dpk,
		OldValue: hmtypes.NoneValue(),
		NewValue: hmtypes.NoneValue(),
	}, ch)
	b.publishDiscoveryForUnobservedDP(ctx, centralName, ifaceID, d, ch, channelNo, parameter, hmenum.ParamsetKeyValues)
}

// Stop releases every subscription, stops the MQTT fan-out worker, cancels
// background goroutines and waits for them to exit. Idempotent and safe to call
// before Start.
func (b *EventBridge) Stop() {
	b.detach()
}

// FanoutDropped reports how many live value-change MQTT publishes were dropped
// because the per-broker fan-out queue was full (drop-oldest backpressure).
// Zero until a broker is slow enough to overflow the queue.
func (b *EventBridge) FanoutDropped() uint64 {
	if f := b.fanout.Load(); f != nil {
		return f.droppedCount()
	}
	return 0
}

// FanoutQueueDepth reports how many live value-change publishes are currently
// queued for the fan-out worker. A steadily growing depth indicates a broker
// that cannot keep up.
func (b *EventBridge) FanoutQueueDepth() int {
	if f := b.fanout.Load(); f != nil {
		return f.queueDepth()
	}
	return 0
}

// Flush blocks until the MQTT fan-out worker has drained every publish enqueued
// before the call. It is a test barrier — the live path is intentionally
// asynchronous — and a no-op before Start.
func (b *EventBridge) Flush() {
	if f := b.fanout.Load(); f != nil {
		f.flush()
	}
}

func (b *EventBridge) onValueChanged(centralName string, e hmevent.DataPointValueChangedEvent) {
	b.dispatchLive(centralName, ws.KindChange, e)
}

// dispatchLive fans a live bus event (a real value change or a source-token
// refresh) out to both north-bound sinks. The WebSocket publish stays inline on
// the dispatch goroutine because the hub's per-client enqueue is already
// bounded and non-blocking (drop-on-full closes the slow client). The MQTT
// publish is handed to the per-broker fan-out worker so a slow / half-open
// broker never blocks bus dispatch — the finding this method exists to fix.
//
// When the bridge has no MQTT wiring there is nothing to fan out. When the
// fan-out worker is not running (a unit test that drives a handler without
// calling Start), the publish falls back to running inline so behaviour is
// preserved.
func (b *EventBridge) dispatchLive(centralName string, envKind hmenum.WSEnvelopeKind, e hmevent.DataPointValueChangedEvent) {
	b.publishValueChangedWS(centralName, envKind, e)
	if b.mqtt == nil {
		// No broker to publish to, but the availability transition this
		// value may carry still has to reach the bus consumers — the
		// WebSocket device-lifecycle plane and the Matter reachability
		// forward exist in a deployment without MQTT too.
		b.refreshDeviceAvailabilityFor(b.lifetimeCtx, centralName, e)
		return
	}
	f := b.fanout.Load()
	if f == nil {
		b.publishValueChangedSinks(b.lifetimeCtx, centralName, envKind, e)
		return
	}
	f.enqueue(func() {
		b.publishValueChangedSinks(f.ctx, centralName, envKind, e)
	})
}

// publishValueChangedSinks runs the MQTT fan-out for one value change and
// then re-evaluates the owning device's availability.
//
// The availability refresh is deliberately NOT the tail of
// [EventBridge.publishValueChangedMQTT]: that function returns early for a
// parameter the global visibility filter suppresses, and UNREACH /
// STICKY_UNREACH / CONFIG_PENDING — the three parameters that carry
// reachability at all — are exactly the suppressed ones. Announcing that a
// device went offline must not depend on whether the parameter carrying that
// news is exposed as an entity: gated there, a device dying produced no
// device-availability broadcast on the WebSocket plane, no Matter
// ReachableChanged, and no retained availability topic — on every deployment,
// because the suppression is the shipped default.
// The hoist is scoped to the reachability parameters on purpose. For any
// other parameter the refresh only ever flips a device back to online — news
// that the value publish itself already carries — so running it for a
// parameter the filter dropped would put a retained availability topic on the
// broker for a data point the operator asked not to see, and for a device
// whose data points are all hidden it would create an availability topic with
// no entity to own it.
func (b *EventBridge) publishValueChangedSinks(ctx context.Context, centralName string, envKind hmenum.WSEnvelopeKind, e hmevent.DataPointValueChangedEvent) {
	published := b.publishValueChangedMQTT(ctx, centralName, envKind, e)
	if published || isReachabilityParameter(e.Key.Parameter) {
		b.refreshDeviceAvailabilityFor(ctx, centralName, e)
	}
}

// refreshDeviceAvailabilityFor resolves the device coordinates of a value
// change and re-evaluates that device's availability.
func (b *EventBridge) refreshDeviceAvailabilityFor(ctx context.Context, centralName string, e hmevent.DataPointValueChangedEvent) {
	deviceAddr, _ := deviceAddrAndChannel(e.Key.ChannelAddress)
	b.refreshDeviceAvailability(ctx, centralName, inferInterface(e.Key), deviceAddr, e.Key.Parameter)
}

// onValueChangedKind is the envelope-kind-aware variant. Callers in
// the initial-snapshot loop pass [ws.KindInitial]; the source-token
// re-emit path passes [ws.KindRefresh]; the regular bus subscription
// flows through [onValueChanged] which defaults to [ws.KindChange].
//
// This is the SYNCHRONOUS composition used by the boot-time snapshot
// path, which runs off the bus dispatch goroutine and needs the broker
// publish to complete inline. The live bus subscriptions do NOT call
// this — they go through [EventBridge.dispatchLive], which runs the WS
// side inline and hands the MQTT side to the fan-out worker.
func (b *EventBridge) onValueChangedKind(ctx context.Context, centralName string, envKind hmenum.WSEnvelopeKind, e hmevent.DataPointValueChangedEvent) {
	b.publishValueChangedWS(centralName, envKind, e)
	b.publishValueChangedSinks(ctx, centralName, envKind, e)
}

// publishSnapshotValue publishes one data point of a boot snapshot.
//
// MQTT only, deliberately. MQTT is a retained-state plane: a consumer
// that arrives later reads the topic and gets the value, so the snapshot
// has to write each one. The WebSocket plane is a live stream whose
// consumers hold their own state and load it over REST — replaying the
// whole model into it as individual frames tells them nothing they
// cannot ask for, and on a large installation it is tens of thousands of
// frames arriving faster than a browser drains them. Subscribers get a
// single resync signal at the end of the walk instead; see
// [Hub.SignalResync] and the call in [EventBridge.publishCentralSnapshot].
//
// It does not go through [EventBridge.publishValueChangedSinks] the way the
// live path does: [EventBridge.publishDeviceSnapshot] — the only caller's
// caller — publishes the device's availability once, up front, before walking
// its data points, so re-evaluating it per data point would look up the device
// in the registry tens of thousands of times to reach the same cached verdict.
func (b *EventBridge) publishSnapshotValue(ctx context.Context, centralName string, e hmevent.DataPointValueChangedEvent) {
	b.publishValueChangedMQTT(ctx, centralName, ws.KindInitial, e)
}

// publishValueChangedWS emits the WebSocket-side fan-out for a value change.
// The hub's per-client enqueue is bounded and non-blocking, so this is safe to
// run inline on the bus dispatch goroutine.
func (b *EventBridge) publishValueChangedWS(centralName string, envKind hmenum.WSEnvelopeKind, e hmevent.DataPointValueChangedEvent) {
	if b.wsHub == nil {
		return
	}
	_, channelNo := parseChannel(e.Key.ChannelAddress)
	deviceAddr, _ := deviceAddrAndChannel(e.Key.ChannelAddress)
	iface := inferInterface(e.Key)

	// Resolve the channel once: it feeds both the inline DP
	// classification (category / functional type) and the CDP-state
	// aggregate below. The look-up is in-memory and nil-safe.
	ch := lookupChannel(b.registry, deviceAddr, channelNo)
	category, dpType := valueChangedClassification(ch, e.Key.Parameter)
	serialSuffix := b.registry.SerialSuffix(centralName)
	bucket := slotBucket(ch, e.Key)
	// A calculated data point carries the `calculated` family marker; the
	// WS key has to match the REST and MQTT ones byte for byte, because a
	// consumer keys one entity registry from all three.
	uniqueID := routingkey.CanonicalUniqueID(serialSuffix, e.Key.ChannelAddress, e.Key.Parameter, "")
	if bucket == payload.BucketCalculated {
		uniqueID = routingkey.CalculatedUniqueID(serialSuffix, e.Key.ChannelAddress, e.Key.Parameter)
	}
	// Availability rides the push: it can flip without the value moving, and
	// the transition into a fault usually arrives *as* a value change. The
	// same helper answers for the MQTT slot state, so the two planes cannot
	// disagree about one data point.
	_, availDP := lookupDPSource(ch, e.Key.Parameter, bucket)
	newValue := e.NewValue.Unwrap()
	// DisplayValue mirrors the REST data-point summary's projection so a
	// client that seeds from REST and updates from this stream never sees
	// the reading jump between a scaled and a raw number for the same DP.
	// MQTT is deliberately excluded from this projection — see
	// [EventBridge.publishValueChangedMQTT] and
	// internal/north/mqtt/discovery.go's applyMultiplierSensor /
	// applyMultiplierNumber, which already scale via HA value templates.
	var displayValue any
	if m, ok := availDP.(interface{ Multiplier() float64 }); ok {
		if dv, dvOK := generic.DisplayValue(newValue, m.Multiplier()); dvOK {
			displayValue = dv
		}
	}
	b.wsHub.PublishDataPointValueChanged(ws.ValueChange{
		EnvelopeKind:  envKind,
		Central:       centralName,
		Interface:     iface,
		DeviceAddress: deviceAddr,
		Channel:       channelNo,
		Parameter:     e.Key.Parameter,
		ParamsetKey:   string(e.Key.ParamsetKey),
		Value:         newValue,
		DisplayValue:  displayValue,
		Previous:      e.OldValue.Unwrap(),
		When:          e.Timestamp(),
		Category:      category,
		DataPointType: dpType,
		UniqueID:      uniqueID,
		Available:     dpAvailability(availDP, bucket),
	})
	// CDP-state aggregate: when the affected channel hosts a
	// Custom-DP, also emit a state snapshot on
	// `device.<addr>.cdps.<name>` so SPA tiles can subscribe
	// once per CDP instead of N times per slot. The look-up is
	// cheap (in-memory) and only runs when a CDP exists.
	//
	// A slot on a sibling channel resolves to the channel that hosts
	// the composing Custom-DP — see [customDPHostChannel].
	cdpChannel, cdpChannelNo := ch, channelNo
	if ch == nil || ch.CustomDataPoint() == nil {
		if host, hostNo := customDPHostChannel(b.registry, deviceAddr, e.Key); host != nil {
			cdpChannel, cdpChannelNo = host, hostNo
		}
	}
	if cdpChannel != nil {
		if cdp := cdpChannel.CustomDataPoint(); cdp != nil {
			if state, ok := customDPStatePayload(cdp); ok {
				// The wire NAME must match the identity the cdps
				// REST/WS surface assigns (`GET …/cdps`): a profile
				// channel group materialises the same parameter as a
				// CDP on several channels (a switch's STATE on
				// ch3/vch4/vch5), so the bare parameter name no longer
				// identifies one CDP. [custom.WireName] disambiguates
				// colliding names as `PARAM@<channel>` (e.g. `STATE@3`)
				// and keeps unique names bare. Publishing the bare name
				// here would mismatch the client's `(addr, name)` CDP
				// key and the event would be silently dropped — leaving
				// channel-group switch entities stuck on the optimistic
				// state. The reference stack re-renders each custom DP
				// on its own member events; using the WireName keeps the
				// state topic aligned with the catalogue entry.
				wireName := cdp.DataPointKey().Parameter
				if dev := lookupDeviceObject(b.registry, deviceAddr); dev != nil {
					wireName = custom.WireName(dev, cdp, cdpChannelNo)
				}
				// CHANNEL-level key (no parameter): the reference stack keys
				// custom data points by their primary channel; the summary
				// (customDPUniqueID) and this payload must stamp the same
				// shape so clients can correlate both surfaces.
				b.wsHub.PublishCustomDataPointStateChangedKind(
					envKind,
					centralName, deviceAddr, cdpChannelNo,
					wireName,
					cdpkind.Of(cdp),
					state, e.Timestamp(),
					routingkey.CanonicalUniqueID(serialSuffix, cdp.DataPointKey().ChannelAddress, "", ""),
				)
			}
		}
	}
}

// publishValueChangedMQTT emits the MQTT-side fan-out for a value change: raw
// plane, HA-Discovery, per-DP slot state and custom-DP aggregates. Every call
// in its body may block on the broker (a QoS1 publish waits for a PUBACK up to
// the transport's AckTimeout), so on the live path it runs on the fan-out
// worker rather than the bus dispatch goroutine. The boot-time snapshot path
// calls it inline via [onValueChangedKind].
//
// Device availability is NOT refreshed here — it is a sibling step in
// [EventBridge.publishValueChangedSinks], because this function drops
// everything for a globally suppressed parameter and the reachability
// parameters are suppressed by default.
// It reports whether the value reached the broker — false when there is no
// wiring, or when the global visibility filter dropped the parameter. The
// availability refresh in [EventBridge.publishValueChangedSinks] keys on that
// answer.
func (b *EventBridge) publishValueChangedMQTT(ctx context.Context, centralName string, envKind hmenum.WSEnvelopeKind, e hmevent.DataPointValueChangedEvent) bool {
	if b.mqtt == nil {
		return false
	}
	channel, channelNo := parseChannel(e.Key.ChannelAddress)
	deviceAddr, _ := deviceAddrAndChannel(e.Key.ChannelAddress)
	iface := inferInterface(e.Key)
	model, name := lookupDevice(b.registry, deviceAddr)

	ev, ch, ok, discoveryEligible := b.buildPublishEvent(centralName, iface, deviceAddr, channel, channelNo, model, name, e.Key, e.NewValue.Unwrap(), e.Key.ParamsetKey)
	if !ok {
		// Globally suppressed (operator's ignoredParameters
		// hiddenParameters / un-ignore overrides) — drop every
		// downstream publish.
		return false
	}
	// Discovery has TWO independent gates:
	//
	// 1. **Aggregate channel-level discovery** (climate / cover
	// lock / light / siren / valve …): fires whenever the
	// channel carries a Custom-DP (`ev.Source != nil`),
	// regardless of the triggering DP's own visibility.
	// HMIP-PSM ch3 STATE is the canonical case: its STATE is
	// NoCreate-suppressed by the custom-DP composition, so
	// `discoveryEligible == false`, but the channel still
	// hosts a Switch Custom-DP — without an unconditional
	// aggregate publish, HA never sees the switch entity at
	// all. The bridge's `declared` dedup keeps repeat events
	// on the same channel from re-publishing the aggregate.
	//
	// 2. **Per-DP discovery** (sensor / binary_sensor / select
	// number …): fires only when the DP itself is
	// discovery-eligible (Visible() == true via CDPVisible
	// DataPoint usage). NoCreate-suppressed DPs skip this
	// publish; their slot state still emits below so the
	// custom-DP HA-Discovery's `temperature_state_topic` etc.
	// references resolve to live values.
	if ev.Source != nil {
		// Aggregate path (Source set → aggregateChannel → climate/cover/...).
		b.publishEvent(ctx, ev)
	}
	if discoveryEligible {
		if ev.Source != nil {
			// Custom-DP channel + visible sub-DP → also emit the
			// per-DP discovery alongside the aggregate (HmIP-BWTH
			// HUMIDITY / ACTUAL_TEMPERATURE / HEATING_COOLING
			// WINDOW_STATE — the `additional_data_points` from
			// PublishDiscoveryOnly
			// with `Source = nil` falls through `aggregateChannel`
			// and reaches `classifyComponent` → standalone sensor.
			perDP := ev
			perDP.Source = nil
			// The bridge is nil while MQTT is disabled at runtime; the
			// Wiring's own publish helpers treat that as a no-op and this
			// reach-through has to do the same. It sits on the value-change
			// path, so an unguarded dereference would crash the daemon on
			// the first event after a config swap.
			if bridge := b.mqtt.Bridge(); bridge != nil {
				notePublish(ctx, bridge.PublishDiscoveryOnly(ctx, perDP))
			}
		} else {
			// No Custom-DP on the channel → straight per-DP path.
			b.publishEvent(ctx, ev)
		}
	}

	// Press-event aggregation: when the channel has 2+ PRESS_*
	// parameters, publish a non-retained per-channel aggregate event
	// to `<base>/<central>/<iface>/<addr>/<ch>/event`. HA's event
	// entity (one per channel) reads `value_json.event_type` from
	// this topic.
	//
	// Gated on a LIVE value change. A keypress is an edge, so only an
	// event arriving off the bus may pulse the topic. The boot-time
	// snapshot ([ws.KindInitial]) and the source-token re-emit
	// ([ws.KindRefresh]) both replay a value the model already holds —
	// and a PRESS_* value survives a restart through the persistent
	// VALUES cache and the fetch_all_device_data seed. Pulsing on
	// those replays is indistinguishable downstream from a real press
	// (same event_type, fresh modified_at), so every consumer
	// automation fired on every daemon restart.
	// Best-effort — a broker error here does not roll back the main
	// publish above.
	if discoveryEligible && envKind == ws.KindChange {
		b.publishChannelEventState(ctx, centralName, iface, deviceAddr, channelNo, ev.Model, e.Key.Parameter, ev.Channel)
	}

	// ADR 0011 phase 1b — additionally publish the per-DP slot
	// topic (`channels/<ch>/values/<param>/state`) with the
	// canonical JSON wrapper. Always runs — slot state is the
	// raw plane that HA-Discovery references via
	// `temperature_state_topic`, `current_position_topic`,
	// etc., regardless of whether the DP itself is exposed as
	// a HA entity.
	b.publishSlotState(ctx, centralName, iface, deviceAddr, channelNo, e, ch)

	// A `<X>_STATUS` event updates the base parameter's measurement
	// status out-of-band; republish the base slot so its availability
	// reflects the new status under the IsValid() gate.
	if _, isPair := hmenum.Parameter(e.Key.Parameter).BasePair(); isPair {
		b.republishBaseForStatusPair(ctx, centralName, iface, deviceAddr, channelNo, e.Key.Parameter, ch)
	}

	// ADR 0011 phase 1c — when the channel carries a custom-DP,
	// publish its derived-field aggregate to
	// `channels/<ch>/custom/<kind>/state` so HA's discovery (which
	// references this slot for hvac_mode / preset_mode / action
	// lock_state / …) sees the latest derived view. The config
	// companion (channels/<ch>/custom/<kind>/config) carries the
	// static capability set — modes / preset_modes / min_temp
	// max_temp / supports_tilt / available_tones / etc. The config
	// re-publishes on every value change so DiscoveryDynamic
	// (mode-aware Profiles, capability-conditional modes) gets
	// reflected; the bridge diff-gates the broker traffic.
	b.publishCustomDPState(ctx, centralName, iface, deviceAddr, channelNo, ch)
	b.publishCustomDPConfig(ctx, centralName, iface, deviceAddr, channelNo, ch)
	// A Custom-DP composes slots from sibling channels too (HM-CC-TC's
	// setpoint). Those channels carry no Custom-DP of their own, so the
	// two calls above are no-ops for them — resolve the hosting channel
	// and publish its aggregate as well.
	if ch == nil || ch.CustomDataPoint() == nil {
		if host, hostNo := customDPHostChannel(b.registry, deviceAddr, e.Key); host != nil {
			b.publishCustomDPState(ctx, centralName, iface, deviceAddr, hostNo, host)
			b.publishCustomDPConfig(ctx, centralName, iface, deviceAddr, hostNo, host)
		}
	}
	return true
}

// refreshDeviceAvailability re-evaluates a device's effective availability
// after one of its data points absorbed a value, publishes the retained MQTT
// availability topic on a real transition and announces the same transition on
// the owning central's bus.
//
// Both effects hang off ONE transition gate (the availability cache in
// [EventBridge.markAvailability]) on purpose: a second gate would let whichever
// consumer looked first swallow the flip for the other, and the two planes
// would then disagree about the same device.
//
// The bus announcement is what carries a per-device reachability change north
// beyond MQTT — the WebSocket device-lifecycle plane and the Matter
// reachability forward both read it. The interface-level forced-availability
// path is the only other producer, and it speaks for a whole interface, so
// without this a single device the CCU stopped reaching reaches no consumer
// at all.
func (b *EventBridge) refreshDeviceAvailability(ctx context.Context, centralName, iface, deviceAddr, parameter string) {
	dev := lookupDeviceObject(b.registry, deviceAddr)
	if dev == nil {
		return
	}
	online := dev.Available()
	if !isReachabilityParameter(parameter) {
		// Any non-reachability value change implies the device just
		// produced data — flip it to online if we previously held
		// the cache at offline (cache-gated, so the broker only
		// sees the transition publish, not every event). This is
		// what unfreezes a device that booted before any DP was
		// observed: the first real value-change event ushers the
		// device into the available state. A device the model still
		// holds as unreachable keeps that state until the
		// reachability parameter itself says otherwise.
		if !online {
			return
		}
	}
	if !b.markAvailability(ctx, centralName, iface, deviceAddr, online) {
		return
	}
	u, ok := b.registry.Get(centralName)
	if !ok || u == nil || u.EventBus == nil {
		return
	}
	events.Publish(u.EventBus, hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: iface,
		Address:     deviceAddr,
		Subtype:     hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available:   online,
	})
}

// buildPublishEvent composes the [mqtt.Event] used by the bridge for
// per-DP raw-plane / discovery / slot-state publishes. Extracted from
// [EventBridge.onValueChanged] so the boot-time
// [EventBridge.PublishInitialSnapshot] can build the same Event for
// unobserved-and-unloadable DPs and route it through
// [Bridge.PublishDiscoveryOnly] — the openccu-loom.
// discovery even when the value did not load).
//
// Returns ok=false when the GLOBAL visibility filter (operator
// `ignoredParameters` / `hiddenParameters` / un-ignore overrides)
// drops the parameter — those are silenced everywhere, no slot
// publish, no discovery.
//
// The third boolean `discoveryEligible` reports whether the DP
// should additionally surface as a HA-Discovery entity. NoCreate
// DPs (per-DP `Visible() == false`) — like the SuppressUndefinedGenericDataPoints
// pass marks for HmIP-BWTH ch1's `SET_POINT_TEMPERATURE`
// `SET_POINT_MODE` / `ACTIVE_PROFILE` — return `discoveryEligible
// == false` but still publish their slot/custom-DP state.
//
// Why two gates? The Climate / Lock / Cover custom-DP HA-Discovery
// payloads reference the slot topics of their constituent wire DPs
// via `temperature_state_topic`, `current_position_topic`, …
// Suppressing the slot publish would leave those references
// pointing at empty payloads — HA would render the climate card
// with `temperature: null` even though the wire value is observed.
// The global gate stays the kill switch for parameters the
// operator explicitly hid; the per-DP `Visible()` gate is the
// "don't surface as own entity" mark that doesn't extend to the
// raw plane.
//
// Channel may be nil when the registry has not yet hydrated the
// channel — the caller falls back to a minimal Event in that case.
func (b *EventBridge) buildPublishEvent( //nolint:gocognit,gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	centralName, iface, deviceAddr, channelAddress string,
	channelNo int,
	model, deviceName string,
	key hmtypes.DataPointKey,
	value any,
	paramset hmenum.ParamsetKey,
) (ev mqtt.Event, ch *device.Channel, ok, discoveryEligible bool) {
	ch = lookupChannel(b.registry, deviceAddr, channelNo)
	channelType := ""
	if ch != nil {
		channelType = ch.Type
	}
	// Global visibility filter — operator's ignoredParameters
	// hiddenParameters / un-ignore overrides. Skip everything. The query
	// must carry the event's REAL paramset key: MASTER visibility is a
	// default-deny whitelist, and asking with VALUES instead reported
	// every week-program / router-table parameter as visible. An empty
	// key (hand-built events) classifies as VALUES so the ignore lists
	// keep applying.
	visParamset := paramset
	if visParamset == "" {
		visParamset = hmenum.ParamsetKeyValues
	}
	if b.vis != nil && !b.vis.VisibleForChannel(model, channelType, channelNo, visParamset, hmenum.Parameter(key.Parameter)) {
		return mqtt.Event{}, nil, false, false
	}
	// Per-DP runtime Visible() — set by SuppressUndefinedGenericDataPoints
	// and family-specific marks. The treatment depends on WHY the DP
	// was suppressed:
	//
	// - When the channel carries a Custom-DP (Climate / Lock
	// Cover / Light / Siren / Valve / TextDisplay), the
	// suppressed wire DP is part of that Custom-DP's working
	// set — its slot state is referenced by the HA-Discovery
	// payload (e.g. `temperature_state_topic` for Climate).
	// Skip discovery, keep slot publishes.
	//
	// - When the channel has NO Custom-DP, the DP is hidden by
	// the global suppression pass (BWTH ch10/11/12 STATE) or
	// by an operator's explicit ignore. No consumer needs the
	// slot — drop everything.
	discoveryEligible = true
	var dp device.ParameterDataPoint
	if ch != nil {
		// Look up the DP in the paramset that originated the event.
		// MASTER paramset DPs (BOOST_TIME_PERIOD, OPTIMUM_START_STOP,
		// TEMPERATURE_OFFSET, …) only exist on `ch.MasterParameter`;
		// a VALUES-only lookup leaves dp=nil → no Category, no
		// Writable flag, and the discovery builder's writability
		// override demotes Number→Sensor incorrectly. HA then rejects
		// the entity (sensor + entity_category=config is invalid).
		switch paramset {
		case hmenum.ParamsetKeyMaster:
			dp = ch.MasterParameter(hmenum.Parameter(key.Parameter))
		default:
			dp = ch.Parameter(hmenum.Parameter(key.Parameter))
		}
		if dp != nil {
			if v, ok := dp.(interface{ Visible() bool }); ok && !v.Visible() {
				discoveryEligible = false
				if ch.CustomDataPoint() == nil {
					return mqtt.Event{}, nil, false, false
				}
			}
		}
	}
	// Build the canonical name quadruple (mirrors
	// `get_data_point_name_data`) so HA receives the same name HA
	// users get from the Python integration. Cached NameData first
	// (Task #30 hot path); fall back to the
	// BuildDataPointName factory.
	var (
		label        string
		labelOmitted bool
	)
	if dp != nil && ch != nil {
		var (
			translation string
			translated  bool
		)
		if b.labels != nil {
			translation, translated = b.labels.ParameterLabelOk(channelType, key.Parameter)
		}
		// translation_custom signals "primary parameter" with an
		// explicit empty string (e.g. `"state": ""`). HA discovery
		// then renders `name: null` so the entity id collapses to
		// the device name alone.
		if translated && translation == "" {
			labelOmitted = true
		}
		if cached, ok := datapointNameDataOf(dp); ok && !cached.IsZero() {
			// The cached quadruple was built without a translation, so the
			// locale-aware label is applied here — through the name data's
			// own composer, which re-applies the postfix the authority
			// decided on. Deciding it again here is what made the MQTT
			// discovery name diverge from the REST one.
			label = cached.WithTranslatedParameter(translation).TranslatedName()
		} else {
			nameData := device.BuildDataPointName(ch, key.Parameter, translation)
			label = nameData.TranslatedName()
		}
	} else if ch != nil && b.labels != nil {
		// Calculated DPs (DEW_POINT, ENTHALPY, OPERATING_VOLTAGE_LEVEL,
		// …) don't show up in `ch.Parameter()` because they live on
		// the calculator slot, but they still need a translated label
		// for the HA-Discovery `name` field. Without this lookup HA
		// renders the raw English title-cased fallback (e.g.
		// "Operating Voltage Level") instead of the locale-correct
		// "Betriebsspannung in V".
		translation, translated := b.labels.ParameterLabelOk(channelType, key.Parameter)
		if translation != "" {
			label = translation
		}
		if translated && translation == "" {
			labelOmitted = true
		}
	}
	ev = mqtt.Event{
		Central:        centralName,
		Interface:      iface,
		DeviceAddress:  deviceAddr,
		DeviceName:     deviceName,
		Model:          model,
		ChannelNo:      channelNo,
		ChannelAddress: channelAddress,
		ChannelType:    channelType,
		Parameter:      key.Parameter,
		Value:          value,
		Device:         lookupDeviceObject(b.registry, deviceAddr),
	}
	if ch != nil {
		ev.Channel = ch
		// Custom-DP propagation — discovery aggregator reads ev.Source
		// to switch from per-parameter to channel-aggregate mode. Skip
		// operation-mode secondary channels (e.g. HmIP-RGBW secondary colour
		// channels in the current mode): they are folded into the primary
		// channel's aggregate and must not surface as their own entity.
		if cdp := ch.CustomDataPoint(); cdp != nil {
			hidden := false
			if h, ok := cdp.(interface{ HiddenByOperationMode() bool }); ok {
				hidden = h.HiddenByOperationMode()
			}
			if src, ok := cdp.(payload.Source); ok && src != nil && !hidden {
				ev.Source = src
			}
		}
		// Mark calculated DPs so the discovery builder can route
		// `state_topic` to `calculated/<name>` instead of
		// `values/<name>`. publishSlotState routes the actual state
		// to the calculated bucket; without this flag the discovery's
		// `state_topic` mismatches the publish topic and HA renders
		// the entity unavailable.
		if isCalculatedParameter(ch, key.Parameter) {
			ev.Calculated = true
		}
		// Wire-descriptor metadata for HA-Discovery min/max/step/options.
		if dp != nil {
			pd := dp.ParameterData()
			if cd, ok := dp.(interface {
				Category() hmenum.DataPointCategory
			}); ok {
				ev.Category = cd.Category()
			}
			// Usage verdict — the same classification REST surfaces as
			// `DataPointSummary.usage`. The discovery builder gates the
			// per-parameter HA entity on it (no_create / ignored DPs and
			// ce_primary / ce_secondary custom-DP constituents never get
			// their own entity).
			if u, ok := dp.(interface{ Usage() hmenum.DataPointUsage }); ok {
				ev.Usage = u.Usage()
			}
			ev.Writable = pd.Operations&hmenum.OperationsWrite != 0
			desc := &payload.GenericConfig{
				Unit:         pd.Unit,
				Type:         pd.Type,
				Paramset:     paramset,
				Label:        label,
				LabelOmitted: labelOmitted,
			}
			if v, ok := parseFloat(pd.Min); ok {
				desc.Min = &v
			}
			if v, ok := parseFloat(pd.Max); ok {
				desc.Max = &v
			}
			if v, ok := parseFloat(pd.Default); ok {
				desc.Default = v
			}
			if len(pd.ValueList) > 0 {
				desc.ValueList = append([]string(nil), pd.ValueList...)
				// Localised options for the discovery payload. The raw
				// tokens stay in ValueList because a write carries them
				// back to the CCU verbatim.
				if vl, ok := b.labels.(mqtt.ValueListLabeler); ok && vl != nil {
					desc.ValueLabels = vl.ValueListLabels(channelType, key.Parameter, pd.ValueList)
				}
			}
			ev.Descriptor = desc
		} else {
			// Calculated DPs route through their calculator slot.
			desc := &payload.GenericConfig{Paramset: paramset, Label: label, LabelOmitted: labelOmitted}
			if u, ok := lookupCalculatedUnit(ch, key.Parameter); ok {
				desc.Unit = u
			}
			for _, calc := range ch.CalculatedDataPoints() {
				if k := calc.DataPointKey(); k.Parameter == key.Parameter {
					if cd, ok := calc.(interface {
						Category() hmenum.DataPointCategory
					}); ok {
						ev.Category = cd.Category()
					}
					if sp, ok := calc.(interface{ SourceParameters() []string }); ok {
						desc.SourceParams = append([]string(nil), sp.SourceParameters()...)
					}
					break
				}
			}
			ev.Descriptor = desc
		}
	}
	return ev, ch, true, discoveryEligible
}

// publishSlotState publishes the ADR-0011 per-DP slot state topic
// for the given value-change event. The JSON wrapper carries
// `value`, `available`, `unit`, `type`, `modified_at`, `refreshed_at`
// — the schema downstream HA-Discovery will reference via
// `value_json.value` templates.
//
// Bucket selection: VALUES paramset → `values/<param>/state`,
// MASTER → `master/<param>/state`. Calculated DPs flow through a
// separate path because they don't ride the VALUES bus.
//
// Best-effort: errors are swallowed at debug level — the legacy
// publish path is still authoritative until the aggregate publish is
// retired.
func (b *EventBridge) publishSlotState(
	ctx context.Context,
	centralName, iface, deviceAddr string,
	channelNo int,
	e hmevent.DataPointValueChangedEvent,
	ch *device.Channel,
) {
	if b.mqtt == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}

	bucket := slotBucket(ch, e.Key)
	slot := payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channelNo,
		Bucket:    bucket,
		Parameter: e.Key.Parameter,
	}

	value := e.NewValue.Unwrap()
	state := payload.PerDPState{
		Available: true,
	}
	if !e.Timestamp().IsZero() {
		ts := payload.EpochSeconds(e.Timestamp())
		state.RefreshedAt = ts
		state.ModifiedAt = ts
	}

	// Resolve the DP's source for ENUM value-label coercion. PerDPState
	// no longer carries descriptor metadata (unit/type) — those live
	// on the retained /config companion topic, ADR 0011.
	src, dp := lookupDPSource(ch, e.Key.Parameter, bucket)
	if dp != nil {
		state.Available = dpAvailability(dp, bucket)
		pd := dp.ParameterData()
		// ENUM wire values come off the wire as int indices; HA's
		// MQTT discovery declares `options: [...]` from the same
		// VALUE_LIST. Resolve to the matching label so consumers
		// see "OPEN" / "CLOSED" instead of "2" / "0".
		if pd.Type == hmenum.ParameterTypeEnum && len(pd.ValueList) > 0 {
			value = mqtt.ResolveEnumLabel(value, pd.Type, pd.ValueList)
		}
		state.AdditionalInformation = dpAdditionalInformation(dp)
	}
	state.Value = value

	notePublish(ctx, bridge.PublishSlotState(ctx, centralName, iface, slot, state))

	// ADR 0011: every DP also gets a `/config` companion carrying the
	// descriptor (min/max/value_list/unit/multiplier/usage). Diff-gated
	// in the bridge — identical bytes don't reach the broker. The
	// typed [payload.ConfigPayload] flows through as-is; the bridge
	// JSON-marshals it directly.
	if src != nil {
		notePublish(ctx, bridge.PublishSlotConfig(ctx, centralName, iface, slot, src.Config()))
	}
}

// additionalInfoProvider is the optional capability a data point implements
// to expose enriched model metadata (battery type/quantity/limits, …)
// north-bound. Most data points carry none; only enriched types (e.g. the
// calculated operating-voltage sensor) override it.
type additionalInfoProvider interface {
	AdditionalInformation() map[string]any
}

// dpAdditionalInformation returns the data point's enriched metadata, or nil
// when it carries none. Non-invasive: a DP that does not implement the
// capability (or whose map is empty) contributes nothing, so the per-DP
// state payload stays byte-identical for the common scalar case.
func dpAdditionalInformation(dp device.ParameterDataPoint) map[string]any {
	if dp == nil {
		return nil
	}
	if p, ok := dp.(additionalInfoProvider); ok {
		if m := p.AdditionalInformation(); len(m) > 0 {
			return m
		}
	}
	return nil
}

// dpValid reports whether a data point is in a fully valid state for
// north-bound exposure. Data points that do not expose IsValid default to
// available. Mirrors the reference is_valid (model/data_point.py): refreshed
// + acceptable STATUS + value type + range.
func dpValid(dp device.ParameterDataPoint) bool {
	if v, ok := dp.(interface{ IsValid() bool }); ok {
		return v.IsValid()
	}
	return true
}

// slotBucket selects the paramset bucket a value-changed event belongs to.
// It steers the MQTT topic segment and, through [dpAvailability], the
// availability rule — so both planes classify one data point identically.
func slotBucket(ch *device.Channel, key hmtypes.DataPointKey) payload.Bucket {
	switch {
	case key.ParamsetKey == hmenum.ParamsetKeyMaster:
		return payload.BucketMaster
	case ch != nil && isCalculatedParameter(ch, key.Parameter):
		return payload.BucketCalculated
	default:
		return payload.BucketValues
	}
}

// dpAvailability is the single north-bound answer to "is this value a
// confirmed reading?", shared by the MQTT slot state and the WebSocket
// value-changed push so the two can never disagree about one data point.
//
// Mirrors the reference is_valid gate (model/data_point.py): refreshed +
// acceptable STATUS + value type + range. CALCULATED is gated too — a
// derived value is only as good as the sources it was computed from, which
// the calculated sensor answers through its source-validity gate. MASTER is
// exempt: configuration is not a runtime reading, and a sleeping battery
// device may never deliver a fresh MASTER read.
//
// An unresolvable data point reports available: an unclassifiable entry must
// not be greyed out on missing information.
func dpAvailability(dp device.ParameterDataPoint, bucket payload.Bucket) bool {
	if dp == nil || bucket == payload.BucketMaster {
		return true
	}
	return dpValid(dp)
}

// republishBaseForStatusPair re-publishes the base parameter's slot state
// after its paired `<X>_STATUS` changed. A status event is delivered on its
// own topic and updates the base data point's status out-of-band, so without
// this the base slot would keep its stale availability when a measured value
// flips (in)valid as its STATUS changes.
func (b *EventBridge) republishBaseForStatusPair(
	ctx context.Context,
	centralName, iface, deviceAddr string,
	channelNo int,
	statusParam string,
	ch *device.Channel,
) {
	if b.mqtt == nil || ch == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	base, isPair := hmenum.Parameter(statusParam).BasePair()
	if !isPair {
		return
	}
	_, dp := lookupDPSource(ch, string(base), payload.BucketValues)
	if dp == nil {
		return
	}
	value, observed := dp.RawValue()
	if !observed {
		return
	}
	pd := dp.ParameterData()
	if pd.Type == hmenum.ParameterTypeEnum && len(pd.ValueList) > 0 {
		value = mqtt.ResolveEnumLabel(value, pd.Type, pd.ValueList)
	}
	state := payload.PerDPState{
		Value:                 value,
		Available:             dpValid(dp),
		AdditionalInformation: dpAdditionalInformation(dp),
	}
	if ts := dp.ModifiedAt(); !ts.IsZero() {
		epoch := payload.EpochSeconds(ts)
		state.RefreshedAt = epoch
		state.ModifiedAt = epoch
	}
	slot := payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channelNo,
		Bucket:    payload.BucketValues,
		Parameter: string(base),
	}
	notePublish(ctx, bridge.PublishSlotState(ctx, centralName, iface, slot, state))
}

// lookupDPSource returns the DP and (if available) the payload.Source
// view for the given parameter on the channel. The bucket steers the
// lookup to the right paramset bag (VALUES vs MASTER vs CALCULATED).
func lookupDPSource(
	ch *device.Channel, parameter string, bucket payload.Bucket,
) (payload.Source, device.ParameterDataPoint) {
	if ch == nil {
		return nil, nil
	}
	switch bucket {
	case payload.BucketMaster:
		dp := ch.MasterParameter(hmenum.Parameter(parameter))
		if dp == nil {
			return nil, nil
		}
		src, _ := dp.(payload.Source)
		return src, dp
	case payload.BucketCalculated:
		for _, calc := range ch.CalculatedDataPoints() {
			if calc.DataPointKey().Parameter == parameter {
				if src, ok := calc.(payload.Source); ok {
					if pdp, ok2 := calc.(device.ParameterDataPoint); ok2 {
						return src, pdp
					}
					return src, nil
				}
				return nil, nil
			}
		}
		return nil, nil
	default:
		dp := ch.Parameter(hmenum.Parameter(parameter))
		if dp == nil {
			return nil, nil
		}
		src, _ := dp.(payload.Source)
		return src, dp
	}
}

// publishDeviceInfo composes the umfangreich device-info JSON
// (mirrors, model_id
// sw_version, manufacturer, rooms, functions, channels, …) and
// publishes it to the per-device retained `<addr>/info` topic.
//
// The body is built from Device.InfoPayload (which harvests the
// `payload:"info"` tags) plus firmware tracker + channel summary.
// Single retained snapshot — every consumer (REST, UI, HA, external)
// reads from the same source.
func (b *EventBridge) publishDeviceInfo(ctx context.Context, centralName, iface string, d *device.Device) {
	if b.mqtt == nil || d == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	base, _ := d.Info().(*payload.DeviceInfo)
	if base == nil {
		base = &payload.DeviceInfo{}
	}
	info := *base
	info.Central = centralName
	if fw := d.Firmware(); fw != nil {
		if v := fw.Info().Current; v != "" {
			info.SWVersion = v
		}
	}
	chs := d.Channels()
	rows := make([]payload.DeviceInfoChannelRow, 0, len(chs))
	for _, ch := range chs {
		row := payload.DeviceInfoChannelRow{
			ChannelNo:    ch.Number,
			Type:         ch.Type,
			ParamsetKeys: []string{"VALUES", "MASTER"},
		}
		if cdp := ch.CustomDataPoint(); cdp != nil {
			if slotted, ok := cdp.(payload.Slotted); ok {
				row.CustomDPs = []string{slotted.TopicSlot().Parameter}
			}
		}
		rows = append(rows, row)
	}
	info.Channels = rows
	notePublish(ctx, bridge.PublishDeviceInfo(ctx, centralName, iface, d.Address, &info))
}

// publishDeviceDiagnostics aggregates the maintenance-channel DPs
// (RSSI_DEVICE / RSSI_PEER / DUTY_CYCLE / LOW_BAT / UNREACH
// STICKY_UNREACH / CONFIG_PENDING / UPDATE_PENDING) into one
// retained `<addr>/diagnostics` topic. The same DPs continue to be
// published individually under channels/0/values/<param>/state for
// granular subscribers — this is a convenience aggregate.
func (b *EventBridge) publishDeviceDiagnostics(ctx context.Context, centralName, iface string, d *device.Device) {
	if b.mqtt == nil || d == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	diag := map[string]any{}
	for _, ch := range d.Channels() {
		if ch.Number != 0 {
			continue
		}
		for _, param := range []string{
			"RSSI_DEVICE", "RSSI_PEER", "DUTY_CYCLE",
			"LOW_BAT", "UNREACH", "STICKY_UNREACH",
			"CONFIG_PENDING", "UPDATE_PENDING",
		} {
			if dp := ch.Parameter(hmenum.Parameter(param)); dp != nil {
				if raw, observed := dp.RawValue(); observed {
					diag[strings.ToLower(param)] = raw
				}
			}
		}
	}
	if len(diag) == 0 {
		return
	}
	notePublish(ctx, bridge.PublishDeviceDiagnostics(ctx, centralName, iface, d.Address, diag))
}

// publishCustomDPState publishes the curated derived-state JSON for
// any custom-DP attached to the channel. The custom-DP's TopicSlot
// (Slotted interface) declares the slot kind ("climate", "lock",
// "cover", …); its StatePayload carries the derived/synthetic fields
// the discovery payload references via `value_json.<derived>`.
//
// Best-effort: a channel without a custom-DP, or one whose custom-DP
// doesn't satisfy [payload.Slotted] / [payload.Source], is skipped.
func (b *EventBridge) publishCustomDPState(
	ctx context.Context,
	centralName, iface, deviceAddr string,
	channelNo int,
	ch *device.Channel,
) {
	if b.mqtt == nil || ch == nil {
		return
	}
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	slotted, ok := cdp.(payload.Slotted)
	if !ok {
		return
	}
	src, ok := cdp.(payload.Source)
	if !ok {
		return
	}
	slot := slotted.TopicSlot()
	// Trust the source-declared address+channel (model knows the
	// canonical CCU address shape) — but make sure the channel
	// matches what the channel-context expects so a misconfigured
	// model can't accidentally write to the wrong slot.
	if slot.Channel == 0 && channelNo != 0 {
		slot.Channel = channelNo
	}
	if slot.Address == "" {
		slot.Address = deviceAddr
	}
	notePublish(ctx, bridge.PublishCustomDPState(ctx, centralName, iface, slot, src.State()))
}

// publishCustomDPConfig publishes the custom-DP's ConfigPayload
// static / capability-conditional fields like Climate's
// `hvac_modes`/`preset_modes`/`min_temp`/`max_temp`/`temp_step`/
// `temperature_unit`, Cover's `inverted_control`/`supports_tilt`/
// `supports_stop`, Siren's `available_tones`/`available_lights`,
// or Lock's `supports_open`. Companion to [publishCustomDPState]
// HA discovery doesn't read it, but external consumers (REST
// dashboards, debugging tools) can subscribe to the config topic
// for the canonical capability surface without parsing the
// HA-Discovery JSON.
//
// Re-publishes on every value-change event so DiscoveryDynamic
// (Climate's mode-aware preset_modes) is reflected. The bridge
// diff-gates the actual broker publish.
func (b *EventBridge) publishCustomDPConfig(
	ctx context.Context,
	centralName, iface, deviceAddr string,
	channelNo int,
	ch *device.Channel,
) {
	if b.mqtt == nil || ch == nil {
		return
	}
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	slotted, ok := cdp.(payload.Slotted)
	if !ok {
		return
	}
	src, ok := cdp.(payload.Source)
	if !ok {
		return
	}
	slot := slotted.TopicSlot()
	if slot.Channel == 0 && channelNo != 0 {
		slot.Channel = channelNo
	}
	if slot.Address == "" {
		slot.Address = deviceAddr
	}
	notePublish(ctx, bridge.PublishSlotConfig(ctx, centralName, iface, slot, src.Config()))
}

// isCalculatedParameter reports whether the parameter name maps to a
// calculated/derived data point on the channel (DEW_POINT,
// DEW_POINT_SPREAD, ENTHALPY, OPERATING_VOLTAGE_LEVEL, …) rather
// than a wire VALUES parameter. Used by [publishSlotState] to route
// the publish to `calculated/<name>/state` instead of
// `values/<param>/state`.
func isCalculatedParameter(ch *device.Channel, parameter string) bool {
	if ch == nil {
		return false
	}
	for _, dp := range ch.CalculatedDataPoints() {
		if dp.DataPointKey().Parameter == parameter {
			return true
		}
	}
	return false
}

// nameDataProvider is the narrow contract that every DP embedding
// [datapoint.BaseDataPointFields] satisfies via promotion — it is the
// hot-path read for the cached presentation surface installed by
// `device_pipeline.go` at construction time. Used in
// [EventBridge.onValueChanged] (Task #30) to skip the
// per-event `device.BuildDataPointName` recompute when the cache is
// populated.
type nameDataProvider interface {
	NameData() naming.NameData
}

// datapointNameDataOf returns the cached [naming.NameData] when dp
// satisfies [nameDataProvider]. The boolean signals whether the type
// assertion succeeded — a false here drives the eventbridge fallback
// to the legacy [device.BuildDataPointName] factory.
func datapointNameDataOf(dp any) (naming.NameData, bool) {
	if p, ok := dp.(nameDataProvider); ok {
		return p.NameData(), true
	}
	return naming.NameData{}, false
}

// publishChannelEventState publishes the per-channel aggregate event
// payload when a PRESS_* parameter fires on a press channel — any channel
// that exposes at least one PRESS_* parameter (detected via the
// ChannelInspector). A channel with no press parameter is skipped.
//
// Best-effort: a broker error here does not affect the main value-change
// publish that has already succeeded for the caller.
func (b *EventBridge) publishChannelEventState(
	ctx context.Context,
	centralName, iface, deviceAddr string,
	channelNo int,
	model, parameter string,
	ch mqtt.ChannelInspector,
) {
	if b.mqtt == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	// Route by the kind the parameter belongs to. Until 0.68.2 only the
	// keypress branch existed, so impulse and device-error events reached
	// the REST/WS planes and simply never appeared on MQTT — a coverage gap
	// rather than a different shape.
	kind, known := event.Classify(hmenum.Parameter(parameter))
	if !known {
		return
	}
	lowered := strings.ToLower(parameter)
	switch kind {
	case event.KindKeypress:
		if len(mqtt.ChannelPressTypes(ch)) == 0 {
			// No PRESS_* parameter on the channel → not a press channel, skip.
			return
		}
		notePublish(ctx, bridge.PublishChannelEventState(ctx, centralName, iface, deviceAddr, channelNo, model, parameter))
	case event.KindImpulse:
		notePublish(ctx, bridge.PublishChannelImpulseState(ctx, centralName, iface, deviceAddr, channelNo, lowered))
	case event.KindDeviceError:
		notePublish(ctx, bridge.PublishChannelDeviceErrorState(ctx, centralName, iface, deviceAddr, channelNo, lowered))
	}
}

// publishChannelEventDiscoverySnapshot publishes the HA `event`
// entity discovery for every press channel without waiting for a
// runtime PRESS_* event. PublishInitialSnapshot calls this once per
// channel so HA sees the button entity even when the broker has no
// retained value (the typical case — buttons have no persistent
// state).
//
// Synthesises a single Event for the first PRESS_* parameter the
// channel exposes. The discovery builder's Build() method routes the
// event:
// - Multi-press channels (≥2 PRESS_* params) → BuildChannelEvent
// emits one HA event entity carrying every event_type.
// - Single-press channels → per-parameter HAComponentEvent emits
// one HA event entity per press parameter (we only synthesise
// for the first; runtime events for the same channel deduplicate
// against the bridge's discovery cache).
//
// Best-effort: a broker / discovery-builder hiccup is logged and the
// snapshot continues with the next channel.
func (b *EventBridge) publishChannelEventDiscoverySnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
	ch *device.Channel,
) {
	if b.mqtt == nil || d == nil || ch == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	pressParam := firstPressParameter(ch)
	if pressParam == "" {
		return
	}
	_, channelNo := parseChannel(ch.Address)
	ev := mqtt.Event{
		Central:        centralName,
		Interface:      iface,
		DeviceAddress:  d.Address,
		DeviceName:     d.Name(),
		Model:          d.Model,
		ChannelNo:      channelNo,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Parameter:      pressParam,
		Channel:        ch,
		Device:         d,
	}
	// Best-effort per topic; the pass-level tracker still records the
	// rejection so an incomplete snapshot does not arm the orphan sweeps.
	notePublish(ctx, bridge.PublishChannelEventDiscovery(ctx, ev))
}

// firstPressParameter returns the first click-event parameter the channel
// exposes (matching the canonical [mqtt.PressParameters] set), or an empty
// string when the channel has none. The bridge's BuildChannelEvent path uses
// the parameter only as a routing trigger; the channel inspector then
// collects every press type into the channel-level event entity.
func firstPressParameter(ch *device.Channel) string {
	if ch == nil {
		return ""
	}
	for _, p := range mqtt.PressParameters() {
		if ch.HasParameter(p) {
			return p
		}
	}
	return ""
}

// publishCustomDPDiscoverySnapshot emits the aggregate (channel-level)
// HA-Discovery payload for a channel's custom-DP plus its companion
// entities. The register-and-load path only emits the aggregate as a
// side effect of an observed VALUES parameter; write-only custom-DPs
// (HmIP-WRCD text-display) carry no readable parameter, so without this
// snapshot they never reach HA. Idempotent — the bridge diff-gates the
// publish, so channels whose aggregate was already emitted by an
// observed DP are a no-op.
//
// Best-effort: a broker / discovery-builder hiccup is swallowed and the
// snapshot continues with the next channel.
func (b *EventBridge) publishCustomDPDiscoverySnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
	ch *device.Channel,
) {
	if b.mqtt == nil || d == nil || ch == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		return
	}
	// Skip operation-mode secondary channels (e.g. HmIP-RGBW secondary colour
	// channels in the current mode): folded into the primary channel's
	// aggregate, they must not surface as their own entity.
	if h, ok := cdp.(interface{ HiddenByOperationMode() bool }); ok && h.HiddenByOperationMode() {
		return
	}
	src, ok := cdp.(payload.Source)
	if !ok || src == nil {
		return
	}
	_, channelNo := parseChannel(ch.Address)
	ev := mqtt.Event{
		Central:        centralName,
		Interface:      iface,
		DeviceAddress:  d.Address,
		DeviceName:     d.Name(),
		Model:          d.Model,
		ChannelNo:      channelNo,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Channel:        ch,
		Device:         d,
		Source:         src,
	}
	ev.SelectionLabels = b.selectionLabelsFor(ch, src)
	// Best-effort per topic; the pass-level tracker still records the
	// rejection so an incomplete snapshot does not arm the orphan sweeps.
	notePublish(ctx, bridge.PublishCustomDPDiscovery(ctx, ev))
}

func (b *EventBridge) onCentralState(centralName string, e hmevent.CentralStateChangedEvent) {
	if b.wsHub == nil {
		return
	}
	b.wsHub.PublishCentralStateChanged(centralName, string(e.From), string(e.To), e.Timestamp())
}

func (b *EventBridge) onCentralReadiness(centralName string, e hmevent.CentralReadinessChangedEvent) {
	if b.wsHub == nil {
		return
	}
	b.wsHub.PublishCentralReadinessChanged(centralName, string(e.Phase), e.Phase == hmenum.ReadinessReady, e.InterfacesLoaded, e.InterfacesTotal, e.Timestamp())
}

// --- helpers ---

func parseChannel(channelAddress string) (addr string, number int) {
	idx := strings.LastIndexByte(channelAddress, ':')
	if idx < 0 {
		return channelAddress, 0
	}
	if n, err := strconv.Atoi(channelAddress[idx+1:]); err == nil {
		number = n
	}
	return channelAddress, number
}

func deviceAddrAndChannel(channelAddress string) (deviceAddr string, channel int) {
	idx := strings.LastIndexByte(channelAddress, ':')
	if idx < 0 {
		return channelAddress, 0
	}
	deviceAddr = channelAddress[:idx]
	if n, err := strconv.Atoi(channelAddress[idx+1:]); err == nil {
		channel = n
	}
	return deviceAddr, channel
}

// inferInterface returns the interface id carried in the data-point
// key. Older callers passed a key without the interface filled in,
// so this function used to return ""; that produced topics with a
// double slash (`{base}/{central}//{addr}/...`) and broke HA's
// device-availability / state-topic resolution. Today the key is
// populated by `EventCoordinator.HandleRawEvent` and by
// `EventBridge.PublishInitialSnapshot`, so the field is the
// authoritative source.
func inferInterface(key hmtypes.DataPointKey) string {
	return key.InterfaceID
}

// customDPStatePayload pulls the aggregated state map off a Custom-DP.
// Returns (nil, false) when the DP exposes no state — the caller skips
// the CDP-state publish in that case.
//
// The canonical state contract every shipping Custom-DP implements is
// [payload.Source] (ADR 0007): `State()` returns a typed struct
// (`*payload.SwitchState{IsOn}`, `*payload.LockState`, …) that also
// feeds the cdps REST snapshot. Those structs carry json tags
// (`is_on`, `is_locked`, …), so we JSON round-trip the typed payload
// into the `map[string]any` the WS `custom_data_point.state_changed`
// event carries. This keeps the wire state identical to the REST
// `GET …/cdps` snapshot the client seeds its catalogue from, so the
// client's keyed `_state` dict and the pushed state agree key-for-key.
//
// A bare `State() map[string]any` interface (the previous shape) was
// matched by no shipping CDP — the Source structs are typed — so the
// CDP-state push silently never fired. Tying this to the Source
// contract is what makes the push reach the client at all.
func customDPStatePayload(dp device.AttachableDataPoint) (map[string]any, bool) {
	// Legacy/test hook: a DP that already exposes the map shape wins
	// without a JSON round-trip.
	if s, ok := dp.(interface{ State() map[string]any }); ok {
		if state := s.State(); state != nil {
			return state, true
		}
	}
	src, ok := dp.(payload.Source)
	if !ok || src == nil {
		return nil, false
	}
	typed := src.State()
	if typed == nil {
		return nil, false
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, false
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil || state == nil {
		return nil, false
	}
	return state, true
}

// customDPHostChannel resolves the channel whose Custom-DP composes the
// given wire data point, for a data point whose own channel carries no
// Custom-DP.
//
// A Custom-DP does not necessarily live on the channel of every value it
// composes. The classic HM-CC-TC keeps its setpoint on the regulator
// channel while the thermostat Custom-DP is materialised on the weather
// channel, and the profile schema says so. Every Custom-DP fan-out on
// this path keys on the *event's* channel, so without this resolution a
// setpoint change updates the model and reaches no aggregate surface at
// all: the SPA tile and the MQTT `custom/<kind>/state` slot both keep the
// previous value until some unrelated parameter on the primary channel
// happens to change.
//
// Returns (nil, 0) when no Custom-DP on the device composes the key.
func customDPHostChannel(reg *central.Registry, deviceAddr string, key hmtypes.DataPointKey) (host *device.Channel, channelNo int) {
	dev := lookupDeviceObject(reg, deviceAddr)
	if dev == nil {
		return nil, 0
	}
	for _, ch := range dev.Channels() {
		cdp := ch.CustomDataPoint()
		if cdp == nil {
			continue
		}
		agg, ok := cdp.(custom.AggregateDataPoint)
		if !ok {
			continue
		}
		for _, sub := range agg.SubDataPointKeys() {
			if sameWireTarget(sub, key) {
				return ch, ch.Number
			}
		}
	}
	return nil, 0
}

// sameWireTarget reports whether two data-point keys address the same wire
// parameter. The interface id is ignored (a Custom-DP slot and the event
// that carries it are the same central by construction) and the paramset
// key only compared when both sides state one — a synthesised key may
// leave it blank.
func sameWireTarget(a, b hmtypes.DataPointKey) bool {
	if a.ChannelAddress != b.ChannelAddress || a.Parameter != b.Parameter {
		return false
	}
	if a.ParamsetKey == "" || b.ParamsetKey == "" {
		return true
	}
	return a.ParamsetKey == b.ParamsetKey
}

func lookupDeviceObject(reg *central.Registry, address string) *device.Device {
	if reg == nil {
		return nil
	}
	for _, u := range reg.List() {
		if d, ok := u.ModelRegistry.Get(address); ok {
			return d
		}
	}
	return nil
}

func lookupDevice(reg *central.Registry, address string) (model, name string) {
	if reg == nil {
		return "", ""
	}
	for _, u := range reg.List() {
		if d, ok := u.ModelRegistry.Get(address); ok {
			return d.Model, d.Name()
		}
	}
	return "", ""
}

// lookupChannel returns the [*device.Channel] for a (device, no)
// pair. The MQTT bridge consumes it through the
// [mqtt.ChannelInspector] interface to decide which auxiliary
// discovery topics actually apply on this channel.
func lookupChannel(reg *central.Registry, deviceAddress string, channelNo int) *device.Channel {
	if reg == nil {
		return nil
	}
	for _, u := range reg.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		for _, ch := range dev.Channels() {
			if ch.Number == channelNo {
				return ch
			}
		}
	}
	return nil
}

// valueChangedClassification resolves the (category, functional type)
// pair for the DP named by parameter on ch, mirroring the assertion the
// REST DataPointSummary and MQTT discovery use. Returns empty strings
// when the channel or DP is unknown, or the DP does not implement the
// categorised surface — the WS write pump only surfaces these to clients
// that opted into `classify`, so empty is a safe no-op.
func valueChangedClassification(ch *device.Channel, parameter string) (category, dataPointType string) {
	if ch == nil {
		return "", ""
	}
	dp := ch.Parameter(hmenum.Parameter(parameter))
	if dp == nil {
		return "", ""
	}
	cdp, ok := dp.(device.CategorisedDataPoint)
	if !ok {
		return "", ""
	}
	cat := cdp.Category()
	return string(cat), string(hmenum.CategoryToType[cat])
}

// lookupCalculatedUnit resolves the canonical unit of a calculated
// parameter (DEW_POINT / ENTHALPY / OPERATING_VOLTAGE_LEVEL …) by
// inspecting the channel's attached calculated DPs. Returns the unit
// string + true on hit, empty + false otherwise.
func lookupCalculatedUnit(ch *device.Channel, parameter string) (string, bool) {
	if ch == nil {
		return "", false
	}
	for _, dp := range ch.CalculatedDataPoints() {
		if dp.DataPointKey().Parameter != parameter {
			continue
		}
		if u, ok := dp.(unitReporter); ok {
			return u.Unit(), true
		}
	}
	return "", false
}

// unitReporter is the narrow contract a calculated sensor satisfies
// to expose its descriptor unit. Every `*generic.Sensor[T]` (the
// embedded sink of every climate-derived sensor) implements it
// through `(*generic.DataPoint[T]).Unit`.
type unitReporter interface {
	Unit() string
}

// publishWeekProfileSnapshot publishes the HA-Discovery `select` entity
// and the initial state for a channel's attached week-profile DP.
// If the channel has no WeekProfile, or the bridge is not wired, this
// is a no-op.
//
// In addition to the one-shot publish this method wires a live
// OnChange subscription so subsequent profile-pointer updates (fired
// by subscribeProfilePointer) automatically push a fresh state to the
// broker. It goes through [EventBridge.subscribeOnce], so re-running the
// pass over the same week profile does not stack a second callback on
// it, and Stop() releases what is installed.
func (b *EventBridge) publishWeekProfileSnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
	ch *device.Channel,
) {
	if b.mqtt == nil || d == nil || ch == nil {
		return
	}
	wp := ch.WeekProfile()
	if wp == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}

	_, channelNo := parseChannel(ch.Address)

	// HA-Discovery for the week-profile pointer is intentionally
	// Suppressed
	// week-profile selector as its own HA entity — the Climate
	// entity carries the active profile via the
	// `current_schedule_profile` / `device_active_profile_index`
	// attributes (see climate.payload `extra_state_attributes`).
	// The state topic is still useful internally (REST snapshots,
	// retain-cleanup baselines), so we keep PublishWeekProfileState
	// active; only the HA-Discovery `select` is dropped.
	notePublish(ctx, bridge.PublishWeekProfileState(ctx, centralName, iface, d.Address, channelNo, wp.CurrentProfile()))
	// Eagerly load the climate schedule so Custom-DP `StatePayload`'s
	// `schedule_data` field surfaces P1..P6 per-day periods on the
	// HA card from boot. Without this, `wp.Climate().Current()`
	// returns nil until the first manual schedule write or
	// CONFIG_PENDING transition forces a reload.
	//
	// After a successful load the custom-DP state is re-published
	// so the freshly-built `schedule_data` lands in the climate JSON
	// envelope; without this, the state topic still carries the
	// pre-load snapshot (with `schedule_data` absent) and the HA
	// climate card never sees the schedule. Subsequent reloads (CCU
	// push → schedule edited externally) also flow through this
	// callback because [weekprofile.Profile.Load] calls publish().
	//
	// Best-effort: a load failure leaves the field absent (the
	// climate `json_attributes_template`'s `default(none, true)`
	// guard renders it as `null`); the next config-changed hook
	// refreshes it. Only Climate-type week profiles carry a
	// schedule; switch-type week profiles are loaded via their
	// own ChannelSwitch path on the schedule_switch DP.
	if cp := wp.Climate(); cp != nil {
		// Re-publish the custom-DP state whenever the climate
		// schedule changes (Load + Save both publish() through the
		// Profile generic). The subscription is keyed on the profile
		// object so a repeated pass over the same one does not stack a
		// second callback, and [EventBridge.detach] tears it down with
		// the rest of the bridge.
		// Use context.Background() because [weekprofile.Profile.Load]
		// fires the callback synchronously inside the goroutine
		// below, and any later Save (from the UI) will likewise be
		// triggered from a request context that may already be
		// closed by the time the callback runs.
		b.subscribeOnce(liveSubKey{
			central: centralName, iface: iface, device: d.Address,
			channel: channelNo, kind: liveSubClimateSchedule,
		}, cp, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
			return cp.OnChange(func(_, _ *schedule.Climate) {
				SafeGo("eventbridge.climate_dp_state", func() {
					b.publishCustomDPState(context.Background(), centralName, iface, d.Address, channelNo, ch)
					b.publishScheduleChangedWS(centralName, iface, d.Address, channelNo)
				})
			})
		})
		// Background load: deliberately decoupled from any request
		// context — the goroutine outlives the function call and a
		// cancelled request must not abort the warm-up fetch. Skipped once
		// the bridge is tearing down (see beginBackgroundLoad).
		if b.beginBackgroundLoad() {
			SafeGo("eventbridge.climate_schedule_load", func() { //nolint:contextcheck // background goroutine bounded by b.lifetimeCtx; Stop() cancels and drains; see #20
				defer b.goroutineWG.Done()
				loadCtx, cancel := context.WithTimeout(b.lifetimeCtx, 30*time.Second)
				defer cancel()
				_, _ = cp.Load(loadCtx)
			})
		}
	}

	// Wire live updates: when the profile pointer changes (via CCU push
	// → subscribeProfilePointer → SyncProfilePointer → OnChange), we
	// re-publish the state so HA tracks the active profile in real time.
	//
	// Capture loop-local copies that are safe to close over.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedChannel := channelNo
	capturedWP := wp

	b.subscribeOnce(liveSubKey{
		central: centralName, iface: iface, device: d.Address,
		channel: channelNo, kind: liveSubWeekProfilePointer,
	}, capturedWP, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
		return capturedWP.OnChange(func() {
			// Not the snapshot pass's ctx: the subscription survives the
			// pass (it is keyed on the profile object, so later passes
			// leave it in place), and the broker-reconnect pass runs on a
			// context that is cancelled when that connection ends.
			// Publishing on it would silently drop every later pointer
			// change. The sibling callbacks below make the same choice.
			_ = bridge.PublishWeekProfileState(
				context.Background(), capturedCentral, capturedIface, capturedAddr, capturedChannel,
				capturedWP.CurrentProfile(),
			)
			b.publishScheduleChangedWS(capturedCentral, capturedIface, capturedAddr, capturedChannel)
		})
	})
}

// publishScheduleChangedWS broadcasts the WebSocket `schedules.changed`
// frame for the channel whose week profile moved, alongside the MQTT
// publish the caller performs on the same OnChange tick, so an SPA
// schedule view held open learns about a CCU-side or second-operator
// change instead of keeping the stale schedule until the user navigates
// away and back.
//
// Both call sites live inside [EventBridge.publishWeekProfileSnapshot],
// which still wires its OnChange subscriptions only when b.mqtt is set —
// with MQTT disabled neither plane sees the change. Decoupling the WS
// wiring from that guard is a separate, larger change to that function's
// gating and out of scope here.
func (b *EventBridge) publishScheduleChangedWS(centralName, iface, deviceAddr string, channel int) {
	if b == nil || b.wsHub == nil {
		return
	}
	b.wsHub.Publish(ws.Event{
		Topic: ws.DeviceLifecycleTopic(deviceAddr),
		Type:  "schedules.changed",
		When:  time.Now(),
		Payload: ws.ScheduleChangedPayload{
			Central:       centralName,
			InterfaceID:   iface,
			DeviceAddress: deviceAddr,
			Channel:       channel,
		},
	})
}

// publishUpdateSnapshot publishes the HA-Discovery `update` entity and
// the initial state for a device's firmware-update tracker. If the
// device is not updatable (Update() is nil) this is a no-op.
//
// In addition to the one-shot publish this method wires a live
// OnChange subscription on the Firmware tracker so subsequent
// firmware-state transitions (CCU push → FirmwareInfo.Set) automatically
// re-publish the state topic. It goes through
// [EventBridge.subscribeOnce], so a repeated pass over the same tracker
// leaves the single callback in place and Stop() releases it.
func (b *EventBridge) publishUpdateSnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
) {
	if b.mqtt == nil || d == nil {
		return
	}
	upd := d.Update()
	if upd == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}

	ev := mqtt.UpdateEvent{
		Central:       centralName,
		Interface:     iface,
		DeviceAddress: d.Address,
		DeviceName:    d.Name(),
		Model:         d.Model,
		Device:        d,
		Update:        upd,
	}

	// Publish HA Discovery (deduplicated by the bridge's declared cache).
	notePublish(ctx, bridge.PublishUpdateDiscovery(ctx, centralName, ev))
	// Publish the current firmware state.
	notePublish(ctx, bridge.PublishUpdateState(ctx, centralName, iface, d.Address, upd.State()))

	// Wire live updates: when the firmware tracker fires OnChange (via
	// CCU-reported firmware-state transitions), re-publish the state
	// topic so HA reflects the new in_progress / firmware_update_state.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedUpd := upd
	fw := d.Firmware()

	// A re-ingest reuses the device object (and with it the firmware
	// tracker), so this subscribes once and stays; only a device that left
	// the model and came back gets a fresh one.
	b.subscribeOnce(liveSubKey{
		central: centralName, iface: iface, device: d.Address,
		kind: liveSubFirmware,
	}, fw, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
		return fw.OnChange(func(_ device.FirmwareInfo) {
			_ = bridge.PublishUpdateState(
				context.Background(), capturedCentral, capturedIface, capturedAddr,
				capturedUpd.State(),
			)
		})
	})
}

// publishCombinedDPSnapshot publishes HA-Discovery entities for every
// combined DP attached to ch. Visible CombinedTimerField surfaces are
// exposed as their own HA entity (separate from the wrapping custom
// DP), with the underlying wire DPs staying NoCreate-suppressed.
//
// publishScheduleEntitySnapshot emits the device-level Zeitplan HA
// `sensor` entity for a channel that carries a [weekprofile.ProfileDataPoint].
// The sensor's native state is the count of active schedule entries; the
// rich schedule structure (schedule_type, max_entries,
// available_target_channels, schedule_enabled, schedule_data) is
// surfaced via the json_attributes_topic.
//
// Wires a live OnChange subscription on the ProfileDataPoint so
// subsequent schedule-enabled / current-profile updates re-publish the
// attrs topic — once per data-point object, via
// [EventBridge.subscribeOnce].
func (b *EventBridge) publishScheduleEntitySnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
	ch *device.Channel,
) {
	if b.mqtt == nil || d == nil || ch == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	wp := ch.WeekProfile()
	if wp == nil {
		return
	}
	_, channelNo := parseChannel(ch.Address)
	ev := mqtt.ScheduleEntityEvent{
		Central:       centralName,
		Interface:     iface,
		DeviceAddress: d.Address,
		ChannelNo:     channelNo,
		DeviceName:    d.Name(),
		Model:         d.Model,
		Device:        d,
	}
	notePublish(ctx, bridge.PublishScheduleEntityDiscovery(ctx, centralName, ev))

	// Per-channel switches (non-climate only).
	b.publishScheduleSwitchSnapshot(ctx, centralName, iface, d, channelNo, wp)

	// Background-hydrate the Simple schedule from the MASTER paramset so
	// schedule_data.entries surfaces in HA. The Load() call goes through
	// the channel's refresher; on success it publishes through the
	// Profile's OnChange, which we subscribe to below for re-publishing.
	if sp := wp.Simple(); sp != nil {
		capturedSP := sp
		if b.beginBackgroundLoad() {
			SafeGo("eventbridge.simple_schedule_load", func() { //nolint:contextcheck // background goroutine bounded by b.lifetimeCtx; Stop() cancels and drains; see #20
				defer b.goroutineWG.Done()
				loadCtx, cancel := context.WithTimeout(b.lifetimeCtx, 30*time.Second)
				defer cancel()
				_, _ = capturedSP.Load(loadCtx)
			})
		}
		// Re-publish the Zeitplan attrs whenever the Simple schedule
		// changes (Load + Save both fire OnChange). Captured locals
		// avoid the loop-closure pitfall.
		capCentral := centralName
		capIface := iface
		capAddr := d.Address
		capCh := channelNo
		capWP := wp
		b.subscribeOnce(liveSubKey{
			central: centralName, iface: iface, device: d.Address,
			channel: channelNo, kind: liveSubSimpleSchedule,
		}, sp, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
			return sp.OnChange(func(_, _ *schedule.Simple) {
				b.publishScheduleEntityPayload(
					context.Background(),
					capCentral, capIface, capAddr, capCh, capWP,
				)
			})
		})
	}

	// Wire-read sync: when the WEEK_PROGRAM_CHANNEL_LOCKS bitfield
	// changes on the wire, decode it and push the per-key enabled state
	// into the ProfileDataPoint via SyncScheduleEnabled. ChannelSwitch
	// values pick it up automatically; OnChange fires the MQTT
	// re-publish below.
	if locksDP := ch.Parameter(hmenum.ParameterWeekProgramChannelLocks); locksDP != nil {
		if anyDP, ok := any(locksDP).(interface {
			OnAnyUpdate(func(old, next any)) func()
			RawValue() (any, bool)
		}); ok {
			availableKeys := orderedTargetKeys(wp.AvailableTargetChannels())
			applyLocks := func(raw any) {
				v, vok := rawLocksToUint32(raw)
				if !vok {
					return
				}
				wp.SyncScheduleEnabled(weekprofile.ParseChannelLocks(v, availableKeys))
			}
			if raw, observed := anyDP.RawValue(); observed {
				applyLocks(raw)
			}
			b.subscribeOnce(liveSubKey{
				central: centralName, iface: iface, device: d.Address,
				channel: channelNo, kind: liveSubChannelLocks,
			}, anyDP, func() func() {
				return anyDP.OnAnyUpdate(func(_, next any) { applyLocks(next) })
			})
		}
	}

	b.publishScheduleEntityPayload(ctx, centralName, iface, d.Address, channelNo, wp)
	// Live updates — re-publish attrs + state on every OnChange tick.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedCh := channelNo
	capturedWP := wp
	b.subscribeOnce(liveSubKey{
		central: centralName, iface: iface, device: d.Address,
		channel: channelNo, kind: liveSubScheduleEntity,
	}, capturedWP, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
		return capturedWP.OnChange(func() {
			b.publishScheduleEntityPayload(
				context.Background(),
				capturedCentral, capturedIface, capturedAddr, capturedCh, capturedWP,
			)
		})
	})
}

// simpleScheduleEntriesJSON encodes the wp.Simple() schedule entries
// as a JSON-shaped map keyed by slot number (stringified) — matches
// the `schedule_data.entries` attribute layout the HA-side template
// expects.
//
// Returns an empty map when the Simple profile is not attached, not
// loaded yet, or carries zero active entries.
func simpleScheduleEntriesJSON(wp *weekprofile.ProfileDataPoint) map[string]any {
	out := map[string]any{}
	if wp == nil {
		return out
	}
	sp := wp.Simple()
	if sp == nil {
		return out
	}
	sched, err := sp.Current()
	if err != nil || sched == nil {
		return out
	}
	for slot := range sched.Entries {
		out[strconv.Itoa(slot)] = simpleEntryJSON(sched.Entries[slot])
	}
	return out
}

// simpleEntryJSON renders one SimpleEntry in the flat JSON form the
// HA-side template expects. Empty / zero fields are emitted as JSON
// null so the template renders them cleanly.
func simpleEntryJSON(e schedule.SimpleEntry) map[string]any {
	weekdays := make([]string, 0, len(e.Weekdays))
	for _, w := range e.Weekdays {
		weekdays = append(weekdays, string(w))
	}
	var (
		level2     any
		duration   any
		rampTime   any
		astroType  any
		lockMode   any
		lockAction any
		permission any
	)
	if e.Level2 != nil {
		level2 = *e.Level2
	}
	// An empty Duration/RampTime means "no duration — leave the device's
	// value alone", which is a different wire pair from the genuine (base
	// 0, factor 0) zero duration weekprofile.ZeroDuration ("0ms") encodes
	// — including the firmware's "permanent" sentinel (base 7, factor 31),
	// which decodes to "" the same way. Coercing both to the string "0ms"
	// collapsed that distinction on this plane; publish JSON null instead,
	// exactly like every other optional field in this payload.
	if e.Duration != "" {
		duration = e.Duration
	}
	if e.RampTime != "" {
		rampTime = e.RampTime
	}
	if e.AstroType != "" {
		astroType = string(e.AstroType)
	}
	if e.LockMode != "" {
		lockMode = string(e.LockMode)
	}
	if e.LockAction != "" {
		lockAction = string(e.LockAction)
	}
	if e.Permission != "" {
		permission = string(e.Permission)
	}
	condition := string(e.Condition)
	if condition == "" {
		condition = string(schedule.ConditionFixedTime)
	}
	targets := e.TargetChannels
	if targets == nil {
		targets = []string{}
	}
	return map[string]any{
		"weekdays":             weekdays,
		"time":                 e.Time,
		"condition":            condition,
		"astro_type":           astroType,
		"astro_offset_minutes": e.AstroOffsetMinutes,
		"target_channels":      targets,
		"level":                e.Level,
		"level_2":              level2,
		"duration":             duration,
		"ramp_time":            rampTime,
		"lock_mode":            lockMode,
		"lock_action":          lockAction,
		"permission":           permission,
	}
}

// publishScheduleSwitchSnapshot emits one HA `switch` entity per
// ScheduleChannelSwitch registered on the device. Each switch maps a
// target channel key ("<actor>_<sub>") to an enabled/disabled toggle
// that fans out to COMBINED_PARAMETER via SetScheduleEnabled.
//
// State is read from ProfileDataPoint.ScheduleEnabled at boot and
// re-published on every wp.OnChange tick (driven by SyncScheduleEnabled
// from the wire-read sync or by direct SetScheduleEnabled writes).
func (b *EventBridge) publishScheduleSwitchSnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
	scheduleChannelNo int,
	wp *weekprofile.ProfileDataPoint,
) {
	if wp == nil || d == nil {
		return
	}
	if wp.ScheduleType() == weekprofile.ScheduleTypeClimate {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	targets := wp.AvailableTargetChannels()
	if len(targets) == 0 {
		return
	}
	enabled := wp.ScheduleEnabled()
	for _, key := range orderedTargetKeys(targets) {
		info := targets[key]
		label := b.tr("discovery.schedule_channel", "ch", strconv.Itoa(info.ChannelNo))
		if info.Name != "" && info.Name != fmt.Sprintf("Channel %d", info.ChannelNo) {
			label = b.tr("discovery.schedule_named", "name", info.Name)
		}
		notePublish(ctx, bridge.PublishScheduleSwitchDiscovery(ctx, centralName, mqtt.ScheduleSwitchEvent{
			Central:           centralName,
			Interface:         iface,
			DeviceAddress:     d.Address,
			ScheduleChannelNo: scheduleChannelNo,
			DeviceName:        d.Name(),
			Model:             d.Model,
			Device:            d,
			Key:               key,
			TargetChannelNo:   info.ChannelNo,
			Label:             label,
		}))
		st := true
		if enabled != nil {
			st = enabled[key]
		}
		notePublish(ctx, bridge.PublishScheduleSwitchState(ctx, centralName, iface, d.Address, scheduleChannelNo, key, st))
	}
	// Wire OnChange to re-publish every switch's state. The same
	// callback fires for both wire-read sync and user-driven writes.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedCh := scheduleChannelNo
	capturedWP := wp
	b.subscribeOnce(liveSubKey{
		central: centralName, iface: iface, device: d.Address,
		channel: scheduleChannelNo, kind: liveSubScheduleSwitch,
	}, capturedWP, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
		return capturedWP.OnChange(func() {
			state := capturedWP.ScheduleEnabled()
			for k, v := range state {
				_ = bridge.PublishScheduleSwitchState(
					context.Background(),
					capturedCentral, capturedIface, capturedAddr, capturedCh, k, v,
				)
			}
		})
	})
}

// orderedTargetKeys returns the keys of channels in canonical
// (`<actor>_<sub>`) order — needed so ParseChannelLocks consumes a
// stable enumeration that matches the bitfield positions.
func orderedTargetKeys(channels map[string]weekprofile.TargetChannelInfo) []string {
	if len(channels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(channels))
	for k := range channels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// rawLocksToUint32 decodes the wire-level WEEK_PROGRAM_CHANNEL_LOCKS
// value to a uint32 bitfield. CCU sends it as INTEGER, but the wire
// parser may surface it as int / int32 / int64 / float64 depending on
// transport. Returns (0, false) for an unexpected type.
func rawLocksToUint32(v any) (uint32, bool) {
	switch x := v.(type) {
	case int:
		return uint32(x), true //nolint:gosec // CCU sends a bitmask; bit-pattern reinterpretation is intentional; see #20
	case int32:
		return uint32(x), true //nolint:gosec // CCU sends a bitmask; bit-pattern reinterpretation is intentional; see #20
	case int64:
		return uint32(x), true //nolint:gosec // CCU sends a bitmask; bit-pattern reinterpretation is intentional; see #20
	case uint32:
		return x, true
	case float64:
		return uint32(x), true
	}
	return 0, false
}

// scheduleDomainForChannel resolves the user-facing schedule domain of a
// non-climate device via the same resolution the REST read path uses
// ([resolveScheduleDomain]), so the MQTT `schedule_domain` attribute and the
// REST bucket never disagree — a cover / dimmer / light / lock stops
// masquerading as a "switch". Falls back to "switch" when the device or its
// type cannot be resolved, preserving the historically non-empty attribute.
func (b *EventBridge) scheduleDomainForChannel(address string, channelNo int) string {
	if b.registry != nil {
		for _, u := range b.registry.List() {
			dev, ok := u.ModelRegistry.Get(address)
			if !ok {
				continue
			}
			if d := resolveScheduleDomain(dev, channelNo); d != "" {
				return d
			}
			break
		}
	}
	return "switch"
}

// publishScheduleEntityPayload publishes the current state + attrs JSON
// for a Zeitplan sensor. Split out so both the initial-snapshot path and
// the live OnChange callback share it.
func (b *EventBridge) publishScheduleEntityPayload(
	ctx context.Context,
	centralName, iface, address string,
	channelNo int,
	wp *weekprofile.ProfileDataPoint,
) {
	if b.mqtt == nil || wp == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	chAddr := fmt.Sprintf("%s:%d", address, channelNo)
	scheduleType := "default"
	if wp.ScheduleType() == weekprofile.ScheduleTypeClimate {
		scheduleType = "climate"
	}
	attrs := map[string]any{
		"interface_id":             iface,
		"address":                  chAddr,
		"schedule_type":            scheduleType,
		"max_entries":              wp.MaxEntries(),
		"schedule_channel_address": chAddr,
		"schedule_api_version":     "v1.0",
	}
	if wp.ScheduleType() == weekprofile.ScheduleTypeClimate {
		if profiles := wp.AvailableProfiles(); len(profiles) > 0 {
			attrs["available_profiles"] = profiles
		}
		if current := wp.CurrentProfile(); current != "" {
			attrs["current_schedule_profile"] = current
		}
		if mn := wp.MinTemp(); mn != 0 {
			attrs["min_temp"] = mn
		}
		if mx := wp.MaxTemp(); mx != 0 {
			attrs["max_temp"] = mx
		}
	} else {
		// Non-climate (default) schedules: surface schedule_enabled +
		// available_target_channels populated by the pipeline. Empty maps
		// render as `{}` so HA-side templates do not crash on a missing key.
		enabled := wp.ScheduleEnabled()
		if enabled == nil {
			enabled = map[string]bool{}
		}
		attrs["schedule_enabled"] = enabled
		targets := wp.AvailableTargetChannels()
		atcMap := make(map[string]any, len(targets))
		for k, t := range targets {
			atcMap[k] = map[string]any{
				"channel_no":      t.ChannelNo,
				"channel_address": t.ChannelAddress,
				"name":            t.Name,
				"channel_type":    t.ChannelType,
			}
		}
		attrs["available_target_channels"] = atcMap
		attrs["schedule_data"] = map[string]any{"entries": simpleScheduleEntriesJSON(wp)}
		attrs["schedule_domain"] = b.scheduleDomainForChannel(address, channelNo)
	}
	notePublish(ctx, bridge.PublishScheduleEntityAttrs(ctx, centralName, iface, address, channelNo, attrs))
	// state := count of active entries. Currently 0 until the
	// non-climate schedule-data hydrator lands; climate counters are
	// available via CountClimateEntries.
	count := 0
	if cp := wp.Climate(); cp != nil {
		if sched, err := cp.Current(); err == nil && sched != nil {
			count = weekprofile.CountClimateEntries(sched)
		}
	}
	if sp := wp.Simple(); sp != nil {
		if sched, err := sp.Current(); err == nil && sched != nil {
			count = len(sched.Entries)
		}
	}
	notePublish(ctx, bridge.PublishScheduleEntityState(ctx, centralName, iface, address, channelNo, count))
}

// combinedDiscoveryContext implements [payload.CombinedDiscoveryContext]
// for one (central, interface, device, channel, kind) tuple. It is the
// bridge's half of the projection seam: topics come from the MQTT topic
// builder, labels from the daemon catalogue and the CCU's own
// translations, and the model layer sees neither.
type combinedDiscoveryContext struct {
	stateTopic   string
	commandTopic string
	bridge       *EventBridge
	channelType  string
}

func (c combinedDiscoveryContext) CombinedStateTopic() string   { return c.stateTopic }
func (c combinedDiscoveryContext) CombinedCommandTopic() string { return c.commandTopic }

func (c combinedDiscoveryContext) Translate(key string) string {
	if c.bridge == nil {
		return key
	}
	return c.bridge.tr(key)
}

func (c combinedDiscoveryContext) ParameterLabel(parameter hmenum.Parameter) (string, bool) {
	if c.bridge == nil || c.bridge.labels == nil || c.channelType == "" {
		return "", false
	}
	return c.bridge.labels.ParameterLabelOk(c.channelType, string(parameter))
}

// publishCombinedDPSnapshot publishes discovery and state for every
// combined data point on the channel that carries a
// [payload.CombinedProjection], and wires a live subscription so later
// CCU-driven changes re-publish the state topic — once per combined
// data-point object, via [EventBridge.subscribeOnce], which Stop() tears
// down.
//
// It dispatches through the projection interface rather than switching
// on concrete types. The switch it replaced had no default branch, so a
// combined type nobody remembered to add a case for attached to its
// channel, published nothing, and was indistinguishable from a working
// one. TestCombinedProjectionCoversEveryCombinedType now fails instead.
//
// A data point without a projection is skipped deliberately: that is how
// a combined DP declares itself internal to its parent custom DP.
func (b *EventBridge) publishCombinedDPSnapshot(
	ctx context.Context,
	centralName, iface string,
	d *device.Device,
	ch *device.Channel,
) {
	if b.mqtt == nil || d == nil || ch == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	_, channelNo := parseChannel(ch.Address)
	for _, cdp := range ch.CombinedDataPoints() {
		proj, ok := cdp.(payload.CombinedProjection)
		if !ok {
			continue
		}
		b.publishCombinedProjection(ctx, bridge, centralName, iface, d, ch, channelNo, proj)
	}
}

// publishCombinedProjection publishes one projection's discovery entity
// and current state, then keeps the state topic live.
func (b *EventBridge) publishCombinedProjection(
	ctx context.Context,
	bridge *mqtt.Bridge,
	centralName, iface string,
	d *device.Device,
	ch *device.Channel,
	channelNo int,
	proj payload.CombinedProjection,
) {
	kind := proj.CombinedKind()
	topics := bridge.Topics()
	if kind == "" || topics == nil {
		return
	}
	dctx := combinedDiscoveryContext{
		stateTopic:   topics.CombinedState(centralName, iface, d.Address, channelNo, kind),
		commandTopic: topics.CombinedCommand(centralName, iface, d.Address, channelNo, kind),
		bridge:       b,
		channelType:  ch.Type,
	}
	component, body := proj.HACombinedDiscovery(dctx)
	notePublish(ctx, bridge.PublishCombinedDiscovery(ctx, centralName, mqtt.CombinedEvent{
		Central:       centralName,
		Interface:     iface,
		DeviceAddress: d.Address,
		ChannelNo:     channelNo,
		DeviceName:    d.Name(),
		Model:         d.Model,
		Device:        d,
		Kind:          kind,
		Component:     component,
		Body:          body,
	}))
	if state, observed := proj.CombinedStatePayload(); observed {
		notePublish(ctx, bridge.PublishCombinedState(ctx, centralName, iface, d.Address, channelNo, kind, state))
	}
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedChannel := channelNo
	// Keyed on the kind rather than on the wrapped wire parameter: the
	// kind is also the retained topic segment, so two projections sharing
	// a kind on one channel already write to the same topic. Keying the
	// subscriptions apart would only hide that collision behind two
	// publishers fighting over one topic.
	b.subscribeOnce(liveSubKey{
		central: centralName, iface: iface, device: d.Address,
		channel: channelNo, kind: liveSubCombined,
		variant: kind,
	}, proj, func() func() { //nolint:contextcheck // the callback outlives the pass that wired it; the snapshot ctx may already be done
		return proj.OnCombinedChange(func() {
			state, observed := proj.CombinedStatePayload()
			if !observed {
				return
			}
			_ = bridge.PublishCombinedState(
				context.Background(),
				capturedCentral, capturedIface, capturedAddr, capturedChannel,
				kind, state,
			)
		})
	})
}

// stampHubSerial registers the central's CCU serial with the MQTT
// discovery builder, reading it live from the registry unit.
//
// It is deliberately gated on a non-empty serial: an empty stamp would
// overwrite one an earlier pass already resolved, and the whole point of
// the stamp is to have a discriminator. When the serial is genuinely not
// known yet, the builder skips the payloads that need it rather than
// publishing colliding ones.
func (b *EventBridge) stampHubSerial(u *central.Unit) {
	if b.mqtt == nil || u == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	si := u.SystemInformation()
	if si.Serial == "" {
		return
	}
	bridge.SetHubInfoFor(u.Name(), mqtt.HubInfo{
		Name:    u.Name(),
		Model:   si.Model,
		Version: si.Version,
		Serial:  si.Serial,
		URL:     si.URL,
	})
}

// selectionLabelsFor localises the discovery-body lists a custom data
// point declares, so Home Assistant shows the operator words rather than
// wire tokens.
//
// The raw tokens stay authoritative everywhere else: they remain in the
// VALUE_LIST, they are what a write carries to the CCU, and the command
// path keeps accepting them. Only what an operator reads changes.
func (b *EventBridge) selectionLabelsFor(ch *device.Channel, src payload.Source) map[string][]string {
	decl, ok := src.(payload.LocalisableSelections)
	if !ok || ch == nil {
		return nil
	}
	vl, ok := b.labels.(mqtt.ValueListLabeler)
	if !ok || vl == nil {
		return nil
	}
	var out map[string][]string
	for _, sel := range decl.LocalisableSelections() {
		dp := ch.Parameter(hmenum.Parameter(sel.Parameter))
		if dp == nil {
			continue
		}
		values := dp.ParameterData().ValueList
		if len(values) == 0 {
			continue
		}
		labels := vl.ValueListLabels(ch.Type, sel.Parameter, values)
		if len(labels) != len(values) {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[sel.BodyKey] = labels
	}
	return out
}
