// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"maps"
	"sync"
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

// valuesCacheGCMultiplier is how many flush ticks separate one GC tick.
// GC is a maintenance sweep, not part of the hot persistence path: a row
// only becomes dead when a device or parameter permanently disappears
// from the model, which is rare compared to ordinary value churn.
const valuesCacheGCMultiplier = 30

// DefaultValuesCacheGCInterval is the GC cadence produced by
// [gcTickInterval] under the default flush interval (60 s × 30 = 30 min).
// Documented as a named constant so tests and operators have a stable
// reference point; the actual cadence always derives from the flusher's
// configured interval, not this constant directly.
const DefaultValuesCacheGCInterval = DefaultValuesCacheFlushInterval * valuesCacheGCMultiplier

// gcTickInterval derives the GC cadence from the configured flush
// interval so a single `interval` argument to [WireValuesCacheFlusher]
// drives both tickers proportionally: the default flush interval yields
// [DefaultValuesCacheGCInterval], and a custom (e.g. test) interval scales
// the same way without needing a second configuration knob.
func gcTickInterval(flushInterval time.Duration) time.Duration {
	if flushInterval <= 0 {
		flushInterval = DefaultValuesCacheFlushInterval
	}
	return flushInterval * valuesCacheGCMultiplier
}

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

// dirtyKey identifies a single (channel, parameter) VALUES data point for
// dirty-tracking purposes. Central scoping is implicit — each central owns
// its own entry in [dirtyTracker.centrals], so the key does not need to
// carry the central name.
type dirtyKey struct {
	channelAddress string
	parameter      string
}

// dirtyClaim is what [dirtyTracker.SwapClean] hands the flusher for one
// central: either invalidateAll (walk every channel of the central,
// regardless of keys — used for the initial post-Register state) or the
// exact set of (channel, parameter) keys that changed since the previous
// claim.
type dirtyClaim struct {
	keys          map[dirtyKey]struct{}
	invalidateAll bool
}

// dirtyState is the mutable per-central bookkeeping [dirtyTracker] guards
// with its mutex.
type dirtyState struct {
	keys          map[dirtyKey]struct{}
	invalidateAll bool
}

// dirtyTracker scopes the "needs flush" state per central down to the
// individual (channel, parameter) keys that changed, so a central with one
// hot data point does not force flushOnce to re-serialise every other
// live/stale DP on that central every tick — only the tick's own claim.
// Quiet centrals are skipped entirely so the periodic flusher's cost is
// proportional to the activity, not the fleet size. See ADR 0019, "Future
// work".
//
// Mark is on the event-dispatch hot path but only ever does a map lookup
// plus (at most) one map insert under the tracker's mutex — cheap relative
// to the event-bus dispatch that invokes it, and multi-CCU installs already
// get natural parallelism from each central owning its own EventBus.
type dirtyTracker struct {
	mu       sync.Mutex
	centrals map[string]*dirtyState
}

func newDirtyTracker() *dirtyTracker {
	return &dirtyTracker{centrals: make(map[string]*dirtyState)}
}

// Register adds central to the tracker in the invalidateAll state, so the
// first post-boot tick still performs a full walk even if no events fire
// in the warm-up window (the restore pass may have populated DPs that the
// flusher would otherwise consider unchanged). A central that is already
// registered keeps its current state — re-registering must not re-arm an
// already-drained claim.
func (t *dirtyTracker) Register(centralName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.centrals[centralName]; ok {
		return
	}
	t.centrals[centralName] = &dirtyState{invalidateAll: true}
}

// Mark records that (channelAddress, parameter) changed for centralName.
// Unknown centrals are silently ignored — the tracker only follows
// centrals that registered. A no-op when the central is already flagged
// invalidateAll: the pending full walk already covers this key, so there
// is nothing to narrow or widen.
func (t *dirtyTracker) Mark(centralName, channelAddress, parameter string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.centrals[centralName]
	if !ok {
		return
	}
	if st.invalidateAll {
		return
	}
	if st.keys == nil {
		st.keys = make(map[dirtyKey]struct{})
	}
	st.keys[dirtyKey{channelAddress: channelAddress, parameter: parameter}] = struct{}{}
}

// SwapClean returns the accumulated [dirtyClaim] for centralName and
// resets it to clean. ok is false when centralName never registered or
// nothing changed since the previous claim — the flusher uses that to skip
// the central entirely. Used by the flusher to atomically claim a tick's
// worth of work.
func (t *dirtyTracker) SwapClean(centralName string) (dirtyClaim, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.centrals[centralName]
	if !ok {
		return dirtyClaim{}, false
	}
	if !st.invalidateAll && len(st.keys) == 0 {
		return dirtyClaim{}, false
	}
	claim := dirtyClaim{keys: st.keys, invalidateAll: st.invalidateAll}
	t.centrals[centralName] = &dirtyState{}
	return claim, true
}

