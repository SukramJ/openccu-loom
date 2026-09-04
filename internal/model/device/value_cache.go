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
	// For these two interfaces a per-parameter GetParamset fallback during init
	// cannot be trusted to return a device-fresh reading, so it is skipped
	// entirely: the bulk seeder gates its data points on a valid
	// LastTimestamp() and is the trustworthy source. The data point stays
	// unobserved (sentinel) until a real value arrives via the event callback
	// (#3228, #3260).
	//
	// What the CCU actually does, per interface:
	//
	//   - BidCos-RF: rfd answers a VALUES read from its own value store. It
	//     sends a get-request frame to the device only when the parameter
	//     declares one AND the device does not need waking — 282 `<get>`
	//     frames across 65 of the 127 shipped RF device types
	//     (RFPhysicalDataInterfaceCommand.cpp:147-176, the empty-frame
	//     return at :152 ahead of the RxNeedsWakeup branch at :166). For every
	//     other parameter the read is answered from the store, and a store
	//     miss is a hard failure, not a substituted value: HSSParamset::Get
	//     aborts on the first unreadable parameter (HSSParamset.cpp:162-176)
	//     and the call comes back as fault -1 "Failure"
	//     (rfd/XmlRpcMethods.cpp:228-244). The paramset DEFAULT is used only
	//     where the device XML sets use_default_on_failure, which in the
	//     shipped rftypes is 65 empty-string parameters across 56 files
	//     (HSSParameter.cpp:220-234, HSSLogicalType.cpp:25). So the risk here
	//     is not a plausible-looking placeholder marked valid, and it is not
	//     interface-wide either — this skip is broader than the firmware's own
	//     condition, which is (declares a get frame) AND (mains-powered or
	//     already stored).
	//   - VirtualDevices (e.g. heating groups) have no physical device behind
	//     them; their VALUES are aggregated by the CCU (e.g. 0 for a
	//     not-yet-measured ACTUAL_TEMPERATURE right after a CCU restart). That
	//     read path is served by the HmIP server's group stack, not by rfd,
	//     and it is unverified against any source — this leg of the rule is
	//     the reason to keep it, and the reason it cannot be narrowed yet.
	//
	// An earlier version of this comment justified the skip with a CCU-internal
	// placeholder "reported with *_STATUS = NORMAL so the status cannot be used
	// to reject it". That convention is an HmIP one: the `*_STATUS` ids in the
	// shipped RF device types are device-specific signals (LED_STATUS,
	// BACKLIGHT_AT_STATUS and similar), none of them a per-parameter validity
	// flag of the kind the HmIP paramsets carry.
	//
	// What would settle this properly: rfd tracks exactly the flag this skip is
	// guessing at — `undefined`, true from construction (HSSParameter.cpp:28)
	// and cleared only when a device frame delivered the value (:355) — and
	// exposes it through the three-argument getValue/getParamset form, which
	// answers {VALUE, UNDEFINED} (:252-279). Reading that flag replaces the
	// heuristic with the CCU's own verdict; widening or narrowing the interface
	// list without it would be a second guess.
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
