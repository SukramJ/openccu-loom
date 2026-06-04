// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// tracker_record_state_test.go — Tracker record/state-machine and ConnectionRegistry threshold tests.
//
// Covers: score degradation curve, tracker initial state, Record/RecordFailure/
// RecordSuccess semantics, ShouldBeDegraded/ShouldBeRunning thresholds,
// stale-decay to Unknown, multi-interface registry score, Connection recovery
// flag, WindowedScore, and MetricsHealthSummary.
package health

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

var parityT0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func newParityTracker(opts ...Option) (*Tracker, *clock.Fake) {
	clk := clock.NewFake(parityT0)
	opts = append([]Option{WithClock(clk), WithStaleAfter(0)}, opts...)
	return NewTracker(opts...), clk
}

// driveUnhealthy transitions name from HEALTHY → DEGRADED → UNHEALTHY by
// recording one healthy then two unhealthy samples.
func driveUnhealthy(t *Tracker, name string) {
	t.Record(name, Sample{Healthy: true})
	t.Record(name, Sample{Healthy: false})
	t.Record(name, Sample{Healthy: false})
}

// ── 1. Initial state (before any event) ─────────────────────────────────────

// TestParityInitialStateUnknown verifies that a brand-new Tracker returns
// StatusUnknown and Score == 0 before any component has been recorded.
func TestParityInitialStateUnknown(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()

	if got := tr.Overall(); got != StatusUnknown {
		t.Errorf("Overall() = %s, want unknown", got)
	}
	if got := tr.Score(); got != 0 {
		t.Errorf("Score() = %f, want 0", got)
	}
	if got := tr.ScoreInt(); got != 0 {
		t.Errorf("ScoreInt() = %d, want 0", got)
	}
}

// ── 2. Score degradation curve (gradual lower scores) ────────────────────────

// TestParityScoreDegradationCurve records components one by one in increasingly
// bad states and verifies the monotone decrease in Score.
func TestParityScoreDegradationCurve(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()

	// all healthy → 1.0
	tr.Record("a", Sample{Healthy: true})
	tr.Record("b", Sample{Healthy: true})
	tr.Record("c", Sample{Healthy: true})
	if s := tr.Score(); s != 1.0 {
		t.Fatalf("3 healthy: Score=%f want 1.0", s)
	}

	// a goes degraded (healthy→fail) → score = (1+0.5+1)/3 ≈ 0.833
	tr.Record("a", Sample{Healthy: false})
	s1 := tr.Score()
	if s1 >= 1.0 || s1 < 0.8 {
		t.Fatalf("1 degraded: Score=%f out of expected range", s1)
	}

	// a escalates to unhealthy → score = (0+0.5+1)/3 ... wait, a was degraded;
	// second consecutive failure → unhealthy. score = (0+1+1)/3 ≈ 0.667
	tr.Record("a", Sample{Healthy: false})
	s2 := tr.Score()
	if s2 >= s1 {
		t.Fatalf("escalation did not lower score: s1=%f s2=%f", s1, s2)
	}

	// b goes degraded → score = (0+0.5+1)/3 ≈ 0.5
	tr.Record("b", Sample{Healthy: false})
	s3 := tr.Score()
	if s3 >= s2 {
		t.Fatalf("b degraded did not lower score: s2=%f s3=%f", s2, s3)
	}

	// b escalates + c goes degraded → all trending down
	tr.Record("b", Sample{Healthy: false})
	tr.Record("c", Sample{Healthy: false})
	s4 := tr.Score()
	if s4 >= s3 {
		t.Fatalf("further degradation did not lower score: s3=%f s4=%f", s3, s4)
	}
}

// ── 3. Record/RecordFailure/RecordSuccess behaviour ──────────────────────────

// TestParityRecordHealthyTransitionsToHealthy verifies that a single healthy
// sample records the component as HEALTHY.
func TestParityRecordHealthyTransitionsToHealthy(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()
	tr.Record("x", Sample{Healthy: true, Note: "ok"})
	c, ok := tr.Get("x")
	if !ok {
		t.Fatal("Get returned false after Record")
	}
	if c.Status != StatusHealthy {
		t.Fatalf("Status=%s want healthy", c.Status)
	}
	if c.LastSample.Note != "ok" {
		t.Errorf("Note not preserved: got %q", c.LastSample.Note)
	}
}

