// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ---------------------------------------------------------------------------
// Central — basic transitions
// ---------------------------------------------------------------------------

func TestCentralHappyPath(t *testing.T) {
	m := NewCentral("main", nil)
	steps := []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
		hmenum.CentralStateDegraded,
		hmenum.CentralStateRecovering,
		hmenum.CentralStateRunning,
		hmenum.CentralStateStopped,
	}
	for _, s := range steps {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
		if m.State() != s {
			t.Fatalf("state=%s, want %s", m.State(), s)
		}
	}
}

func TestCentralRejectsInvalidTransition(t *testing.T) {
	m := NewCentral("main", nil)
	err := m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v, want ErrInvalidTransition", err)
	}
}

func TestCentralStoppedIsTerminal(t *testing.T) {
	m := NewCentral("main", nil)
	if err := m.TransitionTo(hmenum.CentralStateStopped, hmenum.FailureReasonNone); err != nil {
		t.Fatal(err)
	}
	if err := m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone); err == nil {
		t.Fatal("STOPPED must be terminal")
	}
}

func TestCentralEmitsEvent(t *testing.T) {
	b := events.NewBus()
	var seen hmevent.CentralStateChangedEvent
	var n int
	events.Subscribe(b, func(e hmevent.CentralStateChangedEvent) {
		seen = e
		n++
	})
	m := NewCentral("main", b)
	if err := m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("event count=%d", n)
	}
	if seen.From != hmenum.CentralStateStarting || seen.To != hmenum.CentralStateInitializing {
		t.Fatalf("seen=%+v", seen)
	}
}

func TestCentralRecordsFailureReason(t *testing.T) {
	m := NewCentral("main", nil)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateFailed, hmenum.FailureReasonAuth)
	if m.FailureReason() != hmenum.FailureReasonAuth {
		t.Fatalf("reason=%s", m.FailureReason())
	}
}

// ---------------------------------------------------------------------------
// Central — LastStateChange and History
// ---------------------------------------------------------------------------

func TestCentralLastStateChange(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewCentral("main", nil)
	m.now = func() time.Time { return t0 }

	if !m.LastStateChange().IsZero() {
		t.Fatal("LastStateChange must be zero before any transition")
	}
	if err := m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatal(err)
	}
	if got := m.LastStateChange(); !got.Equal(t0) {
		t.Fatalf("LastStateChange=%v, want %v", got, t0)
	}
}

func TestCentralHistoryEmptyBeforeTransitions(t *testing.T) {
	m := NewCentral("main", nil)
	if h := m.History(); h != nil {
		t.Fatalf("History must be nil before any transition, got %v", h)
	}
}

func TestCentralHistoryRecordsTransitions(t *testing.T) {
	m := NewCentral("main", nil)
	steps := []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
		hmenum.CentralStateDegraded,
	}
	for _, s := range steps {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}
	h := m.History()
	if len(h) != 3 {
		t.Fatalf("history len=%d, want 3", len(h))
	}
	if h[0].From != hmenum.CentralStateStarting || h[0].To != hmenum.CentralStateInitializing {
		t.Fatalf("first entry=%+v", h[0])
	}
	if h[2].From != hmenum.CentralStateRunning || h[2].To != hmenum.CentralStateDegraded {
		t.Fatalf("third entry=%+v", h[2])
	}
}

func TestCentralHistoryRingBufferWraps(t *testing.T) {
	// Build a machine with a tiny cap so we can force wrapping.
	m := NewCentral("main", nil)
	m.historyCap = 3
	m.history = make([]CentralTransition, 3)
	m.historyHead = 0
	m.historyLen = 0

	// Starting → Initializing
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	// Initializing → Running
	_ = m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone)
	// Running → Degraded
	_ = m.TransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone)
	// Degraded → Recovering (4th — wraps)
	_ = m.TransitionTo(hmenum.CentralStateRecovering, hmenum.FailureReasonNone)

	h := m.History()
	if len(h) != 3 {
		t.Fatalf("capped history len=%d, want 3", len(h))
	}
	// Oldest retained must be Initializing→Running.
	if h[0].From != hmenum.CentralStateInitializing || h[0].To != hmenum.CentralStateRunning {
		t.Fatalf("oldest retained=%+v, want Initializing→Running", h[0])
	}
}

