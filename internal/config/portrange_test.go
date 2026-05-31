// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"errors"
	"testing"
)

func TestParsePortRangeValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		lo    int
		hi    int
	}{
		{"30000-30099", 30000, 30099},
		{"1024-65535", 1024, 65535},
		{"1-65535", 1, 65535},
		// Degenerate single-port range.
		{"30000-30000", 30000, 30000},
		{"8120-8120", 8120, 8120},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			lo, hi, err := ParsePortRange(tc.input)
			if err != nil {
				t.Fatalf("ParsePortRange(%q) unexpected error: %v", tc.input, err)
			}
			if lo != tc.lo || hi != tc.hi {
				t.Fatalf("ParsePortRange(%q) = (%d, %d), want (%d, %d)", tc.input, lo, hi, tc.lo, tc.hi)
			}
		})
	}
}

func TestParsePortRangeEmpty(t *testing.T) {
	t.Parallel()

	lo, hi, err := ParsePortRange("")
	if err != nil {
		t.Fatalf("empty string: unexpected error: %v", err)
	}
	if lo != 0 || hi != 0 {
		t.Fatalf("empty string: want (0, 0), got (%d, %d)", lo, hi)
	}
}

func TestParsePortRangeErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		desc  string
	}{
		{"100-50", "lo > hi"},
		{"abc", "non-numeric, no hyphen"},
		{"abc-def", "non-numeric values"},
		{"30000-abc", "non-numeric hi"},
		{"abc-30000", "non-numeric lo"},
		{"0-1000", "lo=0 out of range"},
		{"1-65536", "hi=65536 out of range"},
		{"0-65536", "both out of range"},
		{"-100-200", "negative lo (leading hyphen)"},
		{"30000", "no hyphen"},
		{"-30000", "leading hyphen only"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			_, _, err := ParsePortRange(tc.input)
			if err == nil {
				t.Fatalf("ParsePortRange(%q) want error for %s, got nil", tc.input, tc.desc)
			}
			if !errors.Is(err, ErrInvalidPortRange) {
				t.Fatalf("ParsePortRange(%q) error %v does not wrap ErrInvalidPortRange", tc.input, err)
			}
		})
	}
}
