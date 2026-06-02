// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"log/slog"
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
	// Stop() blocks until all goroutines have returned.
	wg sync.WaitGroup
	// ctx / cancel control background tasks; Stop() cancels this context
	// so long-running tasks (e.g. reload fetches) can abort promptly.
	ctx    context.Context
	cancel context.CancelFunc
	// writer is the optional south-bound ValueWriter used to resolve
	// per-interface backends for device refresh operations (UpdateDevice,
	// ReplaceDevice, ReaddedDevice). When nil, those callbacks degrade to
	// cache-invalidation only.
	writer *clientpkg.ValueWriter
}

// NewCallbackHandlers wires the adapter for c.
func NewCallbackHandlers(u *central.Unit, logger *slog.Logger) *CallbackHandlers {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CallbackHandlers{unit: u, logger: logger, ctx: ctx, cancel: cancel}
}

// SetWriter wires the south-bound ValueWriter so UpdateDevice, ReplaceDevice,
// and ReaddedDevice can resolve per-interface backends for fresh description
// fetches.
func (h *CallbackHandlers) SetWriter(w *clientpkg.ValueWriter) {
	h.writer = w
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

// Stop cancels all in-flight background goroutines and waits for them to
// finish. Safe to call multiple times.
func (h *CallbackHandlers) Stop() {
	h.cancel()
	h.wg.Wait()
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
	dp := ch.Parameter(hmenum.Parameter(parameter))
	if dp == nil {
		// Status-pair fallback: a CCU echo for "<X>_STATUS" is the canonical
		// confirmation for an optimistic write on "<X>". When no dedicated _STATUS
		// data point is registered, route the event to the source data point so its
		// tracker can confirm or mismatch.
		if base, isPair := hmenum.Parameter(parameter).BasePair(); isPair {
			if dp = ch.Parameter(base); dp != nil {
				h.logger.Debug("callback.event.status_pair",
					slog.String("interface", interfaceID),
					slog.String("channel", channelAddress),
					slog.String("status_param", parameter),
					slog.String("base_param", string(base)))
			}
		}
	}
	if dp == nil {
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
			h.scheduleSelfReload(dev, channelAddress, parameter)
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
		h.unit.Events.HandleRawEventNormalized(ctx, interfaceID, channelAddress, parameter, value)
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
// The reload is best-effort: a load failure leaves the DP in its previous
// (possibly empty) state, no further retries. The reconciler's
// [UnobservedSweep] eventually picks up persistently unobserved DPs anyway.
func (h *CallbackHandlers) scheduleSelfReload(d *device.Device, channelAddress, parameter string) {
	if d == nil || d.ValueLoader() == nil {
		return
	}
	dpk := hmtypes.DataPointKey{
		InterfaceID:    d.InterfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
		defer cancel()
		if _, _, err := d.LoadValue(ctx, dpk, hmenum.CallSourceManualOrScheduled, true); err != nil {
			h.logger.Debug("callback.event.self_reload_failed",
				slog.String("channel", channelAddress),
				slog.String("parameter", parameter),
				slog.String("err", err.Error()))
		}
	}()
}

// NewDevices acknowledges a hot-plug announcement. It stores the incoming
// device descriptions in the DeviceCoordinator's delayed-inbox for later
// manual acceptance and immediately ingests them via HandleNewDevices so
// that the DeviceRegistry and ModelRegistry are updated without waiting
// for the next reconnect cycle.
func (h *CallbackHandlers) NewDevices(ctx context.Context, interfaceID string, descs xmlrpc.ArrayValue) error {
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
	iface := hmenum.Interface(interfaceID)
	descriptions := backends.ParseDeviceDescriptions(raw)
	if len(descriptions) == 0 {
		return nil
	}
	// Store for deferred manual acceptance (operator inbox flow).
	h.unit.Devices.StoreDelayedDeviceDescriptions(iface, descriptions)
	// Immediately ingest so the device is reachable via the REST / MQTT
	// surfaces without requiring a daemon restart or reconnect.
	h.unit.Devices.HandleNewDevices(ctx, iface, descriptions)
	return nil
}

// DeleteDevices drops the listed devices from the model registry so
// the REST / MQTT views stop advertising them.
func (h *CallbackHandlers) DeleteDevices(_ context.Context, interfaceID string, addresses []string) error {
	h.logger.Info("callback.delete_devices",
		slog.String("interface", interfaceID),
		slog.Int("count", len(addresses)))
	for _, addr := range addresses {
		h.unit.RemoveDevice(addr)
	}
	return nil
}

// UpdateDevice handles a firmware-update notification (hint=0) or a link
// partner change (hint=1) from the CCU. For hint=0 the firmware cache is
// invalidated and a background refresh is scheduled to pull fresh device and
// paramset descriptions. hint=1 is a no-op beyond logging — link-peer
// changes are small and reconciled on the next scheduled sweep.
func (h *CallbackHandlers) UpdateDevice(ctx context.Context, interfaceID, address string, hint int) error {
	h.logger.Info("callback.update_device",
		slog.String("interface", interfaceID),
		slog.String("address", address),
		slog.Int("hint", hint))
	const hintFirmware = 0
	if hint != hintFirmware || h.unit == nil || h.unit.Devices == nil {
		return nil
	}
	iface := hmenum.Interface(interfaceID)
	h.unit.Devices.InvalidateFirmwareCache(iface, address)
	if h.writer == nil {
		return nil
	}
	b, ok := h.writer.Backend(h.unit.Name(), interfaceID)
	if !ok {
		h.logger.Warn("callback.update_device.no_backend",
			slog.String("interface", interfaceID))
		return nil
	}
	fetcher := &callbackDescFetcher{ops: b}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		bgCtx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		if err := h.unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(bgCtx, fetcher, iface); err != nil {
			h.logger.Warn("callback.update_device.refresh_failed",
				slog.String("interface", interfaceID),
				slog.String("address", address),
				slog.String("err", err.Error()))
		}
	}()
	return nil
}

// ReplaceDevice evicts the old device and ingests the replacement by
// delegating to [coordinators.DeviceCoordinator.ReplaceDevice]. When no
// writer is wired the call degrades to eviction-only (no fresh fetch).
func (h *CallbackHandlers) ReplaceDevice(ctx context.Context, interfaceID, oldAddress, newAddress string) error {
	h.logger.Info("callback.replace_device",
		slog.String("interface", interfaceID),
		slog.String("old", oldAddress),
		slog.String("new", newAddress))
	if h.unit == nil || h.unit.Devices == nil || h.writer == nil {
		return nil
	}
	b, ok := h.writer.Backend(h.unit.Name(), interfaceID)
	if !ok {
		h.logger.Warn("callback.replace_device.no_backend",
			slog.String("interface", interfaceID))
		return nil
	}
	fetcher := &callbackDescFetcher{ops: b}
	iface := hmenum.Interface(interfaceID)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		bgCtx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		if err := h.unit.Devices.ReplaceDevice(bgCtx, fetcher, iface, oldAddress, newAddress); err != nil {
			h.logger.Warn("callback.replace_device.failed",
				slog.String("interface", interfaceID),
				slog.String("old", oldAddress),
				slog.String("new", newAddress),
				slog.String("err", err.Error()))
		}
	}()
	return nil
}

// ReaddedDevice handles devices that re-pair via install mode. The cache
// is invalidated for each address and fresh descriptions are fetched in a
// background goroutine.
func (h *CallbackHandlers) ReaddedDevice(_ context.Context, interfaceID string, addresses []string) error {
	h.logger.Info("callback.readded_device",
		slog.String("interface", interfaceID),
		slog.Int("count", len(addresses)))
	if len(addresses) == 0 || h.unit == nil || h.unit.Devices == nil || h.writer == nil {
		return nil
	}
	b, ok := h.writer.Backend(h.unit.Name(), interfaceID)
	if !ok {
		h.logger.Warn("callback.readded_device.no_backend",
			slog.String("interface", interfaceID))
		return nil
	}
	fetcher := &callbackDescFetcher{ops: b}
	iface := hmenum.Interface(interfaceID)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		bgCtx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		for _, addr := range addresses {
			h.unit.Devices.InvalidateFirmwareCache(iface, addr)
			if err := h.unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(bgCtx, fetcher, iface); err != nil {
				h.logger.Warn("callback.readded_device.refresh_failed",
					slog.String("interface", interfaceID),
					slog.String("address", addr),
					slog.String("err", err.Error()))
			}
		}
	}()
	return nil
}

// callbackDescFetcher wraps a [backends.Operations] as a
// [coordinators.DeviceDescriptionFetcher] for use in callback-triggered
// refresh goroutines.
type callbackDescFetcher struct {
	ops backends.Operations
}

// ListDevices implements [coordinators.DeviceDescriptionFetcher].
func (f *callbackDescFetcher) ListDevices(ctx context.Context, _ hmenum.Interface) ([]hmproto.DeviceDescription, error) {
	return f.ops.ListDevices(ctx)
}

// ensure callbackDescFetcher satisfies the interface at compile time.
var _ coordinators.DeviceDescriptionFetcher = (*callbackDescFetcher)(nil)

// ListDevices is the CCU's request for the daemon's view of devices.
// Returning an empty array means "you (CCU) are authoritative" which
// is exactly what we want — the CCU will then announce everything via
// NewDevices on the next reconnect.
func (h *CallbackHandlers) ListDevices(_ context.Context, interfaceID string) (xmlrpc.ArrayValue, error) {
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
func (h *CallbackHandlers) Error(_ context.Context, interfaceID string, errorCode int, msg string) error {
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
		_ = rec.RecordIncident(context.Background(), reliability.IncidentRecord{
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