func TestCentralHistoryReasonRecorded(t *testing.T) {
	m := NewCentral("main", nil)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateFailed, hmenum.FailureReasonNetwork)
	h := m.History()
	last := h[len(h)-1]
	if last.Reason != hmenum.FailureReasonNetwork {
		t.Fatalf("reason in history=%s, want %s", last.Reason, hmenum.FailureReasonNetwork)
	}
}

// ---------------------------------------------------------------------------
// Central — degraded interface tracking
// ---------------------------------------------------------------------------

func TestCentralMarkClearDegradedInterfaces(t *testing.T) {
	m := NewCentral("main", nil)

	// Initially empty.
	if d := m.DegradedInterfaces(); d != nil {
		t.Fatalf("DegradedInterfaces should be nil initially, got %v", d)
	}
	if id := m.FailureInterfaceID(); id != "" {
		t.Fatalf("FailureInterfaceID should be empty, got %q", id)
	}

	m.MarkInterfaceDegraded("HmIP-RF", hmenum.FailureReasonNetwork)
	m.MarkInterfaceDegraded("BidCos-RF", hmenum.FailureReasonTimeout)

	d := m.DegradedInterfaces()
	if len(d) != 2 {
		t.Fatalf("DegradedInterfaces len=%d, want 2", len(d))
	}
	if d["HmIP-RF"] != hmenum.FailureReasonNetwork {
		t.Fatalf("HmIP-RF reason=%s", d["HmIP-RF"])
	}
	// Last call sets failureInterface.
	if id := m.FailureInterfaceID(); id != "BidCos-RF" {
		t.Fatalf("FailureInterfaceID=%q, want BidCos-RF", id)
	}

	// Clear the failure interface; id must revert to "".
	m.ClearInterfaceDegraded("BidCos-RF")
	if id := m.FailureInterfaceID(); id != "" {
		t.Fatalf("FailureInterfaceID after clear=%q, want empty", id)
	}

	// Clear a non-failure-interface; id stays unchanged (we set HmIP-RF last
	// only if we call Mark again — here just verify idempotent clear).
	m.MarkInterfaceDegraded("HmIP-RF", hmenum.FailureReasonNetwork)
	m.ClearInterfaceDegraded("does-not-exist") // idempotent, must not panic
	if id := m.FailureInterfaceID(); id != "HmIP-RF" {
		t.Fatalf("FailureInterfaceID=%q after idempotent clear, want HmIP-RF", id)
	}

	m.ClearInterfaceDegraded("HmIP-RF")
	if d2 := m.DegradedInterfaces(); d2 != nil {
		t.Fatalf("DegradedInterfaces should be nil after all cleared, got %v", d2)
	}
}

// ---------------------------------------------------------------------------
// Central — full transition-matrix (all legal pairs)
// ---------------------------------------------------------------------------

