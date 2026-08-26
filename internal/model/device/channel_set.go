// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- Sentinel errors ---------------------------------------------------

// ErrNoChannelWriter is returned when Channel.Set / SetMany is called
// on a channel that has no writer configured.
var ErrNoChannelWriter = errors.New("channel: no writer configured")

// ErrNoChannelRefresher is returned when Channel.Refresh is called on
// a channel that has no refresher configured.
var ErrNoChannelRefresher = errors.New("channel: no refresher configured")

// ErrUnknownParameter is returned when the requested parameter is not
// present in the channel's paramset.
var ErrUnknownParameter = errors.New("channel: parameter not in paramset")

// ErrParameterNotWritable is returned when the data point reports
// non-writable through [writableReporter.IsWritable].
var ErrParameterNotWritable = errors.New("channel: parameter not writable")

// ErrChannelOperationLocked is returned when an operator has locked the
// channel against control writes (G12). It rejects VALUES-paramset writes
// only; MASTER/config writes and all reads are unaffected.
var ErrChannelOperationLocked = errors.New("channel: operation locked")

// ErrValidation wraps a client-side value rejection surfaced by Set/SetMany
// when opts.Validate is set (type / range / enum / length / writability). It
// lets callers such as the REST PUT handler tell a value the client got wrong
// — which never reached the wire — apart from a genuine upstream failure, and
// answer 4xx instead of 5xx. The underlying sentinel (e.g. parameter.ErrStringTooLong)
// stays reachable via errors.Is through the wrap chain.
// loom:reachable:reason="matched by the REST PUT /value handler (internal/north/rest/handlers/devices.go) via errors.Is to map a client-side validation rejection to 400 instead of a 502 upstream failure; the reachability heuristic does not follow sentinel-var references through errors.Is"
var ErrValidation = errors.New("channel: value rejected by validation")

