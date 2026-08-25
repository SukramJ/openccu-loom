// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newProductionVisibilityGate returns a *visibility.Registry wired exactly as
// the daemon's composition root does it: a fresh registry with the full
// required-parameter whitelist from [custom.DefaultRegistry].
// Tests that want to verify realistic pipeline behaviour (the gate active,
// required parameters visible) should use this helper instead of
// constructing a bare registry or passing nil.
func newProductionVisibilityGate() *visibility.Registry {
	reg := visibility.NewRegistry()
	reg.SetRequiredParameters(custom.DefaultRegistry().RequiredParameters())
	return reg
}

func TestDevicePipelineIngestsDeviceAndChannels(t *testing.T) {
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	bt := true
	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "0001ABCD", Type: "HmIP-STH", Firmware: "2.0", FirmwareUpdatable: &bt},
		{Address: "0001ABCD:0", Parent: "0001ABCD"},
		{Address: "0001ABCD:1", Parent: "0001ABCD"},
		{Address: "0001ABCD:2", Parent: "0001ABCD"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	d, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device missing")
	}
	if len(d.Channels()) != 3 {
		t.Fatalf("channels=%d", len(d.Channels()))
	}
	if d.Firmware().Info().Current != "2.0" {
		t.Fatalf("firmware=%+v", d.Firmware().Info())
	}
	if !d.Updatable {
		t.Fatal("updatable flag not propagated")
	}
	if entry, ok := c.DeviceRegistry.Get(wireHmIPRF, "0001ABCD"); !ok || entry.Model != "HmIP-STH" {
		t.Fatalf("registry entry=%+v ok=%v", entry, ok)
	}
}

func TestDevicePipelineIdempotent(t *testing.T) {
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)
	descs := []hmproto.DeviceDescription{
		{Address: "0001", Type: "X"},
		{Address: "0001:0", Parent: "0001"},
	}
	_ = p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, descs)
	_ = p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, descs)
	if c.ModelRegistry.Len() != 1 {
		t.Fatalf("registry len=%d", c.ModelRegistry.Len())
	}
}

// newHydratingBackend returns a paramsetFakeOps that acts as a full
// backend for IngestFromBackend: it lists one device with one channel,
// returns a single LEVEL float DP for VALUES paramset descriptions, and
// returns empty maps for value reads.
func newHydratingBackend() *paramsetFakeOps {
	return &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "0001ABCD", Type: "HmIP-STH"},
				{Address: "0001ABCD:1", Parent: "0001ABCD", Type: "LEVEL"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key == hmenum.ParamsetKeyValues {
				return map[string]hmproto.ParameterData{
					string(hmenum.ParameterLevel): {
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
					},
				}, nil
			}
			return nil, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}
}

// TestIngestPopulatesLinkRoles verifies the second pass stamps the raw
// CCU LINK_SOURCE_ROLES / LINK_TARGET_ROLES onto the channel model so
// the direct-link role-matching filter can intersect them without a CCU
// roundtrip.
func TestIngestPopulatesLinkRoles(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())

	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "0001ABCD", Type: "HmIP-WRC"},
				{
					Address:         "0001ABCD:1",
					Parent:          "0001ABCD",
					Type:            "KEY_TRANSCEIVER",
					LinkSourceRoles: hmproto.LinkRoles{"SWITCH", "REMOTECONTROL_RECEIVER"},
					LinkTargetRoles: hmproto.LinkRoles{"WEATHER"},
				},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			return nil, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", b)

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry after IngestFromBackend")
	}
	ch := dev.Channel("0001ABCD:1")
	if ch == nil {
		t.Fatal("channel 0001ABCD:1 not found")
	}
	src := ch.LinkSourceRoles()
	if len(src) != 2 || src[0] != "SWITCH" || src[1] != "REMOTECONTROL_RECEIVER" {
		t.Errorf("Channel.LinkSourceRoles() = %v, want [SWITCH REMOTECONTROL_RECEIVER]", src)
	}
	tgt := ch.LinkTargetRoles()
	if len(tgt) != 1 || tgt[0] != "WEATHER" {
		t.Errorf("Channel.LinkTargetRoles() = %v, want [WEATHER]", tgt)
	}
}

// TestHydrateChannelInstallsWriterAndRefresher verifies that
// IngestFromBackend wires Channel.SetWriter and Channel.SetRefresher on
// every channel. After hydration:
// - Channel.Set must NOT return ErrNoChannelWriter.
// - Channel.Refresh must NOT return ErrNoChannelRefresher.
func TestHydrateChannelInstallsWriterAndRefresher(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())

	b := newHydratingBackend()

	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", b)

	vw := &fakeWriter{} // satisfies adapter.ValueWriter

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, vw, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry after IngestFromBackend")
	}
	ch := dev.Channel("0001ABCD:1")
	if ch == nil {
		t.Fatal("channel 0001ABCD:1 not found")
	}

	// --- verify SetWriter was installed ---
	// Channel.Set dispatches through the installed ChannelWriter. The fake
	// writer (fakeWriter.SetValue) always succeeds, so we expect either nil
	// or a non-ErrNoChannelWriter error (e.g. ErrNoWriter from the
	// boundWriter when the ValueWriter has no registered backend for this
	// channel). Either way, ErrNoChannelWriter must NOT be the result.
	setErr := ch.Set(
		context.Background(),
		hmenum.ParamsetKeyValues,
		hmenum.ParameterLevel,
		hmtypes.FloatValue(0.5),
		device.SetOptions{},
	)
	if errors.Is(setErr, device.ErrNoChannelWriter) {
		t.Fatal("SetWriter was NOT installed on channel by hydrateChannel")
	}

	// --- verify SetRefresher was installed ---
	refreshErr := ch.Refresh(context.Background(), hmenum.ParamsetKeyValues)
	if errors.Is(refreshErr, device.ErrNoChannelRefresher) {
		t.Fatal("SetRefresher was NOT installed on channel by hydrateChannel")
	}
}