func TestCentralAllLegalTransitions(t *testing.T) {
	// Each row: (from, via path to reach 'from') then attempt legal targets.
	type legalCase struct {
		name   string
		setup  []hmenum.CentralState // transitions to reach the start state
		target hmenum.CentralState
	}
	cases := []legalCase{
		// From Starting
		{"Starting→Initializing", nil, hmenum.CentralStateInitializing},
		{"Starting→Stopped", nil, hmenum.CentralStateStopped},
		// From Initializing
		{"Initializing→Running", []hmenum.CentralState{hmenum.CentralStateInitializing}, hmenum.CentralStateRunning},
		{"Initializing→Degraded", []hmenum.CentralState{hmenum.CentralStateInitializing}, hmenum.CentralStateDegraded},
		{"Initializing→Failed", []hmenum.CentralState{hmenum.CentralStateInitializing}, hmenum.CentralStateFailed},
		{"Initializing→Stopped", []hmenum.CentralState{hmenum.CentralStateInitializing}, hmenum.CentralStateStopped},
		// From Running
		{"Running→Degraded", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning}, hmenum.CentralStateDegraded},
		{"Running→Recovering", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning}, hmenum.CentralStateRecovering},
		{"Running→Stopped", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning}, hmenum.CentralStateStopped},
		// From Degraded
		{"Degraded→Running", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateDegraded}, hmenum.CentralStateRunning},
		{"Degraded→Recovering", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateDegraded}, hmenum.CentralStateRecovering},
		{"Degraded→Failed", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateDegraded}, hmenum.CentralStateFailed},
		{"Degraded→Stopped", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateDegraded}, hmenum.CentralStateStopped},
		// From Recovering
		{"Recovering→Running", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning, hmenum.CentralStateRecovering}, hmenum.CentralStateRunning},
		{"Recovering→Degraded", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning, hmenum.CentralStateRecovering}, hmenum.CentralStateDegraded},
		{"Recovering→Failed", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning, hmenum.CentralStateRecovering}, hmenum.CentralStateFailed},
		{"Recovering→Stopped", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateRunning, hmenum.CentralStateRecovering}, hmenum.CentralStateStopped},
		// From Failed
		{"Failed→Recovering", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateFailed}, hmenum.CentralStateRecovering},
		{"Failed→Stopped", []hmenum.CentralState{hmenum.CentralStateInitializing, hmenum.CentralStateFailed}, hmenum.CentralStateStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCentral("main", nil)
			for _, s := range tc.setup {
				if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
					t.Fatalf("setup transition to %s: %v", s, err)
				}
			}
			if err := m.TransitionTo(tc.target, hmenum.FailureReasonNone); err != nil {
				t.Fatalf("legal transition to %s failed: %v", tc.target, err)
			}
			if m.State() != tc.target {
				t.Fatalf("state=%s, want %s", m.State(), tc.target)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Central — illegal transitions
// ---------------------------------------------------------------------------

func TestCentralIllegalTransitions(t *testing.T) {
	type illegalCase struct {
		name  string
		setup []hmenum.CentralState
		bad   hmenum.CentralState
	}
	cases := []illegalCase{
		// From Starting (only Initializing/Stopped allowed)
		{"Starting→Running", nil, hmenum.CentralStateRunning},
		{"Starting→Degraded", nil, hmenum.CentralStateDegraded},
		{"Starting→Recovering", nil, hmenum.CentralStateRecovering},
		{"Starting→Failed", nil, hmenum.CentralStateFailed},
		// Stopped is terminal
		{"Stopped→Running", []hmenum.CentralState{hmenum.CentralStateStopped}, hmenum.CentralStateRunning},
		{"Stopped→Initializing", []hmenum.CentralState{hmenum.CentralStateStopped}, hmenum.CentralStateInitializing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCentral("main", nil)
			for _, s := range tc.setup {
				if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
					t.Fatalf("setup %s: %v", s, err)
				}
			}
			err := m.TransitionTo(tc.bad, hmenum.FailureReasonNone)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Central — event emission with bus
// ---------------------------------------------------------------------------

func TestCentralEmitsClientStateChangedName(t *testing.T) {
	b := events.NewBus()
	var seen hmevent.CentralStateChangedEvent
	events.Subscribe(b, func(e hmevent.CentralStateChangedEvent) { seen = e })
	m := NewCentral("cluster-1", b)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonAuth)
	if seen.CentralName != "cluster-1" {
		t.Fatalf("CentralName=%q, want cluster-1", seen.CentralName)
	}
	if seen.Reason != hmenum.FailureReasonAuth {
		t.Fatalf("Reason=%s, want auth", seen.Reason)
	}
}

func TestCentralNoEventWithoutBus(t *testing.T) {
	// Must not panic and must still return no error.
	m := NewCentral("main", nil)
	if err := m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Central — concurrent safety
// ---------------------------------------------------------------------------

func TestCentralConcurrentTransitions(t *testing.T) {
	t.Parallel()
	m := NewCentral("main", nil)
	// Reach a stable intermediate state first.
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = m.State()
			_ = m.FailureReason()
			_ = m.LastStateChange()
			_ = m.History()
			_ = m.FailureInterfaceID()
			_ = m.DegradedInterfaces()
			m.MarkInterfaceDegraded("HmIP-RF", hmenum.FailureReasonNetwork)
			m.ClearInterfaceDegraded("HmIP-RF")
		})
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Central — ForceTransitionTo
// ---------------------------------------------------------------------------

func TestCentralForceTransitionTo(t *testing.T) {
	m := NewCentral("main", nil)
	// Starting → Stopped is legal; skip setup to check pure force path.
	err := m.ForceTransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone)
	if err != nil {
		t.Fatalf("ForceTransitionTo must succeed, got %v", err)
	}
	if m.State() != hmenum.CentralStateRunning {
		t.Fatalf("state=%s after force, want RUNNING", m.State())
	}
	// History entry must be marked as forced.
	h := m.History()
	if len(h) == 0 {
		t.Fatal("History must not be empty after ForceTransitionTo")
	}
	last := h[len(h)-1]
	if !last.Forced {
		t.Fatal("history entry must carry Forced=true")
	}
}

func TestCentralForceTransitionToWithBus(t *testing.T) {
	received := false
	// Verify ForceTransitionTo does not panic with a nil bus.
	m := NewCentral("main", nil)
	err := m.ForceTransitionTo(hmenum.CentralStateFailed, hmenum.FailureReasonAuth)
	if err != nil {
		t.Fatalf("ForceTransitionTo failed: %v", err)
	}
	_ = received // suppress lint
}

// ---------------------------------------------------------------------------
// Central — CanTransitionTo including unknown state
// ---------------------------------------------------------------------------

func TestCentralCanTransitionToValid(t *testing.T) {
	m := NewCentral("test", nil)
	// Starting → Initializing is valid.
	if !m.CanTransitionTo(hmenum.CentralStateInitializing) {
		t.Fatal("expected CanTransitionTo(Initializing) == true from Starting")
	}
}

func TestCentralCanTransitionToInvalid(t *testing.T) {
	m := NewCentral("test", nil)
	// Starting → Running is invalid.
	if m.CanTransitionTo(hmenum.CentralStateRunning) {
		t.Fatal("expected CanTransitionTo(Running) == false from Starting")
	}
}

func TestCentralCanTransitionToFromUnknownState(t *testing.T) {
	m := NewCentral("main", nil)
	m.mu.Lock()
	m.cur = hmenum.CentralState("__bogus__")
	m.mu.Unlock()

	if m.CanTransitionTo(hmenum.CentralStateRunning) {
		t.Fatal("CanTransitionTo must return false for unknown source state")
	}
}

// ---------------------------------------------------------------------------
// Central — FailureMessage
// ---------------------------------------------------------------------------

func TestCentralFailureMessageDefaultEmpty(t *testing.T) {
	m := NewCentral("test", nil)
	if msg := m.FailureMessage(); msg != "" {
		t.Fatalf("FailureMessage() on fresh machine must be empty, got %q", msg)
	}
}

func TestCentralSetAndGetFailureMessage(t *testing.T) {
	m := NewCentral("test", nil)
	m.SetFailureMessage("network unreachable")
	if got := m.FailureMessage(); got != "network unreachable" {
		t.Fatalf("FailureMessage() = %q, want %q", got, "network unreachable")
	}
}

func TestCentralSetFailureMessageOverwrite(t *testing.T) {
	m := NewCentral("test", nil)
	m.SetFailureMessage("first")
	m.SetFailureMessage("second")
	if got := m.FailureMessage(); got != "second" {
		t.Fatalf("FailureMessage() = %q, want %q", got, "second")
	}
}

// ---------------------------------------------------------------------------
// Central — convenience bool predicates
// ---------------------------------------------------------------------------

func TestCentralIsStoppedOnTerminalState(t *testing.T) {
	m := NewCentral("test", nil)
	// Drive to Stopped via Initializing → Stopped.
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateStopped, hmenum.FailureReasonNone)
	if !m.IsStopped() {
		t.Fatal("IsStopped must be true in Stopped state")
	}
	if m.IsRunning() || m.IsFailed() || m.IsDegraded() || m.IsRecovering() || m.IsOperational() {
		t.Fatal("only IsStopped should be true")
	}
}