// validateForSet runs the value validation shared by Set and SetMany. It uses
// ValidateWithDP when dp reports its own writability (so the IsForcedSensor
// overlay is honoured) and falls back to the descriptor-only Validate
// otherwise. Any rejection is wrapped in ErrValidation.
func validateForSet(dp any, desc hmproto.ParameterData, v hmtypes.ParamValue) error {
	var err error
	if wr, ok := dp.(parameter.WritabilityReporter); ok {
		err = parameter.ValidateWithDP(wr, desc, v, parameter.ValidateOptions{AllowSpecialValues: true})
	} else {
		err = parameter.Validate(desc, v)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return nil
}

// writableReporter is the narrow contract a [ParameterDataPoint]
// satisfies to participate in the early-reject gate. Every
// `*generic.DataPoint[T]` implements it through its `IsWritable`
// method. DPs that don't implement it (test fakes, future families)
// are treated as writable — the gate degrades gracefully.
type writableReporter interface {
	IsWritable() bool
}

// --- Interfaces --------------------------------------------------------

// ChannelWriter is the wire-level surface Channel.Set / SetMany need
// to dispatch commands. Implementations must be safe for concurrent
// use. A nil ChannelWriter causes Set/SetMany to return
// [ErrNoChannelWriter].
//
// The concrete production implementation is the [boundWriter] in the
// adapter package, which routes through the per-(central, interface)
// backend. The interface is kept small here so tests can supply a
// simple fake.
type ChannelWriter interface {
	// SetValue writes a single parameter value.
	SetValue(
		ctx context.Context,
		channelAddress string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error

	// PutParamset writes a batch of parameters atomically. The priority
	// hint is forwarded to the transport. Implementations that do not
	// support put_paramset should return [hmerr.ErrUnsupported] so
	// callers can fall back to sequential SetValue.
	PutParamset(
		ctx context.Context,
		channelAddress string,
		paramsetKey hmenum.ParamsetKey,
		values map[string]any,
		priority hmenum.CommandPriority,
	) error
}

// ChannelRefresher is the read-side surface Channel.Refresh needs.
// The concrete backend [backends.Operations] satisfies this interface
// (its GetParamset method has the matching signature).
type ChannelRefresher interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
}

// --- SetOptions --------------------------------------------------------

// SetOptions configures a single [Channel.Set] or [Channel.SetMany]
// call. The zero value gives sensible defaults: no validation,
// optimistic updates enabled, normal command priority, no echo wait.
// North-bound adapters should set Validate=true; trusted internal
// callers (coordinators, custom DPs) can leave it false for speed.
type SetOptions struct {
	// Validate enables pre-backend range/type/writability validation
	// via [parameter.Validate]. Default false.
	Validate bool

	// Optimistic enables Tracker-driven optimistic value application
	// before the backend round-trip. Default false (zero value).
	// Set true when the caller wants the data point to reflect the
	// new value immediately rather than waiting for the CCU echo.
	Optimistic bool

	// RxMode hints the underlying transport about the device's RX
	// window (BURST / WAKEUP). Empty means "default". Reserved for
	// future battery-device dispatch ordering — not yet consumed by
	// the transport layer.
	RxMode hmenum.CommandRxMode

	// Priority controls the throttle queue position. Default
	// CommandPriorityHigh (zero value of CommandPriority). CRITICAL
	// bypasses the burst window.
	Priority hmenum.CommandPriority

	// WaitForEcho blocks until the CCU echoes the new value back via
	// the callback channel (or ctx times out). Default false.
	// Requires the data point to satisfy the WaitForConfirmation
	// interface; ignored otherwise.
	WaitForEcho bool

	// Source is a free-form attribution tag for audit / observability
	// (e.g. "rest:PUT /devices/.../value", "mqtt:command", "ws:put").
	// Default empty.
	Source string
}

// --- Operator-assigned identity (name / rooms / functions / ise-id) -----
//
// These four carry CCU-owned operator state. The ingest pipeline seeds them
// while it builds the channel, and both the pipeline (on reconnect / hot-plug)
// and the rename path rewrite them while north-bound readers are serving
// requests, so every access goes through mu. The getters hand out copies of
// the slices: callers such as the alarm candidate scan and the payload
// assembler keep the result well past the call, and must not alias state the
// next re-ingest overwrites in place.

// Name returns the CCU-assigned channel name, or "" when the operator
// configured none.
func (c *Channel) Name() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.name
}

// SetName replaces the CCU-assigned channel name.
func (c *Channel) SetName(name string) {
	c.mu.Lock()
	c.name = name
	c.mu.Unlock()
}

// Rooms returns a copy of the channel's assigned room names.
func (c *Channel) Rooms() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.rooms)
}

// SetRooms replaces the channel's assigned room names with a copy of rooms.
func (c *Channel) SetRooms(rooms []string) {
	c.mu.Lock()
	c.rooms = slices.Clone(rooms)
	c.mu.Unlock()
}

// Functions returns a copy of the channel's assigned function (Gewerk) names.
func (c *Channel) Functions() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.functions)
}

// SetFunctions replaces the channel's assigned function names with a copy of
// functions.
func (c *Channel) SetFunctions(functions []string) {
	c.mu.Lock()
	c.functions = slices.Clone(functions)
	c.mu.Unlock()
}

// IseID returns the CCU-internal numeric identifier of this channel, or 0
// when the device-details cache had no entry for its address.
func (c *Channel) IseID() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.iseID
}

// SetIseID replaces the CCU-internal numeric identifier of this channel.
func (c *Channel) SetIseID(id int) {
	c.mu.Lock()
	c.iseID = id
	c.mu.Unlock()
}

// --- Wire dependency installers ----------------------------------------

// SetWriter installs the wire-level writer for Set / SetMany.
// Called by the device pipeline during hydration immediately after the
// data points are loaded. Subsequent calls replace the previous writer.
func (c *Channel) SetWriter(w ChannelWriter) {
	c.mu.Lock()
	c.writer = w
	c.mu.Unlock()
}

