// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// EventCoordinator turns raw CCU callbacks into typed domain events on
// the internal bus. It owns the cache write path and the duplicate-
// detection that avoids emitting no-op value-change events. The
// per-interface monotonic-event timestamps are also tracked here so
// the recovery / loop-detection path can ask "when was the last time
// any callback for interface X arrived?".
type EventCoordinator struct {
	bus         *events.Bus
	cache       *CacheCoordinator
	logger      *slog.Logger
	centralName string

	mu             sync.RWMutex
	lastEventStamp map[string]time.Time
	// lastEventWall tracks wall-clock time of the last event per interface.
	// Distinct from lastEventStamp only conceptually; in Go time.Time carries
	// both monotonic and wall readings so either field could serve both
	// Purposes. We keep both for API symmetry
	// `_last_event_seen_for_interface` (wall) and
	// `_last_event_monotonic_for_interface` (monotonic).
	lastEventWall map[string]time.Time

	// dpUnsubs tracks unsubscribe closures added by AddDataPointSubscription so
	// Clear() can release them.
	dpUnsubs []func()

	// onConfigSettled is invoked when CONFIG_PENDING transitions from true to
	// false on a device, signalling that a MASTER paramset write has been
	// applied and the device's MASTER values can now be re-fetched.
	onConfigSettled func(interfaceID, deviceAddress string)

	// pingPongTracker is an optional hook invoked when a PONG parameter
	// arrives. Wired via SetPingPongTracker. Nil = no routing.
	// The second argument is the token extracted from the echoed caller_id
	// ("<interfaceID>#<token>"); empty string when no token was embedded.
	pingPongTracker func(interfaceID, pongToken string)
}

// NewEventCoordinator wires a cache and a bus.
func NewEventCoordinator(bus *events.Bus, cache *CacheCoordinator, logger *slog.Logger) *EventCoordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventCoordinator{
		bus:            bus,
		cache:          cache,
		logger:         logger,
		lastEventStamp: make(map[string]time.Time),
		lastEventWall:  make(map[string]time.Time),
	}
}

// SetCentralName stores the central name for use in published events.
// Called by the central wiring once the coordinator is associated with
// a named central. Safe to call before or after Start.
func (c *EventCoordinator) SetCentralName(name string) {
	c.mu.Lock()
	c.centralName = name
	c.mu.Unlock()
}

// SetOnConfigSettled installs the callback fired when a device's
// CONFIG_PENDING transitions from true to false. The callback is expected to
// schedule a MASTER paramset re-fetch (typically through
// `backends.MasterPoller.SchedulePoll` for every relevant (channelAddress,
// ParamsetKeyMaster) of the device). Pass nil to detach.
func (c *EventCoordinator) SetOnConfigSettled(fn func(interfaceID, deviceAddress string)) {
	c.mu.Lock()
	c.onConfigSettled = fn
	c.mu.Unlock()
}

// LastEventMonotonicForInterface returns the wall-time stamp of the most
// recent callback observed for `interfaceID`, plus an `observed` flag that
// distinguishes "never" from "long ago at the zero time".
func (c *EventCoordinator) LastEventMonotonicForInterface(interfaceID string) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.lastEventStamp[interfaceID]
	return t, ok
}

// NewestEventAge returns the seconds elapsed (relative to `now`) since the
// most recent callback observed across every interface, and ok=false when no
// event has been observed yet. It backs the MetricLastEventAgeSecs liveness
// gauge: a single hub-level value derived from the freshest per-interface
// stamp, so a quiet interface does not mask a busy one.
func (c *EventCoordinator) NewestEventAge(now time.Time) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var newest time.Time
	for _, ts := range c.lastEventStamp {
		if newest.IsZero() || ts.After(newest) {
			newest = ts
		}
	}
	if newest.IsZero() {
		return 0, false
	}
	return now.Sub(newest).Seconds(), true
}

// MarkEvent stamps the per-interface event clock to `at`. Public so
// transport-side callbacks (init, ping-pong, error) can refresh the
// "interface alive" signal without going through the
// HandleRawEvent code path. Pass [time.Time]{} to use time.Now().
func (c *EventCoordinator) MarkEvent(interfaceID string, at time.Time) {
	if interfaceID == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	c.mu.Lock()
	if c.lastEventStamp == nil {
		c.lastEventStamp = make(map[string]time.Time)
	}
	c.lastEventStamp[interfaceID] = at
	if c.lastEventWall == nil {
		c.lastEventWall = make(map[string]time.Time)
	}
	c.lastEventWall[interfaceID] = at
	c.mu.Unlock()
}

