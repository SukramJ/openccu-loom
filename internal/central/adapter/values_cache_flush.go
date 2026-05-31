// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DefaultValuesCacheFlushInterval is the periodic-flush cadence used
// when the operator did not override it in config.yaml. 60 s keeps
// SQLite write load low (typically tens of inserts per tick on a
// 1000-DP installation) while bounding crash data loss to roughly
// the same window the daemon's other periodic jobs already use.
const DefaultValuesCacheFlushInterval = 60 * time.Second

// sourcedDP is the subset of methods [values_cache_flush] needs from
// each wire data point. The generic.DataPoint satisfies it.
type sourcedDP interface {
	Source() hmenum.ValueSource
	LastSeenAt() time.Time
	LastChangedAt() time.Time
}

// valueReader is the optional interface that exposes the raw wire
// value for cache persistence. Generic data points implement it via
// the existing RawValue accessor.
type valueReader interface {
	RawValue() (any, bool)
}

// addressed gives the cache (channel, parameter) coordinates. The
// generic.DataPoint implements DataPointKey returning a struct that
// includes ChannelAddress + Parameter — we depend on the typed key
// to stay schema-stable across DP specialisations.
type addressed interface {
	DataPointKey() hmtypes.DataPointKey
}

// dirtyTracker scopes the "needs flush" flag per central name. The
// flusher's periodic tick reads the flag for each central and walks
// only the dirty ones, then resets the flag. Quiet centrals are
// skipped entirely so the periodic flusher's cost is proportional to
// the activity, not the fleet size. See ADR 0019, "Future work".
//
// Operations are atomic; the tracker itself is lock-free in the hot
// path (Mark) and only locks for the rare add-central path.
type dirtyTracker struct {
	mu       sync.RWMutex
	centrals map[string]*atomic.Bool
}

func newDirtyTracker() *dirtyTracker {
	return &dirtyTracker{centrals: make(map[string]*atomic.Bool)}
}

// Register adds central to the tracker. Returns the *atomic.Bool that
// represents the central's dirty flag — callers can stash this and
// avoid the per-event map lookup. Initially set to true so the first
// post-boot tick still runs even if no events fire in the warm-up
// window (the restore pass may have populated DPs that the flusher
// would otherwise consider unchanged).
func (t *dirtyTracker) Register(centralName string) *atomic.Bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if flag, ok := t.centrals[centralName]; ok {
		return flag
	}
	flag := &atomic.Bool{}
	flag.Store(true)
	t.centrals[centralName] = flag
	return flag
}

// Mark flips the central's dirty flag. Cheap: a single atomic store
// after a read-locked map lookup. Unknown centrals are silently
// ignored — the tracker only follows centrals that registered.
func (t *dirtyTracker) Mark(centralName string) {
	t.mu.RLock()
	flag, ok := t.centrals[centralName]
	t.mu.RUnlock()
	if !ok {
		return
	}
	flag.Store(true)
}

// SwapClean returns the previous dirty state for centralName and
// resets it to clean. Used by the flusher to atomically claim a
// tick's worth of work.
func (t *dirtyTracker) SwapClean(centralName string) bool {
	t.mu.RLock()
	flag, ok := t.centrals[centralName]
	t.mu.RUnlock()
	if !ok {
		return false
	}
	return flag.Swap(false)
}

// WireValuesCacheFlusher starts a background goroutine that flushes
// the wire-DP snapshot of every central into the persistent cache
// every `interval`. interval == 0 falls back to
// [DefaultValuesCacheFlushInterval]. Pass a nil store or nil registry
// to disable.
//
// The flusher also runs once on shutdown — the returned closer
// blocks until the final flush has completed, so the cache survives
// a graceful daemon stop without missing the last interval's worth
// of updates.
//
// Persistence rule: only data points whose Source is `live` or
// `stale` are written. `cache` rows are re-restored values that
// would round-trip with no new information; `unobserved` rows have
// nothing to store.
//
// Tick cost: each central subscribes its own EventBus for
// DataPointValueChangedEvent + DataPointSourceChangedEvent and
// marks itself dirty when one fires. The flusher walks only the
// dirty centrals, claims their flag via SwapClean, and skips the
// rest. Quiet daemons therefore pay only the per-tick noop cost.
func WireValuesCacheFlusher(
	reg *central.Registry,
	store *sqlite.ValuesCacheStore,
	interval time.Duration,
	logger *slog.Logger,
) func() {
	if reg == nil || store == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultValuesCacheFlushInterval
	}

	tracker := newDirtyTracker()
	var unsubs []func()
	for _, unit := range reg.List() {
		if unit == nil || unit.EventBus == nil {
			continue
		}
		name := unit.Name()
		tracker.Register(name)
		bus := unit.EventBus
		unsubVal := events.Subscribe(bus, func(_ hmevent.DataPointValueChangedEvent) {
			tracker.Mark(name)
		})
		unsubSrc := events.Subscribe(bus, func(_ hmevent.DataPointSourceChangedEvent) {
			tracker.Mark(name)
		})
		unsubs = append(unsubs, unsubVal, unsubSrc)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				// Shutdown flush ignores the dirty flag — the daemon
				// is going down, every central gets one final write
				// so the next boot sees the very latest snapshot.
				flushOnce(context.Background(), reg, store, nil, logger, "shutdown")
				return
			case <-ticker.C:
				flushOnce(ctx, reg, store, tracker, logger, "tick")
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
			for _, u := range unsubs {
				u()
			}
		})
	}
}

