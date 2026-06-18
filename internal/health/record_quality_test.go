// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// record_quality_test.go — tests for RecordQuality, ServiceAvailability
// ping/pong component exclusion, and PrimaryClientHealthy ping/pong skipping.
package health

import "testing"

// TestRecordQualityYieldsDegraded verifies that RecordQuality sets the named
// component to StatusDegraded — never StatusUnhealthy — so a ping/pong
// correlation noise event cannot escalate the interface health to a fatal state.
func TestRecordQualityYieldsDegraded(t *testing.T) {
	t.Parallel()
	tr := NewTracker(WithStaleAfter(0))
	tr.RecordQuality("ping_pong/ccu-HmIP-RF", "orphan PONG from co-located daemon")
	c, ok := tr.Get("ping_pong/ccu-HmIP-RF")
	if !ok {
		t.Fatal("Get returned false after RecordQuality")
	}
	if c.Status != StatusDegraded {
		t.Errorf("RecordQuality status = %s, want %s", c.Status, StatusDegraded)
	}
}

// TestRecordQualityNeverEscalatesToUnhealthy verifies that repeated calls to
// RecordQuality keep the component at StatusDegraded; they must never escalate
// to StatusUnhealthy regardless of call count.
func TestRecordQualityNeverEscalatesToUnhealthy(t *testing.T) {
	t.Parallel()
	tr := NewTracker(WithStaleAfter(0))
	for range 10 {
		tr.RecordQuality("ping_pong/x", "noise")
	}
	c, _ := tr.Get("ping_pong/x")
	if c.Status == StatusUnhealthy {
		t.Errorf("RecordQuality escalated to %s after repeated calls; cap is %s", StatusUnhealthy, StatusDegraded)
	}
	if c.Status != StatusDegraded {
		t.Errorf("RecordQuality status = %s after repeated calls, want %s", c.Status, StatusDegraded)
	}
}

// TestServiceAvailabilityPingPongUnhealthyDoesNotCause503 verifies that a
// healthy interface + a healthy persistence layer + an unhealthy ping/pong
// component does NOT push ServiceAvailability to StatusUnhealthy. The
// ping/pong signal is a quality hint, not a liveness signal; it must never
// trip the "every interface down → 503" rule.
func TestServiceAvailabilityPingPongUnhealthyDoesNotCause503(t *testing.T) {
	t.Parallel()
	comp := func(name string, s Status) Component { return Component{Name: name, Status: s} }
	components := []Component{
		comp("OttoGo-HmIP-RF", StatusHealthy),
		comp("sqlite", StatusHealthy),
		comp("ping_pong/OttoGo-HmIP-RF", StatusUnhealthy),
	}
	got := ServiceAvailability(components)
	if got == StatusUnhealthy {
		t.Errorf("ServiceAvailability = %s with ping_pong unhealthy; must not return %s", got, StatusUnhealthy)
	}
	// The ping/pong mismatch degrades quality but the service is not down.
	if got != StatusDegraded {
		t.Errorf("ServiceAvailability = %s, want %s when ping_pong is the only bad signal", got, StatusDegraded)
	}
}

// TestServiceAvailabilityPingPongDoesNotCountAsInterface verifies that a
// ping/pong component does not inflate ifaceTotal — if the only real interface
// is healthy and only the ping/pong component is unhealthy, the result is
// NOT StatusUnhealthy (the "every interface down" rule must not fire).
func TestServiceAvailabilityPingPongDoesNotCountAsInterface(t *testing.T) {
	t.Parallel()
	comp := func(name string, s Status) Component { return Component{Name: name, Status: s} }
	components := []Component{
		comp("OttoGo-HmIP-RF", StatusHealthy),
		comp("ping_pong/OttoGo-HmIP-RF", StatusUnhealthy),
	}
	got := ServiceAvailability(components)
	if got == StatusUnhealthy {
		t.Errorf("ServiceAvailability = %s; ping/pong must not count as the only interface being down", got)
	}
}

// TestPrimaryClientHealthyIgnoresPingPongComponent verifies that a degraded or
// unhealthy ping/pong component for the same interface does not prevent
// PrimaryClientHealthy from returning true when the actual liveness component
// is healthy.
func TestPrimaryClientHealthyIgnoresPingPongComponent(t *testing.T) {
	t.Parallel()
	tr := NewTracker(WithStaleAfter(0))
	// Register a healthy liveness component for the primary interface.
	tr.Record("ccu-"+PrimaryInterfaceHmIP, Sample{Healthy: true})
	// Register a noisy ping/pong quality component for the same interface.
	tr.RecordQuality(PingPongComponent("ccu-"+PrimaryInterfaceHmIP), "co-located daemon noise")
	if !tr.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy() = false; ping/pong quality component must not override liveness verdict")
	}
}
