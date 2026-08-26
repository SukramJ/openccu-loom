// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ErrNoValueLoader is returned by [Device.LoadValue] when the device
// has no [ValueLoader] installed yet.
var ErrNoValueLoader = errors.New("device: no value loader configured")

// Cache TTLs — see the Python reference implementation's const.py:308
// (MAX_CACHE_AGE = 10s). Different paramsets have very different
// change frequencies, so we pick per-paramset defaults rather than a
// single shared constant.
const (
	// valuesCacheTTL is the TTL for VALUES-paramset entries. Zero means
	// "no expiry" — VALUES are kept fresh by push events from the CCU,
	// so the cache only avoids redundant cold reads. Push events
	// override the cache via [Device.OnObservedValue].
	valuesCacheTTL = 0

	// masterCacheTTL is the TTL for MASTER-paramset entries. MASTER is
	// configuration data that rarely changes, but the daemon should re-read
	// after a long uptime to catch operator changes performed directly on the
	// CCU.
	masterCacheTTL = 30 * time.Minute

	// sentinelCacheTTL is the TTL for "CCU returned nothing" entries.
	// Without this guard, every read of an unsupported parameter
	// would round-trip to the CCU forever. 5 minutes is long enough to
	// suppress retry storms but short enough that operator changes
	// (e.g. enabling an optional feature) become visible quickly.
	sentinelCacheTTL = 5 * time.Minute
)

// ValueLoader is the south-bound contract that [Device.LoadValue]
// uses to fetch values from the CCU. The concrete
// `internal/client/backends.Operations` satisfies this interface;
// tests pass a fake.
type ValueLoader interface {
	GetValue(ctx context.Context, address string, parameter hmenum.Parameter) (any, error)
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
}

// cacheEntry is one row in the per-device value cache.
type cacheEntry struct {
	value     any
	observed  bool // true → CCU returned a value; false → sentinel "nothing"
	writtenAt time.Time
}

// fresh reports whether entry is still valid given a TTL. Zero TTL is
// "never expires".
func (e cacheEntry) fresh(now time.Time, ttl time.Duration) bool {
	if ttl <= 0 {
		return true
	}
	return now.Sub(e.writtenAt) < ttl
}

// valueCache holds per-DataPointKey entries with paramset-scoped TTLs
// and uses a [singleflight.Group] to deduplicate concurrent loads for
// the same wire call. The keys mirror the LOAD path, not the cache
// path: a single MASTER paramset load is one wire call but fills many
// cache entries (every parameter on the channel) — so the singleflight
// key is `<channel>:M` for any MASTER load on that channel, and
// `<channel>:V:<param>` for a VALUES single-parameter load.
type valueCache struct {
	mu      sync.RWMutex
	entries map[hmtypes.DataPointKey]cacheEntry

	sf singleflight.Group

	// clock is overridden by tests; nil falls back to time.Now.
	clock func() time.Time
}

func newValueCache() *valueCache {
	return &valueCache{
		entries: make(map[hmtypes.DataPointKey]cacheEntry),
	}
}

func (c *valueCache) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// ttlFor returns the appropriate TTL for the given paramset key.
func ttlFor(paramsetKey hmenum.ParamsetKey, observed bool) time.Duration {
	if !observed {
		return sentinelCacheTTL
	}
	if paramsetKey == hmenum.ParamsetKeyMaster {
		return masterCacheTTL
	}
	return valuesCacheTTL
}

// get returns the cached entry for dpk if present AND fresh. Returns
// (value, observed, true) on a fresh hit; (nil, false, false) on miss
// or stale entry.
func (c *valueCache) get(dpk hmtypes.DataPointKey) (value any, observed, hit bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[dpk]
	if !ok {
		return nil, false, false
	}
	if !e.fresh(c.now(), ttlFor(dpk.ParamsetKey, e.observed)) {
		return nil, false, false
	}
	return e.value, e.observed, true
}

