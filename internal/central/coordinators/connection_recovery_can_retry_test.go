// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// connection_recovery_can_retry_test.go — CanRetry / AttemptCount /
// default-backoff constants.
package coordinators

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
)

// TestCanRetryTable consolidates the six Python can_retry_* assertions into a
// single table-driven test. Each case drives a fresh, limited coordinator and
// asserts the CanRetry flag on the resulting MetricsRecoveryStates snapshot.
func TestCanRetryTable(t *testing.T) {
	t.Parallel()

	failing := []Pipeline{{
		Stage: "ping",
		Run:   func(_ context.Context) error { return errors.New("forced") },
	}}

	cases := []struct {
		name      string
		maxCap    int
		runCount  int
		wantRetry bool
	}{
		{
			name:      "true initially (no attempts yet)",
			maxCap:    10,
			runCount:  0,
			wantRetry: true,
		},
		{
			name:      "true when under max attempts",
			maxCap:    5,
			runCount:  4, // one below cap
			wantRetry: true,
		},
		{
			name:      "false when at max attempts",
			maxCap:    3,
			runCount:  3, // exactly at cap
			wantRetry: false,
		},
		{
			name:      "false when above max attempts (cap=1)",
			maxCap:    1,
			runCount:  2, // above cap
			wantRetry: false,
		},
		{
			name:      "true when cap is zero (unlimited)",
			maxCap:    0,
			runCount:  50,
			wantRetry: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := NewConnectionRecoveryCoordinatorWithLimit("ccu-canretry", events.NewBus(), tc.maxCap)

			for range tc.runCount {
				_ = c.Run(context.Background(), "HmIP-RF", failing)
			}

			// For runCount == 0 no state entry exists yet; the coordinator
			// must still report CanRetry=true via the metrics view.
			if tc.runCount == 0 {
				// Seed a state entry with zero attempts via AttemptCount so
				// MetricsRecoveryStates has something to observe; alternatively,
				// verify directly via AttemptCount (no entry => 0, below any cap).
				if got := c.AttemptCount("HmIP-RF"); got != 0 {
					t.Fatalf("fresh coordinator: AttemptCount=%d, want 0", got)
				}
				// No entry in MetricsRecoveryStates yet — that is the definition
				// of "can retry" for a fresh interface. Confirm by running once
				// (success path) and then checking CanRetry=true.
				_ = c.Run(context.Background(), "HmIP-RF", []Pipeline{{
					Stage: "ping",
					Run:   func(_ context.Context) error { return nil },
				}})
				states := c.MetricsRecoveryStates()
				if st, ok := states["HmIP-RF"]; ok {
					if !st.CanRetry() {
						t.Fatalf("CanRetry()=false after first success on unlimited cap, want true")
					}
				}
				return
			}

			states := c.MetricsRecoveryStates()
			st, ok := states["HmIP-RF"]
			if !ok {
				t.Fatal("no state entry for HmIP-RF after runs")
			}
			if got := st.CanRetry(); got != tc.wantRetry {
				t.Fatalf("CanRetry()=%v, want %v (cap=%d runCount=%d)",
					got, tc.wantRetry, tc.maxCap, tc.runCount)
			}
		})
	}
}

// TestDefaultBackoffConstants verifies that the coordinator's default backoff
// parameters produce the expected delays (base=2s, max=120s) without any
// SetBackoff override. Formula: min(max, base * 2^(attempts-1)).
func TestDefaultBackoffConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		failures  int
		wantDelay time.Duration
		desc      string
	}{
		{
			name:      "base delay after first failure is 2s",
			failures:  1,
			wantDelay: 2 * time.Second,
			desc:      "base * 2^0 = 2s",
		},
		{
			name:      "delay capped at 120s after many failures",
			failures:  20,
			wantDelay: 120 * time.Second,
			desc:      "saturates at max=120s",
		},
		{
			name:      "third failure yields 8s",
			failures:  3,
			wantDelay: 8 * time.Second,
			desc:      "base*2^(n-1): 2*2^2=8s",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := NewConnectionRecoveryCoordinatorWithLimit("ccu-backoff", events.NewBus(), 0)

			failing := []Pipeline{{
				Stage: "ping",
				Run:   func(_ context.Context) error { return errors.New("forced") },
			}}
			for range tc.failures {
				_ = c.Run(context.Background(), "HmIP-RF", failing)
			}

			got := c.NextRetryDelay("HmIP-RF")
			if got != tc.wantDelay {
				t.Fatalf("after %d failures: NextRetryDelay=%v, want %v (%s)",
					tc.failures, got, tc.wantDelay, tc.desc)
			}
		})
	}
}
