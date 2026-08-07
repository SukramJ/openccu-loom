// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// mqtt_sink_writer_test.go covers the non-nil writer path in
// MQTTCommandSink.SetValue and the non-nil cdpDispatch path in
// MQTTCommandSink.InvokeCustomDP.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestMQTTCommandSinkSetValueWithWriter(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	s := NewMQTTCommandSink(central.NewRegistry(), w)
	err := s.SetValue(
		context.Background(),
		"ccu-01", "HmIP-RF", "DEV001:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityLow,
	)
	// fakeWriter.SetValue returns nil — call must succeed.
	if err != nil {
		t.Errorf("SetValue with fakeWriter: %v", err)
	}
	if w.calls.Load() != 1 {
		t.Errorf("fakeWriter not called, got %d calls", w.calls.Load())
	}
}

// fakeInstallWriter records SetInstallMode calls for the
// ActivateInstallMode sink path.
type fakeInstallWriter struct {
	enabled  bool
	iface    string
	duration time.Duration
	calls    int
}

func (w *fakeInstallWriter) SetInstallMode(_ context.Context, interfaceID string, enabled bool, duration time.Duration) error {
	w.calls++
	w.iface = interfaceID
	w.enabled = enabled
	w.duration = duration
	return nil
}

func TestMQTTCommandSinkActivateInstallMode(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	w := &fakeInstallWriter{}
	c.HubModel.PutInstallMode(hub.NewInstallMode("HmIP-RF", w))

	s := NewMQTTCommandSink(reg, &fakeWriter{})

	// PRESS (seconds=0) → default 60s window.
	if err := s.ActivateInstallMode(context.Background(), "ccu-01", "HmIP-RF", 0); err != nil {
		t.Fatalf("ActivateInstallMode default: %v", err)
	}
	if w.calls != 1 || !w.enabled || w.iface != "HmIP-RF" || w.duration != 60*time.Second {
		t.Fatalf("default press: calls=%d enabled=%v iface=%q dur=%v", w.calls, w.enabled, w.iface, w.duration)
	}

	// Explicit duration → forwarded verbatim.
	if err := s.ActivateInstallMode(context.Background(), "ccu-01", "HmIP-RF", 120); err != nil {
		t.Fatalf("ActivateInstallMode 120s: %v", err)
	}
	if w.duration != 120*time.Second {
		t.Fatalf("explicit duration: got %v want 120s", w.duration)
	}
}

func TestMQTTCommandSinkActivateInstallModeUnknownInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	s := NewMQTTCommandSink(reg, &fakeWriter{})
	if err := s.ActivateInstallMode(context.Background(), "ccu-01", "BidCos-RF", 0); err == nil {
		t.Fatal("expected error for unknown install-mode interface")
	}
}

// TestMQTTCommandSinkSetValueCanonicalizesTopicAddress pins the wire address
// a topic-derived command resolves to. The naming layer upper-cases every
// address in the MQTT topic path, but XML-RPC addresses are case-sensitive
// — and the virtual remote ("HmIP-RCV-1") is the one mixed-case address in a
// CCU's inventory. Feeding the topic's upper-cased form straight into
// setValue faults with "Invalid device", which is exactly why HA button
// presses on the virtual remote never reached the CCU.
func TestMQTTCommandSinkSetValueCanonicalizesTopicAddress(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "HmIP-RCV-1", Model: "HmIP-RCV-50", Name: "Virtuelle Fernbedienung",
	})
	dev.AddChannel("HmIP-RCV-1:50", 50, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	w := &fakeWriter{}
	s := NewMQTTCommandSink(reg, w)
	if err := s.SetValue(context.Background(), "ccu-01", "HmIP-RF", "HMIP-RCV-1:50",
		hmenum.Parameter("PRESS_SHORT"), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if w.calls.Load() != 1 {
		t.Fatalf("writer not called, got %d calls", w.calls.Load())
	}
	if w.last.chanAddr != "HmIP-RCV-1:50" {
		t.Fatalf("writer received %q — the topic-derived address must be canonicalized to the model's spelling", w.last.chanAddr)
	}
	// An address the model does not know passes through unchanged.
	if err := s.SetValue(context.Background(), "ccu-01", "HmIP-RF", "0001ABCD:1",
		hmenum.Parameter("STATE"), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetValue passthrough: %v", err)
	}
	if w.last.chanAddr != "0001ABCD:1" {
		t.Fatalf("unknown address must pass through unchanged, got %q", w.last.chanAddr)
	}
}
