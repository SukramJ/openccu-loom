// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- MASTER-side helper builders -----------------------------------

// makeFloatMaster builds a *generic.Float attached to the MASTER paramset.
func makeFloatMaster(channelAddr string, p hmenum.Parameter) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
}

// makeIntegerMaster builds a *generic.Integer attached to the MASTER paramset.
func makeIntegerMaster(channelAddr string, p hmenum.Parameter) *generic.Integer {
	return generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// makeSwitchMaster builds a *generic.Switch attached to the MASTER paramset.
func makeSwitchMaster(channelAddr string, p hmenum.Parameter) *generic.Switch {
	return generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
}

// makeBinarySensorMaster builds a *generic.BinarySensor attached to the MASTER paramset.
func makeBinarySensorMaster(channelAddr string, p hmenum.Parameter) *generic.BinarySensor {
	return generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// makeFloatSensorMaster builds a *generic.Sensor[float64] attached to the MASTER paramset.
func makeFloatSensorMaster(channelAddr string, p hmenum.Parameter) *generic.Sensor[float64] {
	return generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// makeIntegerSensorMaster builds a *generic.Sensor[int32] attached to the MASTER paramset.
func makeIntegerSensorMaster(channelAddr string, p hmenum.Parameter) *generic.Sensor[int32] {
	return generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// makeStringSensorMaster builds a *generic.Sensor[string] attached to the MASTER paramset.
func makeStringSensorMaster(channelAddr string, p hmenum.Parameter) *generic.Sensor[string] {
	return generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// VALUES-only sensor helpers (mirrors the VALUES pattern for sensor types).
func makeFloatSensor(channelAddr string, p hmenum.Parameter) *generic.Sensor[float64] {
	return generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

func makeIntegerSensor(channelAddr string, p hmenum.Parameter) *generic.Sensor[int32] {
	return generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

func makeStringSensor(channelAddr string, p hmenum.Parameter) *generic.Sensor[string] {
	return generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// =====================================================================
// Cluster E — MASTER-paramset fallback for *Field accessors
// =====================================================================

// TestResolveFloatFieldMasterFallback covers the four fallback scenarios
// for FloatField: MASTER-only, VALUES wins over MASTER, MASTER type
// mismatch, and VALUES type mismatch + MASTER correct.
func TestResolveFloatFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("FLT:1")
		dp := makeFloatMaster("FLT:1", hmenum.ParameterTemperatureMinimum)
		ch.PutMaster(dp)

		got := FloatField(ch, hmenum.ParameterTemperatureMinimum)
		if got == nil {
			t.Fatal("FloatField returned nil, want MASTER Float DP")
		}
		if got != dp {
			t.Errorf("FloatField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("FLT:2")
		valuesDp := makeFloat("FLT:2", hmenum.ParameterTemperatureMinimum)
		masterDp := makeFloatMaster("FLT:2", hmenum.ParameterTemperatureMinimum)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := FloatField(ch, hmenum.ParameterTemperatureMinimum)
		if got == nil {
			t.Fatal("FloatField returned nil, want VALUES Float DP")
		}
		if got != valuesDp {
			t.Errorf("FloatField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("FLT:3")
		ch.PutMaster(makeIntegerMaster("FLT:3", hmenum.ParameterTemperatureMinimum))

		if got := FloatField(ch, hmenum.ParameterTemperatureMinimum); got != nil {
			t.Errorf("FloatField with Integer in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// When VALUES carries a wrong-typed DP for the same parameter,
		// resolveDP returns that VALUES entry and the type assertion fails.
		// The MASTER entry is NOT consulted — the fallback only activates
		// when ch.Parameter(p) returns nil (parameter absent in VALUES).
		// This mirrors
		// the VALUES entry unconditionally when present, leaving type
		// discrimination to the caller.
		t.Parallel()
		ch := makeChannel("FLT:4")
		ch.Put(makeInteger("FLT:4", hmenum.ParameterTemperatureMinimum))
		ch.PutMaster(makeFloatMaster("FLT:4", hmenum.ParameterTemperatureMinimum))

		// VALUES has wrong type → nil is returned; MASTER is not reached.
		if got := FloatField(ch, hmenum.ParameterTemperatureMinimum); got != nil {
			t.Errorf("FloatField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}

// TestResolveIntegerFieldMasterFallback covers the four fallback scenarios
// for IntegerField.
func TestResolveIntegerFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("INT:1")
		dp := makeIntegerMaster("INT:1", hmenum.ParameterTemperatureMinimum)
		ch.PutMaster(dp)

		got := IntegerField(ch, hmenum.ParameterTemperatureMinimum)
		if got == nil {
			t.Fatal("IntegerField returned nil, want MASTER Integer DP")
		}
		if got != dp {
			t.Errorf("IntegerField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("INT:2")
		valuesDp := makeInteger("INT:2", hmenum.ParameterTemperatureMinimum)
		masterDp := makeIntegerMaster("INT:2", hmenum.ParameterTemperatureMinimum)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := IntegerField(ch, hmenum.ParameterTemperatureMinimum)
		if got == nil {
			t.Fatal("IntegerField returned nil, want VALUES Integer DP")
		}
		if got != valuesDp {
			t.Errorf("IntegerField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("INT:3")
		ch.PutMaster(makeFloatMaster("INT:3", hmenum.ParameterTemperatureMinimum))

		if got := IntegerField(ch, hmenum.ParameterTemperatureMinimum); got != nil {
			t.Errorf("IntegerField with Float in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// Same contract as FloatField: wrong-type VALUES entry blocks the
		// MASTER fallback; resolveDP returns the VALUES DP, the assertion
		// fails, and the helper returns nil.
		t.Parallel()
		ch := makeChannel("INT:4")
		ch.Put(makeFloat("INT:4", hmenum.ParameterTemperatureMinimum))
		ch.PutMaster(makeIntegerMaster("INT:4", hmenum.ParameterTemperatureMinimum))

		if got := IntegerField(ch, hmenum.ParameterTemperatureMinimum); got != nil {
			t.Errorf("IntegerField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}

// TestResolveSwitchFieldMasterFallback covers the four fallback scenarios
// for SwitchField.
func TestResolveSwitchFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("SW:1")
		dp := makeSwitchMaster("SW:1", hmenum.ParameterState)
		ch.PutMaster(dp)

		got := SwitchField(ch, hmenum.ParameterState)
		if got == nil {
			t.Fatal("SwitchField returned nil, want MASTER Switch DP")
		}
		if got != dp {
			t.Errorf("SwitchField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("SW:2")
		valuesDp := makeSwitch("SW:2", hmenum.ParameterState)
		masterDp := makeSwitchMaster("SW:2", hmenum.ParameterState)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := SwitchField(ch, hmenum.ParameterState)
		if got == nil {
			t.Fatal("SwitchField returned nil, want VALUES Switch DP")
		}
		if got != valuesDp {
			t.Errorf("SwitchField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("SW:3")
		// BinarySensor is a different type even though both are bool-backed.
		ch.PutMaster(makeBinarySensorMaster("SW:3", hmenum.ParameterState))

		if got := SwitchField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("SwitchField with BinarySensor in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// Wrong-type VALUES entry (BinarySensor) blocks the MASTER Switch
		// fallback — resolveDP returns the VALUES entry; assertion fails → nil.
		t.Parallel()
		ch := makeChannel("SW:4")
		ch.Put(makeBinarySensor("SW:4", hmenum.ParameterState))
		ch.PutMaster(makeSwitchMaster("SW:4", hmenum.ParameterState))

		if got := SwitchField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("SwitchField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}

// TestResolveBinarySensorFieldMasterFallback covers the four fallback
// scenarios for BinarySensorField.
func TestResolveBinarySensorFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("BS:1")
		dp := makeBinarySensorMaster("BS:1", hmenum.ParameterState)
		ch.PutMaster(dp)

		got := BinarySensorField(ch, hmenum.ParameterState)
		if got == nil {
			t.Fatal("BinarySensorField returned nil, want MASTER BinarySensor DP")
		}
		if got != dp {
			t.Errorf("BinarySensorField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("BS:2")
		valuesDp := makeBinarySensor("BS:2", hmenum.ParameterState)
		masterDp := makeBinarySensorMaster("BS:2", hmenum.ParameterState)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := BinarySensorField(ch, hmenum.ParameterState)
		if got == nil {
			t.Fatal("BinarySensorField returned nil, want VALUES BinarySensor DP")
		}
		if got != valuesDp {
			t.Errorf("BinarySensorField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("BS:3")
		// Switch is a different type even though both are bool-backed.
		ch.PutMaster(makeSwitchMaster("BS:3", hmenum.ParameterState))

		if got := BinarySensorField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("BinarySensorField with Switch in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// Wrong-type VALUES entry (Switch) blocks the MASTER BinarySensor
		// fallback — resolveDP returns the VALUES entry; assertion fails → nil.
		t.Parallel()
		ch := makeChannel("BS:4")
		ch.Put(makeSwitch("BS:4", hmenum.ParameterState))
		ch.PutMaster(makeBinarySensorMaster("BS:4", hmenum.ParameterState))

		if got := BinarySensorField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("BinarySensorField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}

// TestResolveFloatSensorFieldMasterFallback covers the four fallback
// scenarios for FloatSensorField.
func TestResolveFloatSensorFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("FSN:1")
		dp := makeFloatSensorMaster("FSN:1", hmenum.ParameterLevel)
		ch.PutMaster(dp)

		got := FloatSensorField(ch, hmenum.ParameterLevel)
		if got == nil {
			t.Fatal("FloatSensorField returned nil, want MASTER Sensor[float64] DP")
		}
		if got != dp {
			t.Errorf("FloatSensorField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("FSN:2")
		valuesDp := makeFloatSensor("FSN:2", hmenum.ParameterLevel)
		masterDp := makeFloatSensorMaster("FSN:2", hmenum.ParameterLevel)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := FloatSensorField(ch, hmenum.ParameterLevel)
		if got == nil {
			t.Fatal("FloatSensorField returned nil, want VALUES Sensor[float64] DP")
		}
		if got != valuesDp {
			t.Errorf("FloatSensorField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("FSN:3")
		ch.PutMaster(makeIntegerSensorMaster("FSN:3", hmenum.ParameterLevel))

		if got := FloatSensorField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("FloatSensorField with Sensor[int32] in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// Wrong-type VALUES entry (Sensor[int32]) blocks MASTER Sensor[float64]
		// fallback — resolveDP returns the VALUES entry; assertion fails → nil.
		t.Parallel()
		ch := makeChannel("FSN:4")
		ch.Put(makeIntegerSensor("FSN:4", hmenum.ParameterLevel))
		ch.PutMaster(makeFloatSensorMaster("FSN:4", hmenum.ParameterLevel))

		if got := FloatSensorField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("FloatSensorField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}

// TestResolveIntegerSensorFieldMasterFallback covers the four fallback
// scenarios for IntegerSensorField.
func TestResolveIntegerSensorFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("ISN:1")
		dp := makeIntegerSensorMaster("ISN:1", hmenum.ParameterLevel)
		ch.PutMaster(dp)

		got := IntegerSensorField(ch, hmenum.ParameterLevel)
		if got == nil {
			t.Fatal("IntegerSensorField returned nil, want MASTER Sensor[int32] DP")
		}
		if got != dp {
			t.Errorf("IntegerSensorField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("ISN:2")
		valuesDp := makeIntegerSensor("ISN:2", hmenum.ParameterLevel)
		masterDp := makeIntegerSensorMaster("ISN:2", hmenum.ParameterLevel)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := IntegerSensorField(ch, hmenum.ParameterLevel)
		if got == nil {
			t.Fatal("IntegerSensorField returned nil, want VALUES Sensor[int32] DP")
		}
		if got != valuesDp {
			t.Errorf("IntegerSensorField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("ISN:3")
		ch.PutMaster(makeFloatSensorMaster("ISN:3", hmenum.ParameterLevel))

		if got := IntegerSensorField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("IntegerSensorField with Sensor[float64] in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// Wrong-type VALUES entry (Sensor[float64]) blocks MASTER Sensor[int32]
		// fallback — resolveDP returns the VALUES entry; assertion fails → nil.
		t.Parallel()
		ch := makeChannel("ISN:4")
		ch.Put(makeFloatSensor("ISN:4", hmenum.ParameterLevel))
		ch.PutMaster(makeIntegerSensorMaster("ISN:4", hmenum.ParameterLevel))

		if got := IntegerSensorField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("IntegerSensorField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}

// TestResolveStringSensorFieldMasterFallback covers the four fallback
// scenarios for StringSensorField.
func TestResolveStringSensorFieldMasterFallback(t *testing.T) {
	t.Parallel()

	t.Run("MasterOnly", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("SSN:1")
		dp := makeStringSensorMaster("SSN:1", hmenum.ParameterState)
		ch.PutMaster(dp)

		got := StringSensorField(ch, hmenum.ParameterState)
		if got == nil {
			t.Fatal("StringSensorField returned nil, want MASTER Sensor[string] DP")
		}
		if got != dp {
			t.Errorf("StringSensorField returned wrong pointer: got %p, want %p", got, dp)
		}
	})

	t.Run("ValuesWinsOverMaster", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("SSN:2")
		valuesDp := makeStringSensor("SSN:2", hmenum.ParameterState)
		masterDp := makeStringSensorMaster("SSN:2", hmenum.ParameterState)
		ch.Put(valuesDp)
		ch.PutMaster(masterDp)

		got := StringSensorField(ch, hmenum.ParameterState)
		if got == nil {
			t.Fatal("StringSensorField returned nil, want VALUES Sensor[string] DP")
		}
		if got != valuesDp {
			t.Errorf("StringSensorField did not prefer VALUES: got %p, want %p (values)", got, valuesDp)
		}
	})

	t.Run("MasterTypeMismatch", func(t *testing.T) {
		t.Parallel()
		ch := makeChannel("SSN:3")
		ch.PutMaster(makeIntegerSensorMaster("SSN:3", hmenum.ParameterState))

		if got := StringSensorField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("StringSensorField with Sensor[int32] in MASTER returned non-nil: %v", got)
		}
	})

	t.Run("ValuesTypeMismatchBlocksMasterFallback", func(t *testing.T) {
		// Wrong-type VALUES entry (Sensor[int32]) blocks MASTER Sensor[string]
		// fallback — resolveDP returns the VALUES entry; assertion fails → nil.
		t.Parallel()
		ch := makeChannel("SSN:4")
		ch.Put(makeIntegerSensor("SSN:4", hmenum.ParameterState))
		ch.PutMaster(makeStringSensorMaster("SSN:4", hmenum.ParameterState))

		if got := StringSensorField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("StringSensorField with wrong-type VALUES entry returned non-nil: %v", got)
		}
	})
}