// TestParityRecordSingleFailureAfterHealthyDegrades verifies the flap-damp rule:
// one unhealthy sample after a healthy run yields DEGRADED (not UNHEALTHY).
func TestParityRecordSingleFailureAfterHealthyDegrades(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()
	tr.Record("x", Sample{Healthy: true})
	tr.Record("x", Sample{Healthy: false})
	c, _ := tr.Get("x")
	if c.Status != StatusDegraded {
		t.Fatalf("Status=%s want degraded after first failure", c.Status)
	}
}

// TestParityRecordTwoConsecutiveFailuresEscalates verifies that a second
// consecutive unhealthy sample escalates from DEGRADED to UNHEALTHY.
func TestParityRecordTwoConsecutiveFailuresEscalates(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()
	tr.Record("x", Sample{Healthy: true})
	tr.Record("x", Sample{Healthy: false})
	tr.Record("x", Sample{Healthy: false})
	c, _ := tr.Get("x")
	if c.Status != StatusUnhealthy {
		t.Fatalf("Status=%s want unhealthy after second consecutive failure", c.Status)
	}
}

// TestParityRecordSuccessAfterFailureRecovers verifies that a healthy sample
// following unhealthy transitions back to HEALTHY.
func TestParityRecordSuccessAfterFailureRecovers(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()
	driveUnhealthy(tr, "x")
	c, _ := tr.Get("x")
	if c.Status != StatusUnhealthy {
		t.Fatalf("pre-condition failed: status=%s", c.Status)
	}
	tr.Record("x", Sample{Healthy: true})
	c, _ = tr.Get("x")
	if c.Status != StatusHealthy {
		t.Fatalf("Status=%s want healthy after recovery", c.Status)
	}
}

// ── 4. ShouldBeDegraded / ShouldBeRunning thresholds ─────────────────────────

// TestParityShouldBeDegradedWhenSomeBad verifies that ShouldBeDegraded is true
// when at least one connection is healthy but not all.
func TestParityShouldBeDegradedWhenSomeBad(t *testing.T) {
	t.Parallel()
	reg := NewConnectionRegistry()

	healthy, _ := newTestConn("p-a")
	healthy.SetClientState(hmenum.ClientStateConnected)
	reg.Register(healthy)

	sick, _ := newTestConn("p-b")
	// sick stays in CREATED — not connected
	reg.Register(sick)

	if !reg.ShouldBeDegraded() {
		t.Error("ShouldBeDegraded() = false, want true when one healthy + one not")
	}
	if reg.ShouldBeRunning() {
		t.Error("ShouldBeRunning() = true, want false when not all healthy")
	}
}

// TestParityShouldBeRunningWhenAllHealthy verifies that ShouldBeRunning is true
// only when every connection is connected and not degraded.
func TestParityShouldBeRunningWhenAllHealthy(t *testing.T) {
	t.Parallel()
	reg := NewConnectionRegistry()

	for _, id := range []string{"r-a", "r-b"} {
		c, _ := newTestConn(id)
		c.SetClientState(hmenum.ClientStateConnected)
		reg.Register(c)
	}

	if !reg.ShouldBeRunning() {
		t.Error("ShouldBeRunning() = false, want true when all connected")
	}
	if reg.ShouldBeDegraded() {
		t.Error("ShouldBeDegraded() = true, want false when all healthy")
	}
}

// TestParityShouldBeDegradedEmptyRegistryFalse verifies that an empty registry
// reports ShouldBeDegraded == false and ShouldBeRunning == false.
func TestParityShouldBeDegradedEmptyRegistryFalse(t *testing.T) {
	t.Parallel()
	reg := NewConnectionRegistry()
	if reg.ShouldBeDegraded() {
		t.Error("ShouldBeDegraded() = true on empty registry, want false")
	}
	if reg.ShouldBeRunning() {
		t.Error("ShouldBeRunning() = true on empty registry, want false")
	}
}

// ── 5. Stale-decay to Unknown ─────────────────────────────────────────────────

