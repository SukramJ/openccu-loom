// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

const (
	// rpcRecordingDefaultTTL is the rolling-window TTL the recorder runs with
	// when idle. A recording raises the TTL to capture the whole session and
	// restores this value on stop.
	rpcRecordingDefaultTTL = 600 * time.Second
	// rpcRecordingMaxDuration caps every recording: an open ("until stop")
	// recording is auto-stopped after this long so a forgotten session cannot
	// grow without bound. A requested duration above the cap is clamped to it.
	rpcRecordingMaxDuration = 60 * time.Minute
	// rpcRecordingActiveMarker is the file (under data_dir) that records an
	// active recording so it resumes after a daemon restart.
	rpcRecordingActiveMarker = "active_rpc_recording.json"
)

// RPCRecorderAdapter exposes every central's [session.Recorder] to the REST
// layer. Recording is operator-controlled (start/stop), duration-bounded with
// a safety cap, optionally anonymised, and survives a daemon restart via a
// persisted active marker. Satisfies [interfaces.RPCRecorderService].
type RPCRecorderAdapter struct {
	registry *central.Registry
	dataDir  string

	mu     sync.Mutex
	states map[string]*recState // keyed by central name
}

// recState is the per-central recording metadata the adapter holds while a
// recording is armed. It outlives a stop (minus the timer) so a post-stop
// download still honours the anonymisation choice until the next start.
type recState struct {
	randomize       bool
	startedAt       time.Time
	durationSeconds int // the operator-requested value (0 = open), pre-clamp
	endsAt          time.Time
	timer           *time.Timer
}

// NewRPCRecorderAdapter constructs the adapter. dataDir may be empty to
// disable the restart-survival marker (in-process only).
func NewRPCRecorderAdapter(r *central.Registry, dataDir string) *RPCRecorderAdapter {
	return &RPCRecorderAdapter{registry: r, dataDir: dataDir, states: map[string]*recState{}}
}

// markerState is the persisted active-recording marker.
type markerState struct {
	Centrals        []string `json:"centrals"`
	StartedAt       int64    `json:"started_at"`
	DurationSeconds int64    `json:"duration_seconds"`
	Randomize       bool     `json:"randomize"`
}

// units returns the Units selected by names (empty = all) that have a
// recorder wired.
func (a *RPCRecorderAdapter) units(names []string) []*central.Unit {
	if a == nil || a.registry == nil {
		return nil
	}
	if len(names) == 0 {
		var out []*central.Unit
		for _, u := range a.registry.List() {
			if u != nil && u.Recorder != nil {
				out = append(out, u)
			}
		}
		return out
	}
	var out []*central.Unit
	for _, name := range names {
		if c, ok := a.registry.Get(name); ok && c.Recorder != nil {
			out = append(out, c)
		}
	}
	return out
}

// effectiveDuration clamps a requested duration to the safety cap. A
// non-positive request ("open") becomes the cap.
func effectiveDuration(durationSeconds int) time.Duration {
	if durationSeconds <= 0 {
		return rpcRecordingMaxDuration
	}
	d := time.Duration(durationSeconds) * time.Second
	if d > rpcRecordingMaxDuration {
		return rpcRecordingMaxDuration
	}
	return d
}

// Start implements [interfaces.RPCRecorderService]. It raises the TTL to no
// expiry (so the full session is captured), activates the recorder on the
// selected centrals, arms an auto-stop timer (bounded by the cap), persists
// the active marker, and records the anonymisation choice.
func (a *RPCRecorderAdapter) Start(centrals []string, durationSeconds int, randomize bool) []hmapi.RPCRecordingStatus {
	eff := effectiveDuration(durationSeconds)
	now := time.Now()
	units := a.units(centrals)
	a.mu.Lock()
	for _, c := range units {
		c.Recorder.SetTTL(0)
		c.Recorder.StartSession()
		a.armLocked(c.Name(), eff, durationSeconds, randomize, now)
	}
	a.mu.Unlock()
	a.writeMarker(centrals, durationSeconds, randomize, now)
	return a.Status()
}

