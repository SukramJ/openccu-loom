// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// hmCliScheduleProbe runs one retry chain against a recording clock and
// returns the backoff delays the retrier asked for, in order.
type hmCliScheduleProbe struct {
	name   string
	run    func(r *Retrier, fn func(ctx context.Context, attempt int) error) error
	delays []time.Duration
}

// TestHmCliRetryScheduleIsTheSameForDoAndDoForKey pins that the two retry
// entry points follow ONE backoff policy.
//
// [Retrier.Do] and [Retrier.DoForKey] are two transcriptions of the same state
// machine, and DoForKey — the one that owns every device write via setValue /
// putParamset — is the copy a reader is less likely to update. The schedule is
// where drift is both most likely and least visible: a change to the initial
// delay, the multiplier, the cap, or to the special-delay carve-out applied to
// one loop leaves the other on the old policy, and every existing test stays
// green because each pins its own entry point.
//
// The chain is driven with an ordinary transient failure (regular schedule)
// and then with a DUTY_CYCLE fault (fixed special delay, and the exponential
// schedule must NOT advance while it is in force), because those are the two
// branches the advance rule distinguishes.
func TestHmCliRetryScheduleIsTheSameForDoAndDoForKey(t *testing.T) {
	cases := []struct {
		name string
		fail error
	}{
		{"transient error walks the exponential schedule", errors.New("boom")},
		{"duty-cycle fault holds the exponential schedule", &hmerr.XMLRPCFault{Code: -8, Message: "DUTY_CYCLE"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probes := []*hmCliScheduleProbe{
				{
					name: "Do",
					run: func(r *Retrier, fn func(ctx context.Context, attempt int) error) error {
						return r.Do(context.Background(), fn)
					},
				},
				{
					name: "DoForKey",
					run: func(r *Retrier, fn func(ctx context.Context, attempt int) error) error {
						key := hmtypes.DataPointKey{
							InterfaceID:    "central-HmIP-RF",
							ChannelAddress: "VCU0000123:1",
							ParamsetKey:    "VALUES",
							Parameter:      "LEVEL",
						}
						return r.DoForKey(context.Background(), key, fn)
					},
				},
			}

			for _, p := range probes {
				clk := newRecordingClock()
				r := NewRetrier(RetryConfig{
					MaxAttempts: 4,
					Initial:     100 * time.Millisecond,
					Max:         10 * time.Second,
					Multiplier:  2,
					Jitter:      -1, // negative disables jitter; the schedule itself is under test
					Clock:       clk,
				})
				err := p.run(r, func(context.Context, int) error { return tc.fail })
				if err == nil {
					t.Fatalf("%s: retry chain succeeded, want the injected failure", p.name)
				}
				p.delays = clk.Delays()
				if len(p.delays) == 0 {
					t.Fatalf("%s: no backoff delay recorded — the probe measured nothing", p.name)
				}
			}

			got, want := probes[1].delays, probes[0].delays
			if len(got) != len(want) {
				t.Fatalf("DoForKey took %d backoff waits, Do took %d (%v vs %v) — the two entry points are on different retry policies", len(got), len(want), got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("backoff wait %d: DoForKey = %s, Do = %s — the two entry points are on different retry policies", i+1, got[i], want[i])
				}
			}
		})
	}
}
