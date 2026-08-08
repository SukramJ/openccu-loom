// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmreliability

import (
	"testing"
	"time"
)

// TestRecordedReliabilityDefaults pins every public default to a hard
// expected value. This is the drift-detector test from audit O10/R12:
// When an advances and a default genuinely changes
// update both the constant and this snapshot in the same commit and
// reference the upstream change in the commit message.
//
// The test fails fast and verbosely so reviewers see *which* knob
// moved without diffing the whole defaults file.
func TestRecordedReliabilityDefaults(t *testing.T) {
	type want struct {
		name string
		got  any
		want any
	}
	cases := []want{
		// circuit
		{"CircuitFailureThreshold", CircuitFailureThreshold, 5},
		{"CircuitResetTimeout", CircuitResetTimeout, 30 * time.Second},
		{"CircuitHalfOpenSuccess", CircuitHalfOpenSuccess, 2},
		// retry
		{"RetryInitialBackoff", RetryInitialBackoff, 2 * time.Second},
		{"RetryMaxBackoff", RetryMaxBackoff, 30 * time.Second},
		{"RetryDutyCycleDelay", RetryDutyCycleDelay, 40 * time.Second},
		{"RetryTransmissionPendingDelay", RetryTransmissionPendingDelay, 5 * time.Second},
		{"RetryRecoveryWait", RetryRecoveryWait, 120 * time.Second},
		// throttle
		{"ThrottleInterCommandDelay", ThrottleInterCommandDelay, 0 * time.Millisecond},
		{"ThrottleBurstThreshold", ThrottleBurstThreshold, 5},
		{"ThrottleBurstWindow", ThrottleBurstWindow, 500 * time.Millisecond},
		{"ThrottleMaxQueueDepthFactor", ThrottleMaxQueueDepthFactor, 4},
		// ping/pong
		{"PingPongTTL", PingPongTTL, 300 * time.Second},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("hmreliability.%s drifted: got %v, want %v — update the reference pin and notes/parity/by_design.md if intentional",
				c.name, c.got, c.want)
		}
	}
}
