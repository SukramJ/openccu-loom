// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// mqtt_sink_paths_test.go covers additional nil-guard and error branches in
// MQTTCommandSink: SetSysvar unknown-sysvar, TriggerProgram unknown-program,
// InvokeChannelService device/channel/cdp not found paths.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// SetSysvar — unknown sysvar path (lines 57-59)
// ============================================================

func TestMQTTCommandSinkSetSysvarUnknownSysvar(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-sv"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	s := NewMQTTCommandSink(reg, nil)
	// "no-such-sysvar" is not registered → !ok path.
	err = s.SetSysvar(context.Background(), "ccu-sv", "no-such-sysvar", true)
	if err == nil {
		t.Error("SetSysvar with unknown sysvar must return error")
	}
}

// ============================================================
// TriggerProgram — unknown program path (lines 74-76)
// ============================================================

func TestMQTTCommandSinkTriggerProgramUnknownProgram(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-prog"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	s := NewMQTTCommandSink(reg, nil)
	// "no-such-program" not in registry → !ok path.
	err = s.TriggerProgram(context.Background(), "ccu-prog", "no-such-program")
	if err == nil {
		t.Error("TriggerProgram with unknown program must return error")
	}
}

// ============================================================
// InvokeChannelService — device not found (lines 116-118)
// ============================================================

func TestMQTTCommandSinkInvokeChannelServiceDeviceNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ics"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	s := NewMQTTCommandSink(reg, nil)
	// Device "NOTADEV" not registered → !ok path.
	err = s.InvokeChannelService(context.Background(), "ccu-ics", "HmIP-RF", "NOTADEV", 1, "method", nil, hmenum.CommandPriorityLow)
	if err == nil {
		t.Error("InvokeChannelService with unknown device must return error")
	}
}

// ============================================================
// InvokeChannelService — channel not found (lines 121-123)
// ============================================================

func TestMQTTCommandSinkInvokeChannelServiceChannelNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-icsch"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "ICSDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	_ = d.AddChannel("ICSDEV001:1", 1, "TEST", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)
	// Channel :99 not registered → ch == nil path.
	err = s.InvokeChannelService(context.Background(), "ccu-icsch", "HmIP-RF", "ICSDEV001", 99, "method", nil, hmenum.CommandPriorityLow)
	if err == nil {
		t.Error("InvokeChannelService with unknown channel must return error")
	}
}

// ============================================================
// InvokeChannelService — no custom DP on channel (lines 125-127)
// ============================================================

func TestMQTTCommandSinkInvokeChannelServiceNoCustomDP(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-icscdp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "ICSDEV002",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	_ = d.AddChannel("ICSDEV002:1", 1, "TEST", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)
	// Channel exists but has no custom DP → cdp == nil path.
	err = s.InvokeChannelService(context.Background(), "ccu-icscdp", "HmIP-RF", "ICSDEV002", 1, "method", nil, hmenum.CommandPriorityLow)
	if err == nil {
		t.Error("InvokeChannelService with no custom DP must return error")
	}
}

// ============================================================
// InvokeChannelService — custom DP present but no Invoke method (lines 133-134)
// ============================================================

// minimalCustomDP implements AttachableDataPoint (DataPointKey only)
// but does NOT expose an Invoke method — triggering the type-assertion
// failure in InvokeChannelService.
type minimalCustomDP struct{}

func (m *minimalCustomDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{ChannelAddress: "ICSDEV003:1"}
}

func TestMQTTCommandSinkInvokeChannelServiceNoInvoker(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-icsnoinv"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "ICSDEV003",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	ch := d.AddChannel("ICSDEV003:1", 1, "TEST", hmenum.ParamsetKeyValues)
	// Attach a custom DP that does NOT implement Invoke → !ok path.
	ch.SetCustomDataPoint(&minimalCustomDP{})
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)
	err = s.InvokeChannelService(context.Background(), "ccu-icsnoinv", "HmIP-RF", "ICSDEV003", 1, "method", nil, hmenum.CommandPriorityLow)
	if err == nil {
		t.Error("InvokeChannelService with non-invoker custom DP must return error")
	}
}
