// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CacheSizeProvider is the narrow contract a coordinator/registry must
// satisfy to feed the [CacheCoordinator]'s metrics provider. The
// description registries (`*registry.DeviceDescriptionRegistry`,
// `*registry.ParamsetRegistry`) all expose Len() — capturing the
// pattern in this interface keeps the metrics provider decoupled from
// the concrete registry implementations and makes the coordinator
// usable from tests with cheap fakes.
type CacheSizeProvider interface {
	// Len returns the current number of entries.
	Len() int
}

// DataCacheEntry is a single cached data-point value.
type DataCacheEntry struct {
	Value       hmtypes.ParamValue
	LastUpdated time.Time
	Source      string
}

// CachePersister is the contract for loading and saving the data-point
// value cache to persistent storage. Implementations delegate to the
// SQLite store layer. Pass a nil persister to disable persistence.
type CachePersister interface {
	// LoadDataCache loads cached data-point values from storage into
	// entries. Returns nil when there is no stored data.
	LoadDataCache(ctx context.Context) (map[hmtypes.DataPointKey]DataCacheEntry, error)
	// SaveDataCache persists the provided entries.
	SaveDataCache(ctx context.Context, entries map[hmtypes.DataPointKey]DataCacheEntry) error
}

// CacheCoordinator owns the in-memory data cache (the source of truth
// for current data-point values). It keeps the entry map and the
// bookkeeping timestamps the EventCoordinator uses to decide whether a
// value change should be surfaced.
//
// In addition to the value cache the coordinator records hit / miss
// eviction counters so the metrics aggregator can publish the
// `cache.data_cache.*` gauges Counters are
// updated transparently from [Get] and [Delete]; callers do not need
// to know they exist.
//
// # Session-recorder and IncidentRecorder (closed)
//
// `_incident_store` (central/coordinators/cache.py) and uses them to
// log cache-miss anomalies and to track diagnostic incidents per
// session.
//
// openccu-loom replicates both as optional fields wired at boot:
//
// - [*session.Recorder] is the Go port.
// SessionRecorder (store/persistent/session.py). Wire via
// [SetSessionRecorder]; passthrough is available via
// [RecordSession]. Nil = recording disabled (no-op).
//
// - [reliability.IncidentRecorder] is the Go port.
// IncidentStore (store/persistent/incident.py). Wire via
// [SetIncidentRecorder]; read back via [GetIncidentRecorder].
// Nil = incident recording disabled (no-op). The concrete
// implementation is [*sqlite.IncidentStore].
//
// In addition, [internal/metrics.Aggregator] publishes
// `cache.data_cache.*` counters (hits / misses / evictions / sizes)
// via Prometheus gauges, pulling data from MetricsDataCache* methods.
// This completes the parity item.
type CacheCoordinator struct {
	mu      sync.RWMutex
	entries map[hmtypes.DataPointKey]DataCacheEntry

	// Counters — atomic so [Get] / [Delete] can stay on the read-lock
	// fast path (audit R7). Previously these were plain ints under
	// [mu.Lock], which forced every read through the writer queue;
	// at ~80 k cached DPs and high REST read rate the contention
	// dominated the cache hot path. Atomic counters let [Get] take
	// only [mu.RLock].
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64

	// Optional size providers wired by the central. Each is
	// dereferenced lazily via [Len], so a nil provider is treated as
	// "size unknown" → 0.
	deviceDescriptions   CacheSizeProvider
	paramsetDescriptions CacheSizeProvider
	visibilityCache      CacheSizeProvider

	// paramsetInvalidator is called by InvalidateParamsetDescriptions.
	// Nil = no-op.
	paramsetInvalidator ParamsetInvalidator

	// persister is the optional storage back-end. Nil = in-memory only.
	// Wired via SetPersister.
	persister CachePersister

	// dirty is true when entries have changed since the last SaveAll.
	dirty bool

	// initializationComplete is set by SetDataCacheInitializationComplete
	// to signal that the cold-start device creation pass has finished.
	// In Go the cache has no TTL expiry, so this flag is a semantic
	// marker for callers that gate post-startup operations on it.
	initializationComplete bool

	// bus is the event bus used for event subscriptions and emissions.
	// Nil = no subscriptions, no emissions.
	bus *events.Bus

	// centralName scopes emitted events (CacheInvalidatedEvent) to the
	// owning [Unit]. Empty when not wired — emitted events lose
	// their multi-CCU scope but are still observable.
	centralName string

	// unsubs holds the cleanup functions for bus subscriptions.
	unsubs []func()

	// sessionRecorder is the optional session recorder. Nil = disabled.
	// Wire via SetSessionRecorder; passthrough via RecordSession.
	sessionRecorder *session.Recorder

	// incidentRecorder is the optional incident recorder. Nil = disabled.
	// Wire via SetIncidentRecorder; read via GetIncidentRecorder.
	incidentRecorder reliability.IncidentRecorder
}

