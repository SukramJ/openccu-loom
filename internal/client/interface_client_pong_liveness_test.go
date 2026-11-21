// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// pongLivenessHarness drives the keepalive of one client through
// CheckConnectionAvailability and remembers the token of every ping that
// reached the wire, so a test can answer exactly the PONGs it wants to.
type pongLivenessHarness struct {
	c      *InterfaceClient
	mu     sync.Mutex
	tokens []string
}

func newPongLivenessHarness(t *testing.T, kind backends.Kind, circuit *reliability.CircuitBreaker) *pongLivenessHarness {
	t.Helper()
	h := &pongLivenessHarness{}
	c, err := New(Config{
		CentralName: "ccu-pong",
		Interface:   hmenum.InterfaceHmIPRF,
		Circuit:     circuit,
		Caller: CallerFunc(func(_ context.Context, _ string, params []any) (any, error) {
			if len(params) == 1 {
				if callerID, ok := params[0].(string); ok {
					if _, token, tokenized := ParsePingCallerID(callerID); tokenized {
						h.mu.Lock()
						h.tokens = append(h.tokens, token)
						h.mu.Unlock()
					}
				}
			}
			return nil, nil
		}),
		Capabilities: backends.CapabilityFor(kind),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A fresh callback timestamp keeps the freshness window satisfied, so
	// every verdict in these tests comes from the PONG bookkeeping alone.
	c.NotifyCallback()
	h.c = c
	return h
}

// ping runs one keepalive probe with PONG correlation requested.
func (h *pongLivenessHarness) ping(t *testing.T) {
	t.Helper()
	h.c.CheckConnectionAvailability(context.Background(), true)
}

// token returns the token of the n-th ping (1-based) that reached the wire.
func (h *pongLivenessHarness) token(t *testing.T, n int) string {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if n < 1 || n > len(h.tokens) {
		t.Fatalf("ping #%d requested, only %d reached the wire", n, len(h.tokens))
	}
	return h.tokens[n-1]
}

// TestIsCallbackAliveFalseAfterThreeMissedPongs pins the detection bound: a
// CCU that silently dropped the callback registration still answers ping,
// but its PONG never arrives. Three consecutive unanswered pings must turn
// the interface dead, long before the callback freshness window expires.
func TestIsCallbackAliveFalseAfterThreeMissedPongs(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindCCU, nil)

	// Pings 1–3: by the third admission two earlier PONGs are missing.
	for range 3 {
		h.ping(t)
	}
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false after two missed PONGs; want true (two late or lost PONGs are tolerated)")
	}

	// Ping 4: the third consecutive PONG is missing.
	h.ping(t)
	if h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = true after three consecutive missed PONGs; " +
			"want false — a CCU that stopped delivering callbacks must be noticed by the keepalive, not after the freshness window")
	}
}

// TestMatchedPongResetsMissedPongs: the PONG of the newest ping proves the
// callback path works again, so the interface is alive and the next ping
// starts counting from zero.
func TestMatchedPongResetsMissedPongs(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindCCU, nil)
	for range 4 {
		h.ping(t)
	}
	if h.c.IsCallbackAlive() {
		t.Fatal("pre-condition: IsCallbackAlive() = true after three missed PONGs")
	}

	if matched, _ := h.c.RecordPong(h.token(t, 4)); !matched {
		t.Fatal("RecordPong(newest token) did not match")
	}
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false after the newest PONG matched; want true")
	}

	// The answered ping must not count as missed at the next admission.
	h.ping(t)
	h.ping(t)
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false one missed PONG after a reset; want true")
	}
}

