// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// buildRec is a test helper that constructs a slog.Record at the given level.
func buildRec(level slog.Level, msg string) slog.Record {
	return slog.NewRecord(time.Now(), level, msg, 0)
}

// --------------------------------------------------------------------------
// Ring eviction
// --------------------------------------------------------------------------

func TestLiveLog_RingEviction_KeepsLastN(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(3)
	for i := 0; i < 5; i++ {
		r := buildRec(slog.LevelInfo, "msg")
		l.record(r, nil)
	}
	got := l.Snapshot(0, slog.LevelDebug)
	if len(got) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(got))
	}
	// Must be oldest-first with monotonically increasing Seq 3,4,5.
	for i, rec := range got {
		want := uint64(3 + i)
		if rec.Seq != want {
			t.Errorf("got[%d].Seq = %d, want %d", i, rec.Seq, want)
		}
	}
}

// --------------------------------------------------------------------------
// Snapshot — limit + min_level filtering
// --------------------------------------------------------------------------

func TestLiveLog_Snapshot_LimitAndMinLevel(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(20)
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	for _, lvl := range levels {
		for i := 0; i < 3; i++ {
			l.record(buildRec(lvl, "msg"), nil)
		}
	}
	// Warn and above: 3 warn + 3 error = 6 total.
	got := l.Snapshot(0, slog.LevelWarn)
	for _, rec := range got {
		if rec.Level != "warn" && rec.Level != "error" {
			t.Errorf("unexpected level %q in warn+ snapshot", rec.Level)
		}
	}
	if len(got) != 6 {
		t.Errorf("len = %d, want 6", len(got))
	}
	// Limit to newest 2 of the warn+ set.
	limited := l.Snapshot(2, slog.LevelWarn)
	if len(limited) != 2 {
		t.Errorf("limited len = %d, want 2", len(limited))
	}
	// Must be chronological (oldest of the 2 first).
	if limited[0].Seq >= limited[1].Seq {
		t.Errorf("not chronological: seq %d then %d", limited[0].Seq, limited[1].Seq)
	}
}

// --------------------------------------------------------------------------
// Since
// --------------------------------------------------------------------------

func TestLiveLog_Since_ReturnsRecordsAfterSeq(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	for i := 0; i < 5; i++ {
		l.record(buildRec(slog.LevelInfo, "msg"), nil)
	}
	got := l.Since(3, slog.LevelDebug)
	if len(got) != 2 {
		t.Fatalf("Since(3) len = %d, want 2 (seq 4+5)", len(got))
	}
	for _, rec := range got {
		if rec.Seq <= 3 {
			t.Errorf("Since returned record with Seq = %d, want > 3", rec.Seq)
		}
	}
}

func TestLiveLog_Since_ZeroReturnsAll(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	for i := 0; i < 4; i++ {
		l.record(buildRec(slog.LevelInfo, "msg"), nil)
	}
	got := l.Since(0, slog.LevelDebug)
	if len(got) != 4 {
		t.Errorf("Since(0) len = %d, want 4", len(got))
	}
}

func TestLiveLog_Since_MinLevelFilters(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	l.record(buildRec(slog.LevelDebug, "d"), nil)
	l.record(buildRec(slog.LevelWarn, "w"), nil)
	got := l.Since(0, slog.LevelWarn)
	if len(got) != 1 || got[0].Level != "warn" {
		t.Errorf("Since with min=warn: got %v, want 1 warn record", got)
	}
}

// --------------------------------------------------------------------------
// LastSeq
// --------------------------------------------------------------------------

func TestLiveLog_LastSeq_Zero_WhenEmpty(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	if l.LastSeq() != 0 {
		t.Errorf("LastSeq on empty = %d, want 0", l.LastSeq())
	}
}

func TestLiveLog_LastSeq_IncrementsPerRecord(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	for i := 1; i <= 5; i++ {
		l.record(buildRec(slog.LevelInfo, "msg"), nil)
		if got := l.LastSeq(); got != uint64(i) {
			t.Errorf("after record %d: LastSeq = %d, want %d", i, got, i)
		}
	}
}

// --------------------------------------------------------------------------
// Subscribe
// --------------------------------------------------------------------------