// put writes/replaces an entry. observed=false marks a sentinel ("CCU
// returned nothing for this parameter").
func (c *valueCache) put(dpk hmtypes.DataPointKey, value any, observed bool) {
	c.mu.Lock()
	c.entries[dpk] = cacheEntry{
		value:     value,
		observed:  observed,
		writtenAt: c.now(),
	}
	c.mu.Unlock()
}

// invalidate removes an entry, e.g. after a successful write that the
// caller knows changes the value. The next read will refetch.
func (c *valueCache) invalidate(dpk hmtypes.DataPointKey) {
	c.mu.Lock()
	delete(c.entries, dpk)
	c.mu.Unlock()
}

// SetValueLoader installs the south-bound loader. Called by the device
// pipeline once a backend is wired for the device's interface. Safe to
// call multiple times — replaces the previous loader.
func (d *Device) SetValueLoader(loader ValueLoader) {
	d.loaderMu.Lock()
	d.loader = loader
	if d.cache == nil {
		d.cache = newValueCache()
	}
	d.loaderMu.Unlock()
}

// ValueLoader returns the installed loader, or nil when none has been
// configured (e.g. in test fixtures).
func (d *Device) ValueLoader() ValueLoader {
	d.loaderMu.RLock()
	defer d.loaderMu.RUnlock()
	return d.loader
}

// LoadValue fetches the current value for dpk, using the per-device cache +
// singleflight to avoid redundant wire calls. Returns the value and true on
// success (CCU answered with a real value), or (nil, false, nil) when the CCU
// explicitly has no value for the parameter (sentinel — cached for
// [sentinelCacheTTL]). Returns an error for transport / loader failures.
//
// When direct=true the cache is bypassed both on read and write — used by the
// reconciler for forced refreshes.
//
// Both MASTER and VALUES loads are batched: a single GetParamset call fills
// every parameter on the channel into the cache and propagates it to the
// underlying [ParameterDataPoint]s via OnWireValue when present. VALUES sibling
// fills are gated on not-yet-observed so a bulk read never clobbers a restored
// value (see [Device.runLoadValuesParamset]).
//
// src is logged / surfaced in metrics as the trigger label (HM_INIT /
// MANUAL_OR_SCHEDULED). It does not change behaviour.
func (d *Device) LoadValue(ctx context.Context, dpk hmtypes.DataPointKey, src hmenum.CallSource, direct bool) (value any, observed bool, err error) {
	d.loaderMu.RLock()
	loader := d.loader
	cache := d.cache
	d.loaderMu.RUnlock()
	if loader == nil || cache == nil {
		return nil, false, ErrNoValueLoader
	}
	_ = src // surface as a logger / metrics tag — currently log-only via callers

	// 1) Cache hit (unless direct=true).
	if !direct {
		if v, obs, hit := cache.get(dpk); hit {
			return v, obs, nil
		}
	}

	// 2) Singleflight key: MASTER loads are channel-scoped (one
	// GetParamset call covers every parameter on the channel); VALUES
	// loads are parameter-scoped.
	sfKey := singleflightKey(dpk)

	// 3) Coalesce concurrent loads on the same key.
	_, sfErr, _ := cache.sf.Do(sfKey, func() (any, error) {
		return nil, d.runLoad(ctx, loader, cache, dpk, direct)
	})
	if sfErr != nil {
		return nil, false, sfErr
	}

	// 4) Re-read from cache. After a successful runLoad the entry must
	// exist (either with observed=true or as a sentinel).
	v, obs, hit := cache.get(dpk)
	if !hit {
		// Should not happen unless the load returned nil for this
		// specific dpk while populating only siblings (MASTER batch
		// where the caller asked for a parameter the CCU didn't ship).
		// Treat as sentinel.
		return nil, false, nil
	}
	return v, obs, nil
}

// singleflightKey builds the deduplication key for dpk. MASTER loads share one
// key per channel. VALUES loads stay parameter-scoped even though each one now
// GetParamsets the whole channel: a direct force-read of a single parameter
// must refresh exactly that parameter (the requested one is always applied),
// while sibling fills are gated on not-yet-observed — a per-channel key would
// let one parameter's forced refresh be coalesced away by another's.
func singleflightKey(dpk hmtypes.DataPointKey) string {
	if dpk.ParamsetKey == hmenum.ParamsetKeyMaster {
		return dpk.ChannelAddress + ":M"
	}
	return dpk.ChannelAddress + ":V:" + dpk.Parameter
}

