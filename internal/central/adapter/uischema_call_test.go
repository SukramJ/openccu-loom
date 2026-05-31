// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// uischema_call_test.go covers UISchemaAdapter.UISchema with a real device +
// channel to exercise the main code path through the function body
// (lines 57–164).

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// UISchema — dev==nil path (lines 60-62)
// ============================================================

func TestUISchemaDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := &UISchemaAdapter{registry: reg}
	_, err := a.UISchema(context.Background(), handlers.UISchemaRequest{
		Address:  "NODEV001",
		Channel:  1,
		Paramset: "VALUES",
	})
	if err == nil {
		t.Error("UISchema with unknown device must return error")
	}
}

// ============================================================
// UISchema — valid device + VALUES paramset — exercises lines 64–105
// ============================================================

func TestUISchemaDeviceFoundValuesParamset(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-uis"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "UISDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	ch := d.AddChannel("UISDEV001:1", 1, "CLIMATE_TRANSCEIVER", hmenum.ParamsetKeyValues)
	// Add a visible, readable data point so buildParameters loop body runs.
	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "UISDEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ACTUAL_TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Flags:      hmenum.FlagVisible,
		},
	})
	ch.Put(dp)
	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg}
	schema, err := a.UISchema(context.Background(), handlers.UISchemaRequest{
		Address:  "UISDEV001",
		Channel:  1,
		Paramset: "VALUES",
		Locale:   "en",
	})
	if err != nil {
		t.Fatalf("UISchema: %v", err)
	}
	if schema == nil {
		t.Fatal("UISchema returned nil schema")
	}
}

// ============================================================
// UISchema — invalid paramset key (lines 64-67)
// ============================================================

func TestUISchemaInvalidParamsetKey(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-uis2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "UISDEV002",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	_ = d.AddChannel("UISDEV002:1", 1, "CLIMATE_TRANSCEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg}
	_, err = a.UISchema(context.Background(), handlers.UISchemaRequest{
		Address:  "UISDEV002",
		Channel:  1,
		Paramset: "INVALID_PARAMSET",
	})
	if err == nil {
		t.Error("UISchema with invalid paramset must return error")
	}
}

// ============================================================
// UISchema — MASTER paramset (exercises different branch at line 68)
// ============================================================

func TestUISchemaDeviceFoundMasterParamset(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-uis3"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "UISDEV003",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	_ = d.AddChannel("UISDEV003:1", 1, "CLIMATE_TRANSCEIVER", hmenum.ParamsetKeyMaster)
	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg}
	schema, err := a.UISchema(context.Background(), handlers.UISchemaRequest{
		Address:  "UISDEV003",
		Channel:  1,
		Paramset: "MASTER",
		Locale:   "en",
	})
	if err != nil {
		t.Fatalf("UISchema MASTER: %v", err)
	}
	if schema == nil {
		t.Fatal("UISchema MASTER returned nil schema")
	}
}
