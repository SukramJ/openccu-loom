// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
)

// TestPerExchangePaseProvider_Reuses verifies that two Resolve calls
// for the same exchange-id return the same adapter (Pake1 → Pake3
// state must stay coherent across opcodes).
func TestPerExchangePaseProvider_Reuses(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	p := NewPerExchangePaseProvider(func() *PaseAdapter {
		calls.Add(1)
		return NewPaseAdapter(nil)
	})
	first := p.Resolve(42)
	second := p.Resolve(42)
	if first != second {
		t.Errorf("Resolve same exchange returned distinct adapters: %p vs %p", first, second)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("factory invocations: %d, want 1", got)
	}
	if got := p.Active(); got != 1 {
		t.Errorf("Active = %d, want 1", got)
	}
}

// TestPerExchangePaseProvider_IsolatesExchanges verifies that
// distinct exchange-ids get distinct adapters — two commissioners
// pairing in parallel must not share PASE state.
func TestPerExchangePaseProvider_IsolatesExchanges(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	p := NewPerExchangePaseProvider(func() *PaseAdapter {
		calls.Add(1)
		return NewPaseAdapter(nil)
	})
	a1 := p.Resolve(1)
	a2 := p.Resolve(2)
	if a1 == a2 {
		t.Error("Resolve different exchanges returned the same adapter; state would race")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("factory invocations: %d, want 2", got)
	}
	if got := p.Active(); got != 2 {
		t.Errorf("Active = %d, want 2", got)
	}
}

// TestPerExchangePaseProvider_NilFactory verifies that a provider
// built with a nil factory returns nil from every Resolve — the SC
// router degrades to a debug-logged drop.
func TestPerExchangePaseProvider_NilFactory(t *testing.T) {
	t.Parallel()
	p := NewPerExchangePaseProvider(nil)
	if h := p.Resolve(7); h != nil {
		t.Errorf("nil factory: Resolve returned %v, want nil", h)
	}
	if got := p.Active(); got != 0 {
		t.Errorf("Active = %d, want 0", got)
	}
}

// TestPerExchangePaseProvider_FactoryReturnsNil verifies that a
// factory which returns nil (e.g. PaseAdapter build failed) does
// not pollute the provider's map.
func TestPerExchangePaseProvider_FactoryReturnsNil(t *testing.T) {
	t.Parallel()
	p := NewPerExchangePaseProvider(func() *PaseAdapter { return nil })
	if h := p.Resolve(9); h != nil {
		t.Errorf("factory returned nil: Resolve should also return nil, got %v", h)
	}
	if got := p.Active(); got != 0 {
		t.Errorf("Active after nil-factory result: %d, want 0", got)
	}
}

// TestPerExchangePaseProvider_ForgetReleases verifies that Forget
// drops the adapter so the next Resolve allocates a fresh one.
func TestPerExchangePaseProvider_ForgetReleases(t *testing.T) {
	t.Parallel()
	p := NewPerExchangePaseProvider(func() *PaseAdapter { return NewPaseAdapter(nil) })
	first := p.Resolve(5)
	p.Forget(5)
	if got := p.Active(); got != 0 {
		t.Errorf("Active after Forget: %d, want 0", got)
	}
	second := p.Resolve(5)
	if first == second {
		t.Error("Resolve after Forget returned the stale adapter")
	}
}

// TestPerExchangePaseProvider_ResetClearsAll verifies that Reset
// drops every per-exchange adapter (operators rotating PBKDF params).
func TestPerExchangePaseProvider_ResetClearsAll(t *testing.T) {
	t.Parallel()
	p := NewPerExchangePaseProvider(func() *PaseAdapter { return NewPaseAdapter(nil) })
	for id := uint16(1); id <= 5; id++ {
		p.Resolve(id)
	}
	if got := p.Active(); got != 5 {
		t.Errorf("Active before Reset: %d, want 5", got)
	}
	p.Reset()
	if got := p.Active(); got != 0 {
		t.Errorf("Active after Reset: %d, want 0", got)
	}
}

// TestPerExchangePaseProvider_ReapBefore_EvictsStale verifies that
// reapBefore drops every entry whose lastTouched is at or before the
// supplied cutoff, while preserving entries touched after the cutoff.
// Drives the reaper deterministically without spinning a real ticker.
func TestPerExchangePaseProvider_ReapBefore_EvictsStale(t *testing.T) {
	t.Parallel()
	p := NewPerExchangePaseProvider(func() *PaseAdapter { return NewPaseAdapter(nil) })

	// First Resolve populates exchange 1.
	_ = p.Resolve(1)
	earlyTouch := time.Now()

	// Wait a hair so the second entry's timestamp is strictly later.
	time.Sleep(10 * time.Millisecond)

	// Second Resolve populates exchange 2.
	_ = p.Resolve(2)

	// Reap with a cutoff between the two — exchange 1 must die,
	// exchange 2 must survive.
	p.reapBefore(earlyTouch.Add(time.Millisecond))
	if got := p.Active(); got != 1 {
		t.Errorf("Active after partial reap = %d, want 1", got)
	}

	// Reap with a future cutoff — both die.
	p.reapBefore(time.Now().Add(time.Hour))
	if got := p.Active(); got != 0 {
		t.Errorf("Active after wide reap = %d, want 0", got)
	}
}

// TestPerExchangePaseProvider_StartReaper_Cancels verifies that
// StartReaper followed by Stop returns cleanly and that a second
// StartReaper is idempotent (cancels the first goroutine).
func TestPerExchangePaseProvider_StartReaper_Cancels(t *testing.T) {
	t.Parallel()
	p := NewPerExchangePaseProvider(func() *PaseAdapter { return NewPaseAdapter(nil) })
	ctx := t.Context()

	p.StartReaper(ctx, 5*time.Millisecond, time.Millisecond)
	p.StartReaper(ctx, 5*time.Millisecond, time.Millisecond) // idempotent
	p.Stop()
	p.Stop() // idempotent
}

// TestPerExchangePaseProvider_FactoryProducesRealAdapter — sanity
// check that the factory can return a PaseAdapter wrapping a real
// spake2.Verifier without crashing the provider's bookkeeping.
func TestPerExchangePaseProvider_FactoryProducesRealAdapter(t *testing.T) {
	t.Parallel()
	salt := []byte("SPAKE2P Key Salt")
	vc, err := spake2.NewVerifierContext(20202021, salt, 1000)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	p := NewPerExchangePaseProvider(func() *PaseAdapter {
		return NewPaseAdapter(spake2.NewVerifier(vc, nil, nil, []byte(spake2.MatterContext)))
	})
	if h := p.Resolve(11); h == nil {
		t.Error("Resolve returned nil for a fully-functional factory")
	}
}
