// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestClearWaitsForInFlightHandlers pins that the Clear* family gives the
// same guarantee the unsubscribe closure documents: once it returns, no
// cleared handler is still executing.
//
// It did not. Clear only dropped entries from the map — it neither set the
// dead flag nor waited on the in-flight count — so a central teardown
// (Unit.Stop calls ClearExternalSubscriptions + ClearAllSubscriptions)
// returned while a dispatch snapshot kept invoking handlers, and then tore
// down the adapters those handlers were calling into. Every surrounding
// test passed: the handlers ran, the map was empty, nothing compared the
// two in time.
func TestClearWaitsForInFlightHandlers(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var running, finished atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})

	Subscribe(b, func(advEvtGamma) {
		running.Store(true)
		close(entered)
		<-release
		finished.Store(true)
	}, WithName("slow-handler"))

	go Publish(b, advEvtGamma{Base: hmevent.NewBase()})

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	cleared := make(chan struct{})
	go func() {
		b.ClearAllSubscriptions()
		close(cleared)
	}()

	// The clear must still be blocked while the handler runs.
	select {
	case <-cleared:
		t.Fatal("ClearAllSubscriptions returned while a handler was still executing — " +
			"a teardown may now free what that handler is using")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-cleared:
	case <-time.After(2 * time.Second):
		t.Fatal("ClearAllSubscriptions never returned after the handler finished")
	}
	if !finished.Load() {
		t.Fatal("handler did not run to completion")
	}
}

// TestClearMarksHandlersDeadForTheRunningSnapshot pins the other half: a
// cross-goroutine clear stops handlers the running dispatch has snapshotted
// but not yet called. During teardown those calls would land on adapters
// that are already going away.
func TestClearMarksHandlersDeadForTheRunningSnapshot(t *testing.T) {
	t.Parallel()

	b := NewBus()
	entered := make(chan struct{})
	release := make(chan struct{})
	var lateCalls atomic.Int32

	Subscribe(b, func(advEvtGamma) {
		close(entered)
		<-release
	}, WithPriority(PriorityHigh), WithName("first"))

	Subscribe(b, func(advEvtGamma) {
		lateCalls.Add(1)
	}, WithPriority(PriorityNormal), WithName("second"))

	done := make(chan struct{})
	go func() {
		Publish(b, advEvtGamma{Base: hmevent.NewBase()})
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first handler never started")
	}

	cleared := make(chan struct{})
	go func() {
		b.ClearAllSubscriptions()
		close(cleared)
	}()
	// Give the clear time to mark the entries dead before the snapshot
	// advances to the second handler.
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish deadlocked")
	}
	select {
	case <-cleared:
	case <-time.After(2 * time.Second):
		t.Fatal("clear deadlocked")
	}

	if got := lateCalls.Load(); got != 0 {
		t.Errorf("handler ran %d times after a cross-goroutine clear, want 0", got)
	}
}
