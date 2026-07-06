// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package session provides the SessionRecorder, which captures RPC
// method calls and their responses for deterministic golden-file
// Replay tests. It is the Go equivalent.
// `store/persistent/session.py:SessionRecorder`.
//
// Data structure:
//
//	store[rpcType][method][frozenParams] = Entry{response, recordedAt}
//
// TTL mechanism:
// - Each entry carries the timestamp it was recorded at.
// - Entries expire after TTL seconds (0 = no expiry).
// - Expiration is lazy: checked on access, not via background task.
//
// Thread safety: all exported methods on [Recorder] are safe for
// concurrent use.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// RPCType identifies the wire protocol that produced a session entry.
type RPCType string

// RPCType values.
const (
	// RPCTypeXML is the XML-RPC protocol.
	RPCTypeXML RPCType = "xml"
	// RPCTypeJSON is the JSON-RPC protocol.
	RPCTypeJSON RPCType = "json"
	// RPCTypeBIN is the BIN-RPC protocol (CUxD). Kept distinct from XML
	// so a replay can tell the two transports apart.
	RPCTypeBIN RPCType = "bin"
)

// Entry is one recorded RPC request/response pair.
type Entry struct {
	// Response holds the decoded RPC response (or an error excerpt).
	// nil is valid when the CCU returned no result.
	Response any
	// RecordedAt is the wall-clock time the entry was captured.
	RecordedAt time.Time
}

// key is the composite bucket key for one recorded call.
type key struct {
	rpcType RPCType
	method  string
	params  string // frozen (stable string representation of params)
}

// freezeParams converts arbitrary params to a deterministic string key.
// In Go we use fmt.Sprintf("%#v") which is stable and reasonably deterministic
// for the supported param types (nil, bool, int, float64, string, []any, map[string]any).
func freezeParams(params any) string {
	return fmt.Sprintf("%#v", params)
}

// Recorder is a thread-safe cache of RPC call/response pairs intended
// for test session capture and replay.
type Recorder struct {
	mu sync.Mutex

	// active controls whether new entries are accepted.
	active bool
	// isRecording is true while a timed activation is in progress.
	isRecording bool
	// ttl is the entry time-to-live. Zero means entries never expire.
	ttl time.Duration
	// refreshOnGet extends an entry's effective TTL when it is read.
	refreshOnGet bool

	// store[rpcType][method][frozenParams] = []Entry (multiple
	// timestamps for the same params when refresh-on-get is used).
	store map[key][]Entry
}

// Config groups the constructor options for Recorder.
type Config struct {
	// Active controls whether the recorder accepts new entries at
	// construction time.
	Active bool
	// TTL is the entry time-to-live; 0 means no expiry.
	TTL time.Duration
	// RefreshOnGet extends an entry's TTL when it is read.
	RefreshOnGet bool
}

// New returns a ready-to-use Recorder.
func New(cfg Config) *Recorder {
	return &Recorder{
		active:       cfg.Active,
		ttl:          cfg.TTL,
		refreshOnGet: cfg.RefreshOnGet,
		store:        make(map[key][]Entry),
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// SetTTL changes the entry time-to-live. A recording that should capture a
// complete session (rather than the default rolling 600 s window) raises the
// TTL to 0 (no expiry) while active and restores it on stop. Existing entries
// are re-evaluated against the new TTL on the next read/cleanup.
func (r *Recorder) SetTTL(ttl time.Duration) {
	r.mu.Lock()
	r.ttl = ttl
	r.mu.Unlock()
}

// StartSession activates the recorder and clears any existing data.
// Returns false if already recording (idempotent on same active state).
func (r *Recorder) StartSession() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isRecording {
		return false
	}
	r.store = make(map[key][]Entry)
	r.active = true
	return true
}

// Resume re-activates the recorder WITHOUT clearing the store, so entries
// restored from persistence (or still held in memory) survive while recording
// continues. Used when a recording is carried over a daemon restart, as
// opposed to [Recorder.StartSession] which begins a fresh session.
func (r *Recorder) Resume() {
	r.mu.Lock()
	r.active = true
	r.mu.Unlock()
}

// StopSession deactivates the recorder. Returns false if already stopped.
func (r *Recorder) StopSession() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active && !r.isRecording {
		return false
	}
	r.active = false
	r.isRecording = false
	return true
}

// IsActive reports whether the recorder will accept new entries.
func (r *Recorder) IsActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// =============================================================================
// Record
// =============================================================================

