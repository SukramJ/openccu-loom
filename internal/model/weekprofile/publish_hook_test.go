// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// TestPublishHookFiredOnLoad verifies the hook registered with SetPublishHook
// is called after a successful Load.
func TestPublishHookFiredOnLoad(t *testing.T) {
	var count int32
	p := NewDefault(&stubLoader{sched: schedule.NewSimple()}, nil)
	p.SetPublishHook(func() { atomic.AddInt32(&count, 1) })

	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Fatalf("hook fires=%d, want 1", count)
	}
}

// TestPublishHookFiredOnSave verifies the hook is called after a successful Save.
func TestPublishHookFiredOnSave(t *testing.T) {
	var count int32
	p := NewDefault(nil, &stubSaver{})
	p.SetPublishHook(func() { atomic.AddInt32(&count, 1) })

	if err := p.Save(context.Background(), schedule.NewSimple()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Fatalf("hook fires=%d, want 1", count)
	}
}

// TestPublishHookFiredAfterOnChangeCallbacks verifies the hook runs after all
// OnChange subscribers, not before.
func TestPublishHookFiredAfterOnChangeCallbacks(t *testing.T) {
	var order []string
	p := NewDefault(nil, &stubSaver{})
	p.OnChange(func(_, _ *schedule.Simple) { order = append(order, "onchange") })
	p.SetPublishHook(func() { order = append(order, "hook") })

	if err := p.Save(context.Background(), schedule.NewSimple()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 events, got %v", order)
	}
	if order[0] != "onchange" || order[1] != "hook" {
		t.Fatalf("unexpected order: %v", order)
	}
}

// TestPublishHookClearable verifies passing nil to SetPublishHook removes the hook.
func TestPublishHookClearable(t *testing.T) {
	var count int32
	p := NewDefault(nil, &stubSaver{})
	p.SetPublishHook(func() { atomic.AddInt32(&count, 1) })
	p.SetPublishHook(nil) // clear

	if err := p.Save(context.Background(), schedule.NewSimple()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if atomic.LoadInt32(&count) != 0 {
		t.Fatalf("hook should not fire after cleared; fires=%d", count)
	}
}
