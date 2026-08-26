// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// status_value_validity_contract_test.go locks the invariant that the
// north-bound `available` flag is gated on the full IsValid() chain:
// refreshed + STATUS valid + value type ok + value in range.
//
// STATUS validity follows reference parity (issues #2630 and #2634):
// NORMAL and UNKNOWN are both treated as valid for ALL parameters.
// There is no measured-vs-control discriminator.
//
// The spurious 0.0/0°C reading after a CCU restart (issue #3228) is
// prevented at the seed-script source
// (internal/client/rega/scripts/fetch_all_device_data.fn), which skips
// empty numeric values instead of coercing them to "0". The status gate
// intentionally leaves UNKNOWN valid so that control actuators whose
// LEVEL value is real (e.g. 0.0 = fully closed) remain available during
// the CCU init phase.
//
// OVERFLOW and UNDERFLOW indicate a sensor fault and make IsStatusValid()
// return false, driving available=false.
package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestStatusValidityContract_ControlLevelUnknownStaysValid guards the
// general UNKNOWN rule (reference issue #2630): any data point with
// STATUS=UNKNOWN must remain valid and north-bound available. The
// actuator position is real even when the CCU init-phase placeholder
// status has not yet resolved to NORMAL.
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

// TestStatusValidityContract_MeasuredTemperatureUnknownStaysValid asserts
// that ACTUAL_TEMPERATURE + STATUS=UNKNOWN is valid and available. This is
// reference parity (#2630): UNKNOWN is valid for all parameters. The
// spurious 0.0 after a CCU restart (#3228) is prevented at the seed script
// (fetch_all_device_data.fn), not by the status gate — so the status gate
// must NOT suppress an UNKNOWN-status temperature value.
func TestStatusValidityContract_MeasuredTemperatureUnknownStaysValid(t *testing.T) {
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

	if !dp.IsStatusValid() {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=UNKNOWN: IsStatusValid() = false; want true")
	}

	st, ok := dp.State().(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("State() did not return *payload.GenericDataPointState")
	}
	if !st.Available {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=UNKNOWN: st.Available = false; want true")
	}
}

// TestStatusValidityContract_OverflowStatusIsUnavailable demonstrates the
// IsValid gate doing real work: OVERFLOW indicates a sensor fault, so
// IsStatusValid() must return false and the north-bound slot must be
// unavailable regardless of the observed value.
func TestStatusValidityContract_OverflowStatusIsUnavailable(t *testing.T) {
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
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)

	if dp.IsStatusValid() {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=OVERFLOW: IsStatusValid() = true; want false")
	}

	st, ok := dp.State().(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("State() did not return *payload.GenericDataPointState")
	}
	if st.Available {
		t.Fatalf("ACTUAL_TEMPERATURE + STATUS=OVERFLOW: st.Available = true; want false")
	}
}
