// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestDefinitionExporter_Export_TopLevel(t *testing.T) {
	d := newTestDevice(t)
	exp := NewDefinitionExporter(d)
	got := exp.Export()

	if got["address"] != d.Address {
		t.Errorf("address: got %v, want %v", got["address"], d.Address)
	}
	if got["model"] != d.Model {
		t.Errorf("model: got %v, want %v", got["model"], d.Model)
	}
	if got["interface_id"] != d.InterfaceID {
		t.Errorf("interface_id: got %v, want %v", got["interface_id"], d.InterfaceID)
	}
	channels, ok := got["channels"].([]map[string]any)
	if !ok {
		t.Fatalf("channels: expected []map[string]any, got %T", got["channels"])
	}
	// newTestDevice adds :0 and :1 — no root channel
	if len(channels) != 2 {
		t.Errorf("channels count: got %d, want 2", len(channels))
	}
}

func TestDefinitionExporter_Export_WithDataPoints(t *testing.T) {
	d := newTestDevice(t)
	ch1 := d.Channel("0001ABCD:1")
	if ch1 == nil {
		t.Fatal("channel :1 missing")
	}

	// Attach a VALUES DP.
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLowBat),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Unit:       "",
		},
	})
	ch1.Put(dp)

	// Attach a calculated DP (reuse a sensor as a stand-in).
	calcDP := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "CALC_TEST",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})
	ch1.AttachCalculatedDataPoint(calcDP)

	got := NewDefinitionExporter(d).Export()
	channels := got["channels"].([]map[string]any)

	var ch1Export map[string]any
	for _, ch := range channels {
		if ch["address"] == "0001ABCD:1" {
			ch1Export = ch
			break
		}
	}
	if ch1Export == nil {
		t.Fatal("channel :1 not found in export")
	}

	values := ch1Export["values"].([]map[string]any)
	if len(values) != 1 {
		t.Errorf("values count: got %d, want 1", len(values))
	}
	if values[0]["parameter"] != string(hmenum.ParameterLowBat) {
		t.Errorf("parameter: got %v", values[0]["parameter"])
	}

	calcKeys := ch1Export["calculated"].([]string)
	if len(calcKeys) != 1 {
		t.Errorf("calculated count: got %d, want 1", len(calcKeys))
	}
	wantKey := "0001ABCD:1/" + string(hmenum.ParamsetKeyValues) + "/CALC_TEST"
	if calcKeys[0] != wantKey {
		t.Errorf("calculated key: got %v, want %v", calcKeys[0], wantKey)
	}
}