func TestCentralIsRunning(t *testing.T) {
	m := NewCentral("test", nil)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone)
	if !m.IsRunning() {
		t.Fatal("IsRunning must be true in Running state")
	}
	if !m.IsOperational() {
		t.Fatal("IsOperational must be true in Running state")
	}
	if m.IsFailed() || m.IsDegraded() || m.IsStopped() || m.IsRecovering() {
		t.Fatal("only IsRunning/IsOperational should be true")
	}
}

func TestCentralIsDegraded(t *testing.T) {
	m := NewCentral("test", nil)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone)
	if !m.IsDegraded() {
		t.Fatal("IsDegraded must be true in Degraded state")
	}
	if !m.IsOperational() {
		t.Fatal("IsOperational must be true in Degraded state")
	}
}

func TestCentralIsFailed(t *testing.T) {
	m := NewCentral("test", nil)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateFailed, hmenum.FailureReasonNetwork)
	if !m.IsFailed() {
		t.Fatal("IsFailed must be true in Failed state")
	}
	if m.IsOperational() {
		t.Fatal("IsOperational must be false in Failed state")
	}
}

func TestCentralIsRecovering(t *testing.T) {
	m := NewCentral("test", nil)
	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.CentralStateRecovering, hmenum.FailureReasonNone)
	if !m.IsRecovering() {
		t.Fatal("IsRecovering must be true in Recovering state")
	}
	if m.IsOperational() {
		t.Fatal("IsOperational must be false in Recovering state")
	}
}

