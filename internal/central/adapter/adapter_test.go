// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// registryWithDevice models an operational central: its southbound bring-up
// has completed, so the ready latch is set — matching the production order in
// which every snapshot pass observes a central (gatedCentralBringUp latches
// ready after finishIngest applied the visibility marks). Tests that exercise
// the mid-bring-up window use [registryWithDeviceNotReady].
func registryWithDevice(t *testing.T) (*central.Registry, *device.Device) {
	t.Helper()
	reg, d := registryWithDeviceNotReady(t)
	if u, ok := reg.Get("ccu-01"); ok {
		u.MarkSouthboundReady()
	}
	return reg, d
}

// registryWithDeviceNotReady is registryWithDevice without the ready latch —
// the state a central is in while its readiness-gated bring-up still runs.
func registryWithDeviceNotReady(t *testing.T) (*central.Registry, *device.Device) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "0001ABCD", Model: "HmIP-STH", Name: "Flur",
	})
	c.ModelRegistry.Put(d)
	return reg, d
}

func TestDevicesAdapterListsEveryCentral(t *testing.T) {
	reg, d := registryWithDevice(t)
	a := NewDevicesAdapter(reg)
	if got := a.Devices(); len(got) != 1 || got[0].Address != d.Address {
		t.Fatalf("devices=%+v", got)
	}
	if got, ok := a.Device(d.Address); !ok || got != d {
		t.Fatalf("device=%v ok=%v", got, ok)
	}
	if _, ok := a.Device("MISSING"); ok {
		t.Fatal("expected miss")
	}
}

type fakeWriter struct {
	calls atomic.Int32
	last  struct{ centralName, iface, chanAddr, param string }
}

func (f *fakeWriter) SetValue(_ context.Context, centralName, iface, chanAddr string,
	param hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.last.centralName = centralName
	f.last.iface = iface
	f.last.chanAddr = chanAddr
	f.last.param = string(param)
	return nil
}

func TestDataPointWriterDispatchesByDevice(t *testing.T) {
	reg, _ := registryWithDevice(t)
	w := &fakeWriter{}
	a := NewDataPointWriterAdapter(reg, w)
	if err := a.SetValue(context.Background(), "0001ABCD:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set: %v", err)
	}
	if w.calls.Load() != 1 || w.last.centralName != "ccu-01" || w.last.iface != "HmIP-RF" {
		t.Fatalf("last=%+v", w.last)
	}
}

func TestDataPointWriterUnknownDeviceFails(t *testing.T) {
	reg, _ := registryWithDevice(t)
	a := NewDataPointWriterAdapter(reg, &fakeWriter{})
	if err := a.SetValue(context.Background(), "MISSING:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("expected error")
	}
}

func TestHubAdapterReturnsFirstCentral(t *testing.T) {
	reg, _ := registryWithDevice(t)
	a := NewHubAdapter(reg)
	if a.Hub() == nil {
		t.Fatal("hub expected")
	}
}

func TestInterfacesAdapterEnumerates(t *testing.T) {
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	a := NewInterfacesAdapter(reg, nil)
	if got := a.Interfaces(); len(got) != 0 {
		t.Fatalf("no clients expected, got %+v", got)
	}
}
