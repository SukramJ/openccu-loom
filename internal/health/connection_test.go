// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// connT0 is the stable time anchor used by fake-clock tests in this file.
var connT0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// newTestConn is a helper that creates a Connection with a fake clock anchored
// at connT0.
func newTestConn(id string) (*Connection, *clock.Fake) {
	clk := clock.NewFake(connT0)
	c := NewConnection(id, hmenum.InterfaceHmIPRF, WithConnectionClock(clk))
	return c, clk
}

// ── Connection tests ──────────────────────────────────────────────────────────

// TestNewConnectionDefaults verifies that a freshly constructed Connection
// has CREATED state, CLOSED XML-RPC circuit, and JSONRPCCircuitKnown == false.
func TestNewConnectionDefaults(t *testing.T) {
	t.Parallel()
	c, _ := newTestConn("iface-0")
	snap := c.Snapshot()
	if snap.ClientState != hmenum.ClientStateCreated {
		t.Errorf("ClientState = %s, want %s", snap.ClientState, hmenum.ClientStateCreated)
	}
	if snap.XMLRPCCircuit != hmenum.CircuitStateClosed {
		t.Errorf("XMLRPCCircuit = %s, want %s", snap.XMLRPCCircuit, hmenum.CircuitStateClosed)
	}
	if snap.JSONRPCCircuitKnown {
		t.Error("JSONRPCCircuitKnown = true, want false")
	}
	if snap.InterfaceID != "iface-0" {
		t.Errorf("InterfaceID = %q, want %q", snap.InterfaceID, "iface-0")
	}
}

// TestRecordSuccessClearsConsecutiveFailures records 3 failures then 1 success
// and asserts that ConsecutiveFailures drops to 0 and LastSuccess is stamped.
func TestRecordSuccessClearsConsecutiveFailures(t *testing.T) {
	t.Parallel()
	c, clk := newTestConn("iface-1")
	clk.Advance(5 * time.Second) // move past zero so timestamps are non-zero
	c.RecordFailedRequest()
	c.RecordFailedRequest()
	c.RecordFailedRequest()
	if snap := c.Snapshot(); snap.ConsecutiveFailures != 3 {
		t.Fatalf("after 3 failures: ConsecutiveFailures = %d, want 3", snap.ConsecutiveFailures)
	}
	clk.Advance(time.Second)
	c.RecordSuccessfulRequest()
	snap := c.Snapshot()
	if snap.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", snap.ConsecutiveFailures)
	}
	if snap.LastSuccess.IsZero() {
		t.Error("LastSuccess is zero after RecordSuccessfulRequest")
	}
}

// TestRecordFailureBumpsConsecutive records 3 failures and asserts the
// counter reaches 3 and LastFailure is stamped.
func TestRecordFailureBumpsConsecutive(t *testing.T) {
	t.Parallel()
	c, clk := newTestConn("iface-2")
	clk.Advance(time.Second) // ensure non-zero timestamps
	c.RecordFailedRequest()
	c.RecordFailedRequest()
	c.RecordFailedRequest()
	snap := c.Snapshot()
	if snap.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", snap.ConsecutiveFailures)
	}
	if snap.LastFailure.IsZero() {
		t.Error("LastFailure is zero after RecordFailedRequest")
	}
}

// TestIsConnectedTrueOnlyWhenConnected verifies that IsConnected returns true
// only for ClientStateConnected and false for every other state.
func TestIsConnectedTrueOnlyWhenConnected(t *testing.T) {
	t.Parallel()
	nonConnectedStates := []hmenum.ClientState{
		hmenum.ClientStateCreated,
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateDisconnected,
		hmenum.ClientStateReconnecting,
		hmenum.ClientStateStopping,
		hmenum.ClientStateStopped,
		hmenum.ClientStateFailed,
	}
	c, _ := newTestConn("iface-3")
	for _, s := range nonConnectedStates {
		c.SetClientState(s)
		if c.IsConnected() {
			t.Errorf("IsConnected() = true for state %s, want false", s)
		}
	}
	c.SetClientState(hmenum.ClientStateConnected)
	if !c.IsConnected() {
		t.Error("IsConnected() = false for ClientStateConnected, want true")
	}
}

// TestIsDegradedRequiresConnected verifies that half-open XML-RPC does not
// cause IsDegraded when the client is not yet CONNECTED.
func TestIsDegradedRequiresConnected(t *testing.T) {
	t.Parallel()
	c, _ := newTestConn("iface-4")
	// state is CREATED, XML-RPC is CLOSED by default — not degraded
	c.SetXMLRPCCircuit(hmenum.CircuitStateHalfOpen)
	if c.IsDegraded() {
		t.Error("IsDegraded() = true for CREATED state + half-open XML-RPC, want false")
	}
	// now connect — half-open circuit triggers degraded
	c.SetClientState(hmenum.ClientStateConnected)
	if !c.IsDegraded() {
		t.Error("IsDegraded() = false for CONNECTED + half-open XML-RPC, want true")
	}
}

