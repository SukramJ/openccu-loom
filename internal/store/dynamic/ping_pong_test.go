// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"testing"
	"time"
)

// =============================================================================
// PongTracker tests
// =============================================================================

func TestPongTrackerAddContainsRemove(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()

	if pt.Contains("tok") {
		t.Fatal("empty tracker must not contain any token")
	}

	now := time.Now()
	pt.Add("tok", now)

	if !pt.Contains("tok") {
		t.Fatal("Contains should return true after Add")
	}
	if pt.Len() != 1 {
		t.Fatalf("Len=%d, want 1", pt.Len())
	}

	seenAt, ok := pt.SeenAt("tok")
	if !ok {
		t.Fatal("SeenAt should find inserted token")
	}
	if !seenAt.Equal(now) {
		t.Fatalf("SeenAt=%v, want %v", seenAt, now)
	}

	pt.Remove("tok")
	if pt.Contains("tok") {
		t.Fatal("Contains should return false after Remove")
	}
	if pt.Len() != 0 {
		t.Fatalf("Len=%d, want 0 after Remove", pt.Len())
	}
}

func TestPongTrackerLen(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	for i := range 5 {
		pt.Add(string(rune('A'+i)), time.Now())
	}
	if pt.Len() != 5 {
		t.Fatalf("Len=%d, want 5", pt.Len())
	}
}

func TestPongTrackerClear(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	pt.Add("x", time.Now())
	pt.SetLogged(true)

	pt.Clear()

	if pt.Len() != 0 {
		t.Fatalf("Len=%d after Clear, want 0", pt.Len())
	}
	if pt.Logged() {
		t.Fatal("logged flag should be reset by Clear")
	}
}

func TestPongTrackerLoggedFlag(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	if pt.Logged() {
		t.Fatal("logged should start false")
	}
	pt.SetLogged(true)
	if !pt.Logged() {
		t.Fatal("logged should be true after SetLogged(true)")
	}
	pt.SetLogged(false)
	if pt.Logged() {
		t.Fatal("logged should be false after SetLogged(false)")
	}
}

func TestPongTrackerTokensSnapshot(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	pt.Add("a", time.Now())
	pt.Add("b", time.Now())

	tokens := pt.Tokens()
	if len(tokens) != 2 {
		t.Fatalf("Tokens len=%d, want 2", len(tokens))
	}
	seen := make(map[string]bool)
	for _, tok := range tokens {
		seen[tok] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("Tokens did not include expected values, got %v", tokens)
	}
}

// =============================================================================
// PingPongEventType tests
// =============================================================================

func TestPingPongEventTypeValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    PingPongEventType
		want string
	}{
		{PingPongEventTypePingSent, "PING_SENT"},
		{PingPongEventTypePongReceived, "PONG_RECEIVED"},
		{PingPongEventTypePongUnknown, "PONG_UNKNOWN"},
		{PingPongEventTypePongExpired, "PONG_EXPIRED"},
	}
	for _, c := range cases {
		if string(c.v) != c.want {
			t.Errorf("PingPongEventType value %q, want %q", c.v, c.want)
		}
	}
}

// =============================================================================
// PingPongDiagJournal tests (, , )
// =============================================================================

func TestPingPongDiagJournalRecordPingSent(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10, MaxAge: time.Minute})
	j.RecordPingSent("token-123")

	events := j.GetRecentEvents(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0]["type"] != string(PingPongEventTypePingSent) {
		t.Errorf("event type=%v, want PING_SENT", events[0]["type"])
	}
}

func TestPingPongDiagJournalRecordPongReceived(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10, MaxAge: time.Minute})
	j.RecordPongReceived("tok", 42.5)

	events := j.GetRecentEvents(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0]["type"] != string(PingPongEventTypePongReceived) {
		t.Errorf("event type=%v, want PONG_RECEIVED", events[0]["type"])
	}
	if events[0]["rtt_ms"] != 42.5 {
		t.Errorf("rtt_ms=%v, want 42.5", events[0]["rtt_ms"])
	}
}

func TestPingPongDiagJournalRecordPongExpiredAndUnknown(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10, MaxAge: time.Minute})
	j.RecordPongExpired("e1")
	j.RecordPongUnknown("u1")

	events := j.GetRecentEvents(10)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0]["type"] != string(PingPongEventTypePongExpired) {
		t.Errorf("event[0] type=%v, want PONG_EXPIRED", events[0]["type"])
	}
	if events[1]["type"] != string(PingPongEventTypePongUnknown) {
		t.Errorf("event[1] type=%v, want PONG_UNKNOWN", events[1]["type"])
	}
}

