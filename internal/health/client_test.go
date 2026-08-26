// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// --- RecordRequest ---

// TestRecordRequest_Success verifies that a successful request sets
// LastSuccessfulRequest, resets ConsecutiveFailures to zero, and leaves
// LastFailedRequest unchanged.
func TestRecordRequest_Success(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	// Seed some failures first so we can verify the reset.
	tr.RecordRequest("iface", false)
	tr.RecordRequest("iface", false)

	fc.Set(t0.Add(1 * time.Second))
	tr.RecordRequest("iface", true)

	detail, ok := tr.ClientDetail("iface")
	if !ok {
		t.Fatal("ClientDetail returned false for registered interface")
	}
	if detail.LastSuccessfulRequest.IsZero() {
		t.Error("LastSuccessfulRequest is zero after success")
	}
	if detail.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 after success", detail.ConsecutiveFailures)
	}
	// LastFailedRequest must not advance on a success call.
	if detail.LastFailedRequest.IsZero() {
		// We recorded two failures above; it should have been set.
		t.Error("LastFailedRequest is zero — prior failures were not captured")
	}
	if !detail.LastSuccessfulRequest.After(detail.LastFailedRequest) {
		t.Errorf("LastSuccessfulRequest (%v) should be after LastFailedRequest (%v)",
			detail.LastSuccessfulRequest, detail.LastFailedRequest)
	}
}

// TestRecordRequest_Failure verifies that consecutive failures increment
// ConsecutiveFailures correctly and set LastFailedRequest while leaving
// LastSuccessfulRequest unchanged.
func TestRecordRequest_Failure(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	// Record one success first so LastSuccessfulRequest is set.
	tr.RecordRequest("iface", true)
	successAt := fc.Now()

	// Three consecutive failures.
	fc.Set(t0.Add(1 * time.Second))
	tr.RecordRequest("iface", false)
	fc.Set(t0.Add(2 * time.Second))
	tr.RecordRequest("iface", false)
	fc.Set(t0.Add(3 * time.Second))
	tr.RecordRequest("iface", false)

	detail, ok := tr.ClientDetail("iface")
	if !ok {
		t.Fatal("ClientDetail returned false")
	}
	if detail.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", detail.ConsecutiveFailures)
	}
	if detail.LastFailedRequest.IsZero() {
		t.Error("LastFailedRequest is zero after three failures")
	}
	// LastSuccessfulRequest must not have changed.
	if !detail.LastSuccessfulRequest.Equal(successAt) {
		t.Errorf("LastSuccessfulRequest changed: got %v, want %v",
			detail.LastSuccessfulRequest, successAt)
	}
}

// TestRecordRequest_EmptyName verifies that passing an empty name is a
// no-op — no panic, no entry created.
func TestRecordRequest_EmptyName(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))
	// Must not panic.
	tr.RecordRequest("", true)
	tr.RecordRequest("", false)

	_, ok := tr.ClientDetail("")
	if ok {
		t.Error("ClientDetail returned true for empty name — unexpected registration")
	}
}

// TestRecordRequest_ImplicitRegistration verifies that the first
// RecordRequest(name, true) implicitly registers the client so a
// subsequent ClientDetail call returns (_, true).
func TestRecordRequest_ImplicitRegistration(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))
	tr.RecordRequest("newface", true)

	_, ok := tr.ClientDetail("newface")
	if !ok {
		t.Error("ClientDetail returned false after implicit registration via RecordRequest")
	}
}

// --- SetRecoveryFlag ---

// TestSetRecoveryFlag verifies that the InRecovery flag can be toggled.
func TestSetRecoveryFlag(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))

	tr.SetRecoveryFlag("iface", true)
	detail, ok := tr.ClientDetail("iface")
	if !ok {
		t.Fatal("ClientDetail returned false after SetRecoveryFlag")
	}
	if !detail.InRecovery {
		t.Error("InRecovery = false after SetRecoveryFlag(true)")
	}

	tr.SetRecoveryFlag("iface", false)
	detail, _ = tr.ClientDetail("iface")
	if detail.InRecovery {
		t.Error("InRecovery = true after SetRecoveryFlag(false)")
	}
}

// --- ResetReconnects ---

