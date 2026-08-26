// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// errTransportDown is the transport failure that drives the breaker open.
var errTransportDown = errors.New("transport down")

// countingTransport records how often the wire was actually touched and
// can be switched between failing and succeeding.
type countingTransport struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (tr *countingTransport) call(_ context.Context, _ string, _ []any) (any, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.calls++
	if tr.fail {
		return nil, errTransportDown
	}
	return nil, nil
}

func (tr *countingTransport) count() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.calls
}

// TestACriticalWriteIsAttemptedWhileTheBreakerIsOpen measures the one
// property S5 rests on, through the assembly production uses.
//
// S5 — stop beats everything — says a stop is attempted even when the
// circuit breaker for the interface is open, as a single probe. The
// breaker implements exactly that, and selects it by asking whether the
// call carries [hmenum.CommandPriorityCritical]. The alarm engine's
// stoppers pass Critical, and every layer between them and the wire
// declares a priority parameter.
//
// The two ends were tested and the trip between them was not: the alarm
// side asserts Critical reaches the custom data point, the reliability
// side asserts Critical is let through an open breaker. Nothing asserted
// that the value observed at the second end is the one handed in at the
// first.
//
// The assertion is on the effect rather than on the value, deliberately.
// A test that reads the priority off some seam has to pick the seam, and
// picking the wrong one is how a chain test ends up confirming its own
// setup. An open breaker answers the question the invariant actually
// asks: did the command reach the wire, or did it not.
func TestACriticalWriteIsAttemptedWhileTheBreakerIsOpen(t *testing.T) {
	t.Parallel()

	tr := &countingTransport{fail: true}
	w := priorityChainWriter(t, tr)

	// Drive the breaker open with ordinary traffic.
	for range 12 {
		_ = w.SetValue(context.Background(), "ccu-prio", "HmIP-RF", "ABC0001:1",
			hmenum.ParameterState, false, hmenum.CommandPriorityLow)
	}
	openedAt := tr.count()
	if openedAt == 0 {
		t.Fatal("no wire attempt at all — the harness never reached the transport")
	}

	// A low-priority write must now be rejected without touching the wire:
	// that is what confirms the breaker is open, so the critical case below
	// is measuring the bypass and not a breaker that never tripped.
	_ = w.SetValue(context.Background(), "ccu-prio", "HmIP-RF", "ABC0001:1",
		hmenum.ParameterState, false, hmenum.CommandPriorityLow)
	if got := tr.count(); got != openedAt {
		t.Fatalf("a low-priority write still reached the wire (%d → %d attempts); the breaker is not "+
			"open, so this test cannot say anything about the critical path", openedAt, got)
	}

	// The stop. It has to reach the wire.
	//
	// The assertion is "at least once", not an exact count: the breaker
	// admits one probe at a time, but the retrier sits above it and will
	// take another probe per attempt. Pinning the total here would pin
	// the retry configuration instead of the invariant.
	_ = w.SetValue(context.Background(), "ccu-prio", "HmIP-RF", "ABC0001:1",
		hmenum.ParameterState, false, hmenum.CommandPriorityCritical)
	if got := tr.count(); got <= openedAt {
		t.Errorf("the critical write produced no wire attempt past the open breaker. A siren stop " +
			"the breaker refuses never reaches the device, which is the failure S5 names in as " +
			"many words: the alarm is screaming and nothing can stop it")
	}
}

// priorityChainWriter assembles the production write path: a
// ValueWriter over a real CcuBackend over a real BackendCaller over a
// real InterfaceClient with a real circuit breaker. Only the transport
// underneath is a fake, because that is the part a test has to be able
// to break.
func priorityChainWriter(t *testing.T, tr *countingTransport) *ValueWriter {
	t.Helper()
	ic, err := New(Config{
		CentralName: "ccu-prio",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(tr.call),
		Circuit: reliability.NewCircuit(reliability.CircuitConfig{
			FailureThreshold: 3,
		}),
		// A non-positive backoff is normalised back to the production
		// 2s/30s, so "do not sleep" has to be spelled as the shortest
		// positive delay. This test counts attempts, not seconds.
		Retrier: reliability.NewRetrier(reliability.RetryConfig{
			MaxAttempts: 3,
			Initial:     time.Microsecond,
			Max:         time.Microsecond,
		}),
	})
	if err != nil {
		t.Fatalf("New IC: %v", err)
	}
	t.Cleanup(ic.Close)

	// The composition root builds exactly this: one caller per interface,
	// constructed at low priority for the ordinary wire path.
	b, err := backends.FactoryWithKind(hmenum.InterfaceHmIPRF, backends.KindCCU, backends.FactoryInput{
		XMLRPC: NewBackendCaller(ic, hmenum.CommandPriorityLow),
	})
	if err != nil {
		t.Fatalf("backend factory: %v", err)
	}
	w := NewValueWriter()
	w.Register("ccu-prio", "HmIP-RF", b)
	return w
}
