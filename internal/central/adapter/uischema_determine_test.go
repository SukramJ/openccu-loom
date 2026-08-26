// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// uischema_determine_test.go covers the OPERATIONS DETERMINE bit
// (hmenum.OperationsDetermine) making it through to
// hmapi.UISchemaParameterOps.Determine on both paths that build a
// UISchemaParameter: buildParameters (uischema_adapter.go, serves
// VALUES + MASTER) and buildLinkSchema (uischema_link.go, serves LINK).

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestBuildParametersSetsDetermineFlag exercises buildParameters (shared by
// VALUES and MASTER) with one determine-capable data point and one plain
// one, and asserts the DTO carries the bit through per-parameter rather
// than defaulting true or leaking across parameters.
func TestBuildParametersSetsDetermineFlag(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-det-params"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	d := device.New(device.Config{
		Address:     "DETDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	ch := d.AddChannel("DETDEV001:1", 1, "CLIMATE_TRANSCEIVER", hmenum.ParamsetKeyValues)

	determinable := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DETDEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ACTUAL_TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsDetermine,
			Flags:      hmenum.FlagVisible,
		},
	})
	ch.Put(determinable)

	plain := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DETDEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "SET_POINT_TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Flags:      hmenum.FlagVisible,
		},
	})
	ch.Put(plain)

	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg}
	schema, err := a.UISchema(context.Background(), hmapi.UISchemaRequest{
		Address:  "DETDEV001",
		Channel:  1,
		Paramset: "VALUES",
		Locale:   "en",
	})
	if err != nil {
		t.Fatalf("UISchema: %v", err)
	}

	byName := make(map[string]hmapi.UISchemaParameter, len(schema.Parameters))
	for _, p := range schema.Parameters {
		byName[p.Name] = p
	}

	det, ok := byName["ACTUAL_TEMPERATURE"]
	if !ok {
		t.Fatal("ACTUAL_TEMPERATURE missing from schema parameters")
	}
	if !det.Operations.Determine {
		t.Error("ACTUAL_TEMPERATURE: expected Operations.Determine=true (OPERATIONS carries DETERMINE)")
	}

	nondet, ok := byName["SET_POINT_TEMPERATURE"]
	if !ok {
		t.Fatal("SET_POINT_TEMPERATURE missing from schema parameters")
	}
	if nondet.Operations.Determine {
		t.Error("SET_POINT_TEMPERATURE: expected Operations.Determine=false (no DETERMINE bit)")
	}
}

// determineLinkOps is a minimal backends.Operations fake for
// buildLinkSchema, exposing one determine-capable and one plain LINK
// parameter.
type determineLinkOps struct {
	paramsetFakeOps
}

func (determineLinkOps) GetLinkParamsetDescription(_ context.Context, _, _ string) (map[string]hmproto.ParameterData, error) {
	return map[string]hmproto.ParameterData{
		"TRANSMIT_TRY_MAX": {
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsDetermine,
			Flags:      hmenum.FlagVisible,
		},
		"ON_TIME": {
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Flags:      hmenum.FlagVisible,
		},
	}, nil
}

func (determineLinkOps) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}

// TestBuildLinkSchemaSetsDetermineFlag exercises buildLinkSchema's own
// Operations assembly (a separate literal from buildParameters') with the
// same true/false pairing.
func TestBuildLinkSchemaSetsDetermineFlag(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-det-link"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DETLINK001",
		Model:       "HmIP-STH",
		Name:        "DETLINK001",
	})
	c.ModelRegistry.Put(d)
	d.AddChannel("DETLINK001:1", 1, "THERMOSTAT", hmenum.ParamsetKeyValues)

	b := &determineLinkOps{}
	w := client.NewValueWriter()
	w.Register("ccu-det-link", "HmIP-RF", b)

	a := NewUISchemaAdapter(reg, w, nil, nil, nil)
	schema, err := a.UISchema(context.Background(), hmapi.UISchemaRequest{
		Address:  "DETLINK001",
		Channel:  1,
		Paramset: "LINK",
		Peer:     "PEER:1",
	})
	if err != nil {
		t.Fatalf("UISchema LINK: %v", err)
	}

	byName := make(map[string]hmapi.UISchemaParameter, len(schema.Parameters))
	for _, p := range schema.Parameters {
		byName[p.Name] = p
	}

	det, ok := byName["TRANSMIT_TRY_MAX"]
	if !ok {
		t.Fatal("TRANSMIT_TRY_MAX missing from link schema parameters")
	}
	if !det.Operations.Determine {
		t.Error("TRANSMIT_TRY_MAX: expected Operations.Determine=true")
	}

	nondet, ok := byName["ON_TIME"]
	if !ok {
		t.Fatal("ON_TIME missing from link schema parameters")
	}
	if nondet.Operations.Determine {
		t.Error("ON_TIME: expected Operations.Determine=false")
	}
}