// TestParityStaleDecayToUnknown verifies that a component whose last sample is
// older than StaleAfter decays to StatusUnknown on Get and Snapshot.
func TestParityStaleDecayToUnknown(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(parityT0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(30*time.Second))

	tr.Record("s", Sample{Healthy: true, Timestamp: clk.Now()})
	c, _ := tr.Get("s")
	if c.Status != StatusHealthy {
		t.Fatalf("before stale: status=%s want healthy", c.Status)
	}

	// Advance past stale threshold.
	clk.Advance(31 * time.Second)
	c, _ = tr.Get("s")
	if c.Status != StatusUnknown {
		t.Fatalf("after stale: status=%s want unknown", c.Status)
	}

	// Overall should also degrade.
	if tr.Overall() != StatusUnknown {
		t.Errorf("Overall() = %s, want unknown when stale", tr.Overall())
	}
}

// ── 6. Multi-interface score aggregation (Multi-CCU) ─────────────────────────

// TestParityMultiInterfaceScoreAggregation registers three interfaces in
// different health states and asserts that Score and ScoreInt correctly average
// their contributions.
func TestParityMultiInterfaceScoreAggregation(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()

	// xmlrpc → healthy (1.0)
	tr.Record("xmlrpc", Sample{Healthy: true})
	// json → degraded (0.5)
	tr.Record("json", Sample{Healthy: true})
	tr.Record("json", Sample{Healthy: false})
	// bin → unhealthy (0.0)
	tr.Record("bin", Sample{Healthy: true})
	tr.Record("bin", Sample{Healthy: false})
	tr.Record("bin", Sample{Healthy: false})

	want := (1.0 + 0.5 + 0.0) / 3
	got := tr.Score()
	if diff := got - want; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("Score()=%f want %f (1.0+0.5+0.0)/3", got, want)
	}
	wantInt := int(want * 100)
	if gotInt := tr.ScoreInt(); gotInt != wantInt {
		t.Errorf("ScoreInt()=%d want %d", gotInt, wantInt)
	}
}

// ── 7. Connection.SetInRecovery probe-timeout behaviour ──────────────────────

// TestParityConnectionRecoveryFlagDegrades verifies that setting InRecovery on
// a CONNECTED connection causes IsDegraded to return true, modelling the probe-
// Timeout path described in py.
func TestParityConnectionRecoveryFlagDegrades(t *testing.T) {
	t.Parallel()
	c, _ := newTestConn("recovery-iface")
	c.SetClientState(hmenum.ClientStateConnected)

	if c.IsDegraded() {
		t.Fatal("pre-condition: should not be degraded before SetInRecovery")
	}

	c.SetInRecovery(true)
	if !c.IsDegraded() {
		t.Error("IsDegraded() = false after SetInRecovery(true), want true")
	}
	if c.IsFailed() {
		t.Error("IsFailed() = true during recovery, want false (in-recovery ≠ failed)")
	}
}

// ── 8. WindowedScore ─────────────────────────────────────────────────────────

// TestParityWindowedScoreOnlyCountsRecentSamples verifies that WindowedScore
// ignores samples outside the window and counts only fresh ones.
func TestParityWindowedScoreOnlyCountsRecentSamples(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(parityT0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(0), WithHistorySize(100))

	// Three old failures (5 min ago) — will be outside the 2 min window.
	old := parityT0.Add(-5 * time.Minute)
	for range 3 {
		tr.Record("w", Sample{Healthy: false, Timestamp: old})
	}
	// Two recent successes.
	tr.Record("w", Sample{Healthy: true, Timestamp: clk.Now()})
	tr.Record("w", Sample{Healthy: true, Timestamp: clk.Now()})

	score := tr.WindowedScore("w", 2*time.Minute)
	// Only the 2 recent successes are inside the window → 2/2 = 1.0
	if score != 1.0 {
		t.Errorf("WindowedScore() = %f, want 1.0 (only recent samples counted)", score)
	}
}

// ── 9. MetricsHealthSummary ───────────────────────────────────────────────────

