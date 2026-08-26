// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health

import "testing"

// TestServiceAvailability locks the /health HTTP-code collapse: a single
// south-bound interface (or the MQTT bridge) down only DEGRADES service,
// while a fatal dependency or a total south-bound outage is UNHEALTHY.
func TestServiceAvailability(t *testing.T) {
	t.Parallel()
	comp := func(name string, s Status) Component { return Component{Name: name, Status: s} }

	cases := []struct {
		name string
		in   []Component
		want Status
	}{
		{"empty", nil, StatusUnknown},
		{
			"all healthy",
			[]Component{comp("OttoGo-HmIP-RF", StatusHealthy), comp("sqlite", StatusHealthy)},
			StatusHealthy,
		},
		{
			"single interface down degrades",
			[]Component{
				comp("OttoGo-HmIP-RF", StatusHealthy),
				comp("KearneyGo-CUxD", StatusUnhealthy),
				comp("sqlite", StatusHealthy),
			},
			StatusDegraded,
		},
		{
			"mqtt down degrades (north-bound, daemon still serves)",
			[]Component{comp("OttoGo-HmIP-RF", StatusHealthy), comp("mqtt", StatusUnhealthy)},
			StatusDegraded,
		},
		{
			"critical persistence down is fatal",
			[]Component{comp("OttoGo-HmIP-RF", StatusHealthy), comp("sqlite", StatusUnhealthy)},
			StatusUnhealthy,
		},
		{
			"central coordinator down is fatal",
			[]Component{comp("OttoGo-HmIP-RF", StatusHealthy), comp("central", StatusUnhealthy)},
			StatusUnhealthy,
		},
		{
			"every interface down is a total outage",
			[]Component{
				comp("OttoGo-HmIP-RF", StatusUnhealthy),
				comp("KearneyGo-CUxD", StatusUnhealthy),
				comp("sqlite", StatusHealthy),
			},
			StatusUnhealthy,
		},
		{
			"only unknown is unknown",
			[]Component{comp("central", StatusUnknown)},
			StatusUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ServiceAvailability(tc.in); got != tc.want {
				t.Fatalf("ServiceAvailability = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestServiceAvailabilityOnAScopedSnapshot drives the same verdict over the
// component names /health actually sees.
//
// Every production caller reads the unioned multi-CCU snapshot, where each
// per-central entry is rewritten to "<central>/<component>". Classifying that
// by the bare recorder name matched nothing: an unhealthy per-central `central`
// heartbeat could not reach the critical rule, and the ping/pong quality entry
// — kept out of the interface count precisely because it is capped at DEGRADED
// — counted as an interface again, so "every interface down" became
// unreachable and a total south-bound outage answered 200.
func TestServiceAvailabilityOnAScopedSnapshot(t *testing.T) {
	t.Parallel()
	comp := func(name string, s Status) Component { return Component{Name: name, Status: s} }

	cases := []struct {
		name string
		in   []Component
		want Status
	}{
		{
			"scoped central heartbeat down is fatal",
			[]Component{
				comp("ccu/ccu-HmIP-RF", StatusHealthy),
				comp("ccu/central", StatusUnhealthy),
			},
			StatusUnhealthy,
		},
		{
			"total outage with a ping/pong entry recorded",
			[]Component{
				comp("ccu/ccu-HmIP-RF", StatusUnhealthy),
				comp("ccu/ccu-CUxD", StatusUnhealthy),
				comp("ccu/"+PingPongComponent("ccu-HmIP-RF"), StatusDegraded),
				comp("sqlite", StatusHealthy),
			},
			StatusUnhealthy,
		},
		{
			"total outage on a hyphenated central with a startup entry",
			[]Component{
				comp("ccu-main/ccu-main-HmIP-RF", StatusUnhealthy),
				comp("ccu-main/startup.ccu-main", StatusDegraded),
				comp("ccu-main/scheduler", StatusHealthy),
				comp("sqlite", StatusHealthy),
			},
			StatusUnhealthy,
		},
		{
			"one of two CCUs down still only degrades",
			[]Component{
				comp("ccu/ccu-HmIP-RF", StatusUnhealthy),
				comp("other/other-HmIP-RF", StatusHealthy),
				comp("sqlite", StatusHealthy),
			},
			StatusDegraded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ServiceAvailability(tc.in); got != tc.want {
				t.Fatalf("ServiceAvailability = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCentralScoreMatchesOnBoundariesNotSubstrings pins that one CCU's health
// never contributes to another's tile.
//
// Component names carry the central name, and central names may contain "-",
// so a substring test let "ccu" match every component of "ccu-test". With the
// adapter treating a zero score as "this tracker has nothing for that central",
// a completely dead CCU could then render its neighbour's score.
func TestCentralScoreMatchesOnBoundariesNotSubstrings(t *testing.T) {
	t.Parallel()
	tr := NewTracker()
	// A healthy "ccu-test" alongside a dead "ccu".
	tr.Record("ccu-test-HmIP-RF", Sample{Healthy: true})
	tr.Record("ccu-test-CUxD", Sample{Healthy: true})
	tr.Record("ccu-HmIP-RF", Sample{Healthy: false})
	tr.Record("startup.ccu", Sample{Healthy: false})

	if got := tr.CentralScore("ccu"); got != 0 {
		t.Fatalf("CentralScore(ccu) = %v, want 0 — ccu is down; ccu-test must not be counted", got)
	}
	if got := tr.CentralScore("ccu-test"); got != 1 {
		t.Fatalf("CentralScore(ccu-test) = %v, want 1", got)
	}
	// The ping/pong quality entry names the interface id it tracks and
	// belongs to that interface's central.
	tr.RecordQuality(PingPongComponent("ccu-test-HmIP-RF"), "orphan pong")
	if got := tr.CentralScore("ccu-test"); got != 5.0/6.0 {
		t.Fatalf("CentralScore(ccu-test) = %v, want 5/6 with the ping/pong entry counted", got)
	}
	// The unioned snapshot form resolves through the scope prefix.
	scoped := NewTracker()
	scoped.Record("ccu/central", Sample{Healthy: true})
	if got := scoped.CentralScore("ccu"); got != 1 {
		t.Fatalf("CentralScore over a scoped name = %v, want 1", got)
	}
	if got := scoped.CentralScore("cc"); got != 0 {
		t.Fatalf("CentralScore(cc) = %v, want 0 — a name prefix is not a central", got)
	}
}
