// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// mqtt_sink_paths_test.go covers additional nil-guard and error branches in
// MQTTCommandSink: SetSysvar unknown-sysvar, TriggerProgram unknown-program,
// InvokeChannelService device/channel/cdp not found paths, and
// SetMasterValue resolution + write paths.

package adapter

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
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

// ============================================================
// SetMasterValue — channel writer records the write
// ============================================================

// sinkChannelWriter is a minimal device.ChannelWriter that records
// SetValue calls so SetMasterValue tests can assert the write reached
// the wire layer.
type sinkChannelWriter struct {
	mu    sync.Mutex
	param hmenum.Parameter
	value any
	calls int
}

func (w *sinkChannelWriter) SetValue(
	_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.param = p
	w.value = v
	w.calls++
	return nil
}

func (w *sinkChannelWriter) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority,
) error {
	return nil
}

func (w *sinkChannelWriter) snapshot() (p hmenum.Parameter, v any, calls int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.param, w.value, w.calls
}

func TestMQTTCommandSinkSetMasterValueWritesValue(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	const chAddr = "MPDEV001:1"
	const paramName = "SHORT_ON_TIME"

	cw := &sinkChannelWriter{}
	d := device.New(device.Config{
		Address:     "MPDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	ch := d.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	// Wire a MASTER data point and the channel writer.
	masterDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      paramName,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100.0"),
		},
		Writer: cw,
	})
	ch.PutMaster(masterDP)
	ch.SetWriter(cw)
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)
	if err := s.SetMasterValue(
		context.Background(), "ccu-mp", "HmIP-RF", chAddr,
		hmenum.Parameter(paramName), 0.5, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("SetMasterValue: %v", err)
	}

	p, v, calls := cw.snapshot()
	if calls != 1 {
		t.Fatalf("expected 1 SetValue call, got %d", calls)
	}
	if p != hmenum.Parameter(paramName) {
		t.Errorf("param: got %q want %q", p, paramName)
	}
	if v != 0.5 {
		t.Errorf("value: got %v want 0.5", v)
	}
}

func TestMQTTCommandSinkSetMasterValueUnknownCentral(t *testing.T) {
	t.Parallel()
	s := NewMQTTCommandSink(central.NewRegistry(), nil)
	err := s.SetMasterValue(context.Background(), "no-such-central", "HmIP-RF", "DEV:1",
		hmenum.Parameter("FOO"), true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error for unknown central")
	}
}

func TestMQTTCommandSinkSetMasterValueUnknownDevice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mpdev"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	s := NewMQTTCommandSink(reg, nil)
	err = s.SetMasterValue(context.Background(), "ccu-mpdev", "HmIP-RF", "NODEV:1",
		hmenum.Parameter("FOO"), true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error for unknown device")
	}
}

func TestMQTTCommandSinkSetMasterValueUnknownChannel(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mpch"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "MPDEV002",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	_ = d.AddChannel("MPDEV002:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)
	s := NewMQTTCommandSink(reg, nil)
	// Channel :99 does not exist.
	err = s.SetMasterValue(context.Background(), "ccu-mpch", "HmIP-RF", "MPDEV002:99",
		hmenum.Parameter("FOO"), true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error for unknown channel")
	}
}

func TestMQTTCommandSinkSetMasterValueUnknownParam(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mpparam"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "MPDEV003",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	_ = d.AddChannel("MPDEV003:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)
	s := NewMQTTCommandSink(reg, nil)
	// Channel exists but has no MASTER DP named NO_SUCH_PARAM.
	err = s.SetMasterValue(context.Background(), "ccu-mpparam", "HmIP-RF", "MPDEV003:1",
		hmenum.Parameter("NO_SUCH_PARAM"), true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error for parameter not in MASTER paramset")
	}
}
