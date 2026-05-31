// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package audit

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// DefaultMaxEntriesPerSession is the FIFO cap per session.
const DefaultMaxEntriesPerSession = 500

// ChangeEntry records one MASTER-paramset change made during a UI
// configuration session.
type ChangeEntry struct {
	Timestamp      time.Time              `json:"timestamp"`
	EntryID        string                 `json:"entry_id"`
	InterfaceID    string                 `json:"interface_id"`
	ChannelAddress string                 `json:"channel_address"`
	DeviceName     string                 `json:"device_name"`
	DeviceModel    string                 `json:"device_model"`
	ParamsetKey    string                 `json:"paramset_key"`
	Changes        map[string]ParamChange `json:"changes"`
	Source         string                 `json:"source"`
}

// ParamChange holds the before/after values for a single parameter
// within a ChangeEntry. Mirrors the inner dict {"old": ..., "new": ...}
type ParamChange struct {
	Old any `json:"old,omitempty"`
	New any `json:"new,omitempty"`
}

// ChangeLog is a session-scoped, in-memory FIFO of ChangeEntry slices.
// Concurrent-safe. Sessions are identified by an opaque string key
// (typically a UUID from the UI/REST layer); the key corresponds to
// Entry_id in 's ConfigChangeLog.
//
// The optional MaxEntries field caps each session's slice to prevent
// unbounded growth; oldest entries are dropped when the cap is hit.
type ChangeLog struct {
	mu         sync.RWMutex
	sessions   map[string][]ChangeEntry
	maxEntries int
	clk        clock.Clock
}

// NewChangeLog constructs an empty ChangeLog with the default per-session
// cap (DefaultMaxEntriesPerSession).
func NewChangeLog() *ChangeLog {
	return NewChangeLogCapped(DefaultMaxEntriesPerSession)
}

// NewChangeLogCapped constructs an empty ChangeLog with a custom
// per-session cap. Values < 1 fall back to DefaultMaxEntriesPerSession.
func NewChangeLogCapped(maxEntries int) *ChangeLog {
	return NewChangeLogCappedWithClock(maxEntries, clock.New())
}

// NewChangeLogCappedWithClock is the test seam: pass a [clock.Fake]
// to make change-log timestamps deterministic. Nil clk falls back to
// [clock.New].
func NewChangeLogCappedWithClock(maxEntries int, clk clock.Clock) *ChangeLog {
	if maxEntries < 1 {
		maxEntries = DefaultMaxEntriesPerSession
	}
	if clk == nil {
		clk = clock.New()
	}
	return &ChangeLog{
		sessions:   make(map[string][]ChangeEntry),
		maxEntries: maxEntries,
		clk:        clk,
	}
}

// Add appends e to the session's slice. New sessions are auto-created.
// If the session already has maxEntries entries, the oldest one is
// dropped (FIFO) before appending.
func (l *ChangeLog) Add(sessionID string, e ChangeEntry) ChangeEntry {
	if e.Timestamp.IsZero() {
		e.Timestamp = l.clk.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := append(l.sessions[sessionID], e) //nolint:gocritic // intentional append into slice copy
	if len(entries) > l.maxEntries {
		entries = entries[len(entries)-l.maxEntries:]
	}
	l.sessions[sessionID] = entries
	return e
}

// GetEntries returns entries for sessionID, optionally filtered by
// channelAddress prefix (empty = no filter), most-recent-first, up to
// limit entries. Also returns the total number of matching entries
// Before the limit is applied (mirrors 's
// get_entries return signature).
//
// An unknown sessionID returns (nil, 0, false).
func (l *ChangeLog) GetEntries(sessionID, channelAddress string, limit int) ([]ChangeEntry, int, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	raw, ok := l.sessions[sessionID]
	if !ok {
		return nil, 0, false
	}
	// Filter by channel address prefix when requested.
	filtered := raw
	if channelAddress != "" {
		filtered = make([]ChangeEntry, 0, len(raw))
		for i := range raw {
			if len(raw[i].ChannelAddress) >= len(channelAddress) &&
				raw[i].ChannelAddress[:len(channelAddress)] == channelAddress {
				filtered = append(filtered, raw[i])
			}
		}
	}
	total := len(filtered)
	if limit <= 0 || limit > total {
		limit = total
	}
	// Newest-first: reverse the last `limit` entries.
	tail := filtered[total-limit:]
	out := make([]ChangeEntry, limit)
	for i := range tail {
		out[limit-1-i] = tail[i]
	}
	return out, total, true
}

// ClearByEntryID removes all entries whose EntryID equals entryID from
// SessionID's slice. It mirrors 's
// clear_by_entry_id. Returns the number of entries removed.
func (l *ChangeLog) ClearByEntryID(sessionID, entryID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, ok := l.sessions[sessionID]
	if !ok {
		return 0
	}
	filtered := raw[:0]
	for i := range raw {
		if raw[i].EntryID != entryID {
			filtered = append(filtered, raw[i])
		}
	}
	removed := len(raw) - len(filtered)
	l.sessions[sessionID] = filtered
	return removed
}

// Discard releases all entries for sessionID. Idempotent.
func (l *ChangeLog) Discard(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessions, sessionID)
}

// Sessions returns the active session IDs (for diagnostics). Order is
// unspecified.
func (l *ChangeLog) Sessions() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.sessions))
	for id := range l.sessions {
		ids = append(ids, id)
	}
	return ids
}

// BuildChangeDiff computes the per-parameter before/after map from oldValues
// and newValues. Only parameters whose value changed are included.
func BuildChangeDiff(oldValues, newValues map[string]any) map[string]ParamChange {
	diff := make(map[string]ParamChange)
	for param, newVal := range newValues {
		if oldVal, ok := oldValues[param]; !ok || oldVal != newVal {
			diff[param] = ParamChange{Old: oldVal, New: newVal}
		}
	}
	return diff
}
