// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"log/slog"
	"sort"
	"sync"
	"time"

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
	//
	// 60 s is a POLICY bound of this daemon, not a CCU-stated latency, and
	// no firmware value would settle it: for a device whose rx mode is
	// wake-up only, rfd stores the written value in its own volatile store
	// and QUEUES the frame (OpenCCU-Base src/rfd/RFPhysicalDataInterfaceCommand.cpp,
	// PutData), so setValue/putParamset answer success without transmitting.
	// The queue is drained only when the device itself next transmits
	// (src/rfd/RFDevice.cpp SendAfterWakeupFrames, reached from the
	// incoming-frame path); it carries no expiry at all — the only bound is
	// a depth of 10, and an overflow silently drops the OLDEST frame. So the
	// confirming VALUES callback can arrive minutes later or never, and no
	// finite TTL can cover that path. Of the shipped RF device types a
	// substantial minority declare a wake-up-only rx mode, including wall
	// thermostats users write setpoints to.
	//
	// What the firmware does bound is the synchronous case, and 60 s covers
	// it with room: per-frame BidCos wait times are 1500 / 4500 / 12500 ms
	// (src/rfd/BidcosFrameWaitTime.h) and the HmIP legacy call budget is
	// Legacy.ResponseTimeout, default 25 s
	// (HMIPServer de.eq3.cbcs.legacy.bidcos.rpc.LegacyServiceHandler).
	// [TestHmCliCommandTrackerTTLCoversSynchronousBudgets] pins that margin.
	//
	// The daemon's OWN retry waits do not compete with this TTL either,
	// which is the relation a reader is most likely to assume backwards:
	// RetryConfig.DutyCycleDelay is 40 s and a three-attempt chain waits it
	// twice, so a TTL of 60 s would look far too short. It is not, because
	// the entry is stamped only after the reliability stack has returned
	// success — InterfaceClient.SetValue calls WriteUnconfirmedValue below
	// the Circuit/Retrier call and after its error check, and
	// ValueWriter.SetValueWithOptions calls its command-tracker hook after
	// the wire write. Every backoff is therefore spent BEFORE sentAt
	// exists, not against it, and a write that exhausts its attempts
	// records nothing at all.
	// [TestW2CliCommandTrackerRecordsAfterTheRetryWindow] and
	// [TestW2CliCommandTrackerRecordsNothingForAFailedWrite] pin both
	// halves; stamping the entry before the chain would subtract the whole
	// retry window from the TTL.
	//
	// On a real CCU that chain does not arise on this path in the first
	// place: fault -8 is thrown at exactly one place in the CCU sources,
	// inside RFDevice::UpdateFirmware (OpenCCU-Base
	// src/rfd/RFDevice.cpp:1492 `throw XmlRpcException("not enough
	// DutyCycle free",-8)`, the only XmlRpcException carrying -8 in the
	// tree), so no value write produces it.
	//
	// Consequence of the uncovered path, stated so it is not mistaken for a
	// guarantee: on a wake-up device the optimistic entry expires before the
	// device answers, GetLastSentValue reports (nil, false), and the value
	// reverts to whatever the model last knew. That is a deliberate
	// bounded-memory choice; extending the TTL only widens the window, it
	// does not close it.
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