// RecordRequest records the request half of an RPC call. rpcType is "xml" or
// "json"; method is the RPC method name; params is the method arguments (any
// JSON-serialisable value). Returns immediately without blocking when the
// recorder is inactive.
func (r *Recorder) RecordRequest(rpcType RPCType, method string, params any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	// A request without a response is stored with a nil Response.
	// RecordResponse will overwrite this when the response arrives.
	k := key{rpcType: rpcType, method: method, params: freezeParams(params)}
	r.purgeExpiredLocked(k)
	r.store[k] = append(r.store[k], Entry{Response: nil, RecordedAt: time.Now()})
}

// RecordResponse associates a response (or error excerpt) with the
// most recently recorded request for (rpcType, method, params).
func (r *Recorder) RecordResponse(rpcType RPCType, method string, params, response any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	k := key{rpcType: rpcType, method: method, params: freezeParams(params)}
	r.purgeExpiredLocked(k)
	now := time.Now()
	entries := r.store[k]
	// If the last entry has a nil Response (i.e. we recorded the
	// request already), update it in-place.
	if len(entries) > 0 && entries[len(entries)-1].Response == nil {
		entries[len(entries)-1].Response = response
		entries[len(entries)-1].RecordedAt = now
		r.store[k] = entries
		return
	}
	r.store[k] = append(entries, Entry{Response: response, RecordedAt: now})
}

// =============================================================================
// Lookup
// =============================================================================

// GetSessions returns all non-expired (rpcType, method) pairs. The
// outer key is "<rpcType>/<method>".
func (r *Recorder) GetSessions() map[string][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]string)
	for k := range r.store {
		bucket := string(k.rpcType) + "/" + k.method
		out[bucket] = append(out[bucket], k.params)
	}
	return out
}

// GetSession returns all non-expired entries for (rpcType, method).
func (r *Recorder) GetSession(rpcType RPCType, method string) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Entry
	for k := range r.store {
		if k.rpcType != rpcType || k.method != method {
			continue
		}
		r.purgeExpiredLocked(k)
		if latest := r.latestEntryLocked(k); latest != nil {
			out = append(out, *latest)
		}
	}
	return out
}

// Get returns the most recent non-expired response for the given call
// triple, or (nil, false) when no entry matches.
func (r *Recorder) Get(rpcType RPCType, method string, params any) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{rpcType: rpcType, method: method, params: freezeParams(params)}
	r.purgeExpiredLocked(k)
	e := r.latestEntryLocked(k)
	if e == nil {
		return nil, false
	}
	if r.refreshOnGet {
		r.store[k] = append(r.store[k], Entry{Response: e.Response, RecordedAt: time.Now()})
	}
	return e.Response, true
}

// ExportSession returns all non-expired entries for (rpcType, method)
// as a list of (params, response) pairs. Suitable for serialising to
// a golden file.
func (r *Recorder) ExportSession(rpcType RPCType, method string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for k, entries := range r.store {
		if k.rpcType != rpcType || k.method != method {
			continue
		}
		r.purgeExpiredLocked(k)
		if len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		out = append(out, map[string]any{
			"params":      k.params,
			"response":    latest.Response,
			"recorded_at": latest.RecordedAt.Format(time.RFC3339),
		})
	}
	return out
}

// ClearSessions removes all stored data.
func (r *Recorder) ClearSessions() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = make(map[key][]Entry)
}

// Delete removes all entries for (rpcType, method, params).
// Returns true when at least one entry was found and removed.
func (r *Recorder) Delete(rpcType RPCType, method string, params any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{rpcType: rpcType, method: method, params: freezeParams(params)}
	_, exists := r.store[k]
	if exists {
		delete(r.store, k)
	}
	return exists
}

// PeekTS returns the recorded-at timestamp of the most recent entry for
// (rpcType, method, params) without consuming or modifying the entry.
// Returns (zero time, false) when no matching entry exists.
func (r *Recorder) PeekTS(rpcType RPCType, method string, params any) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{rpcType: rpcType, method: method, params: freezeParams(params)}
	r.purgeExpiredLocked(k)
	e := r.latestEntryLocked(k)
	if e == nil {
		return time.Time{}, false
	}
	return e.RecordedAt, true
}

