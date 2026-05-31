// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestThrottlePurgedCountInitialZero verifies that PurgedCount() is zero on a
// freshly constructed CommandThrottle.
func TestThrottlePurgedCountInitialZero(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()

	if got := tt.PurgedCount(); got != 0 {
		t.Fatalf("PurgedCount() initial = %d, want 0", got)
	}
}

// TestThrottleQueueSizeInitialZero verifies that QueueSize() (alias: Waiting())
// is zero on a freshly constructed CommandThrottle with no waiters.
func TestThrottleQueueSizeInitialZero(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()

	if got := tt.QueueSize(); got != 0 {
		t.Fatalf("QueueSize() initial = %d, want 0", got)
	}
}

// --- CommandThrottle.IsEnabled ---

func TestCommandThrottle_IsEnabled_WithDelay(t *testing.T) {
	th := NewThrottle(ThrottleConfig{
		InterCommandDelay: 10 * time.Millisecond,
	})
	if !th.IsEnabled() {
		t.Error("IsEnabled must return true when InterCommandDelay > 0")
	}
}

func TestCommandThrottle_IsEnabled_ZeroConfig(t *testing.T) {
	th := NewThrottle(ThrottleConfig{
		InterCommandDelay: 0,
		BurstThreshold:    0,
	})
	if th.IsEnabled() {
		t.Error("IsEnabled must return false when no delay or burst is configured")
	}
}

func TestCommandThrottle_IsEnabled_WithBurst(t *testing.T) {
	th := NewThrottle(ThrottleConfig{
		BurstThreshold: 5,
		BurstWindow:    time.Second,
	})
	if !th.IsEnabled() {
		t.Error("IsEnabled must return true when BurstThreshold > 0")
	}
}

// --- CommandThrottle.AcquireAndPurge ---

func TestCommandThrottle_AcquireAndPurge_NoAddr(t *testing.T) {
	th := NewThrottle(ThrottleConfig{
		MaxInFlight: 1,
	})
	// addr="" means Purge is skipped, Acquire proceeds immediately.
	if err := th.AcquireAndPurge(context.Background(), hmenum.CommandPriorityHigh, ""); err != nil {
		t.Fatalf("AcquireAndPurge(empty addr): %v", err)
	}
}

func TestCommandThrottle_AcquireAndPurge_WithAddr(t *testing.T) {
	th := NewThrottle(ThrottleConfig{
		MaxInFlight: 1,
	})
	// AcquireAndPurge with addr must purge pending and then acquire.
	if err := th.AcquireAndPurge(context.Background(), hmenum.CommandPriorityHigh, "DEV:1"); err != nil {
		t.Fatalf("AcquireAndPurge(with addr): %v", err)
	}
}
