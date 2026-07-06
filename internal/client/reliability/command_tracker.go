// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CommandTrackerConfig configures a [CommandTracker].
type CommandTrackerConfig struct {
	// MaxSize is the hard limit on the number of tracked entries.
	// When reached, the oldest 20% are evicted (LRU). Default: 1000.
	MaxSize int

	// WarningThreshold triggers a log warning when the tracker exceeds
	// this size (before MaxSize). Default: 800.
	WarningThreshold int

	// TTL is the max age of a tracked entry. Default: 60s.
	TTL time.Duration

	// CleanupThreshold triggers lazy cleanup when the tracker exceeds
	// this size. Default: 500.
	CleanupThreshold int

	// Logger for threshold warnings. Nil uses slog.Default().
	Logger *slog.Logger
}

func (c *CommandTrackerConfig) applyDefaults() {
	if c.MaxSize <= 0 {
		c.MaxSize = 1000
	}
	if c.WarningThreshold <= 0 {
		c.WarningThreshold = 800
	}
	if c.TTL <= 0 {
		c.TTL = 60 * time.Second
	}
	if c.CleanupThreshold <= 0 {
		c.CleanupThreshold = 500
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// cachedCommand holds a sent value and its send timestamp.
type cachedCommand struct {
	value  any
	sentAt time.Time
}

// CommandTracker tracks recently-sent commands per DataPointKey with
// automatic expiry and configurable size limits.
//
// Used by InterfaceClient as the last_value_send_tracker to enable
// optimistic UI updates: after a SetValue/PutParamset call the sent
// value is immediately visible before the CCU pushes back a callback.
//
// Thread-safe: all public methods are protected by a mutex.
type CommandTracker struct {
	cfg         CommandTrackerConfig
	interfaceID string

	mu            sync.Mutex
	entries       map[hmtypes.DataPointKey]cachedCommand
	warningLogged bool
}

// NewCommandTracker constructs a tracker for the given interface ID.
func NewCommandTracker(interfaceID string, cfg CommandTrackerConfig) *CommandTracker {
	cfg.applyDefaults()
	return &CommandTracker{
		cfg:         cfg,
		interfaceID: interfaceID,
		entries:     make(map[hmtypes.DataPointKey]cachedCommand),
	}
}

// AddSetValue records a single setValue send and returns the
// (DataPointKey, value) pair for optimistic-update propagation.
func (t *CommandTracker) AddSetValue(
	channelAddress string,
	parameter hmenum.Parameter,
	paramsetKey hmenum.ParamsetKey,
	value any,
) (hmtypes.DataPointKey, bool) {
	dpk := hmtypes.DataPointKey{
		InterfaceID:    t.interfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    paramsetKey,
		Parameter:      string(parameter),
	}
	if dpk.InterfaceID == "" || dpk.ChannelAddress == "" || dpk.ParamsetKey == "" || dpk.Parameter == "" {
		return hmtypes.DataPointKey{}, false
	}
	t.mu.Lock()
	t.lazyCleanupLocked()
	t.enforceSizeLimitLocked()
	t.entries[dpk] = cachedCommand{value: value, sentAt: time.Now()}
	t.mu.Unlock()
	return dpk, true
}

// AddCombinedParameter parses a combined-parameter wire string (COMBINED_PARAMETER
// or LEVEL_COMBINED) into its component key/value pairs and records each pair as
// a tracked command under ParamsetKeyValues. Returns the list of DataPointKeys
// registered, or nil when the wire string cannot be parsed.
//
// This mirrors the Python add_combined_parameter path: parse the combined string
// into a paramset map, then delegate to AddPutParamset so both sends land in the
// tracker as a single atomic unit.
func (t *CommandTracker) AddCombinedParameter(
	channelAddress string,
	parameter string,
	value string,
) []hmtypes.DataPointKey {
	values, ok := backends.ParseCombinedParameter(parameter, value)
	if !ok || len(values) == 0 {
		return nil
	}
	return t.AddPutParamset(channelAddress, hmenum.ParamsetKeyValues, values)
}

// AddPutParamset records a putParamset send for all values and returns
// the set of (DataPointKey, value) pairs.
func (t *CommandTracker) AddPutParamset(
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
) []hmtypes.DataPointKey {
	if len(values) == 0 {
		return nil
	}
	now := time.Now()
	t.mu.Lock()
	t.lazyCleanupLocked()
	t.enforceSizeLimitLocked()
	keys := make([]hmtypes.DataPointKey, 0, len(values))
	for param, value := range values {
		dpk := hmtypes.DataPointKey{
			InterfaceID:    t.interfaceID,
			ChannelAddress: channelAddress,
			ParamsetKey:    paramsetKey,
			Parameter:      param,
		}
		t.entries[dpk] = cachedCommand{value: value, sentAt: now}
		keys = append(keys, dpk)
	}
	t.mu.Unlock()
	return keys
}

// GetLastSentValue returns the last sent value for a DataPointKey if
// still within TTL, and true; otherwise returns (nil, false).
func (t *CommandTracker) GetLastSentValue(dpk hmtypes.DataPointKey) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cmd, ok := t.entries[dpk]
	if !ok {
		return nil, false
	}
	if time.Since(cmd.sentAt) > t.cfg.TTL {
		delete(t.entries, dpk)
		return nil, false
	}
	return cmd.value, true
}

// HasInFlight returns true when there is a tracked sent value for dpk
// that has not yet expired. Used to detect whether a callback event
// can be considered the confirmation of a recent send.
func (t *CommandTracker) HasInFlight(dpk hmtypes.DataPointKey) bool {
	_, ok := t.GetLastSentValue(dpk)
	return ok
}

// ClearForKey removes the tracking entry for dpk. Called after the
// CCU confirms the value via callback.
func (t *CommandTracker) ClearForKey(dpk hmtypes.DataPointKey) {
	t.mu.Lock()
	delete(t.entries, dpk)
	t.mu.Unlock()
}

// CleanupExpired removes all entries older than TTL and returns the
// count removed.
func (t *CommandTracker) CleanupExpired() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cleanupExpiredLocked()
}