// GetLatestResponseByParams returns the most recent non-expired response for
// (rpcType, method) whose frozen params string contains the given params
// substring. It mirrors the Python reference implementation's
// SessionRecorder.get_latest_response_by_params (session.py).
// Returns (nil, false) when no matching entry exists.
func (r *Recorder) GetLatestResponseByParams(rpcType RPCType, method, paramsSubstr string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var latestEntry *Entry
	var latestKey key

	for k := range r.store {
		if k.rpcType != rpcType || k.method != method {
			continue
		}
		if paramsSubstr != "" && !strings.Contains(k.params, paramsSubstr) {
			continue
		}
		r.purgeExpiredLocked(k)
		e := r.latestEntryLocked(k)
		if e == nil {
			continue
		}
		if latestEntry == nil || e.RecordedAt.After(latestEntry.RecordedAt) {
			latestEntry = e
			latestKey = k
		}
	}
	_ = latestKey // satisfies staticcheck; key retained for future use
	if latestEntry == nil {
		return nil, false
	}
	return latestEntry.Response, true
}

// =============================================================================
// Replay
// =============================================================================

// ReplaySession looks up the response for (rpcType, method, params)
// and returns it. Returns (nil, false) on cache miss. Intended for
// use in golden-file replay test helpers.
func (r *Recorder) ReplaySession(rpcType RPCType, method string, params any) (any, bool) {
	return r.Get(rpcType, method, params)
}

// Metadata returns a summary of the current session state.
func (r *Recorder) Metadata() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked()
	methods := make(map[string]int)
	for k := range r.store {
		methods[string(k.rpcType)+"/"+k.method]++
	}
	return map[string]any{
		"active":           r.active,
		"is_recording":     r.isRecording,
		"ttl_seconds":      r.ttl.Seconds(),
		"refresh_on_get":   r.refreshOnGet,
		"total_entries":    len(r.store),
		"methods_recorded": methods,
	}
}

// =============================================================================
// File-based backend
// =============================================================================

// SerializeToMap converts the entire live (non-expired) store to a
// JSON-serialisable map. Suitable for writing to a golden-file backend.
func (r *Recorder) SerializeToMap() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked()
	out := make(map[string]any, len(r.store))
	for k, entries := range r.store {
		if len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		slotKey := strings.Join([]string{string(k.rpcType), k.method, k.params}, "|")
		out[slotKey] = map[string]any{
			"rpc_type":    string(k.rpcType),
			"method":      k.method,
			"params":      k.params,
			"response":    latest.Response,
			"recorded_at": latest.RecordedAt.Unix(),
		}
	}
	return out
}

// GoldenRecord is one entry in the golden-fixture export — a flat,
// ordered representation suitable as direct replay input, as opposed to
// the keyed map of [Recorder.SerializeToMap].
type GoldenRecord struct {
	RPCType    string `json:"rpc_type"`
	Method     string `json:"method"`
	Params     string `json:"params"`
	Response   any    `json:"response"`
	RecordedAt int64  `json:"recorded_at"`
}

