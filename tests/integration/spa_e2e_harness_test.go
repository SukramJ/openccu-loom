// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

// SPA-E2E harness — exercises the daemon's CDP operation surface
// against a godevccu virtual CCU without spinning up a separate
// daemon process. The full Central → Pipeline → CustomDPDispatcher
// stack is built in-process; tests drive it through the same
// interfaces the REST handler does. Captures the wire-side answer
// from godevccu via getValue / getParamset so each plan can
// assert both the daemon path (no 502) and the device-state
// outcome.
//
// See `notes/contributor/spa-e2e-against-godevccu.md` for the
// architecture overview.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// spaHarness holds the in-process daemon stack used by the SPA-E2E
// plans. One harness is built per test (NewSPAHarness); each plan
// re-uses the same stack so multi-step plans (set_temperature →
// set_mode → enable_boost) share the central state.
type spaHarness struct {
	t          *testing.T
	mock       *mockCCU
	central    *central.Unit
	dispatcher *adapter.CustomDPDispatcher
	xmlClient  *xmlrpc.Client
	caller     *xmlrpcBackendCaller
	logger     *slog.Logger

	// Event capture — subscribed to the central's EventBus during
	// newSPAHarness. Plans use expectEvents / drainEvents to assert
	// against the wire-event stream the SPA's WS pump would publish.
	eventMu sync.Mutex
	events  []hmevent.DataPointValueChangedEvent
	unsubFn func()

	// setCallMu protects setCalls. setCalls records every
	// successful SetValue / PutParamset against godevccu via the
	// OnSetValue hook — used by plans that target write-only
	// parameters where getValue read-back is not meaningful
	// (COMBINED_PARAMETER, LEVEL_COMBINED, DOOR_COMMAND, ACTION
	// parameters in general).
	setCallMu sync.Mutex
	setCalls  []spaSetCall
}

// spaSetCall records a single OnSetValue invocation. The harness
// keeps every call so plans can scan for the most recent write to a
// given (address, parameter) tuple regardless of how many steps ran
// before.
type spaSetCall struct {
	address string
	param   string
	value   any
}