// ─── Item 1 — device.SchemaVersion propagation ───────────────────────────────

// TestDevicePipelineSchemaVersionPropagated verifies that the CCU's wire
// VERSION field (DeviceDescription.Version *int) is stored on
// Device.SchemaVersion after ingest.
func TestDevicePipelineSchemaVersionPropagated(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	v := 42
	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "AABBCCDD", Type: "HmIP-STH", Version: &v},
		{Address: "AABBCCDD:1", Parent: "AABBCCDD"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	d, ok := c.ModelRegistry.Get("AABBCCDD")
	if !ok {
		t.Fatal("device missing after ingest")
	}
	if d.SchemaVersion != 42 {
		t.Fatalf("SchemaVersion = %d, want 42", d.SchemaVersion)
	}
}

// TestDevicePipelineSchemaVersionNilIsZero verifies that absent VERSION
// (nil pointer) is treated as 0, matching Python's `or 0` fallback.
func TestDevicePipelineSchemaVersionNilIsZero(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "AABBCC01", Type: "HmIP-STH"}, // Version field absent → nil
		{Address: "AABBCC01:1", Parent: "AABBCC01"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	d, ok := c.ModelRegistry.Get("AABBCC01")
	if !ok {
		t.Fatal("device missing after ingest")
	}
	if d.SchemaVersion != 0 {
		t.Fatalf("SchemaVersion = %d, want 0 when VERSION absent", d.SchemaVersion)
	}
}

// TestDevicePipelineRxModePropagated verifies that the CCU RX_MODE bitmask
// on the device description reaches the device model, so the REST DTO can
// tell WAKEUP / LAZY_CONFIG battery devices apart from mains devices.
func TestDevicePipelineRxModePropagated(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	// RX_WAKEUP (8) | RX_LAZY_CONFIG (16) = 24 — a battery device.
	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "BATT0001", Type: "HmIP-eTRV", RXMode: 24},
		{Address: "BATT0001:1", Parent: "BATT0001"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	d, ok := c.ModelRegistry.Get("BATT0001")
	if !ok {
		t.Fatal("device missing after ingest")
	}
	if !d.RxMode.Has(hmenum.RxModeWakeup) || !d.RxMode.Has(hmenum.RxModeLazyConfig) {
		t.Fatalf("RxMode = %d, want WAKEUP|LAZY_CONFIG set", d.RxMode)
	}
}

// TestDevicePipelineRxModeUndefinedWhenAbsent verifies that a device
// description without an RX_MODE field (the Go zero value, 0) propagates as
// hmenum.RxModeUndefined rather than being mistaken for RX_ALWAYS — the CCU
// omits RX_MODE for backends that never report it (e.g. some CUxD/Homegear
// device kinds), and the REST DTO must be able to tell "no rx mode reported"
// apart from "explicitly mains-powered".
func TestDevicePipelineRxModeUndefinedWhenAbsent(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "NORXM0001", Type: "HmIP-BSM"},
		{Address: "NORXM0001:1", Parent: "NORXM0001"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	d, ok := c.ModelRegistry.Get("NORXM0001")
	if !ok {
		t.Fatal("device missing after ingest")
	}
	if d.RxMode != hmenum.RxModeUndefined {
		t.Fatalf("RxMode = %d, want RxModeUndefined for an absent RX_MODE field", d.RxMode)
	}
}

// ─── Item 2 — device.ProductGroup model-prefix matching ──────────────────────

