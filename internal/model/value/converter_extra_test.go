// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package value_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/value"
)

// TestToHomematicValue_Float32 verifies float32 → float64 conversion.
func TestToHomematicValue_Float32(t *testing.T) {
	in := float32(1.5)
	got, ok := value.ToHomematicValue(in).(float64)
	if !ok {
		t.Fatalf("expected float64 result, got %T", value.ToHomematicValue(in))
	}
	if got < 1.499999 || got > 1.500001 {
		t.Errorf("ToHomematicValue(float32(1.5)) = %v, want ~1.5", got)
	}
}

// TestToHomematicValue_Time verifies time.Time → RFC3339 string.
func TestToHomematicValue_Time(t *testing.T) {
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	got, ok := value.ToHomematicValue(ts).(string)
	if !ok {
		t.Fatalf("expected string result, got %T", value.ToHomematicValue(ts))
	}
	if got != "2026-01-15T10:30:00Z" {
		t.Errorf("ToHomematicValue(time.Time) = %q, want 2026-01-15T10:30:00Z", got)
	}
}

// TestFromHomematicValue_Int64ToBool verifies int64 → bool coercion.
func TestFromHomematicValue_Int64ToBool(t *testing.T) {
	tests := []struct {
		in   int64
		want bool
	}{
		{1, true},
		{0, false},
		{-1, true},
	}
	for _, tt := range tests {
		got := value.FromHomematicValue(tt.in, "bool")
		if got != tt.want {
			t.Errorf("FromHomematicValue(%v, bool) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestFromHomematicValue_Float64ToBool verifies float64 → bool coercion.
func TestFromHomematicValue_Float64ToBool(t *testing.T) {
	if got := value.FromHomematicValue(float64(1.0), "bool"); got != true {
		t.Errorf("got %v, want true", got)
	}
	if got := value.FromHomematicValue(float64(0.0), "bool"); got != false {
		t.Errorf("got %v, want false", got)
	}
}

// TestFromHomematicValue_BoolToBool verifies bool → bool passthrough.
func TestFromHomematicValue_BoolToBool(t *testing.T) {
	if got := value.FromHomematicValue(true, "bool"); got != true {
		t.Errorf("got %v, want true", got)
	}
	if got := value.FromHomematicValue(false, "bool"); got != false {
		t.Errorf("got %v, want false", got)
	}
}

// TestFromHomematicValue_ISOTimeWithoutTimezone verifies the fallback
// ISO-without-timezone parser.
func TestFromHomematicValue_ISOTimeWithoutTimezone(t *testing.T) {
	s := "2026-03-10T14:00:00"
	got := value.FromHomematicValue(s, "time.Time")
	ts, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if ts.Year() != 2026 || ts.Month() != 3 || ts.Day() != 10 {
		t.Errorf("parsed = %v, unexpected", ts)
	}
}

// TestFromHomematicValue_BadTimeFallsThrough verifies that an unparseable
// string is returned unchanged when targetType is "time.Time".