// Restore merges a claim back into centralName's pending state after the write
// it was taken for failed, so the next tick retries it instead of dropping the
// values on the floor. Marks that arrived while the write was in flight are
// kept — the claim is merged, never assigned. Unknown names are a no-op: a
// central that left the registry meanwhile has nothing left to flush.
func (t *dirtyTracker) Restore(centralName string, claim dirtyClaim) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.centrals[centralName]
	if !ok {
		return
	}
	if claim.invalidateAll {
		st.invalidateAll = true
		st.keys = nil
		return
	}
	if st.invalidateAll {
		return
	}
	if st.keys == nil {
		st.keys = make(map[dirtyKey]struct{}, len(claim.keys))
	}
	maps.Copy(st.keys, claim.keys)
}

// Forget drops centralName from the tracker, so a removed CCU stops being
// claimed on every tick. Unknown names are a no-op.
func (t *dirtyTracker) Forget(centralName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.centrals, centralName)
}

// ValuesCacheFlusher is the handle [WireValuesCacheFlusher] returns. It owns
// the periodic flush + GC goroutine and the per-central dirty tracking.
//
// The per-central half is a seam rather than a one-shot loop because the
// registry is not a fixed set: a CCU adopted at runtime never appeared in the
// boot-time snapshot, so it was never registered with the tracker, never
// marked dirty, and skipped by every tick for the rest of the daemon's life —
// its values reached SQLite only through the graceful-shutdown flush, which a
// SIGKILL, an OOM kill or a host reboot never runs.
//
// loom:reachable:reason="returned by WireValuesCacheFlusher to the daemon's southbound bring-up, which calls Stop as a teardown and StartCentral for every central it adopts; the analyzer resolves a type's methods per loaded package variant, so the reachable instance is not the one it classifies"
type ValuesCacheFlusher struct {
	tracker *dirtyTracker

	// allow is the per-central opt-out predicate
	// (`persistence.values_cache.disabled_centrals`). Nil means every central
	// is persisted.
	allow func(centralName string) bool

	// removeObserver detaches the registry observer and every per-central
	// subscription it attached. It replaces a hand-kept slice: the registry
	// already owns that ledger, and one ledger cannot disagree with itself.
	removeObserver func()

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// ValuesCacheFlusherOption tunes [WireValuesCacheFlusher].
type ValuesCacheFlusherOption func(*ValuesCacheFlusher)

// WithValuesCacheCentralFilter restricts the flusher to the centrals the
// predicate accepts.
//
// The same predicate already decides whether a central's pipeline *restores*
// from the cache; without it here the write half ran for every central, so an
// excluded CCU had its data points serialised into SQLite on every tick and
// never read back — rows the operator explicitly asked not to keep.
func WithValuesCacheCentralFilter(allow func(centralName string) bool) ValuesCacheFlusherOption {
	return func(f *ValuesCacheFlusher) { f.allow = allow }
}

// persists reports whether centralName may be written to the values cache.
func (f *ValuesCacheFlusher) persists(centralName string) bool {
	return f == nil || f.allow == nil || f.allow(centralName)
}

// StartCentral registers one central with the dirty tracker and subscribes its
// EventBus so subsequent value / source changes mark it dirty. It is the
// observer the registry runs per central, for boot-time and runtime-adopted
// CCUs alike — production exercises one path, not two.
//
// A central the operator excluded from the values cache is not registered and
// not subscribed at all — there is nothing to track for a central that is
// never written.
//
// The returned closure releases the subscriptions and drops the central from
// the tracker; it is nil-safe and idempotent. Nil receiver / unit / bus are
// no-ops so a disabled cache needs no guard at the call site.
func (f *ValuesCacheFlusher) StartCentral(u *central.Unit) func() {
	if f == nil || u == nil || u.EventBus == nil {
		return func() {}
	}
	name := u.Name()
	if !f.persists(name) {
		return func() {}
	}
	f.tracker.Register(name)
	bus := u.EventBus
	unsubVal := events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
		f.tracker.Mark(name, e.Key.ChannelAddress, e.Key.Parameter)
	})
	unsubSrc := events.Subscribe(bus, func(e hmevent.DataPointSourceChangedEvent) {
		f.tracker.Mark(name, e.ChannelAddress, e.Parameter)
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			unsubVal()
			unsubSrc()
			f.tracker.Forget(name)
		})
	}
}

