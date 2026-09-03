// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmAdpTwoCentralRegistry builds two centrals that both hold a device at the
// same address — the multi-CCU shape an address repeat produces. Only the
// central named in withCDP carries a custom data point on that device; the
// other holds a bare device, so a walk that stops at the first central holding
// the address answers with the wrong installation.
//
// Registry.List() sorts by name, so "ccu-01" is always visited first.
func hmAdpTwoCentralRegistry(t *testing.T, deviceAddr, withCDP string, dp device.AttachableDataPoint) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, name := range []string{"ccu-01", "ccu-02"} {
		c, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("reg.Register(%s): %v", name, err)
		}
		dev := device.New(device.Config{
			InterfaceID: "HmIP-RF",
			Interface:   hmenum.InterfaceHmIPRF,
			Address:     deviceAddr,
			Model:       "TestDevice",
		})
		ch := dev.AddChannel(deviceAddr+":1", 1, "TEST", hmenum.ParamsetKeyValues)
		if name == withCDP {
			ch.SetCustomDataPoint(dp)
		}
		c.ModelRegistry.Put(dev)
	}
	return reg
}

// TestHmAdpInvokeCustomDPHonoursTheTopicCentral pins that the central the MQTT
// topic carried decides which installation receives the command. Without it,
// the dispatcher walks the name-sorted registry and returns at the first
// central holding the address — refusing a valid command when that copy has no
// such data point, and, when it does, writing to the wrong physical device.
func TestHmAdpInvokeCustomDPHonoursTheTopicCentral(t *testing.T) {
	t.Parallel()

	w := &dispatchWriter{}
	dp := buildLightDP(t, "DUP001", w)
	reg := hmAdpTwoCentralRegistry(t, "DUP001", "ccu-02", dp)
	sink := NewMQTTCommandSink(reg, nil)

	err := sink.InvokeCustomDP(context.Background(), "ccu-02", "DUP001",
		string(hmenum.ParameterLevel), "set_brightness", map[string]any{"brightness": 0.5}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("InvokeCustomDP on ccu-02 = %v, want nil — the command was resolved against another central", err)
	}
	if len(w.sets) == 0 && len(w.puts) == 0 {
		t.Fatal("no write reached the device on ccu-02")
	}
}

// TestHmAdpInvokeCustomDPRejectsAnUnknownCentral is the negative control: a
// central the topic names but the registry does not hold must not silently
// fall back to a registry-wide walk.
func TestHmAdpInvokeCustomDPRejectsAnUnknownCentral(t *testing.T) {
	t.Parallel()

	w := &dispatchWriter{}
	dp := buildLightDP(t, "DUP002", w)
	reg := hmAdpTwoCentralRegistry(t, "DUP002", "ccu-02", dp)
	sink := NewMQTTCommandSink(reg, nil)

	err := sink.InvokeCustomDP(context.Background(), "ccu-99", "DUP002",
		string(hmenum.ParameterLevel), "set_brightness", map[string]any{"brightness": 0.5}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("InvokeCustomDP on an unknown central = nil, want an error")
	}
	if len(w.sets) != 0 || len(w.puts) != 0 {
		t.Fatalf("a write reached a device although the named central is unknown: sets=%v puts=%v", w.sets, w.puts)
	}
}