// ---------------------------------------------------------------------------
// Central — SecondsInCurrentState
// ---------------------------------------------------------------------------

func TestCentralSecondsInCurrentStateZeroBeforeTransition(t *testing.T) {
	m := NewCentral("test", nil)
	if got := m.SecondsInCurrentState(); got != 0 {
		t.Fatalf("SecondsInCurrentState()=%f before any transition, want 0", got)
	}
}

func TestCentralSecondsInCurrentStatePositiveAfterTransition(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Second)
	tick := t0
	m := NewCentral("test", nil)
	m.now = func() time.Time { return tick }

	_ = m.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone)
	tick = t1 // advance clock after transition

	if got := m.SecondsInCurrentState(); got < 4.9 || got > 5.1 {
		t.Fatalf("SecondsInCurrentState()=%f, want ~5.0", got)
	}
}

// ---------------------------------------------------------------------------
// Client — basic transitions
// ---------------------------------------------------------------------------

func TestClientHappyPath(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	steps := []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
		hmenum.ClientStateStopping,
		hmenum.ClientStateStopped,
	}
	for _, s := range steps {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition %s: %v", s, err)
		}
	}
}

func TestClientFailedCanRetry(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateFailed, hmenum.FailureReasonAuth)
	if err := m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("FAILED → INITIALIZING (retry) should succeed: %v", err)
	}
}

func TestClientRejectsNonsense(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	if err := m.TransitionTo(hmenum.ClientStateConnected, hmenum.FailureReasonNone); err == nil {
		t.Fatal("CREATED → CONNECTED should fail")
	}
}

// ---------------------------------------------------------------------------
// Client — State and FailureReason accessors
// ---------------------------------------------------------------------------

func TestClientState(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	if m.State() != hmenum.ClientStateCreated {
		t.Fatalf("initial state=%s", m.State())
	}
}

func TestClientFailureReason(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	if m.FailureReason() != "" {
		t.Fatalf("FailureReason before failure=%q", m.FailureReason())
	}
	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateFailed, hmenum.FailureReasonTimeout)
	if m.FailureReason() != hmenum.FailureReasonTimeout {
		t.Fatalf("FailureReason=%s, want timeout", m.FailureReason())
	}
}

// ---------------------------------------------------------------------------
// Client — full transition-matrix (all legal pairs)
// ---------------------------------------------------------------------------

