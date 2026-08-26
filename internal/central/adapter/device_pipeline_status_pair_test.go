// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestIngestPairsStatusParameterSoWireStatusReachesTheBaseDataPoint drives
// the production chain: the pipeline builds OPERATING_VOLTAGE and its
// paired OPERATING_VOLTAGE_STATUS, then the CCU pushes the status as an
// ENUM index.
//
// The index can only be mapped onto a ParameterStatus with the paired
// parameter's VALUE_LIST cached on the base data point. Without that
// pairing the status event is dropped and a measurement the device
// itself reports as unusable keeps surfacing as available.
func TestIngestPairsStatusParameterSoWireStatusReachesTheBaseDataPoint(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).
		WithVisibility(newProductionVisibilityGate()).
		WithNames(map[string]string{"0001ABCD": "Fenster"})

	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "0001ABCD", Type: "HmIP-SWDO"},
				{Address: "0001ABCD:0", Parent: "0001ABCD", Type: "MAINTENANCE"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key != hmenum.ParamsetKeyValues || addr != "0001ABCD:0" {
				return nil, nil
			}
			return map[string]hmproto.ParameterData{
				"OPERATING_VOLTAGE": {
					Type:       hmenum.ParameterTypeFloat,
					Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					Min:        json.RawMessage("0.0"),
					Max:        json.RawMessage("5.0"),
				},
				"OPERATING_VOLTAGE_STATUS": {
					Type:       hmenum.ParameterTypeEnum,
					Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					Min:        json.RawMessage("0"),
					Max:        json.RawMessage("3"),
					ValueList:  []string{"NORMAL", "UNKNOWN", "OVERFLOW", "EXTERNAL"},
				},
			}, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry")
	}
	base := dev.Channel("0001ABCD:0").Parameter(hmenum.Parameter("OPERATING_VOLTAGE"))
	if base == nil {
		t.Fatal("OPERATING_VOLTAGE data point missing")
	}
	paired, ok := base.(interface {
		StatusParameter() (string, bool)
		IsStatusValid() bool
		IsValid() bool
	})
	if !ok {
		t.Fatalf("OPERATING_VOLTAGE data point %T exposes no status surface", base)
	}
	if name, set := paired.StatusParameter(); !set || name != "OPERATING_VOLTAGE_STATUS" {
		t.Fatalf("StatusParameter()=(%q,%v) want (\"OPERATING_VOLTAGE_STATUS\",true)", name, set)
	}

	h := NewCallbackHandlers(c, nil)
	ctx := context.Background()
	if err := h.Event(ctx, "HmIP-RF", "0001ABCD:0", "OPERATING_VOLTAGE", xmlrpc.DoubleValue(2.9)); err != nil {
		t.Fatalf("Event(OPERATING_VOLTAGE): %v", err)
	}
	if !paired.IsStatusValid() || !paired.IsValid() {
		t.Fatalf("before the status event: IsStatusValid=%v IsValid=%v want true/true",
			paired.IsStatusValid(), paired.IsValid())
	}

	// VALUE_LIST index 2 → OVERFLOW: the device says the reading is unusable.
	if err := h.Event(ctx, "HmIP-RF", "0001ABCD:0", "OPERATING_VOLTAGE_STATUS", xmlrpc.IntValue(2)); err != nil {
		t.Fatalf("Event(OPERATING_VOLTAGE_STATUS): %v", err)
	}
	if paired.IsStatusValid() {
		t.Error("IsStatusValid()=true after an OVERFLOW status event")
	}
	if paired.IsValid() {
		t.Error("IsValid()=true after an OVERFLOW status event — the value is published as available")
	}

	// Index 0 → NORMAL restores validity.
	if err := h.Event(ctx, "HmIP-RF", "0001ABCD:0", "OPERATING_VOLTAGE_STATUS", xmlrpc.IntValue(0)); err != nil {
		t.Fatalf("Event(OPERATING_VOLTAGE_STATUS): %v", err)
	}
	if !paired.IsStatusValid() || !paired.IsValid() {
		t.Errorf("after NORMAL: IsStatusValid=%v IsValid=%v want true/true",
			paired.IsStatusValid(), paired.IsValid())
	}
}
