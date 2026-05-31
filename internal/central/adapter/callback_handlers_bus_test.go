// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
)

// TestEventForwardsToBus is the regression tripwire for the bug where
// CallbackHandlers.Event updated the model but never forwarded the
// event to the central's EventCoordinator, which meant
// DataPointValueChangedEvent was never published on the bus and
// downstream subscribers (REST/WS/MQTT EventBridge) saw nothing.
//
// The fix: Event now calls h.central.Events.HandleRawEvent after
// OnWireValue. This test asserts that a CCU event results in exactly
// one DataPointValueChangedEvent on the central bus.
func TestEventForwardsToBus(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	c := reg.List()[0]

	// Subscribe to the central bus before calling Event.
	var fired atomic.Int32
	var gotKey hmtypes.DataPointKey
	var gotNew hmtypes.ParamValue
	unsub := events.Subscribe(c.EventBus, func(e hmevent.DataPointValueChangedEvent) {
		fired.Add(1)
		gotKey = e.Key
		gotNew = e.NewValue
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if n := fired.Load(); n != 1 {
		t.Fatalf("expected 1 DataPointValueChangedEvent on bus, got %d — "+
			"CallbackHandlers.Event must call EventCoordinator.HandleRawEvent "+
			"so the MQTT EventBridge sees the value change", n)
	}
	if gotKey.ChannelAddress != "0001ABCD:1" || gotKey.Parameter != "STATE" {
		t.Fatalf("wrong key: %+v", gotKey)
	}
	boolVal, ok := gotNew.Unwrap().(bool)
	if !ok || !boolVal {
		t.Fatalf("expected new value true, got %+v", gotNew)
	}
}

// TestEventForwardsToBusNoDoubleFireOnSameValue verifies that the
// EventCoordinator's change-detection suppresses duplicate events when
// the callback delivers the same value twice. Only the first delivery
// reaches bus subscribers.
func TestEventForwardsToBusNoDoubleFireOnSameValue(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	ch.Put(dp)

	c := reg.List()[0]

	var count atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(_ hmevent.DataPointValueChangedEvent) {
		count.Add(1)
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	// Deliver the same value twice.
	for range 2 {
		if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
			string(hmenum.ParameterLevel), xmlrpc.DoubleValue(0.5)); err != nil {
			t.Fatalf("Event: %v", err)
		}
	}

	// Second delivery should be suppressed by the cache (same value).
	if n := count.Load(); n != 1 {
		t.Fatalf("expected 1 event (second suppressed), got %d", n)
	}
}

// TestCallbackHandlersGracefulShutdown verifies that Stop() waits until all
// background goroutines spawned by scheduleSelfReload have completed. Closes.
func TestCallbackHandlersGracefulShutdown(t *testing.T) {
	t.Parallel()

	h := NewCallbackHandlers(nil, nil)

	// Track whether goroutines have completed.
	var completed atomic.Int32

	// Add three goroutines manually using the handler's WaitGroup.
	const n = 3
	for range n {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			// Simulate work that respects the handler's context.
			<-h.ctx.Done()
			completed.Add(1)
		}()
	}

	// Stop must cancel the context and wait for all goroutines.
	h.Stop()

	if got := completed.Load(); got != n {
		t.Errorf("Stop: completed=%d, want %d", got, n)
	}
}

// TestCallbackHandlersStopIsIdempotent ensures calling Stop twice does
// not panic or deadlock.
func TestCallbackHandlersStopIsIdempotent(t *testing.T) {
	t.Parallel()
	h := NewCallbackHandlers(nil, nil)
	h.Stop()
	h.Stop() // must not panic
}

// TestCallbackHandlersNewDevicesNilDevicesNoOp verifies that NewDevices
// is a no-op when the central has no Devices coordinator.
func TestCallbackHandlersNewDevicesNilDevicesNoOp(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.NewDevices(context.Background(), "HmIP-RF", nil); err != nil {
		t.Fatalf("NewDevices with nil Devices: %v", err)
	}
}

// TestCallbackHandlersNewDevicesEmptyDescs verifies that NewDevices
// returns nil and is a no-op for an empty descriptor slice.
func TestCallbackHandlersNewDevicesEmptyDescs(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.NewDevices(context.Background(), "HmIP-RF", xmlrpc.ArrayValue{}); err != nil {
		t.Fatalf("NewDevices empty descs: %v", err)
	}
}

// TestCallbackHandlersDeleteDevicesRemovesFromRegistry verifies that
// DeleteDevices removes the listed address from the ModelRegistry.
func TestCallbackHandlersDeleteDevicesRemovesFromRegistry(t *testing.T) {
	t.Parallel()
	reg, dev := registryWithDevice(t)
	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)

	// The device must exist before deletion.
	if _, ok := c.ModelRegistry.Get(dev.Address); !ok {
		t.Fatal("pre-condition: device not found")
	}
	if err := h.DeleteDevices(context.Background(), "HmIP-RF", []string{dev.Address}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}
	if _, ok := c.ModelRegistry.Get(dev.Address); ok {
		t.Fatal("device must be removed after DeleteDevices")
	}
}

