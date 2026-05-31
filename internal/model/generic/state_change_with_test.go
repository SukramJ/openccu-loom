// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func boolDPForStateChange(t *testing.T) *DataPoint[bool] {
	t.Helper()
	return NewDataPoint[bool](Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Kind: KindSwitch,
	})
}

// TestIsStateChangeWith_ZeroOpts returns false when no value has been observed
// and the validateStateChange flag is true (Switch kind).
func TestIsStateChangeWith_ZeroOpts(t *testing.T) {
	dp := boolDPForStateChange(t)
	// No value observed yet, uncertain → should be true even with zero opts.
	if !dp.IsStateChangeWith() {
		t.Fatal("expected true (state uncertain) with zero opts and no observed value")
	}
}

// TestIsStateChangeWith_WithValue_SignalsChange returns true when the candidate
// differs from the confirmed value.
func TestIsStateChangeWith_WithValue_SignalsChange(t *testing.T) {
	dp := boolDPForStateChange(t)
	dp.OnEvent(false)
	if !dp.IsStateChangeWith(WithValue[bool](true)) {
		t.Fatal("expected true: candidate (true) differs from confirmed (false)")
	}
}

// TestIsStateChangeWith_WithValue_NoChange returns false when the candidate
// matches the confirmed value and state is certain.
func TestIsStateChangeWith_WithValue_NoChange(t *testing.T) {
	dp := boolDPForStateChange(t)
	dp.OnEvent(true)
	if dp.IsStateChangeWith(WithValue[bool](true)) {
		t.Fatal("expected false: candidate (true) matches confirmed (true) and state is certain")
	}
}

// TestIsStateChangeWith_MultipleOpts any-of semantics: returns true when at
// least one option signals a change.
func TestIsStateChangeWith_MultipleOpts(t *testing.T) {
	dp := NewDataPoint[int32](Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Kind: KindSensor,
	})
	dp.OnEvent(int32(5))
	// opt1: candidate == confirmed → false; opt2: candidate != confirmed → true.
	opt1 := WithValue[int32](5) // no change
	opt2 := WithValue[int32](7) // change
	if !dp.IsStateChangeWith(opt1, opt2) {
		t.Fatal("expected true: at least one option (opt2) signals a change")
	}
	// Both opts match → false.
	if dp.IsStateChangeWith(opt1, WithValue[int32](5)) {
		t.Fatal("expected false: both options match confirmed value")
	}
}