// TestIsDegradedHonorsRecovery checks that SetInRecovery(true) puts a
// CONNECTED connection into degraded state even when both circuits are closed.
func TestIsDegradedHonorsRecovery(t *testing.T) {
	t.Parallel()
	c, _ := newTestConn("iface-5")
	c.SetClientState(hmenum.ClientStateConnected)
	// circuits are already closed (default); in_recovery is false
	if c.IsDegraded() {
		t.Error("IsDegraded() = true before SetInRecovery, want false")
	}
	c.SetInRecovery(true)
	if !c.IsDegraded() {
		t.Error("IsDegraded() = false after SetInRecovery(true), want true")
	}
	c.SetInRecovery(false)
	if c.IsDegraded() {
		t.Error("IsDegraded() = true after SetInRecovery(false), want false")
	}
}

// TestIsFailedDetectsOpenCircuits covers:
// - CONNECTED + XML-RPC OPEN → IsFailed true
// - CONNECTED + JSON-RPC known+OPEN → IsFailed true
// - CONNECTED + both circuits closed → IsFailed false
func TestIsFailedDetectsOpenCircuits(t *testing.T) {
	t.Parallel()
	c, _ := newTestConn("iface-6")
	c.SetClientState(hmenum.ClientStateConnected)

	// XML-RPC open
	c.SetXMLRPCCircuit(hmenum.CircuitStateOpen)
	if !c.IsFailed() {
		t.Error("IsFailed() = false for CONNECTED + XML-RPC OPEN, want true")
	}
	c.SetXMLRPCCircuit(hmenum.CircuitStateClosed) // reset

	// JSON-RPC known + open
	c.SetJSONRPCCircuit(hmenum.CircuitStateOpen)
	if !c.IsFailed() {
		t.Error("IsFailed() = false for CONNECTED + JSON-RPC known+OPEN, want true")
	}
	c.SetJSONRPCCircuit(hmenum.CircuitStateClosed) // reset to closed, still known

	// Both closed → not failed
	if c.IsFailed() {
		t.Error("IsFailed() = true for CONNECTED + both circuits closed, want false")
	}
}

// TestCanReceiveEventsRequiresFreshness uses a fake clock to verify that
// CanReceiveEvents returns true within the staleness threshold and false once
// the threshold has elapsed.
func TestCanReceiveEventsRequiresFreshness(t *testing.T) {
	t.Parallel()
	c, clk := newTestConn("iface-7")
	c.SetClientState(hmenum.ClientStateConnected)

	// No event recorded yet → false
	if c.CanReceiveEvents() {
		t.Error("CanReceiveEvents() = true before any event, want false")
	}

	c.RecordEventReceived()

	// Advance 30 s (within threshold) → true
	clk.Advance(30 * time.Second)
	if !c.CanReceiveEvents() {
		t.Error("CanReceiveEvents() = false after 30 s, want true")
	}

	// Advance another 60 s (30 + 60 = 90 s total, past threshold) → false
	clk.Advance(60 * time.Second)
	if c.CanReceiveEvents() {
		t.Error("CanReceiveEvents() = true after 90 s, want false")
	}
}

// TestHealthScoreFullyHealthy verifies that connected + closed circuits +
// just-recorded success yields a score of exactly 1.0.
func TestHealthScoreFullyHealthy(t *testing.T) {
	t.Parallel()
	c, clk := newTestConn("iface-8")
	c.SetClientState(hmenum.ClientStateConnected)
	// XML-RPC is already closed by default.
	// Record a successful request so the activity bucket is filled.
	c.RecordSuccessfulRequest()
	// No time has passed yet, so the success is still "fresh".
	_ = clk // retained for clarity; fake clock is at connT0
	score := c.HealthScore()
	const want = 1.0
	if diff := score - want; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("HealthScore() = %f, want %f", score, want)
	}
}

// TestHealthScoreFullyUnhealthy verifies that disconnected + open circuits +
// no successful request yields a score of 0.
func TestHealthScoreFullyUnhealthy(t *testing.T) {
	t.Parallel()
	c, _ := newTestConn("iface-9")
	// State is CREATED (not connected).
	c.SetXMLRPCCircuit(hmenum.CircuitStateOpen)
	c.SetJSONRPCCircuit(hmenum.CircuitStateOpen) // marks JSON-RPC as known too
	// No successful request recorded.
	score := c.HealthScore()
	const want = 0.0
	if diff := score - want; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("HealthScore() = %f, want %f", score, want)
	}
}

// ── ConnectionRegistry tests ──────────────────────────────────────────────────

// TestRegistryEmptyAllHealthyFalse verifies the empty-registry semantics:
// AllHealthy, AnyHealthy both false and OverallHealthScore == 0.
func TestRegistryEmptyAllHealthyFalse(t *testing.T) {
	t.Parallel()
	r := NewConnectionRegistry()
	if r.AllHealthy() {
		t.Error("AllHealthy() = true on empty registry, want false")
	}
	if r.AnyHealthy() {
		t.Error("AnyHealthy() = true on empty registry, want false")
	}
	if s := r.OverallHealthScore(); s != 0 {
		t.Errorf("OverallHealthScore() = %f on empty registry, want 0", s)
	}
}

