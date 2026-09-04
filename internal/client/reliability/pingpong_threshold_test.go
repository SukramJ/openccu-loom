// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPingPongThresholdBoundaryIsOneRule drives the two production readers
// of the mismatch threshold — the emit decision inside RecordPong's matched
// branch and the severity reported by Stats — over the same pending-table
// sizes and requires them to agree on where the boundary sits.
//
// The four boundary tests were written three times over ("> threshold" at
// the emit sites, ">= threshold" in Stats), so the same table size counted
// as an anomaly for the health severity and as normal for the event. Both
// now read overThreshold; this asserts the agreement through the public
// entry points rather than through that helper.
func TestPingPongThresholdBoundaryIsOneRule(t *testing.T) {
	t.Parallel()

	const threshold = 3

	for pending := 2; pending <= 6; pending++ {
		t.Run(fmt.Sprintf("pending_%d", pending), func(t *testing.T) {
			t.Parallel()

			fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			tr := NewPingPongTracker(PingPongConfig{
				PendingTTL:        time.Hour,
				UnknownTTL:        time.Hour,
				MismatchThreshold: threshold,
				Clock:             fake,
			})

			ids := make([]string, pending)
			for i := range ids {
				ids[i] = fmt.Sprintf("ping-%d", i)
				tr.RecordPing(ids[i])
			}

			// Install the hook only now, so the pings above cannot emit and
			// the single observation below belongs to the pong path.
			var emitted []int
			tr.SetPublishHook(func(kind hmenum.PingPongMismatchType, count int) {
				if kind == hmenum.PingPongMismatchPending {
					emitted = append(emitted, count)
				}
			})

			wantSeverityOver := pending > threshold
			gotSeverity := tr.Stats().Severity
			if (gotSeverity != "ok") != wantSeverityOver {
				t.Errorf("pending=%d threshold=%d: Stats().Severity=%q, over-threshold=%v",
					pending, threshold, gotSeverity, wantSeverityOver)
			}

			// One matched PONG leaves pending-1 entries behind. The emit
			// decision must use the same boundary the severity just used.
			if matched, _ := tr.RecordPong(ids[0]); !matched {
				t.Fatalf("RecordPong(%q) did not match", ids[0])
			}
			remaining := pending - 1
			wantEmit := remaining > threshold
			if gotEmit := len(emitted) == 1; gotEmit != wantEmit {
				t.Errorf("pending=%d after match threshold=%d: emitted=%v, want emit=%v",
					remaining, threshold, emitted, wantEmit)
			}
			if wantEmit && emitted[0] != remaining {
				t.Errorf("emitted count=%d, want %d", emitted[0], remaining)
			}

			// And the severity that follows the same table agrees again.
			if gotAfter := tr.Stats().Severity != "ok"; gotAfter != wantEmit {
				t.Errorf("pending=%d: Severity-over=%v, emit=%v — boundaries disagree",
					remaining, gotAfter, wantEmit)
			}
		})
	}
}