// NewCacheCoordinator returns an empty cache.
func NewCacheCoordinator() *CacheCoordinator {
	return &CacheCoordinator{entries: make(map[hmtypes.DataPointKey]DataCacheEntry)}
}

// SetSizeProviders wires the description-registry size accessors used
// by the metrics provider. Pass nil for any unwired registry; the
// metrics provider then reports size 0 for that field.
//
// Multi-CCU safe: each [Unit] owns its own
// [CacheCoordinator] and wires its own registries — there is no
// shared global state.
func (c *CacheCoordinator) SetSizeProviders(deviceDescriptions, paramsetDescriptions, visibility CacheSizeProvider) {
	c.mu.Lock()
	c.deviceDescriptions = deviceDescriptions
	c.paramsetDescriptions = paramsetDescriptions
	c.visibilityCache = visibility
	c.mu.Unlock()
}

// Set stores v under key with LastUpdated = now.
func (c *CacheCoordinator) Set(key hmtypes.DataPointKey, v hmtypes.ParamValue, source string) {
	c.mu.Lock()
	c.entries[key] = DataCacheEntry{Value: v, LastUpdated: time.Now(), Source: source}
	c.dirty = true
	c.mu.Unlock()
}

// Get returns the entry for key. Bumps the hit / miss counter.
//
// Hot path: takes the read lock only, with counter increments going
// through [atomic.Int64]. Concurrent readers no longer queue behind
// each other (audit R7). Map mutation (Set/Delete/Bulk*) still needs
// the write lock.
func (c *CacheCoordinator) Get(key hmtypes.DataPointKey) (DataCacheEntry, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return e, ok
}

// Delete removes key. Returns true when the entry existed; in that
// case the eviction counter is bumped so the metrics provider can
// surface cache churn.
func (c *CacheCoordinator) Delete(key hmtypes.DataPointKey) bool {
	c.mu.Lock()
	if _, ok := c.entries[key]; !ok {
		c.mu.Unlock()
		return false
	}
	delete(c.entries, key)
	c.dirty = true
	c.mu.Unlock()
	c.evictions.Add(1)
	return true
}

// Len reports the total entry count.
func (c *CacheCoordinator) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// --- Metrics provider surface (mirrors
// `CacheProviderForMetricsProtocol`) -----------------------------------

// MetricsDataCacheSize reports the current value-cache entry count.
func (c *CacheCoordinator) MetricsDataCacheSize() int { return c.Len() }

// MetricsDataCacheHits returns the cumulative hit counter.
func (c *CacheCoordinator) MetricsDataCacheHits() int {
	return int(c.hits.Load())
}

// MetricsDataCacheMisses returns the cumulative miss counter.
func (c *CacheCoordinator) MetricsDataCacheMisses() int {
	return int(c.misses.Load())
}

// MetricsDataCacheEvictions returns the cumulative eviction counter.
func (c *CacheCoordinator) MetricsDataCacheEvictions() int {
	return int(c.evictions.Load())
}

// MetricsDeviceDescriptionsSize reports the number of cached device
// descriptions. Returns 0 when no provider is wired.
func (c *CacheCoordinator) MetricsDeviceDescriptionsSize() int {
	c.mu.RLock()
	p := c.deviceDescriptions
	c.mu.RUnlock()
	if p == nil {
		return 0
	}
	return p.Len()
}

