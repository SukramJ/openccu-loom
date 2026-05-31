// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproperty"
)

// TestCachedLazyLoad verifies that the loader is called only once for
// repeated Get calls when the cache is valid.
func TestCachedLazyLoad(t *testing.T) {
	t.Parallel()

	var calls int

	var c hmproperty.Cached[int]
	loader := func() int {
		calls++

		return 42
	}

	first := c.Get(loader)
	second := c.Get(loader)
	third := c.Get(loader)

	if first != 42 || second != 42 || third != 42 {
		t.Errorf("expected 42 for all calls, got %d / %d / %d", first, second, third)
	}

	if calls != 1 {
		t.Errorf("loader called %d times, want 1", calls)
	}
}

// TestCachedInvalidateForcesReload verifies that after Invalidate the loader
// is called again on the next Get.
func TestCachedInvalidateForcesReload(t *testing.T) {
	t.Parallel()

	var calls int

	var c hmproperty.Cached[string]
	loader := func() string {
		calls++

		return "value"
	}

	_ = c.Get(loader)

	if calls != 1 {
		t.Fatalf("expected 1 loader call before Invalidate, got %d", calls)
	}

	c.Invalidate()

	if c.IsValid() {
		t.Error("cache should be invalid after Invalidate")
	}

	_ = c.Get(loader)

	if calls != 2 {
		t.Errorf("expected 2 loader calls after Invalidate+Get, got %d", calls)
	}

	// A second Get without another Invalidate must not re-invoke the loader.
	_ = c.Get(loader)

	if calls != 2 {
		t.Errorf("expected still 2 loader calls, got %d", calls)
	}
}

// TestCachedSet verifies that Set bypasses the loader for subsequent Gets.
func TestCachedSet(t *testing.T) {
	t.Parallel()

	var calls int

	var c hmproperty.Cached[float64]
	loader := func() float64 {
		calls++

		return 3.14
	}

	c.Set(2.71)

	if !c.IsValid() {
		t.Error("cache should be valid after Set")
	}

	v := c.Get(loader)
	if v != 2.71 {
		t.Errorf("expected 2.71 from Set, got %v", v)
	}

	if calls != 0 {
		t.Errorf("loader should not have been called after Set, got %d calls", calls)
	}

	// After Invalidate, loader should run and overwrite the Set value.
	c.Invalidate()

	v = c.Get(loader)
	if v != 3.14 {
		t.Errorf("expected 3.14 after Invalidate+Get, got %v", v)
	}

	if calls != 1 {
		t.Errorf("expected 1 loader call after Invalidate, got %d", calls)
	}
}

// TestCachedConcurrent is a race-detector test for 100 parallel Get calls.
// All goroutines must observe a consistent value; the loader must be called
// at most once.
func TestCachedConcurrent(t *testing.T) {
	t.Parallel()

	const goroutines = 100

	var (
		calls atomic.Int64
		c     hmproperty.Cached[int]
	)

	loader := func() int {
		calls.Add(1)

		return 7
	}

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()

			v := c.Get(loader)
			if v != 7 {
				t.Errorf("got %d, want 7", v)
			}
		}()
	}

	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("loader called %d times under concurrent load, want 1", n)
	}
}

// TestCachedInvalidateConcurrent is a race-detector test mixing parallel
// Invalidate and Get calls. The test does not assert on the exact call
// count because invalidation may interleave with in-flight Gets; it only
// checks that the process does not panic, deadlock, or return a corrupt
// value, and that after all goroutines finish the cache is self-consistent.
func TestCachedInvalidateConcurrent(t *testing.T) {
	t.Parallel()

	const (
		readers      = 80
		invalidators = 20
	)

	var (
		c       hmproperty.Cached[int]
		barrier = make(chan struct{})
		wg      sync.WaitGroup
	)

	loader := func() int { return 99 }

	// Pre-populate so readers have something to hit.
	_ = c.Get(loader)

	wg.Add(readers + invalidators)

	for range readers {
		go func() {
			defer wg.Done()
			<-barrier

			v := c.Get(loader)
			if v != 99 {
				t.Errorf("got %d, want 99", v)
			}
		}()
	}

	for range invalidators {
		go func() {
			defer wg.Done()
			<-barrier
			c.Invalidate()
		}()
	}

	close(barrier) // release all goroutines simultaneously
	wg.Wait()

	// After everything settles, IsValid must be coherent with Get.
	final := c.Get(loader)
	if final != 99 {
		t.Errorf("final Get returned %d, want 99", final)
	}

	if !c.IsValid() {
		t.Error("cache should be valid after final Get")
	}
}
