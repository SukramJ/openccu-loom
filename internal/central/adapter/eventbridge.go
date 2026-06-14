// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/model/device"
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

	unsubs []func()

	// availabilityCache is keyed by `<central>|<iface>|<deviceAddr>` and
	// holds the last availability state we published for that device.
	// Drives idempotent publishing: a reachable device is published
	// "online" once at boot and must not re-trigger an "online" publish
	// per value-change event — only on a reachability transition
	// (UNREACH / STICKY_UNREACH flipping) does the topic change.
	availabilityCache sync.Map
}

// NewEventBridge constructs a bridge. vis may be nil (no MQTT filter).
func NewEventBridge(r *central.Registry, wsHub *ws.Hub, mq *mqtt.Wiring) *EventBridge {
	return &EventBridge{registry: r, wsHub: wsHub, mqtt: mq}
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

// Start attaches a subscription per central.
func (b *EventBridge) Start(ctx context.Context) {
	if b.registry == nil {
		return
	}
	for _, u := range b.registry.List() {
		bus := u.EventBus
		unsub := events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
			b.onValueChanged(ctx, u.Name(), e)
		})
		b.unsubs = append(b.unsubs, unsub)

		unsubCentral := events.Subscribe(bus, func(e hmevent.CentralStateChangedEvent) {
			b.onCentralState(u.Name(), e)
		})
		b.unsubs = append(b.unsubs, unsubCentral)

		// Wire-DP source-token transitions (cache → live, live →
		// stale, stale → live) republish the same topic even though
		// the value did not change. Without this consumers that gate
		// on value diff (HA without `force_update`) miss freshness
		// flips. ADR 0019.
		unsubSrc := events.Subscribe(bus, func(e hmevent.DataPointSourceChangedEvent) {
			b.onSourceChanged(ctx, u.Name(), e)
		})
		b.unsubs = append(b.unsubs, unsubSrc)
	}
}

