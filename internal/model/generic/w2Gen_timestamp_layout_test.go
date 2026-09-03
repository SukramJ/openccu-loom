// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestW2GenTimestampLayoutIsFixedWidth pins the one property that separates
// [timestampLayout] from [time.RFC3339Nano]: the fractional second keeps its
// trailing zeros, so every emitted timestamp has the same width.
//
// The two layouts are indistinguishable on a value with nine significant
// fractional digits, which is why the fixture below uses one that ends in
// zeros — swapping the constant for time.RFC3339Nano is otherwise invisible
// to a test and to a re-parsing consumer, and visible only to whatever
// string-compares or column-aligns the field.
func TestW2GenTimestampLayoutIsFixedWidth(t *testing.T) {
	t.Parallel()

	// .5 s: nine digits when padded, one when trimmed.
	at := time.Date(2031, time.March, 4, 12, 0, 0, 500000000, time.UTC)

	const want = "2031-03-04T12:00:00.500000000Z"
	if got := at.Format(timestampLayout); got != want {
		t.Errorf("timestampLayout formatted %v as %q, want %q — the fractional second must stay "+
			"padded to nine digits; time.RFC3339Nano would give %q",
			at, got, want, at.Format(time.RFC3339Nano))
	}
}

// TestW2GenStatePayloadUsesTheFixedWidthLayout runs the same pin through the
// production emitter rather than the constant, so a caller that formats a time
// field some other way is caught too.
func TestW2GenStatePayloadUsesTheFixedWidthLayout(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterPower, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	at := time.Date(2031, time.March, 4, 12, 0, 0, 500000000, time.UTC)
	dp.OnEventAt(1.25, at)

	st, ok := dp.State().(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("State() returned %T, want *payload.GenericDataPointState", dp.State())
	}
	const want = "2031-03-04T12:00:00.500000000Z"
	if st.ModifiedAt != want {
		t.Errorf("State().modified_at = %q, want %q (fixed-width fractional second)", st.ModifiedAt, want)
	}
}
