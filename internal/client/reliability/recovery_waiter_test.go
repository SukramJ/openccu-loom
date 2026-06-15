// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests that the circuit-recovery waiter appends its state-change hook rather
// than replacing the breaker's primary listener, so multiple waiters on one
// breaker are all woken when it recovers.
package reliability

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestCircuitRecoveryWaiterWakesAllWaitersOnRecovery(t *testing.T) {
	cb := NewCircuit(CircuitConfig{FailureThreshold: 1})
	cb.RecordFailure()
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("breaker state = %v, want OPEN before recovery", cb.State())
	}

	w1, ok1 := NewCircuitRecoveryWaiter(cb).(*circuitRecoveryWaiter)
	w2, ok2 := NewCircuitRecoveryWaiter(cb).(*circuitRecoveryWaiter)
	if !ok1 || !ok2 {
		t.Fatal("NewCircuitRecoveryWaiter did not return *circuitRecoveryWaiter")
	}
	w1.ensureHook()
	w2.ensureHook()

	ch1 := make(chan struct{})
	ch2 := make(chan struct{})
	register := func(w *circuitRecoveryWaiter, ch chan struct{}) {
		w.mu.Lock()
		w.wakers = append(w.wakers, ch)
		w.mu.Unlock()
	}
	register(w1, ch1)
	register(w2, ch2)

	cb.Reset() // OPEN -> CLOSED fires every registered hook synchronously

	for name, ch := range map[string]chan struct{}{"first waiter": ch1, "second waiter": ch2} {
		select {
		case <-ch:
		default:
			t.Errorf("%s was not woken — ensureHook replaced a listener instead of appending", name)
		}
	}
}
