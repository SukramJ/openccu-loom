// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CallbackHandlers implements [rpcserver.Handlers] for one central. It routes
// every incoming CCU callback into the central's registry and notifies the
// matching data point via [generic.DataPoint.OnWireValue].
//
// NewDevices / DeleteDevices / UpdateDevice / ReplaceDevice ReaddedDevice are
// handled minimally: they log the event and touch the registry where cheap. A
// full hot-plug story (rebuilding device profiles on the fly) is outside the
// current scope and can be added without changing this file's public surface.
//
// Background goroutines (e.g. self-reload tasks spawned by
// [scheduleSelfReload]) are tracked with a [sync.WaitGroup] so [Stop] can
// block until all in-flight tasks complete.
type CallbackHandlers struct {
	unit   *central.Unit
	logger *slog.Logger
	// wg tracks every background goroutine spawned by this handler.
	// Stop() blocks until all goroutines have returned. Every start goes
	// through [CallbackHandlers.goBackground], never through wg.Go
	// directly — see stopMu.
	wg sync.WaitGroup
	// stopMu orders goroutine starts against Stop. Deregistering a route
	// does not drain the callbacks already dispatched on it, so one of
	// them can still reach a wg.Go while Stop is parked in wg.Wait — the
	// Add-concurrent-with-Wait misuse the runtime panics on. Taking this
	// lock on both sides makes "started" and "stopping" mutually
	// exclusive: a start that wins the race is waited for, a start that
	// loses it does not happen.
	stopMu   sync.Mutex
	stopping bool
	// ctx / cancel control background tasks; Stop() cancels this context
	// so long-running tasks (e.g. reload fetches) can abort promptly.
	ctx    context.Context
	cancel context.CancelFunc
	// writer is the optional south-bound ValueWriter used to resolve
	// per-interface backends for device refresh operations (UpdateDevice,
	// ReplaceDevice, ReaddedDevice). When nil, those callbacks degrade to
	// cache-invalidation only.
	writer *clientpkg.ValueWriter

	// delayNewDeviceCreation defers immediate ingest of newly-paired
	// devices: when true, NewDevices only stores the descriptions for
	// the operator inbox / manual-accept flow instead of creating the
	// entities right away. Set per-central via [SetDelayNewDeviceCreation].
	delayNewDeviceCreation bool

	// selfReloadSem is a non-blocking semaphore (buffered channel) that
	// caps the number of concurrent self-reload goroutines at
	// selfReloadConcurrency. A value-flood from the CCU can otherwise
	// spawn many simultaneous direct LoadValue calls against the CCU,
	// exceeding the CCU's duty-cycle budget. When the semaphore is full
	// the incoming reload is dropped with a debug log — the
	// UnobservedSweep or the next CCU push will fill in the gap.
	selfReloadSem chan struct{}
}

// selfReloadConcurrency is the maximum number of concurrent self-reload
// goroutines per CallbackHandlers instance. Chosen large enough to absorb
// short burst events from the CCU (e.g. an initialisation wave) without
// queuing unbounded work against the CCU radio.
const selfReloadConcurrency = 16

// NewCallbackHandlers wires the adapter for c.
func NewCallbackHandlers(u *central.Unit, logger *slog.Logger) *CallbackHandlers {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CallbackHandlers{
		unit:          u,
		logger:        logger,
		ctx:           ctx,
		cancel:        cancel,
		selfReloadSem: make(chan struct{}, selfReloadConcurrency),
	}
}

// SetWriter wires the south-bound ValueWriter so UpdateDevice, ReplaceDevice,
// and ReaddedDevice can resolve per-interface backends for fresh description
// fetches.
func (h *CallbackHandlers) SetWriter(w *clientpkg.ValueWriter) {
	h.writer = w
}

// SetDelayNewDeviceCreation toggles deferred ingest of newly-paired
// devices for this central. When true, NewDevices stores the
// descriptions for the manual-accept flow but does not create the
// entities immediately.
func (h *CallbackHandlers) SetDelayNewDeviceCreation(delay bool) {
	h.delayNewDeviceCreation = delay
}

// incidentRecorder returns the incident recorder wired to the central's
// CacheCoordinator, or nil when none has been set. Reading lazily from
// the coordinator avoids a separate wiring step: wireIncidentRecorder (in
// cmd/) installs the recorder on CacheCoordinator; from that point onward
// every Error() callback automatically persists incidents without requiring
// an explicit SetIncidentRecorder call on this handler.
func (h *CallbackHandlers) incidentRecorder() reliability.IncidentRecorder {
	if h.unit == nil || h.unit.Cache == nil {
		return nil
	}
	return h.unit.Cache.GetIncidentRecorder()
}

