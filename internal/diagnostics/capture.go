// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package diagnostics owns the runtime artefacts the operator-facing
// diagnostics endpoints produce — currently the bounded-duration log
// capture sessions.
//
// A capture is an in-memory recording of every slog record emitted
// while it runs. When the operator stops it (or its duration
// expires), the buffered ndjson stream is wrapped in a tar.gz that
// also carries a capture.meta.json sidecar (lifecycle timestamps,
// status, event/byte counts, the trigger and any override applied —
// see [buildArchive]). The archive lives in RAM (gzip-compressed) for
// download; older archives are evicted FIFO once the rolling
// retention cap is reached so a forgetful operator cannot exhaust
// the daemon's heap.
package diagnostics

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// Sentinel errors returned by the manager.
var (
	// ErrCaptureBusy is returned when StartCapture is called while
	// another capture is still running. The MVP allows at most one
	// active capture at a time.
	ErrCaptureBusy = errors.New("diagnostics: a capture is already in progress")
	// ErrCaptureNotFound is returned when the supplied capture ID
	// does not match any known capture (active or archived).
	ErrCaptureNotFound = errors.New("diagnostics: capture id not found")
	// ErrCaptureNotActive is returned when StopCapture is called on
	// a capture that has already stopped or expired.
	ErrCaptureNotActive = errors.New("diagnostics: capture is not active")
	// ErrCaptureDurationTooLong is returned when the requested
	// duration exceeds [MaxCaptureDuration].
	ErrCaptureDurationTooLong = errors.New("diagnostics: capture duration exceeds the 30-minute cap")
)

// Capture configuration knobs.
const (
	// MaxCaptureDuration is the upper limit on a single capture
	// window. Mirrors the safety brief in the diagnostics concept
	// document: longer windows are almost always a sign the operator
	// has forgotten to stop the capture, so we cap and let them
	// explicitly start a new one if they really need more.
	MaxCaptureDuration = 30 * time.Minute
	// DefaultCaptureDuration is used when the caller passes 0.
	DefaultCaptureDuration = 5 * time.Minute
	// MaxArchivedCaptures bounds the rolling FIFO of finished
	// captures. Older archives evict on Stop / expiry.
	MaxArchivedCaptures = 5
	// ArchiveRetention is the maximum age a finished archive is
	// retained before the next Sweep() eviction removes it.
	ArchiveRetention = 24 * time.Hour
)

// Status describes the lifecycle stage of a [Capture].
type Status string

// Capture lifecycle.
const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusExpired Status = "expired"
	StatusAborted Status = "aborted"
)

// StartOptions configures a new capture session.
type StartOptions struct {
	// Duration bounds the capture. Zero falls back to
	// [DefaultCaptureDuration]; values larger than
	// [MaxCaptureDuration] are rejected.
	Duration time.Duration
	// LogLevelOverrides are TTL-bounded subsystem overrides applied
	// when the capture starts and removed when it ends. Use this to
	// dial up debug logging for the recording window only.
	LogLevelOverrides map[string]string
	// Anonymise hashes device-address-shaped values in the recorded
	// records. Defaults to true; set explicitly to false for local
	// debugging where you want raw values in the archive.
	Anonymise bool
	// Triggered carries an operator subject for the audit trail.
	Triggered string
	// BufferBytes overrides the soft cap on the in-memory ndjson
	// buffer (default [hmlog.DefaultCaptureBufferBytes]).
	BufferBytes int
}

// Summary is the wire-shape returned by the REST list endpoint.
type Summary struct {
	ID          string    `json:"id"`
	Status      Status    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	EndsAt      time.Time `json:"ends_at"`
	StoppedAt   time.Time `json:"stopped_at,omitzero"`
	Anonymised  bool      `json:"anonymised"`
	Events      int       `json:"events"`
	BufferBytes int       `json:"buffer_bytes"`
	ArchiveSize int       `json:"archive_size,omitempty"`
	Triggered   string    `json:"triggered_by,omitempty"`
}

// Capture is one capture session. Internal state is encapsulated;
// REST handlers consume only [Capture.Summary].
type Capture struct {
	ID         string
	Anonymised bool
	StartedAt  time.Time
	EndsAt     time.Time
	StoppedAt  time.Time
	Status     Status
	Triggered  string

	sink      *hmlog.CaptureSink
	archive   []byte // populated on Stop()
	expiryAt  time.Time
	overrides map[string]string
}

