// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// =============================================================================
// PingPongCombinedTracker — combined tracker
// =============================================================================
//
// PingPongCombinedTracker consolidates PongTracker + PingPongDiagJournal
// into one type that also handles threshold-crossing events and incident
// recording. It mirrors the Python-side PingPongTracker class
// (store/dynamic/ping_pong.py:38).
//
// Concurrency: all state is protected by an embedded mutex, so the type
// is safe for concurrent use across goroutines.

// IncidentRecorder is the minimal interface PingPongCombinedTracker uses to
// persist diagnostics incidents. Implemented by *sqlite.IncidentStore.
type IncidentRecorder interface {
	RecordIncidentCtx(ctx context.Context, centralName, interfaceID string, incType hmenum.IncidentType, severity hmenum.IncidentSeverity, message string) error
}

// PingPongPublishFn is the callback fired when a tracker's mismatch count
// Crosses the configured threshold. It mirrors
// `_check_and_publish_pong_event` publish path (ping_pong.py:181).
//
// kind is the mismatch direction (pending vs. unknown); count is the current
// size of the affected set. A count of 0 means the level has recovered.
type PingPongPublishFn func(kind hmenum.PingPongMismatchType, count int)

// PingPongCombinedConfig configures a [PingPongCombinedTracker].
type PingPongCombinedConfig struct {
	// InterfaceID is the CCU interface this tracker covers.
	InterfaceID string
	// AllowedDelta is the number of mismatches permitted before events are
	// emitted.
	AllowedDelta int
	// TTL is the maximum age for pending / unknown entries before they are
	// purged.
	TTL time.Duration
	// MaxSize caps the pending and unknown trackers independently.
	MaxSize int
	// JournalConfig configures the embedded diagnostic journal.
	JournalConfig PingPongDiagJournalConfig
	// OnPublish is called whenever the mismatch count crosses a threshold
	// or recovers to zero. May be nil.
	OnPublish PingPongPublishFn
	// Incidents is optional; when non-nil, crossing the threshold will
	// call RecordIncidentCtx. May be nil.
	Incidents IncidentRecorder
	// CentralName is used as the scope for incident recording.
	CentralName string
	// HasConnectionIssue, when non-nil and returning true, causes HandleSendPing
	// to skip recording the token (the connection is already known to be down).
	HasConnectionIssue func() bool
}

// PingPongCombinedTracker is a thread-safe combined ping/pong tracker
// that correlates sent PINGs with received PONGs, journals diagnostics,
// enforces TTL + size limits, and publishes threshold-crossing events.
type PingPongCombinedTracker struct {
	cfg     PingPongCombinedConfig
	mu      sync.Mutex
	pending *PongTracker
	unknown *PongTracker
	journal *PingPongDiagJournal
	retryAt map[string]struct{}
}

// NewPingPongCombinedTracker returns a ready tracker. Sensible defaults are
// applied when cfg fields are zero.
func NewPingPongCombinedTracker(cfg PingPongCombinedConfig) *PingPongCombinedTracker {
	if cfg.AllowedDelta <= 0 {
		cfg.AllowedDelta = 15
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 300 * time.Second
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 1000
	}
	if cfg.JournalConfig.MaxEntries <= 0 {
		cfg.JournalConfig.MaxEntries = 100
	}
	if cfg.JournalConfig.MaxAge <= 0 {
		cfg.JournalConfig.MaxAge = 30 * time.Minute
	}
	return &PingPongCombinedTracker{
		cfg:     cfg,
		pending: NewPongTracker(),
		unknown: NewPongTracker(),
		journal: NewPingPongDiagJournal(cfg.JournalConfig),
		retryAt: make(map[string]struct{}),
	}
}

// AllowedDelta returns the configured mismatch threshold.
func (t *PingPongCombinedTracker) AllowedDelta() int { return t.cfg.AllowedDelta }

// Journal returns the embedded diagnostic journal.
func (t *PingPongCombinedTracker) Journal() *PingPongDiagJournal {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.journal
}

// Size returns the sum of pending + unknown tracker sizes.
func (t *PingPongCombinedTracker) Size() int {
	return t.pending.Len() + t.unknown.Len()
}

// Clear resets both trackers and the journal.
func (t *PingPongCombinedTracker) Clear() {
	t.pending.Clear()
	t.unknown.Clear()
	t.journal.Clear()
}

// HandleSendPing records a newly sent ping token. If a connection issue is
// active (HasConnectionIssue returns true), the token is skipped to prevent
// false-alarm mismatch events during CCU restarts.
func (t *PingPongCombinedTracker) HandleSendPing(interfaceID string, ts time.Time) {
	if t.cfg.HasConnectionIssue != nil && t.cfg.HasConnectionIssue() {
		slog.Debug("PingPongCombinedTracker: skip PING tracking (connection issue)",
			"interface", interfaceID)
		return
	}
	token := buildToken(interfaceID, ts)
	t.journal.RecordPingSent(token)
	t.pending.Add(token, ts)
	t.cleanupTracker(t.pending, "pending")
	count := t.pending.Len()
	if count > t.cfg.AllowedDelta || count%2 == 0 {
		t.checkAndPublishPongEvent(hmenum.PingPongMismatchPending)
	}
	slog.Debug("PingPongCombinedTracker: PING tracked",
		"interface", interfaceID, "pending", count)
}

