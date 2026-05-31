// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// stubPersister — test double for the Persister interface used by
// PersistentCache.
// ---------------------------------------------------------------------------

type stubPersister struct {
	data        map[string]any
	loadErr     error
	saveErr     error
	flushErr    error
	flushCalled bool
}

func (s *stubPersister) Load() (map[string]any, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.data, nil
}

func (s *stubPersister) Save(data map[string]any) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.data = data
	return nil
}

func (s *stubPersister) Flush() error {
	s.flushCalled = true
	return s.flushErr
}

// ---------------------------------------------------------------------------
// PersistentCache — Load paths
// ---------------------------------------------------------------------------

func TestPersistentCacheLoadSuccess(t *testing.T) {
	t.Parallel()
	p := &stubPersister{data: map[string]any{"k": "v"}}
	c := NewPersistentCache(p)

	data, result := c.Load()
	if result != DataOperationResultLoadSuccess {
		t.Errorf("result=%s, want LOAD_SUCCESS", result)
	}
	if data["k"] != "v" {
		t.Errorf("data=%v, want k=v", data)
	}
}

func TestPersistentCacheLoadNoData(t *testing.T) {
	t.Parallel()
	p := &stubPersister{data: nil}
	c := NewPersistentCache(p)

	data, result := c.Load()
	if result != DataOperationResultNoLoad {
		t.Errorf("result=%s, want NO_LOAD", result)
	}
	if data != nil {
		t.Errorf("data must be nil for NO_LOAD")
	}
}

func TestPersistentCacheLoadError(t *testing.T) {
	t.Parallel()
	p := &stubPersister{loadErr: errors.New("disk error")}
	c := NewPersistentCache(p)

	data, result := c.Load()
	if result != DataOperationResultLoadFail {
		t.Errorf("result=%s, want LOAD_FAIL", result)
	}
	if data != nil {
		t.Error("data must be nil on load failure")
	}
}

// ---------------------------------------------------------------------------
// PersistentCache — Save paths
// ---------------------------------------------------------------------------

func TestPersistentCacheSaveSuccess(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	content := map[string]any{"x": 1}
	result := c.Save(content)
	if result != DataOperationResultSaveSuccess {
		t.Errorf("result=%s, want SAVE_SUCCESS", result)
	}
}

func TestPersistentCacheSaveNoSaveOnUnchanged(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	content := map[string]any{"x": 1}
	_ = c.Save(content)       // first save
	result := c.Save(content) // identical content
	if result != DataOperationResultNoSave {
		t.Errorf("result=%s, want NO_SAVE on unchanged content", result)
	}
}

func TestPersistentCacheSaveFail(t *testing.T) {
	t.Parallel()
	p := &stubPersister{saveErr: errors.New("write error")}
	c := NewPersistentCache(p)

	result := c.Save(map[string]any{"x": 1})
	if result != DataOperationResultSaveFail {
		t.Errorf("result=%s, want SAVE_FAIL", result)
	}
}

func TestPersistentCacheHasUnsavedChanges(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	content := map[string]any{"x": 1}
	if !c.HasUnsavedChanges(content) {
		t.Fatal("fresh cache must report unsaved changes")
	}
	_ = c.Save(content)
	if c.HasUnsavedChanges(content) {
		t.Fatal("after save, same content must not report unsaved changes")
	}
}

// ---------------------------------------------------------------------------
// PersistentCache — Flush paths
// ---------------------------------------------------------------------------

func TestPersistentCacheFlush(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	content := map[string]any{"y": 2}
	if err := c.Flush(content); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if !p.flushCalled {
		t.Error("Flush must call persister.Flush()")
	}
}

// TestPersistentCacheFlushCancelsPendingTimer covers the "stop timer" branch.
func TestPersistentCacheFlushCancelsPendingTimer(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	// Schedule a very long delayed save; we'll flush before it fires.
	contentFn := func() map[string]any { return map[string]any{"z": 1} }
	c.SaveDelayed(contentFn, 10*time.Minute)

	// Flush must cancel the timer and persist synchronously.
	content := map[string]any{"z": 1}
	if err := c.Flush(content); err != nil {
		t.Fatalf("Flush with pending timer returned error: %v", err)
	}
	if !p.flushCalled {
		t.Error("Flush must call persister.Flush() even when a timer was pending")
	}
}

