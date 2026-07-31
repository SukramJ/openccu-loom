// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package operational

// White-box tests for the StartReaper background sweep — the production
// activation path (daemon wiring calls StartReaper with
// [SessionIdleTimeout] / a 60 s poll; the bridge's receive and seal
// chokepoints refresh MarkActiveRx / MarkActiveTx). Mirrors matter.js
// packages/protocol/src/session/Session.ts activity timestamps; the
// graceful farewell semantics of the sweep itself are pinned in
// manager_close_parity_test.go.

import (
	"context"
	"testing"
	"time"
)

// TestStartReaper_EvictsIdleSessionGracefully verifies the end-to-end
// background path: a session whose last activity falls behind the idle
// TTL is evicted by the periodic sweep, with the graceful CloseSession
// notifier and the close cascade firing exactly as for a direct
// reapIdle call.
func TestStartReaper_EvictsIdleSessionGracefully(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())
	rec := &closeRecorder{}
	rec.wire(m)

	entry, err := m.OpenFromPase(0xB0B, 0xA11, 0x2222, []byte("shared-secret"))
	if err != nil {
		t.Fatalf("OpenFromPase: %v", err)
	}
	entry.MarkActiveRx() // traffic once, then silence

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.StartReaper(ctx, 30*time.Millisecond, 10*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for m.Active() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := m.Active(); n != 0 {
		t.Fatalf("Active() = %d, want 0 — reaper did not evict the idle session", n)
	}
	// Active() dropping to zero and the notifiers having run are two
	// different observables: the reaper removes the session from the count
	// before it notifies, so asserting straight after the loop above races
	// the reaper's own tail. Wait for what the assertions are about.
	var graceful, closed []uint16
	for deadline = time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		graceful, closed, _, _ = rec.snapshot()
		if len(graceful) >= 1 && len(closed) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(graceful) != 1 || graceful[0] != entry.SessionID {
		t.Errorf("graceful notifier calls = %v, want [%d]", graceful, entry.SessionID)
	}
	if len(closed) != 1 || closed[0] != entry.SessionID {
		t.Errorf("onSessionClose calls = %v, want [%d]", closed, entry.SessionID)
	}
}

// TestStartReaper_ActiveSessionSurvivesSweeps verifies the keep-alive
// contract the production TTL depends on: a session whose peer keeps
// sending (subscription heartbeat acks → MarkActiveRx) survives many
// sweep intervals, and only becomes reap-eligible once the traffic
// stops. Concurrent marking against the running sweep also exercises
// the Entry/Manager locking under -race.
func TestStartReaper_ActiveSessionSurvivesSweeps(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())
	rec := &closeRecorder{}
	rec.wire(m)

	entry, err := m.OpenFromPase(0xB0B, 0xA11, 0x2222, []byte("shared-secret"))
	if err != nil {
		t.Fatalf("OpenFromPase: %v", err)
	}
	entry.MarkActiveRx()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.StartReaper(ctx, 50*time.Millisecond, 10*time.Millisecond)

	// Keep the peer "alive" for well over the TTL: mark every 10 ms for
	// 200 ms (4× the idle timeout) while the sweep runs concurrently.
	stopMarking := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(stopMarking) {
		entry.MarkActiveRx()
		time.Sleep(10 * time.Millisecond)
	}
	if n := m.Active(); n != 1 {
		t.Fatalf("Active() = %d during live traffic, want 1 — reaper evicted an active session", n)
	}

	// Silence: the session must now age out.
	deadline := time.Now().Add(2 * time.Second)
	for m.Active() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := m.Active(); n != 0 {
		t.Fatalf("Active() = %d after traffic stopped, want 0", n)
	}
}

// TestStartReaper_NeverMarkedSessionIsNotReaped pins the zero-activity
// guard: an established session that has never exchanged a message
// (commissioning may still be in flight) is exempt from the sweep —
// the reaper only trusts sessions whose idle clock has started, i.e.
// after the receive path recorded their first authenticated message.
func TestStartReaper_NeverMarkedSessionIsNotReaped(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())
	rec := &closeRecorder{}
	rec.wire(m)

	if _, err := m.OpenFromPase(0xB0B, 0xA11, 0x2222, []byte("shared-secret")); err != nil {
		t.Fatalf("OpenFromPase: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.StartReaper(ctx, 20*time.Millisecond, 10*time.Millisecond)

	time.Sleep(150 * time.Millisecond) // several TTL windows
	if n := m.Active(); n != 1 {
		t.Fatalf("Active() = %d, want 1 — a never-used session must not be reaped", n)
	}
	if graceful, closed, _, _ := rec.snapshot(); len(graceful) != 0 || len(closed) != 0 {
		t.Errorf("close hooks fired (graceful %v / closed %v) for a never-used session", graceful, closed)
	}
}
