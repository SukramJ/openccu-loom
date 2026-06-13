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