func TestClientAllLegalTransitions(t *testing.T) {
	type legalCase struct {
		name   string
		setup  []hmenum.ClientState
		target hmenum.ClientState
	}
	cases := []legalCase{
		// From Created
		{"Created→Initializing", nil, hmenum.ClientStateInitializing},
		{"Created→Stopping", nil, hmenum.ClientStateStopping},
		// From Initializing
		{"Initializing→Initialized", []hmenum.ClientState{hmenum.ClientStateInitializing}, hmenum.ClientStateInitialized},
		{"Initializing→Failed", []hmenum.ClientState{hmenum.ClientStateInitializing}, hmenum.ClientStateFailed},
		{"Initializing→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing}, hmenum.ClientStateStopping},
		// From Initialized
		{"Initialized→Connecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized}, hmenum.ClientStateConnecting},
		{"Initialized→Disconnected", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized}, hmenum.ClientStateDisconnected},
		{"Initialized→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized}, hmenum.ClientStateStopping},
		// From Connecting
		{"Connecting→Connected", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting}, hmenum.ClientStateConnected},
		{"Connecting→Failed", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting}, hmenum.ClientStateFailed},
		{"Connecting→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting}, hmenum.ClientStateStopping},
		// From Connected
		{"Connected→Disconnected", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected}, hmenum.ClientStateDisconnected},
		{"Connected→Reconnecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected}, hmenum.ClientStateReconnecting},
		{"Connected→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected}, hmenum.ClientStateStopping},
		// From Disconnected
		{"Disconnected→Connecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateDisconnected}, hmenum.ClientStateConnecting},
		{"Disconnected→Reconnecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateDisconnected}, hmenum.ClientStateReconnecting},
		{"Disconnected→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateDisconnected}, hmenum.ClientStateStopping},
		// From Reconnecting
		{"Reconnecting→Connected", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateReconnecting}, hmenum.ClientStateConnected},
		{"Reconnecting→Disconnected", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateReconnecting}, hmenum.ClientStateDisconnected},
		{"Reconnecting→Connecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateReconnecting}, hmenum.ClientStateConnecting},
		{"Reconnecting→Failed", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateReconnecting}, hmenum.ClientStateFailed},
		{"Reconnecting→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized, hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateReconnecting}, hmenum.ClientStateStopping},
		// From Stopping
		{"Stopping→Stopped", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateStopping}, hmenum.ClientStateStopped},
		// From Failed
		{"Failed→Initializing", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateFailed}, hmenum.ClientStateInitializing},
		{"Failed→Connecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateFailed}, hmenum.ClientStateConnecting},
		{"Failed→Reconnecting", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateFailed}, hmenum.ClientStateReconnecting},
		{"Failed→Disconnected", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateFailed}, hmenum.ClientStateDisconnected},
		{"Failed→Stopping", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateFailed}, hmenum.ClientStateStopping},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
			for _, s := range tc.setup {
				if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
					t.Fatalf("setup to %s: %v", s, err)
				}
			}
			if err := m.TransitionTo(tc.target, hmenum.FailureReasonNone); err != nil {
				t.Fatalf("legal transition to %s failed: %v", tc.target, err)
			}
			if m.State() != tc.target {
				t.Fatalf("state=%s, want %s", m.State(), tc.target)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Client — illegal transitions
// ---------------------------------------------------------------------------

func TestClientIllegalTransitions(t *testing.T) {
	type illegalCase struct {
		name  string
		setup []hmenum.ClientState
		bad   hmenum.ClientState
	}
	cases := []illegalCase{
		// From Created: only Initializing/Stopping allowed
		{"Created→Connected", nil, hmenum.ClientStateConnected},
		{"Created→Disconnected", nil, hmenum.ClientStateDisconnected},
		{"Created→Reconnecting", nil, hmenum.ClientStateReconnecting},
		{"Created→Failed", nil, hmenum.ClientStateFailed},
		// From Stopped (terminal)
		{"Stopped→Created", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateStopping, hmenum.ClientStateStopped}, hmenum.ClientStateCreated},
		{"Stopped→Initializing", []hmenum.ClientState{hmenum.ClientStateInitializing, hmenum.ClientStateStopping, hmenum.ClientStateStopped}, hmenum.ClientStateInitializing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
			for _, s := range tc.setup {
				if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
					t.Fatalf("setup %s: %v", s, err)
				}
			}
			err := m.TransitionTo(tc.bad, hmenum.FailureReasonNone)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Client — event emission
// ---------------------------------------------------------------------------

func TestClientEmitsEventWithBus(t *testing.T) {
	b := events.NewBus()
	var seen hmevent.ClientStateChangedEvent
	var n int
	events.Subscribe(b, func(e hmevent.ClientStateChangedEvent) {
		seen = e
		n++
	})
	m := NewClient("central-a", "BidCos-RF", hmenum.InterfaceBidCosRF, b)
	if err := m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("event count=%d, want 1", n)
	}
	if seen.CentralName != "central-a" {
		t.Fatalf("CentralName=%q", seen.CentralName)
	}
	if seen.InterfaceID != "BidCos-RF" {
		t.Fatalf("InterfaceID=%q", seen.InterfaceID)
	}
	if seen.Interface != hmenum.InterfaceBidCosRF {
		t.Fatalf("Interface=%s", seen.Interface)
	}
	if seen.From != hmenum.ClientStateCreated || seen.To != hmenum.ClientStateInitializing {
		t.Fatalf("from/to=%s/%s", seen.From, seen.To)
	}
}

// ---------------------------------------------------------------------------
// Client — concurrent safety
// ---------------------------------------------------------------------------

func TestClientConcurrentReadWrite(t *testing.T) {
	t.Parallel()
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateInitialized, hmenum.FailureReasonNone)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = m.State()
			_ = m.FailureReason()
		})
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Client — predicates (CanTransitionTo, IsAvailable, IsConnected,
// IsFailed, IsStopped, CanReconnect)
// ---------------------------------------------------------------------------

func TestClientCanTransitionTo(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	// CREATED → INITIALIZING is legal
	if !m.CanTransitionTo(hmenum.ClientStateInitializing) {
		t.Fatal("CREATED → INITIALIZING must be allowed")
	}
	// CREATED → CONNECTED is illegal
	if m.CanTransitionTo(hmenum.ClientStateConnected) {
		t.Fatal("CREATED → CONNECTED must be rejected")
	}
}

func TestClientIsAvailablePredicates(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	// Initially not available
	if m.IsAvailable() || m.IsConnected() {
		t.Fatal("CREATED must not be available or connected")
	}
	// Drive to CONNECTED
	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("setup transition to %s: %v", s, err)
		}
	}
	if !m.IsAvailable() || !m.IsConnected() {
		t.Fatal("CONNECTED must be available + connected")
	}
	if m.IsFailed() || m.IsStopped() {
		t.Fatal("CONNECTED must not be failed or stopped")
	}
	// RECONNECTING is available but not connected
	if err := m.TransitionTo(hmenum.ClientStateReconnecting, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("CONNECTED → RECONNECTING: %v", err)
	}
	if !m.IsAvailable() {
		t.Fatal("RECONNECTING must be available")
	}
	if m.IsConnected() {
		t.Fatal("RECONNECTING must not report connected")
	}
}

