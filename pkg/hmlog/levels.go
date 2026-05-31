// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// LevelRegistry holds the effective slog level for every logger
// subsystem path in the daemon. Paths are dot-separated and resolved
// hierarchically — an override on `openccu-loom.client` applies to
// every descendant (`openccu-loom.client.transport`, etc.) unless that
// descendant carries an explicit override of its own.
//
// Overrides may carry a TTL. An expired override is treated as absent
// by [Resolve] and removed lazily on the next [Sweep] call; a daemon
// background job is expected to invoke [Sweep] periodically (every
// 30 s is enough — TTL semantics are coarse on purpose).
//
// Concurrency: the registry is safe for concurrent use. The hot path
// is [Resolve], called once per log record via a [Leveler]; it holds
// only a read lock and short string operations.
type LevelRegistry struct {
	mu           sync.RWMutex
	defaultLevel slog.Level
	overrides    map[string]override
	// now is replaced in tests to advance virtual time without sleeping.
	now func() time.Time
}

type override struct {
	level     slog.Level
	expiresAt time.Time // zero ⇒ no TTL (permanent until Reset / restart)
}

// OverrideInfo is a snapshot of a single configured override, suitable
// for the diagnostics REST endpoint.
type OverrideInfo struct {
	Path        string
	Level       slog.Level
	ExpiresAt   time.Time     // zero ⇒ no expiry
	RemainingMS int64         // 0 when no TTL or already expired
	Permanent   bool          // true ⇒ no TTL
	Remaining   time.Duration // mirror of RemainingMS as Duration
}

// NewLevelRegistry creates a registry with the given default level.
// The default applies to every path that has no override of its own
// and no ancestor with an override.
func NewLevelRegistry(defaultLevel slog.Level) *LevelRegistry {
	return &LevelRegistry{
		defaultLevel: defaultLevel,
		overrides:    map[string]override{},
		now:          time.Now,
	}
}

// SetDefault updates the fallback level used when no override matches.
func (r *LevelRegistry) SetDefault(level slog.Level) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultLevel = level
}

// Default returns the current fallback level.
func (r *LevelRegistry) Default() slog.Level {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultLevel
}

// Set installs an override for the given path. Passing ttl <= 0 makes
// the override permanent (it survives until [Reset] or daemon
// restart); a positive ttl computes an absolute expiry from the
// registry's clock.
//
// The path is normalised to lowercase so that callers cannot
// accidentally split overrides between `Client` and `client`.
func (r *LevelRegistry) Set(path string, level slog.Level, ttl time.Duration) {
	p := normalisePath(path)
	if p == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ov := override{level: level}
	if ttl > 0 {
		ov.expiresAt = r.now().Add(ttl)
	}
	r.overrides[p] = ov
}

// Reset removes the override for the given path (if any). Returns
// true when an override was actually removed.
func (r *LevelRegistry) Reset(path string) bool {
	p := normalisePath(path)
	if p == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.overrides[p]; !ok {
		return false
	}
	delete(r.overrides, p)
	return true
}

// Resolve returns the effective level for path. The lookup walks from
// the most specific override (exact match) up to the registry default,
// stripping one dot-separated segment per step.
func (r *LevelRegistry) Resolve(path string) slog.Level {
	p := normalisePath(path)
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.now()
	for {
		if ov, ok := r.overrides[p]; ok {
			if ov.expiresAt.IsZero() || ov.expiresAt.After(now) {
				return ov.level
			}
			// Expired — fall through to the ancestor lookup. Cleanup
			// happens out-of-band in Sweep so Resolve stays read-only.
		}
		idx := strings.LastIndex(p, ".")
		if idx < 0 {
			break
		}
		p = p[:idx]
	}
	return r.defaultLevel
}