// TestParityMetricsHealthSummary verifies that MetricsHealthSummary correctly
// counts healthy/degraded/failed components and computes OverallScore.
func TestParityMetricsHealthSummary(t *testing.T) {
	t.Parallel()
	tr, _ := newParityTracker()

	// h1 → healthy
	tr.Record("h1", Sample{Healthy: true})
	// d1 → degraded
	tr.Record("d1", Sample{Healthy: true})
	tr.Record("d1", Sample{Healthy: false})
	// f1 → unhealthy
	driveUnhealthy(tr, "f1")

	v := tr.MetricsHealthSummary()
	if v.ClientsHealthy != 1 {
		t.Errorf("ClientsHealthy=%d want 1", v.ClientsHealthy)
	}
	if v.ClientsDegraded != 1 {
		t.Errorf("ClientsDegraded=%d want 1", v.ClientsDegraded)
	}
	if v.ClientsFailed != 1 {
		t.Errorf("ClientsFailed=%d want 1", v.ClientsFailed)
	}
	// Expected aggregate score: average of connected/degraded/failed
	// per-client scores [1.0, 0.5, 0.0] — i.e. 0.5.
	wantScore := (1.0 + 0.5 + 0.0) / 3
	if diff := v.OverallScore - wantScore; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("OverallScore=%f want %f", v.OverallScore, wantScore)
	}
}

// ── 10. UpdateClientHealth reconnect tracking ─────────────────────────────────

// TestParityUpdateClientHealthTracksReconnects verifies that transitioning a
// connection from CONNECTED → RECONNECTING bumps ReconnectAttempts, and that
// going RECONNECTING → CONNECTED resets the counter to 0.
func TestParityUpdateClientHealthTracksReconnects(t *testing.T) {
	t.Parallel()
	reg := NewConnectionRegistry()
	c, _ := newTestConn("uch-iface")
	c.SetClientState(hmenum.ClientStateConnected)
	reg.Register(c)

	// Connected → Reconnecting: counter bumps.
	reg.UpdateClientHealth("uch-iface", hmenum.ClientStateConnected, hmenum.ClientStateReconnecting)
	snap := c.Snapshot()
	if snap.ReconnectAttempts != 1 {
		t.Errorf("ReconnectAttempts=%d want 1 after first reconnect", snap.ReconnectAttempts)
	}

	// Reconnecting → Reconnecting: does NOT bump (old == new == Reconnecting per
	reg.UpdateClientHealth("uch-iface", hmenum.ClientStateReconnecting, hmenum.ClientStateReconnecting)
	snap = c.Snapshot()
	if snap.ReconnectAttempts != 1 {
		t.Errorf("ReconnectAttempts=%d want 1 (same→same does not bump)", snap.ReconnectAttempts)
	}

	// Reconnecting → Connected: counter reset.
	reg.UpdateClientHealth("uch-iface", hmenum.ClientStateReconnecting, hmenum.ClientStateConnected)
	snap = c.Snapshot()
	if snap.ReconnectAttempts != 0 {
		t.Errorf("ReconnectAttempts=%d want 0 after successful connect", snap.ReconnectAttempts)
	}
}

// ── 11. PrimaryClientHealthy ─────────────────────────────────────────────────

// TestParityPrimaryClientHealthyPrefersHmIPRF verifies that when both a
// HmIP-RF and a BidCos-RF connection are registered, PrimaryClientHealthy
// reflects the HmIP-RF state, not the fallback.
func TestParityPrimaryClientHealthyPrefersHmIPRF(t *testing.T) {
	t.Parallel()
	reg := NewConnectionRegistry()

	// HmIP-RF: unhealthy (stays in CREATED)
	hmip := NewConnection("HmIP-RF.1", hmenum.InterfaceHmIPRF)
	reg.Register(hmip)

	// BidCos-RF: fully healthy
	bidcos := NewConnection("BidCos-RF.1", hmenum.InterfaceBidCosRF)
	bidcos.SetClientState(hmenum.ClientStateConnected)
	reg.Register(bidcos)

	// PrimaryClientHealthy must follow HmIP-RF (not the healthy BidCos-RF).
	if reg.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = true when HmIP-RF is not connected, want false")
	}

	hmip.SetClientState(hmenum.ClientStateConnected)
	if !reg.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = false after HmIP-RF connected, want true")
	}
}