// canonicalInterfaceID maps the interface_id the CCU echoes in a callback
// envelope (the [InitInterfaceID] form `loom-<instance>-<central>-<iface>`)
// back to the canonical host-independent [WireInterfaceID]
// (`<central>-<iface>`) used by the stamped devices, the Clients registry, and
// DataPointKeys. Every inbound callback entry runs this before touching the
// model so the echoed id matches the internal id. Nil-safe.
func (h *CallbackHandlers) canonicalInterfaceID(interfaceID string) string {
	if h == nil || h.unit == nil {
		return interfaceID
	}
	return CanonicalInterfaceID(h.unit.InstanceName(), h.unit.Name(), interfaceID)
}

// truncateWireID bounds an untrusted interface id before it reaches a log
// record. The XML-RPC body limit is 10 MiB, so an id echoed into a log line
// verbatim would let any caller of the callback listener write log records of
// arbitrary size. Byte-sliced first so a huge id is never fully copied.
func truncateWireID(s string) string {
	const maxBytes = 64
	if len(s) <= maxBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxBytes], "") + "…"
}

// truncateCallbackMessage bounds an untrusted free-text field of a callback
// before it is logged, published on the bus or persisted as an incident.
// The same reasoning as [truncateWireID] applies, with a limit generous
// enough to keep every real CCU error text intact. Byte-sliced first so a
// huge message is never fully copied.
func truncateCallbackMessage(s string) string {
	const maxBytes = 512
	if len(s) <= maxBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxBytes], "") + "…"
}

// Stop cancels all in-flight background goroutines and waits for them to
// finish. Safe to call multiple times. After it returns, no callback can
// start a new background goroutine on this handler.
func (h *CallbackHandlers) Stop() {
	h.stopMu.Lock()
	h.stopping = true
	h.stopMu.Unlock()
	h.cancel()
	h.wg.Wait()
}

// goBackground runs fn on the handler's WaitGroup and reports whether it
// started. It does not start after Stop has begun; the handler context is
// cancelled by then, so the work would abort on its first ctx check anyway.
// Callers that hold a resource for fn release it when the start is refused.
func (h *CallbackHandlers) goBackground(fn func()) bool {
	h.stopMu.Lock()
	defer h.stopMu.Unlock()
	if h.stopping {
		return false
	}
	h.wg.Go(fn)
	return true
}

// noteCallbackAndRoutePong refreshes the per-client callback-liveness
// timestamp and routes a PONG callback to the ping-pong tracker. It runs
// before [Event]'s device-existence guard so neither signal is lost for
// callbacks that do not map to a mirrored device.
//
// Every inbound callback — including a PONG and an event for a device we do
// not mirror — is proof the callback channel is alive. Stamping liveness here
// (not only on reconnect) is what stops IsCallbackAlive from going stale
// callbackFreshness (180 s) after each reconnect on a quiet CCU, which would
// otherwise make the check_connection watchdog reconnect in an endless loop.
// Mirrors the reference set_last_event_seen_for_interface call at the top of
// the event-coordinator's data_point_event flow.
//
// PONG arrives on the "CENTRAL" pseudo-address, which is not a mirrored
// device, so Event's device-existence guard would otherwise drop it and the
// tracker would never correlate the round-trip (pending piles to its cap and
// health stays degraded). Returns true when the event was a PONG and is fully
// handled.
//
// Because it runs ahead of that guard, the Clients registry is the only thing
// left that constrains the interface_id a PONG may name — and the registry is
// exactly the set of interfaces this central brought up. A PONG for anything
// else can carry no liveness signal for us, so it is dropped here instead of
// creating a per-interface event clock that is only released when the central
// is torn down. The callback listener takes no authentication and its
// source-IP allow-list is off by default, so without this gate any LAN peer
// could grow those maps until the daemon is out of memory.
func (h *CallbackHandlers) noteCallbackAndRoutePong(
	ctx context.Context, interfaceID, channelAddress, parameter string, value xmlrpc.Value,
) bool {
	var registered bool
	if h.unit != nil && h.unit.Clients != nil {
		if entry, ok := h.unit.Clients.Get(interfaceID); ok && entry != nil {
			registered = true
			if entry.Client != nil {
				entry.Client.NotifyCallback()
			}
		}
	}
	if parameter != string(hmenum.ParameterPong) {
		return false
	}
	if !registered {
		h.logger.Debug("callback.pong.unregistered_interface",
			slog.String("interface", truncateWireID(interfaceID)))
		return true
	}
	if h.unit != nil && h.unit.Events != nil {
		h.unit.Events.HandleRawEventNormalized(ctx, interfaceID, channelAddress, parameter, ParamValueFromWire(value))
	}
	return true
}