// Writer returns the installed [ChannelWriter], or nil when no writer
// has been configured. Custom-DP constructors call this to capture the
// write path without going through SetValue / Set.
// ChannelWriter is a superset of [generic.Writer] — a constructor that
// only needs SetValue can assign the result directly to a Writer field.
//
// The returned writer enforces the operator channel lock (see
// [operationLockedWriter]): a captured write path is the only thing a
// custom data point holds, so the lock has to travel with it rather than
// live in Set / SetMany alone. The wrapper is stateless apart from the
// channel reference and re-reads the lock on every call.
func (c *Channel) Writer() ChannelWriter {
	c.mu.RLock()
	w := c.writer
	c.mu.RUnlock()
	if w == nil {
		return nil
	}
	return &operationLockedWriter{origin: c, next: w}
}

// SetRefresher installs the read-side refresher for Refresh.
// Called by the device pipeline during hydration.
func (c *Channel) SetRefresher(r ChannelRefresher) {
	c.mu.Lock()
	c.refresher = r
	c.mu.Unlock()
}

// Refresher returns the installed [ChannelRefresher], or nil when no
// refresher has been configured. Symmetric to [Channel.Writer]; used by
// week-profile / schedule loaders that need to read the raw MASTER
// paramset without going through [Channel.Refresh] (which writes the
// values into the channel's DPs as a side effect).
func (c *Channel) Refresher() ChannelRefresher {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.refresher
}

// SetMasterRefreshHook installs a hook that is called non-blocking after
// every successful MASTER paramset write (Set or SetMany with
// [hmenum.ParamsetKeyMaster]). The hook receives the channel address and
// the paramset key so the caller can schedule a MasterPoller read-back.
// Pass nil to detach. Classic HM devices wire this to
// [backends.MasterPoller.SchedulePoll]; HmIP devices leave it nil
// because they use the CONFIG_PENDING True→False signal instead.
func (c *Channel) SetMasterRefreshHook(fn func(addr string, key hmenum.ParamsetKey)) {
	c.mu.Lock()
	c.masterRefreshHook = fn
	c.mu.Unlock()
}

// --- Set / SetMany / Get / GetAll / Refresh ----------------------------

