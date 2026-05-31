// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"testing"
)

// TestFindGroupByMembersNoMatch exercises the nil-return path when no
// group's member set matches the supplied set.
func TestFindGroupByMembersNoMatch(t *testing.T) {
	t.Parallel()

	groups := []SubsetGroup{
		{MemberParams: []string{"A", "B"}},
		{MemberParams: []string{"C"}},
	}
	// Two members — matches neither existing group.
	result := findGroupByMembers(groups, toStringSet([]string{"X", "Y"}))
	if result != nil {
		t.Fatalf("findGroupByMembers should return nil for no match, got %+v", result)
	}
}

// TestFindGroupByMembersExactMatch verifies that an exact member-set
// match returns a pointer to the correct group.
func TestFindGroupByMembersExactMatch(t *testing.T) {
	t.Parallel()

	groups := []SubsetGroup{
		{MemberParams: []string{"A", "B"}, ID: "g1"},
		{MemberParams: []string{"C", "D"}, ID: "g2"},
	}
	result := findGroupByMembers(groups, toStringSet([]string{"C", "D"}))
	if result == nil {
		t.Fatal("findGroupByMembers should return a group for an exact match")
	}
	if result.ID != "g2" {
		t.Fatalf("findGroupByMembers returned group %q, want g2", result.ID)
	}
}

// TestFindGroupByMembersPartialNoMatch ensures that a subset is not
// treated as a match (length guard must fire first).
func TestFindGroupByMembersPartialNoMatch(t *testing.T) {
	t.Parallel()

	groups := []SubsetGroup{
		{MemberParams: []string{"A", "B", "C"}, ID: "g1"},
	}
	// Only 2 of 3 members supplied — lengths differ → must return nil.
	result := findGroupByMembers(groups, toStringSet([]string{"A", "B"}))
	if result != nil {
		t.Fatalf("findGroupByMembers should not match on partial member set")
	}
}

// TestFirstOrEmptyWithSlice exercises the happy-path branch of firstOrEmpty.
func TestFirstOrEmptyWithSlice(t *testing.T) {
	t.Parallel()

	if got := firstOrEmpty([]string{"alpha", "beta"}); got != "alpha" {
		t.Fatalf("firstOrEmpty = %q, want alpha", got)
	}
}

// TestFirstOrEmptyEmptySlice exercises the nil/empty guard.
func TestFirstOrEmptyEmptySlice(t *testing.T) {
	t.Parallel()

	if got := firstOrEmpty(nil); got != "" {
		t.Fatalf("firstOrEmpty(nil) = %q, want empty", got)
	}
	if got := firstOrEmpty([]string{}); got != "" {
		t.Fatalf("firstOrEmpty([]) = %q, want empty", got)
	}
}

// TestAllMatchNumericLoose exercises the numeric loose-comparison path:
// float32 vs float64 values that represent the same number.
func TestAllMatchNumericLoose(t *testing.T) {
	t.Parallel()

	want := map[string]any{"X": float64(1.0)}
	current := map[string]any{"X": float32(1.0)} // different concrete type
	if !allMatch(want, current) {
		t.Fatal("allMatch should accept numerically equal values of different float types")
	}
}

// TestAllMatchMissingKey verifies that a missing key causes allMatch to
// return false.
func TestAllMatchMissingKey(t *testing.T) {
	t.Parallel()

	want := map[string]any{"A": 1.0, "B": 2.0}
	current := map[string]any{"A": 1.0}
	if allMatch(want, current) {
		t.Fatal("allMatch should return false when a wanted key is absent")
	}
}

// TestAllMatchTypeMismatch exercises the path where values are
// non-numeric and not equal.
func TestAllMatchTypeMismatch(t *testing.T) {
	t.Parallel()

	want := map[string]any{"MODE": "auto"}
	current := map[string]any{"MODE": "manual"}
	if allMatch(want, current) {
		t.Fatal("allMatch should return false for mismatched string values")
	}
}