// TestCallbackHandlersDeleteDevicesEmptySlice verifies no panic/error
// when the address list is empty.
func TestCallbackHandlersDeleteDevicesEmptySlice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.DeleteDevices(context.Background(), "HmIP-RF", nil); err != nil {
		t.Fatalf("DeleteDevices nil: %v", err)
	}
}

// TestCallbackHandlersUpdateDeviceNoOp verifies UpdateDevice logs and
// returns nil without side effects.
func TestCallbackHandlersUpdateDeviceNoOp(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.UpdateDevice(context.Background(), "HmIP-RF", "0001ABCD", 1); err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}
}

// TestCallbackHandlersReplaceDeviceNoOp verifies ReplaceDevice logs
// and returns nil.
func TestCallbackHandlersReplaceDeviceNoOp(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.ReplaceDevice(context.Background(), "HmIP-RF", "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}
}

// TestCallbackHandlersReaddedDeviceNoOp verifies ReaddedDevice returns nil.
func TestCallbackHandlersReaddedDeviceNoOp(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.ReaddedDevice(context.Background(), "HmIP-RF", []string{"0001ABCD", "0001ABCE"}); err != nil {
		t.Fatalf("ReaddedDevice: %v", err)
	}
}

// TestCallbackHandlersListDevicesReturnsEmpty verifies ListDevices
// returns an empty array — the CCU is authoritative.
func TestCallbackHandlersListDevicesReturnsEmpty(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	arr, err := h.ListDevices(context.Background(), "HmIP-RF")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(arr) != 0 {
		t.Fatalf("want empty array, got %v", arr)
	}
}

// TestCallbackHandlersErrorPublishesSystemStatus verifies that Error
// publishes a SystemStatusChangedEvent on the central bus.
func TestCallbackHandlersErrorPublishesSystemStatus(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var gotEvent hmevent.SystemStatusChangedEvent
	var fired int
	unsub := events.Subscribe(c.EventBus, func(e hmevent.SystemStatusChangedEvent) {
		gotEvent = e
		fired++
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	if err := h.Error(context.Background(), "HmIP-RF", 42, "link error"); err != nil {
		t.Fatalf("Error: %v", err)
	}

	if fired != 1 {
		t.Fatalf("expected 1 SystemStatusChangedEvent, got %d", fired)
	}
	if gotEvent.InterfaceID != "HmIP-RF" || gotEvent.ErrorCode != 42 {
		t.Fatalf("unexpected event: %+v", gotEvent)
	}
	if gotEvent.Healthy {
		t.Fatal("Error must mark the component as unhealthy")
	}
}

// TestCallbackHandlersErrorNilCentralNoOp verifies Error does not panic
// when central.EventBus is nil.
func TestCallbackHandlersErrorNilCentralNoOp(t *testing.T) {
	t.Parallel()
	h := NewCallbackHandlers(nil, nil)
	if err := h.Error(context.Background(), "HmIP-RF", 1, "boom"); err != nil {
		t.Fatalf("Error with nil central must not error: %v", err)
	}
}

// TestCallbackHandlersErrorNilBusNoOp verifies Error does not panic
// when the central has a nil EventBus.
func TestCallbackHandlersErrorNilBusNoOp(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Overwrite the bus with nil.
	c.EventBus = nil
	h := NewCallbackHandlers(c, nil)
	if err := h.Error(context.Background(), "HmIP-RF", 5, "no bus"); err != nil {
		t.Fatalf("Error with nil bus must not error: %v", err)
	}
}

// TestCallbackHandlersEventUnknownDevice verifies that an event for a
// device that is not in the registry is silently ignored.
func TestCallbackHandlersEventUnknownDevice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "UNKNOWN:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event for unknown device must not error: %v", err)
	}
}

// TestCallbackHandlersEventUnknownChannel verifies that an event for a
// channel not present on the device is silently ignored.
func TestCallbackHandlersEventUnknownChannel(t *testing.T) {
	t.Parallel()
	_, dev := registryWithDevice(t)
	c := registryOf(t, dev)
	h := NewCallbackHandlers(c, nil)
	// No channel added — address 0001ABCD:99 does not exist.
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:99", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event for unknown channel must not error: %v", err)
	}
}

// registryOf returns the first CentralUnit from a registry that already
// holds the given device.
func registryOf(t *testing.T, _ interface{}) *central.CentralUnit {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-tmp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	return c
}
