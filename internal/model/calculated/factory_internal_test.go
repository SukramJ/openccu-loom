// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package calculated tests factory.go and climate.go internal branches:
// nil channel guard, DerivedBinary channel/parameter mismatch paths,
// full DerivedBinary creation path, and derivedFloatSensorUnit default branch.
package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// factory.go — nil channel guard
// ---------------------------------------------------------------------------

func TestCreateCalculatedDataPointsNilChannel(t *testing.T) {
	result := CreateCalculatedDataPoints(nil, "HmIP-SRH")
	if result != nil {
		t.Fatalf("CreateCalculatedDataPoints(nil, ...) must return nil, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// factory.go — DerivedBinary mapping: AppliesToChannel skip
// ---------------------------------------------------------------------------

func TestCreateCalculatedDataPointsDerivedBinaryChannelMismatch(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SRH0001", Model: "HmIP-SRH"})
	ch := d.AddChannel("SRH0001:2", 2, "SHUTTER_CONTACT", hmenum.ParamsetKeyValues)
	dp := generic.NewStringSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterState)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(dp)

	sensors := CreateCalculatedDataPoints(ch, "HmIP-SRH")
	for _, s := range sensors {
		if s.CalculatedParameter() == hmenum.CalculatedParameterWindowOpen {
			t.Error("WindowOpen must not be created for channel number 2 (mapping requires channel 1)")
		}
	}
}

// ---------------------------------------------------------------------------
// factory.go — DerivedBinary mapping: HasParameter skip
// ---------------------------------------------------------------------------

func TestCreateCalculatedDataPointsDerivedBinaryMissingParam(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SRH0002", Model: "HmIP-SRH"})
	ch := d.AddChannel("SRH0002:1", 1, "SHUTTER_CONTACT", hmenum.ParamsetKeyValues)
	// No STATE parameter added — mapping must be skipped.

	sensors := CreateCalculatedDataPoints(ch, "HmIP-SRH")
	for _, s := range sensors {
		if s.CalculatedParameter() == hmenum.CalculatedParameterWindowOpen {
			t.Error("WindowOpen must not be created when STATE param is absent from channel")
		}
	}
}

// ---------------------------------------------------------------------------
// factory.go — DerivedBinary full path: correct model + channel + param
// ---------------------------------------------------------------------------

func TestCreateCalculatedDataPointsDerivedBinaryCreated(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SRH0003", Model: "HmIP-SRH"})
	ch := d.AddChannel("SRH0003:1", 1, "SHUTTER_CONTACT", hmenum.ParamsetKeyValues)
	dp := generic.NewStringSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterState)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(dp)

	sensors := CreateCalculatedDataPoints(ch, "HmIP-SRH")
	found := false
	for _, s := range sensors {
		if s.CalculatedParameter() == hmenum.CalculatedParameterWindowOpen {
			found = true
		}
	}
	if !found {
		t.Error("WindowOpen must be created for HmIP-SRH channel 1 with STATE param present")
	}
}

// ---------------------------------------------------------------------------
// climate.go — derivedFloatSensorUnit default branch
// ---------------------------------------------------------------------------

func TestDerivedFloatSensorUnitDefaultBranch(t *testing.T) {
	unknown := hmenum.CalculatedParameter("FUTURE_UNKNOWN")
	got := derivedFloatSensorUnit(unknown)
	if got != "" {
		t.Errorf("derivedFloatSensorUnit(unknown)=%q, want empty string", got)
	}
}