// runLoad executes the actual wire call and seeds the cache. Always
// called from inside singleflight.Do so concurrent invocations for the
// same key are deduplicated. direct=true bypasses no-op short-circuits;
// the cache is always written.
func (d *Device) runLoad(ctx context.Context, loader ValueLoader, cache *valueCache, dpk hmtypes.DataPointKey, direct bool) error {
	if dpk.ParamsetKey == hmenum.ParamsetKeyMaster {
		return d.runLoadMaster(ctx, loader, cache, dpk)
	}
	return d.runLoadValuesParamset(ctx, loader, cache, dpk, direct)
}

// runLoadMaster fetches the entire MASTER paramset for the channel and
// fills every parameter into the cache. Also propagates each value to
// the underlying generic.DataPoint via OnWireValue when present.
func (d *Device) runLoadMaster(ctx context.Context, loader ValueLoader, cache *valueCache, dpk hmtypes.DataPointKey) error {
	values, err := loader.GetParamset(ctx, dpk.ChannelAddress, hmenum.ParamsetKeyMaster)
	if err != nil {
		// Sentinel for the requested parameter so the next read does
		// not retry-storm.
		cache.put(dpk, nil, false)
		return err
	}
	ch := d.Channel(dpk.ChannelAddress)
	for name, v := range values {
		entry := hmtypes.DataPointKey{
			InterfaceID:    dpk.InterfaceID,
			ChannelAddress: dpk.ChannelAddress,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      name,
		}
		cache.put(entry, v, true)
		if ch != nil {
			if dp := ch.MasterParameter(hmenum.Parameter(name)); dp != nil {
				if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
					setter.OnWireValue(v)
				}
			}
		}
	}
	// If the CCU did not include the requested parameter at all, mark
	// it as a sentinel so callers don't retry forever. Common for
	// CUxD virtual channels where the MASTER paramset is sparse.
	if _, ok := values[dpk.Parameter]; !ok {
		cache.put(dpk, nil, false)
	}
	return nil
}

