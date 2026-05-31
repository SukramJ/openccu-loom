// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/statemachine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestConnectionStateOnChangedCallback_AddRemove verifies that the
// SetOnChanged hook fires with the correct (source, connected) pair for
// AddIssue and RemoveIssue.
func TestConnectionStateOnChangedCallback_AddRemove(t *testing.T) {
	type call struct {
		source    string
		connected bool
	}
	var mu sync.Mutex
	var calls []call

	cs := statemachine.NewConnectionState()
	cs.SetOnChanged(func(source string, connected bool) {
		mu.Lock()
		calls = append(calls, call{source, connected})
		mu.Unlock()
	})

	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "ping timeout")

	mu.Lock()
	if len(calls) != 1 {
		t.Fatalf("AddIssue: expected 1 callback, got %d", len(calls))
	}
	if calls[0].source != "HmIP-RF" || calls[0].connected {
		t.Fatalf("AddIssue callback: got (%q, %v), want (HmIP-RF, false)",
			calls[0].source, calls[0].connected)
	}
	mu.Unlock()

	cs.RemoveIssue("HmIP-RF")

	mu.Lock()
	if len(calls) != 2 {
		t.Fatalf("RemoveIssue: expected 2 callbacks total, got %d", len(calls))
	}
	if calls[1].source != "HmIP-RF" || !calls[1].connected {
		t.Fatalf("RemoveIssue callback: got (%q, %v), want (HmIP-RF, true)",
			calls[1].source, calls[1].connected)
	}
	mu.Unlock()
}

// TestConnectionStateOnChangedCallback_ClearAll verifies that
// ClearAllIssues fires no callbacks (the Go implementation batch-clears
// without per-source notifications — the design rationale is that callers
// publish a single SystemStatusChangedEvent with the full issue list instead).
func TestConnectionStateOnChangedCallback_ClearAll(t *testing.T) {
	var mu sync.Mutex
	var calls []string

	cs := statemachine.NewConnectionState()
	cs.SetOnChanged(func(source string, _ bool) {
		mu.Lock()
		calls = append(calls, source)
		mu.Unlock()
	})

	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "lost")
	cs.AddIssue("BidCos-RF", hmenum.FailureReasonNetwork, "lost")

	mu.Lock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 callbacks after AddIssue x2, got %d", len(calls))
	}
	mu.Unlock()

	cs.ClearAllIssues()

	if cs.HasAnyIssue() {
		t.Fatal("ClearAllIssues: HasAnyIssue must be false after clear")
	}
	if cs.IssueCount() != 0 {
		t.Fatalf("ClearAllIssues: IssueCount must be 0, got %d", cs.IssueCount())
	}
}

// TestConnectionStateTransitions exercises the add/remove sequence
// through degraded-to-healthy and multiple-interface transitions.
func TestConnectionStateTransitions(t *testing.T) {
	type call struct {
		source    string
		connected bool
	}
	var mu sync.Mutex
	var history []call

	cs := statemachine.NewConnectionState()
	cs.SetOnChanged(func(source string, connected bool) {
		mu.Lock()
		history = append(history, call{source, connected})
		mu.Unlock()
	})

	// Both interfaces go down.
	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "timeout")
	cs.AddIssue("BidCos-RF", hmenum.FailureReasonNetwork, "timeout")

	// One recovers.
	cs.RemoveIssue("HmIP-RF")
	if cs.IssueCount() != 1 {
		t.Fatalf("after one recovery: IssueCount=%d, want 1", cs.IssueCount())
	}

	// Second recovers.
	cs.RemoveIssue("BidCos-RF")
	if cs.HasAnyIssue() {
		t.Fatal("after both recoveries: HasAnyIssue must be false")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(history) != 4 {
		t.Fatalf("expected 4 callback events, got %d", len(history))
	}
	disconnects := 0
	reconnects := 0
	for _, c := range history {
		if c.connected {
			reconnects++
		} else {
			disconnects++
		}
	}
	if disconnects != 2 || reconnects != 2 {
		t.Fatalf("expected 2 disconnects + 2 reconnects, got %d/%d", disconnects, reconnects)
	}
}

// TestConnectionStateSubscriptionPersists verifies that the
// ConnectionState callback survives multiple disconnect/reconnect cycles.
func TestConnectionStateSubscriptionPersists(t *testing.T) {
	const cycles = 5

	var mu sync.Mutex
	var events []bool // true = reconnect, false = disconnect

	cs := statemachine.NewConnectionState()
	cs.SetOnChanged(func(_ string, connected bool) {
		mu.Lock()
		events = append(events, connected)
		mu.Unlock()
	})

	for i := range cycles {
		cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "lost")
		cs.RemoveIssue("HmIP-RF")
		_ = i // suppress loop-variable shadow lint
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != cycles*2 {
		t.Fatalf("expected %d callback events, got %d", cycles*2, len(events))
	}
	for i, connected := range events {
		wantConnected := i%2 == 1 // odd = reconnect
		if connected != wantConnected {
			t.Errorf("event[%d]: got connected=%v, want %v", i, connected, wantConnected)
		}
	}
}

// TestConnectionStateConcurrentOps verifies that concurrent
// AddIssue / RemoveIssue calls do not corrupt the state.
func TestConnectionStateConcurrentOps(t *testing.T) {
	cs := statemachine.NewConnectionState()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range n {
			src := "HmIP-RF"
			cs.AddIssue(src, hmenum.FailureReasonNetwork, "json")
			_ = i
			cs.RemoveIssue(src)
		}
	}()

	go func() {
		defer wg.Done()
		for i := range n {
			src := "BidCos-RF"
			cs.AddIssue(src, hmenum.FailureReasonNetwork, "rpc")
			_ = i
			cs.RemoveIssue(src)
		}
	}()

	wg.Wait()

	if cs.HasAnyIssue() {
		t.Fatalf("after all ops: HasAnyIssue=true, expected clean state")
	}
}