// Event routes a CCU value-change callback onto the matching data point.
// Missing devices/channels/parameters are silently ignored — the CCU
// occasionally emits events for entities we deliberately don't mirror.
//
// Combined parameters (`COMBINED_PARAMETER`, `LEVEL_COMBINED`) are
// decomposed into their constituent (LEVEL, LEVEL_2, LEVEL_SLATS)
// Updates before being routed — mirrors
// `add_combined_parameter` flow so the model layer always sees real
// parameters, never the wire shorthand.
//
// As a final step the event is also forwarded to the central's
// EventCoordinator. The coordinator owns the dynamic value cache and
// publishes [hmevent.DataPointValueChangedEvent] on the central bus
// without that publish the EventBridge (REST/WS/MQTT) sees nothing
// and downstream topics never appear at the broker.
func (h *CallbackHandlers) Event(ctx context.Context, interfaceID, channelAddress, parameter string, value xmlrpc.Value) error {
	interfaceID = h.canonicalInterfaceID(interfaceID)

	// Stamp liveness and route PONG before the device-existence guard below.
	// Returns true when the event was a PONG (fully handled here).
	if h.noteCallbackAndRoutePong(ctx, interfaceID, channelAddress, parameter, value) {
		return nil
	}

	deviceAddr := deviceAddressOf(channelAddress)
	dev, ok := h.unit.ModelRegistry.Get(deviceAddr)
	if !ok {
		return nil
	}
	ch := dev.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	if backends.IsCombinedParameter(parameter) {
		h.dispatchCombined(interfaceID, channelAddress, parameter, value)
		// Combined-parameter values are decomposed and dispatched on
		// the model side; the EventCoordinator sees nothing because
		// the wire never carries the constituent parameters as their
		// own events. Skip the bus publish for the combined value.
		return nil
	}
	// Status-pair handling: "<X>_STATUS" carries the MEASUREMENT STATUS of
	// "<X>" (NORMAL / UNKNOWN / OVERFLOW, wire value 0/1/2), not a value
	// echo. Record the status on the BASE data point via
	// UpdateStatusFromWire — regardless of whether a dedicated _STATUS DP
	// exists (it usually does; openccu-loom creates a DP for every wire
	// parameter, so the former dp==nil-only fallback almost never fired).
	// Routing the event into the base DP's OnWireValue wrote the STATUS
	// INDEX (usually 0) over the real measurement: every CCU burst then
	// published the true value AND a bogus 0 within ~1 ms, and whichever
	// landed last won (HA sensors oscillated between 19.6 and 0).
	if base, isPair := hmenum.Parameter(parameter).BasePair(); isPair {
		if baseDP := ch.Parameter(base); baseDP != nil {
			h.logger.Debug("callback.event.status_pair",
				slog.String("interface", interfaceID),
				slog.String("channel", channelAddress),
				slog.String("status_param", parameter),
				slog.String("base_param", string(base)))
			if su, ok := baseDP.(interface{ UpdateStatusFromWire(any) }); ok {
				su.UpdateStatusFromWire(xmlRPCValueToGo(value))
			}
		}
	}
	dp := ch.Parameter(hmenum.Parameter(parameter))
	if dp == nil {
		// A parameter with no data point is normally noise — an unknown name,
		// or one this build does not model — and dropping it is right.
		//
		// Two event families are the exception, and they are not edge cases:
		// the resolver deliberately creates no data point for an impulse
		// (SEQUENCE_OK) or a device error (ERROR*, SENSOR_ERROR*), because
		// they are events rather than state. Returning here dropped exactly
		// those before they reached the coordinator, so a reported fault
		// produced nothing anywhere — no device-trigger event, no WebSocket
		// broadcast, and no record on the channel's event group. Only a
		// keypress ever arrived, because a PRESS_* parameter is writable and
		// therefore does have a data point.
		h.forwardDataPointLessEvent(ctx, interfaceID, channelAddress, parameter, value)
		return nil
	}
	goValue := xmlRPCValueToGo(value)
	// CCU quirk: for a numeric (INTEGER/FLOAT) descriptor the CCU
	// sometimes ships an empty string instead of the numeric default.
	// That's an "absent value" sentinel, not a coerce failure — skip
	// the DP update + the self-reload retry silently. Observed live
	// on HmIP-BDT channel 3 SECTION during dim-program transitions:
	// descriptor TYPE=INTEGER, wire value "".
	if s, isString := goValue.(string); isString && s == "" {
		if pd, ok := dp.(interface{ ParameterData() hmproto.ParameterData }); ok {
			t := pd.ParameterData().Type
			if t == hmenum.ParameterTypeInteger || t == hmenum.ParameterTypeFloat {
				return nil
			}
		}
	}
	var coerced bool
	if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
		coerced = setter.OnWireValue(goValue)
		if !coerced {
			h.logger.Debug("callback.event.coerce_failed",
				slog.String("interface", interfaceID),
				slog.String("channel", channelAddress),
				slog.String("parameter", parameter),
				// Capture the decoded wire type + value so the exact
				// mismatch is identifiable (e.g. go_type=<nil> ⇒ a
				// NilValue "absent" push, go_type=string ⇒ a non-numeric
				// payload for a numeric descriptor).
				slog.String("go_type", fmt.Sprintf("%T", goValue)),
				slog.Any("value", goValue))
			// Self-reload: the wire value did not coerce — most commonly because the
			// descriptor's expected type and the CCU's serialised payload disagree
			// (e.g. CCU shipped a string for an integer field). A single direct
			// LoadValue refresh fetches the canonical value via getValue, which
			// returns the type the descriptor claims. Best-effort, fire-and-forget;
			// runs in a fresh goroutine so the callback dispatch never blocks on
			// network I/O.
			//
			// Skip the reload for write-only DPs (PRESS_*, ACTION, COMMAND
			// triggers): getValue on those returns `Unknown Parameter`
			// (code -5), trips the circuit breaker, and the value will
			// never become "canonical" anyway — the callback IS the only
			// observation point. Filter via the Read operations bit.
			if pd, ok := dp.(interface{ ParameterData() hmproto.ParameterData }); ok {
				if pd.ParameterData().Operations&hmenum.OperationsRead == 0 {
					return nil
				}
			}
			h.scheduleSelfReload(dev, channelAddress, parameter) //nolint:contextcheck // scheduleSelfReload has no ctx param; its goroutine uses h.ctx (handler lifetime) not the short-lived callback ctx
		}
	}
	// Clear the last_value_send tracker entry on a successful CCU echo.
	// Mirrors the reference event() flow which calls
	// last_value_send_tracker.remove_last_value_send(dpk, value) after
	// every confirmed CCU push. Without this the tracker keeps stale
	// in-flight entries until their TTL expires, which surfaces as
	// false-positive `HasInFlight` results for north-bound metrics that
	// query the tracker. Best-effort: a missing client entry or absent
	// tracker is a no-op (test fixtures, lifecycle edges).
	dpk := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
	if coerced && h.unit.Clients != nil {
		if entry, ok := h.unit.Clients.Get(interfaceID); ok && entry != nil && entry.Client != nil {
			entry.Client.CommandTracker().ClearForKey(dpk)
		}
	}
	// Forward to the EventCoordinator via the normalized variant so that
	// _STATUS suffix stripping and PONG-token correlation both run on the
	// live callback path. Without this call the model is updated silently
	// and no broker topic is ever published; PONG events would also never
	// reach the ping-pong tracker.
	if h.unit.Events != nil {
		h.unit.Events.HandleRawEventNormalized(ctx, interfaceID, channelAddress, parameter, ParamValueFromWire(value))
	}
	return nil
}