// TestProductGroupForModelPrefix verifies that ProductGroupForModel
// applies model-name prefix matching before falling back to the
// interface.
//
// hmipw- → HmIP-Wired hmip- → HmIP-RF hmw- → BidCos-Wired hm- → BidCos-RF
func TestProductGroupForModelPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model     string
		iface     hmenum.Interface
		wantGroup hmenum.ProductGroup
	}{
		// Model-prefix wins over interface. HmIP-Wired devices reach us
		// through the HmIP-RF interface; only the model prefix can
		// distinguish them from RF cousins.
		{"HmIPW-DRDI3", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHmIPW},
		{"HmIP-STH", hmenum.InterfaceBidCosRF, hmenum.ProductGroupHmIP},
		{"HMW-LC-Sw2-DR", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHmW},
		{"HM-CC-RT-DN", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHM},
		// Prefix matching is case-insensitive.
		{"hmipw-drdi3", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHmIPW},
		{"hmip-sth", hmenum.InterfaceBidCosRF, hmenum.ProductGroupHmIP},
		{"hmw-lc-sw2-dr", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHmW},
		{"hm-cc-rt-dn", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHM},
		// No prefix → interface fallback.
		{"UNKNOWN-DEVICE", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHmIP},
		{"UNKNOWN-DEVICE", hmenum.InterfaceBidCosRF, hmenum.ProductGroupHM},
		{"UNKNOWN-DEVICE", hmenum.InterfaceBidCosWired, hmenum.ProductGroupHmW},
		{"UNKNOWN-DEVICE", hmenum.InterfaceVirtualDevices, hmenum.ProductGroupVirtual},
		{"UNKNOWN-DEVICE", hmenum.InterfaceCUxD, hmenum.ProductGroupUnknown},
		// Empty model → interface fallback.
		{"", hmenum.InterfaceHmIPRF, hmenum.ProductGroupHmIP},
	}
	for _, tc := range cases {
		got := hmenum.ProductGroupForModel(tc.model, tc.iface)
		if got != tc.wantGroup {
			t.Errorf("ProductGroupForModel(%q, %q) = %q, want %q",
				tc.model, tc.iface, got, tc.wantGroup)
		}
	}
}

// TestDevicePipelineProductGroupFromModel verifies that after Ingest the device
// carries the model-prefix-derived product group, not the interface fallback.
// This closes the 20-device drift where HmIPW-xxx devices on an HmIP-RF
// interface were classified as HmIP-RF instead of HmIP-Wired.
func TestDevicePipelineProductGroupFromModel(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		// HmIPW-xxx on HmIP-RF interface — prefix must win.
		{Address: "WIRED001", Type: "HmIPW-DRDI3"},
		{Address: "WIRED001:1", Parent: "WIRED001"},
		// HMW-xxx on HmIP-RF interface — prefix must win.
		{Address: "WIRED002", Type: "HMW-LC-Sw2-DR"},
		{Address: "WIRED002:1", Parent: "WIRED002"},
		// Normal HmIP device — prefix still correct.
		{Address: "RADIO001", Type: "HmIP-STH"},
		{Address: "RADIO001:1", Parent: "RADIO001"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if d, ok := c.ModelRegistry.Get("WIRED001"); !ok || d.ProductGroup != hmenum.ProductGroupHmIPW {
		t.Fatalf("WIRED001 product_group = %q, want %q", d.ProductGroup, hmenum.ProductGroupHmIPW)
	}
	if d, ok := c.ModelRegistry.Get("WIRED002"); !ok || d.ProductGroup != hmenum.ProductGroupHmW {
		t.Fatalf("WIRED002 product_group = %q, want %q", d.ProductGroup, hmenum.ProductGroupHmW)
	}
	if d, ok := c.ModelRegistry.Get("RADIO001"); !ok || d.ProductGroup != hmenum.ProductGroupHmIP {
		t.Fatalf("RADIO001 product_group = %q, want %q", d.ProductGroup, hmenum.ProductGroupHmIP)
	}
}

// ─── Item 3 — snapshot interface_id = string(d.ProductGroup) ─────────────────

// TestDevicePipelineProductGroupMatchesSnapshotInterfaceID verifies the
// contract that Device.ProductGroup (not Device.Interface) is the correct
// source for the snapshot's "interface_id" field, matching the Python
// snapshot script's `str(device.product_group)`
// (aiohomematic_snapshot.py:552).
//
// For most devices Interface and ProductGroup carry the same string, but
// when a device's model prefix overrides the group (e.g. HmIPW-xxx on
// HmIP-RF) the two diverge. The snapshot must use ProductGroup.
func TestDevicePipelineProductGroupMatchesSnapshotInterfaceID(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c)

	err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		// HmIPW model on HmIP-RF interface: Interface="HmIP-RF" but
		// ProductGroup="HmIP-Wired". The snapshot must emit "HmIP-Wired"
		// for interface_id, NOT "HmIP-RF".
		{Address: "WIREDX01", Type: "HmIPW-DRDI3"},
		{Address: "WIREDX01:1", Parent: "WIREDX01"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	d, ok := c.ModelRegistry.Get("WIREDX01")
	if !ok {
		t.Fatal("device missing")
	}
	// Interface is still HmIP-RF (the physical listener the CCU used).
	if d.Interface != hmenum.InterfaceHmIPRF {
		t.Fatalf("Interface = %q, want %q", d.Interface, hmenum.InterfaceHmIPRF)
	}
	// ProductGroup is HmIP-Wired (model-prefix derived).
	if d.ProductGroup != hmenum.ProductGroupHmIPW {
		t.Fatalf("ProductGroup = %q, want %q", d.ProductGroup, hmenum.ProductGroupHmIPW)
	}
	// Snapshot contract: interface_id must equal string(ProductGroup).
	// Using string(Interface) would give "HmIP-RF" — that is a 4-device
	// drift that was observed in testing.
	if string(d.ProductGroup) != string(hmenum.ProductGroupHmIPW) {
		t.Fatalf("snapshot interface_id = string(ProductGroup) = %q, want %q",
			string(d.ProductGroup), string(hmenum.ProductGroupHmIPW))
	}
}

// ─── Item 4 — reseller-model interface-fallback (test-setup limitation) ──────

// TestProductGroupForResellerModelsUseInterfaceFallback documents the
// known test-setup limitation for reseller-branded BidCos-RF devices
// (, Folge):
//
// - get_product_group
// Has NO reseller
// lookup table — there is no map of "ZEL STG RM …" → HM, etc.
// - In production these devices arrive on BidCos-RF, so the interface
// fallback gives ProductGroup.HM (correct).
// - In the godevccu integration test all devices run over a single
// HmIP-RF interface, so the interface fallback gives ProductGroup.HmIP
// For these models (incorrect vs, which distributes devices
// across BidCos-RF + HmIP-RF). This causes 69/399 mismatch in the
// OpenCCU-Loom-vs-
// not a code bug.
//
// This test pins the correct production behaviour: when the CCU reports
// these models under BidCos-RF, ProductGroupForModel must return ProductGroup.HM.
func TestProductGroupForResellerModelsUseInterfaceFallback(t *testing.T) {
	t.Parallel()

	// Reseller models: no HM-/HmIP-/HMW-/HmIPW- prefix → interface fallback.
	resellerModels := []string{
		"ZEL STG RM FFK",
		"ZEL STG RM FDK",
		"ZEL STG RM FWT",
		"ZEL STG RM FEP 230V",
		"263 155",
		"263 146",
		"263 147",
		"263 132",
		"263 133",
		"263 134",
		"ASH550",
		"ASH550I",
		"CMM",
		"IS-WDS-TH-OD-S-R3",
		"HSS-DX",
	}

	for _, model := range resellerModels {
		// Production scenario: BidCos-RF interface → ProductGroup.HM.
		if got := hmenum.ProductGroupForModel(model, hmenum.InterfaceBidCosRF); got != hmenum.ProductGroupHM {
			t.Errorf("ProductGroupForModel(%q, BidCos-RF) = %q, want HM (production interface)", model, got)
		}
		// godevccu scenario: all devices on HmIP-RF → ProductGroup.HmIP
		// (known test-setup divergence, not a code bug).
		if got := hmenum.ProductGroupForModel(model, hmenum.InterfaceHmIPRF); got != hmenum.ProductGroupHmIP {
			t.Errorf("ProductGroupForModel(%q, HmIP-RF) = %q, want HmIP (godevccu test-setup)", model, got)
		}
	}
}

// ============================================================
// DevicePipeline.materialiseCustomDataPoints
// ============================================================

func TestMaterialiseCustomDataPointsNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.materialiseCustomDataPoints("HmIP-RF", nil) // must not panic
}

func TestMaterialiseCustomDataPointsWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mcdp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "MCDPDEV001", InterfaceID: "BidCos-RF", Model: "HM-CC-RT-DN"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Wrong interface → device skipped
	p.materialiseCustomDataPoints("HmIP-RF", nil)
}

func TestMaterialiseCustomDataPointsMatchingInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mcdp2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "MCDPDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("MCDPDEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Matching interface → processed, must not panic
	p.materialiseCustomDataPoints("HmIP-RF", nil)
}

// ============================================================
// DevicePipeline.materialiseCalculatedDataPoints
// ============================================================

func TestMaterialiseCalculatedDataPointsNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.materialiseCalculatedDataPoints("HmIP-RF", nil) // must not panic
}

func TestMaterialiseCalculatedDataPointsWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-calc"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "CALCDEV001", InterfaceID: "BidCos-RF", Model: "HM-CC-RT-DN"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	p.materialiseCalculatedDataPoints("HmIP-RF", nil) // skipped, no panic
}

func TestMaterialiseCalculatedDataPointsMatchingInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-calc2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "CALCDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("CALCDEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	p.materialiseCalculatedDataPoints("HmIP-RF", nil)
}

// ============================================================
// DevicePipeline.applyIgnoredParameterMarks — nil visibility
// ============================================================

func TestApplyIgnoredParameterMarksNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.applyIgnoredParameterMarks("HmIP-RF") // must not panic
}

func TestApplyIgnoredParameterMarksNilVisibility(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ignore"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "IGNDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	c.ModelRegistry.Put(dev)

	// visibility is nil → early return (p.central == nil || p.visibility == nil)
	p := NewDevicePipeline(c) // visibility is nil by default
	p.applyIgnoredParameterMarks("HmIP-RF")
}

// ============================================================
// DevicePipeline.applyHiddenParameterMarks — nil visibility fallback
// ============================================================

func TestApplyHiddenParameterMarksNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.applyHiddenParameterMarks("HmIP-RF") // must not panic
}

func TestApplyHiddenParameterMarksMatchingInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-hidden"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "HIDDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("HIDDEV001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c) // nil visibility → decider is nil
	p.applyHiddenParameterMarks("HmIP-RF")
}

// ============================================================
// parseFloat — invalid JSON path (json.Unmarshal error branch)
// ============================================================

func TestParsefloatInvalidJSON(t *testing.T) {
	t.Parallel()
	// A JSON string is not a float → Unmarshal error → returns (0, false).
	_, ok := parseFloat(json.RawMessage(`"not_a_number"`))
	if ok {
		t.Error("parseFloat: expected false for string JSON value")
	}
}

