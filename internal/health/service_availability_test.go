// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
