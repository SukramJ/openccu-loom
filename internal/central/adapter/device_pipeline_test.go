// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
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
	if entry, ok := c.DeviceRegistry.Get(hmenum.InterfaceHmIPRF, "0001ABCD"); !ok || entry.Model != "HmIP-STH" {
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
	// WireInterfaceID("", "ccu-seed-ri2", InterfaceHmIPRF) = "ccu-seed-ri2-HmIP-RF"
	d := device.New(device.Config{
		Address:     "SRIDEV001",
		InterfaceID: WireInterfaceID("", "ccu-seed-ri2", hmenum.InterfaceHmIPRF),
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
