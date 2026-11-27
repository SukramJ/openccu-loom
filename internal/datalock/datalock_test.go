// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package datalock

import (
	"errors"
	"testing"
)

// TestAcquireIsExclusiveUntilReleased: a second Acquire on a held directory
// is refused with ErrHeld, succeeds once the first lock is released, and a
// lock on another directory is independent.
func TestAcquireIsExclusiveUntilReleased(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire on a held directory: err = %v, want ErrHeld", err)
	}
	other, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatalf("acquire on another directory: %v", err)
	}
	_ = other.Release()

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = again.Release()
}
