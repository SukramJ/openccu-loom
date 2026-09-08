// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestCallbackBindFailureIsVisibleOnHealth is the guard for a daemon whose
// push path is dead while it reports itself fine.
//
// A callback listener that cannot bind is deliberately non-fatal — the daemon
// still serves REST and the config surface, which is how an operator fixes the
// conflict. But until this component existed the condition was a single boot
// WARN: /health answered 200, every central reached readiness.ready, and no
// CCU value-change event could ever arrive. Two daemons on one host, or
// anything else already holding 8120, produced exactly that.
func TestCallbackBindFailureIsVisibleOnHealth(t *testing.T) {
	t.Parallel()
	bindErr := errors.New("listen tcp 127.0.0.1:8120: bind: address already in use")

	cases := []struct {
		name              string
		xmlrpcErr         error
		binrpcErr         error
		wantHealthy       bool
		wantNoteFragments []string
	}{
		{
			name:              "both listeners bound",
			wantHealthy:       true,
			wantNoteFragments: []string{"bound"},
		},
		{
			name:              "XML-RPC listener down",
			xmlrpcErr:         bindErr,
			wantHealthy:       false,
			wantNoteFragments: []string{"XML-RPC", "address already in use"},
		},
		{
			name:              "BIN-RPC listener down",
			binrpcErr:         bindErr,
			wantHealthy:       false,
			wantNoteFragments: []string{"BIN-RPC", "address already in use"},
		},
		{
			name:              "both listeners down",
			xmlrpcErr:         bindErr,
			binrpcErr:         bindErr,
			wantHealthy:       false,
			wantNoteFragments: []string{"no callback listener bound"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tracker := health.NewTracker()
			recordCallbackHealth(tracker, nil, tc.xmlrpcErr, tc.binrpcErr)

			var got *health.Component
			for _, c := range tracker.Snapshot() {
				if c.Name == callbackHealthComponent {
					got = &c
					break
				}
			}
			if got == nil {
				t.Fatalf("no %q component recorded — a dead push path stays invisible",
					callbackHealthComponent)
			}
			healthy := got.Status == health.StatusHealthy
			if healthy != tc.wantHealthy {
				t.Errorf("status = %q, want healthy=%v (note %q)", got.Status, tc.wantHealthy, got.LastSample.Note)
			}
			for _, frag := range tc.wantNoteFragments {
				if !strings.Contains(got.LastSample.Note, frag) {
					t.Errorf("note %q does not mention %q — an operator cannot tell which listener is down",
						got.LastSample.Note, frag)
				}
			}
		})
	}
}

// TestCallbackBindFailureDegradesWithoutDraining pins the severity. The
// daemon is still worth reaching — an operator has to be able to load the
// config surface to fix the port conflict — so the failure must collapse to
// `degraded`, never to the 503 that /health maps StatusUnhealthy to.
func TestCallbackBindFailureDegradesWithoutDraining(t *testing.T) {
	t.Parallel()
	tracker := health.NewTracker()
	recordCallbackHealth(tracker, nil, errors.New("bind: address already in use"), nil)
	// A healthy peer, so the snapshot is not a single-component edge case.
	tracker.Record("persistence", health.Sample{Healthy: true, Sticky: true})

	if got := health.ServiceAvailability(tracker.Snapshot()); got != health.StatusDegraded {
		t.Fatalf("ServiceAvailability = %q, want %q — a daemon that can still be "+
			"reconfigured must not be drained, but the condition must show",
			got, health.StatusDegraded)
	}
}
