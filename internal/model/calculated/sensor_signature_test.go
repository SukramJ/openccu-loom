// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestDewPointSensor_Signature(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensorWithIdentity("ccu1", "VCU:1")
	got := s.Signature()
	const want = "sensor//DEW_POINT"
	if got != want {
		t.Fatalf("DewPointSensor.Signature() = %q, want %q", got, want)
	}
}

func TestDewPointSensor_Signature_WithDeviceModel(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensorWithIdentity("ccu1", "VCU:1")
	s.DeviceModel = "HmIP-STHO"
	got := s.Signature()
	const want = "sensor/HmIP-STHO/DEW_POINT"
	if got != want {
		t.Fatalf("DewPointSensor.Signature() with model = %q, want %q", got, want)
	}
}

func TestDerivedBinarySensor_Signature(t *testing.T) {
	t.Parallel()
	s := NewDerivedBinarySensor(calcParamForTest(), []string{"OPEN"}, nil)
	got := s.Signature()
	// format: binary_sensor/{model}/{calcParam}
	if got == "" {
		t.Fatal("DerivedBinarySensor.Signature() must not be empty")
	}
	if got[0:14] != "binary_sensor/" {
		t.Fatalf("DerivedBinarySensor.Signature() = %q, want prefix binary_sensor/", got)
	}
}

// calcParamForTest returns a CalculatedParameter value for fixture use.
func calcParamForTest() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterDewPoint
}