// dispatchCombined parses a combined-parameter wire value and fans
// the resulting (parameter, value) pairs onto the channel's
// individual data points. Unparseable payloads are silently dropped
// at DEBUG level — the model layer never observes a partial/garbled
// combined update.
func (h *CallbackHandlers) dispatchCombined(interfaceID, channelAddress, parameter string, value xmlrpc.Value) {
	raw, err := xmlrpc.AsString(value)
	if err != nil || raw == "" {
		h.logger.Debug("callback.event.combined.non_string",
			slog.String("interface", interfaceID),
			slog.String("channel", channelAddress),
			slog.String("parameter", parameter))
		return
	}
	parsed, parsedOK := backends.ParseCombinedParameter(parameter, raw)
	if !parsedOK {
		h.logger.Debug("callback.event.combined.unparseable",
			slog.String("interface", interfaceID),
			slog.String("channel", channelAddress),
			slog.String("parameter", parameter),
			slog.String("value", raw))
		return
	}
	deviceAddr := deviceAddressOf(channelAddress)
	dev, _ := h.unit.ModelRegistry.Get(deviceAddr)
	if dev == nil {
		return
	}
	ch := dev.Channel(channelAddress)
	if ch == nil {
		return
	}
	for subParam, subValue := range parsed {
		dp := ch.Parameter(hmenum.Parameter(subParam))
		if dp == nil {
			continue
		}
		setter, ok := dp.(interface{ OnWireValue(any) bool })
		if !ok {
			continue
		}
		if !setter.OnWireValue(subValue) {
			h.logger.Debug("callback.event.combined.coerce_failed",
				slog.String("interface", interfaceID),
				slog.String("channel", channelAddress),
				slog.String("parameter", subParam),
				slog.String("go_type", fmt.Sprintf("%T", subValue)),
				slog.Any("value", subValue))
		}
	}
}