// Set writes a single parameter value to the channel.
//
// Paramset key selects VALUES vs MASTER. The parameter must exist in
// the channel's paramset for that key; otherwise [ErrUnknownParameter]
// is returned.
//
// When an active [generic.CallParameterCollector] is found in ctx (via
// [generic.CollectorFromContext]) the value is added to the collector
// and no wire dispatch happens — the caller controls the Send. This
// lets a logical compound operation (e.g. LEVEL + LEVEL_2) be grouped
// into one put_paramset.
//
// When no collector is active Set dispatches directly via the
// configured ChannelWriter. With opts.Optimistic=true the data point's
// optimistic tracker is staged before the wire call; the tracker is
// rolled back automatically on wire failure.
func (c *Channel) Set(ctx context.Context, key hmenum.ParamsetKey, p hmenum.Parameter, v hmtypes.ParamValue, opts SetOptions) error {
	// Operator lock (G12): reject control writes to a locked channel before
	// touching the CCU. Scoped to the VALUES paramset so MASTER/config edits
	// still work.
	if key == hmenum.ParamsetKeyValues && c.IsLocked() {
		return ErrChannelOperationLocked
	}
	dp := c.paramsetParameterFast(key, p)
	if dp == nil {
		return ErrUnknownParameter
	}
	// Reject the write up-front when the DP itself reports
	// non-writable. The descriptor's WRITE bit is necessary but not
	// sufficient: the `_SWITCH_DP_TO_SENSOR` overlay (PR-8 / PR-10)
	// marks HmIP-eTRV.LEVEL / HmIP-HEATING.LEVEL as forced sensors,
	// at which point `dp.IsWritable()` flips to false even though
	// the descriptor still advertises WRITE. Without this gate the
	// write would travel to the CCU only to be rejected with -5.
	if w, ok := dp.(writableReporter); ok && !w.IsWritable() {
		return ErrParameterNotWritable
	}
	if opts.Validate {
		// Validate against the descriptor (type / range / enum / length) with
		// the IsForcedSensor overlay respected. A rejection here is a
		// client-side error that never reaches the wire; validateForSet wraps
		// it in ErrValidation so the caller can answer 4xx, not 5xx.
		if err := validateForSet(dp, dp.ParameterData(), v); err != nil {
			return err
		}
	}

	// Collector path — add to the active collector and return.
	if coll := generic.CollectorFromContext(ctx); coll != nil {
		if cdp, ok := dp.(generic.CollectableDataPoint); ok {
			return coll.Add(cdp, v.Unwrap(), 0)
		}
	}

	// Direct dispatch path.
	c.mu.RLock()
	w := c.writer
	c.mu.RUnlock()
	if w == nil {
		return ErrNoChannelWriter
	}

	wireVal := v.Unwrap()

	if opts.Optimistic {
		// Stage optimistic state before the wire call; roll back if
		// the call fails. Mirrors CallParameterCollector.Send().
		if cdp, ok := dp.(generic.CollectableDataPoint); ok {
			rb := cdp.ApplyOptimistic(wireVal)
			if err := c.dispatchSet(ctx, w, key, p, wireVal, opts.Priority); err != nil {
				if rb != nil {
					rb()
				}
				return err
			}
			if opts.WaitForEcho {
				waitForEcho(ctx, dp)
			}
			c.fireMasterHookIfMaster(key)
			return nil
		}
	}

	if err := c.dispatchSet(ctx, w, key, p, wireVal, opts.Priority); err != nil {
		return err
	}
	if opts.WaitForEcho {
		waitForEcho(ctx, dp)
	}
	c.fireMasterHookIfMaster(key)
	return nil
}