func TestPingPongDiagJournalSizeEviction(t *testing.T) {
	t.Parallel()
	const maxEntries = 5
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: maxEntries, MaxAge: time.Hour})
	for range maxEntries + 3 {
		j.RecordPingSent("tok")
	}
	events := j.GetRecentEvents(100)
	if len(events) != maxEntries {
		t.Fatalf("journal size=%d after overflow, want %d (max_entries)", len(events), maxEntries)
	}
}

func TestPingPongDiagJournalCountEventsByType(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 50, MaxAge: time.Hour})
	j.RecordPingSent("a")
	j.RecordPingSent("b")
	j.RecordPongReceived("a", 10.0)

	pings := j.CountEventsByType(PingPongEventTypePingSent, 5)
	if pings != 2 {
		t.Fatalf("CountEventsByType(PING_SENT)=%d, want 2", pings)
	}
	pongs := j.CountEventsByType(PingPongEventTypePongReceived, 5)
	if pongs != 1 {
		t.Fatalf("CountEventsByType(PONG_RECEIVED)=%d, want 1", pongs)
	}
}

func TestPingPongDiagJournalGetSuccessRate(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 50, MaxAge: time.Hour})

	// No pings → 1.0 success rate (nothing to fail).
	if r := j.GetSuccessRate(5); r != 1.0 {
		t.Fatalf("GetSuccessRate with no pings=%v, want 1.0", r)
	}

	j.RecordPingSent("t1")
	j.RecordPingSent("t2")
	j.RecordPongReceived("t1", 5.0)

	// 1 pong / 2 pings = 0.5
	rate := j.GetSuccessRate(5)
	if rate < 0.49 || rate > 0.51 {
		t.Fatalf("GetSuccessRate=%v, want ~0.5", rate)
	}
}

func TestPingPongDiagJournalGetRTTStatistics(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 50, MaxAge: time.Hour})

	// No samples → zero stats.
	stats := j.GetRTTStatistics()
	if stats["samples"] != 0 {
		t.Fatalf("RTT samples before any records=%v, want 0", stats["samples"])
	}
	if stats["avg_ms"] != nil {
		t.Fatalf("avg_ms before records=%v, want nil", stats["avg_ms"])
	}

	j.RecordPongReceived("t1", 20.0)
	j.RecordPongReceived("t2", 40.0)

	stats = j.GetRTTStatistics()
	if stats["samples"] != 2 {
		t.Fatalf("samples=%v, want 2", stats["samples"])
	}
	if stats["min_ms"].(float64) < 19.9 || stats["min_ms"].(float64) > 20.1 {
		t.Fatalf("min_ms=%v, want ~20", stats["min_ms"])
	}
	if stats["max_ms"].(float64) < 39.9 || stats["max_ms"].(float64) > 40.1 {
		t.Fatalf("max_ms=%v, want ~40", stats["max_ms"])
	}
	if stats["avg_ms"].(float64) < 29.9 || stats["avg_ms"].(float64) > 30.1 {
		t.Fatalf("avg_ms=%v, want ~30", stats["avg_ms"])
	}
}

func TestPingPongDiagJournalGetDiagnostics(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10, MaxAge: time.Minute})
	j.RecordPingSent("t1")

	d := j.GetDiagnostics()
	if d["total_events"].(int) != 1 {
		t.Fatalf("total_events=%v, want 1", d["total_events"])
	}
	if d["max_entries"].(int) != 10 {
		t.Fatalf("max_entries=%v, want 10", d["max_entries"])
	}
}

func TestPingPongDiagJournalClear(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10, MaxAge: time.Hour})
	j.RecordPingSent("t1")
	j.RecordPongReceived("t1", 10.0)

	j.Clear()

	events := j.GetRecentEvents(100)
	if len(events) != 0 {
		t.Fatalf("events after Clear=%d, want 0", len(events))
	}
	stats := j.GetRTTStatistics()
	if stats["samples"] != 0 {
		t.Fatalf("RTT samples after Clear=%v, want 0", stats["samples"])
	}
}

func TestPingPongDiagJournalTokenTruncation(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10, MaxAge: time.Hour})
	// 25-char token: last 20 chars should appear in display.
	long := "12345abcdefghijklmnopq"
	j.RecordPingSent(long)

	events := j.GetRecentEvents(10)
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	tok := events[0]["token"].(string)
	if len(tok) > 20 {
		t.Fatalf("token not truncated: len=%d (>20), value=%q", len(tok), tok)
	}
	want := long[len(long)-20:]
	if tok != want {
		t.Fatalf("token=%q, want %q (last 20 chars)", tok, want)
	}
}

func TestPingPongDiagJournalRTTSamplesCappedAt50(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 200, MaxAge: time.Hour})
	for i := range 60 {
		j.RecordPongReceived("t", float64(i))
	}
	stats := j.GetRTTStatistics()
	if stats["samples"].(int) > 50 {
		t.Fatalf("RTT samples=%d, want <=50 (cap)", stats["samples"])
	}
}