// HandleRawEvent processes a single event callback. It updates the cache
// with the supplied domain value and publishes
// [hmevent.DataPointValueChangedEvent] if the value actually changed.
// Wire-to-domain conversion (xmlrpc.Value → hmtypes.ParamValue) is the
// caller's responsibility; the adapter layer holds the xmlrpc import.
func (c *EventCoordinator) HandleRawEvent(
	_ context.Context,
	interfaceID, channelAddress, parameter string,
	value hmtypes.ParamValue,
) {
	key := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
	c.MarkEvent(interfaceID, time.Time{})
	newVal := value

	old, hadOld := c.cache.Get(key)
	c.cache.Set(key, newVal, "ccu_event")
	if hadOld && old.Value.Equal(newVal) {
		// Unchanged — skip the event to avoid downstream spam.
		return
	}

	var oldVal hmtypes.ParamValue
	if hadOld {
		oldVal = old.Value
	} else {
		oldVal = hmtypes.NoneValue()
	}
	events.Publish(c.bus, hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		Key:      key,
		OldValue: oldVal,
		NewValue: newVal,
	})

	// CONFIG_PENDING True → False signals the device has applied a MASTER
	// paramset write. Invoke the configured hook so the adapter layer can
	// re-fetch the affected paramsets.
	if parameter == string(hmenum.ParameterConfigPending) && hadOld {
		if isFalseBool(newVal) && isTrueBool(oldVal) {
			c.mu.RLock()
			hook := c.onConfigSettled
			c.mu.RUnlock()
			if hook != nil {
				hook(interfaceID, splitDeviceAddress(channelAddress))
			}
		}
	}
}

// PublishBackendParameterEvent publishes a
// [hmevent.RPCParameterReceivedEvent] that carries the raw wire value before
// it is written to the cache. Callers invoke this before calling
// HandleRawEvent when they want subscribers to see the unprocessed wire form.
func (c *EventCoordinator) PublishBackendParameterEvent(
	interfaceID, channelAddress, parameter, rawValue string,
) {
	if interfaceID == "" {
		return
	}
	c.mu.RLock()
	cn := c.centralName
	c.mu.RUnlock()
	events.Publish(c.bus, hmevent.RPCParameterReceivedEvent{
		Base:           hmevent.NewBase(),
		CentralName:    cn,
		InterfaceID:    interfaceID,
		ChannelAddress: channelAddress,
		Parameter:      parameter,
		RawValue:       rawValue,
	})
}

// PublishDeviceTriggerEvent converts a raw device-trigger callback into a
// typed [hmevent.DeviceTriggerEvent] and publishes it on the internal bus.
func (c *EventCoordinator) PublishDeviceTriggerEvent(
	_ context.Context,
	interfaceID, deviceAddress string,
	channelNo int,
	triggerEventType hmenum.DeviceTriggerEventType,
	parameter string,
	value hmtypes.ParamValue,
) {
	if interfaceID == "" {
		return
	}
	c.mu.RLock()
	cn := c.centralName
	c.mu.RUnlock()
	c.MarkEvent(interfaceID, time.Time{})
	events.Publish(c.bus, hmevent.DeviceTriggerEvent{
		Base:          hmevent.NewBase(),
		CentralName:   cn,
		InterfaceID:   interfaceID,
		DeviceAddress: deviceAddress,
		ChannelNo:     channelNo,
		EventType_:    triggerEventType,
		Parameter:     parameter,
		Value:         value,
	})
}

// PublishSystemEvent publishes a [hmevent.SystemStatusChangedEvent] on the
// internal bus. The caller populates the payload; this method simply
// refreshes the interface timestamp and dispatches.
func (c *EventCoordinator) PublishSystemEvent(
	_ context.Context,
	ev hmevent.SystemStatusChangedEvent,
) {
	if ev.InterfaceID != "" {
		c.MarkEvent(ev.InterfaceID, time.Time{})
	}
	if ev.Base == (hmevent.Base{}) {
		ev.Base = hmevent.NewBase()
	}
	events.Publish(c.bus, ev)
}

// AddDataPointSubscription registers a callback that is called for every
// [hmevent.DataPointValueChangedEvent] on the bus, regardless of which data
// point triggered it (wildcard subscription). Returns an unsubscribe function;
// callers must call it when the subscription is no longer needed to avoid
// leaking goroutines. The coordinator also stores the unsubscribe internally
// so [Clear] can release all registered subscriptions at shutdown time.
func (c *EventCoordinator) AddDataPointSubscription(fn func(hmevent.DataPointValueChangedEvent)) func() {
	return c.AddDataPointSubscriptionForKey(hmtypes.DataPointKey{}, fn)
}

