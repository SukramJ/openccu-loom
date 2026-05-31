// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestLockSend_AcceptsCriticalPriority verifies that Lock/Unlock/Open accept
// CommandPriorityCritical — lock lifecycle commands use Critical priority.
func TestLockSend_AcceptsCriticalPriority(t *testing.T) {
	l := New(Config{
		Writer: &stubWriter{},
		Kind:   KindRF,
	})
	ctx := context.Background()
	for _, fn := range []func() error{
		func() error { return l.Lock(ctx, hmenum.CommandPriorityCritical) },
		func() error { return l.Unlock(ctx, hmenum.CommandPriorityCritical) },
	} {
		if err := fn(); err != nil {
			t.Fatalf("expected nil for Critical priority, got %v", err)
		}
	}
}

// TestLockSend_AcceptsHighPriority verifies that a High-priority Lock call
// reaches the writer without error.
func TestLockSend_AcceptsHighPriority(t *testing.T) {
	l := New(Config{
		Writer: &stubWriter{},
		Kind:   KindRF,
	})
	if err := l.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("expected nil for High priority, got %v", err)
	}
}
