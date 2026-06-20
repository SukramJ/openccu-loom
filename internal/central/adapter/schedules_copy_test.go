// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// CopyClimateProfile
// ---------------------------------------------------------------------------

// TestCopyClimateProfile_HappyPath verifies that a profile lifted from the
// source channel is written under the destination profile name on the same
// channel.  The source fixture only carries P1 keys; after a P1→P2 copy the
// backend must receive exactly one PutParamset with keys prefixed "P2_".
func TestCopyClimateProfile_HappyPath(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	if err := domain.CopyClimateProfile(
		t.Context(),
		"0001ABCD:1", 1,
		"0001ABCD:1", 2,
	); err != nil {
		t.Fatalf("CopyClimateProfile P1→P2: %v", err)
	}

	if got := backend.putCallCount(); got != 1 {
		t.Errorf("put calls: got %d, want 1", got)
	}
	written := backend.lastPut("0001ABCD:1")
	if written == nil {
		t.Fatal("nothing written to destination channel")
	}
	// Every key written must start with "P2_"; no P1_ leakage allowed.
	for k := range written {
		if !strings.HasPrefix(k, "P2_") {
			t.Errorf("unexpected key %q in destination (want P2_*)", k)
		}
	}
	// Spot-check a concrete key that the fixture carries.
	if _, ok := written["P2_ENDTIME_MONDAY_1"]; !ok {
		t.Errorf("P2_ENDTIME_MONDAY_1 missing in written payload: %v", written)
	}
}

// TestCopyClimateProfile_NoOp verifies that copying a profile to the same
// channel/profile slot is rejected as a no-op.
func TestCopyClimateProfile_NoOp(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	err := domain.CopyClimateProfile(
		t.Context(),
		"0001ABCD:1", 1,
		"0001ABCD:1", 1, // same channel, same profile → no-op
	)
	if !errors.Is(err, ErrScheduleCopyNoOp) {
		t.Errorf("got %v, want ErrScheduleCopyNoOp", err)
	}
}

// TestCopyClimateProfile_ProfileOutOfRange verifies that profile indices
// outside [1,6] are rejected immediately.
func TestCopyClimateProfile_ProfileOutOfRange(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	cases := []struct {
		name string
		srcP int
		dstP int
	}{
		{"srcZero", 0, 2},
		{"dstSeven", 1, 7},
		{"bothBad", 0, 7},
		{"srcNeg", -1, 2},
		{"dstEight", 1, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := domain.CopyClimateProfile(
				t.Context(),
				"0001ABCD:1", tc.srcP,
				"0001ABCD:1", tc.dstP,
			)
			if !errors.Is(err, ErrScheduleCopyProfileRange) {
				t.Errorf("srcP=%d dstP=%d: got %v, want ErrScheduleCopyProfileRange",
					tc.srcP, tc.dstP, err)
			}
		})
	}
}

// TestCopyClimateProfile_MissingSourceProfile verifies that requesting a
// profile the source channel does not carry results in an error whose message
// mentions "no profile".
func TestCopyClimateProfile_MissingSourceProfile(t *testing.T) {
	t.Parallel()
	// Fixture only carries P1 keys; request P3.
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	err := domain.CopyClimateProfile(
		t.Context(),
		"0001ABCD:1", 3, // P3 absent from the fixture
		"0001ABCD:1", 2,
	)
	if err == nil {
		t.Fatal("expected error for missing source profile P3, got nil")
	}
	if !strings.Contains(err.Error(), "no profile") {
		t.Errorf("error message should mention 'no profile'; got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CopySchedule
// ---------------------------------------------------------------------------

// TestCopySchedule_NoOp verifies that copying a device's schedule to itself
// is rejected immediately.
func TestCopySchedule_NoOp(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	err := domain.CopySchedule(t.Context(), "0001ABCD", "0001ABCD")
	if !errors.Is(err, ErrScheduleCopyNoOp) {
		t.Errorf("got %v, want ErrScheduleCopyNoOp", err)
	}
}

// TestCopySchedule_UnknownSourceErrors verifies that CopySchedule wraps a
// read-path failure with the "copy read source" prefix when the source device
// is not registered in any central.
func TestCopySchedule_UnknownSourceErrors(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	// "UNKNOWNDEV" is not registered → GetClimateScheduleAuto returns an error
	// and CopySchedule wraps it with "copy read source".
	err := domain.CopySchedule(t.Context(), "UNKNOWNDEV", "0001ABCD")
	if err == nil {
		t.Fatal("expected error when source device is unknown, got nil")
	}
	if !strings.Contains(err.Error(), "copy read source") {
		t.Errorf("error should mention 'copy read source'; got: %v", err)
	}
}

// TestCopySchedule_SourceNoScheduleErrors verifies that CopySchedule wraps
// the ErrNoSchedule error with the "copy read source" prefix when the
// registered source device has no climate schedule channel.
func TestCopySchedule_SourceNoScheduleErrors(t *testing.T) {
	t.Parallel()
	// buildScheduleIOFixture registers "0001ABCD" but without explicit channels,
	// so FindScheduleChannel cannot locate a schedule channel and returns
	// ErrNoSchedule.  CopySchedule must wrap this under "copy read source".
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	err := domain.CopySchedule(t.Context(), "0001ABCD", "0009XXXX")
	if err == nil {
		t.Fatal("expected error when source device has no schedule channel, got nil")
	}
	// The error originates from GetClimateScheduleAuto and is wrapped by
	// CopySchedule with "copy read source".
	if !strings.Contains(err.Error(), "copy read source") {
		t.Errorf("error should mention 'copy read source'; got: %v", err)
	}
}
