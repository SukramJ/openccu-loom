// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"testing"
	"time"
)

func TestPingPongJournalDefaultCapacity(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(0) // ≤ 0 → default 100
	// Fill 105 entries; only the last 100 should be retained.
	base := time.Now()
	for i := range 105 {
		j.RecordSent("iface", base.Add(time.Duration(i)*time.Millisecond))
	}

	snap := j.Snapshot()
	if len(snap) != 100 {
		t.Fatalf("snapshot len=%d want 100 (default capacity)", len(snap))
	}
}

func TestPingPongJournalRecordAckNoMatch(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(10)

	// Ack for an interface that has no sent ping at all.
	if j.RecordAck("UNKNOWN", time.Now()) {
		t.Fatal("RecordAck on empty journal must return false")
	}

	// Ack for the right interface but already acked.
	sent := time.Now()
	j.RecordSent("HmIP-RF", sent)
	j.RecordAck("HmIP-RF", sent.Add(10*time.Millisecond)) // first ack → true

	// Second ack for same interface — no unacked entry remains.
	if j.RecordAck("HmIP-RF", sent.Add(20*time.Millisecond)) {
		t.Fatal("second RecordAck must return false (entry already acked)")
	}
}

func TestPingPongJournalLatestNoEntries(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(10)

	if _, ok := j.Latest("NONE"); ok {
		t.Fatal("Latest on empty journal must return false")
	}

	// Sent but never acked.
	j.RecordSent("A", time.Now())
	if _, ok := j.Latest("A"); ok {
		t.Fatal("unacked ping must not appear in Latest")
	}

	// Wrong interface.
	j.RecordSent("B", time.Now())
	j.RecordAck("B", time.Now())
	if _, ok := j.Latest("C"); ok {
		t.Fatal("Latest for different interface must return false")
	}
}

func TestPingPongJournalStatsSingleInterface(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(20)
	base := time.Now()

	// Record 3 pings. RecordAck walks backwards (most-recent-unacked first),
	// so the first RecordAck pairs with the last ping, the second with the
	// second-last ping. The first ping remains pending.
	//
	// Ping at base → pending
	// Ping at base+10ms → acked at base+50ms → latency 40 ms
	// Ping at base+20ms → acked at base+35ms → latency 15 ms
	j.RecordSent("HmIP-RF", base)
	j.RecordSent("HmIP-RF", base.Add(10*time.Millisecond))
	j.RecordSent("HmIP-RF", base.Add(20*time.Millisecond))

	j.RecordAck("HmIP-RF", base.Add(35*time.Millisecond)) // pairs with base+20ms → latency 15 ms
	j.RecordAck("HmIP-RF", base.Add(50*time.Millisecond)) // pairs with base+10ms → latency 40 ms

	s := j.Stats("HmIP-RF")

	if s.Total != 3 {
		t.Errorf("Total=%d want 3", s.Total)
	}
	if s.Acked != 2 {
		t.Errorf("Acked=%d want 2", s.Acked)
	}
	if s.Pending != 1 {
		t.Errorf("Pending=%d want 1", s.Pending)
	}
	if s.MinRTT != 15*time.Millisecond {
		t.Errorf("MinRTT=%v want 15ms", s.MinRTT)
	}
	if s.MaxRTT != 40*time.Millisecond {
		t.Errorf("MaxRTT=%v want 40ms", s.MaxRTT)
	}
	want := (15*time.Millisecond + 40*time.Millisecond) / 2
	if s.AvgRTT != want {
		t.Errorf("AvgRTT=%v want %v", s.AvgRTT, want)
	}
}

func TestPingPongJournalStatsAggregate(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(20)
	base := time.Now()

	j.RecordSent("A", base)
	j.RecordSent("B", base.Add(5*time.Millisecond))
	j.RecordAck("A", base.Add(10*time.Millisecond))
	j.RecordAck("B", base.Add(20*time.Millisecond))

	s := j.Stats("") // aggregate
	if s.Total != 2 {
		t.Errorf("aggregate Total=%d want 2", s.Total)
	}
	if s.Acked != 2 {
		t.Errorf("aggregate Acked=%d want 2", s.Acked)
	}
	if s.Pending != 0 {
		t.Errorf("aggregate Pending=%d want 0", s.Pending)
	}
}

// TestPingPongJournalStatsFilterSkipsOtherInterfaces covers the inner
// continue branch in Stats when entries belonging to a different interface
// are present in the journal (interfaceID != "" and entry doesn't match).
func TestPingPongJournalStatsFilterSkipsOtherInterfaces(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(20)
	base := time.Now()

	// Two pings for "A", one for "B".
	j.RecordSent("A", base)
	j.RecordSent("A", base.Add(5*time.Millisecond))
	j.RecordSent("B", base.Add(10*time.Millisecond))

	j.RecordAck("A", base.Add(15*time.Millisecond)) // latest A → latency 10ms
	j.RecordAck("A", base.Add(20*time.Millisecond)) // earlier A → latency 15ms
	j.RecordAck("B", base.Add(25*time.Millisecond)) // B → latency 15ms

	// Stats for "A" only — the "B" entry must be skipped via the continue.
	s := j.Stats("A")
	if s.Total != 2 {
		t.Errorf("Total=%d want 2 (only A entries)", s.Total)
	}
	if s.Acked != 2 {
		t.Errorf("Acked=%d want 2", s.Acked)
	}
	if s.Pending != 0 {
		t.Errorf("Pending=%d want 0", s.Pending)
	}
}

func TestPingPongStatsSuccessRate(t *testing.T) {
	t.Parallel()

	// No pings recorded → 0.
	s := PingPongStats{}
	if s.SuccessRate() != 0 {
		t.Fatalf("SuccessRate()=%v want 0 when Total==0", s.SuccessRate())
	}

	// 2 acked out of 4 → 0.5.
	s = PingPongStats{Total: 4, Acked: 2}
	if got := s.SuccessRate(); got != 0.5 {
		t.Fatalf("SuccessRate()=%v want 0.5", got)
	}

	// All acked → 1.0.
	s = PingPongStats{Total: 3, Acked: 3}
	if got := s.SuccessRate(); got != 1.0 {
		t.Fatalf("SuccessRate()=%v want 1.0", got)
	}
}

func TestPingPongJournalClear(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(10)
	base := time.Now()
	j.RecordSent("X", base)
	j.RecordSent("X", base.Add(time.Millisecond))
	j.RecordAck("X", base.Add(2*time.Millisecond))

	if len(j.Snapshot()) == 0 {
		t.Fatal("journal must have entries before Clear")
	}

	j.Clear()

	if snap := j.Snapshot(); len(snap) != 0 {
		t.Fatalf("Snapshot len=%d after Clear, want 0", len(snap))
	}
	if _, ok := j.Latest("X"); ok {
		t.Fatal("Latest must return false after Clear")
	}
}

func TestPingPongJournalStatsEmpty(t *testing.T) {
	t.Parallel()

	j := NewPingPongJournal(10)
	s := j.Stats("ANY")

	if s.Total != 0 || s.Acked != 0 || s.Pending != 0 {
		t.Errorf("empty journal Stats=%+v want all zeros", s)
	}
	if s.AvgRTT != 0 {
		t.Errorf("AvgRTT=%v want 0 when no acked entries", s.AvgRTT)
	}
	if s.SuccessRate() != 0 {
		t.Errorf("SuccessRate=%v want 0 on empty journal", s.SuccessRate())
	}
}