// TestLateMatchOfOlderPongResetsMissedPongs: a PONG that arrives after
// later pings went out still proves the CCU delivers callbacks.
func TestLateMatchOfOlderPongResetsMissedPongs(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindCCU, nil)
	for range 4 {
		h.ping(t)
	}
	if h.c.IsCallbackAlive() {
		t.Fatal("pre-condition: IsCallbackAlive() = true after three missed PONGs")
	}

	if matched, _ := h.c.RecordPong(h.token(t, 2)); !matched {
		t.Fatal("RecordPong(older token) did not match")
	}
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false after a late PONG of an older ping matched; want true")
	}
}

// TestUnmatchedPongDoesNotResetMissedPongs: a PONG that matches no pending
// ping (another daemon's, or a stale one) is no proof of our registration.
func TestUnmatchedPongDoesNotResetMissedPongs(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindCCU, nil)
	for range 4 {
		h.ping(t)
	}
	if matched, _ := h.c.RecordPong("not-ours"); matched {
		t.Fatal("RecordPong(unknown) matched")
	}
	if h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = true after an unmatched PONG; want false")
	}
}

// TestShedPingDoesNotCountAsMissedPong: a probe the circuit breaker sheds
// never reaches the CCU, so no PONG can be owed for it.
func TestShedPingDoesNotCountAsMissedPong(t *testing.T) {
	t.Parallel()
	circuit := reliability.NewCircuit(reliability.CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour, // stay OPEN for the test
	})
	h := newPongLivenessHarness(t, backends.KindCCU, circuit)

	// One ping on the wire whose PONG never arrives.
	h.ping(t)
	circuit.RecordFailure() // trip OPEN
	if circuit.State() != hmenum.CircuitStateOpen {
		t.Fatalf("circuit state = %v, want OPEN", circuit.State())
	}
	for range 5 {
		h.ping(t)
	}
	h.mu.Lock()
	onWire := len(h.tokens)
	h.mu.Unlock()
	if onWire != 1 {
		t.Fatalf("%d pings reached the wire, want 1 (the breaker must shed the rest)", onWire)
	}
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false after pings the breaker shed; want true — a shed ping owes no PONG")
	}
}

// TestGatedPingDoesNotCountAsMissedPong: while the connection-issue gate is
// raised the tracker does not record pings, so their PONGs cannot match and
// must not be owed either.
func TestGatedPingDoesNotCountAsMissedPong(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindCCU, nil)
	h.c.SetConnectionIssueGate(func() bool { return true })
	for range 5 {
		h.ping(t)
	}
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false after pings the recovery gate left untracked; want true")
	}
}

// TestMissedPongsIgnoredWithoutPingPong: a backend without ping/pong
// (Homegear) is never judged by PONGs.
func TestMissedPongsIgnoredWithoutPingPong(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindHomegear, nil)
	for range 5 {
		h.ping(t)
	}
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false for a backend without ping/pong; want true")
	}
}

// TestFreshClientIsCallbackAlive: no ping has gone out yet, so nothing is
// owed.
func TestFreshClientIsCallbackAlive(t *testing.T) {
	t.Parallel()
	c, err := New(Config{
		CentralName: "ccu-pong",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: CallerFunc(func(context.Context, string, []any) (any, error) {
			return nil, nil
		}),
		Capabilities: backends.CapabilityFor(backends.KindCCU),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false on a fresh client; want true")
	}
}

// TestSystemStatusRestoredResetsMissedPongs: a re-established session owes
// none of the dead session's PONGs.
func TestSystemStatusRestoredResetsMissedPongs(t *testing.T) {
	t.Parallel()
	h := newPongLivenessHarness(t, backends.KindCCU, nil)
	for range 4 {
		h.ping(t)
	}
	if h.c.IsCallbackAlive() {
		t.Fatal("pre-condition: IsCallbackAlive() = true after three missed PONGs")
	}
	h.c.OnSystemStatusRestored()
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false after the connection was restored; want true")
	}
	// The dead session's last ping must not be owed at the next admission.
	h.ping(t)
	h.ping(t)
	h.ping(t)
	if !h.c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive() = false two missed PONGs after a restore; want true")
	}
}
