// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"slices"
	"sync"
	"time"
)

// PingPongEntry is one recorded round-trip.
type PingPongEntry struct {
	InterfaceID string
	SentAt      time.Time
	AckedAt     time.Time // zero until the pong arrived
	Latency     time.Duration
}

// PingPongJournal keeps a rolling window of ping/pong pairs. The
// health tracker reads [Latest] to decide whether a backend is
// keepalive-healthy.
type PingPongJournal struct {
	capacity int
	mu       sync.RWMutex
	items    []PingPongEntry
}

// NewPingPongJournal constructs a journal with the given capacity
// (number of entries kept per interface across the whole daemon).
func NewPingPongJournal(capacity int) *PingPongJournal {
	if capacity <= 0 {
		capacity = 100
	}
	return &PingPongJournal{capacity: capacity}
}

// RecordSent logs a ping send; caller must later call [RecordAck]
// with the matching interfaceID + SentAt.
func (j *PingPongJournal) RecordSent(interfaceID string, at time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.items = append(j.items, PingPongEntry{InterfaceID: interfaceID, SentAt: at})
	if len(j.items) > j.capacity {
		j.items = j.items[len(j.items)-j.capacity:]
	}
}

// RecordAck pairs a pong with the most recent unacked ping for the
// same interface. Returns true when a matching ping was found.
func (j *PingPongJournal) RecordAck(interfaceID string, at time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range slices.Backward(j.items) {
		if j.items[i].InterfaceID != interfaceID || !j.items[i].AckedAt.IsZero() {
			continue
		}
		j.items[i].AckedAt = at
		j.items[i].Latency = at.Sub(j.items[i].SentAt)
		return true
	}
	return false
}

// Latest returns the last acked entry for interfaceID or the zero
// [PingPongEntry] when none is recorded.
func (j *PingPongJournal) Latest(interfaceID string) (PingPongEntry, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	for _, v := range slices.Backward(j.items) {
		if v.InterfaceID == interfaceID && !v.AckedAt.IsZero() {
			return v, true
		}
	}
	return PingPongEntry{}, false
}

// Snapshot returns a copy of every entry.
func (j *PingPongJournal) Snapshot() []PingPongEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]PingPongEntry, len(j.items))
	copy(out, j.items)
	return out
}

// PingPongStats summarises the journal for one interface.
type PingPongStats struct {
	InterfaceID string
	// Total is the number of recorded pings (acked + pending).
	Total int
	// Acked is the number of pings with a matching pong.
	Acked int
	// Pending is the number of pings without a pong yet.
	Pending int
	// MinRTT / MaxRTT / AvgRTT cover only acked entries. Zero when
	// Acked == 0.
	MinRTT time.Duration
	MaxRTT time.Duration
	AvgRTT time.Duration
}

// SuccessRate returns acked / total in [0, 1]. Returns 0 when no
// pings have been recorded.
func (s PingPongStats) SuccessRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Acked) / float64(s.Total)
}

// Stats returns RTT-statistics + success rate for `interfaceID`. Pass
// "" to get aggregate stats over every recorded interface.
func (j *PingPongJournal) Stats(interfaceID string) PingPongStats {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := PingPongStats{InterfaceID: interfaceID}
	var sum time.Duration
	for _, e := range j.items {
		if interfaceID != "" && e.InterfaceID != interfaceID {
			continue
		}
		out.Total++
		if e.AckedAt.IsZero() {
			out.Pending++
			continue
		}
		out.Acked++
		if out.MinRTT == 0 || e.Latency < out.MinRTT {
			out.MinRTT = e.Latency
		}
		if e.Latency > out.MaxRTT {
			out.MaxRTT = e.Latency
		}
		sum += e.Latency
	}
	if out.Acked > 0 {
		out.AvgRTT = sum / time.Duration(out.Acked)
	}
	return out
}

// Clear drops every recorded entry. Used during reconnect to reset health
// metrics.
func (j *PingPongJournal) Clear() {
	j.mu.Lock()
	j.items = nil
	j.mu.Unlock()
}