func TestParsefloatEmptyRaw(t *testing.T) {
	t.Parallel()
	// Empty RawMessage → immediate false return (line 39-41).
	_, ok := parseFloat(json.RawMessage{})
	if ok {
		t.Error("parseFloat: expected false for empty raw")
	}
}

func TestParsefloatValid(t *testing.T) {
	t.Parallel()
	v, ok := parseFloat(json.RawMessage(`18.5`))
	if !ok || v != 18.5 {
		t.Errorf("parseFloat: expected (18.5, true), got (%v, %v)", v, ok)
	}
}

// ============================================================
// isProfileIDWithinCap — malformed ID / over-cap branches
// ============================================================

func TestIsProfileIDWithinCapMalformedID(t *testing.T) {
	t.Parallel()
	// "P" alone has length 1, not 2 → false.
	if isProfileIDWithinCap("P", 6) {
		t.Error("isProfileIDWithinCap: expected false for 'P' (len==1)")
	}
	// "XY" has correct length but wrong prefix → false.
	if isProfileIDWithinCap("XY", 6) {
		t.Error("isProfileIDWithinCap: expected false for 'XY'")
	}
	// "" empty → false.
	if isProfileIDWithinCap("", 6) {
		t.Error("isProfileIDWithinCap: expected false for ''")
	}
}

func TestIsProfileIDWithinCapValidRange(t *testing.T) {
	t.Parallel()
	// P1..P3 valid against cap of 3.
	for _, p := range []string{"P1", "P2", "P3"} {
		if !isProfileIDWithinCap(p, 3) {
			t.Errorf("isProfileIDWithinCap: expected true for %q cap=3", p)
		}
	}
}

func TestIsProfileIDWithinCapOverCap(t *testing.T) {
	t.Parallel()
	// P4 exceeds cap of 3 → false (n > maxProfiles branch).
	if isProfileIDWithinCap("P4", 3) {
		t.Error("isProfileIDWithinCap: expected false for P4 with cap=3")
	}
}

func TestIsProfileIDWithinCapZeroIndex(t *testing.T) {
	t.Parallel()
	// P0 → n=0 < 1 → false (n < 1 branch).
	if isProfileIDWithinCap("P0", 6) {
		t.Error("isProfileIDWithinCap: expected false for P0")
	}
}

// ============================================================
// seedRelevantInitParameters — non-nil unit, no matching devices
// ============================================================

func TestSeedRelevantInitParametersNonNilUnitNoDevices(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-seed-ri"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// No devices registered → inner loop never entered, but outer body runs
	// covering lines 40-42 (wireID + loop header) and 83-88 (logger check).
	seedRelevantInitParameters(context.Background(), c, hmenum.InterfaceHmIPRF, nil)
}

// TestSeedRelevantInitParametersWithMatchingDevice exercises deeper branches:
// a device matching the wireID exists, but has no channel :0 → ch == nil path.
func TestSeedRelevantInitParametersWithMatchingDevice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-seed-ri2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Register a device whose InterfaceID matches the composite wire ID.
	// WireInterfaceID("ccu-seed-ri2", InterfaceHmIPRF) = "ccu-seed-ri2-HmIP-RF"
	d := device.New(device.Config{
		Address:     "SRIDEV001",
		InterfaceID: WireInterfaceID("ccu-seed-ri2", hmenum.InterfaceHmIPRF),
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	// Add channel :1 but NOT :0 → ch == nil inside the loop → continue path.
	_ = d.AddChannel("SRIDEV001:1", 1, "TEST", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	// Must not panic; covers the loop body including the ch == nil branch.
	seedRelevantInitParameters(context.Background(), c, hmenum.InterfaceHmIPRF, nil)
}

// ============================================================
// seedReadableEvents — nil unit guard and non-nil unit with no devices
// ============================================================

func TestSeedReadableEventsNilUnit(t *testing.T) {
	t.Parallel()
	// Must not panic when unit is nil — covers the nil guard branch.
	seedReadableEvents(context.Background(), nil, hmenum.InterfaceHmIPRF, nil)
}

func TestSeedReadableEventsNonNilUnitNoDevices(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-seed-re"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// No devices → covers lines 110-112 (wireID assignment) and 147-152 (log block).
	seedReadableEvents(context.Background(), c, hmenum.InterfaceHmIPRF, nil)
}

// ============================================================
// DevicePipeline.materialiseCombinedDataPoints
// ============================================================

func TestMaterialiseCombinedDataPointsNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.materialiseCombinedDataPoints("HmIP-RF", nil) // must not panic
}

func TestMaterialiseCombinedDataPointsWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cdp-iface"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "CDPDEV001", InterfaceID: "BidCos-RF", Model: "HM-Sec-Win"})
	c.ModelRegistry.Put(dev)
	p := NewDevicePipeline(c)
	p.materialiseCombinedDataPoints("HmIP-RF", nil) // no matching device; no panic
}