// Stop cancels the periodic goroutine, waits for the final shutdown flush and
// releases every subscription. Idempotent and nil-safe.
func (f *ValuesCacheFlusher) Stop() {
	if f == nil {
		return
	}
	f.once.Do(func() {
		f.cancel()
		<-f.done
		if f.removeObserver != nil {
			f.removeObserver()
		}
	})
}

// WireValuesCacheFlusher starts a background goroutine that flushes
// the wire-DP snapshot of every central into the persistent cache
// every `interval`. interval == 0 falls back to
// [DefaultValuesCacheFlushInterval]. Pass a nil store or nil registry
// to disable — the returned handle is nil and every method on it is a no-op.
//
// The flusher also runs once on shutdown — [ValuesCacheFlusher.Stop]
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
// marks itself dirty when one fires (see [ValuesCacheFlusher.StartCentral]).
// The flusher walks only the dirty centrals, claims their flag via SwapClean,
// and skips the rest. Quiet daemons therefore pay only the per-tick noop cost.
//
// The same goroutine also drives the dead-row garbage collector on a
// much lower-frequency second ticker (see [gcTickInterval]): every GC
// tick it rebuilds the alive-key set from the current device model
// across every central and deletes any values_cache row that no
// longer maps to a live parameter. GC does not run on the shutdown
// flush — see [gcOnce] for the rationale and the per-scope safety
// guard.
func WireValuesCacheFlusher(
	reg *central.Registry,
	store *sqlite.ValuesCacheStore,
	interval time.Duration,
	logger *slog.Logger,
	opts ...ValuesCacheFlusherOption,
) *ValuesCacheFlusher {
	if reg == nil || store == nil {
		return nil
	}
	if interval <= 0 {
		interval = DefaultValuesCacheFlushInterval
	}

	ctx, cancel := context.WithCancel(context.Background())
	f := &ValuesCacheFlusher{
		tracker: newDirtyTracker(),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	// Attach before the loop starts so the very first value a central reports
	// — a boot-time one or one adopted at runtime — already marks the cache
	// dirty in time for the next flush tick, instead of only at shutdown.
	f.removeObserver = reg.OnRegister(f.StartCentral)

	go func() {
		defer close(f.done)
		flushTicker := time.NewTicker(interval)
		defer flushTicker.Stop()
		// The GC ticker shares this goroutine rather than spawning a
		// second one: both tickers touch the same store and registry, and
		// a shared select loop keeps the shutdown path (below) the single
		// place that has to reason about in-flight work.
		gcTicker := time.NewTicker(gcTickInterval(interval))
		defer gcTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				// Shutdown flush ignores the dirty flag — the daemon
				// is going down, every central gets one final write
				// so the next boot sees the very latest snapshot. GC does
				// not run on shutdown: it is a maintenance sweep, not
				// part of the data-loss-prevention path the flush covers.
				//nolint:contextcheck // shutdown path must not inherit the cancelled flusher ctx
				flushOnce(context.Background(), reg, store, nil, f.persists, logger, "shutdown")
				return
			case <-flushTicker.C:
				flushOnce(ctx, reg, store, f.tracker, f.persists, logger, "tick")
			case <-gcTicker.C:
				gcOnce(ctx, reg, store, logger)
			}
		}
	}()

	return f
}

// valuesCacheSaver is the subset of [*sqlite.ValuesCacheStore] flushOnce
// needs. Narrowed to an interface so tests can spy on exactly which keys a
// tick persists without a real SQLite-backed store; [WireValuesCacheFlusher]
// always passes a concrete *[sqlite.ValuesCacheStore], which satisfies it.
type valuesCacheSaver interface {
	SaveBatch(ctx context.Context, entries []sqlite.SaveEntry) error
}

