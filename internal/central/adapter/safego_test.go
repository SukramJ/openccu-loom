// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSafeGoRecoversFromPanic verifies that SafeGo recovers a panic in the
// started goroutine without crashing the test binary.
func TestSafeGoRecoversFromPanic(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	SafeGo("test-panic", func() {
		defer close(done)
		panic("intentional panic from test")
	})

	select {
	case <-done:
		// expected: function ran, panic was recovered, fn body
		// completed (defer close fired before panic propagated up).
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGo goroutine did not run within 2s")
	}
}

// TestSafeGoRunsFn verifies that a non-panicking function is called
// normally.
func TestSafeGoRunsFn(t *testing.T) {
	t.Parallel()

	var ran atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo("test-normal", func() {
		defer wg.Done()
		ran.Add(1)
	})
	wg.Wait()
	if ran.Load() != 1 {
		t.Errorf("fn invocation count = %d, want 1", ran.Load())
	}
}

// TestSafeGoNilFnNoOp verifies that a nil fn is a no-op and does not
// panic.
func TestSafeGoNilFnNoOp(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SafeGo(nil) panicked: %v", r)
		}
	}()
	SafeGo("test-nil", nil)
}