// newSPAHarness builds the in-process daemon stack against a fresh
// godevccu instance loaded with the named models.
func newSPAHarness(t *testing.T, models []string) *spaHarness {
	t.Helper()
	var h *spaHarness
	onSet := func(address, valueKey string, value any) {
		if h == nil {
			return
		}
		h.setCallMu.Lock()
		h.setCalls = append(h.setCalls, spaSetCall{address: address, param: valueKey, value: value})
		h.setCallMu.Unlock()
	}
	mock := startMockCCUWithOptions(t, models, onSet)

	xmlClient := newXMLRPCClient(t, mock.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "ccu-e2e"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	valueWriter := backendValueWriter{backend: backend}

	pipeline := adapter.NewDevicePipeline(c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, valueWriter, nil, logger); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if c.ModelRegistry.Len() == 0 {
		t.Fatal("godevccu returned no devices — fleet loading failed")
	}

	// Make the central discoverable to the CDP dispatcher via the
	// usual *central.Registry indirection. Daemon production wires
	// many centrals in; for the harness one is enough.
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	disp := adapter.NewCustomDPDispatcher(reg)

	h = &spaHarness{
		t:          t,
		mock:       mock,
		central:    c,
		dispatcher: disp,
		xmlClient:  xmlClient,
		caller:     caller,
		logger:     logger,
	}
	h.unsubFn = events.Subscribe(c.EventBus, func(e hmevent.DataPointValueChangedEvent) {
		h.eventMu.Lock()
		h.events = append(h.events, e)
		h.eventMu.Unlock()
	})
	t.Cleanup(func() {
		if h.unsubFn != nil {
			h.unsubFn()
		}
	})
	return h
}

// resetEvents drops the captured event buffer so plan assertions
// against `events` are scoped to the most recent action only.
func (h *spaHarness) resetEvents() {
	h.eventMu.Lock()
	h.events = h.events[:0]
	h.eventMu.Unlock()
	h.setCallMu.Lock()
	h.setCalls = h.setCalls[:0]
	h.setCallMu.Unlock()
}

// lastSetValue returns the most recent value that the daemon wrote
// to (channelAddress, parameter) via SetValue or as a member of a
// PutParamset, plus ok=true when one was recorded. Empty buffer →
// (nil, false).
func (h *spaHarness) lastSetValue(channelAddress string, parameter hmenum.Parameter) (any, bool) {
	h.setCallMu.Lock()
	defer h.setCallMu.Unlock()
	for i := len(h.setCalls) - 1; i >= 0; i-- {
		c := h.setCalls[i]
		if c.address == channelAddress && c.param == string(parameter) {
			return c.value, true
		}
	}
	return nil, false
}

// lastSetValueAnyChannel returns the most recent captured setValue for
// parameter on ANY channel of the device. Channel-group custom DPs (e.g.
// the HmIP-BSL fixed-color light) write some parameters to an offset
// sub-channel rather than the DP's primary key channel, so a channel-scoped
// lookup cannot see them; this matches on the parameter alone.
func (h *spaHarness) lastSetValueAnyChannel(parameter hmenum.Parameter) (any, bool) {
	h.setCallMu.Lock()
	defer h.setCallMu.Unlock()
	for i := len(h.setCalls) - 1; i >= 0; i-- {
		c := h.setCalls[i]
		if c.param == string(parameter) {
			return c.value, true
		}
	}
	return nil, false
}

// drainEvents waits up to `wait` for at least one event matching
// `match`, then returns a snapshot of the captured buffer. godevccu
// dispatches its callbacks synchronously, so events normally land
// inline with the wire write — the wait covers async event paths
// (deviceresponses + the OnSetValue hook).
func (h *spaHarness) drainEvents(wait time.Duration, match func(hmevent.DataPointValueChangedEvent) bool) []hmevent.DataPointValueChangedEvent {
	deadline := time.Now().Add(wait)
	for {
		h.eventMu.Lock()
		snap := make([]hmevent.DataPointValueChangedEvent, len(h.events))
		copy(snap, h.events)
		h.eventMu.Unlock()
		if match == nil {
			return snap
		}
		for _, e := range snap {
			if match(e) {
				return snap
			}
		}
		if time.Now().After(deadline) {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// findDevice looks up the first device whose model matches `model`.
// Fails the test when no such device exists.
func (h *spaHarness) findDevice(model string) *device.Device {
	h.t.Helper()
	for _, d := range h.central.ModelRegistry.List() {
		if d.Model == model {
			return d
		}
	}
	h.t.Fatalf("device with model %q not present in central; loaded: %s", model, h.loadedModels())
	return nil
}

// findCustomDP returns (cdp, channelNo) for the first attached CDP
// on the given device whose channel.Number matches `channelNo`.
// Fails the test when not found.
func (h *spaHarness) findCustomDP(model string, channelNo int) (device.AttachableDataPoint, *device.Channel) {
	h.t.Helper()
	d := h.findDevice(model)
	for _, ch := range d.Channels() {
		if ch.Number != channelNo {
			continue
		}
		dp := ch.CustomDataPoint()
		if dp == nil {
			h.t.Fatalf("device %s channel %d has no attached CustomDataPoint", d.Address, channelNo)
		}
		return dp, ch
	}
	h.t.Fatalf("device %s channel %d not found", d.Address, channelNo)
	return nil, nil
}

// invoke runs an operation through the CDP dispatcher — the same
// path the REST handler `InvokeCustomDataPoint` takes.
func (h *spaHarness) invoke(dp device.AttachableDataPoint, op string, params map[string]any) error {
	h.t.Helper()
	chAddr := dp.DataPointKey().ChannelAddress
	deviceAddr, _ := deviceAddrAndChannelHarness(chAddr)
	return h.dispatcher.InvokeCustomDP(
		context.Background(),
		deviceAddr,
		dp.DataPointKey().Parameter,
		op,
		params,
		hmenum.CommandPriorityHigh,
		"spa-e2e",
	)
}

// readWireValue queries godevccu directly via XML-RPC getValue to
// confirm the wire-side state matches the expectation.
func (h *spaHarness) readWireValue(channelAddress string, parameter hmenum.Parameter) any {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addr, _ := goToXMLRPCValue(channelAddress)
	param, _ := goToXMLRPCValue(string(parameter))
	v, err := h.xmlClient.Call(ctx, "getValue", []xmlrpc.Value{addr, param})
	if err != nil {
		h.t.Fatalf("godevccu getValue %s/%s: %v", channelAddress, parameter, err)
	}
	return xmlRPCValueToGo(v)
}

// loadedModels formats the loaded model list for failure messages.
func (h *spaHarness) loadedModels() string {
	seen := map[string]struct{}{}
	for _, d := range h.central.ModelRegistry.List() {
		seen[d.Model] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	return fmt.Sprintf("%v", out)
}

// kindOf is a convenience shortcut used by plan assertions.
func (h *spaHarness) kindOf(dp device.AttachableDataPoint) string {
	return cdpkind.Of(dp)
}

// backendValueWriter satisfies adapter.ValueWriter and routes
// single-DP writes back through the same CcuBackend godevccu owns.
// Production wires this through the InterfaceClient stack; for the
// harness the backend hop is enough.
type backendValueWriter struct {
	backend *backends.CcuBackend
}

func (w backendValueWriter) SetValue(
	ctx context.Context,
	_ string, // central name (production routing dimension, irrelevant here)
	_ string, // interface id (CcuBackend.SetValue ignores it)
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	return w.backend.SetValue(ctx, channelAddress, parameter, value, priority, hmenum.CommandRxModeUnset)
}

// deviceAddrAndChannelHarness splits "0001ABCD:4" into the device
// address and channel number. Mirrors handler.deviceAddrAndChannel
// but local-only to keep test code self-contained.
func deviceAddrAndChannelHarness(channelAddress string) (string, int) {
	for i := len(channelAddress) - 1; i >= 0; i-- {
		if channelAddress[i] == ':' {
			n := 0
			for j := i + 1; j < len(channelAddress); j++ {
				if channelAddress[j] < '0' || channelAddress[j] > '9' {
					return channelAddress[:i], 0
				}
				n = n*10 + int(channelAddress[j]-'0')
			}
			return channelAddress[:i], n
		}
	}
	return channelAddress, 0
}