// Summary projects c onto the wire shape.
func (c *Capture) summary() Summary {
	s := Summary{
		ID:         c.ID,
		Status:     c.Status,
		StartedAt:  c.StartedAt,
		EndsAt:     c.EndsAt,
		StoppedAt:  c.StoppedAt,
		Anonymised: c.Anonymised,
		Triggered:  c.Triggered,
	}
	if c.sink != nil {
		s.Events = c.sink.Events()
		s.BufferBytes = c.sink.Bytes()
	}
	s.ArchiveSize = len(c.archive)
	return s
}

// LevelRegistry is the subset of [*hmlog.LevelRegistry] the manager
// needs. Defined as an interface so tests can drop in a fake.
type LevelRegistry interface {
	Set(path string, level slog.Level, ttl time.Duration)
	Reset(path string) bool
}

// Tee is the subset of [*hmlog.TeeHandler] the manager needs.
type Tee interface {
	Attach(*hmlog.CaptureSink)
	Detach() *hmlog.CaptureSink
}

// Manager owns the capture lifecycle. Safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	tee      Tee
	levels   LevelRegistry
	now      func() time.Time
	active   *Capture
	archived []*Capture
	// expiry fires [Manager.Sweep] when the running capture's window
	// ends. Stopped and cleared when the capture finalises.
	expiry *time.Timer
}

