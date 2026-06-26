// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// rssiOrNull
// ---------------------------------------------------------------------------

func TestRSSIOrNull_SentinelBecomesNil(t *testing.T) {
	t.Parallel()
	if got := rssiOrNull(rssiNoInfo); got != nil {
		t.Fatalf("want nil for sentinel %d, got %v", rssiNoInfo, got)
	}
}

func TestRSSIOrNull_RealReadingPassedThrough(t *testing.T) {
	t.Parallel()
	cases := []int{-82, -90, 0, -1, rssiNoInfo - 1}
	for _, v := range cases {
		got := rssiOrNull(v)
		if got != v {
			t.Errorf("rssiOrNull(%d): want %d, got %v", v, v, got)
		}
	}
}

// ---------------------------------------------------------------------------
// wsRSSIInfo.RSSIInfo — nil registry / nil writer guard
// ---------------------------------------------------------------------------

func TestWSRSSIInfo_NilRegistryReturnsEmptyDevices(t *testing.T) {
	t.Parallel()
	w := &wsRSSIInfo{registry: nil, writer: nil}
	out, err := w.RSSIInfo(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	devs, ok := out["devices"].([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any devices, got %T", out["devices"])
	}
	if len(devs) != 0 {
		t.Fatalf("expected empty devices slice, got %d entries", len(devs))
	}
}

func TestWSRSSIInfo_EmptyRegistryReturnsEmptyDevices(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t) // no centrals
	w := &wsRSSIInfo{registry: reg, writer: nil}
	out, err := w.RSSIInfo(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	devs := out["devices"].([]map[string]any)
	if len(devs) != 0 {
		t.Fatalf("expected 0 devices from empty registry, got %d", len(devs))
	}
}

// ---------------------------------------------------------------------------
// buildRSSIDeviceEntries — name resolution + 65536 normalisation (pure)
// ---------------------------------------------------------------------------

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
// wsRSSIInfo.RSSIInfo — full path through registry + writer + a real backend
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

func TestWSRSSIInfo_FullPath_ResolvesAndDispatches(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not found in registry")
	}
	cu.ModelRegistry.Put(device.New(device.Config{
		Address:     "DEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Living Room Switch",
	}))

	// A real CcuBackend over a fake Caller — it implements both
	// backends.Operations (so the writer accepts it) and
	// backends.RSSIInfoProvider (so the adapter's type assertion holds).
	caller := &fakeRSSICaller{reply: map[string]any{
		"DEV001": map[string]any{
			"BidCoS-RF": []any{-72, rssiNoInfo},
		},
	}}
	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", backends.NewCcuBackend(caller, nil, nil))

	w := &wsRSSIInfo{registry: reg, writer: writer}
	out, err := w.RSSIInfo(t.Context())
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
func TestWSRSSIInfo_FullPath_BackendErrorSkipsInterface(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, _ := reg.Get("ccu-01")
	cu.ModelRegistry.Put(device.New(device.Config{
		Address:     "DEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Living Room Switch",
	}))
	caller := &fakeRSSICaller{err: errors.New("boom")}
	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", backends.NewCcuBackend(caller, nil, nil))

	w := &wsRSSIInfo{registry: reg, writer: writer}
	out, err := w.RSSIInfo(t.Context())
	if err != nil {
		t.Fatalf("RSSIInfo must not surface the per-interface error, got %v", err)
	}
	if devs := out["devices"].([]map[string]any); len(devs) != 0 {
		t.Fatalf("want 0 devices when the only interface errors, got %d", len(devs))
	}
}
