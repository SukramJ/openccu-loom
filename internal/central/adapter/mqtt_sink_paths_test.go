// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// mqtt_sink_paths_test.go covers additional nil-guard and error branches in
// MQTTCommandSink: SetSysvar unknown-sysvar, TriggerProgram unknown-program,
// InvokeChannelService device/channel/cdp not found paths, and
// SetMasterValue resolution + write paths.

package adapter

import (
	"context"
	"encoding/json"
	"maps"
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
	mu       sync.Mutex
	param    hmenum.Parameter
	value    any
	calls    int
	putKey   hmenum.ParamsetKey
	putVals  map[string]any
	putCalls int
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
	_ context.Context, _ string, key hmenum.ParamsetKey, vals map[string]any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.putKey = key
	w.putVals = maps.Clone(vals)
	w.putCalls++
	return nil
}

func (w *sinkChannelWriter) snapshot() (p hmenum.Parameter, v any, calls int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.param, w.value, w.calls
}

func (w *sinkChannelWriter) putSnapshot() (key hmenum.ParamsetKey, vals map[string]any, calls int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.putKey, maps.Clone(w.putVals), w.putCalls
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

	// The MASTER bucket command must reach the CCU as put_paramset(MASTER).
	// xml-rpc setValue carries no paramset key and always targets VALUES, so
	// dispatching it there would write a device configuration change to the
	// wrong paramset.
	if _, _, setCalls := cw.snapshot(); setCalls != 0 {
		t.Errorf("MASTER command must not use the VALUES-only SetValue; got %d SetValue calls", setCalls)
	}
	key, vals, calls := cw.putSnapshot()
	if calls != 1 {
		t.Fatalf("expected 1 PutParamset call, got %d", calls)
	}
	if key != hmenum.ParamsetKeyMaster {
		t.Errorf("paramset key: got %q want %q", key, hmenum.ParamsetKeyMaster)
	}
	if vals[paramName] != 0.5 {
		t.Errorf("values: got %v want %s=0.5", vals, paramName)
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

// ============================================================
// Fix 1 — SetMasterValue interface mismatch guard
// ============================================================

func TestMQTTCommandSinkSetMasterValueInterfaceMismatch(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ifmm"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	// Device is registered on HmIP-RF.
	d := device.New(device.Config{
		Address:     "MMDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	_ = d.AddChannel("MMDEV001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)
	// Caller claims device is on BidCos-RF — mismatch must be rejected.
	err = s.SetMasterValue(context.Background(), "ccu-ifmm", "BidCos-RF", "MMDEV001:1",
		hmenum.Parameter("NO_PARAM"), true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error when interfaceID does not match device's interface")
	}
}

func TestMQTTCommandSinkSetMasterValueInterfaceEmptySkipsCheck(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ifempty"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "EMDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	_ = d.AddChannel("EMDEV001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)
	// Empty interfaceID must not trigger the mismatch guard; the error
	// here comes from the missing MASTER parameter, not the iface check.
	err = s.SetMasterValue(context.Background(), "ccu-ifempty", "", "EMDEV001:1",
		hmenum.Parameter("NO_PARAM"), true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error for missing MASTER param, not interface mismatch")
	}
	// The error must NOT mention "belongs to interface" — it was the
	// parameter guard that fired, not the interface guard.
	if containsString(err.Error(), "belongs to interface") {
		t.Errorf("unexpected interface-mismatch error with empty interfaceID: %v", err)
	}
}

// containsString reports whether sub is a substring of s.
func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || sub == "" ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ============================================================
// SetCombinedValue — an unresolvable target is an error
// ============================================================

// TestMQTTCommandSinkSetCombinedValueUnresolvableTargetErrors pins that a
// combined write the sink cannot route reports an error rather than
// returning nil.
//
// The sink used to swallow any kind it did not implement, back when it
// understood exactly one ("duration") and HA could plausibly publish
// others. Dispatch now runs through payload.CombinedProjection, so the
// only kinds with a command topic are the ones a projection advertises —
// and a write arriving for anything else means something published to a
// topic nobody offered. Swallowing that leaves an operator watching a
// control that reports success and does nothing.
func TestMQTTCommandSinkSetCombinedValueUnresolvableTargetErrors(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cdt"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	s := NewMQTTCommandSink(reg, nil)

	cases := []struct {
		name          string
		kind, raw     string
		deviceAddress string
	}{
		{name: "empty kind", kind: "", raw: "30", deviceAddress: "SOMEDEV"},
		{name: "unknown device", kind: "duration", raw: "30", deviceAddress: "SOMEDEV"},
		{name: "unknown kind on unknown device", kind: "hs_color", raw: "x", deviceAddress: "SOMEDEV"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := s.SetCombinedValue(
				context.Background(),
				"ccu-cdt", "HmIP-RF", tc.deviceAddress, 1,
				tc.kind, tc.raw,
				hmenum.CommandPriorityLow,
			)
			if err == nil {
				t.Error("SetCombinedValue must report an unroutable write, got nil")
			}
		})
	}
}
