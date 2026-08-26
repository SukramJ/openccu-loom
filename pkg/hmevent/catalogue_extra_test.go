// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmevent

import (
	"testing"
)

// TestIntegrationIssueTranslationPlaceholders exercises the three conditional
// branches inside TranslationPlaceholders.
func TestIntegrationIssueTranslationPlaceholders(t *testing.T) {
	// Base: only interface_id key.
	issue := IntegrationIssue{
		IssueType:   "some_issue",
		InterfaceID: "BidCos-RF",
	}
	m := issue.TranslationPlaceholders()
	if m["interface_id"] != "BidCos-RF" {
		t.Fatalf("interface_id=%q", m["interface_id"])
	}
	if _, ok := m["mismatch_count"]; ok {
		t.Fatal("mismatch_count should be absent when MismatchCount==0")
	}

	// With mismatch_count.
	issue.MismatchCount = 3
	m = issue.TranslationPlaceholders()
	if m["mismatch_count"] != "3" {
		t.Fatalf("mismatch_count=%q", m["mismatch_count"])
	}

	// With device addresses.
	issue.DeviceAddresses = []string{"ABC:1", "ABC:2"}
	m = issue.TranslationPlaceholders()
	if m["device_count"] != "2" {
		t.Fatalf("device_count=%q", m["device_count"])
	}
	if m["device_addresses"] == "" {
		t.Fatal("device_addresses should not be empty")
	}

	// With missing parameters.
	issue.MissingParameters = []string{"LEVEL", "SETPOINT"}
	m = issue.TranslationPlaceholders()
	if m["parameter_count"] != "2" {
		t.Fatalf("parameter_count=%q", m["parameter_count"])
	}
	if m["missing_parameters"] == "" {
		t.Fatal("missing_parameters should not be empty")
	}
}

// TestIssueIDAndTranslationKey tests small helpers on IntegrationIssue.
func TestIssueIDAndTranslationKey(t *testing.T) {
	issue := IntegrationIssue{IssueType: "ping_pong_mismatch", InterfaceID: "HmIP-RF"}
	want := "ping_pong_mismatch_HmIP-RF"
	if got := issue.IssueID(); got != want {
		t.Fatalf("IssueID=%q want %q", got, want)
	}
	if got := issue.TranslationKey(); got != "ping_pong_mismatch" {
		t.Fatalf("TranslationKey=%q", got)
	}
}

// TestJoinStringsAndItoa exercises the two unexported helpers via the
// TranslationPlaceholders public surface so they get counted in coverage.
func TestJoinStringsViaPlaceholders(t *testing.T) {
	issue := IntegrationIssue{
		InterfaceID:     "X",
		DeviceAddresses: []string{"a", "b", "c"},
	}
	m := issue.TranslationPlaceholders()
	// joinStrings separates with ", "
	if m["device_addresses"] != "a, b, c" {
		t.Fatalf("device_addresses=%q", m["device_addresses"])
	}
}