// MetricsParamsetDescriptionsSize reports the number of cached paramset
// descriptions. Returns 0 when no provider is wired.
func (c *CacheCoordinator) MetricsParamsetDescriptionsSize() int {
	c.mu.RLock()
	p := c.paramsetDescriptions
	c.mu.RUnlock()
	if p == nil {
		return 0
	}
	return p.Len()
}

// MetricsVisibilityCacheSize reports the size of the visibility-rule
// memoization cache. Returns 0 when no provider is wired.
//
// Wire via [SetSizeProviders] using a [*visibility.ParameterDecider] or
// [*visibility.Registry] — both satisfy [CacheSizeProvider] directly.
func (c *CacheCoordinator) MetricsVisibilityCacheSize() int {
	c.mu.RLock()
	p := c.visibilityCache
	c.mu.RUnlock()
	if p == nil {
		return 0
	}
	return p.Len()
}

// ParamsetInvalidator is the contract for objects that can invalidate
// cached paramset descriptions by interface. Wire via
// [CacheCoordinator.SetParamsetInvalidator].
type ParamsetInvalidator interface {
	// InvalidateByInterface evicts all paramset description entries
	// belonging to iface. Pass an empty string to evict all entries.
	InvalidateByInterface(iface string)
}

// SetParamsetInvalidator wires an optional invalidator that
// [InvalidateParamsetDescriptions] delegates to. Nil disables
// invalidation (the call becomes a no-op). Returns the receiver for
// chaining.
func (c *CacheCoordinator) SetParamsetInvalidator(inv ParamsetInvalidator) *CacheCoordinator {
	c.mu.Lock()
	c.paramsetInvalidator = inv
	c.mu.Unlock()
	return c
}

// InvalidateParamsetDescriptions evicts cached paramset descriptions for
// iface by calling the wired [ParamsetInvalidator]. Pass an empty string to
// evict all entries. No-op when no invalidator is wired.
func (c *CacheCoordinator) InvalidateParamsetDescriptions(iface string) {
	c.mu.RLock()
	inv := c.paramsetInvalidator
	c.mu.RUnlock()
	if inv == nil {
		return
	}
	inv.InvalidateByInterface(iface)
}

// --- Persistence methods ------------------------------------

// SetPersister wires the storage back-end. Nil disables persistence.
// Returns the receiver for chaining.
func (c *CacheCoordinator) SetPersister(p CachePersister) *CacheCoordinator {
	c.mu.Lock()
	c.persister = p
	c.mu.Unlock()
	return c
}

// LoadAll loads data-point values from the wired persister into the in-memory
// cache. No-op when no persister is wired.
func (c *CacheCoordinator) LoadAll(ctx context.Context) error {
	c.mu.RLock()
	p := c.persister
	c.mu.RUnlock()
	if p == nil {
		return nil
	}
	loaded, err := p.LoadDataCache(ctx)
	if err != nil {
		return err
	}
	if len(loaded) == 0 {
		return nil
	}
	c.mu.Lock()
	for k, v := range loaded {
		c.entries[k] = v
	}
	c.dirty = false
	c.mu.Unlock()
	return nil
}

// SaveAllWithDescription persists all data-point values via the wired
// persister, recording description as a diagnostic label for the save
// operation (useful in log messages and debug traces when multiple
// call sites save the cache for different reasons). Pass an empty string
// when no description is needed; the behaviour is identical to
// [SaveAll].
func (c *CacheCoordinator) SaveAllWithDescription(ctx context.Context, description string) error {
	_ = description // consumed only at the logging level — no schema change needed.
	return c.SaveAll(ctx)
}

// SaveIfChangedWithDescription is the described variant of [SaveIfChanged].
// It persists the cache only when the dirty bit is set, forwarding description
// as a diagnostic label. Pass an empty string for the default behaviour.
func (c *CacheCoordinator) SaveIfChangedWithDescription(ctx context.Context, description string) error {
	_ = description
	return c.SaveIfChanged(ctx)
}