// ---------------------------------------------------------------------------
// PingPongDiagJournal.Events — snapshot and copy semantics
// ---------------------------------------------------------------------------

func TestPingPongDiagJournalEvents(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10})
	if got := j.Events(); got != nil {
		t.Fatalf("Events on empty journal should be nil, got %v", got)
	}
	j.RecordPingSent("tok1")
	j.RecordPongReceived("tok2", 5.5)

	evts := j.Events()
	if len(evts) != 2 {
		t.Fatalf("Events len=%d want 2", len(evts))
	}
	if evts[0].EventType != PingPongEventTypePingSent {
		t.Errorf("evts[0].EventType=%v want PingSent", evts[0].EventType)
	}
	if evts[1].EventType != PingPongEventTypePongReceived {
		t.Errorf("evts[1].EventType=%v want PongReceived", evts[1].EventType)
	}
	// Returned slice is a copy — modifying it must not affect the journal.
	evts[0].Token = "MUTATED"
	orig := j.Events()
	if orig[0].Token == "MUTATED" {
		t.Error("Events returned a reference, not a copy")
	}
}

// ---------------------------------------------------------------------------
// PingPongDiagJournal.RTTSamples — snapshot and copy semantics
// ---------------------------------------------------------------------------

func TestPingPongDiagJournalRTTSamples(t *testing.T) {
	t.Parallel()
	j := NewPingPongDiagJournal(PingPongDiagJournalConfig{MaxEntries: 10})
	if got := j.RTTSamples(); got != nil {
		t.Fatalf("RTTSamples on empty journal should be nil, got %v", got)
	}
	j.RecordPongReceived("t1", 10.5)
	j.RecordPongReceived("t2", 20.0)

	samples := j.RTTSamples()
	if len(samples) != 2 {
		t.Fatalf("RTTSamples len=%d want 2", len(samples))
	}
	if samples[0] != 10.5 {
		t.Errorf("samples[0]=%v want 10.5", samples[0])
	}
	if samples[1] != 20.0 {
		t.Errorf("samples[1]=%v want 20.0", samples[1])
	}
	// Returned slice is a copy.
	samples[0] = 999
	orig := j.RTTSamples()
	if orig[0] == 999 {
		t.Error("RTTSamples returned a reference, not a copy")
	}
}

// ---------------------------------------------------------------------------
// PingPongDiagEvent.TimestampISO and ToMap
// ---------------------------------------------------------------------------

func TestPingPongDiagEventTimestampISO(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	e := PingPongDiagEvent{
		Timestamp: ts,
		EventType: PingPongEventTypePingSent,
		Token:     "tok1",
	}
	iso := e.TimestampISO()
	if iso == "" {
		t.Fatal("TimestampISO must not return empty string")
	}
	// Must be parseable as RFC3339.
	parsed, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t.Fatalf("TimestampISO returned non-RFC3339 string %q: %v", iso, err)
	}
	if !parsed.Equal(ts) {
		t.Errorf("TimestampISO round-trip: got %v want %v", parsed, ts)
	}
}

func TestPingPongDiagEventToMapIncludesTimestampISO(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 5, 2, 9, 30, 0, 0, time.UTC)
	e := PingPongDiagEvent{
		Timestamp: ts,
		EventType: PingPongEventTypePongReceived,
		Token:     "tok2",
		RTTms:     12.5,
	}
	m := e.ToMap()

	// Must contain timestamp_iso key.
	tsISO, ok := m["timestamp_iso"].(string)
	if !ok || tsISO == "" {
		t.Fatalf("ToMap must contain non-empty timestamp_iso, got %v", m["timestamp_iso"])
	}
	if len(tsISO) < 10 || tsISO[:10] != "2026-05-02" {
		t.Errorf("timestamp_iso=%q want a 2026-05-02 prefix", tsISO)
	}

	// Must still contain time, type, token, rtt_ms.
	if _, ok := m["time"]; !ok {
		t.Error("ToMap must contain 'time' key")
	}
	if m["type"] != string(PingPongEventTypePongReceived) {
		t.Errorf("ToMap type=%v want %v", m["type"], PingPongEventTypePongReceived)
	}
	if m["rtt_ms"] != 12.5 {
		t.Errorf("ToMap rtt_ms=%v want 12.5", m["rtt_ms"])
	}
}

func TestPingPongDiagEventToMapNonReceived(t *testing.T) {
	t.Parallel()
	e := PingPongDiagEvent{
		Timestamp: time.Now(),
		EventType: PingPongEventTypePingSent,
		Token:     "t",
	}
	m := e.ToMap()
	if _, hasRTT := m["rtt_ms"]; hasRTT {
		t.Error("ToMap must not include rtt_ms for non-PONG_RECEIVED events")
	}
	if _, hasISO := m["timestamp_iso"]; !hasISO {
		t.Error("ToMap must include timestamp_iso for all event types")
	}
}