// runLoadValuesParamset fetches the whole VALUES paramset for the channel in a
// single GetParamset call and seeds the cache for every returned parameter —
// not just the requested one. The CCU's bulk seed (fetch_all_device_data) only
// ships data points that already carry a non-zero value, so the per-parameter
// fallback is the path that fills the rest; batching it warms every
// not-yet-loaded sibling on the channel in one round-trip instead of one
// GetValue each.
//
// Propagation rules keep the restore-first / not-yet-measured guarantees
// (see docs/caching.md "Restored values and north-bound availability"):
//
//   - the explicitly requested parameter is always applied — this preserves the
//     single-GetValue force-read semantics (a direct refresh of a parameter
//     refreshes that parameter even if it was already observed);
//   - a sibling is only applied when it has not been observed yet, so a bulk
//     fill can never overwrite a restored / already-known value with a fresh
//     read that may be a not-yet-measured placeholder.
func (d *Device) runLoadValuesParamset(ctx context.Context, loader ValueLoader, cache *valueCache, dpk hmtypes.DataPointKey, direct bool) error {
	// For some interfaces a per-parameter GetParamset/GetValue fallback during
	// init cannot return a device-fresh reading — only a CCU-internal placeholder
	// (reported with *_STATUS = NORMAL so the status cannot be used to reject it),
	// which would be marked valid and thereby mask an actually uncertain state:
	//   - VirtualDevices (e.g. heating groups) have no physical device behind them;
	//     their VALUES are aggregated by the CCU (e.g. 0 for a not-yet-measured
	//     ACTUAL_TEMPERATURE right after a CCU restart).
	//   - BidCos-RF hosts passive/battery devices that cannot be actively queried;
	//     the fallback then returns the paramset default instead of a real reading.
	// The bulk seeder already gates these data points on a valid LastTimestamp()
	// and is the only trustworthy source for these interfaces, so skip the
	// per-parameter fallback entirely. The data point stays unobserved (sentinel)
	// until a real value arrives via the event callback (#3228, #3260).
	//
	// direct=true is excluded from the skip: it is only ever set by
	// scheduleSelfReload (callback_handlers.go), which fires right after a
	// live CCU push whose inline OnWireValue coercion failed — the CCU has
	// just sent a fresh value for this exact channel, so the placeholder
	// concern above does not apply. Applying the skip there silently wrote
	// the sentinel instead of resolving the reload, leaving the data point
	// permanently unobserved on every interface this skip covers.
	if !direct && (d.Interface == hmenum.InterfaceVirtualDevices || d.Interface == hmenum.InterfaceBidCosRF) {
		cache.put(dpk, nil, false)
		return nil
	}
	values, err := loader.GetParamset(ctx, dpk.ChannelAddress, hmenum.ParamsetKeyValues)
	if err != nil {
		// Sentinel for the requested parameter so the next read does not
		// retry-storm.
		cache.put(dpk, nil, false)
		return err
	}
	ch := d.Channel(dpk.ChannelAddress)
	for name, v := range values {
		requested := name == dpk.Parameter
		var setter interface{ OnWireValue(any) bool }
		observed := false
		if ch != nil {
			if dp := ch.Parameter(hmenum.Parameter(name)); dp != nil {
				setter, _ = dp.(interface{ OnWireValue(any) bool })
				if rv, ok := dp.(interface{ RawValue() (any, bool) }); ok {
					_, observed = rv.RawValue()
				}
			}
		}
		// Never let a sibling bulk-fill clobber an already-observed value.
		if !requested && observed {
			continue
		}
		entry := hmtypes.DataPointKey{
			InterfaceID:    dpk.InterfaceID,
			ChannelAddress: dpk.ChannelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      name,
		}
		cache.put(entry, v, true)
		if setter != nil {
			setter.OnWireValue(v)
		}
	}
	// If the CCU did not include the requested parameter at all, mark it as a
	// sentinel so callers don't retry forever (e.g. sparse CUxD channels).
	if _, ok := values[dpk.Parameter]; !ok {
		cache.put(dpk, nil, false)
	}
	return nil
}

// OnObservedValue is called by the event-bus subscriber whenever the
// CCU pushes a fresh value via the callback channel. The cache entry
// is overwritten so a subsequent LoadValue returns the live value
// without a wire round-trip. Idempotent.
func (d *Device) OnObservedValue(dpk hmtypes.DataPointKey, value any) {
	d.loaderMu.RLock()
	cache := d.cache
	d.loaderMu.RUnlock()
	if cache == nil {
		return
	}
	cache.put(dpk, value, true)
}

// InvalidateCache removes a single cache entry, used after a write that
// the caller knows changes the value. The next read repopulates.
func (d *Device) InvalidateCache(dpk hmtypes.DataPointKey) {
	d.loaderMu.RLock()
	cache := d.cache
	d.loaderMu.RUnlock()
	if cache == nil {
		return
	}
	cache.invalidate(dpk)
}

// relevantInitParameters is the set of VALUES-paramset parameters that the
// Python reference pre-fetches from channel 0 during the initial device load.
// These three parameters report transient link/config quality states that
// most devices carry on channel 0 and that cannot be relied on to arrive via
// CCU-pushed events in the short window between boot and the first subscriber
// attachment.
var relevantInitParameters = map[hmenum.Parameter]struct{}{
	hmenum.ParameterConfigPending: {},
	hmenum.ParameterStickyUnreach: {},
	hmenum.ParameterUnreach:       {},
}