func TestMaterialiseCombinedDataPointsBridgesValueToBus(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cdp-bus"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	dev := device.New(device.Config{Address: "CDPDEV002", InterfaceID: "HmIP-RF", Model: "HM-Blind"})
	ch := dev.AddChannel("CDPDEV002:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	fdp := &pipelineFakeCombinedDP{dpKey: hmtypes.DataPointKey{
		ChannelAddress: "CDPDEV002:1",
		ParamsetKey:    hmenum.ParamsetKeyCombined,
		Parameter:      "LEVEL_COMBINED",
	}}
	ch.AttachCalculatedDataPoint(fdp)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	p.materialiseCombinedDataPoints("HmIP-RF", slog.Default())

	if !fdp.subscribed {
		t.Fatal("materialiseCombinedDataPoints must call OnAnyUpdate on the combined DP")
	}
}

// pipelineFakeCombinedDP is a minimal fake that satisfies device.CombinedDataPoint
// and the adapter CombinedDataPoint interface (OnAnyUpdate).
type pipelineFakeCombinedDP struct {
	dpKey      hmtypes.DataPointKey
	subscribed bool
}

func (f *pipelineFakeCombinedDP) IsCombined() bool                   { return true }
func (f *pipelineFakeCombinedDP) DataPointKey() hmtypes.DataPointKey { return f.dpKey }
func (f *pipelineFakeCombinedDP) OnAnyUpdate(_ func(old, next any)) func() {
	f.subscribed = true
	return func() {}
}