// onSourceChanged fans a lifecycle transition out to the same MQTT
// publish path as a regular value change. It synthesises a
// DataPointValueChangedEvent with OldValue == NoneValue so the
// dedup gate downstream treats it as a fresh emission. The value
// itself comes from the source-changed event's Value field, which
// the DP layer fills with its current RawValue at transition time.
func (b *EventBridge) onSourceChanged(ctx context.Context, centralName string, e hmevent.DataPointSourceChangedEvent) {
	if b == nil || b.mqtt == nil {
		return
	}
	if e.Value == nil {
		return
	}
	newVal, err := hmtypes.NewParamValue(e.Value)
	if err != nil {
		return
	}
	b.onValueChangedKind(ctx, centralName, ws.KindRefresh, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    e.InterfaceID,
			ChannelAddress: e.ChannelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      e.Parameter,
		},
		OldValue: hmtypes.NoneValue(),
		NewValue: newVal,
	})
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
	for _, u := range b.registry.List() {
		centralName := u.Name()
		for _, d := range u.ModelRegistry.List() {
			ifaceID := d.InterfaceID
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
					b.onValueChangedKind(ctx, centralName, ws.KindInitial, hmevent.DataPointValueChangedEvent{
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
	}
}

// markAvailability publishes the per-device availability topic, but
// only when the desired state differs from the last published state.
// Without this gate every value-change event would re-publish "online"
// (and any UNREACH parameter would re-publish whatever it just
// computed) — broker spam plus retained-topic churn. With it the topic
// flips exactly on transitions: boot → offline; first observed DP →
// online; UNREACH true → offline; UNREACH false → online.
//
// Errors are swallowed at debug level — the broker not having the
// device-availability topic is a degraded-but-not-fatal state.
func (b *EventBridge) markAvailability(ctx context.Context, centralName, iface, deviceAddr string, online bool) {
	if b.mqtt == nil {
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	key := centralName + "|" + iface + "|" + deviceAddr
	if prev, ok := b.availabilityCache.Load(key); ok {
		// availabilityCache only ever stores bool values written by this
		// method, so the assertion cannot fail.
		if prevBool, _ := prev.(bool); prevBool == online {
			return
		}
	}
	b.availabilityCache.Store(key, online)
	_ = bridge.PublishAvailability(ctx, centralName, iface, deviceAddr, online)
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
		b.onValueChangedKind(ctx, centralName, ws.KindInitial, hmevent.DataPointValueChangedEvent{
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
	model, name := d.Model, d.Name
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
	_ = bridge.PublishDiscoveryOnly(ctx, ev)
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

// Stop releases every subscription.
func (b *EventBridge) Stop() {
	for _, u := range b.unsubs {
		if u != nil {
			u()
		}
	}
	b.unsubs = nil
}

func (b *EventBridge) onValueChanged(ctx context.Context, centralName string, e hmevent.DataPointValueChangedEvent) {
	b.onValueChangedKind(ctx, centralName, ws.KindChange, e)
}

// onValueChangedKind is the envelope-kind-aware variant. Callers in
// the initial-snapshot loop pass [ws.KindInitial]; the source-token
// re-emit path passes [ws.KindRefresh]; the regular bus subscription
// flows through [onValueChanged] which defaults to [ws.KindChange].
func (b *EventBridge) onValueChangedKind(ctx context.Context, centralName, envKind string, e hmevent.DataPointValueChangedEvent) {
	channel, channelNo := parseChannel(e.Key.ChannelAddress)
	deviceAddr, _ := deviceAddrAndChannel(e.Key.ChannelAddress)

	iface := inferInterface(e.Key)
	model, name := lookupDevice(b.registry, deviceAddr)

	if b.wsHub != nil {
		// Resolve the channel once: it feeds both the inline DP
		// classification (category / functional type) and the CDP-state
		// aggregate below. The look-up is in-memory and nil-safe.
		ch := lookupChannel(b.registry, deviceAddr, channelNo)
		category, dpType := valueChangedClassification(ch, e.Key.Parameter)
		serialSuffix := b.registry.SerialSuffix(centralName)
		uniqueID := routingkey.CanonicalUniqueID(serialSuffix, e.Key.ChannelAddress, e.Key.Parameter, "")
		b.wsHub.PublishDataPointValueChangedKind(
			envKind,
			centralName, iface, deviceAddr, channelNo,
			e.Key.Parameter, string(e.Key.ParamsetKey),
			e.NewValue.Unwrap(), e.OldValue.Unwrap(),
			e.Timestamp(),
			category, dpType, uniqueID,
		)
		// CDP-state aggregate: when the affected channel hosts a
		// Custom-DP, also emit a state snapshot on
		// `device.<addr>.cdps.<name>` so SPA tiles can subscribe
		// once per CDP instead of N times per slot. The look-up is
		// cheap (in-memory) and only runs when a CDP exists.
		if ch != nil {
			if cdp := ch.CustomDataPoint(); cdp != nil {
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
						wireName = custom.WireName(dev, cdp, channelNo)
					}
					b.wsHub.PublishCustomDataPointStateChangedKind(
						envKind,
						centralName, deviceAddr, channelNo,
						wireName,
						cdpkind.Of(cdp),
						state, e.Timestamp(),
						routingkey.CanonicalUniqueID(serialSuffix, cdp.DataPointKey().ChannelAddress, cdp.DataPointKey().Parameter, ""),
					)
				}
			}
		}
	}
	if b.mqtt != nil {
		ev, ch, ok, discoveryEligible := b.buildPublishEvent(centralName, iface, deviceAddr, channel, channelNo, model, name, e.Key, e.NewValue.Unwrap(), e.Key.ParamsetKey)
		if !ok {
			// Globally suppressed (operator's ignoredParameters
			// hiddenParameters / un-ignore overrides) — drop every
			// downstream publish.
			return
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
			b.mqtt.Publish(ctx, ev)
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
				_ = b.mqtt.Bridge().PublishDiscoveryOnly(ctx, perDP)
			} else {
				// No Custom-DP on the channel → straight per-DP path.
				b.mqtt.Publish(ctx, ev)
			}
		}

		// Press-event aggregation: when the channel has 2+ PRESS_*
		// parameters, publish a non-retained per-channel aggregate event
		// to `<base>/<central>/<iface>/<addr>/<ch>/event`. HA's event
		// entity (one per channel) reads `value_json.event_type` from
		// this topic.
		// Best-effort — a broker error here does not roll back the main
		// publish above.
		if discoveryEligible {
			b.publishChannelEventState(ctx, centralName, iface, deviceAddr, channelNo, e.Key.Parameter, ev.Channel)
		}

		// ADR 0011 phase 1b — additionally publish the per-DP slot
		// topic (`channels/<ch>/values/<param>/state`) with the
		// canonical JSON wrapper. Always runs — slot state is the
		// raw plane that HA-Discovery references via
		// `temperature_state_topic`, `current_position_topic`,
		// etc., regardless of whether the DP itself is exposed as
		// a HA entity.
		b.publishSlotState(ctx, centralName, iface, deviceAddr, channelNo, e, ch)

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

		// Re-publish device availability when a reachability-relevant
		// parameter just flipped. The Device.Available() result is
		// derived from the same parameter the model just absorbed, so
		// reading it here gives the post-update view.
		if isReachabilityParameter(e.Key.Parameter) {
			if dev := lookupDeviceObject(b.registry, deviceAddr); dev != nil {
				b.markAvailability(ctx, centralName, iface, deviceAddr, dev.Available())
			}
		} else {
			// Any non-reachability value change implies the device just
			// produced data — flip it to online if we previously held
			// the cache at offline (cache-gated, so the broker only
			// sees the transition publish, not every event). This is
			// what unfreezes a device that booted before any DP was
			// observed: the first real value-change event ushers the
			// device into the available state.
			if dev := lookupDeviceObject(b.registry, deviceAddr); dev != nil && dev.Available() {
				b.markAvailability(ctx, centralName, iface, deviceAddr, true)
			}
		}
	}
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
	// hiddenParameters / un-ignore overrides. Skip everything.
	if b.vis != nil && !b.vis.VisibleForChannel(model, channelType, channelNo, hmenum.ParamsetKeyValues, hmenum.Parameter(key.Parameter)) {
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
			if translation != "" {
				postfix := ""
				if ch.IsParameterInMultipleChannels(key.Parameter) && ch.Number != 0 {
					postfix = fmt.Sprintf(" ch%d", ch.Number)
				}
				cached.TranslatedParameterName = strings.TrimSpace(translation + postfix)
			}
			label = cached.TranslatedName()
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

	bucket := payload.BucketValues
	switch {
	case e.Key.ParamsetKey == hmenum.ParamsetKeyMaster:
		bucket = payload.BucketMaster
	case ch != nil && isCalculatedParameter(ch, e.Key.Parameter):
		bucket = payload.BucketCalculated
	}
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
		pd := dp.ParameterData()
		// ENUM wire values come off the wire as int indices; HA's
		// MQTT discovery declares `options: [...]` from the same
		// VALUE_LIST. Resolve to the matching label so consumers
		// see "OPEN" / "CLOSED" instead of "2" / "0".
		if pd.Type == hmenum.ParameterTypeEnum && len(pd.ValueList) > 0 {
			value = mqtt.ResolveEnumLabel(value, pd.Type, pd.ValueList)
		}
	}
	state.Value = value

	_ = bridge.PublishSlotState(ctx, centralName, iface, slot, state)

	// ADR 0011: every DP also gets a `/config` companion carrying the
	// descriptor (min/max/value_list/unit/multiplier/usage). Diff-gated
	// in the bridge — identical bytes don't reach the broker. The
	// typed [payload.ConfigPayload] flows through as-is; the bridge
	// JSON-marshals it directly.
	if src != nil {
		_ = bridge.PublishSlotConfig(ctx, centralName, iface, slot, src.Config())
	}
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
	_ = bridge.PublishDeviceInfo(ctx, centralName, iface, d.Address, &info)
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
	_ = bridge.PublishDeviceDiagnostics(ctx, centralName, iface, d.Address, diag)
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
	_ = bridge.PublishCustomDPState(ctx, centralName, iface, slot, src.State())
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
	_ = bridge.PublishSlotConfig(ctx, centralName, iface, slot, src.Config())
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
	parameter string,
	ch mqtt.ChannelInspector,
) {
	if b.mqtt == nil {
		return
	}
	if !mqtt.IsPressParameter(parameter) {
		return
	}
	if len(mqtt.ChannelPressTypes(ch)) == 0 {
		// No PRESS_* parameter on the channel → not a press channel, skip.
		return
	}
	bridge := b.mqtt.Bridge()
	if bridge == nil {
		return
	}
	_ = bridge.PublishChannelEventState(ctx, centralName, iface, deviceAddr, channelNo, parameter)
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
		DeviceName:     d.Name,
		Model:          d.Model,
		ChannelNo:      channelNo,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Parameter:      pressParam,
		Channel:        ch,
		Device:         d,
	}
	if err := bridge.PublishChannelEventDiscovery(ctx, ev); err != nil {
		// Snapshot pass is best-effort.
		_ = err
	}
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
		DeviceName:     d.Name,
		Model:          d.Model,
		ChannelNo:      channelNo,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Channel:        ch,
		Device:         d,
		Source:         src,
	}
	if err := bridge.PublishCustomDPDiscovery(ctx, ev); err != nil {
		// Snapshot pass is best-effort.
		_ = err
	}
}

func (b *EventBridge) onCentralState(centralName string, e hmevent.CentralStateChangedEvent) {
	if b.wsHub == nil {
		return
	}
	b.wsHub.PublishCentralStateChanged(centralName, string(e.From), string(e.To), e.Timestamp())
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
			return d.Model, d.Name
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
// broker. The unsubscribe function is appended to b.unsubs so Stop()
// cleans it up.
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
	_ = bridge.PublishWeekProfileState(ctx, centralName, iface, d.Address, channelNo, wp.CurrentProfile())
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
		// Profile generic). The unsubscribe is appended to b.unsubs
		// so Stop() tears it down with the rest of the bridge.
		// Use context.Background() because [weekprofile.Profile.Load]
		// fires the callback synchronously inside the goroutine
		// below, and any later Save (from the UI) will likewise be
		// triggered from a request context that may already be
		// closed by the time the callback runs.
		scheduleUnsub := cp.OnChange(func(_, _ *schedule.Climate) { //nolint:contextcheck // OnChange callback fires asynchronously; the snapshot ctx may already be done
			b.publishCustomDPState(context.Background(), centralName, iface, d.Address, channelNo, ch)
		})
		b.unsubs = append(b.unsubs, scheduleUnsub)
		// Background load: deliberately decoupled from any request
		// context — the goroutine outlives the function call and a
		// cancelled request must not abort the warm-up fetch.
		go func() { //nolint:gosec,contextcheck // intentionally background-scoped; snapshot ctx must not cancel the warm-up load; see #20
			loadCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = cp.Load(loadCtx)
		}()
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

	unsub := capturedWP.OnChange(func() {
		_ = bridge.PublishWeekProfileState(
			ctx, capturedCentral, capturedIface, capturedAddr, capturedChannel,
			capturedWP.CurrentProfile(),
		)
	})
	b.unsubs = append(b.unsubs, unsub)
}

// publishUpdateSnapshot publishes the HA-Discovery `update` entity and
// the initial state for a device's firmware-update tracker. If the
// device is not updatable (Update() is nil) this is a no-op.
//
// In addition to the one-shot publish this method wires a live
// OnChange subscription on the Firmware tracker so subsequent
// firmware-state transitions (CCU push → FirmwareInfo.Set) automatically
// re-publish the state topic. The unsubscribe function is appended to
// b.unsubs so Stop() cleans it up.
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
		DeviceName:    d.Name,
		Model:         d.Model,
		Device:        d,
		Update:        upd,
	}

	// Publish HA Discovery (deduplicated by the bridge's declared cache).
	_ = bridge.PublishUpdateDiscovery(ctx, centralName, ev)
	// Publish the current firmware state.
	_ = bridge.PublishUpdateState(ctx, centralName, iface, d.Address, upd.State())

	// Wire live updates: when the firmware tracker fires OnChange (via
	// CCU-reported firmware-state transitions), re-publish the state
	// topic so HA reflects the new in_progress / firmware_update_state.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedUpd := upd

	unsub := d.Firmware().OnChange(func(_ device.FirmwareInfo) {
		_ = bridge.PublishUpdateState(
			ctx, capturedCentral, capturedIface, capturedAddr,
			capturedUpd.State(),
		)
	})
	b.unsubs = append(b.unsubs, unsub)
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
// attrs topic. The unsubscribe is appended to b.unsubs.
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
		DeviceName:    d.Name,
		Model:         d.Model,
		Device:        d,
	}
	_ = bridge.PublishScheduleEntityDiscovery(ctx, centralName, ev)

	// Per-channel switches (non-climate only).
	b.publishScheduleSwitchSnapshot(ctx, centralName, iface, d, channelNo, wp)

	// Background-hydrate the Simple schedule from the MASTER paramset so
	// schedule_data.entries surfaces in HA. The Load() call goes through
	// the channel's refresher; on success it publishes through the
	// Profile's OnChange, which we subscribe to below for re-publishing.
	if sp := wp.Simple(); sp != nil {
		go func(p *weekprofile.DefaultProfile) { //nolint:gosec,contextcheck // background load intentionally uses its own timeout context; snapshot ctx must not cancel the load; see #20
			loadCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = p.Load(loadCtx)
		}(sp)
		// Re-publish the Zeitplan attrs whenever the Simple schedule
		// changes (Load + Save both fire OnChange). Captured locals
		// avoid the loop-closure pitfall.
		capCentral := centralName
		capIface := iface
		capAddr := d.Address
		capCh := channelNo
		capWP := wp
		unsubSimple := sp.OnChange(func(_, _ *schedule.Simple) { //nolint:contextcheck // OnChange callback fires asynchronously; the snapshot ctx may already be done
			b.publishScheduleEntityPayload(
				context.Background(),
				capCentral, capIface, capAddr, capCh, capWP,
			)
		})
		b.unsubs = append(b.unsubs, unsubSimple)
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
			unsubLocks := anyDP.OnAnyUpdate(func(_, next any) { applyLocks(next) })
			b.unsubs = append(b.unsubs, unsubLocks)
		}
	}

	b.publishScheduleEntityPayload(ctx, centralName, iface, d.Address, channelNo, wp)
	// Live updates — re-publish attrs + state on every OnChange tick.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedCh := channelNo
	capturedWP := wp
	unsub := capturedWP.OnChange(func() { //nolint:contextcheck // OnChange callback fires asynchronously; the snapshot ctx may already be done
		b.publishScheduleEntityPayload(
			context.Background(),
			capturedCentral, capturedIface, capturedAddr, capturedCh, capturedWP,
		)
	})
	b.unsubs = append(b.unsubs, unsub)
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
		duration   = e.Duration
		rampTime   = e.RampTime
		astroType  any
		lockMode   any
		lockAction any
		permission any
	)
	if e.Level2 != nil {
		level2 = *e.Level2
	}
	if duration == "" {
		duration = "0ms"
	}
	if rampTime == "" {
		rampTime = "0ms"
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
		label := fmt.Sprintf("Zeitplan Kanal %d", info.ChannelNo)
		if info.Name != "" && info.Name != fmt.Sprintf("Channel %d", info.ChannelNo) {
			label = "Zeitplan " + info.Name
		}
		_ = bridge.PublishScheduleSwitchDiscovery(ctx, centralName, mqtt.ScheduleSwitchEvent{
			Central:           centralName,
			Interface:         iface,
			DeviceAddress:     d.Address,
			ScheduleChannelNo: scheduleChannelNo,
			DeviceName:        d.Name,
			Model:             d.Model,
			Device:            d,
			Key:               key,
			TargetChannelNo:   info.ChannelNo,
			Label:             label,
		})
		st := true
		if enabled != nil {
			st = enabled[key]
		}
		_ = bridge.PublishScheduleSwitchState(ctx, centralName, iface, d.Address, scheduleChannelNo, key, st)
	}
	// Wire OnChange to re-publish every switch's state. The same
	// callback fires for both wire-read sync and user-driven writes.
	capturedCentral := centralName
	capturedIface := iface
	capturedAddr := d.Address
	capturedCh := scheduleChannelNo
	capturedWP := wp
	unsub := capturedWP.OnChange(func() { //nolint:contextcheck // OnChange callback fires asynchronously; the snapshot ctx may already be done
		state := capturedWP.ScheduleEnabled()
		for k, v := range state {
			_ = bridge.PublishScheduleSwitchState(
				context.Background(),
				capturedCentral, capturedIface, capturedAddr, capturedCh, k, v,
			)
		}
	})
	b.unsubs = append(b.unsubs, unsub)
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
		attrs["schedule_domain"] = "switch"
	}
	_ = bridge.PublishScheduleEntityAttrs(ctx, centralName, iface, address, channelNo, attrs)
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
	_ = bridge.PublishScheduleEntityState(ctx, centralName, iface, address, channelNo, count)
}

