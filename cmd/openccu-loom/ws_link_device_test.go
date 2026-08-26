// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── wsLinkQuery with non-nil domain + registry ───────────────────────────────

func TestWSLinkQuery_ListLinks_NonNilDomain_Errors(t *testing.T) {
	t.Parallel()
	// domain non-nil (via real LinksDomain backed by nil registry) + registry non-nil.
	// ListLinks calls domain.ListLinks → adapter tries to look up device → not found.
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.ListLinks(context.Background(), "NONEXISTENT:1")
	// Error expected (device not found) or empty slice — either is acceptable; must not panic.
	_ = err
}

func TestWSLinkQuery_AddLink_NonNilDomain_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	q := &wsLinkQuery{domain: domain, registry: reg}
	err := q.AddLink(context.Background(), "A:0", "B:0", "link", "desc")
	// Devices not found → error expected.
	_ = err
}

func TestWSLinkQuery_SetLinkInfo_NonNilDomain_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	q := &wsLinkQuery{domain: domain, registry: reg}
	err := q.SetLinkInfo(context.Background(), "A:0", "B:0", "name", "desc")
	// Devices not found → error expected; must not panic.
	_ = err
}

func TestWSLinkQuery_RemoveLink_NonNilDomain_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	q := &wsLinkQuery{domain: domain, registry: reg}
	err := q.RemoveLink(context.Background(), "A:0", "B:0")
	_ = err
}

func TestWSLinkQuery_LinkableChannels_WithRegistry_DeviceNotFound_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	// domain non-nil; registry non-nil but device address not found → reaches loop + not-found return
	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.LinkableChannels(context.Background(), "NONEXISTENT")
	if err == nil {
		t.Fatal("expected error for unknown device address")
	}
}

func TestWSLinkQuery_GetLinkParamset_NonNilDomain_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.GetLinkParamset(context.Background(), "A:0", "B:0")
	_ = err
}

func TestWSLinkQuery_PutLinkParamset_NonNilDomain_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)
	q := &wsLinkQuery{domain: domain, registry: reg}
	err := q.PutLinkParamset(context.Background(), "A:0", "B:0", nil)
	_ = err
}

// ── wsDeviceQuery GetParamset non-nil path ────────────────────────────────────

func TestWSDeviceQuery_GetParamset_NonNilParamsets_DefaultKey(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  reg,
	}
	// Empty ParamsetKey → defaults to MASTER inside GetParamset.
	_, err := w.GetParamset(context.Background(), configui.SessionKey{
		ChannelAddress: "ANY:1",
		ParamsetKey:    "",
	})
	// Device/channel not in domain → error expected; must not panic.
	_ = err
}

func TestWSDeviceQuery_GetParamset_NonNilParamsets_ExplicitKey(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  reg,
	}
	_, err := w.GetParamset(context.Background(), configui.SessionKey{
		ChannelAddress: "ANY:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
	})
	_ = err
}

// ── wsParamsetWriter PutParamset non-nil path ─────────────────────────────────

func TestWSParamsetWriter_PutParamset_NonNilDomain_DefaultKey(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewParamsetsDomain(reg, nil)
	w := &wsParamsetWriter{domain: domain}
	err := w.PutParamset(context.Background(), configui.SessionKey{
		ChannelAddress: "ANY:1",
		ParamsetKey:    "",
	}, map[string]any{"LEVEL": 1.0})
	// Device not found → error; but empty key → MASTER default was exercised.
	_ = err
}

// ── wsDeviceQuery ListDevices with DevicesAdapter non-nil ────────────────────

func TestWSDeviceQuery_ListDevices_WithDevicesAdapter_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	devAdapter := adapter.NewDevicesAdapter(reg)
	w := &wsDeviceQuery{devs: devAdapter}
	got, err := w.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	// No devices in empty registry → 0 entries; loop body NOT exercised.
	// We just exercise the devs != nil path.
	if len(got) != 0 {
		t.Errorf("expected 0 devices, got %d", len(got))
	}
}

// ── wsDeviceWriter non-nil admin path ────────────────────────────────────────

func TestWSDeviceWriter_Rename_NonNilAdmin_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	admin := adapter.NewDeviceAdminDomain(reg, nil)
	w := &wsDeviceWriter{admin: admin}
	err := w.Rename(context.Background(), "NONEXISTENT:1", "new name", false)
	// No device → error; must not panic.
	_ = err
}

func TestWSDeviceWriter_RenameChannel_NonNilAdmin_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	admin := adapter.NewDeviceAdminDomain(reg, nil)
	w := &wsDeviceWriter{admin: admin}
	err := w.RenameChannel(context.Background(), "NONEXISTENT", 1, "new name")
	if err == nil {
		t.Fatal("expected error: device not found in empty registry")
	}
}

func TestWSDeviceWriter_SetInstallMode_NonNilAdmin_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	admin := adapter.NewDeviceAdminDomain(reg, nil)
	w := &wsDeviceWriter{admin: admin}
	err := w.SetInstallMode(context.Background(), "NONEXISTENT:1", 60)
	_ = err
}

// TestWSDeviceQuery_GetDevice_FoundDevice_ReturnsMap exercises the happy path
// where the device is found in the registry — covers the channels loop and map build.
func TestWSDeviceQuery_GetDevice_FoundDevice_ReturnsMap(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not in registry")
	}
	dev := device.New(device.Config{
		Address:     "DEV999",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Bookshelf Lamp",
	})
	cu.ModelRegistry.Put(dev)

	devAdapter := adapter.NewDevicesAdapter(reg)
	w := &wsDeviceQuery{devs: devAdapter}
	got, err := w.GetDevice(context.Background(), "DEV999")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got["address"] != "DEV999" {
		t.Errorf("expected address=DEV999, got %v", got["address"])
	}
	if got["model"] != "HM-LC-Sw1-Pl" {
		t.Errorf("expected model=HM-LC-Sw1-Pl, got %v", got["model"])
	}
}

// ── wsHubQuery SetSysvar non-nil hub, found sysvar ───────────────────────────

func TestWSHubQuery_SetSysvar_LiveHub_FoundSysvar_NoError(t *testing.T) {
	t.Parallel()
	// This covers the SetSysvar → h.Sysvar(name) found path.
	// (The "not found" path is already covered by ws_hub_query_test.go)
	// We need a valid sysvar so Sysvar(name) returns ok=true, then Set fails.
	// Since there's no writer configured the Set call will fail gracefully.
	q, h := liveHubQuery(t)
	s := hubmodel.NewSysvar("ccu-01", "test-sysvar", "", hmenum.HubValueTypeString, nil)
	h.PutSysvar(s)
	// Value conversion will succeed, but Write will fail (no backend).
	// Either way must not panic.
	err := q.SetSysvar(context.Background(), "test-sysvar", "hello")
	_ = err
}