// TestHydrateStoresParamsetDescriptionsInRegistry locks the fleet-wide
// descriptor capture: hydration must store every fetched paramset
// description in the central's ParamsetRegistry (which feeds
// channel-paramset reads and, via the persistence sink, the warm-boot
// descriptor cache) — not only the per-channel reload path.
func TestHydrateStoresParamsetDescriptionsInRegistry(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-psreg-01"})
	p := NewDevicePipeline(c)

	b := backendWithParams("AABBCC77", "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		"STATE": {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	if c.ParamsetReg.Len() == 0 {
		t.Fatal("hydration stored no paramset descriptions in the registry")
	}
	d, ok := c.ModelRegistry.Get("AABBCC77")
	if !ok {
		t.Fatal("device missing")
	}
	found := false
	for _, ch := range d.Channels() {
		descs := c.ParamsetReg.GetChannelParamsetDescriptions(wireHmIPRF, ch.Address)
		if ps, ok := descs[hmenum.ParamsetKeyValues]; ok {
			if _, ok := ps["STATE"]; ok {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("VALUES description with STATE not found in registry for any hydrated channel")
	}
}

// ---------------------------------------------------------------------------
// DevicePipeline.seedValues — string data points must be UriEncode-decoded
// ---------------------------------------------------------------------------

// TestDevicePipeline_SeedValues_DecodesURLEncodedStringValue pins a defect
// measured live against two HmIP access points: fetch_all_device_data.fn
// (see internal/client/rega/scripts/fetch_all_device_data.fn) wraps every
// STRING-typed data point's value in UriEncode() so an embedded quote or
// control character cannot break the script's hand-rolled JSON envelope —
// "192.0.2.40" is written as "192%2E0%2E2%2E40". seedValues already
// url.QueryUnescape's the *key*; the value must go through the same
// decoding, or the percent-encoded literal lands straight in the model. The
// value below carries both a space and a dot, mirroring the class of value
// (free-text / address-like strings) the script encodes.
func TestDevicePipeline_SeedValues_DecodesURLEncodedStringValue(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	p := NewDevicePipeline(f.unit)

	ch := f.dev.Channel("DEV002:0")
	if ch == nil {
		t.Fatal("fixture channel DEV002:0 missing")
	}
	dp := generic.NewDataPoint[string](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV002:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "IP_ADDRESS",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	// UriEncode("192.0.2.40 room") — both the dots and the space are
	// percent-escaped, matching the CCU-observed encoding of a live
	// IP_ADDRESS data point ("192%2E0%2E2%2E40").
	const encoded = "192%2E0%2E2%2E40%20room"
	const want = "192.0.2.40 room"
	payload := `{"HmIP-RF.DEV002%3A0.IP_ADDRESS": "` + encoded + `"}`

	srv := newBoost6JSONRPCServerAlwaysOK(t, payload)
	defer srv.Close()
	jc := newBoost6JSONRPCClient(t, srv.URL)
	r := newBoost6RegaRunner(t, jc)

	if err := p.seedValues(context.Background(), "HmIP-RF", r, slog.Default()); err != nil {
		t.Fatalf("seedValues: %v", err)
	}

	got, observed := dp.Value()
	if !observed {
		t.Fatal("IP_ADDRESS data point was never seeded — OnWireValue did not apply")
	}
	if got != want {
		t.Fatalf("IP_ADDRESS = %q, want decoded value %q (URL-encoded literal must not reach the model)", got, want)
	}
}

// TestDevicePipeline_SeedValues_TranscodesLatin1StringValue pins the second
// half of the ReGa decode: the CCU speaks ISO-8859-1, so a percent-escaped
// umlaut unescapes to a raw high byte that is not valid UTF-8.
//
// A bare url.QueryUnescape seeded that byte into the live model, and every
// north-bound encoder then replaced it with U+FFFD — "Spüle" reached MQTT,
// REST and the SPA as "Sp\uFFFDle" and stayed that way until the next live
// event overwrote the value, while the same name fetched through the hub path
// rendered correctly.
func TestDevicePipeline_SeedValues_TranscodesLatin1StringValue(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	p := NewDevicePipeline(f.unit)

	ch := f.dev.Channel("DEV002:0")
	if ch == nil {
		t.Fatal("fixture channel DEV002:0 missing")
	}
	dp := generic.NewDataPoint[string](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV002:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEXT",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	// UriEncode("Spüle") on a CCU: the umlaut is the Latin-1 byte 0xFC.
	const encoded = "Sp%FCle"
	const want = "Spüle"
	payload := `{"HmIP-RF.DEV002%3A0.TEXT": "` + encoded + `"}`

	srv := newBoost6JSONRPCServerAlwaysOK(t, payload)
	defer srv.Close()
	jc := newBoost6JSONRPCClient(t, srv.URL)
	r := newBoost6RegaRunner(t, jc)

	if err := p.seedValues(context.Background(), "HmIP-RF", r, slog.Default()); err != nil {
		t.Fatalf("seedValues: %v", err)
	}

	got, observed := dp.Value()
	if !observed {
		t.Fatal("TEXT data point was never seeded — OnWireValue did not apply")
	}
	if got != want {
		t.Fatalf("TEXT = %q, want %q (Latin-1 value must be transcoded, not seeded as invalid UTF-8)", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("TEXT = %q is not valid UTF-8; every north-bound encoder will replace it with U+FFFD", got)
	}
}

// TestDevicePipeline_SeedValues_SkipsEdgeTriggerParameters pins that the
// fetch_all_device_data seed never marks a button as observed.
//
// The script emits every data point carrying a valid Timestamp() (see
// internal/client/rega/scripts/fetch_all_device_data.fn), and a PRESS_*
// data point acquires one on its first press and keeps it for good. Seeding
// that value hands the boot-time snapshot a keypress to replay, so a button
// pressed once on the CCU fires its consumers again on every daemon start.
// The neighbouring STATE value must still land — the exclusion is scoped to
// edge-trigger parameters, not to the whole seed.
func TestDevicePipeline_SeedValues_SkipsEdgeTriggerParameters(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	p := NewDevicePipeline(f.unit)

	ch := f.dev.Channel("DEV002:0")
	if ch == nil {
		t.Fatal("fixture channel DEV002:0 missing")
	}
	press := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV002:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "PRESS_SHORT",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(press)
	state := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV002:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(state)

	payload := `{"HmIP-RF.DEV002%3A0.PRESS_SHORT": true, "HmIP-RF.DEV002%3A0.STATE": true}`
	srv := newBoost6JSONRPCServerAlwaysOK(t, payload)
	defer srv.Close()
	jc := newBoost6JSONRPCClient(t, srv.URL)
	r := newBoost6RegaRunner(t, jc)

	if err := p.seedValues(context.Background(), "HmIP-RF", r, slog.Default()); err != nil {
		t.Fatalf("seedValues: %v", err)
	}

	if _, observed := press.Value(); observed {
		t.Error("PRESS_SHORT was seeded — an edge-trigger parameter must stay unobserved after a seed")
	}
	if _, observed := state.Value(); !observed {
		t.Error("STATE was not seeded — the exclusion must be scoped to edge-trigger parameters")
	}
}

// TestDevicePipeline_RestoreValuesFromCache_SkipsEdgeTriggerParameters
// pins the second half of the edge-trigger exclusion: rows written before
// the cache stopped accepting PRESS_* are still on disk, so the restore
// side has to reject them as well.
//
// Without this an existing installation keeps replaying its last keypress
// on every boot until the GC pass happens to clear the row — the write-side
// filter alone fixes only fresh databases.
func TestDevicePipeline_RestoreValuesFromCache_SkipsEdgeTriggerParameters(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	store := freshValuesCacheStoreForAdapter(t)
	p := NewDevicePipeline(f.unit).WithValuesCacheStore(store, f.unit.Name())

	ch := f.dev.Channel("DEV002:0")
	if ch == nil {
		t.Fatal("fixture channel DEV002:0 missing")
	}
	for _, param := range []string{"PRESS_SHORT", "STATE"} {
		ch.Put(generic.NewDataPoint[bool](generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    "HmIP-RF",
				ChannelAddress: "DEV002:0",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      param,
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		}))
	}

	// A legacy database: both rows present, written by an older build.
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	for _, param := range []string{"PRESS_SHORT", "STATE"} {
		if err := store.SaveValue(ctx, f.unit.Name(), "HmIP-RF", "DEV002:0", param, true, now, now); err != nil {
			t.Fatalf("SaveValue %s: %v", param, err)
		}
	}

	p.restoreValuesFromCache(ctx, "HmIP-RF", slog.Default())

	press := ch.Parameter(hmenum.ParameterPressShort)
	if press == nil {
		t.Fatal("PRESS_SHORT missing from the channel")
	}
	if reader, ok := press.(interface{ RawValue() (any, bool) }); ok {
		if _, observed := reader.RawValue(); observed {
			t.Error("PRESS_SHORT was restored — a persisted keypress must never be replayed into the model")
		}
	} else {
		t.Fatal("PRESS_SHORT does not expose RawValue")
	}

	state := ch.Parameter(hmenum.ParameterState)
	if state == nil {
		t.Fatal("STATE missing from the channel")
	}
	if reader, ok := state.(interface{ RawValue() (any, bool) }); ok {
		if _, observed := reader.RawValue(); !observed {
			t.Error("STATE was not restored — the exclusion must be scoped to edge-trigger parameters")
		}
	}
}

// TestDevicePipelineKeysRegistriesByTheStampedWireID pins the agreement
// between the two things the ingest pipeline produces for one device: the
// InterfaceID it stamps on the model, and the key it writes its registry rows
// under. Production hands the pipeline the canonical `<central>-<iface>` wire
// id — the only form the CCU callback path can produce — so a row written
// under the bare interface name is one no consumer ever resolves: the device
// appears twice after a hot-plug, the firmware refresh finds no description,
// the team-candidate list comes back empty.
//
// The named central is the whole point. With an unnamed one the two forms are
// the same string and the test would pass against either producer.
func TestDevicePipelineKeysRegistriesByTheStampedWireID(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-wirekey"})
	wireID := WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)
	p := NewDevicePipeline(c)

	b := backendWithParams("AABBCC90", "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		"STATE": {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	if err := p.IngestFromBackend(
		context.Background(), wireID, hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("AABBCC90")
	if !ok {
		t.Fatal("device missing from the model registry")
	}
	if dev.InterfaceID != wireID {
		t.Fatalf("device InterfaceID=%q, want the wire id %q", dev.InterfaceID, wireID)
	}

	// Resolve the way every consumer does: through the id the device carries.
	key := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
	if entry, ok := c.DeviceRegistry.Get(key, "AABBCC90"); !ok || entry.Model != "HmIP-STH" {
		t.Fatalf("DeviceRegistry.Get(%q)=%+v ok=%v; the pipeline keyed the entry "+
			"under something no consumer looks up", key, entry, ok)
	}
	foundParamset := false
	for _, ch := range dev.Channels() {
		if _, ok := c.ParamsetReg.Get(key, ch.Address, hmenum.ParamsetKeyValues); ok {
			foundParamset = true
		}
	}
	if !foundParamset {
		t.Fatalf("ParamsetReg holds no VALUES description under %q for any hydrated channel", key)
	}

	// And nothing under the bare interface: that key space has no reader.
	bare := hmtypes.WireInterfaceID(hmenum.InterfaceHmIPRF)
	if _, ok := c.DeviceRegistry.Get(bare, "AABBCC90"); ok {
		t.Fatalf("DeviceRegistry also holds an entry under the bare interface %q — "+
			"the same device is registered twice", bare)
	}
}

// TestHydrateStampsVirtDevPathRootsOnANamedCentral pins the `virtdev/` path
// roots of a virtual-remote data point through the real hydration path, on a
// central that has a name — which every configured central does.
//
// The interface reaches the path-data constructor as the canonical
// `<central>-<interface>` wire id, so a root selection that compared it
// against the bare `VirtualDevices` constant never matched in a running
// daemon; it only matched in unit tests that handed the constant in directly.
// Asserting through IngestFromBackend is what makes the difference visible:
// the pipeline decides which id it passes, not the test.
func TestHydrateStampsVirtDevPathRootsOnANamedCentral(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-virt"
	c, _ := central.New(central.Config{Name: centralName})
	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())
	wireID := WireInterfaceID(centralName, hmenum.InterfaceVirtualDevices)

	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "INT0000001", Type: "HM-RCV-50"},
				{Address: "INT0000001:1", Parent: "INT0000001", Type: "VIRTUAL_KEY"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key != hmenum.ParamsetKeyValues {
				return nil, nil
			}
			return map[string]hmproto.ParameterData{
				"PRESS_SHORT": {
					Type:       hmenum.ParameterTypeAction,
					Operations: hmenum.OperationsWrite | hmenum.OperationsEvent,
				},
			}, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}

	if err := p.IngestFromBackend(
		context.Background(), wireID, hmenum.InterfaceVirtualDevices,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("INT0000001")
	if !ok {
		t.Fatal("virtual device missing from the model registry")
	}
	ch := dev.Channel("INT0000001:1")
	if ch == nil {
		t.Fatal("channel INT0000001:1 not hydrated")
	}
	dp := ch.Parameter("PRESS_SHORT")
	if dp == nil {
		t.Fatal("PRESS_SHORT data point not hydrated")
	}
	pather, ok := dp.(interface{ PathData() naming.PathData })
	if !ok {
		t.Fatalf("%T carries no path data — the pipeline could not stamp it either", dp)
	}
	pd := pather.PathData()
	if pd.SetPath != "virtdev/set/INT0000001:1/1/values/PRESS_SHORT" {
		t.Errorf("SetPath = %q, want the virtdev root", pd.SetPath)
	}
	if pd.StatePath != "virtdev/status/INT0000001:1/1/values/PRESS_SHORT" {
		t.Errorf("StatePath = %q, want the virtdev root", pd.StatePath)
	}
}