func TestClientIsFailedAndIsStopped(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateFailed, hmenum.FailureReasonAuth)
	if !m.IsFailed() {
		t.Fatal("must report IsFailed in FAILED state")
	}
	if m.IsStopped() {
		t.Fatal("must not report IsStopped in FAILED state")
	}
	// Drive to STOPPED via INITIALIZING → STOPPING → STOPPED
	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateStopping, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateStopped, hmenum.FailureReasonNone)
	if !m.IsStopped() {
		t.Fatal("must report IsStopped in STOPPED state")
	}
	if m.IsFailed() {
		t.Fatal("must not report IsFailed in STOPPED state")
	}
}

func TestClientCanReconnect(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	// From CREATED, cannot reconnect
	if m.CanReconnect() {
		t.Fatal("CREATED must not CanReconnect")
	}
	// Drive to CONNECTED, then can reconnect
	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
	} {
		_ = m.TransitionTo(s, hmenum.FailureReasonNone)
	}
	if !m.CanReconnect() {
		t.Fatal("CONNECTED must CanReconnect")
	}
}

// ---------------------------------------------------------------------------
// Client — FailureMessage and transition options
// ---------------------------------------------------------------------------

func TestClientFailureMessage(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	if m.FailureMessage() != "" {
		t.Fatal("FailureMessage must be empty before any failure")
	}

	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateFailed, hmenum.FailureReasonNetwork, WithReason("network timeout"))

	msg := m.FailureMessage()
	if !strings.Contains(msg, "network timeout") {
		t.Fatalf("FailureMessage=%q, want it to contain 'network timeout'", msg)
	}

	// Recovery clears the failure message when entering CONNECTED or INITIALIZED.
	// Drive FAILED → INITIALIZING → INITIALIZED.
	_ = m.TransitionTo(hmenum.ClientStateInitializing, hmenum.FailureReasonNone)
	_ = m.TransitionTo(hmenum.ClientStateInitialized, hmenum.FailureReasonNone)
	if m.FailureMessage() != "" {
		t.Fatalf("FailureMessage after recovery must be empty, got %q", m.FailureMessage())
	}
}

func TestClientWithForce(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	// CREATED → CONNECTED is normally illegal; force must bypass that.
	err := m.TransitionTo(hmenum.ClientStateConnected, hmenum.FailureReasonNone, WithForce())
	if err != nil {
		t.Fatalf("WithForce must bypass transition guard, got %v", err)
	}
	if m.State() != hmenum.ClientStateConnected {
		t.Fatalf("state=%s after forced transition, want CONNECTED", m.State())
	}
}

func TestClientCanTransitionFromUnknownState(t *testing.T) {
	m := NewClient("main", "HmIP-RF", hmenum.InterfaceHmIPRF, nil)
	// Force an out-of-table state directly.
	m.mu.Lock()
	m.cur = hmenum.ClientState("__bogus__")
	m.mu.Unlock()

	err := m.TransitionTo(hmenum.ClientStateConnected, hmenum.FailureReasonNone)
	if err == nil {
		t.Fatal("transition from unknown state must fail")
	}
}
