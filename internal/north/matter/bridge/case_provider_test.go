// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for PerExchangeCaseProvider: Resolve, Forget, Reset,
// Active, StartReaper, StopReaper. Lives in package bridge to access
// unexported types.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Resolve ─────────────────────────────────────────────────────────────────

func TestCaseProvider_Resolve_NilFactory_ReturnsNil(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(nil)
	if h := p.Resolve(1); h != nil {
		t.Errorf("nil factory: expected nil handler, got %T", h)
	}
}

func TestCaseProvider_Resolve_AllocatesFreshAdapter(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	p := NewPerExchangeCaseProvider(func() *CaseAdapter {
		count.Add(1)
		return &CaseAdapter{}
	})
	h1 := p.Resolve(10)
	if h1 == nil {
		t.Fatal("first Resolve: expected non-nil handler")
	}
	if count.Load() != 1 {
		t.Errorf("factory call count: want 1, got %d", count.Load())
	}
}

func TestCaseProvider_Resolve_ReusesByExchangeID(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	p := NewPerExchangeCaseProvider(func() *CaseAdapter {
		count.Add(1)
		return &CaseAdapter{}
	})
	h1 := p.Resolve(10)
	h2 := p.Resolve(10)
	if h1 != h2 {
		t.Error("same exchange ID: expected identical handler on second Resolve")
	}
	if count.Load() != 1 {
		t.Errorf("factory must be called once for same exchange; got %d", count.Load())
	}
}

func TestCaseProvider_Resolve_DifferentExchanges(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter {
		return &CaseAdapter{}
	})
	h10 := p.Resolve(10)
	h20 := p.Resolve(20)
	if h10 == nil || h20 == nil {
		t.Fatal("expected non-nil handlers for both exchanges")
	}
	if h10 == h20 {
		t.Error("different exchange IDs must yield different handlers")
	}
}

func TestCaseProvider_Resolve_FactoryReturnsNil(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter {
		return nil
	})
	if h := p.Resolve(5); h != nil {
		t.Errorf("factory returning nil: expected nil from Resolve, got %T", h)
	}
	// No entry should be cached when factory returned nil.
	if p.Active() != 0 {
		t.Errorf("Active: want 0 after factory-nil Resolve, got %d", p.Active())
	}
}

// ─── Forget ───────────────────────────────────────────────────────────────────

func TestCaseProvider_Forget_RemovesEntry(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.Resolve(7)
	if p.Active() != 1 {
		t.Fatalf("expected 1 active before Forget, got %d", p.Active())
	}
	p.Forget(7)
	if p.Active() != 0 {
		t.Errorf("expected 0 after Forget, got %d", p.Active())
	}
}

func TestCaseProvider_Forget_UnknownIDIsNoop(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.Resolve(7)
	p.Forget(99) // does not exist — must not panic
	if p.Active() != 1 {
		t.Errorf("Active after noop Forget: want 1, got %d", p.Active())
	}
}

func TestCaseProvider_ForgetCausesNextResolveFresh(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	p := NewPerExchangeCaseProvider(func() *CaseAdapter {
		count.Add(1)
		return &CaseAdapter{}
	})
	h1 := p.Resolve(10)
	p.Forget(10)
	h2 := p.Resolve(10)
	if h1 == h2 {
		t.Error("after Forget, next Resolve should produce a fresh handler")
	}
	if count.Load() != 2 {
		t.Errorf("factory count after Forget+Resolve: want 2, got %d", count.Load())
	}
}

// ─── Reset ────────────────────────────────────────────────────────────────────

func TestCaseProvider_Reset_DropsAll(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.Resolve(1)
	p.Resolve(2)
	p.Resolve(3)
	if p.Active() != 3 {
		t.Fatalf("expected 3 active before Reset, got %d", p.Active())
	}
	p.Reset()
	if p.Active() != 0 {
		t.Errorf("expected 0 after Reset, got %d", p.Active())
	}
}

func TestCaseProvider_Reset_CallsOnEvict(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	var evicted []uint16
	p.SetOnEvict(func(id uint16) { evicted = append(evicted, id) })
	p.Resolve(10)
	p.Resolve(20)
	p.Reset()
	if len(evicted) != 2 {
		t.Errorf("Reset: expected 2 eviction callbacks, got %d", len(evicted))
	}
}

func TestCaseProvider_Reset_NoOnEvictIsNoop(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.Resolve(1)
	p.Reset() // must not panic
	if p.Active() != 0 {
		t.Errorf("Active after Reset: want 0, got %d", p.Active())
	}
}

// ─── Active ───────────────────────────────────────────────────────────────────

func TestCaseProvider_Active_EmptyZero(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(nil)
	if p.Active() != 0 {
		t.Errorf("new provider Active: want 0, got %d", p.Active())
	}
}

// ─── StartReaper / StopReaper ─────────────────────────────────────────────────

func TestCaseProvider_Reaper_EvictsStaleEntries(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.Resolve(42)

	// Manually backdate the entry so it looks stale.
	p.mu.Lock()
	p.entries[42].lastTouched = time.Now().Add(-10 * time.Minute)
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.StartReaper(ctx, 10*time.Millisecond, 1*time.Millisecond)
	defer p.StopReaper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if p.Active() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("stale entry not reaped within deadline; Active=%d", p.Active())
}

func TestCaseProvider_Reaper_CallsOnEvict(t *testing.T) {
	t.Parallel()
	var evictedID atomic.Uint32
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.SetOnEvict(func(id uint16) { evictedID.Store(uint32(id)) })
	p.Resolve(77)

	p.mu.Lock()
	p.entries[77].lastTouched = time.Now().Add(-10 * time.Minute)
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.StartReaper(ctx, 10*time.Millisecond, 1*time.Millisecond)
	defer p.StopReaper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if evictedID.Load() == 77 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("onEvict not called for exchange 77; evictedID=%d", evictedID.Load())
}

func TestCaseProvider_StartReaper_DoubleIsNoop(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.StartReaper(ctx, 100*time.Millisecond, time.Minute)
	p.StartReaper(ctx, 100*time.Millisecond, time.Minute) // must not panic
	p.StopReaper()
}

func TestCaseProvider_StopReaper_WithoutStart_IsNoop(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(nil)
	p.StopReaper() // must not panic
}

func TestCaseProvider_Reaper_DoesNotEvictRecentEntries(t *testing.T) {
	t.Parallel()
	p := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	p.Resolve(33)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// TTL is 10 minutes — entry should not be reaped in this test window.
	p.StartReaper(ctx, 10*time.Millisecond, 10*time.Minute)
	defer p.StopReaper()

	time.Sleep(50 * time.Millisecond)
	if p.Active() != 1 {
		t.Errorf("recent entry was incorrectly reaped; Active=%d", p.Active())
	}
}
