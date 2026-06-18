// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// status_value_validity_contract_test.go locks the invariant that STATUS=UNKNOWN
// is handled asymmetrically depending on whether the parameter is a control
// actuator or a numeric physical measurement.
//
// After a CCU restart the CCU pushes every parameter's DEFAULT value paired
// with STATUS=UNKNOWN as an init-phase placeholder. Two upstream issues capture
// the tension:
//
//   - Issue #2630: a CONTROL parameter such as LEVEL reports STATUS=UNKNOWN
//     but its value (e.g. 0.0 = "fully closed") is still meaningful. The DP
//     must remain VALID and north-bound available so automations can read the
//     actuator position.
//
//   - Issue #3228: a MEASURED parameter such as ACTUAL_TEMPERATURE reports
//     STATUS=UNKNOWN because no real sensor reading has arrived yet — the
//     pushed 0.0 is only the DEFAULT placeholder, not a real temperature.
//     The DP must become INVALID and north-bound unavailable so consumers do
//     not act on a phantom "0 °C" reading.
//
// The discriminator is DataPoint.IsStatusValid() + isMeasuredQuantity():
// UNKNOWN is invalid only when the descriptor type is FLOAT or INTEGER AND
// the parameter resolves to a non-None physical Quantity (e.g.
// QuantityTemperature for ACTUAL_TEMPERATURE). LEVEL has no Quantity and is
// therefore not a measured parameter — UNKNOWN stays valid for it.
//
// The north-bound State() payload gates its Available field on IsStatusValid(),
// so these tests cover the full stack from wire status to published availability.
package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestStatusValidityContract_ControlLevelUnknownStaysValid guards issue #2630:
// a LEVEL data point (control actuator, no physical Quantity) with STATUS=UNKNOWN
// must remain valid and available. The actuator position is real even when the
// init-phase placeholder status has not yet been resolved.
func TestStatusValidityContract_ControlLevelUnknownStaysValid(t *testing.T) {
	t.Parallel()

	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
	})
	dp.OnWireValue(0.0)
	dp.UpdateStatus(hmenum.ParameterStatusUnknown)

	if !dp.IsStatusValid() {
		t.Fatalf("LEVEL + STATUS=UNKNOWN: IsStatusValid() = false; want true (#2630 regression)")
	}

	st, ok := dp.State().(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("State() did not return *payload.GenericDataPointState")
	}
	if !st.Available {
		t.Fatalf("LEVEL + STATUS=UNKNOWN: st.Available = false; want true (#2630 regression)")
	}
}

// TestStatusValidityContract_MeasuredTemperatureNormalIsValid asserts the
// baseline: ACTUAL_TEMPERATURE with STATUS=NORMAL is valid and available.
func TestStatusValidityContract_MeasuredTemperatureNormalIsValid(t *testing.T) {
	t.Parallel()

	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
	})
	dp.OnWireValue(21.5)
	dp.UpdateStatus(hmenum.ParameterStatusNormal)

	if !dp.IsStatusValid() {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=NORMAL: IsStatusValid() = false; want true")
	}

	st, ok := dp.State().(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("State() did not return *payload.GenericDataPointState")
	}
	if !st.Available {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=NORMAL: st.Available = false; want true")
	}
}

// TestStatusValidityContract_MeasuredTemperatureUnknownIsInvalid guards
// issue #3228: a ACTUAL_TEMPERATURE data point (numeric physical measurement,
// Quantity=QuantityTemperature) with STATUS=UNKNOWN must be invalid and
// unavailable. The pushed 0.0 value is only the DEFAULT placeholder — not a
// real sensor reading — and must not propagate to north-bound consumers.
func TestStatusValidityContract_MeasuredTemperatureUnknownIsInvalid(t *testing.T) {
	t.Parallel()

	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
	})
	dp.OnWireValue(0.0)
	dp.UpdateStatus(hmenum.ParameterStatusUnknown)

	if dp.IsStatusValid() {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=UNKNOWN: IsStatusValid() = true; want false (#3228 regression)")
	}

	st, ok := dp.State().(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("State() did not return *payload.GenericDataPointState")
	}
	if st.Available {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=UNKNOWN: st.Available = true; want false (#3228 regression)")
	}
}