// LoadInitialDataPoints is the selective boot-time loader. It mirrors the
// Python reference's `_ValueCache.init_base_data_points` specialisation:
// only channel-0 VALUES parameters listed in [relevantInitParameters] are
// fetched from the CCU during the HM_INIT pass. All MASTER parameters are
// covered by [LoadAllValues]; this method focuses on the narrow channel-0
// VALUES subset so battery-powered devices are not woken unnecessarily.
//
// The method is a targeted companion to [LoadAllValues] — callers that need
// the full fleet can call [LoadAllValues] afterwards; callers that want
// only the critical init subset call this method first.
//
// Returns the total counts (loaded successfully, errored) plus the first
// error encountered. Errors during individual loads do not abort the sweep.
func (d *Device) LoadInitialDataPoints(ctx context.Context, src hmenum.CallSource) (loaded, errored int, firstErr error) {
	if d == nil {
		return 0, 0, nil
	}
	// Walk every data point and select only those that match the init filter.
	for _, dp := range d.AllDataPoints() {
		if ctx.Err() != nil {
			return loaded, errored, ctx.Err()
		}
		if dp == nil {
			continue
		}
		dpk := dp.DataPointKey()
		// Only VALUES paramset — MASTER is covered separately.
		if dpk.ParamsetKey != hmenum.ParamsetKeyValues {
			continue
		}
		// Only parameters in the relevant-init set.
		if _, ok := relevantInitParameters[hmenum.Parameter(dpk.Parameter)]; !ok {
			continue
		}
		// Only channel-0 address (trailing ":0").
		if !isChannel0(dpk.ChannelAddress) {
			continue
		}
		// Skip non-readable parameters; LoadValue would fault on the CCU.
		if !dp.ParameterData().Operations.IsReadable() {
			continue
		}
		// Skip already-observed DPs.
		if _, observed := dp.RawValue(); observed {
			continue
		}
		if _, _, err := d.LoadValue(ctx, dpk, src, false); err != nil {
			errored++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		loaded++
	}
	return loaded, errored, firstErr
}

// isChannel0 reports whether the channel address ends with ":0".
func isChannel0(addr string) bool {
	n := len(addr)
	return n >= 2 && addr[n-2] == ':' && addr[n-1] == '0'
}

// LoadAllValues mirrors the Python reference's `Channel.load_values` /
// `DataPoint.load_data_point_value` boot pattern: walk every channel of
// the device, for each readable VALUES DataPoint that has not yet
// observed a wire-level value, issue a [Device.LoadValue] so the
// CCU-side current state lands in the cache before any north-bound
// subscriber (Matter Subscribe-Initial, MQTT discovery state, REST
// snapshot) reads it. Without this pass a freshly-booted daemon ships
// `null` for every DP whose CCU has not pushed an event since startup,
// and Apple Home's HAP-Service mapper either renders the accessory as
// "no value" or refuses to project it at all.
//
// Calls are sequential per device — the underlying singleflight
// already coalesces duplicate parameter loads on the same channel, and
// the CCU's XML-RPC backend handles dozens of getValue calls in tight
// succession without throttling. Callers run multiple devices in
// parallel via [golang.org/x/sync/errgroup] if higher throughput is
// needed.
//
// `direct=false` so the cache short-circuits on a hit — already-loaded
// DPs (from earlier sweeps or push events) cost nothing.
//
// Returns the total counts (loaded successfully, errored) plus the
// first error encountered. Errors during individual loads are logged
// by the caller, not surfaced as a hard stop — a single dead device
// must not block the rest of the fleet.
func (d *Device) LoadAllValues(ctx context.Context, src hmenum.CallSource) (loaded, errored int, firstErr error) {
	if d == nil {
		return 0, 0, nil
	}
	for _, dp := range d.AllDataPoints() {
		if ctx.Err() != nil {
			return loaded, errored, ctx.Err()
		}
		if dp == nil {
			continue
		}
		// Skip non-readable parameters; LoadValue would fault on the
		// CCU side with "Unknown Parameter".
		if !dp.ParameterData().Operations.IsReadable() {
			continue
		}
		// Skip if already observed — covers the steady-state case
		// where push events already populated the DP.
		if _, observed := dp.RawValue(); observed {
			continue
		}
		dpk := dp.DataPointKey()
		if _, _, err := d.LoadValue(ctx, dpk, src, false); err != nil {
			errored++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		loaded++
	}
	return loaded, errored, firstErr
}
