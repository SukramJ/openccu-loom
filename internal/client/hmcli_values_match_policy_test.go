// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import "testing"

// TestHmCliValuesMatchTwoDecimalBucketIsAPolicy measures what the fixed
// 2-decimal float bucket in [valuesMatch] actually does against real CCU
// quantisation, so the rule cannot be read as a wire fact.
//
// The firmware quantises a float write to round((v+offset)*factor) and echoes
// physical/factor - offset, with `factor` declared per parameter in the
// device-type XML and never exposed over getParamsetDescription. The two cases
// below are both real parameters, and they fail in OPPOSITE directions — that
// is the point: no single fixed decimal count can be right, so the constant is
// a policy, not a measurement.
//
// If a future change derives the tolerance from a per-parameter quantum
// instead, these expectations flip, and that is the signal to update the
// documented rule in state_change.go with it.
func TestHmCliValuesMatchTwoDecimalBucketIsAPolicy(t *testing.T) {
	t.Parallel()

	// factor=2 — SET_TEMPERATURE on a wall thermostat (rf_cc_rt_dn.xml:
	// <conversion type="float_integer_scale" factor="2"/>). A write of 20.3
	// quantises to round(40.6)=41 and is echoed as 20.5, half a step away.
	// The bucket rejects the device's own nearest representable setpoint.
	if valuesMatch(20.3, 20.5) {
		t.Error("valuesMatch(20.3, 20.5) = true; the 2-decimal bucket is expected to REJECT a factor=2 echo — if this now matches, the tolerance rule changed and its doc comment must change with it")
	}

	// factor=200 — LEVEL / LEVEL_SLATS on blinds (rf_ja_conf_644.xml:
	// <conversion type="float_integer_scale" factor="200"/>). Physical 23 and
	// 24 are two distinct positions, 0.115 and 0.12; 0.115*100 is exactly
	// 11.5 in binary64 and math.Round is half-away-from-zero, so both land in
	// bucket 0.12 and an echo of the wrong position confirms the write.
	if !valuesMatch(0.12, 0.115) {
		t.Error("valuesMatch(0.12, 0.115) = false; the 2-decimal bucket is expected to CONFIRM two distinct factor=200 positions — if this no longer matches, the tolerance rule changed and its doc comment must change with it")
	}

	// The bucket is genuinely a bucket, not an equality test: this is the
	// negative control for the two cases above, and it is what a caller
	// relies on when the CCU re-serialises a value with "%f".
	if !valuesMatch(1.005, 1.0049) {
		t.Error("valuesMatch(1.005, 1.0049) = false; want the 2-decimal bucket to absorb sub-quantum wire noise")
	}
	if valuesMatch(1.0, 2.0) {
		t.Error("valuesMatch(1.0, 2.0) = true; want distinct values to stay distinct")
	}
}