// flushOnce walks every dirty central + interface + channel and
// pushes the wire-DP snapshot into the store in one SQLite
// transaction. trigger is logged for diagnostics ("tick" /
// "shutdown"). When tracker is non-nil only centrals that signalled
// activity since the last tick are visited; the tracker is
// SwapCleaned per central so a Mark that arrives during the walk
// keeps the central dirty for the next tick. Passing tracker == nil
// (shutdown path) walks every central regardless so the final flush
// catches everything.
func flushOnce(
	ctx context.Context,
	reg *central.Registry,
	store *sqlite.ValuesCacheStore,
	tracker *dirtyTracker,
	logger *slog.Logger,
	trigger string,
) {
	if reg == nil || store == nil {
		return
	}
	var entries []sqlite.SaveEntry
	walked := 0
	for _, unit := range reg.List() {
		if unit == nil || unit.ModelRegistry == nil {
			continue
		}
		name := unit.Name()
		if tracker != nil && !tracker.SwapClean(name) {
			continue
		}
		walked++
		for _, d := range unit.ModelRegistry.List() {
			if d == nil {
				continue
			}
			for _, ch := range d.Channels() {
				if ch == nil {
					continue
				}
				collectChannelEntries(name, d.InterfaceID, ch, &entries)
			}
		}
	}
	if len(entries) == 0 {
		if logger != nil && tracker != nil && walked == 0 {
			logger.Debug("values_cache.flush_skipped",
				slog.String("trigger", trigger),
				slog.String("reason", "no_central_dirty"))
		}
		return
	}
	if err := store.SaveBatch(ctx, entries); err != nil {
		if logger != nil {
			logger.Warn("values_cache.flush_err",
				slog.String("trigger", trigger),
				slog.Int("entries", len(entries)),
				slog.Int("centrals_walked", walked),
				slog.String("err", err.Error()))
		}
		return
	}
	if logger != nil {
		logger.Debug("values_cache.flushed",
			slog.String("trigger", trigger),
			slog.Int("entries", len(entries)),
			slog.Int("centrals_walked", walked))
	}
}

// collectChannelEntries appends one SaveEntry per persistable DP of ch.
// A DP is persistable when its source is `live` or `stale` and the
// concrete value can be coerced into `any` via UntypedValue. Other
// states (cache / unobserved) hold either re-restored data with no
// new information, or no data at all.
func collectChannelEntries(
	centralName, interfaceID string,
	ch *device.Channel,
	out *[]sqlite.SaveEntry,
) {
	for _, dp := range ch.DataPoints() {
		if dp == nil {
			continue
		}
		sourced, ok := dp.(sourcedDP)
		if !ok {
			continue
		}
		src := sourced.Source()
		if src != hmenum.ValueSourceLive && src != hmenum.ValueSourceStale {
			continue
		}
		reader, ok := dp.(valueReader)
		if !ok {
			continue
		}
		v, observed := reader.RawValue()
		if !observed || v == nil {
			continue
		}
		addr, ok := dp.(addressed)
		if !ok {
			continue
		}
		key := addr.DataPointKey()
		*out = append(*out, sqlite.SaveEntry{
			CentralName:    centralName,
			InterfaceID:    interfaceID,
			ChannelAddress: key.ChannelAddress,
			ParameterName:  key.Parameter,
			Value:          v,
			LastSeenAt:     sourced.LastSeenAt(),
			LastChangedAt:  sourced.LastChangedAt(),
		})
	}
}