// armLocked installs (or replaces) the per-central recording state and its
// auto-stop timer. Caller holds a.mu.
func (a *RPCRecorderAdapter) armLocked(name string, eff time.Duration, durationSeconds int, randomize bool, now time.Time) {
	if old := a.states[name]; old != nil && old.timer != nil {
		old.timer.Stop()
	}
	st := &recState{
		randomize:       randomize,
		startedAt:       now,
		durationSeconds: durationSeconds,
		endsAt:          now.Add(eff),
	}
	st.timer = time.AfterFunc(eff, func() { a.Stop([]string{name}) })
	a.states[name] = st
}

// Stop implements [interfaces.RPCRecorderService]. It deactivates the recorder
// on the selected centrals, restores the rolling-window TTL, and cancels the
// auto-stop timer; the trace stays available for download (with the same
// anonymisation choice) until the next Start clears it.
func (a *RPCRecorderAdapter) Stop(centrals []string) []hmapi.RPCRecordingStatus {
	units := a.units(centrals)
	a.mu.Lock()
	for _, c := range units {
		c.Recorder.StopSession()
		c.Recorder.SetTTL(rpcRecordingDefaultTTL)
		if st := a.states[c.Name()]; st != nil {
			if st.timer != nil {
				st.timer.Stop()
				st.timer = nil
			}
			st.endsAt = time.Time{}
		}
	}
	a.mu.Unlock()
	a.refreshMarker()
	return a.Status()
}

// Status implements [interfaces.RPCRecorderService].
func (a *RPCRecorderAdapter) Status() []hmapi.RPCRecordingStatus {
	units := a.units(nil)
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]hmapi.RPCRecordingStatus, 0, len(units))
	for _, c := range units {
		meta := c.Recorder.Metadata()
		entries, _ := meta["total_entries"].(int)
		status := hmapi.RPCRecordingStatus{
			Central: c.Name(),
			Active:  c.Recorder.IsActive(),
			Entries: entries,
		}
		if st := a.states[c.Name()]; st != nil {
			status.Randomize = st.randomize
			if !st.endsAt.IsZero() {
				status.EndsAt = st.endsAt.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, status)
	}
	return out
}

// Export implements [interfaces.RPCRecorderService]. format selects the shape:
// "golden" yields an ordered replay slice, anything else the keyed map. When
// the recording was started with randomize, operator-identifying values in
// the exported trace are anonymised.
func (a *RPCRecorderAdapter) Export(centralName, format string) (any, bool) {
	exportOne := func(u *central.Unit) any {
		var data any
		if format == "golden" {
			data = u.Recorder.SerializeToGolden()
		} else {
			data = u.Recorder.SerializeToMap()
		}
		a.mu.Lock()
		st := a.states[u.Name()]
		a.mu.Unlock()
		if st != nil && st.randomize {
			data = anonymiseExport(data)
		}
		return data
	}
	if centralName == "" {
		out := map[string]any{}
		for _, c := range a.units(nil) {
			out[c.Name()] = exportOne(c)
		}
		return out, true
	}
	c, ok := a.registry.Get(centralName)
	if !ok || c.Recorder == nil {
		return nil, false
	}
	return exportOne(c), true
}

// ResumeFromMarker re-activates a recording that was running before a daemon
// restart, re-arming the auto-stop timer with the time remaining against the
// original deadline. Called once at boot after the centrals are registered.
// Returns the resumed central scope (nil when no marker / nothing to resume).
func (a *RPCRecorderAdapter) ResumeFromMarker(ctx context.Context) []string {
	if a == nil || a.dataDir == "" {
		return nil
	}
	path := filepath.Join(a.dataDir, rpcRecordingActiveMarker)
	raw, err := os.ReadFile(path) // #nosec G304 — fixed name under data_dir
	if err != nil {
		return nil
	}
	var st markerState
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	startedAt := time.Unix(st.StartedAt, 0)
	remaining := time.Until(startedAt.Add(effectiveDuration(int(st.DurationSeconds))))
	if remaining <= 0 {
		// The recording's deadline passed while the daemon was down.
		_ = os.Remove(path)
		return nil
	}
	now := time.Now()
	units := a.units(st.Centrals)
	a.mu.Lock()
	for _, c := range units {
		// Restore what was captured before the restart, then resume WITHOUT
		// clearing (Resume, not StartSession) so the trace is continuous.
		c.ReloadRecorderFromPersistence(ctx)
		c.Recorder.SetTTL(0)
		c.Recorder.Resume()
		a.armLocked(c.Name(), remaining, int(st.DurationSeconds), st.Randomize, now)
	}
	a.mu.Unlock()
	return st.Centrals
}

