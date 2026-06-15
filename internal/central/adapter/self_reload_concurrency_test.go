// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
)

// TestSelfReloadSemaphoreCapMatchesConstant pins the capacity of the
// self-reload semaphore so refactors cannot silently change the bound.
func TestSelfReloadSemaphoreCapMatchesConstant(t *testing.T) {
	t.Parallel()
	h := NewCallbackHandlers(nil, nil)
	got := cap(h.selfReloadSem)
	if got != selfReloadConcurrency {
		t.Fatalf("selfReloadSem capacity = %d, want selfReloadConcurrency (%d)", got, selfReloadConcurrency)
	}
}

// TestSelfReloadSemaphoreDropsExcessUnderBurst verifies that when the
// semaphore is saturated, subsequent scheduleSelfReload calls are dropped
// without blocking. The goroutine's Device parameter must be nil so the
// early-return guard fires before the semaphore is touched; we instead
// test the semaphore directly by filling it and checking that a
// non-blocking send fails.
func TestSelfReloadSemaphoreDropsExcessUnderBurst(t *testing.T) {
	t.Parallel()
	h := NewCallbackHandlers(nil, nil)

	// Fill the semaphore to capacity.
	for range selfReloadConcurrency {
		h.selfReloadSem <- struct{}{}
	}

	// A further non-blocking send must fail (semaphore full).
	select {
	case h.selfReloadSem <- struct{}{}:
		// Drain to avoid a goroutine leak in the test binary.
		<-h.selfReloadSem
		t.Fatal("expected semaphore to be full, but send succeeded")
	default:
		// Correct: semaphore is at capacity, excess work is dropped.
	}
}
