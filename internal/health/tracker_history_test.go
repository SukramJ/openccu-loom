// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// t0 is the stable anchor used by all fake-clock tests.
var t0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// ── History ring ─────────────────────────────────────────────────────────────

func TestRecordAppendsToHistory(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk))

	tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(0)})
	tr.Record("c", Sample{Healthy: false, Timestamp: t0.Add(10 * time.Second)})
	tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(20 * time.Second)})

	hist := tr.History("c", 0)
	if len(hist) != 3 {
		t.Fatalf("len=%d want 3", len(hist))
	}
	// oldest first
	if !hist[0].Timestamp.Equal(t0) {
		t.Errorf("hist[0].Timestamp=%v want %v", hist[0].Timestamp, t0)
	}
	if !hist[2].Timestamp.Equal(t0.Add(20 * time.Second)) {
		t.Errorf("hist[2].Timestamp=%v want %v", hist[2].Timestamp, t0.Add(20*time.Second))
	}
}

func TestHistoryRingEvictsOldest(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithHistorySize(2))

	for i := range 5 {
		tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(time.Duration(i) * time.Second)})
	}

	hist := tr.History("c", 0)
	if len(hist) != 2 {
		t.Fatalf("len=%d want 2 (ring capped at 2)", len(hist))
	}
	// the two newest entries must survive
	want3 := t0.Add(3 * time.Second)
	want4 := t0.Add(4 * time.Second)
	if !hist[0].Timestamp.Equal(want3) || !hist[1].Timestamp.Equal(want4) {
		t.Errorf("hist timestamps = [%v, %v], want [%v, %v]",
			hist[0].Timestamp, hist[1].Timestamp, want3, want4)
	}
}

func TestHistoryLimitClamps(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk))

	for i := range 5 {
		tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(time.Duration(i) * time.Second)})
	}

	hist := tr.History("c", 2)
	if len(hist) != 2 {
		t.Fatalf("len=%d want 2", len(hist))
	}
	// limit=2 returns the most recent 2, oldest-first within those
	want3 := t0.Add(3 * time.Second)
	want4 := t0.Add(4 * time.Second)
	if !hist[0].Timestamp.Equal(want3) || !hist[1].Timestamp.Equal(want4) {
		t.Errorf("hist timestamps = [%v, %v], want [%v, %v]",
			hist[0].Timestamp, hist[1].Timestamp, want3, want4)
	}
}

func TestHistoryUnknownComponentReturnsNil(t *testing.T) {
	t.Parallel()
	tr := NewTracker()
	if got := tr.History("no-such-component", 0); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// ── Stale decay ───────────────────────────────────────────────────────────────

func TestStaleSampleDecaysToUnknown(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(time.Minute))

	tr.Record("c", Sample{Healthy: true, Timestamp: t0})

	// 30 s after recording: still healthy.
	clk.Advance(30 * time.Second)
	c, ok := tr.Get("c")
	if !ok {
		t.Fatal("component not found")
	}
	if c.Status != StatusHealthy {
		t.Errorf("at +30s status=%s, want healthy", c.Status)
	}

	// 90 s after recording (past the 60 s stale threshold): decays to unknown.
	clk.Advance(60 * time.Second) // total +90s
	c, ok = tr.Get("c")
	if !ok {
		t.Fatal("component not found after advance")
	}
	if c.Status != StatusUnknown {
		t.Errorf("at +90s status=%s, want unknown", c.Status)
	}
	// LastSample must remain unchanged.
	if !c.LastSample.Timestamp.Equal(t0) {
		t.Errorf("LastSample.Timestamp=%v, want %v", c.LastSample.Timestamp, t0)
	}
	if !c.LastSample.Healthy {
		t.Error("LastSample.Healthy modified; want true (original value)")
	}
}