// writeMarker persists the active-recording scope + options so it survives a
// restart.
func (a *RPCRecorderAdapter) writeMarker(centrals []string, durationSeconds int, randomize bool, startedAt time.Time) {
	if a.dataDir == "" {
		return
	}
	st := markerState{
		Centrals:        centrals,
		StartedAt:       startedAt.Unix(),
		DurationSeconds: int64(durationSeconds),
		Randomize:       randomize,
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(a.dataDir, rpcRecordingActiveMarker), raw, 0o600)
}

// refreshMarker rewrites or removes the marker to reflect the centrals that
// are still actively recording after a Stop, preserving the original
// duration / randomize / start so a later resume recomputes the deadline.
func (a *RPCRecorderAdapter) refreshMarker() {
	if a.dataDir == "" {
		return
	}
	path := filepath.Join(a.dataDir, rpcRecordingActiveMarker)
	a.mu.Lock()
	var active []string
	var ref *recState
	for _, c := range a.units(nil) {
		if c.Recorder.IsActive() {
			active = append(active, c.Name())
			if ref == nil {
				ref = a.states[c.Name()]
			}
		}
	}
	a.mu.Unlock()
	if len(active) == 0 || ref == nil {
		_ = os.Remove(path)
		return
	}
	a.writeMarker(active, ref.durationSeconds, ref.randomize, ref.startedAt)
}

// hmAddressPattern matches Homematic device addresses — hex serials
// (optionally with a `:channel` suffix) and letter-prefixed serials — so the
// anonymiser can hash them while leaving the surrounding structure intact.
var hmAddressPattern = regexp.MustCompile(`\b([0-9A-Fa-f]{12,14}|[A-Z]{2,4}\d{6,8})(:\d{1,2})?\b`)

// anonymiseExport walks an exported trace and hashes Homematic-address-shaped
// tokens (mirrors [hmlog.AnonymiseToken]) so a recording can be shared without
// leaking the operator's device fleet. Best-effort: it targets address-shaped
// values, not free-form payloads.
func anonymiseExport(data any) any {
	switch v := data.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			// Anonymise the key too: SerializeToMap's slot key is
			// "rpc_type|method|frozen_params", so the device address lives
			// in the key as well as the value. Central-name keys (the
			// all-centrals export) are not address-shaped and pass through.
			out[anonymiseString(k)] = anonymiseExport(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = anonymiseExport(val)
		}
		return out
	case []session.GoldenRecord:
		out := make([]session.GoldenRecord, len(v))
		for i, rec := range v {
			rec.Params = anonymiseString(rec.Params)
			rec.Response = anonymiseExport(rec.Response)
			out[i] = rec
		}
		return out
	case string:
		return anonymiseString(v)
	default:
		return data
	}
}

// anonymiseString replaces every Homematic-address-shaped token in s with a
// stable hash, preserving any `:channel` suffix so replay keys stay aligned.
func anonymiseString(s string) string {
	return hmAddressPattern.ReplaceAllStringFunc(s, func(match string) string {
		addr, channel := match, ""
		if i := lastColon(match); i >= 0 {
			addr, channel = match[:i], match[i:]
		}
		return hmlog.AnonymiseToken(addr) + channel
	})
}

// lastColon returns the index of the final ':' in s, or -1 when absent.
func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}
