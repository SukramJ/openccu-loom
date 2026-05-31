// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"strings"
	"testing"
	"time"
)

// TestAsInt32HappyPath verifies AsInt32 on a valid IntValue.
func TestAsInt32HappyPath(t *testing.T) {
	t.Parallel()

	got, err := AsInt32(IntValue(42))
	if err != nil {
		t.Fatalf("AsInt32: %v", err)
	}
	if got != 42 {
		t.Fatalf("AsInt32 = %d, want 42", got)
	}
}

// TestAsInt32TypeMismatch checks that passing a non-IntValue returns
// an error.
func TestAsInt32TypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := AsInt32(StringValue("hello")); err == nil {
		t.Fatal("AsInt32(StringValue) must return error")
	}
}

// TestAsBoolTypeMismatch checks the error path for AsBool.
func TestAsBoolTypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := AsBool(IntValue(1)); err == nil {
		t.Fatal("AsBool(IntValue) must return error")
	}
}

// TestAsDoubleTypeMismatch checks the error path for AsDouble.
func TestAsDoubleTypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := AsDouble(BoolValue(true)); err == nil {
		t.Fatal("AsDouble(BoolValue) must return error")
	}
}

// TestAsTimeHappyPath verifies AsTime on a valid DateTimeValue.
func TestAsTimeHappyPath(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := AsTime(DateTimeValue(ts))
	if err != nil {
		t.Fatalf("AsTime: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("AsTime = %v, want %v", got, ts)
	}
}

// TestAsTimeTypeMismatch checks that a non-DateTimeValue is rejected.
func TestAsTimeTypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := AsTime(StringValue("2026-04-28T12:00:00")); err == nil {
		t.Fatal("AsTime(StringValue) must return error")
	}
}

// TestAsBytesTypeMismatch checks the error path for AsBytes.
func TestAsBytesTypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := AsBytes(StringValue("not-base64")); err == nil {
		t.Fatal("AsBytes(StringValue) must return error")
	}
}

// TestAsArrayTypeMismatch checks the error path for AsArray.
func TestAsArrayTypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := AsArray(IntValue(0)); err == nil {
		t.Fatal("AsArray(IntValue) must return error")
	}
}

// TestAsStringsWithNonStringElement verifies that AsStrings returns an
// error when an array element is not a StringValue.
func TestAsStringsWithNonStringElement(t *testing.T) {
	t.Parallel()

	arr := ArrayValue{IntValue(1), StringValue("ok"), BoolValue(false)}
	if _, err := AsStrings(arr); err == nil {
		t.Fatal("AsStrings with mixed types must return error")
	}
}

// TestAsStringsHappyPath verifies the all-strings happy path.
func TestAsStringsHappyPath(t *testing.T) {
	t.Parallel()

	arr := ArrayValue{StringValue("a"), StringValue("b"), StringValue("c")}
	got, err := AsStrings(arr)
	if err != nil {
		t.Fatalf("AsStrings: %v", err)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("AsStrings = %v, want [a b c]", got)
	}
}

// TestTypeMismatchNilValue exercises the nil-value guard inside typeMismatch.
func TestTypeMismatchNilValue(t *testing.T) {
	t.Parallel()

	err := typeMismatch(nil, "int")
	if err == nil {
		t.Fatal("typeMismatch(nil) must return error")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Fatalf("typeMismatch(nil) error = %q, expected 'nil' mention", err)
	}
}