func TestLiveLog_Subscribe_ReceivesRecordAfterSubscribe(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	ch, cancel := l.Subscribe(slog.LevelDebug)
	defer cancel()

	l.record(buildRec(slog.LevelInfo, "hello"), nil)
	select {
	case rec := <-ch:
		if rec.Msg != "hello" {
			t.Errorf("msg = %q, want hello", rec.Msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscribed record")
	}
}

func TestLiveLog_Subscribe_Cancel_ClosesChannel(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	ch, cancel := l.Subscribe(slog.LevelDebug)
	cancel()

	if l.Subscribers() != 0 {
		t.Errorf("Subscribers after cancel = %d, want 0", l.Subscribers())
	}
	// Channel must be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel still open after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel to close")
	}
}

func TestLiveLog_Subscribe_CancelPreventsSubsequentDelivery(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	ch, cancel := l.Subscribe(slog.LevelDebug)
	cancel()

	// Feed a record after cancel; it must not appear on the closed channel.
	l.record(buildRec(slog.LevelInfo, "after-cancel"), nil)

	// Drain the closed channel — must only see close, not the new record.
	for rec := range ch {
		if rec.Msg == "after-cancel" {
			t.Errorf("received record after cancel: %q", rec.Msg)
		}
	}
}

// --------------------------------------------------------------------------
// Subscribe — min_level filtering
// --------------------------------------------------------------------------

func TestLiveLog_Subscribe_MinLevel_DebugNotDeliveredToWarnSubscriber(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	ch, cancel := l.Subscribe(slog.LevelWarn)
	defer cancel()

	l.record(buildRec(slog.LevelDebug, "silent"), nil)

	select {
	case rec := <-ch:
		t.Errorf("got unexpected record %q on warn subscriber", rec.Msg)
	case <-time.After(50 * time.Millisecond):
		// Correct: nothing delivered.
	}
}

func TestLiveLog_Subscribe_MinLevel_WarnDeliveredToWarnSubscriber(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(10)
	ch, cancel := l.Subscribe(slog.LevelWarn)
	defer cancel()

	l.record(buildRec(slog.LevelWarn, "audible"), nil)

	select {
	case rec := <-ch:
		if rec.Msg != "audible" {
			t.Errorf("msg = %q, want audible", rec.Msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for warn record on warn subscriber")
	}
}

// --------------------------------------------------------------------------
// Send-or-drop: slow subscriber must not block record
// --------------------------------------------------------------------------

func TestLiveLog_SlowSubscriber_DoesNotBlockRecord(t *testing.T) {
	t.Parallel()
	l := NewLiveLog(1000)
	// Subscribe but never drain.
	_, cancel := l.Subscribe(slog.LevelDebug)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Feed more than the buffer depth (256) without draining.
		for i := 0; i < defaultLiveSubscriberBuffer+10; i++ {
			l.record(buildRec(slog.LevelInfo, "flood"), nil)
		}
	}()

	select {
	case <-done:
		// Feed loop returned promptly — no deadlock.
	case <-time.After(5 * time.Second):
		t.Fatal("feed loop blocked: slow subscriber is stalling record()")
	}
	if l.Subscribers() != 1 {
		t.Errorf("Subscribers = %d, want 1 (still attached)", l.Subscribers())
	}
}

// --------------------------------------------------------------------------
// TeeHandler integration — bound-attr accumulation feeds LiveLog
// --------------------------------------------------------------------------

func TestLiveLog_TeeHandler_BoundAttrsPopulateRecord(t *testing.T) {
	t.Parallel()
	live := NewLiveLog(10)
	tee := NewTeeHandler(slog.NewTextHandler(io.Discard, nil))
	tee.AttachLive(live)

	lg := slog.New(tee).
		With(slog.String("logger", "client.binrpc")).
		With(slog.String("central", "OttoGo"))
	lg.Info("hello", "elapsed_ms", int64(4))

	recs := live.Snapshot(0, slog.LevelDebug)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Logger != "client.binrpc" {
		t.Errorf("Logger = %q, want client.binrpc", rec.Logger)
	}
	if rec.Msg != "hello" {
		t.Errorf("Msg = %q, want hello", rec.Msg)
	}
	if rec.Attrs["central"] != "OttoGo" {
		t.Errorf("Attrs[central] = %v, want OttoGo", rec.Attrs["central"])
	}
	if rec.Attrs["elapsed_ms"] != int64(4) {
		t.Errorf("Attrs[elapsed_ms] = %v (%T), want int64(4)", rec.Attrs["elapsed_ms"], rec.Attrs["elapsed_ms"])
	}
}