// TestStickySampleNeverDecaysToUnknown pins the boot-only-component case:
// a component recorded exactly once at boot (config secrets resolved,
// the DB config overlay applied, unroutable centrals enumerated) must
// not decay to StatusUnknown just because staleAfter elapsed with no
// second Record call — that component was never going to be
// re-recorded, so decaying it turns a permanently healthy fact into a
// permanently degraded /health after 90 s on every daemon, regardless
// of how healthy the rest of it is.
func TestStickySampleNeverDecaysToUnknown(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(time.Minute))

	tr.Record("c", Sample{Healthy: true, Timestamp: t0, Sticky: true})

	// Advance well past the stale threshold — an ordinary sample would
	// have decayed to unknown by now (see TestStaleSampleDecaysToUnknown).
	clk.Advance(24 * time.Hour)
	c, ok := tr.Get("c")
	if !ok {
		t.Fatal("component not found")
	}
	if c.Status != StatusHealthy {
		t.Errorf("sticky component at +24h status=%s, want healthy (no decay)", c.Status)
	}

	// Overall must reflect the same: a sticky component alone in the
	// tracker must not drag Overall() down to unknown either.
	if got := tr.Overall(); got != StatusHealthy {
		t.Errorf("Overall() at +24h = %s, want healthy", got)
	}
}

func TestStaleZeroDisablesDecay(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(0))

	tr.Record("c", Sample{Healthy: true, Timestamp: t0})

	// Advance a very long time — should not decay.
	clk.Advance(365 * 24 * time.Hour)
	c, ok := tr.Get("c")
	if !ok {
		t.Fatal("component not found")
	}
	if c.Status != StatusHealthy {
		t.Errorf("status=%s, want healthy (decay disabled)", c.Status)
	}
}

func TestOverallReportsUnknownWhenAllStale(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(time.Minute))

	tr.Record("c", Sample{Healthy: true, Timestamp: t0})

	clk.Advance(2 * time.Minute) // past stale threshold

	if got := tr.Overall(); got != StatusUnknown {
		t.Errorf("Overall=%s, want unknown (all stale)", got)
	}
}

func TestOverallUnhealthyTrumpsStale(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(time.Minute))

	// Component "stale": recorded once, then goes silent.
	tr.Record("stale", Sample{Healthy: true, Timestamp: t0})

	// Component "bad": freshly unhealthy (two failures to reach UNHEALTHY).
	fresh := t0.Add(2 * time.Minute)
	tr.Record("bad", Sample{Healthy: true, Timestamp: fresh})
	tr.Record("bad", Sample{Healthy: false, Timestamp: fresh.Add(time.Second)})
	tr.Record("bad", Sample{Healthy: false, Timestamp: fresh.Add(2 * time.Second)})

	// Advance past stale threshold so "stale" is now Unknown.
	clk.Advance(3 * time.Minute)

	if got := tr.Overall(); got != StatusUnhealthy {
		t.Errorf("Overall=%s, want unhealthy (unhealthy trumps stale unknown)", got)
	}
}

func TestUnhealthyComponentsListReflectsStaleDecay(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk), WithStaleAfter(time.Minute))

	// Record a component that becomes unhealthy…
	tr.Record("gone", Sample{Healthy: true, Timestamp: t0})
	tr.Record("gone", Sample{Healthy: false, Timestamp: t0.Add(time.Second)})
	tr.Record("gone", Sample{Healthy: false, Timestamp: t0.Add(2 * time.Second)})

	// …and then goes stale (no new samples).
	clk.Advance(2 * time.Minute)

	unhealthy := tr.UnhealthyComponents()
	for _, name := range unhealthy {
		if name == "gone" {
			t.Errorf("stale component %q must not appear in UnhealthyComponents", name)
		}
	}
	degraded := tr.DegradedComponents()
	for _, name := range degraded {
		if name == "gone" {
			t.Errorf("stale component %q must not appear in DegradedComponents", name)
		}
	}
}

// ── Windowed score ────────────────────────────────────────────────────────────

func TestWindowedScoreNoSamplesReturnsZero(t *testing.T) {
	t.Parallel()
	tr := NewTracker()
	if got := tr.WindowedScore("no-such", time.Minute); got != 0 {
		t.Errorf("WindowedScore=%v, want 0 (no component)", got)
	}
}