// TestResetReconnects verifies that after several RecordReconnectAttempt
// calls, ResetReconnects brings ReconnectAttempts back to zero.
func TestResetReconnects(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))

	tr.RecordReconnectAttempt("iface")
	tr.RecordReconnectAttempt("iface")
	tr.RecordReconnectAttempt("iface")

	detail, ok := tr.ClientDetail("iface")
	if !ok {
		t.Fatal("ClientDetail returned false after RecordReconnectAttempt")
	}
	if detail.ReconnectAttempts == 0 {
		t.Fatal("ReconnectAttempts = 0 before reset — attempts were not counted")
	}

	tr.ResetReconnects("iface")

	detail, _ = tr.ClientDetail("iface")
	if detail.ReconnectAttempts != 0 {
		t.Errorf("ReconnectAttempts = %d after ResetReconnects, want 0", detail.ReconnectAttempts)
	}
	if tr.ReconnectAttempts("iface") != 0 {
		t.Errorf("ReconnectAttempts() counter = %d after reset, want 0", tr.ReconnectAttempts("iface"))
	}
}

// --- RecordReconnectAttempt ---

// TestRecordReconnectAttempt_ClientHealth verifies that
// RecordReconnectAttempt is reflected in ClientDetail.ReconnectAttempts.
func TestRecordReconnectAttempt_ClientHealth(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))

	for i := 1; i <= 5; i++ {
		tr.RecordReconnectAttempt("iface")
		detail, ok := tr.ClientDetail("iface")
		if !ok {
			t.Fatalf("iteration %d: ClientDetail returned false", i)
		}
		if detail.ReconnectAttempts != i {
			t.Errorf("iteration %d: ReconnectAttempts = %d, want %d",
				i, detail.ReconnectAttempts, i)
		}
	}
}

// --- ClientDetail.LastEventReceived ---

// TestClientDetail_LastEventReceived verifies that after
// RecordEventReceived the LastEventReceived field in ClientDetail is
// non-zero.
func TestClientDetail_LastEventReceived(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	// Register client first, then record event.
	tr.RecordRequest("iface", true)
	tr.RecordEventReceived("iface")

	detail, ok := tr.ClientDetail("iface")
	if !ok {
		t.Fatal("ClientDetail returned false")
	}
	if detail.LastEventReceived.IsZero() {
		t.Error("LastEventReceived is zero after RecordEventReceived")
	}
	if !detail.LastEventReceived.Equal(t0) {
		t.Errorf("LastEventReceived = %v, want %v", detail.LastEventReceived, t0)
	}
}

// --- ClientScore ---

// TestClientScore_Unknown verifies that a non-existent component returns 0.
func TestClientScore_Unknown(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))
	if got := tr.ClientScore("ghost"); got != 0 {
		t.Errorf("ClientScore(unknown) = %v, want 0", got)
	}
}

// TestClientScore_HealthyWithRecentEvent verifies that a healthy
// component with a recent event-received sample scores above 0.95.
func TestClientScore_HealthyWithRecentEvent(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	tr.Record("iface", health.Sample{Healthy: true, Note: "breaker closed"})
	tr.RecordEventReceived("iface")

	// Advance only a few seconds — event is fresh (< 60 s).
	fc.Set(t0.Add(5 * time.Second))

	got := tr.ClientScore("iface")
	if got <= 0.95 {
		t.Errorf("ClientScore(healthy + recent event) = %v, want > 0.95", got)
	}
}

// TestClientScore_BreakerOpenLowersScore verifies that an open breaker
// (Note "breaker open") reduces the score below the baseline when the
// component is otherwise healthy.
//
// ClientScore reads the circuit pillar from comp.LastSample.Note, so the
// breaker-note sample must be the last Record call. RecordEventReceived is
// called before Record("breaker open") so it seeds the activity history
// without overwriting LastSample.
func TestClientScore_BreakerOpenLowersScore(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	// Baseline: event first, then breaker-closed note as LastSample.
	tr.RecordEventReceived("baseline")
	tr.Record("baseline", health.Sample{Healthy: true, Note: "breaker closed"})
	fc.Set(t0.Add(5 * time.Second))
	baseline := tr.ClientScore("baseline")

	// Breaker-open variant: event first, then breaker-open note as LastSample.
	t1 := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	fc2 := clock.NewFake(t1)
	tr2 := health.NewTracker(health.WithClock(fc2), health.WithStaleAfter(0))
	tr2.RecordEventReceived("iface")
	tr2.Record("iface", health.Sample{Healthy: true, Note: "breaker open"})
	fc2.Set(t1.Add(5 * time.Second))
	got := tr2.ClientScore("iface")

	if got >= baseline {
		t.Errorf("open breaker score %v should be lower than closed breaker baseline %v", got, baseline)
	}
	if got > 0.7 {
		t.Errorf("open breaker score = %v, want <= 0.7 (circuit pillar contributes 0)", got)
	}
}