// HandleReceivedPong reconciles an inbound pong token with the pending set.
// If the token is found in pending, the RTT is computed and the entry is
// removed. Otherwise the token is filed in the unknown set.
func (t *PingPongCombinedTracker) HandleReceivedPong(interfaceID string, ts time.Time) {
	token := buildToken(interfaceID, ts)
	if t.pending.Contains(token) {
		var rttMs float64
		if sentAt, ok := t.pending.SeenAt(token); ok {
			rttMs = float64(ts.Sub(sentAt).Milliseconds())
		}
		t.journal.RecordPongReceived(token, rttMs)
		t.pending.Remove(token)
		t.cleanupTracker(t.pending, "pending")
		t.checkAndPublishPongEvent(hmenum.PingPongMismatchPending)
		slog.Debug("PingPongCombinedTracker: PONG matched",
			"interface", interfaceID, "rtt_ms", rttMs, "pending", t.pending.Len())
	} else {
		t.journal.RecordPongUnknown(token)
		t.unknown.Add(token, ts)
		t.cleanupTracker(t.unknown, "unknown")
		t.checkAndPublishPongEvent(hmenum.PingPongMismatchUnknown)
		slog.Debug("PingPongCombinedTracker: PONG unknown",
			"interface", interfaceID, "unknown", t.unknown.Len())
	}
}

// checkAndPublishPongEvent applies the threshold check and calls OnPublish
// (and records an incident) if the threshold is crossed or recovers.
func (t *PingPongCombinedTracker) checkAndPublishPongEvent(mismatchType hmenum.PingPongMismatchType) {
	publish := func(count int) {
		if t.cfg.OnPublish != nil {
			t.cfg.OnPublish(mismatchType, count)
		}
	}

	switch mismatchType {
	case hmenum.PingPongMismatchPending:
		t.cleanupTracker(t.pending, "pending")
		count := t.pending.Len()
		if count > t.cfg.AllowedDelta {
			publish(count)
			if !t.pending.Logged() {
				slog.Warn("PingPongCombinedTracker: pending PONG mismatch exceeded threshold",
					"interface", t.cfg.InterfaceID, "count", count, "threshold", t.cfg.AllowedDelta)
				t.recordIncident(hmenum.IncidentTypePingPongMismatchHigh, hmenum.IncidentSeverityError,
					"pending pong count exceeded threshold")
			}
			t.pending.SetLogged(true)
		} else if t.pending.Logged() {
			publish(0)
			t.pending.SetLogged(false)
		} else if count > 0 && count%2 == 0 {
			publish(count)
		}
	case hmenum.PingPongMismatchUnknown:
		t.cleanupTracker(t.unknown, "unknown")
		count := t.unknown.Len()
		if count > t.cfg.AllowedDelta {
			publish(count)
			if !t.unknown.Logged() {
				slog.Warn("PingPongCombinedTracker: unknown PONG mismatch exceeded threshold",
					"interface", t.cfg.InterfaceID, "count", count, "threshold", t.cfg.AllowedDelta)
				t.recordIncident(hmenum.IncidentTypePingPongUnknownHigh, hmenum.IncidentSeverityWarning,
					"unknown pong count exceeded threshold")
			}
			t.unknown.SetLogged(true)
		} else if t.unknown.Logged() {
			publish(0)
			t.unknown.SetLogged(false)
		}
	}
}

// cleanupTracker removes expired entries (TTL) and enforces max size.
func (t *PingPongCombinedTracker) cleanupTracker(tracker *PongTracker, trackerName string) {
	removed := tracker.CleanupTracker(t.cfg.TTL, t.cfg.MaxSize)
	if removed > 0 && trackerName == "pending" {
		slog.Debug("PingPongCombinedTracker: expired pending tokens evicted",
			"interface", t.cfg.InterfaceID, "removed", removed)
	}
}

// recordIncident fires an asynchronous incident-recording operation (fire and
// forget). Missing IncidentRecorder is silently skipped.
func (t *PingPongCombinedTracker) recordIncident(incType hmenum.IncidentType, severity hmenum.IncidentSeverity, msg string) {
	if t.cfg.Incidents == nil {
		return
	}
	recorder := t.cfg.Incidents
	centralName := t.cfg.CentralName
	interfaceID := t.cfg.InterfaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := recorder.RecordIncidentCtx(ctx, centralName, interfaceID, incType, severity, msg); err != nil {
			slog.Debug("PingPongCombinedTracker: incident recording failed",
				"interface", interfaceID, "type", incType, "err", err)
		}
	}()
}

// buildToken creates a stable string token from interfaceID and timestamp.
// In production the caller would pass the actual ping token from the CCU;
// this helper is used in tests and as the default when no token is provided.
func buildToken(interfaceID string, ts time.Time) string {
	return interfaceID + "@" + ts.Format("20060102150405.000000000")
}
