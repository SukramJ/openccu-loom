// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// rssiOrNull + buildRSSIDeviceEntries (pure)
// ---------------------------------------------------------------------------

func TestRSSIOrNull(t *testing.T) {
	t.Parallel()
	if got := rssiOrNull(rssiNoInfo); got != nil {
		t.Fatalf("want nil for sentinel %d, got %v", rssiNoInfo, got)
	}
	for _, v := range []int{-82, -90, 0, -1, rssiNoInfo - 1} {
		if got := rssiOrNull(v); got != v {
			t.Errorf("rssiOrNull(%d) = %v, want %d", v, got, v)
		}
	}
}

func TestBuildRSSIDeviceEntries_ResolvesNamesAndNormalises(t *testing.T) {
	t.Parallel()
	matrix := map[string]map[string][2]int{
		"DEV001": {
			"BidCoS-RF": {-72, -68},        // both directions known
			"DEV002:1":  {rssiNoInfo, -80}, // device→peer has no data
		},
	}
	names := map[string]string{
		"DEV001":   "Living Room Switch",
		"DEV002:1": "Hallway Sensor",
		// "BidCoS-RF" deliberately absent → resolves to "".
	}

	out := buildRSSIDeviceEntries(matrix, "BidCos-RF", "ccu-01", names)
	if len(out) != 1 {
		t.Fatalf("want 1 device entry, got %d", len(out))
	}
	d := out[0]
	if d["address"] != "DEV001" || d["name"] != "Living Room Switch" {
		t.Errorf("device identity wrong: %v / %v", d["address"], d["name"])
	}
	if d["interface_id"] != "BidCos-RF" || d["central"] != "ccu-01" {
		t.Errorf("scoping wrong: iface=%v central=%v", d["interface_id"], d["central"])
	}

	partners, ok := d["partners"].([]map[string]any)
	if !ok || len(partners) != 2 {
		t.Fatalf("want 2 partners, got %T len=%d", d["partners"], len(partners))
	}
	byAddr := map[string]map[string]any{}
	for _, p := range partners {
		byAddr[p["address"].(string)] = p
	}

	// Known partner with no name in the lookup → empty string, real RSSI.
	if bc := byAddr["BidCoS-RF"]; bc["name"] != "" || bc["rssi_device"] != -72 || bc["rssi_peer"] != -68 {
		t.Errorf("BidCoS-RF partner wrong: %+v", bc)
	}
	// Named partner with a no-data device→peer slot → name resolved,
	// rssi_device normalised to nil, rssi_peer kept.
	d2 := byAddr["DEV002:1"]
	if d2["name"] != "Hallway Sensor" {
		t.Errorf("DEV002:1 name = %v, want Hallway Sensor", d2["name"])
	}
	if d2["rssi_device"] != nil {
		t.Errorf("DEV002:1 rssi_device = %v, want nil (sentinel)", d2["rssi_device"])
	}
	if d2["rssi_peer"] != -80 {
		t.Errorf("DEV002:1 rssi_peer = %v, want -80", d2["rssi_peer"])
	}
}

// ---------------------------------------------------------------------------
// RSSIInfoDomain.RSSIInfo — nil-deps guard
// ---------------------------------------------------------------------------

func TestRSSIInfoDomain_NilDepsReturnsEmpty(t *testing.T) {
	t.Parallel()
	d := NewRSSIInfoDomain(nil, nil)
	out, err := d.RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devs := out["devices"].([]map[string]any); len(devs) != 0 {
		t.Fatalf("want 0 devices, got %d", len(devs))
	}
}

// ---------------------------------------------------------------------------
// RSSIInfoDomain.RSSIInfo — full path through registry + writer + backend
// ---------------------------------------------------------------------------

// fakeRSSICaller is a minimal backends.Caller that returns a canned reply
// for the single rssiInfo call the CcuBackend makes.
type fakeRSSICaller struct {
	reply  any
	err    error
	method string // records the last method called
}

func (c *fakeRSSICaller) Call(_ context.Context, method string, _ ...any) (any, error) {
	c.method = method
	return c.reply, c.err
}

func seedRSSIRegistry(t *testing.T) (*central.Registry, *central.Unit) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	c.ModelRegistry.Put(device.New(device.Config{
		Address:     "DEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Living Room Switch",
	}))
	return reg, c
}

func TestRSSIInfoDomain_FullPath_ResolvesAndDispatches(t *testing.T) {
	t.Parallel()
	reg, _ := seedRSSIRegistry(t)

	// A real CcuBackend over a fake Caller — it implements both
	// backends.Operations (so the writer accepts it) and
	// backends.RSSIInfoProvider (so the domain's type assertion holds).
	caller := &fakeRSSICaller{reply: map[string]any{
		"DEV001": map[string]any{
			"BidCoS-RF": []any{-72, rssiNoInfo},
		},
	}}
	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", backends.NewCcuBackend(caller, nil, nil))

	out, err := NewRSSIInfoDomain(reg, writer).RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	if caller.method != "rssiInfo" {
		t.Errorf("backend called %q, want rssiInfo", caller.method)
	}
	devs := out["devices"].([]map[string]any)
	if len(devs) != 1 {
		t.Fatalf("want 1 device, got %d", len(devs))
	}
	d := devs[0]
	if d["address"] != "DEV001" || d["name"] != "Living Room Switch" {
		t.Errorf("device identity/name not resolved: %v / %v", d["address"], d["name"])
	}
	if d["interface_id"] != "BidCos-RF" || d["central"] != "ccu-01" {
		t.Errorf("scoping wrong: iface=%v central=%v", d["interface_id"], d["central"])
	}
	partners := d["partners"].([]map[string]any)
	if len(partners) != 1 {
		t.Fatalf("want 1 partner, got %d", len(partners))
	}
	if partners[0]["rssi_device"] != -72 {
		t.Errorf("rssi_device = %v, want -72", partners[0]["rssi_device"])
	}
	if partners[0]["rssi_peer"] != nil {
		t.Errorf("rssi_peer = %v, want nil (sentinel normalised)", partners[0]["rssi_peer"])
	}
}

// A backend whose rssiInfo call fails must not sink the command — the
// interface is skipped and the device list comes back empty.
func TestRSSIInfoDomain_BackendErrorSkipsInterface(t *testing.T) {
	t.Parallel()
	reg, _ := seedRSSIRegistry(t)
	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", backends.NewCcuBackend(&fakeRSSICaller{err: errors.New("boom")}, nil, nil))

	out, err := NewRSSIInfoDomain(reg, writer).RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo must not surface the per-interface error, got %v", err)
	}
	if devs := out["devices"].([]map[string]any); len(devs) != 0 {
		t.Fatalf("want 0 devices when the only interface errors, got %d", len(devs))
	}
}

// A central with no registered backend for its interface yields no devices
// (the Backend() lookup misses and the interface is skipped).
func TestRSSIInfoDomain_NoBackendSkipsInterface(t *testing.T) {
	t.Parallel()
	reg, _ := seedRSSIRegistry(t)
	writer := clientpkg.NewValueWriter() // nothing registered

	out, err := NewRSSIInfoDomain(reg, writer).RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	if devs := out["devices"].([]map[string]any); len(devs) != 0 {
		t.Fatalf("want 0 devices with no backend, got %d", len(devs))
	}
}