// TestRegistryRegisterGetRemove covers the full lifecycle: Register, Get,
// Remove (idempotency of Remove is also checked).
func TestRegistryRegisterGetRemove(t *testing.T) {
	t.Parallel()
	r := NewConnectionRegistry()
	c, _ := newTestConn("reg-a")
	r.Register(c)

	got, ok := r.Get("reg-a")
	if !ok {
		t.Fatal("Get after Register returned false, want true")
	}
	if got != c {
		t.Error("Get returned a different pointer than what was Registered")
	}

	// First Remove returns true.
	if !r.Remove("reg-a") {
		t.Error("Remove returned false on first call, want true")
	}
	// Second Remove returns false.
	if r.Remove("reg-a") {
		t.Error("Remove returned true on second call, want false")
	}
	// Get now returns false.
	if _, ok := r.Get("reg-a"); ok {
		t.Error("Get returned true after Remove, want false")
	}
}

// TestRegistryAllSorted registers connections with ids "z", "a", "m" and
// verifies that All() returns them in ascending alphabetical order.
func TestRegistryAllSorted(t *testing.T) {
	t.Parallel()
	r := NewConnectionRegistry()
	for _, id := range []string{"z", "a", "m"} {
		c, _ := newTestConn(id)
		r.Register(c)
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d items, want 3", len(all))
	}
	want := []string{"a", "m", "z"}
	for i, c := range all {
		snap := c.Snapshot()
		if snap.InterfaceID != want[i] {
			t.Errorf("All()[%d].InterfaceID = %q, want %q", i, snap.InterfaceID, want[i])
		}
	}
}

// TestRegistryAllHealthyRequiresEveryConnected verifies that AllHealthy is
// false when even one connection is not in the CONNECTED state.
func TestRegistryAllHealthyRequiresEveryConnected(t *testing.T) {
	t.Parallel()
	r := NewConnectionRegistry()

	healthy, _ := newTestConn("rh-1")
	healthy.SetClientState(hmenum.ClientStateConnected)
	r.Register(healthy)

	notConnected, _ := newTestConn("rh-2")
	// notConnected stays in CREATED state
	r.Register(notConnected)

	if r.AllHealthy() {
		t.Error("AllHealthy() = true when one connection is CREATED, want false")
	}
}

// TestRegistryAnyHealthyAndOverallScore registers one healthy and one
// disconnected connection and checks that AnyHealthy is true and
// OverallHealthScore equals the average of the two per-connection scores.
func TestRegistryAnyHealthyAndOverallScore(t *testing.T) {
	t.Parallel()
	r := NewConnectionRegistry()

	healthy, clkH := newTestConn("rs-1")
	healthy.SetClientState(hmenum.ClientStateConnected)
	healthy.RecordSuccessfulRequest() // fill activity bucket
	_ = clkH                          // clock stays at connT0 so success is fresh
	r.Register(healthy)

	disconnected, _ := newTestConn("rs-2")
	// CREATED state, open circuit, no success → score 0
	disconnected.SetXMLRPCCircuit(hmenum.CircuitStateOpen)
	r.Register(disconnected)

	if !r.AnyHealthy() {
		t.Error("AnyHealthy() = false when one connection is healthy, want true")
	}

	scoreH := healthy.HealthScore()      // should be 1.0
	scoreD := disconnected.HealthScore() // should be 0.0
	wantOverall := (scoreH + scoreD) / 2
	overall := r.OverallHealthScore()
	if diff := overall - wantOverall; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("OverallHealthScore() = %f, want %f (scoreH=%f scoreD=%f)",
			overall, wantOverall, scoreH, scoreD)
	}
}

// ── Connection.IsAvailable tests ──────────────────────────────────────────────

func TestConnectionIsAvailableNotFailed(t *testing.T) {
	c := NewConnection("HmIP-RF.MyID", hmenum.InterfaceHmIPRF)
	c.SetClientState(hmenum.ClientStateConnected)
	c.SetXMLRPCCircuit(hmenum.CircuitStateClosed)

	if c.IsFailed() {
		t.Fatal("precondition: should not be failed when connected + closed circuit")
	}
	if !c.IsAvailable() {
		t.Fatal("IsAvailable() must be true when not failed")
	}
}

func TestConnectionIsAvailableFalseWhenFailed(t *testing.T) {
	c := NewConnection("HmIP-RF.MyID", hmenum.InterfaceHmIPRF)
	c.SetClientState(hmenum.ClientStateDisconnected)

	if !c.IsFailed() {
		t.Fatal("precondition: should be failed when disconnected")
	}
	if c.IsAvailable() {
		t.Fatal("IsAvailable() must be false when failed")
	}
}

func TestConnectionIsAvailableOpenCircuit(t *testing.T) {
	c := NewConnection("HmIP-RF.MyID", hmenum.InterfaceHmIPRF)
	c.SetClientState(hmenum.ClientStateConnected)
	c.SetXMLRPCCircuit(hmenum.CircuitStateOpen)

	if !c.IsFailed() {
		t.Fatal("precondition: open circuit should count as failed")
	}
	if c.IsAvailable() {
		t.Fatal("IsAvailable() must be false with open circuit")
	}
}
