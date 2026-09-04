// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
)

// TestStMatCelsiusEncoderIsTheMeasurementOne pins that this profile encodes
// temperatures through the one encoder.
//
// It carried a byte-identical copy — same rounding, same clamps, same
// comments. Two encoders for one wire format can be corrected apart: a
// boundary fixed in one leaves the other emitting the old value, and the
// clamps here are the two the Matter spec makes load-bearing (32767 is NULL
// and must never be sent as a reading; -27315 is absolute zero).
func TestStMatCelsiusEncoderIsTheMeasurementOne(t *testing.T) {
	t.Parallel()

	for _, c := range []float64{
		-300, -273.15, -40, -0.005, 0, 0.004, 21.375, 100, 327.66, 327.67, 1000,
	} {
		if got, want := celsiusToMatter(c), measurement.CelsiusToInt16(c); got != want {
			t.Errorf("celsiusToMatter(%v) = %d, measurement.CelsiusToInt16 = %d", c, got, want)
		}
	}
	// The two sentinels the clamps exist for.
	if got := celsiusToMatter(1000); got != 32766 {
		t.Errorf("clamp above range = %d, want 32766 (32767 is NULL)", got)
	}
	if got := celsiusToMatter(-300); got != -27315 {
		t.Errorf("clamp below range = %d, want -27315", got)
	}
}