// SetMany writes a batch of parameter values atomically.
//
// When an active collector is found in ctx, every value is added to
// it — the caller controls the Send. When no collector is active,
// SetMany creates an internal collector backed by the channel's writer,
// enqueues every value, and calls Send immediately. The internal
// collector uses PutParamset when the writer supports it, otherwise
// falls back to sequential SetValue (same dispatch logic as
// [generic.CallParameterCollector]).
//
// All values must exist in the channel's paramset for key; the first
// unknown parameter causes an immediate [ErrUnknownParameter] return
// before any wire activity.
func (c *Channel) SetMany(ctx context.Context, key hmenum.ParamsetKey, values map[hmenum.Parameter]hmtypes.ParamValue, opts SetOptions) error {
	if len(values) == 0 {
		return nil
	}
	// Operator lock (G12): reject control writes to a locked channel. Scoped
	// to the VALUES paramset so MASTER/config edits still work.
	if key == hmenum.ParamsetKeyValues && c.IsLocked() {
		return ErrChannelOperationLocked
	}

	// Validate and look up every DP first, before touching any wire or
	// collector state.
	type entry struct {
		dp  ParameterDataPoint
		p   hmenum.Parameter
		val hmtypes.ParamValue
	}
	entries := make([]entry, 0, len(values))
	for p, v := range values {
		dp := c.paramsetParameterFast(key, p)
		if dp == nil {
			return ErrUnknownParameter
		}
		// Mirror SetValue's writable-gate: the descriptor's WRITE bit
		// is necessary but not sufficient — `_SWITCH_DP_TO_SENSOR`
		// overlays (HmIP-eTRV.LEVEL etc.) flip dp.IsWritable() to
		// false while leaving the descriptor unchanged. Without this
		// gate a SetMany batch would partially apply: optimistic
		// updates fire and the wire call returns -5 from the CCU.
		if w, ok := dp.(writableReporter); ok && !w.IsWritable() {
			return ErrParameterNotWritable
		}
		if opts.Validate {
			if err := validateForSet(dp, dp.ParameterData(), v); err != nil {
				return err
			}
		}
		entries = append(entries, entry{dp: dp, p: p, val: v})
	}
	// Stable order for deterministic collector grouping.
	sort.Slice(entries, func(i, j int) bool { return entries[i].p < entries[j].p })

	// Collector path: join or create.
	if coll := generic.CollectorFromContext(ctx); coll != nil {
		for _, e := range entries {
			if cdp, ok := e.dp.(generic.CollectableDataPoint); ok {
				if err := coll.Add(cdp, e.val.Unwrap(), 0); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Direct dispatch: create an internal collector backed by the
	// channel's writer wrapped as a CollectorBackend.
	c.mu.RLock()
	w := c.writer
	c.mu.RUnlock()
	if w == nil {
		return ErrNoChannelWriter
	}

	backend := &channelWriterBackend{w: w}
	coll := generic.NewCollector(backend, generic.WithPriority(opts.Priority))
	for _, e := range entries {
		if cdp, ok := e.dp.(generic.CollectableDataPoint); ok {
			if err := coll.Add(cdp, e.val.Unwrap(), 0); err != nil {
				return err
			}
		}
	}
	if err := coll.Send(ctx); err != nil {
		return err
	}
	c.fireMasterHookIfMaster(key)
	return nil
}

// Get returns the typed value and last-modified timestamp for the
// parameter in the given paramset. The third return value reports
// whether the parameter exists in the channel and has been observed.
//
// For VALUES, the returned value surfaces the optimistic (in-flight)
// value when one is active, matching [generic.DataPoint.Value].
// For MASTER, the seed value loaded at hydration time is returned.
func (c *Channel) Get(key hmenum.ParamsetKey, p hmenum.Parameter) (hmtypes.ParamValue, time.Time, bool) {
	dp := c.paramsetParameterFast(key, p)
	if dp == nil {
		return hmtypes.NoneValue(), time.Time{}, false
	}
	raw, ok := dp.RawValue()
	if !ok {
		return hmtypes.NoneValue(), dp.ModifiedAt(), false
	}
	pv, err := hmtypes.NewParamValue(raw)
	if err != nil {
		return hmtypes.NoneValue(), dp.ModifiedAt(), false
	}
	return pv, dp.ModifiedAt(), true
}

// GetAll returns a snapshot of every observed value in the paramset.
// Parameters that have not yet been observed (RawValue returns ok=false)
// are omitted. The returned map is safe for the caller to modify.
func (c *Channel) GetAll(key hmenum.ParamsetKey) map[hmenum.Parameter]hmtypes.ParamValue {
	dps := c.ParamsetDataPoints(key)
	out := make(map[hmenum.Parameter]hmtypes.ParamValue, len(dps))
	for _, dp := range dps {
		raw, ok := dp.RawValue()
		if !ok {
			continue
		}
		pv, err := hmtypes.NewParamValue(raw)
		if err != nil {
			continue
		}
		out[dp.Parameter()] = pv
	}
	return out
}

// Refresh re-reads the paramset from the CCU via the configured
// [ChannelRefresher] and pushes every returned value into the matching
// data points via [generic.DataPoint.OnWireValue]. Parameters that are
// not present in the channel's map are silently skipped.
//
// Refresh is uniform across ProductGroups. The AUTO-refresh trigger
// differs per product group (HmIP reacts to CONFIG_PENDING True→False;
// classic HM uses post-write MasterPoller.SchedulePoll) but both reach
// into this method as the common plumbing.
//
// Returns the underlying fetch error on failure.
func (c *Channel) Refresh(ctx context.Context, key hmenum.ParamsetKey) error {
	c.mu.RLock()
	r := c.refresher
	c.mu.RUnlock()
	if r == nil {
		return ErrNoChannelRefresher
	}

	values, err := r.GetParamset(ctx, c.Address, key)
	if err != nil {
		return err
	}

	for name, v := range values {
		dp := c.paramsetParameterFast(key, hmenum.Parameter(name))
		if dp == nil {
			continue
		}
		if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
			setter.OnWireValue(v)
		}
	}
	return nil
}

// --- internal helpers --------------------------------------------------

// paramsetParameterFast looks up a DP without going through the
// full public path (which takes an RLock). Since we're already going to
// take our own lock, we avoid nesting by calling the existing public
// methods which each take their own RLock — this is fine because
// sync.RWMutex is not reentrant but the public methods use separate
// RLock/RUnlock pairs.
// dispatchSet writes one parameter through the transport call that matches
// the paramset key. [ChannelWriter.SetValue] carries no paramset key and
// reaches the wire as xml-rpc setValue, which always targets VALUES — so a
// MASTER write has to go through PutParamset even though it is a batch of
// one. Sending it through SetValue instead makes the CCU answer fault -5, or
// silently apply a same-named VALUES parameter, while the master-refresh hook
// still fires as if the write had landed.
func (c *Channel) dispatchSet(
	ctx context.Context,
	w ChannelWriter,
	key hmenum.ParamsetKey,
	p hmenum.Parameter,
	wireVal any,
	priority hmenum.CommandPriority,
) error {
	if key == hmenum.ParamsetKeyValues {
		return w.SetValue(ctx, c.Address, p, wireVal, priority)
	}
	return w.PutParamset(ctx, c.Address, key, map[string]any{string(p): wireVal}, priority)
}

func (c *Channel) paramsetParameterFast(key hmenum.ParamsetKey, p hmenum.Parameter) ParameterDataPoint {
	switch key { //nolint:exhaustive // only VALUES + MASTER are stored on channels
	case hmenum.ParamsetKeyValues:
		return c.Parameter(p)
	case hmenum.ParamsetKeyMaster:
		return c.MasterParameter(p)
	}
	return nil
}

// waitForEcho attempts to wait for the CCU confirmation on dp. The
// data point must satisfy the WaitForConfirmation interface (which
// generic.DataPoint[T] does). If the interface is not satisfied or
// ctx is already done, the call returns immediately.
func waitForEcho(ctx context.Context, dp ParameterDataPoint) {
	type waiter interface {
		WaitForConfirmation(ctx context.Context) error
	}
	if w, ok := dp.(waiter); ok {
		_ = w.WaitForConfirmation(ctx)
	}
}

// fireMasterHookIfMaster invokes the masterRefreshHook when key is MASTER.
// The hook is called non-blocking: the current goroutine continues without
// waiting for the poll to complete. Callers that supply a nil hook (HmIP
// devices, read-only channels) are no-ops. Only MASTER writes trigger the
// hook; VALUES writes are handled via the CONFIG_PENDING event path.
func (c *Channel) fireMasterHookIfMaster(key hmenum.ParamsetKey) {
	if key != hmenum.ParamsetKeyMaster {
		return
	}
	c.mu.RLock()
	hook := c.masterRefreshHook
	c.mu.RUnlock()
	if hook == nil {
		return
	}
	addr := c.Address
	go hook(addr, key)
}

// channelWriterBackend adapts a [ChannelWriter] to the
// [generic.CollectorBackend] interface so the internal SetMany
// collector can dispatch through it. Priority comes from the collector
// configuration (set at construction time via [generic.WithPriority]).
//
// The adapter is used only within SetMany's internal-collector path;
// it is not exported.
type channelWriterBackend struct {
	w ChannelWriter
}

func (b *channelWriterBackend) SetValue(
	ctx context.Context,
	channelAddress string,
	param hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	return b.w.SetValue(ctx, channelAddress, param, value, priority)
}

func (b *channelWriterBackend) PutParamset(
	ctx context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	priority hmenum.CommandPriority,
) error {
	return b.w.PutParamset(ctx, channelAddress, paramsetKey, values, priority)
}