// Leveler returns a [slog.Leveler] bound to path. Every call to
// Leveler.Level() consults the registry, so subsequent [Set]/[Reset]
// take effect on the next emitted log record without rebuilding the
// logger.
func (r *LevelRegistry) Leveler(path string) slog.Leveler {
	return pathLeveler{reg: r, path: normalisePath(path)}
}

// Sweep removes expired overrides. Safe to call concurrently with
// Resolve / Set. Returns the number of overrides actually removed.
//
// Wire this to a periodic scheduler job so that the diagnostics
// snapshot does not show stale TTL entries that have already elapsed.
func (r *LevelRegistry) Sweep() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	removed := 0
	for p, ov := range r.overrides {
		if !ov.expiresAt.IsZero() && !ov.expiresAt.After(now) {
			delete(r.overrides, p)
			removed++
		}
	}
	return removed
}

// Snapshot returns the configured overrides sorted by path.
// Expired entries are filtered out — they are invisible in
// diagnostics output even before [Sweep] removes them from memory.
func (r *LevelRegistry) Snapshot() []OverrideInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.now()
	out := make([]OverrideInfo, 0, len(r.overrides))
	for p, ov := range r.overrides {
		if !ov.expiresAt.IsZero() && !ov.expiresAt.After(now) {
			continue
		}
		info := OverrideInfo{
			Path:      p,
			Level:     ov.level,
			ExpiresAt: ov.expiresAt,
		}
		if ov.expiresAt.IsZero() {
			info.Permanent = true
		} else {
			remaining := ov.expiresAt.Sub(now)
			info.Remaining = remaining
			info.RemainingMS = remaining.Milliseconds()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ApplyConfig replaces all permanent overrides with the values in
// cfg. Keys map to logger paths, values to slog-level strings
// (debug|info|warn|error). Unknown level strings return an error and
// leave the registry untouched. TTL overrides installed via the
// REST endpoint are preserved.
//
// Use this from the daemon config loader so that
// `logging.overrides:` in `config.yaml` reaches the registry without
// the caller knowing the internal slog.Level encoding.
func (r *LevelRegistry) ApplyConfig(cfg map[string]string) error {
	parsed := make(map[string]slog.Level, len(cfg))
	for path, raw := range cfg {
		lvl, err := ParseLevel(raw)
		if err != nil {
			return fmt.Errorf("logging.overrides[%q]: %w", path, err)
		}
		parsed[normalisePath(path)] = lvl
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Drop existing permanent overrides; keep TTL-backed ones.
	for p, ov := range r.overrides {
		if ov.expiresAt.IsZero() {
			delete(r.overrides, p)
		}
	}
	for p, lvl := range parsed {
		if p == "" {
			continue
		}
		r.overrides[p] = override{level: lvl}
	}
	return nil
}

// SetNowFunc replaces the registry's clock. Intended for tests that
// want to drive TTL expiry deterministically — production code keeps
// the time.Now default installed by [NewLevelRegistry].
func (r *LevelRegistry) SetNowFunc(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// pathLeveler is a [slog.Leveler] bound to a specific logger path.
// It is a value type so that logger pointers stay light; equality and
// copy semantics match slog.Level itself.
type pathLeveler struct {
	reg  *LevelRegistry
	path string
}

// Level returns the effective level for the bound path.
func (l pathLeveler) Level() slog.Level { return l.reg.Resolve(l.path) }

// ParseLevel converts a configuration string to a slog.Level. Accepts
// the four standard names case-insensitively; returns an error for
// anything else (intentionally strict — silent fallback to Info would
// hide typos in the YAML).
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("hmlog: invalid level %q (want debug|info|warn|error)", raw)
	}
}

// FormatLevel renders a slog.Level as the lowercase config string
// (debug|info|warn|error). Used by the diagnostics REST handler to
// expose human-readable levels.
func FormatLevel(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "debug"
	case level <= slog.LevelInfo:
		return "info"
	case level <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

func normalisePath(p string) string {
	return strings.ToLower(strings.TrimSpace(p))
}