// Currently only [combined.Timer] is wired (HSColor / LevelCombined
// remain attachable scaffolding without an MQTT discovery surface).
//
// Wires a live OnUpdate subscription so subsequent CCU-driven seconds
// changes re-publish the state topic. The unsubscribe is appended to
// b.unsubs so Stop() tears it down.
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
		timer, ok := cdp.(*combined.Timer)
		if !ok {
			// HSColor / LevelCombined: scaffolding only — no MQTT
			// surface wired yet.
			continue
		}
		label := b.combinedTimerLabel(ch, timer)
		ev := mqtt.CombinedTimerEvent{
			Central:       centralName,
			Interface:     iface,
			DeviceAddress: d.Address,
			ChannelNo:     channelNo,
			DeviceName:    d.Name,
			Model:         d.Model,
			Device:        d,
			Kind:          "duration",
			Label:         label,
			Unit:          "s",
			MinSeconds:    0,
			// Upper bound: the wire's INTEGER max (16343) reinterpreted
			// at the hours unit (16343 h ≈ 678 days) is far more than HA
			// users need; clamp to a 24h window so the UI stays usable.
			// The Timer's SetDuration auto-promotes the unit when needed,
			// so a user-entered 100 s still writes 100/UnitSeconds rather
			// than overflowing.
			MaxSeconds: 24 * 60 * 60,
			Step:       1,
		}
		_ = bridge.PublishCombinedTimerDiscovery(ctx, centralName, ev)
		if seconds, observed := timer.ValueSeconds(); observed {
			_ = bridge.PublishCombinedTimerState(ctx, centralName, iface, d.Address, channelNo, "duration", seconds)
		}
		// Live updates: on every OnComponents-driven recompute, re-publish.
		capturedCentral := centralName
		capturedIface := iface
		capturedAddr := d.Address
		capturedChannel := channelNo
		unsub := timer.OnUpdate(func(_, next float64) { //nolint:contextcheck // OnUpdate callback fires asynchronously; the snapshot ctx may already be done
			_ = bridge.PublishCombinedTimerState(
				context.Background(),
				capturedCentral, capturedIface, capturedAddr, capturedChannel,
				"duration", next,
			)
		})
		b.unsubs = append(b.unsubs, unsub)
	}
}

// combinedTimerLabel resolves the user-facing label for a combined Timer
// entity. The OCCU catalogue carries a translation for DURATION_VALUE
// ("Wert Zeitdauer" / "Duration Value") on the channel type the timer
// lives on; fall back to "Zeitdauer" / "Duration" when the catalogue has
// no entry. The "Wert " prefix is dropped so HA's entity_id derivation
// (device.name + label) produces e.g. "alarmsirene_fl_zeitdauer" rather
// than the stutter "...wert_zeitdauer".
func (b *EventBridge) combinedTimerLabel(ch *device.Channel, timer *combined.Timer) string {
	if b.labels != nil && ch != nil {
		if t, ok := b.labels.ParameterLabelOk(ch.Type, string(timer.ValueParameter)); ok && t != "" {
			// Strip a leading "Wert " (German OCCU translation for the
			// raw DURATION_VALUE wire parameter) so the combined-DP
			// label reads cleanly.
			label := strings.TrimSpace(strings.TrimPrefix(t, "Wert "))
			label = strings.TrimSpace(strings.TrimPrefix(label, "Value "))
			if label != "" {
				return label
			}
		}
	}
	return "Zeitdauer"
}