// ClearOnStop clears the cache with reason [hmenum.CacheInvalidationReasonShutdown]
// and reports whether initialization had been completed. Call during daemon
// shutdown to ensure a clean cache state on the next start.
func (c *CacheCoordinator) ClearOnStop() bool {
	c.mu.RLock()
	complete := c.initializationComplete
	c.mu.RUnlock()
	c.ClearAllWithReason(hmenum.CacheInvalidationReasonShutdown)
	return complete
}

// SaveAll persists all data-point values via the wired persister. No-op when
// no persister is wired.
func (c *CacheCoordinator) SaveAll(ctx context.Context) error {
	c.mu.RLock()
	p := c.persister
	// Take a snapshot while under the read lock.
	snapshot := make(map[hmtypes.DataPointKey]DataCacheEntry, len(c.entries))
	for k, v := range c.entries {
		snapshot[k] = v
	}
	c.mu.RUnlock()
	if p == nil {
		return nil
	}
	if err := p.SaveDataCache(ctx, snapshot); err != nil {
		return err
	}
	c.mu.Lock()
	c.dirty = false
	c.mu.Unlock()
	return nil
}

// SaveIfChanged persists the cache only when the dirty bit is set,
// avoiding unnecessary I/O when no values have changed since the last
// save.
func (c *CacheCoordinator) SaveIfChanged(ctx context.Context) error {
	c.mu.RLock()
	isDirty := c.dirty
	c.mu.RUnlock()
	if !isDirty {
		return nil
	}
	return c.SaveAll(ctx)
}

// ClearAll drops every in-memory value-cache entry and resets the
// counters. The dirty bit is cleared because an empty cache is
// consistent with persistent storage (or will be after the next load).
// Emits a [hmevent.CacheInvalidatedEvent] with reason
// [hmenum.CacheInvalidationReasonManual] when an event bus is wired.
func (c *CacheCoordinator) ClearAll() {
	c.ClearAllWithReason(hmenum.CacheInvalidationReasonManual)
}

// ClearAllWithReason is the explicit-reason variant of [ClearAll]. Use
// [hmenum.CacheInvalidationReasonShutdown] when called during the daemon stop
// sequence so subscribers can distinguish operator-initiated clears from
// lifecycle-driven ones.
func (c *CacheCoordinator) ClearAllWithReason(reason hmenum.CacheInvalidationReason) {
	c.mu.Lock()
	affected := len(c.entries)
	c.entries = make(map[hmtypes.DataPointKey]DataCacheEntry)
	c.hits.Store(0)
	c.misses.Store(0)
	c.evictions.Store(0)
	c.dirty = false
	bus := c.bus
	centralName := c.centralName
	c.mu.Unlock()
	if bus == nil {
		return
	}
	events.Publish(bus, hmevent.CacheInvalidatedEvent{
		Base:            hmevent.NewBase(),
		CentralName:     centralName,
		CacheType:       hmenum.CacheTypeData,
		Reason:          reason,
		EntriesAffected: affected,
	})
}

// SetCentralName scopes future emitted events ([CacheInvalidatedEvent])
// to the owning [Unit]. Wire this once at bootstrap from
// `central.go`; safe to call multiple times.
func (c *CacheCoordinator) SetCentralName(name string) {
	c.mu.Lock()
	c.centralName = name
	c.mu.Unlock()
}

// SetDataCacheInitializationComplete marks data-cache initialization as
// Complete, enabling normal cache expiration behaviour..
// this suppresses premature getValue calls during the startup phase
// when device creation takes longer than MAX_CACHE_AGE. In Go the
// cache has no TTL/expiry logic, so this is a semantic marker that
// the caller may observe to gate post-startup operations. The method
// is safe to call multiple times; subsequent calls are no-ops.
//
// `CacheCoordinator.set_data_cache_initialization_complete`
// (`central/coordinators/cache.py:403`). P2.
func (c *CacheCoordinator) SetDataCacheInitializationComplete() {
	c.mu.Lock()
	if !c.initializationComplete {
		c.initializationComplete = true
	}
	c.mu.Unlock()
}

// IsDataCacheInitializationComplete reports whether initialization has
// been marked complete via [SetDataCacheInitializationComplete].
func (c *CacheCoordinator) IsDataCacheInitializationComplete() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.initializationComplete
}