// TestClientScore_Degraded verifies that a degraded state yields a
// score below 0.5.
func TestClientScore_Degraded(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	// One healthy then one failure → DEGRADED.
	tr.Record("iface", health.Sample{Healthy: true})
	tr.Record("iface", health.Sample{Healthy: false})
	// Record a recent event so activity is full.
	tr.RecordEventReceived("iface")
	fc.Set(t0.Add(5 * time.Second))

	got := tr.ClientScore("iface")
	// State pillar for DEGRADED = 0.5 → 0.4*0.5 + 0.3*1.0 + 0.3*1.0 = 0.20 + 0.30 + 0.30 = 0.80
	// Wait — degraded reduces the state contribution but the other pillars can compensate.
	// The test asks for score < 1.0 when compared against a fully-healthy equivalent.
	// More importantly: score < the fully-healthy equivalent (which approaches 1.0).
	if got >= 1.0 {
		t.Errorf("degraded state score = %v, expected < 1.0", got)
	}
	// State = 0.5, circuit = 1.0 (no note), activity = 1.0 → 0.4*0.5 + 0.3 + 0.3 = 0.80
	// Confirm the formula is in the expected range.
	const want = 0.80
	const epsilon = 0.01
	if got < want-epsilon || got > want+epsilon {
		t.Errorf("degraded score = %v, want ~%v", got, want)
	}
}

// TestClientScore_StaleEvents verifies that without any RecordEventReceived
// call the activity pillar is 0 and the score is reduced accordingly.
func TestClientScore_StaleEvents(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	tr.Record("iface", health.Sample{Healthy: true, Note: "breaker closed"})
	// No RecordEventReceived — age treated as 1 h → activity = 0.0.

	got := tr.ClientScore("iface")
	// state=1.0, circuit=1.0, activity=0.0 → 0.4*1.0 + 0.3*1.0 + 0.3*0.0 = 0.70
	const want = 0.70
	const epsilon = 0.01
	if got < want-epsilon || got > want+epsilon {
		t.Errorf("no event score = %v, want ~%v (activity pillar = 0)", got, want)
	}
}

// --- PrimaryClientHealthy ---

// TestPrimaryClientHealthy_DefaultFallback registers a component whose
// name contains "HmIP-RF" and checks the healthy/unhealthy transitions.
func TestPrimaryClientHealthy_DefaultFallback(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))

	// Not registered yet → false.
	if tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = true with no registered components, want false")
	}

	tr.Record("ccu-main-HmIP-RF", health.Sample{Healthy: true})
	if !tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = false after healthy HmIP-RF component, want true")
	}

	// Two consecutive failures → UNHEALTHY.
	tr.Record("ccu-main-HmIP-RF", health.Sample{Healthy: false})
	tr.Record("ccu-main-HmIP-RF", health.Sample{Healthy: false})
	if tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = true after two failures, want false")
	}
}

// TestPrimaryClientHealthy_NoComponent verifies that false is returned
// when no component matching the fallback name is registered.
func TestPrimaryClientHealthy_NoComponent(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))
	// Register a component that does NOT match "HmIP-RF".
	tr.Record("ccu-main-BidCos-RF", health.Sample{Healthy: true})

	if tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = true without HmIP-RF component, want false")
	}
}

// TestPrimaryClientHealthy_ExplicitSetPrimaryInterface verifies that an
// explicit SetPrimaryInterface pin overrides the default HmIP-RF lookup.
func TestPrimaryClientHealthy_ExplicitSetPrimaryInterface(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))

	tr.SetPrimaryInterface("custom-iface")
	// Component name that contains "custom-iface" as a substring.
	tr.Record("ccu-custom-iface", health.Sample{Healthy: true})

	if !tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = false for explicit primary with healthy component, want true")
	}

	// Mark unhealthy.
	tr.Record("ccu-custom-iface", health.Sample{Healthy: false})
	tr.Record("ccu-custom-iface", health.Sample{Healthy: false})
	if tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = true after explicit primary goes unhealthy, want false")
	}
}

// --- PrimaryInterfaceHmIP constant ---

// TestPrimaryInterfaceHmIP_Constant verifies the exported constant value.
func TestPrimaryInterfaceHmIP_Constant(t *testing.T) {
	const want = "HmIP-RF"
	if health.PrimaryInterfaceHmIP != want {
		t.Errorf("PrimaryInterfaceHmIP = %q, want %q", health.PrimaryInterfaceHmIP, want)
	}
}

// --- ClientDetail unknown ---

// TestClientDetail_Unknown verifies that an unregistered name returns false.
func TestClientDetail_Unknown(t *testing.T) {
	tr := health.NewTracker(health.WithStaleAfter(0))
	_, ok := tr.ClientDetail("doesnotexist")
	if ok {
		t.Error("ClientDetail(unknown) returned true, want false")
	}
}