// scheduleSelfReload kicks off a background LoadValue(direct=true) for the
// given (channel, parameter) tuple. Used when the inline OnWireValue coercion
// fails — the callback dispatch goroutine is not blocked, and a single
// canonical fetch via getValue resolves the type-mismatch in the next moment.
//
// The goroutine is registered with [h.wg] so [Stop] can wait for it. The
// [h.ctx] cancellation is honoured so a daemon shutdown aborts in-flight
// fetches promptly.
//
// Concurrency is bounded by [selfReloadSem]: at most [selfReloadConcurrency]
// goroutines may run simultaneously. A non-blocking try-acquire is used; if
// the semaphore is full the reload is dropped with a debug log. The
// UnobservedSweep reconciler or the next CCU push will fill in the gap.
//
// The reload is best-effort: a load failure leaves the DP in its previous
// (possibly empty) state, no further retries.
func (h *CallbackHandlers) scheduleSelfReload(d *device.Device, channelAddress, parameter string) {
	if d == nil || d.ValueLoader() == nil {
		return
	}
	// Non-blocking acquire: if the semaphore is full, drop this reload
	// and debug-log so the operator can see pressure without blocking.
	select {
	case h.selfReloadSem <- struct{}{}:
	default:
		h.logger.Debug("callback.event.self_reload_dropped",
			slog.String("channel", channelAddress),
			slog.String("parameter", parameter),
			slog.Int("cap", selfReloadConcurrency))
		return
	}
	dpk := hmtypes.DataPointKey{
		InterfaceID:    d.InterfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
	started := h.goBackground(func() { //nolint:contextcheck // background reload uses h.ctx, not the caller's ctx which may be short-lived
		defer func() { <-h.selfReloadSem }()
		ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
		defer cancel()
		if _, _, err := d.LoadValue(ctx, dpk, hmenum.CallSourceManualOrScheduled, true); err != nil {
			h.logger.Debug("callback.event.self_reload_failed",
				slog.String("channel", channelAddress),
				slog.String("parameter", parameter),
				slog.String("err", err.Error()))
		}
	})
	if !started {
		// The goroutine that would have released the permit never ran.
		<-h.selfReloadSem
	}
}

// NewDevices acknowledges a hot-plug announcement. It hands the incoming
// device descriptions to the hot-plug ingestor in the background so the full
// domain device (channels, data points, custom DPs, values) exists without
// waiting for a daemon restart. When deferred creation is configured they go
// to the DeviceCoordinator's pending-accept inbox instead, and no entity is
// created until the operator accepts them.
//
// The materialisation runs detached: the CCU blocks its event channel
// until this callback returns, while a full hydration round-trips the
// CCU several times per new channel. The ingestor dedups against the
// ModelRegistry, so the full-inventory announcement the CCU sends after
// every reconnect (our listDevices reply is deliberately empty) no-ops.
// HandleNewDevices — and with it the DeviceCreatedEvent — runs after the
// ingest so north-bound subscribers (MQTT discovery, Matter reassembly)
// resolve the device in the model when the event fires.
func (h *CallbackHandlers) NewDevices(_ context.Context, interfaceID string, descs xmlrpc.ArrayValue) error {
	interfaceID = h.canonicalInterfaceID(interfaceID)
	h.logger.Info("callback.new_devices",
		slog.String("interface", interfaceID),
		slog.Int("count", len(descs)))
	if h.unit.Devices == nil || len(descs) == 0 {
		return nil
	}
	raw := make([]any, len(descs))
	for i, v := range descs {
		raw[i] = xmlRPCValueToGo(v)
	}
	iface := hmtypes.ParseWireInterfaceID(interfaceID)
	descriptions := backends.ParseDeviceDescriptions(raw)
	if len(descriptions) == 0 {
		return nil
	}
	if h.delayNewDeviceCreation {
		// Defer entity creation: the device waits on the inbox surface
		// until an operator accepts it. The inbox is only
		// filled here because the accept flow is the sole path that
		// empties it — with immediate creation the descriptions go to the
		// hot-plug ingestor below, and a stored copy would never be read
		// again while the CCU keeps re-announcing its whole inventory on
		// every reconnect.
		h.unit.Devices.StoreDelayedDeviceDescriptions(iface, descriptions)
		// Publish the queue on the operator's inbox surface. Without this
		// the deferred device is invisible: it exists on the CCU, has no
		// data points here, and nothing tells anyone it is waiting.
		PublishPendingDevices(h.unit)
		h.logger.Info("callback.new_devices.deferred",
			slog.String("interface", interfaceID),
			slog.Int("count", len(descriptions)))
		return nil
	}
	h.goBackground(func() { //nolint:contextcheck // background ingest uses h.ctx — the callback ctx dies when the RPC response is written
		bgCtx, cancel := context.WithTimeout(h.ctx, newDevicesIngestTimeout)
		defer cancel()
		if err := h.unit.IngestDevices(bgCtx, interfaceID, descriptions); err != nil {
			h.logger.Warn("callback.new_devices.ingest_failed",
				slog.String("interface", interfaceID),
				slog.String("err", err.Error()))
		}
		// Registry + description-cache bookkeeping and the (at-least-once)
		// DeviceCreatedEvent — after materialisation, see doc comment.
		h.unit.Devices.HandleNewDevices(bgCtx, iface, descriptions)
	})
	return nil
}

// newDevicesIngestTimeout bounds one background hot-plug materialisation.
// A single device needs a handful of paramset reads plus one ReGa call;
// the generous bound only reaps a hung CCU connection.
const newDevicesIngestTimeout = 2 * time.Minute

// DeleteDevices drops the listed devices from the model registry so the
// REST / MQTT views stop advertising them, and from the description /
// paramset / device registries so nothing survives the deletion.
//
// The registry half is what makes this the same operation the REST unpair
// performs (see [DeviceAdminDomain.UnpairDevice]). Removing the model device
// alone left the descriptions and paramsets of every channel behind, which the
// persistence sinks keep in SQLite: the next boot rehydrated them and
// materialised a device the CCU no longer reports, complete with its creation
// event.
func (h *CallbackHandlers) DeleteDevices(_ context.Context, interfaceID string, addresses []string) error {
	interfaceID = h.canonicalInterfaceID(interfaceID)
	h.logger.Info("callback.delete_devices",
		slog.String("interface", interfaceID),
		slog.Int("count", len(addresses)))
	if h.unit == nil {
		return nil
	}
	for _, addr := range addresses {
		h.dropDevice(hmtypes.ParseWireInterfaceID(interfaceID), addr)
	}
	return nil
}

// dropDevice removes one address from the model and from every registry that
// mirrors it, including the persisted rows the registries' sinks own.
//
// The device's own stamped interface id wins over the callback's when the
// device is known: the registries are keyed by the canonical `<central>-<iface>`
// wire id the ingest stamped, and a mismatch there deletes nothing.
func (h *CallbackHandlers) dropDevice(iface hmtypes.WireInterfaceID, address string) {
	// Snapshot the channels before RemoveDevice tears them down — the
	// description and paramset registries carry one entry per CHANNEL, not one
	// per device, so the device address alone matches only the root entry.
	var channels []*device.Channel
	if dev, ok := h.unit.ModelRegistry.Get(address); ok && dev != nil {
		channels = dev.Channels()
		if dev.InterfaceID != "" {
			iface = hmtypes.ParseWireInterfaceID(dev.InterfaceID)
		}
	}
	h.unit.RemoveDevice(address)
	h.unit.DeviceRegistry.Remove(iface, address)
	h.unit.DescRegistry.Delete(iface, address)
	h.unit.ParamsetReg.DeleteChannel(iface, address)
	for _, ch := range channels {
		h.unit.DescRegistry.Delete(iface, ch.Address)
		h.unit.ParamsetReg.DeleteChannel(iface, ch.Address)
	}
}

// UpdateDevice handles a firmware-update notification (hint=0) or a link
// partner change (hint=1) from the CCU. For hint=0 the firmware cache is
// invalidated and a background refresh is scheduled to pull fresh device
// descriptions; the paramset registry is only invalidated, not re-fetched —
// a caller that needs a fresh MASTER schema for the affected channels still
// has to run [coordinators.DeviceCoordinator.ReloadChannelConfig]. hint=1 is
// a no-op beyond logging — link-peer changes are small and reconciled on the
// next scheduled sweep.
func (h *CallbackHandlers) UpdateDevice(ctx context.Context, interfaceID, address string, hint int) error {
	interfaceID = h.canonicalInterfaceID(interfaceID)
	h.logger.Info("callback.update_device",
		slog.String("interface", interfaceID),
		slog.String("address", address),
		slog.Int("hint", hint))
	const hintFirmware = 0
	if hint != hintFirmware || h.unit == nil || h.unit.Devices == nil {
		return nil
	}
	iface := hmtypes.ParseWireInterfaceID(interfaceID)
	h.unit.Devices.InvalidateFirmwareCache(iface, address)
	if h.writer == nil {
		return nil
	}
	b, ok := h.writer.Backend(h.unit.Name(), hmtypes.ParseWireInterfaceID(interfaceID))
	if !ok {
		h.logger.Warn("callback.update_device.no_backend",
			slog.String("interface", interfaceID))
		return nil
	}
	fetcher := &callbackDescFetcher{ops: b}
	h.goBackground(func() { //nolint:contextcheck // background refresh uses h.ctx, not the caller's ctx which may be short-lived
		bgCtx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		if err := h.unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(bgCtx, fetcher, iface); err != nil {
			h.logger.Warn("callback.update_device.refresh_failed",
				slog.String("interface", interfaceID),
				slog.String("address", address),
				slog.String("err", err.Error()))
		}
	})
	return nil
}

// ReplaceDevice evicts the old device and ingests the replacement by
// delegating to [coordinators.DeviceCoordinator.ReplaceDevice]. When no
// writer is wired the call degrades to eviction-only (no fresh fetch).
func (h *CallbackHandlers) ReplaceDevice(ctx context.Context, interfaceID, oldAddress, newAddress string) error {
	interfaceID = h.canonicalInterfaceID(interfaceID)
	h.logger.Info("callback.replace_device",
		slog.String("interface", interfaceID),
		slog.String("old", oldAddress),
		slog.String("new", newAddress))
	if h.unit == nil || h.unit.Devices == nil || h.writer == nil {
		return nil
	}
	b, ok := h.writer.Backend(h.unit.Name(), hmtypes.ParseWireInterfaceID(interfaceID))
	if !ok {
		h.logger.Warn("callback.replace_device.no_backend",
			slog.String("interface", interfaceID))
		return nil
	}
	fetcher := &callbackDescFetcher{ops: b}
	iface := hmtypes.ParseWireInterfaceID(interfaceID)
	h.goBackground(func() { //nolint:contextcheck // background refresh uses h.ctx, not the caller's ctx which may be short-lived
		bgCtx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		if err := h.unit.Devices.ReplaceDevice(bgCtx, fetcher, iface, oldAddress, newAddress); err != nil {
			h.logger.Warn("callback.replace_device.failed",
				slog.String("interface", interfaceID),
				slog.String("old", oldAddress),
				slog.String("new", newAddress),
				slog.String("err", err.Error()))
		}
	})
	return nil
}

// ReaddedDevice handles devices that re-pair via install mode. The cache
// is invalidated for each address and fresh descriptions are fetched in a
// background goroutine.
func (h *CallbackHandlers) ReaddedDevice(_ context.Context, interfaceID string, addresses []string) error {
	interfaceID = h.canonicalInterfaceID(interfaceID)
	h.logger.Info("callback.readded_device",
		slog.String("interface", interfaceID),
		slog.Int("count", len(addresses)))
	if len(addresses) == 0 || h.unit == nil || h.unit.Devices == nil || h.writer == nil {
		return nil
	}
	b, ok := h.writer.Backend(h.unit.Name(), hmtypes.ParseWireInterfaceID(interfaceID))
	if !ok {
		h.logger.Warn("callback.readded_device.no_backend",
			slog.String("interface", interfaceID))
		return nil
	}
	fetcher := &callbackDescFetcher{ops: b}
	iface := hmtypes.ParseWireInterfaceID(interfaceID)
	h.goBackground(func() { //nolint:contextcheck // background refresh uses h.ctx, not the caller's ctx which may be short-lived
		bgCtx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		for _, addr := range addresses {
			h.unit.Devices.InvalidateFirmwareCache(iface, addr)
		}
		// One listDevices covers every re-paired address on this interface —
		// the refresh is address-independent (it re-pulls the whole interface
		// inventory), so calling it once per address repeated the same
		// full-interface fetch K times inside the shared 30 s budget.
		if err := h.unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(bgCtx, fetcher, iface); err != nil {
			h.logger.Warn("callback.readded_device.refresh_failed",
				slog.String("interface", interfaceID),
				slog.Int("count", len(addresses)),
				slog.String("err", err.Error()))
		}
	})
	return nil
}

// callbackDescFetcher wraps a [backends.Operations] as a
// [coordinators.DeviceDescriptionFetcher] for use in callback-triggered
// refresh goroutines.
type callbackDescFetcher struct {
	ops backends.Operations
}

// ListDevices implements [coordinators.DeviceDescriptionFetcher].
func (f *callbackDescFetcher) ListDevices(ctx context.Context, _ hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error) {
	return f.ops.ListDevices(ctx)
}

// ensure callbackDescFetcher satisfies the interface at compile time.
var _ coordinators.DeviceDescriptionFetcher = (*callbackDescFetcher)(nil)

// ListDevices is the CCU's request for the daemon's view of devices.
// Returning an empty array means "you (CCU) are authoritative" which
// is exactly what we want — the CCU will then announce everything via
// NewDevices on the next reconnect.
func (h *CallbackHandlers) ListDevices(_ context.Context, interfaceID string) (xmlrpc.ArrayValue, error) {
	interfaceID = h.canonicalInterfaceID(interfaceID)
	h.logger.Debug("callback.list_devices",
		slog.String("interface", interfaceID))
	return xmlrpc.ArrayValue{}, nil
}

// Error logs the CCU-reported wire failure, forwards it to the central event
// bus as a [hmevent.SystemStatusChangedEvent], and records a
// CALLBACK_TIMEOUT incident when an incident recorder is wired.
//
// Always returns nil — the CCU does not interpret a non-nil response and a
// failed local handler must not break the callback channel.
func (h *CallbackHandlers) Error(ctx context.Context, interfaceID string, errorCode int, msg string) error {
	interfaceID = truncateWireID(h.canonicalInterfaceID(interfaceID))
	// Both strings arrive from the callback listener, which takes no
	// authentication and whose source-IP allow-list is off by default. Bound
	// them before they reach the log, the bus and the incident store — the
	// request body limit alone allows a 10 MiB message per call.
	msg = truncateCallbackMessage(msg)
	h.logger.Warn("callback.error",
		slog.String("interface", interfaceID),
		slog.Int("error_code", errorCode),
		slog.String("msg", msg))
	if h.unit == nil {
		return nil
	}
	centralName := h.unit.Name()
	bus := h.unit.EventBus
	if bus != nil {
		events.Publish(bus, hmevent.SystemStatusChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: centralName,
			Component:   "rpc-server:" + interfaceID,
			Healthy:     false,
			Reason:      msg,
			InterfaceID: interfaceID,
			ErrorCode:   errorCode,
		})
	}
	if rec := h.incidentRecorder(); rec != nil {
		_ = rec.RecordIncident(ctx, reliability.IncidentRecord{
			CentralName: centralName,
			InterfaceID: interfaceID,
			Type:        hmenum.IncidentTypeCallbackTimeout,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     fmt.Sprintf("callback error (code %d): %s", errorCode, msg),
		})
	}
	return nil
}