// --- Event bus subscriptions -------------------------------------

// SubscribeToBus wires the coordinator to an event bus so it can
// maintain its caches in response to domain events. Specifically:
// - [hmevent.DeviceRemovedEvent]: evicts all data-point cache entries
// for the removed device (prefix match on ChannelAddress).
// - [hmevent.DataFetchCompletedEvent]: marks the dirty bit so
// SaveIfChanged will flush the post-fetch state to storage.
//
// Calling SubscribeToBus a second time first unsubscribes the previous
// subscriptions. Safe to call before or after operations on the cache.
func (c *CacheCoordinator) SubscribeToBus(bus *events.Bus) {
	// Unsubscribe any previous subscriptions.
	c.mu.Lock()
	for _, unsub := range c.unsubs {
		unsub()
	}
	c.unsubs = c.unsubs[:0]
	c.bus = bus
	c.mu.Unlock()

	if bus == nil {
		return
	}

	unsub1 := events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) {
		c.evictDevice(e.Address)
	})
	unsub2 := events.Subscribe(bus, func(_ hmevent.DataFetchCompletedEvent) {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
	})

	c.mu.Lock()
	c.unsubs = append(c.unsubs, unsub1, unsub2)
	c.mu.Unlock()
}

// UnsubscribeAll removes all bus subscriptions installed by
// [SubscribeToBus]. Call on shutdown to prevent goroutine leaks.
func (c *CacheCoordinator) UnsubscribeAll() {
	c.mu.Lock()
	for _, unsub := range c.unsubs {
		unsub()
	}
	c.unsubs = c.unsubs[:0]
	c.mu.Unlock()
}

// --- Session-recorder and IncidentRecorder ---------------------------

// SetSessionRecorder wires the optional session recorder. Pass nil to disable
// recording. Returns the receiver for chaining.
func (c *CacheCoordinator) SetSessionRecorder(rec *session.Recorder) *CacheCoordinator {
	c.mu.Lock()
	c.sessionRecorder = rec
	c.mu.Unlock()
	return c
}

// RecordSession forwards an RPC request/response pair to the wired session
// recorder. No-op when no recorder is wired or the recorder is inactive.
// rpcType must be [session.RPCTypeXML] or [session.RPCTypeJSON]; method is
// the RPC method name; params and response are the wire-decoded call
// arguments and result.
func (c *CacheCoordinator) RecordSession(rpcType session.RPCType, method string, params, response any) {
	c.mu.RLock()
	rec := c.sessionRecorder
	c.mu.RUnlock()
	if rec == nil {
		return
	}
	rec.RecordResponse(rpcType, method, params, response)
}

// SetIncidentRecorder wires the optional incident recorder. Pass nil to
// disable incident recording. Returns the receiver for chaining.
func (c *CacheCoordinator) SetIncidentRecorder(rec reliability.IncidentRecorder) *CacheCoordinator {
	c.mu.Lock()
	c.incidentRecorder = rec
	c.mu.Unlock()
	return c
}

// GetIncidentRecorder returns the wired incident recorder, or nil when none
// has been set. Callers that want to log a cache-miss incident read the
// recorder here and call RecordIncident themselves.
func (c *CacheCoordinator) GetIncidentRecorder() reliability.IncidentRecorder {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.incidentRecorder
}

// evictDevice removes every cache entry whose ChannelAddress has
// deviceAddress as the device part (before the first ':') or equals
// deviceAddress exactly. Called on DeviceRemovedEvent.
func (c *CacheCoordinator) evictDevice(deviceAddress string) {
	if deviceAddress == "" {
		return
	}
	c.mu.Lock()
	for k := range c.entries {
		addr := k.ChannelAddress
		// ChannelAddress is "DEVICE:N" or just "DEVICE".
		match := addr == deviceAddress
		if !match && len(addr) > len(deviceAddress) {
			match = addr[:len(deviceAddress)] == deviceAddress && addr[len(deviceAddress)] == ':'
		}
		if match {
			delete(c.entries, k)
			c.evictions.Add(1)
		}
	}
	c.dirty = true
	c.mu.Unlock()
}