func TestWindowedScoreCountsOnlyInWindow(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk))

	// Record 4 samples spaced 30 s apart.
	// t0+0s → unhealthy
	// t0+30s → healthy
	// t0+60s → healthy
	// t0+90s → healthy
	tr.Record("c", Sample{Healthy: false, Timestamp: t0})
	tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(30 * time.Second)})
	tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(60 * time.Second)})
	tr.Record("c", Sample{Healthy: true, Timestamp: t0.Add(90 * time.Second)})

	// Advance clock to t0+90s so "now" = t0+90s.
	// WindowedScore with a 60 s window looks back to t0+30s.
	// In-window samples: t0+30s (healthy), t0+60s (healthy), t0+90s (healthy) → 3/3 = 1.0.
	// The sample at t0 is outside the window.
	clk.Advance(90 * time.Second)
	got := tr.WindowedScore("c", 60*time.Second)
	if got != 1.0 {
		t.Errorf("WindowedScore=%v, want 1.0 (3 healthy in window)", got)
	}

	// Now use a 70 s window (cutoff = t0+20s):
	// In-window: t0+30s, t0+60s, t0+90s → still 3/3 = 1.0.
	// Verify we get 0 from the one unhealthy sample being excluded.

	// Confirm unhealthy sample at t0 is excluded from a 60 s window.
	// For a tighter check: advance to t0+120s (60 s window → cutoff = t0+60s).
	// In-window: t0+60s (healthy), t0+90s (healthy) → 2/2 = 1.0.
	// Unhealthy sample at t0 is gone. Use 30 s window for 2 samples.
	// cutoff = t0+90s; only t0+90s (healthy) qualifies → 1/1 = 1.0.
	// Use 61 s window: cutoff = t0+29s → t0+30s, t0+60s, t0+90s in, t0 out → 1.0.
	// Use 91 s window: all 4 in → 3 healthy / 4 total = 0.75.
	got2 := tr.WindowedScore("c", 91*time.Second)
	const want2 = 3.0 / 4.0
	if got2 != want2 {
		t.Errorf("WindowedScore(91s)=%v, want %v (all 4 samples: 3 healthy)", got2, want2)
	}
}

func TestWindowedScoreNonPositiveWindowReturnsZero(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk))
	tr.Record("c", Sample{Healthy: true, Timestamp: t0})

	if got := tr.WindowedScore("c", 0); got != 0 {
		t.Errorf("WindowedScore(0)=%v, want 0", got)
	}
	if got := tr.WindowedScore("c", -1*time.Second); got != 0 {
		t.Errorf("WindowedScore(-1s)=%v, want 0", got)
	}
}

func TestOverallWindowedScoreAveragesAcrossComponents(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk))

	// component "a": 2 healthy samples → score 1.0
	tr.Record("a", Sample{Healthy: true, Timestamp: t0})
	tr.Record("a", Sample{Healthy: true, Timestamp: t0.Add(10 * time.Second)})

	// component "b": 1 healthy, 1 unhealthy → score 0.5
	tr.Record("b", Sample{Healthy: true, Timestamp: t0})
	tr.Record("b", Sample{Healthy: false, Timestamp: t0.Add(10 * time.Second)})

	// Compute at t0+10s; window = 60 s → all 4 samples are in-window.
	clk.Advance(10 * time.Second)
	got := tr.OverallWindowedScore(60 * time.Second)
	const want = (1.0 + 0.5) / 2
	if got != want {
		t.Errorf("OverallWindowedScore=%v, want %v", got, want)
	}
}

func TestOverallWindowedScoreSkipsComponentsWithoutInWindowSamples(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(t0)
	tr := NewTracker(WithClock(clk))

	// component "old": one sample recorded far in the past.
	tr.Record("old", Sample{Healthy: true, Timestamp: t0})

	// component "fresh": one healthy sample recorded 5 s later.
	tr.Record("fresh", Sample{Healthy: true, Timestamp: t0.Add(5 * time.Second)})

	// Advance 200 s so "old"'s sample (at t0) is outside a 60 s window,
	// while "fresh"'s sample (at t0+5s) is also outside the 60 s window.
	// Use a 180 s window so "fresh" is inside but "old" is not.
	// now = t0+200s, cutoff(180s) = t0+20s → "fresh" (t0+5s) is outside too.
	// Use 200 s window: cutoff = t0 → "old" exactly on boundary (Before is strict).
	// Use 196 s window: cutoff = t0+4s → "fresh" (t0+5s) in, "old" (t0) out.
	clk.Advance(200 * time.Second)
	got := tr.OverallWindowedScore(196 * time.Second)
	const want = 1.0 // only "fresh" contributes; score = 1 healthy / 1 total
	if got != want {
		t.Errorf("OverallWindowedScore=%v, want %v (only fresh component contributes)", got, want)
	}
}
