// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for CommandThrottle accessor additions:
// BurstCount BurstThresholdValue BurstWindowValue,
// IntervalValue QueueSize.

package reliability

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BurstThresholdValue
// ---------------------------------------------------------------------------

func TestBurstThresholdValueWhenSet(t *testing.T) {
	t.Parallel()
	cfg := ThrottleConfig{BurstThreshold: 7, BurstWindow: 500 * time.Millisecond}
	th := NewThrottle(cfg)
	if got := th.BurstThresholdValue(); got != 7 {
		t.Errorf("BurstThresholdValue() = %d; want 7", got)
	}
}

func TestBurstThresholdValueZeroWhenNotSet(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{})
	if got := th.BurstThresholdValue(); got != 0 {
		t.Errorf("BurstThresholdValue() = %d; want 0 when not configured", got)
	}
}

// ---------------------------------------------------------------------------
// BurstWindowValue
// ---------------------------------------------------------------------------

func TestBurstWindowValueWhenSet(t *testing.T) {
	t.Parallel()
	want := 300 * time.Millisecond
	cfg := ThrottleConfig{BurstThreshold: 3, BurstWindow: want}
	th := NewThrottle(cfg)
	if got := th.BurstWindowValue(); got != want {
		t.Errorf("BurstWindowValue() = %s; want %s", got, want)
	}
}

func TestBurstWindowValueZeroWhenNotSet(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{})
	if got := th.BurstWindowValue(); got != 0 {
		t.Errorf("BurstWindowValue() = %s; want 0 when not configured", got)
	}
}

// ---------------------------------------------------------------------------
// IntervalValue
// ---------------------------------------------------------------------------

func TestIntervalValueWhenSet(t *testing.T) {
	t.Parallel()
	want := 75 * time.Millisecond
	cfg := ThrottleConfig{InterCommandDelay: want}
	th := NewThrottle(cfg)
	if got := th.IntervalValue(); got != want {
		t.Errorf("IntervalValue() = %s; want %s", got, want)
	}
}

func TestIntervalValueZeroWhenNotSet(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{})
	if got := th.IntervalValue(); got != 0 {
		t.Errorf("IntervalValue() = %s; want 0 when InterCommandDelay unset", got)
	}
}

// ---------------------------------------------------------------------------
// BurstCount (alias for WaitedForBurstSlot)
// ---------------------------------------------------------------------------

func TestBurstCountAliasMatchesWaitedForBurstSlot(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{})
	// Both should start at zero.
	if got := th.BurstCount(); got != 0 {
		t.Errorf("BurstCount() = %d; want 0 on fresh throttle", got)
	}
	if th.BurstCount() != th.WaitedForBurstSlot() {
		t.Error("BurstCount() != WaitedForBurstSlot(); they must return the same value")
	}
}

// ---------------------------------------------------------------------------
// QueueSize (alias for Waiting)
// ---------------------------------------------------------------------------

func TestQueueSizeAliasMatchesWaiting(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{})
	if got := th.QueueSize(); got != 0 {
		t.Errorf("QueueSize() = %d; want 0 on fresh throttle", got)
	}
	if th.QueueSize() != th.Waiting() {
		t.Error("QueueSize() != Waiting(); they must return the same value")
	}
}