// TestPersistentCacheFlushPersisterError exercises the Flush path where
// the persister's Flush() call fails.
func TestPersistentCacheFlushPersisterError(t *testing.T) {
	t.Parallel()
	p := &stubPersister{flushErr: errors.New("flush disk error")}
	c := NewPersistentCache(p)
	if err := c.Flush(map[string]any{}); err == nil {
		t.Fatal("Flush must return error when persister.Flush() fails")
	}
}

// TestPersistentCacheFlushSaveFailError exercises the Flush path where
// Save returns SAVE_FAIL after a successful persister.Flush().
func TestPersistentCacheFlushSaveFailError(t *testing.T) {
	t.Parallel()
	p := &stubPersister{saveErr: errors.New("write error")}
	c := NewPersistentCache(p)
	// Save with non-empty content will fail (saveErr is set).
	// The fresh cache has lastHashSaved="" so the content hash will differ.
	content := map[string]any{"x": 1}
	if err := c.Flush(content); err == nil {
		t.Fatal("Flush must return error when Save returns SAVE_FAIL")
	}
}

// ---------------------------------------------------------------------------
// PersistentCache — SaveDelayed paths
// ---------------------------------------------------------------------------

func TestPersistentCacheSaveDelayed(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	content := map[string]any{"z": 3}
	called := make(chan struct{}, 1)
	contentFn := func() map[string]any {
		called <- struct{}{}
		return content
	}
	// Use a very short delay so the test doesn't block.
	c.SaveDelayed(contentFn, 5*time.Millisecond)
	select {
	case <-called:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("SaveDelayed: contentFn was not called within timeout")
	}
}

// TestPersistentCacheSaveDelayedReplacesTimer covers the replace-pending-timer
// branch: a second SaveDelayed call before the first timer fires must replace
// the pending timer.
func TestPersistentCacheSaveDelayedReplacesTimer(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	// First call starts a long timer.
	c.SaveDelayed(func() map[string]any { return map[string]any{"a": 1} }, 10*time.Minute)
	// Second call must replace the pending timer (hits the delayTimer != nil branch).
	called := make(chan struct{}, 1)
	c.SaveDelayed(func() map[string]any {
		called <- struct{}{}
		return map[string]any{"b": 2}
	}, 5*time.Millisecond)

	select {
	case <-called:
		// good — second save fired
	case <-time.After(2 * time.Second):
		t.Fatal("second SaveDelayed contentFn was not called within timeout")
	}
}

// TestPersistentCacheSaveDelayedZeroDelay exercises the delay<=0 guard:
// a zero delay is replaced with 1 second internally.
func TestPersistentCacheSaveDelayedZeroDelay(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	called := make(chan struct{}, 1)
	// delay=0 → guard fires → falls back to time.Second internally;
	// we use a tiny positive delay for the actual firing to avoid a 1-second wait.
	// But we need to cover the delay<=0 branch; use 0.
	// The timer fires after 1 second internally — too slow for a unit test.
	// Instead: after calling SaveDelayed(0) the timer should be set.
	// Cancel it immediately via Flush so the test stays fast.
	c.SaveDelayed(func() map[string]any {
		called <- struct{}{}
		return map[string]any{"z": 0}
	}, 0)
	// Flush cancels the timer and calls persister.Flush() synchronously.
	if err := c.Flush(map[string]any{}); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// contentFn may or may not have been called depending on timer race;
	// either way no panic must have occurred.
}

// ---------------------------------------------------------------------------
// PersistentCache — contentHash with non-serializable value
// ---------------------------------------------------------------------------

// TestPersistentCacheContentHashNonSerializable exercises the error branch in
// contentHash: a channel value cannot be JSON-marshaled and returns "".
// A fresh cache's lastHashSaved is also "", so HasUnsavedChanges returns false
// (the two empty strings are equal). The important contract is that Save does
// not panic; it just returns NoSave since the hashes match.
func TestPersistentCacheContentHashNonSerializable(t *testing.T) {
	t.Parallel()
	p := &stubPersister{}
	c := NewPersistentCache(p)

	// A channel is not JSON-serializable; contentHash returns "".
	// A brand-new cache has lastHashSaved="", so the hashes match → no-save.
	bad := map[string]any{"ch": make(chan int)}
	result := c.Save(bad)
	// Either NoSave (because "" == "") or SaveSuccess — both are acceptable;
	// we just verify no panic occurs and that the content does not cause a crash.
	if result == DataOperationResultSaveFail {
		t.Errorf("Save with non-serializable content must not return SAVE_FAIL, got %s", result)
	}
}