// AddDataPointSubscriptionForKey registers a callback that is called only for
// [hmevent.DataPointValueChangedEvent] events whose Key equals dpk. Pass the
// zero [hmtypes.DataPointKey] to receive all events (wildcard), which is the
// same behaviour as [AddDataPointSubscription].
//
// The returned unsubscribe function must be called when the subscription is no
// longer needed. The coordinator also tracks the unsubscribe internally so
// [Clear] can release all registered subscriptions at shutdown time.
func (c *EventCoordinator) AddDataPointSubscriptionForKey(dpk hmtypes.DataPointKey, fn func(hmevent.DataPointValueChangedEvent)) func() {
	isWildcard := dpk == (hmtypes.DataPointKey{})
	var handler func(hmevent.DataPointValueChangedEvent)
	if isWildcard {
		handler = fn
	} else {
		handler = func(e hmevent.DataPointValueChangedEvent) {
			if e.Key == dpk {
				fn(e)
			}
		}
	}
	unsub := events.Subscribe(c.bus, handler)
	c.mu.Lock()
	c.dpUnsubs = append(c.dpUnsubs, unsub)
	c.mu.Unlock()
	return unsub
}

// Clear releases all data-point event subscriptions registered via
// [AddDataPointSubscription] and resets per-interface event timestamps. Call
// on shutdown or when tearing down a central to prevent leaked subscriptions.
// P2.
func (c *EventCoordinator) Clear() {
	c.mu.Lock()
	unsubs := c.dpUnsubs
	c.dpUnsubs = nil
	// Reset the timestamp maps so recovered state is not stale.
	c.lastEventStamp = make(map[string]time.Time)
	c.lastEventWall = make(map[string]time.Time)
	c.mu.Unlock()

	// Run the unsubscribe barriers with c.mu released. Each unsubscribe blocks
	// until an in-flight dispatch of the handler completes, so holding c.mu
	// across it risks deadlocking against a subscriber callback that reaches
	// back into the coordinator.
	for _, unsub := range unsubs {
		unsub()
	}
}

// GetLastEventSeenForInterface returns the wall-clock time of the most recent
// callback observed for interfaceID, plus an observed flag. In Go, time.Time
// carries both wall and monotonic readings, so the same timestamp serves both
// purposes. P2.
func (c *EventCoordinator) GetLastEventSeenForInterface(interfaceID string) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.lastEventWall[interfaceID]
	return t, ok
}

// EmitDevicesDelayedEvent publishes a [hmevent.DeviceLifecycleEvent] with
// subtype [hmenum.DeviceLifecycleSubtypeDelayed] for the given device
// addresses. This signals to north-bound adapters that device creation was
// started but not yet completed (e.g. due to pending acceptance in
// installation mode). P2.
func (c *EventCoordinator) EmitDevicesDelayedEvent(interfaceID string, deviceAddresses []string) {
	if len(deviceAddresses) == 0 {
		return
	}
	c.mu.RLock()
	cn := c.centralName
	c.mu.RUnlock()
	for _, addr := range deviceAddresses {
		events.Publish(c.bus, hmevent.DeviceLifecycleEvent{
			Base:        hmevent.NewBase(),
			CentralName: cn,
			InterfaceID: interfaceID,
			Address:     addr,
			Subtype:     hmenum.DeviceLifecycleSubtypeDelayed,
		})
	}
}

// HandleRawEventNormalized is a wrapper around [HandleRawEvent] that routes
// the PONG sentinel to the optional ping-pong tracker rather than the
// cache/event path; every other parameter dispatches unchanged.
//
// `<X>_STATUS` parameters are deliberately NOT mapped to their base name:
// they carry the MEASUREMENT STATUS of `<X>` (NORMAL / UNKNOWN / OVERFLOW,
// wire value 0/1/2), not a value echo. The former suffix-stripping published
// the status index (usually 0) as a `value_changed` for the base parameter,
// corrupting the dynamic cache and oscillating north-bound consumers (WS,
// MQTT, external clients) between the real measurement and 0 — the model
// layer was correct the whole time. The status itself lands on the base
// data point via UpdateStatusFromWire in the callback handler.
func (c *EventCoordinator) HandleRawEventNormalized(
	ctx context.Context,
	interfaceID, channelAddress, parameter string,
	value hmtypes.ParamValue,
) {
	// PONG routing — divert to the ping-pong tracker.
	if parameter == "PONG" {
		c.mu.RLock()
		pp := c.pingPongTracker
		c.mu.RUnlock()
		if pp != nil {
			// Forward the raw caller_id the CCU echoed in the PONG event. The
			// hook (wired in pingpong_wiring) owns correlation: it knows the
			// client identity, so it both extracts the "<iface>#<token>" token
			// and verifies the embedded prefix is THIS interface's own ping
			// prefix. The CCU broadcasts PONG events to every registered
			// logic-layer client, so we also receive other instances' PONGs
			// (e.g. "Otto-HmIP-RF#...") on our interface — those must not be
			// correlated. Mirrors the reference v_interface_id == interface_id
			// guard.
			if value.Kind == hmtypes.ValueKindString {
				pp(interfaceID, value.String)
			}
		}
		c.MarkEvent(interfaceID, time.Time{})
		return
	}

	c.HandleRawEvent(ctx, interfaceID, channelAddress, parameter, value)
}