// SerializeToGolden converts the live (non-expired) store to an ordered
// slice of [GoldenRecord]. The order is deterministic (rpc_type, method,
// params) so two exports of the same session compare equal. This is the
// `format=golden` download shape; [Recorder.SerializeToMap] is `format=map`.
func (r *Recorder) SerializeToGolden() []GoldenRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked()
	out := make([]GoldenRecord, 0, len(r.store))
	for k, entries := range r.store {
		if len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		out = append(out, GoldenRecord{
			RPCType:    string(k.rpcType),
			Method:     k.method,
			Params:     k.params,
			Response:   latest.Response,
			RecordedAt: latest.RecordedAt.Unix(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RPCType != out[j].RPCType {
			return out[i].RPCType < out[j].RPCType
		}
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		return out[i].Params < out[j].Params
	})
	return out
}

// =============================================================================
// Internal helpers
// =============================================================================

// purgeExpiredLocked removes expired entries for k.
// Must be called with r.mu held.
func (r *Recorder) purgeExpiredLocked(k key) {
	if r.ttl == 0 {
		return
	}
	entries, ok := r.store[k]
	if !ok {
		return
	}
	cutoff := time.Now().Add(-r.ttl)
	kept := entries[:0]
	for _, e := range entries {
		if !e.RecordedAt.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		delete(r.store, k)
	} else {
		r.store[k] = kept
	}
}

// latestEntryLocked returns a pointer to the most recent entry for k,
// or nil when the bucket is empty. Must be called with r.mu held.
// Mirrors the "latest timestamp selection" pattern in session.py:303.
func (r *Recorder) latestEntryLocked(k key) *Entry {
	entries := r.store[k]
	if len(entries) == 0 {
		return nil
	}
	latest := &entries[len(entries)-1]
	return latest
}

// cleanupLocked calls purgeExpiredLocked for every key in the store.
// Must be called with r.mu held.
func (r *Recorder) cleanupLocked() {
	if r.ttl == 0 {
		return
	}
	for k := range r.store {
		r.purgeExpiredLocked(k)
	}
}

// =============================================================================
// Disk-Persistence
// =============================================================================

// PersistRow is the data shape that PersistStore.PersistAll expects.
// Defined here so the session package owns the schema; the sqlite package
// uses a structurally identical type to avoid an import cycle.
type PersistRow struct {
	CentralName  string
	Slug         string
	RPCType      string
	Method       string
	FrozenParams string
	ResponseJSON string
	RecordedAt   time.Time
	TTLSeconds   int64
}

// LoadRow is the data shape returned by PersistStore.Load.
type LoadRow struct {
	RPCType      string
	Method       string
	FrozenParams string
	ResponseJSON string
	RecordedAt   time.Time
	TTLSeconds   int64
}

// PersistStore is the interface that a disk backend (e.g. sqlite.SessionRecorderStore)
// must satisfy to be usable with Recorder.Persist and Recorder.Load.
// Defined in the consumer package per project convention.
type PersistStore interface {
	// PersistAll replaces all rows for (centralName, slug) atomically.
	PersistAll(ctx context.Context, centralName, slug string, rows []PersistRow) error
	// Load returns at most maxEntries rows for (centralName, slug).
	Load(ctx context.Context, centralName, slug string, maxEntries int) ([]LoadRow, error)
}

// DefaultMaxLoadEntries is the default cap used by Load when maxEntries <= 0.
const DefaultMaxLoadEntries = 1000

// Persist serialises all non-expired in-memory entries to store under
// (centralName, slug). The entire operation is atomic: PersistStore
// replaces previous rows in a single transaction.
//
// Persist does not change the in-memory state of the recorder; it is safe
// to call concurrently with RecordRequest / RecordResponse.
func (r *Recorder) Persist(ctx context.Context, store PersistStore, centralName, slug string) error {
	r.mu.Lock()
	r.cleanupLocked()
	rows := make([]PersistRow, 0, len(r.store))
	for k, entries := range r.store {
		if len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		respJSON, err := json.Marshal(latest.Response)
		if err != nil {
			// Skip un-serialisable entries rather than aborting the
			// whole persist; document via fmt format string.
			_ = fmt.Errorf("session: persist: marshal %s/%s: %w (skipped)", k.rpcType, k.method, err)
			continue
		}
		rows = append(rows, PersistRow{
			CentralName:  centralName,
			Slug:         slug,
			RPCType:      string(k.rpcType),
			Method:       k.method,
			FrozenParams: k.params,
			ResponseJSON: string(respJSON),
			RecordedAt:   latest.RecordedAt,
			TTLSeconds:   int64(r.ttl.Seconds()),
		})
	}
	r.mu.Unlock()

	return store.PersistAll(ctx, centralName, slug, rows)
}

// Load reads up to maxEntries rows for (centralName, slug) from store and
// merges them into the in-memory recorder. Existing in-memory entries are
// not removed; loaded entries are only inserted when no in-memory entry
// exists for the same (rpcType, method, frozenParams) key, so live data
// always wins.
//
// If maxEntries <= 0, DefaultMaxLoadEntries is used.
func (r *Recorder) Load(ctx context.Context, store PersistStore, centralName, slug string, maxEntries int) error {
	dbRows, err := store.Load(ctx, centralName, slug, maxEntries)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, row := range dbRows {
		k := key{
			rpcType: RPCType(row.RPCType),
			method:  row.Method,
			params:  row.FrozenParams,
		}
		// Live data wins: don't overwrite an existing in-memory entry.
		if _, exists := r.store[k]; exists {
			continue
		}
		var resp any
		if err := json.Unmarshal([]byte(row.ResponseJSON), &resp); err != nil {
			// Skip malformed rows; best-effort load.
			continue
		}
		r.store[k] = []Entry{{Response: resp, RecordedAt: row.RecordedAt}}
	}
	return nil
}

// SetAutoPersist starts a background goroutine that calls Persist every
// interval. It returns a stop function; callers must invoke it to prevent
// goroutine leaks — typically via defer.
//
//	stop := recorder.SetAutoPersist(ctx, store, centralName, slug, 30*time.Second)
//	defer stop()
//
// If interval <= 0, SetAutoPersist is a no-op and returns a no-op stop func.
func (r *Recorder) SetAutoPersist(
	ctx context.Context,
	store PersistStore,
	centralName, slug string,
	interval time.Duration,
) (stop func()) {
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Best-effort: ignore persist errors in the background
				// worker; callers can call Persist explicitly if they need
				// error visibility.
				_ = r.Persist(ctx, store, centralName, slug)
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
	}
}