// Clear removes all entries.
func (t *CommandTracker) Clear() {
	t.mu.Lock()
	t.entries = make(map[hmtypes.DataPointKey]cachedCommand)
	t.mu.Unlock()
}

// Size returns the current number of tracked entries.
func (t *CommandTracker) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// --- internal helpers (must be called with t.mu held) ---------------

func (t *CommandTracker) lazyCleanupLocked() {
	if len(t.entries) > t.cfg.CleanupThreshold {
		t.cleanupExpiredLocked()
	}
}

func (t *CommandTracker) cleanupExpiredLocked() int {
	cutoff := time.Now().Add(-t.cfg.TTL)
	var removed int
	for dpk, cmd := range t.entries {
		if cmd.sentAt.Before(cutoff) {
			delete(t.entries, dpk)
			removed++
		}
	}
	return removed
}

func (t *CommandTracker) enforceSizeLimitLocked() {
	size := len(t.entries)

	// Warning with hysteresis
	if size >= t.cfg.WarningThreshold && !t.warningLogged {
		t.cfg.Logger.Warn(
			"CommandTracker approaching size limit",
			slog.String("interface", t.interfaceID),
			slog.Int("size", size),
			slog.Int("max", t.cfg.MaxSize),
		)
		t.warningLogged = true
	} else if size < t.cfg.WarningThreshold {
		t.warningLogged = false
	}

	// Hard-limit LRU eviction: remove oldest 20%
	if size >= t.cfg.MaxSize {
		type kv struct {
			key hmtypes.DataPointKey
			ts  time.Time
		}
		ordered := make([]kv, 0, size)
		for k, v := range t.entries {
			ordered = append(ordered, kv{key: k, ts: v.sentAt})
		}
		sort.Slice(ordered, func(i, j int) bool {
			return ordered[i].ts.Before(ordered[j].ts)
		})
		remove := max(1, size/5)
		for _, item := range ordered[:remove] {
			delete(t.entries, item.key)
		}
		t.cfg.Logger.Debug(
			"CommandTracker evicted oldest entries",
			slog.String("interface", t.interfaceID),
			slog.Int("evicted", remove),
			slog.Int("size_was", size),
		)
	}
}