// SetPingPongTracker wires an optional hook that is called when a PONG
// parameter arrives (before cache dispatch). The hook receives the event's
// interfaceID and the raw caller_id the CCU echoed ("<iface>#<token>", or a
// bare interface name for the lightweight liveness probe). The hook owns
// correlation — it extracts the token and verifies the embedded prefix
// identifies this interface — because that needs the client identity, which
// lives in the wiring layer. Pass nil to detach.
func (c *EventCoordinator) SetPingPongTracker(fn func(interfaceID, callerID string)) {
	c.mu.Lock()
	c.pingPongTracker = fn
	c.mu.Unlock()
}

// EmitDevicesCreatedEvents publishes a [hmevent.DeviceCreatedEvent] for each
// address in addresses. Use this when a batch of devices needs to be
// announced after an out-of-band operation (e.g. bulk import, cache restore)
// rather than arriving one-by-one through the newDevices callback.
func (c *EventCoordinator) EmitDevicesCreatedEvents(interfaceID string, addresses []string, model string, source hmenum.SourceOfDeviceCreation) {
	if len(addresses) == 0 {
		return
	}
	c.mu.RLock()
	cn := c.centralName
	c.mu.RUnlock()
	for _, addr := range addresses {
		events.Publish(c.bus, hmevent.DeviceCreatedEvent{
			Base:        hmevent.NewBase(),
			CentralName: cn,
			InterfaceID: interfaceID,
			Address:     addr,
			Model:       model,
			Source:      source,
		})
	}
}

// EmitDeviceRemovedEvent publishes a [hmevent.DeviceRemovedEvent] for the
// given address. Use this when a device-removed signal is received out-of-band
// (e.g. via a scheduled deletion job) rather than through the deleteDevices
// callback.
func (c *EventCoordinator) EmitDeviceRemovedEvent(interfaceID, address string) {
	if address == "" {
		return
	}
	c.mu.RLock()
	cn := c.centralName
	c.mu.RUnlock()
	events.Publish(c.bus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: cn,
		InterfaceID: interfaceID,
		Address:     address,
	})
}

// EmitHubRefreshedEvent signals north-bound adapters that a full hub-data
// refresh has completed. Adapters that derive caches from hub state (MQTT
// discovery, REST ETag tracking) use this event as a flush trigger.
//
// The signal is carried as a [hmevent.DataRefreshCompletedEvent] with
// Scope = "hub" so subscribers can filter without a new event type.
func (c *EventCoordinator) EmitHubRefreshedEvent() {
	c.mu.RLock()
	cn := c.centralName
	c.mu.RUnlock()
	events.Publish(c.bus, hmevent.DataRefreshCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: cn,
		JobName:     "hub",
		Success:     true,
	})
}

// isTrueBool / isFalseBool extract boolean truthiness from a
// [hmtypes.ParamValue] without panicking on non-bool kinds.
func isTrueBool(v hmtypes.ParamValue) bool {
	return v.Kind == hmtypes.ValueKindBool && v.Bool
}

func isFalseBool(v hmtypes.ParamValue) bool {
	return v.Kind == hmtypes.ValueKindBool && !v.Bool
}

// splitDeviceAddress strips the ":<channel>" suffix from an address.
// CONFIG_PENDING fires on channel 0; the hook however needs to apply
// to every channel of the device, so callers operate on the device
// address (everything before the first colon).
func splitDeviceAddress(channelAddress string) string {
	for i := range len(channelAddress) {
		if channelAddress[i] == ':' {
			return channelAddress[:i]
		}
	}
	return channelAddress
}