// Compile-time interface check.
var _ rpcserver.Handlers = (*CallbackHandlers)(nil)

// forwardDataPointLessEvent publishes the device-trigger event for a
// parameter that has no data point but does classify as one of the CCU's
// event families.
//
// It publishes the trigger alone rather than routing through the
// coordinator's full raw-event path on purpose. That path also emits a
// value-change event, which the north-bound planes turn into a per-parameter
// state topic — and a parameter the resolver refused to model as a data
// point should not acquire one through the back door. The trigger is the
// whole content of these events.
//
// A parameter that classifies as nothing is dropped, exactly as before.
func (h *CallbackHandlers) forwardDataPointLessEvent(
	ctx context.Context,
	interfaceID, channelAddress, parameter string,
	value xmlrpc.Value,
) {
	if h.unit == nil || h.unit.Events == nil {
		return
	}
	kind, isEvent := modevent.Classify(hmenum.Parameter(parameter))
	if !isEvent {
		return
	}
	deviceAddress, channelNo := deviceAddrAndChannel(channelAddress)
	h.unit.Events.PublishDeviceTriggerEvent(
		ctx,
		interfaceID, deviceAddress, channelNo,
		hmenum.DeviceTriggerEventType(kind), parameter, ParamValueFromWire(value),
	)
}