// NewManager builds a manager bound to the supplied tee and level
// registry. Either may be nil — the manager then degrades gracefully
// (no capture target, no override application) so the REST endpoints
// can stay mounted without a wired stack.
func NewManager(tee Tee, levels LevelRegistry, opts ...ManagerOption) *Manager {
	m := &Manager{
		tee:    tee,
		levels: levels,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ManagerOption customises a Manager at construction time.
type ManagerOption func(*Manager)

// WithClock injects the time source. Tests pass a closure over a
// mutable variable to advance time deterministically; the default is
// time.Now. A nil clock keeps the default.
func WithClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// Start launches a new capture. Returns the summary so callers can
// echo the ID + ETA back to the operator immediately.
func (m *Manager) Start(opts StartOptions) (Summary, error) {
	duration := opts.Duration
	if duration <= 0 {
		duration = DefaultCaptureDuration
	}
	if duration > MaxCaptureDuration {
		return Summary{}, ErrCaptureDurationTooLong
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// A window that has already elapsed is not a busy manager. The timer
	// below normally does this, but a suspended host wakes up with an
	// overdue capture, and answering 409 for the rest of the daemon's
	// life over a capture whose window closed hours ago is the failure
	// mode this re-check exists to prevent.
	m.sweepLocked()
	if m.active != nil && m.active.Status == StatusRunning {
		return Summary{}, ErrCaptureBusy
	}

	id, err := newCaptureID()
	if err != nil {
		return Summary{}, fmt.Errorf("diagnostics: capture id: %w", err)
	}
	// Strict interpretation: anonymise when the caller asks for it
	// OR when no operator subject is recorded (covers test-runner
	// triggers that should never produce raw archives).
	anonymise := opts.Anonymise || opts.Triggered == ""
	sink := hmlog.NewCaptureSink(opts.BufferBytes, anonymise)
	now := m.now()
	capture := &Capture{
		ID:         id,
		Anonymised: anonymise,
		StartedAt:  now,
		EndsAt:     now.Add(duration),
		Status:     StatusRunning,
		Triggered:  opts.Triggered,
		sink:       sink,
		overrides:  cloneStringMap(opts.LogLevelOverrides),
	}

	if m.tee != nil {
		m.tee.Attach(sink)
	}
	if m.levels != nil {
		for path, raw := range opts.LogLevelOverrides {
			lvl, err := hmlog.ParseLevel(raw)
			if err != nil {
				continue
			}
			m.levels.Set(path, lvl, duration)
		}
	}
	m.active = capture
	// Expiry is self-driven. Nothing outside this package polls the
	// manager, so without a timer here a capture the operator never
	// stops runs for the daemon's whole life: the log tee stays
	// attached, the archive the operator asked for is never built, and
	// every later Start answers 409. The timer only pokes Sweep, which
	// re-checks EndsAt against the injected clock, so a test clock stays
	// authoritative.
	m.expiry = time.AfterFunc(duration, m.Sweep)
	return capture.summary(), nil
}

// Stop finalises the active capture (or the supplied id, when
// matching). Returns the summary with the archive size populated;
// the archive itself is reachable via [Manager.OpenArchive].
func (m *Manager) Stop(id string) (Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := m.active
	if active == nil || active.Status != StatusRunning {
		return Summary{}, ErrCaptureNotActive
	}
	if id != "" && active.ID != id {
		return Summary{}, ErrCaptureNotFound
	}
	m.finaliseLocked(active, StatusStopped)
	return active.summary(), nil
}

// Sweep transitions any expired active capture to "expired" and
// drops archives older than [ArchiveRetention]. It runs off the
// capture's own expiry timer and on every [Manager.Start]; callers may
// also drive it from a scheduler tick.
func (m *Manager) Sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
}

// sweepLocked is [Manager.Sweep] with the lock already held.
func (m *Manager) sweepLocked() {
	now := m.now()
	if m.active != nil && m.active.Status == StatusRunning && !m.active.EndsAt.IsZero() && !m.active.EndsAt.After(now) {
		m.finaliseLocked(m.active, StatusExpired)
	}
	out := m.archived[:0]
	for _, c := range m.archived {
		if c.expiryAt.IsZero() || c.expiryAt.After(now) {
			out = append(out, c)
		}
	}
	m.archived = out
}

func (m *Manager) finaliseLocked(c *Capture, status Status) {
	c.Status = status
	c.StoppedAt = m.now()
	if m.expiry != nil {
		m.expiry.Stop()
		m.expiry = nil
	}
	if m.tee != nil {
		_ = m.tee.Detach()
	}
	if m.levels != nil {
		for path := range c.overrides {
			m.levels.Reset(path)
		}
	}
	if c.sink != nil {
		c.sink.Close()
		archive, err := buildArchive(c)
		if err == nil {
			c.archive = archive
		}
	}
	c.expiryAt = m.now().Add(ArchiveRetention)
	// Move into the archived ring and trim.
	m.archived = append(m.archived, c)
	if len(m.archived) > MaxArchivedCaptures {
		m.archived = m.archived[len(m.archived)-MaxArchivedCaptures:]
	}
	m.active = nil
}

// List returns every known capture (active first), sorted by
// StartedAt descending. Active captures are reported with their
// current event count.
func (m *Manager) List() []Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := make([]*Capture, 0, len(m.archived)+1)
	if m.active != nil {
		all = append(all, m.active)
	}
	all = append(all, m.archived...)
	sort.Slice(all, func(i, j int) bool { return all[i].StartedAt.After(all[j].StartedAt) })
	out := make([]Summary, 0, len(all))
	for _, c := range all {
		out = append(out, c.summary())
	}
	return out
}

// Get returns one capture's summary.
func (m *Manager) Get(id string) (Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.findLocked(id)
	if c == nil {
		return Summary{}, ErrCaptureNotFound
	}
	return c.summary(), nil
}

// OpenArchive returns the tar.gz bytes for a finished capture.
// Active captures return [ErrCaptureNotActive] — the archive only
// exists after Stop/expiry.
func (m *Manager) OpenArchive(id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.findLocked(id)
	if c == nil {
		return nil, ErrCaptureNotFound
	}
	if c.Status == StatusRunning {
		return nil, ErrCaptureNotActive
	}
	if len(c.archive) == 0 {
		return nil, fmt.Errorf("diagnostics: capture %s has no archive", id)
	}
	out := make([]byte, len(c.archive))
	copy(out, c.archive)
	return out, nil
}

func (m *Manager) findLocked(id string) *Capture {
	if id == "" {
		return nil
	}
	if m.active != nil && m.active.ID == id {
		return m.active
	}
	for _, c := range m.archived {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// buildArchive packages the capture's ndjson stream plus a tiny
// metadata sidecar into a tar.gz blob.
func buildArchive(c *Capture) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	logs := c.sink.Snapshot()
	if err := writeTarFile(tw, "logs.ndjson", logs, c.StartedAt); err != nil {
		return nil, err
	}
	meta := map[string]any{
		"id":           c.ID,
		"started_at":   c.StartedAt.UTC().Format(time.RFC3339Nano),
		"ends_at":      c.EndsAt.UTC().Format(time.RFC3339Nano),
		"stopped_at":   c.StoppedAt.UTC().Format(time.RFC3339Nano),
		"status":       string(c.Status),
		"anonymised":   c.Anonymised,
		"events":       c.sink.Events(),
		"buffer_bytes": c.sink.Bytes(),
		"triggered_by": c.Triggered,
		"overrides":    c.overrides,
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("diagnostics: meta marshal: %w", err)
	}
	if err := writeTarFile(tw, "capture.meta.json", metaJSON, c.StoppedAt); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("diagnostics: tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("diagnostics: gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func writeTarFile(tw *tar.Writer, name string, body []byte, modTime time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(body)),
		ModTime: modTime,
	}
	if hdr.ModTime.IsZero() {
		hdr.ModTime = time.Now()
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("diagnostics: tar header %s: %w", name, err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("diagnostics: tar write %s: %w", name, err)
	}
	return nil
}

func newCaptureID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "cap_" + hex.EncodeToString(b[:]), nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