// flushOnce walks every dirty central and pushes the wire-DP snapshot into
// the store in one SQLite transaction. trigger is logged for diagnostics
// ("tick" / "shutdown"). When tracker is non-nil, only centrals that
// signalled activity since the last tick are visited, and each one
// contributes only the entries its [dirtyClaim] calls for — either the
// exact (channel, parameter) keys that changed (the common case) or every
// persistable DP when the claim is invalidateAll (the post-boot safety
// net). The tracker is SwapCleaned per central so a Mark that arrives
// during the walk keeps the central dirty for the next tick. Passing
// tracker == nil (shutdown path) walks every central in full regardless so
// the final flush catches everything.
//
// persists is the per-central opt-out predicate; a nil predicate persists every
// central. It gates both walks, because the shutdown flush ignores the tracker
// and would otherwise write the centrals the operator excluded.
//
// A failed SaveBatch hands every claim it consumed back to the tracker: the
// claim is taken before the write, so dropping it on error would lose that
// tick's values until the data points happen to change again — turning the
// cache's bounded crash-loss window into an unbounded one.
func flushOnce(
	ctx context.Context,
	reg *central.Registry,
	store valuesCacheSaver,
	tracker *dirtyTracker,
	persists func(centralName string) bool,
	logger *slog.Logger,
	trigger string,
) {
	if reg == nil || store == nil {
		return
	}
	var entries []sqlite.SaveEntry
	claimed := make(map[string]dirtyClaim)
	walked := 0
	for _, unit := range reg.List() {
		if unit == nil || unit.ModelRegistry == nil {
			continue
		}
		name := unit.Name()
		if persists != nil && !persists(name) {
			continue
		}
		if tracker == nil {
			walked++
			collectAllChannelEntries(name, unit, &entries)
			continue
		}
		claim, dirty := tracker.SwapClean(name)
		if !dirty {
			continue
		}
		claimed[name] = claim
		walked++
		if claim.invalidateAll {
			collectAllChannelEntries(name, unit, &entries)
			continue
		}
		for key := range claim.keys {
			collectKeyEntry(name, unit, key, &entries)
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
		for name, claim := range claimed {
			tracker.Restore(name, claim)
		}
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

// collectAllChannelEntries walks every device/channel currently registered
// on unit and appends one SaveEntry per persistable VALUES DP. Used for the
// invalidateAll claim and the tracker-less shutdown flush, where the whole
// central must be re-serialised regardless of which specific keys changed.
func collectAllChannelEntries(centralName string, unit *central.Unit, out *[]sqlite.SaveEntry) {
	for _, d := range unit.ModelRegistry.List() {
		if d == nil {
			continue
		}
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			collectChannelEntries(centralName, d.InterfaceID, ch, out)
		}
	}
}

// collectKeyEntry looks up exactly the VALUES DP identified by key on
// unit's current device model and appends a SaveEntry when it is still
// persistable. A key whose channel or parameter no longer exists (removed
// between the Mark and this tick) or whose DP is no longer live/stale
// (rolled back to unobserved) silently contributes nothing — the flusher
// only ever adds entries, deletions are handled by
// [sqlite.ValuesCacheStore.DeleteDevice] / [sqlite.ValuesCacheStore.DeleteChannel]
// and the periodic GC pass (see gcOnce), not by a dirty-key flush tick.
func collectKeyEntry(centralName string, unit *central.Unit, key dirtyKey, out *[]sqlite.SaveEntry) {
	ch := unit.GetChannel(key.channelAddress)
	if ch == nil {
		return
	}
	dp := ch.Parameter(hmenum.Parameter(key.parameter))
	if dp == nil {
		return
	}
	appendPersistableEntry(centralName, ch.Device().InterfaceID, dp, out)
}

// collectChannelEntries appends one SaveEntry per persistable DP of ch. A
// DP is persistable when its source is `live` or `stale` and the concrete
// value can be coerced into `any` via UntypedValue. Other states (cache /
// unobserved) hold either re-restored data with no new information, or no
// data at all.
func collectChannelEntries(
	centralName, interfaceID string,
	ch *device.Channel,
	out *[]sqlite.SaveEntry,
) {
	for _, dp := range ch.DataPoints() {
		appendPersistableEntry(centralName, interfaceID, dp, out)
	}
}

// appendPersistableEntry appends one SaveEntry for dp when it qualifies:
// source `live` or `stale`, and an observed non-nil raw value. Shared by
// the full-channel walk ([collectChannelEntries]) and the single-key
// lookup ([collectKeyEntry]) so both paths apply the exact same
// persistability rule.
func appendPersistableEntry(
	centralName, interfaceID string,
	dp device.ParameterDataPoint,
	out *[]sqlite.SaveEntry,
) {
	if dp == nil {
		return
	}
	sourced, ok := dp.(sourcedDP)
	if !ok {
		return
	}
	src := sourced.Source()
	if src != hmenum.ValueSourceLive && src != hmenum.ValueSourceStale {
		return
	}
	reader, ok := dp.(valueReader)
	if !ok {
		return
	}
	v, observed := reader.RawValue()
	if !observed || v == nil {
		return
	}
	addr, ok := dp.(addressed)
	if !ok {
		return
	}
	key := addr.DataPointKey()
	// An edge-trigger parameter (PRESS_*, CODE_ID, CODE_STATE) carries an
	// edge, not a level: `PRESS_SHORT: true` says a button was pressed once,
	// never that it is being held. Restoring that on the next boot resurrects
	// a keypress nobody made — the north-bound planes cannot tell the replay
	// from the original. Nothing downstream wants the stale edge, so it never
	// enters the cache in the first place.
	if hmenum.IsEdgeTriggerParameter(hmenum.Parameter(key.Parameter)) {
		return
	}
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

// gcOnce builds the current alive-key set across every registered central
// and deletes any values_cache row whose (central, interface, channel,
// parameter) tuple is no longer part of that set — e.g. a channel whose
// parameter list shrank after a firmware/profile update, or a device
// removal that raced the eviction handler in values_cache_evict.go. Unlike
// [collectChannelEntries], the alive set here includes every data point
// regardless of its current [hmenum.ValueSource]: a `cache`-sourced row
// that has not yet received a fresh live value in this run is still a
// legitimate row, not an orphan.
//
// The sweep is scoped per (central, interface): only rows belonging to a
// scope that actually contributed a live model are candidates for deletion.
// An empty in-memory model is never read as "everything is gone" — a CCU
// still blocked in the readiness gate, one rebooting mid-life, or a single
// interface whose ingest exhausted its retries all present exactly that
// picture, and deleting on it destroys the persisted cache that the next cold
// boot restores sleeping battery devices from. The cost of the conservative
// reading is bounded: an interface that genuinely loses its last device keeps
// its rows until the device-level eviction path (values_cache_evict.go)
// removes them, which is the path a real removal takes anyway.
func gcOnce(
	ctx context.Context,
	reg *central.Registry,
	store *sqlite.ValuesCacheStore,
	logger *slog.Logger,
) {
	if reg == nil || store == nil {
		return
	}
	sweep := buildGCSweep(reg)
	if len(sweep.Scopes) == 0 {
		if logger != nil {
			logger.Debug("values_cache.gc_skipped", slog.String("reason", "no_scope_observed"))
		}
		return
	}
	result, err := store.GCDeadRows(ctx, sweep)
	if err != nil {
		if logger != nil {
			logger.Warn("values_cache.gc_err", slog.String("err", err.Error()))
		}
		return
	}
	if logger != nil {
		logger.Debug("values_cache.gc_done",
			slog.Int("scanned", result.Scanned),
			slog.Int("deleted", result.Deleted))
	}
}

// buildGCSweep walks every registered central's current device model and
// returns what the walk observed: the (central, interface) scopes that yielded
// at least one data point, and the [sqlite.AliveKey] tuples GC must preserve
// inside them. Devices or channels not present in the walk (removed, renamed
// address, dropped parameter) leave no key behind, so their cache rows fall
// out of the alive set and become eligible for deletion — but only while
// their own scope is still represented, which is what keeps an offline CCU's
// rows out of the sweep entirely.
func buildGCSweep(reg *central.Registry) sqlite.GCSweep {
	sweep := sqlite.GCSweep{
		Scopes: make(map[string]struct{}),
		Alive:  make(map[string]struct{}),
	}
	for _, unit := range reg.List() {
		if unit == nil || unit.ModelRegistry == nil {
			continue
		}
		// Only a central whose model is complete may speak for its own
		// scopes. Devices enter the model one at a time, so a central that
		// is still ingesting — at boot, or after a cache reset / CCU reboot
		// cleared the model and started over — has its first interface in
		// Scopes while every device still pending is absent from Alive, and
		// GC would delete the cached VALUES rows of exactly those devices.
		// The next cold boot then has no cache for them, and a battery
		// device reads `unknown` until it next reports on its own.
		if !unit.IsSouthboundReady() || unit.Readiness().Phase != hmenum.ReadinessReady {
			continue
		}
		name := unit.Name()
		for _, d := range unit.ModelRegistry.List() {
			if d == nil {
				continue
			}
			for _, ch := range d.Channels() {
				if ch == nil {
					continue
				}
				for _, dp := range ch.DataPoints() {
					if dp == nil {
						continue
					}
					addr, ok := dp.(addressed)
					if !ok {
						continue
					}
					key := addr.DataPointKey()
					sweep.Scopes[sqlite.ScopeKey(name, d.InterfaceID)] = struct{}{}
					sweep.Alive[sqlite.AliveKey(name, d.InterfaceID, key.ChannelAddress, key.Parameter)] = struct{}{}
				}
			}
		}
	}
	return sweep
}
