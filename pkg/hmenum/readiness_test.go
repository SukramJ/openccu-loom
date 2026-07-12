// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

// TestReadinessPhaseValid pins which ReadinessPhase values are recognized as
// valid wire values and confirms an arbitrary unknown string is rejected.
func TestReadinessPhaseValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		phase ReadinessPhase
		want  bool
	}{
		{ReadinessUnknown, true},
		{ReadinessWaitingForCCU, true},
		{ReadinessLoadingHub, true},
		{ReadinessLoadingDevices, true},
		{ReadinessReady, true},
		{ReadinessPhase("bogus"), false},
		{ReadinessPhase(""), false},
	}

	for _, c := range cases {
		if got := c.phase.Valid(); got != c.want {
			t.Errorf("ReadinessPhase(%q).Valid() = %v, want %v", string(c.phase), got, c.want)
		}
	}
}

// TestReadinessPhaseString verifies String returns the raw wire value for
// every defined phase, matching the string(p) conversion the type promises.
func TestReadinessPhaseString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		phase ReadinessPhase
		want  string
	}{
		{ReadinessUnknown, "unknown"},
		{ReadinessWaitingForCCU, "waiting_for_ccu"},
		{ReadinessLoadingHub, "loading_hub"},
		{ReadinessLoadingDevices, "loading_devices"},
		{ReadinessReady, "ready"},
	}

	for _, c := range cases {
		if got := c.phase.String(); got != c.want {
			t.Errorf("ReadinessPhase(%q).String() = %q, want %q", string(c.phase), got, c.want)
		}
	}
}
