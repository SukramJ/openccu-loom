// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/optimistic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Writer is the outbound-command contract every generic data point
// needs. It mirrors the shape the custom packages already use, so
// tests and production code reuse the same stub/implementations.
type Writer interface {
	SetValue(
		ctx context.Context,
		channelAddress string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}

// ParamsetWriter is the optional extension a [Writer] may implement to
// dispatch atomic multi-parameter writes through one CCU
// `put_paramset` call. Custom data points that aggregate multiple
// wire parameters (Climate.SetAway, Light.TurnOnWith, Siren.TurnOn,
// TextDisplay.WriteWithSound, …) prefer PutParamset when available so
// The CCU applies the change atomically — mirrors
// `bind_collector` decorator behaviour.
//
// The contract: PutParamset writes the entire `values` map in one
// CCU call; the CCU either accepts the whole batch or rejects it.
// Concrete Writer implementations that don't support put_paramset
// (or that simply don't override) cause callers to fall back to
// sequential SetValue calls.
type ParamsetWriter interface {
	Writer
	PutParamset(
		ctx context.Context,
		channelAddress string,
		paramsetKey hmenum.ParamsetKey,
		values map[string]any,
		priority hmenum.CommandPriority,
	) error
}

// Spec is the common construction record for every generic data
// point. Concrete types wrap it in their own New* constructor.
type Spec struct {
	Key        hmtypes.DataPointKey
	Descriptor hmproto.ParameterData
	Writer     Writer

	// CentralName is the CentralUnit name used to scope the data
	// point's [datapoint.BaseDataPointFields.UniqueID]. Empty is
	// permitted at the type level (test fixtures) but production
	// callers MUST set it; otherwise two CCUs with the same channel
	// address would produce colliding UniqueIDs.
	//
	// The field is named `CentralName` rather than `Central` to avoid
	// a promotion-ambiguity with [datapoint.BaseDataPointFields.Central],
	// which exposes the same string as a method.
	CentralName string

	// Usage classifies the data point's role for north-bound rendering (primary
	// state vs. diagnostic vs. internal). Empty defaults to
	// [hmenum.DataPointUsageDataPoint] — the catch-all for plain
	// readable/writable parameters.
	Usage hmenum.DataPointUsage

	// Kind is the resolved generic shape (Switch, BinarySensor, Sensor, Number,
	// Select, …). Used by [DataPoint.Category] to derive the DataPointCategory
	// dynamically — when the DP is later flagged via
	// [BaseDataPointFields.MarkForcedSensor] the Category response flips to
	// Sensor regardless of the original Kind.
	//
	// Empty (KindUnknown) is fine for test fixtures; production pipeline call
	// sites set it from the resolver's classification.
	Kind ResolvedKind

	// DeviceModel is the parent device's CCU model name. Used by
	// [DataPoint.Quantity] for the device-aware quantity lookup
	// without it, binary sensors with per-model overrides
	// (HmIP-SWDO.STATE → window) fall back to QuantityNone.
	DeviceModel string

	// KeyNameOverride substitutes a custom keyName segment in the
	// embedded [datapoint.BaseDataPointFields.UniqueID]. Empty falls
	// back to [Spec.Key.Parameter] (the wire-level parameter name).
	//
	// Used by [calculated] sensors so the inner DataPoint's UniqueID
	// renders as `<central>:<channel>:CALCULATED/<param>` instead of
	// the bare wire parameter name. Avoids the dual-embed pattern
	// where calculated sensors had to embed BaseDataPointFields a
	// second time at the outer struct just to override the UniqueID
	// shape — the outer embed introduced a hidden bug where
	// [datapoint.BaseDataPointFields.MarkForcedSensor] on the outer
	// had no effect on the inner [DataPoint.Usage] / [DataPoint.Category].
	KeyNameOverride string

	// OptimisticDisabled turns off the optimistic-update tracker for this data
	// point. Default false → tracking enabled.
	OptimisticDisabled bool

	// OptimisticTimeout overrides the default rollback grace period.
	// Zero falls back to [OptimisticDefaultTimeout] (30 s).
	OptimisticTimeout time.Duration

	// OptimisticBurstWindow is the time window within which consecutive
	// Apply calls are considered a burst and share a single rollback
	// anchor. Zero falls back to the package default (500 ms, matching
	// Setting
	// this to a large value means rapid-fire SetValue calls for the
	// same data point always share one anchor; a very small value (e.g.
	// 1 ns) effectively disables time-bounded burst grouping so each
	// Apply gets its own anchor.
	OptimisticBurstWindow time.Duration

	// RetryableOverride sets the _retryable flag explicitly. The
	// zero value (false) means "use the per-Kind default": true for
	// all non-action kinds, false for Actions and Buttons. Set this to
	// true only when a normally non-retryable DP should allow retries
	// (uncommon).
	RetryableOverride bool

	// ValidateStateChangeOverride, when true, forces is_state_change
	// validation even for Action kinds (normally skipped). The zero
	// value uses the per-Kind default: true for all non-action kinds.
	ValidateStateChangeOverride bool

	// Translation is the locale-aware human-readable label for this parameter.
	// Set by the device-ingest pipeline from the CCU translations catalogue
	// (ccudata.Translations.ParameterLabel).
	Translation string

	// Description is the help/tooltip text for this parameter from the CCU
	// translations catalogue (ccudata.Translations.ParameterHelpText). Empty
	// when the translations catalogue has no entry.
	Description string

	// ValueTranslations maps each value-list entry (ENUM value string) to its
	// human-readable locale translation. Set by the ingest pipeline via
	// ccudata.Translations.ParameterValue for each element in
	// Descriptor.ValueList. Nil when the parameter has no VALUE_LIST.
	ValueTranslations map[string]string

	// IsHmtype marks this as a native HomeMatic-protocol data point one that
	// originated from a real CCU XML-RPC/BIN-RPC paramset rather than being
	// synthesised by the daemon (calculated, combined, custom). Set to true by
	// the device-ingest pipeline for all real protocol DPs; left false for hub /
	// custom / calculated.
	IsHmtype bool

	// NoPushUpdates marks this data point's backend interface as one
	// that does NOT deliver VALUES updates via event callbacks — the
	// CCU-side interface cannot push. When true, [RequiresPolling]
	// Returns true for VALUES parameters
	// `not client.capabilities.push_updates` branch (data_point.py:1033).
	//
	// The field is intentionally inverted ("No" prefix) so that the
	// zero value (false) means "push is active" — which is the correct
	// default for all existing tests and production DPs that do not
	// explicitly opt out. Setting NoPushUpdates = true is the pipeline's
	// responsibility for BIN-RPC CUxD and any other poll-only interface.
	//
	// replaces the unconditional MASTER-only heuristic with
	// the two-part Python condition
	// requires_polling = !push_updates || (HM/HMW && MASTER)
	NoPushUpdates bool
}

// DataPoint is the generic core every concrete data-point type
// composes. It tracks the last observed value, optimistic-update
// bookkeeping, and the common subscription hooks.
//
// T is the concrete Go type of the parameter (bool, int32, float64,
// string). Concrete types expose typed public methods that delegate
// to [DataPoint.setValue] / [DataPoint.observe].
//
// `comparable` is required because the optimistic tracker compares
// applied vs. confirmed values for burst-skip and value-mismatch
// detection. All wire types we care about (bool, int32, int64,
// float64, string) satisfy comparable.
//
// DataPoint embeds [datapoint.BaseDataPointFields] so
// the canonical [datapoint.BaseDataPointFields.UniqueID],
// [datapoint.BaseDataPointFields.SetForcedUsage]
// [datapoint.BaseDataPointFields.ForcedUsage], and
// [datapoint.BaseDataPointFields.SetPublisher]
// [datapoint.BaseDataPointFields.PublishUpdate] surfaces are
// promoted into every concrete generic data-point type. The
// embedded struct's lock is independent of [DataPoint.mu]; both
// have their own internal `mu sync.RWMutex` and the unexported
// names cannot collide across packages.
type DataPoint[T comparable] struct {
	datapoint.BaseDataPointFields

	Spec

	// ServiceRegistry implements the write-half of [payload.Source].
	// Concrete wrapper types (Switch, Float, Select, Button, Text, …)
	// register their typed Set / TurnOn / Press / SelectOption / SetText
	// methods in their constructor; pure read types (Sensor, BinarySensor)
	// leave it empty and inherit a no-op write surface.
	payload.ServiceRegistry

	mu          sync.RWMutex
	value       T
	observed    bool
	modifiedAt  time.Time
	refreshedAt time.Time

	// source tracks the wire-side lifecycle of the current value:
	// unobserved (no value yet), cache (restored at boot), live
	// (confirmed by a CCU event with a healthy connection), or stale
	// (last live value, connection lost). REST surfaces this on every
	// data-point read; MQTT republishes on every transition so
	// consumers see the freshness change. See ADR 0018.
	source hmenum.ValueSource

	// unconfirmed value slot, stored separately from the confirmed value. A nil
	// pointer means no unconfirmed value is pending; a non-nil pointer means an
	// optimistic write has been staged. The base [Value] method returns the
	// unconfirmed value when present. Reset when a CCU-confirmed value arrives
	// (write_value path) or when the unconfirmed value is explicitly cleared.
	unconfirmedValue    *T
	unconfirmedObserved bool

	status         hmenum.ParameterStatus
	statusObserved bool

	// status parameter auto-detection slots. These are populated by
	// [SetStatusParameter] / [SetStatusParameterWithValueList] during device
	// construction and reset on device removal.
	statusParameter string
	statusParamSet  bool

	// cached VALUE_LIST for the paired status parameter. Used by [UpdateStatus]
	// to map integer CCU-index updates to string ParameterStatus values.
	statusValueList []string

	// last non-default value slot. Updated by [OnEvent] when the confirmed value
	// differs from the descriptor's DEFAULT. Used for "restore last value"
	// scenarios (e.g. dimmer restore after power-on).
	lastNonDefaultValue    *T
	lastNonDefaultObserved bool

	// paramDefault is the typed representation of the descriptor DEFAULT field,
	// parsed once at construction. Nil when the descriptor carries no DEFAULT.
	// Used by [OnEventAt] to skip updating [lastNonDefaultValue] when the
	// incoming value equals the parameter's default.
	paramDefault *T

	// cached enum-value-is-index flag. True when the CCU expects integer indices
	// for ENUM parameters (HM devices), false when it expects string values
	// (HmIP devices). Set once at construction from
	// [Spec.Descriptor.EnumValueIsIndex] and never mutated.
	enumValueIsIndex bool

	// cached ignore-on-initial-load flag. When true, load_data_point_value skips
	// the RPC call on HM_INIT / HA_INIT call sources and only reads from cache —
	// avoiding wake-ups of battery-powered devices. Set from
	// [hmenum.CheckIgnoreParameterOnInitialLoad] during construction.
	ignoreOnInitialLoad bool

	optimistic *optimistic.Tracker[T]

	// _retryable flag. False means the caller should not retry a failed write
	// for this data point. Applies to DpAction / DpButton where a repeated send
	// could trigger the action twice. Set via [Spec.RetryableOverride];
	// defaults to true for all other kinds.
	retryable bool

	// _validate_state_change flag. When false the is_state_change check is
	// skipped before sending — used by Actions where every send must go through
	// even if the new value equals the old one.
	validateStateChange bool

	// enabledByChannelOperationMode is the tri-state gate written by the device
	// pipeline's `applyChannelOperationModeGating` pass. Nil means "no
	// CHANNEL_OPERATION_MODE constraint observed" False means the current
	// operation mode excludes this parameter Usage returns NoCreate. True means
	// the mode explicitly includes it. Protected by mu.
	enabledByChannelOperationMode *bool

	// _state_uncertain initial-true invariant. A new DP is uncertain until at
	// least one CCU-confirmed value arrives via [OnEvent] or [MarkRefreshed].
	// The field is distinct from optimistic.IsActive() which tracks
	// write-in-flight uncertainty. [StateUncertain] ORs both: stateUncertain ||
	// optimistic.IsActive(). Protected by mu.
	stateUncertain bool

	rollbackCallbacks []func(reason RollbackReason, rolledBack, restored T, restoredSet bool)

	updateCallbacks []func(old, next T)
	// confirmedUpdateCallbacks fires ONLY from [OnEvent] (CCU-confirmed
	// value transitions) and ONLY when the new value differs from the
	// previously confirmed one (or no value had been observed yet). The
	// Matter Subscribe engine subscribes here instead of [OnUpdate] so
	// optimistic Apply / rollback transitions do not generate spurious
	// ReportData. Apple Home's HAP-Mapper does its own optimistic UI
	// while waiting for the next Subscribe report; pushing optimistic
	// values plus the 30 s rollback would make the user-visible state
	// flip back unprompted.
	confirmedUpdateCallbacks []func(old, next T)
	statusCallbacks          []func(old, next hmenum.ParameterStatus)
	removedCbs               []func()
}

// NewDataPoint constructs a generic data point. The
// [datapoint.BaseDataPointFields] fields are derived from `cfg`:
// `Central` from [Spec.CentralName], `Address` from
// `Spec.Key.ChannelAddress`, `KeyName` from
// [Spec.KeyNameOverride] when non-empty, else from
// `Spec.Key.Parameter`. Constructor signature is unchanged for
// backwards compatibility — callers that want a meaningful UniqueID
// set [Spec.CentralName].
func NewDataPoint[T comparable](cfg Spec) *DataPoint[T] {
	keyName := cfg.Key.Parameter
	if cfg.KeyNameOverride != "" {
		keyName = cfg.KeyNameOverride
	}
	// per-Kind defaults for retryable / validateStateChange.
	// Actions and Buttons do not retry (a repeated press could fire twice)
	// and do not validate state change (every send must go through even
	// when value == old). All other kinds are retryable and validate by
	// default.
	isAction := cfg.Kind.IsAction()
	retryable := !isAction || cfg.RetryableOverride
	validateSC := !isAction || cfg.ValidateStateChangeOverride

	dp := &DataPoint[T]{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(
			cfg.CentralName,
			cfg.Key.ChannelAddress,
			keyName,
		),
		Spec:                cfg,
		enumValueIsIndex:    enumValueIsIndexFromDescriptor(cfg.Descriptor),
		ignoreOnInitialLoad: hmenum.Parameter(cfg.Key.Parameter).IgnoreOnInitialLoad(),
		optimistic:          optimistic.New[T](nil),
		retryable:           retryable,
		validateStateChange: validateSC,
		// initial state is uncertain until the first CCU-confirmed value arrives.
		stateUncertain: true,
	}
	if len(cfg.Descriptor.Default) > 0 {
		var raw any
		if err := json.Unmarshal(cfg.Descriptor.Default, &raw); err == nil {
			if typed, ok := coerceWire[T](raw); ok {
				dp.paramDefault = &typed
			}
		}
	}
	return dp
}

// DataPointKey returns the composite identifier. Returns the zero
// key when the receiver is nil — custom DPs that embed *Float /
// *Integer / *Switch via channel-parameter lookups (e.g.
// [custom.FloatField]) carry a nil embed when the channel materialised
// without that parameter. The auto-generated method promotion would
// otherwise dispatch into a nil receiver and crash the materializer
// in [custom.lookupProfileForCustomDP]; the zero key falls through
// that caller's "channelAddr == ”" early-return per its docstring
// contract.
func (d *DataPoint[T]) DataPointKey() hmtypes.DataPointKey {
	if d == nil {
		return hmtypes.DataPointKey{}
	}
	return d.Key
}

// Parameter returns the wire-level parameter name.
func (d *DataPoint[T]) Parameter() hmenum.Parameter {
	return hmenum.Parameter(d.Key.Parameter)
}

// ParameterData returns the CCU-level parameter description. Callers
// use it to inspect bounds, value lists, and flags.
func (d *DataPoint[T]) ParameterData() hmproto.ParameterData { return d.Descriptor }

// HasEvents reports whether the parameter advertises EVENT in its operations
// bitmask.
func (d *DataPoint[T]) HasEvents() bool { return d.Descriptor.Operations.IsEvent() }

// IsReadable reports whether the parameter advertises READ in its operations
// bitmask.
func (d *DataPoint[T]) IsReadable() bool { return d.Descriptor.Operations.IsReadable() }

// IsWritable reports whether the parameter advertises WRITE in its operations
// bitmask AND has not been demoted to a read-only sensor surface via
// [MarkForcedSensor].
func (d *DataPoint[T]) IsWritable() bool {
	if d.IsForcedSensor() {
		return false
	}
	return d.Descriptor.Operations.IsWritable()
}

// UpdateDescriptor swaps the cached parameter descriptor in place.
// Used after a firmware-driven paramset refresh
// the equivalent `update_parameter_data` so widgets re-render with
// new bounds / value-list / unit without recreating the data point.
// The previous descriptor is overwritten atomically — concurrent
// callers see either the old or the new full descriptor, never a
// half-written state.
func (d *DataPoint[T]) UpdateDescriptor(desc hmproto.ParameterData) {
	d.mu.Lock()
	d.Descriptor = desc
	d.mu.Unlock()
}

// Status returns the last observed [hmenum.ParameterStatus] for the paired
// `*_STATUS` parameter (NORMAL, UNKNOWN, OVERFLOW, …) plus whether one has
// been observed yet.
func (d *DataPoint[T]) Status() (hmenum.ParameterStatus, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.status, d.statusObserved
}

// UpdateStatus records the current parameter status. Empty / unknown status
// strings are silently ignored so a malformed CCU value does not clobber the
// cached status. Subscribers registered via [OnStatusChange] are only
// notified when the status actually changes.
func (d *DataPoint[T]) UpdateStatus(s hmenum.ParameterStatus) {
	if s == "" {
		return
	}
	d.mu.Lock()
	if d.statusObserved && d.status == s {
		d.mu.Unlock()
		return
	}
	old := d.status
	d.status = s
	d.statusObserved = true
	cbs := make([]func(old, next hmenum.ParameterStatus), len(d.statusCallbacks))
	copy(cbs, d.statusCallbacks)
	d.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(old, s)
		}
	}
}

// UpdateStatusFromWire accepts a raw wire value for the paired status
// parameter — either a [hmenum.ParameterStatus] string or an integer CCU
// index — and routes to [UpdateStatus] after resolving the index via the
// cached [statusValueList].
//
// if isinstance(status_value, int) and self._status_value_list: status_value
// = self._status_value_list[status_value] if isinstance(status_value, str)
// and status_value in ParameterStatus: new_status =
// ParameterStatus(status_value)
//
// Invalid indices and unrecognised string values are silently ignored.
func (d *DataPoint[T]) UpdateStatusFromWire(rawValue any) {
	d.mu.RLock()
	vl := d.statusValueList
	d.mu.RUnlock()

	var s string
	switch v := rawValue.(type) {
	case int:
		if vl != nil && v >= 0 && v < len(vl) {
			s = vl[v]
		}
	case int32:
		if vl != nil && int(v) >= 0 && int(v) < len(vl) {
			s = vl[int(v)]
		}
	case int64:
		if vl != nil && int(v) >= 0 && int(v) < len(vl) {
			s = vl[int(v)]
		}
	case string:
		s = v
	case hmenum.ParameterStatus:
		d.UpdateStatus(v)
		return
	}
	if s == "" {
		return
	}
	// Validate that the string is a known ParameterStatus value.
	ps := hmenum.ParameterStatus(s)
	switch ps {
	case hmenum.ParameterStatusNormal,
		hmenum.ParameterStatusUnknown,
		hmenum.ParameterStatusOverflow,
		hmenum.ParameterStatusUnderflow,
		hmenum.ParameterStatusError,
		hmenum.ParameterStatusInvalid,
		hmenum.ParameterStatusUnused,
		hmenum.ParameterStatusExternal:
		d.UpdateStatus(ps)
	}
}

// OnStatusChange registers a callback fired by [UpdateStatus] on
// every actual status transition. Returns an idempotent unsubscribe
// closure.
func (d *DataPoint[T]) OnStatusChange(fn func(old, next hmenum.ParameterStatus)) func() {
	if fn == nil {
		return func() {}
	}
	d.mu.Lock()
	d.statusCallbacks = append(d.statusCallbacks, fn)
	idx := len(d.statusCallbacks) - 1
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if idx < len(d.statusCallbacks) {
				d.statusCallbacks[idx] = nil
			}
		})
	}
}

// WriteUnconfirmedValue stores v in the **separate** unconfirmed value slot,
// leaving the CCU-confirmed value untouched.
//
// 1. Clears the previous unconfirmed value. 2. Computes old_value from the
// blended `_value` property. 3. Stores the new value in `_unconfirmed_value`.
// 4. Stamps the unconfirmed modified/refreshed timestamps (blend). 5.
// Publishes an updated event with old/new payload.
//
// The change: the value is now stored in `unconfirmedValue` (a separate
// pointer slot), NOT in `value`. [Value] returns the unconfirmed value when
// present; [ConfirmedValue] always returns the CCU-confirmed value. [OnEvent]
// resets the unconfirmed slot when a CCU confirmation arrives.
//
// `writeAt` lets callers stamp the change with the time they actually issued
// the wire write. Pass time.Time{} to use time.Now().
func (d *DataPoint[T]) WriteUnconfirmedValue(v T, writeAt time.Time) {
	d.IncInFlightCommands()
	if writeAt.IsZero() {
		writeAt = time.Now()
	}
	d.mu.Lock()
	// old value is the blended value (unconfirmed wins over confirmed when set)
	var old T
	if d.unconfirmedValue != nil {
		old = *d.unconfirmedValue
	} else {
		old = d.value
	}
	hadOld := d.unconfirmedObserved || d.observed
	d.unconfirmedValue = &v
	d.unconfirmedObserved = true
	// When the optimistic value differs from the prior blended value the
	// state is no longer authoritative until a CCU echo confirms it. Re-arm
	// the uncertainty flag (Python `data_point.py:1241` sets the same field
	// in the value-diff branch of `write_unconfirmed_value`). UI consumers
	// gating on [StateUncertain] see the correct signal after a burst write.
	valueDiff := !hadOld || !valuesClose(old, v)
	if valueDiff {
		d.stateUncertain = true
	}
	cbs := make([]func(old, next T), len(d.updateCallbacks))
	copy(cbs, d.updateCallbacks)
	d.mu.Unlock()

	// Update the unconfirmed timestamps on the embedded base fields.
	if valueDiff {
		d.MarkUnconfirmedModified(writeAt)
	} else {
		d.MarkUnconfirmedRefreshed(writeAt)
	}

	for _, cb := range cbs {
		if cb != nil {
			cb(old, v)
		}
	}
}

// resetUnconfirmedValue clears the unconfirmed value slot and the unconfirmed
// timestamps. Called by [OnEvent] when a CCU-confirmed value arrives.
func (d *DataPoint[T]) resetUnconfirmedValue() {
	d.mu.Lock()
	hadUnconfirmed := d.unconfirmedValue != nil
	d.unconfirmedValue = nil
	d.unconfirmedObserved = false
	d.mu.Unlock()
	d.ResetUnconfirmedTimestamps()
	if hadUnconfirmed {
		d.DecInFlightCommands()
	}
}

// Usage returns the data point's classification for north-bound rendering.
//
// 1. `is_forced_sensor` / `is_un_ignored` → DATA_POINT (the
// `_SWITCH_DP_TO_SENSOR` overlay PR-8/PR-10 promotes the DP to a read-only
// sensor surface; the operator-un-ignore flag PR-12 overrides any
// visibility-driven NoCreate). 2. Explicit forced-usage installed via
// [SetForcedUsage] wins the materializer's `Visible(...)` / `Hidden(...)`
// marks come in here, and so does the [SuppressUndefinedGenericDataPoints]
// pass that promotes unmarked generic DPs on custom-DP devices to NoCreate
// (PR-6). 3. Constructor-supplied [Spec.Usage] when set. 4. Default →
// DATA_POINT.
//
// The two override flags before `ForcedUsage` mirror the Python `usage`
// property's two-line head:
//
// if self._is_forced_sensor or self._is_un_ignored: return
// DataPointUsage.DATA_POINT
//
// Without that head, a force-sensor DP whose channel also matches
// `_SWITCH_DP_TO_SENSOR` would be visible only by accident — the suppression
// pass would later overwrite it with NoCreate.
//
// Implementation note: the duplicate `forcedUsage` field that lived directly
// on DataPoint; the embedded [datapoint.BaseDataPointFields] is now the
// single source of truth for the forced-usage state.
func (d *DataPoint[T]) Usage() hmenum.DataPointUsage {
	if d.IsForcedSensor() || d.IsUnIgnored() {
		return hmenum.DataPointUsageDataPoint
	}
	// CHANNEL_OPERATION_MODE gate is consulted BEFORE the forced-usage
	// override — matching Python `generic/data_point.py:69-71`, where
	// `if force_enabled is None: return _get_data_point_usage()` only
	// falls through to the forced-usage check when no op-mode gate is
	// observed. When the gate IS set it wins outright: enabled →
	// DataPoint, disabled → NoCreate, regardless of any forced_usage
	// override the channel may also carry. Practical impact is rare
	// (parameters with BOTH a forced_usage AND op-mode-gate are
	// uncommon), but the order is semantically required for parity.
	if enabled, ok := d.EnabledByChannelOperationMode(); ok {
		if enabled {
			return hmenum.DataPointUsageDataPoint
		}
		return hmenum.DataPointUsageNoCreate
	}
	if forced, ok := d.ForcedUsage(); ok {
		return forced
	}
	if d.Spec.Usage == "" {
		return hmenum.DataPointUsageDataPoint
	}
	return d.Spec.Usage
}

// Category returns the DataPointCategory the DP surfaces as. When
// [BaseDataPointFields.MarkForcedSensor] has been called, the category is
// forced to [hmenum.DataPointCategorySensor] regardless of the
// resolver-assigned [Spec.Kind].
//
// return DataPointCategory.SENSOR if self._is_forced_sensor else
// self._category
//
// Without this override an HmIP-eTRV.LEVEL marked via `_SWITCH_DP_TO_SENSOR`
// would still classify as Number in north- bound adapters that ask the DP for
// its category.
func (d *DataPoint[T]) Category() hmenum.DataPointCategory {
	if d.IsForcedSensor() {
		return hmenum.DataPointCategorySensor
	}
	return d.Kind.Category()
}

// EnabledByDefault reports whether the data point should be visible in a
// default UI without explicit operator action.
//
// Shadows the promotion from [datapoint.BaseDataPointFields] because generic
// data points fall back through [Spec.Usage] when no usage has been forced
// — the foundation layer's "only NoCreate disables it" rule is too permissive
// here. Keeping the override explicit makes the fallback chain obvious and
// lets future Config.Usage values diverge without surprising the foundation.
func (d *DataPoint[T]) EnabledByDefault() bool {
	switch d.Usage() { //nolint:exhaustive // CDPSecondary and NoCreate are internal book-keeping usages; EnabledByDefault correctly returns false for them
	case hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageCDPVisible,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageEvent:
		return true
	}
	return false
}

// Visible reports whether the data point should be exposed by the
// north-bound adapters. Equivalent to [EnabledByDefault] but kept as
// A separate name to mirror
// `enabled_default` (HA-level surface) and the materializer-driven
// "visibility" decision: a forced [hmenum.DataPointUsageNoCreate]
// returns false (the DP stays in the channel for internal use but
// never surfaces), a forced [hmenum.DataPointUsageCDPVisible]
// returns true, and any other usage falls through to the default-
// enabled rule.
//
// Shadows the promoted [datapoint.BaseDataPointFields.Visible]
// generic data points use the EnabledByDefault chain (Config.Usage
// fallback) rather than the foundation's "only NoCreate hides it"
// rule, so a forced [hmenum.DataPointUsageCDPSecondary] correctly
// returns false here.
func (d *DataPoint[T]) Visible() bool { return d.EnabledByDefault() }

// Default returns the typed representation of the descriptor DEFAULT field,
// or nil when the descriptor carries no DEFAULT (or when the raw DEFAULT
// could not be coerced into T at construction time).
//
// Mirrors the Python `default` property on GenericDataPoint
// (`model/data_point.py`).
func (d *DataPoint[T]) Default() *T {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.paramDefault
}

// IsValueInRange reports whether v satisfies the parameter's declared
// constraints (numeric MIN/MAX, enum index range). Bool / string / action /
// dummy parameters have no range constraints and always return true.
//
// Callers use this to short-circuit a write before sending garbage to the CCU
// — the wire-level fault would otherwise come back as code -5 after a round
// trip.
func (d *DataPoint[T]) IsValueInRange(v T) bool {
	return validateRange(d.Descriptor, any(v)) == nil
}

// IsCurrentValueInRange is the read-only counterpart of
// [IsValueInRange]. Returns true when no value has been observed yet
// (mirrors the Python "value is None → vacuously valid" rule).
func (d *DataPoint[T]) IsCurrentValueInRange() bool {
	d.mu.RLock()
	v, observed := d.value, d.observed
	d.mu.RUnlock()
	if !observed {
		return true
	}
	return validateRange(d.Descriptor, any(v)) == nil
}

// Value returns the last known value and whether it has been observed.
// The "observed" flag distinguishes a real zero from the struct's
// zero value.
//
// Priority (highest → lowest):
// 1. Optimistic value (if a pending send is in flight)
// 2. Unconfirmed value (if [WriteUnconfirmedValue] was called)
// 3. CCU-confirmed value
//
// This mirrors.py:807)
//
//	return self._unconfirmed_value if self._unconfirmed_refreshed_at >
//	 self._refreshed_at else self._current_value
func (d *DataPoint[T]) Value() (T, bool) {
	if snap := d.optimistic.Snapshot(); snap.Active {
		return snap.Value, true
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.unconfirmedValue != nil {
		return *d.unconfirmedValue, true
	}
	return d.value, d.observed
}

// ConfirmedValue returns the last CCU-confirmed value, ignoring any
// in-flight optimistic state. Useful for diagnostics and for the
// rollback path itself.
func (d *DataPoint[T]) ConfirmedValue() (T, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.value, d.observed
}

// RawValue returns the last known value as an untyped any together
// with the observed flag. It is the type-erased counterpart of
// [Value] and lets heterogeneous consumers (the Device/Channel
// layer) read from a mixed-type parameter map. Like [Value], it
// surfaces the optimistic value while one is in flight, then the
// unconfirmed value, then the CCU-confirmed value.
func (d *DataPoint[T]) RawValue() (any, bool) {
	if snap := d.optimistic.Snapshot(); snap.Active {
		return snap.Value, true
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.unconfirmedValue != nil {
		return *d.unconfirmedValue, true
	}
	return d.value, d.observed
}

// ModifiedAt reports the timestamp of the most recent value change, blended
// with the unconfirmed modified timestamp. Returns the later of the two so
// that an optimistic/unconfirmed write is reflected immediately in the
// timestamp surface.
//
// if self._unconfirmed_modified_at > self._modified_at: return
// self._unconfirmed_modified_at return self._modified_at
func (d *DataPoint[T]) ModifiedAt() time.Time {
	d.mu.RLock()
	confirmed := d.modifiedAt
	d.mu.RUnlock()
	unconfirmed := d.UnconfirmedModifiedAt()
	if unconfirmed.After(confirmed) {
		return unconfirmed
	}
	return confirmed
}

// RefreshedAt reports the timestamp of the most recent value observation,
// blended with the unconfirmed refreshed timestamp. Returns the later of the
// two.
//
// if self._unconfirmed_refreshed_at > self._refreshed_at: return
// self._unconfirmed_refreshed_at return self._refreshed_at
func (d *DataPoint[T]) RefreshedAt() time.Time {
	d.mu.RLock()
	confirmed := d.refreshedAt
	d.mu.RUnlock()
	unconfirmed := d.UnconfirmedRefreshedAt()
	if unconfirmed.After(confirmed) {
		return unconfirmed
	}
	return confirmed
}

// Source reports the wire-side lifecycle state of the current value.
// Returns [hmenum.ValueSourceUnobserved] when no value has ever been
// applied; one of [hmenum.ValueSourceCache], [hmenum.ValueSourceLive]
// or [hmenum.ValueSourceStale] otherwise. See ADR 0018.
func (d *DataPoint[T]) Source() hmenum.ValueSource {
	d.mu.RLock()
	src := d.source
	observed := d.observed
	d.mu.RUnlock()
	if !observed || src == "" {
		return hmenum.ValueSourceUnobserved
	}
	return src
}

// LastSeenAt reports when the wire-side data point was last observed
// (any push or fetch_all event, including cyclic info telegrams that
// repeated an existing value) — this is the refreshed counterpart of
// the modified-at / refreshed-at split.
func (d *DataPoint[T]) LastSeenAt() time.Time { return d.RefreshedAt() }

// LastChangedAt reports when the value actually changed last; cyclic
// info telegrams that repeat the previous value do not bump this.
// Mirrors the "modified_at" timestamp.
func (d *DataPoint[T]) LastChangedAt() time.Time { return d.ModifiedAt() }

// MarkStale flips the source token to [hmenum.ValueSourceStale]
// without touching the value or timestamps. Used by the connection-
// lost handler so all data points of the affected interface signal
// that their value may no longer reflect reality. A no-op on
// unobserved data points (a value the daemon never saw cannot be
// "stale"). Fires confirmedUpdateCallbacks so MQTT republishes the
// transition.
//
// Returns the previous source token plus a bool that is true when
// the source actually transitioned. Callers (e.g. the lifecycle
// wiring) use the return values to publish a DataPointSourceChanged
// event with the old/new pair.
func (d *DataPoint[T]) MarkStale() (oldSource hmenum.ValueSource, changed bool) {
	d.mu.Lock()
	if !d.observed || d.source == hmenum.ValueSourceStale || d.source == hmenum.ValueSourceUnobserved {
		old := d.source
		d.mu.Unlock()
		return old, false
	}
	oldSource = d.source
	d.source = hmenum.ValueSourceStale
	cbs := make([]func(old, next T), len(d.confirmedUpdateCallbacks))
	copy(cbs, d.confirmedUpdateCallbacks)
	v := d.value
	d.mu.Unlock()
	for _, cb := range cbs {
		cb(v, v)
	}
	return oldSource, true
}

// MarkLive flips the source token to [hmenum.ValueSourceLive] without
// touching the value. Used by the recovery.completed handler so all
// data points of the recovered interface signal renewed freshness.
// A no-op on unobserved data points. Fires confirmedUpdateCallbacks
// so MQTT republishes the transition.
//
// Returns the previous source token plus a bool that is true when
// the source actually transitioned.
func (d *DataPoint[T]) MarkLive() (oldSource hmenum.ValueSource, changed bool) {
	d.mu.Lock()
	if !d.observed || d.source == hmenum.ValueSourceLive || d.source == hmenum.ValueSourceUnobserved {
		old := d.source
		d.mu.Unlock()
		return old, false
	}
	oldSource = d.source
	d.source = hmenum.ValueSourceLive
	cbs := make([]func(old, next T), len(d.confirmedUpdateCallbacks))
	copy(cbs, d.confirmedUpdateCallbacks)
	v := d.value
	d.mu.Unlock()
	for _, cb := range cbs {
		cb(v, v)
	}
	return oldSource, true
}

// RestoreCachedValue applies a persisted snapshot to the data point.
// Unlike [OnEvent] this does not move source to live — the value is
// tagged [hmenum.ValueSourceCache] until the first real push or
// fetch_all_device_data response arrives. Returns false when the
// value cannot be coerced into the data point's typed T (the row
// stays in the cache; the GC pass cleans it up if the schema
// permanently shifted).
//
// Timestamps come from the cache row itself so the REST surface can
// report exactly how stale the snapshot is.
func (d *DataPoint[T]) RestoreCachedValue(v any, lastSeen, lastChanged time.Time) bool {
	if v == nil {
		return false
	}
	var typed T
	if t, ok := v.(T); ok {
		typed = t
	} else if conv, ok := coerceWire[T](v); ok {
		typed = conv
	} else {
		return false
	}
	d.mu.Lock()
	d.value = typed
	d.observed = true
	d.refreshedAt = lastSeen
	d.modifiedAt = lastChanged
	d.source = hmenum.ValueSourceCache
	d.lastNonDefaultValue = &typed
	d.lastNonDefaultObserved = true
	d.mu.Unlock()
	return true
}

// OnWireValue accepts a wire-level value of unknown static type and
// coerces it into T before delegating to [OnEvent]. Callers without a
// typed T (the Rega `fetch_all_device_data` seeder, the REST
// generic SetValue path) use this entry point to avoid a custom type
// switch per [DataPoint] specialisation.
//
// Returns false when the value cannot be converted — e.g. a string
// arriving for a bool-typed data point with an unrecognised literal.
// Numeric widening/narrowing (int↔int32, int64↔int32, float32↔float64)
// is performed where safe; strings are only coerced into bool/int32/
// float64 when they parse cleanly.
func (d *DataPoint[T]) OnWireValue(v any) bool {
	if v == nil {
		return false
	}
	if typed, ok := v.(T); ok {
		d.OnEvent(typed)
		return true
	}
	if conv, ok := coerceWire[T](v); ok {
		d.OnEvent(conv)
		return true
	}
	return false
}

// OnEvent is the entry point for CCU-driven updates. The event
// coordinator calls this after decoding a wire event into T. A
// subscriber callback is invoked after the value has been stored.
//
// Delegates to [OnEventAt] with the current wall-clock time.
func (d *DataPoint[T]) OnEvent(v T) {
	d.OnEventAt(v, time.Now())
}

// OnEventAt is the deterministic counterpart of [OnEvent]: it records v as a
// CCU-confirmed value and stamps the observation with the supplied timestamp
// instead of calling time.Now(). Test fixtures pass a fixed time to assert
// timestamp-dependent behaviour without racing against the wall clock.
//
// Optimistic-state interaction:
//
// - If a tracker is active and the incoming value is "close
// enough" to the optimistic value, we decrement pendingSends.
// On the final confirmation (count == 0) the tracker is
// cleared and the data point's confirmed value is set to v.
// - If a tracker is active and the value mismatches, we treat
// the CCU as authoritative: the tracker is cleared (no
// rollback event — the CCU's value already replaces the
// optimistic one) and observers are notified that the value
// changed from the optimistic guess to v. This.
func (d *DataPoint[T]) OnEventAt(v T, at time.Time) {
	// reset the unconfirmed value slot — a CCU-confirmed value supersedes any
	// pending unconfirmed write.
	d.resetUnconfirmedValue()

	d.mu.Lock()
	old := d.value
	hadValue := d.observed
	d.value = v
	d.observed = true
	d.refreshedAt = at
	prevSource := d.source
	d.source = hmenum.ValueSourceLive
	// clear the initial-uncertain flag on first confirmed value.
	d.stateUncertain = false
	if !hadValue || !valuesClose(old, v) {
		d.modifiedAt = at
		// Update the last-non-default slot only when v differs from the
		// parameter's DEFAULT. This mirrors the reference condition
		// `if new_value != self._default` which prevents the "restore last
		// value" feature from ever restoring the factory default.
		if d.paramDefault == nil || !valuesClose(v, *d.paramDefault) {
			d.lastNonDefaultValue = &v
			d.lastNonDefaultObserved = true
		}
	}
	callbacks := make([]func(old, next T), len(d.updateCallbacks))
	copy(callbacks, d.updateCallbacks)
	// Confirmed-only callbacks fire when the CCU echo represents an
	// actual transition (first observation, or a value diff). A
	// CCU echo that confirms an identical value does not mark the
	// Matter cluster dirty — matches matter.js's `measuredValue$Changed`
	// semantics (fires on change, not on every read).
	var confirmedCallbacks []func(old, next T)
	// Source transitions count as a confirmed event too — a wire-DP
	// going cache→live or stale→live carries new freshness even
	// when the value did not change. MQTT republishes on every such
	// transition so downstream consumers see the freshness flip.
	sourceChanged := prevSource != d.source
	if !hadValue || !valuesClose(old, v) || sourceChanged {
		confirmedCallbacks = make([]func(old, next T), len(d.confirmedUpdateCallbacks))
		copy(confirmedCallbacks, d.confirmedUpdateCallbacks)
	}
	d.mu.Unlock()

	if d.optimistic.IsActive() {
		snap := d.optimistic.Snapshot()
		// ConfirmOne decrements the pending-sends counter regardless of
		// whether the CCU echo matches the optimistic value. The match
		// branch additionally clears the tracker when pending hits zero;
		// the mismatch branch clears unconditionally because the CCU is
		// authoritative and any in-flight optimistic state is now stale.
		// Mirrors the reference event() flow which always decrements
		// pending_sends, then clears on mismatch.
		_ = d.optimistic.ConfirmOne()
		if valuesClose(snap.Value, v) {
			if d.optimistic.Snapshot().PendingSends == 0 {
				d.optimistic.Clear()
			}
		} else {
			// Mismatch: CCU is authoritative. Drop the tracker
			// without firing a rollback event; the regular update
			// callback below already announces v.
			d.optimistic.Clear()
		}
	}

	for _, cb := range callbacks {
		if cb != nil {
			cb(old, v)
		}
	}
	for _, cb := range confirmedCallbacks {
		if cb != nil {
			cb(old, v)
		}
	}
}

// OnAnyUpdate is the type-erased counterpart to [OnUpdate]. The
// callback receives the old and next values as `any`, letting
// heterogeneous subscribers (calculated formulas, custom-DP
// aggregators) hold a single registration list across multiple sub-
// data-points without caring about their concrete generic type.
//
// Returns an idempotent unsubscribe closure.
func (d *DataPoint[T]) OnAnyUpdate(fn func(old, next any)) func() {
	if fn == nil {
		return func() {}
	}
	return d.OnUpdate(func(old, next T) {
		fn(old, next)
	})
}

// OnUpdate registers a change handler. Returns an unsubscribe closure
// that is idempotent.
func (d *DataPoint[T]) OnUpdate(fn func(old, next T)) func() {
	d.mu.Lock()
	d.updateCallbacks = append(d.updateCallbacks, fn)
	idx := len(d.updateCallbacks) - 1
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if idx < len(d.updateCallbacks) {
				d.updateCallbacks[idx] = nil
			}
		})
	}
}

// OnConfirmedUpdate registers a handler that fires only when a
// CCU-confirmed value lands AND the value differs from the previously
// confirmed one (or no value had been observed before). Optimistic
// Apply, rollback, and idempotent no-change CCU echoes do NOT trigger
// the handler. Returns an idempotent unsubscribe closure.
//
// The Matter Subscribe engine uses this hook so Apple Home's
// ReportData stream only carries CCU-confirmed transitions — matches
// matter.js's `events.measuredValue$Changed.on(...)` semantics.
// MQTT / REST / UI continue to consume [OnUpdate] so they keep their
// optimistic-update affordance.
func (d *DataPoint[T]) OnConfirmedUpdate(fn func(old, next T)) func() {
	if fn == nil {
		return func() {}
	}
	d.mu.Lock()
	d.confirmedUpdateCallbacks = append(d.confirmedUpdateCallbacks, fn)
	idx := len(d.confirmedUpdateCallbacks) - 1
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if idx < len(d.confirmedUpdateCallbacks) {
				d.confirmedUpdateCallbacks[idx] = nil
			}
		})
	}
}

// OnRemoved registers a lifecycle hook fired when the device the data
// point belongs to is removed. Returns an unsubscribe closure.
func (d *DataPoint[T]) OnRemoved(fn func()) func() {
	d.mu.Lock()
	d.removedCbs = append(d.removedCbs, fn)
	idx := len(d.removedCbs) - 1
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if idx < len(d.removedCbs) {
				d.removedCbs[idx] = nil
			}
		})
	}
}

// NotifyRemoved fires every registered removed hook. Called by the
// device coordinator when the parent device goes away.
func (d *DataPoint[T]) NotifyRemoved() {
	d.mu.Lock()
	cbs := make([]func(), len(d.removedCbs))
	copy(cbs, d.removedCbs)
	d.removedCbs = nil
	d.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb()
		}
	}
}

// OptimisticAge returns how long ago the last optimistic update was
// applied, or zero when the cached value is fully CCU-confirmed.
func (d *DataPoint[T]) OptimisticAge() time.Duration {
	return d.optimistic.Snapshot().Age
}

// IsOptimistic reports whether the cached value is an optimistic update
// awaiting CCU confirmation.
func (d *DataPoint[T]) IsOptimistic() bool {
	return d.optimistic.IsActive()
}

// PendingSends reports how many SetValue calls are still awaiting
// CCU confirmation. Counts 0 when no optimistic update is in flight.
func (d *DataPoint[T]) PendingSends() int {
	return d.optimistic.Snapshot().PendingSends
}

// InjectSyntheticValue injects a value into the data point as if it
// had arrived via a CCU event, forcing the sensor surface to update
// even if the data point itself is write-only or has no real CCU
// source. Used by calculated/derived sensors that read auxiliary
// parameters not normally surfaced.
//
// Naming note (M22 — v8 §4): the method was formerly
// Called ForceToSensor, which clashed confusingly
// `force_to_sensor()` (data_point.py:1074) — that method sets the
// categorical `_is_forced_sensor=True` flag; the Go equivalent is
// [datapoint.BaseDataPointFields.MarkForcedSensor]. The rename makes
// the two distinct operations unambiguous.
//
// This is a deliberate side-channel — most callers should use
// [OnEvent] / [OnWireValue] instead.
func (d *DataPoint[T]) InjectSyntheticValue(v T) { d.OnEvent(v) }

// TriggerUpdate fires the update callbacks with the current confirmed value as
// both old and next. Callers use this to wake north-bound subscribers (MQTT,
// REST, Matter) after an out-of-band state change that did not go through the
// normal OnEvent / WriteUnconfirmedValue pipeline — for example when the
// parent device's forced-availability flag changes and every DP must republish
// so downstream entities reflect the new availability context.
//
// Mirrors `publish_data_point_updated_event()` (model/data_point.py) which
// re-fires callbacks without changing the stored value.
//
// The method is a no-op when no value has been observed yet.
func (d *DataPoint[T]) TriggerUpdate() {
	d.mu.RLock()
	v := d.value
	observed := d.observed
	cbs := make([]func(old, next T), len(d.updateCallbacks))
	copy(cbs, d.updateCallbacks)
	d.mu.RUnlock()
	if !observed {
		return
	}
	for _, cb := range cbs {
		if cb != nil {
			cb(v, v)
		}
	}
}

// AdditionalInformation returns a free-form metadata map north-bound adapters
// can surface alongside the typed value. The base implementation returns nil;
// concrete custom data points override to surface composite-state details
// (Climate ⇒ activity + away-end, OperatingVoltageLevel ⇒ battery type /
// quantity / max-voltage, …).
func (d *DataPoint[T]) AdditionalInformation() map[string]any { return nil }

// StateUncertain reports whether the data point's value is uncertain.
//
// - True on newly-constructed DPs until the first CCU-confirmed value arrives
// via [OnEvent] or [MarkRefreshed] - True while an optimistic write is
// pending CCU confirmation. - False once a confirmed value has been received
// and no write is in flight.
//
// A UI / MQTT bridge uses this to flag the displayed value as
// not-yet-confirmed.
func (d *DataPoint[T]) StateUncertain() bool {
	d.mu.RLock()
	uncertain := d.stateUncertain
	d.mu.RUnlock()
	return uncertain || d.optimistic.IsActive()
}

// IsUnitFixed reports whether the parameter's raw CCU unit differs from the
// cleaned-up unit that openccu-loom exposes.
//
// return self._raw_unit != self.unit
//
// A true return means the unit advertised to north-bound adapters is the
// overridden/cleaned value, not whatever the CCU descriptor says verbatim.
// Used by diagnostic endpoints to surface the unit-cleanup decision.
func (d *DataPoint[T]) IsUnitFixed() bool {
	return d.Descriptor.Unit != d.Unit()
}

// HasStatusParameter reports whether the data point has a paired
// `<param>_STATUS` parameter registered via [SetStatusParameter].
//
// return self._status_parameter is not None
//
// The flag is set by [SetStatusParameter] (or auto-detection via
// [DetectStatusParameter] at device-construction time) and reflects whether a
// status-parameter *name* has been registered — not whether a status
// observation has been received. This aligns with the Python semantics: the
// method answers "is there a paired STATUS param?", not "has a STATUS value
// arrived?".
//
// changed from returning statusObserved (event-driven) to statusParamSet
// (name-set-driven). IsStatusValid still uses statusObserved so the
// vacuous-pass behaviour for unregistered DPs is unaffected.
func (d *DataPoint[T]) HasStatusParameter() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.statusParamSet
}

// IsStatusValid reports whether the paired `<param>_STATUS` parameter is in
// an acceptable state.
//
// if self._status_value is None: return True return self._status_value in
// (ParameterStatus.NORMAL, ParameterStatus.UNKNOWN)
//
// When no status observation has been recorded (HasStatusParameter() ==
// false) the check passes vacuously — there is nothing to fail. Once observed
// the status must be NORMAL or UNKNOWN; the latter covers the init-phase
// grace period before the CCU has reported a definitive quality reading.
func (d *DataPoint[T]) IsStatusValid() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.statusObserved {
		return true
	}
	return d.status == hmenum.ParameterStatusNormal ||
		d.status == hmenum.ParameterStatusUnknown
}

// allowsNoneValue reports whether nil/zero is a valid "value" for this
// parameter, meaning the DP may legitimately carry no observation.
//
// if self._type == ParameterType.ACTION: return True if self._parameter in
// _OPTIONAL_PARAMETERS: return True return bool(self._special and
// self._special.get("OPTIONAL"))
//
// Used by [HasValidValueType] to let unobserved optional / action DPs pass
// the validity check without requiring a prior CCU push.
func (d *DataPoint[T]) allowsNoneValue() bool {
	if d.Kind.IsAction() {
		return true
	}
	if hmenum.Parameter(d.Spec.Key.Parameter).IsOptional() {
		return true
	}
	// Check SPECIAL.OPTIONAL in the raw descriptor blob.
	if len(d.Descriptor.Special) > 0 {
		// Simple substring check to avoid a full JSON decode on the hot path.
		// The SPECIAL field is a small JSON object; "OPTIONAL" in it means
		// the parameter is explicitly optional.
		raw := string(d.Descriptor.Special)
		if containsOptionalKey(raw) {
			return true
		}
	}
	return false
}

// containsOptionalKey reports whether the SPECIAL JSON blob includes an
// "OPTIONAL" key. It uses a plain string search to avoid allocating a
// map for a seldom-populated, small JSON field.
func containsOptionalKey(special string) bool {
	const key = `"OPTIONAL"`
	return len(special) >= len(key) && specialContains(special, key)
}

func specialContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// HasValidValueType reports whether the data point's current value has the
// expected Go type for the parameter's descriptor.
//
// return isinstance(self.value, self._type)
//
// For a generic DataPoint[T], the value is always of type T by construction —
// the only case where the type could be "wrong" is when the data point has
// never been observed (IsRefreshed() == false) and the zero value of T is
// used as a stand-in. Returns false when the data point is un-refreshed,
// UNLESS the parameter allows a None value (ACTION type, optional parameter,
// or SPECIAL.OPTIONAL marker) in which case the unobserved state is valid.
func (d *DataPoint[T]) HasValidValueType() bool {
	if !d.RefreshedAt().IsZero() {
		return true
	}
	// For optional/action parameters the absence of a CCU-pushed value is
	// intentional and the DP is still "valid".
	return d.allowsNoneValue()
}

// IsValid reports whether the data point is in a fully valid state.
//
// 1. IsRefreshed() — at least one CCU observation received. 2.
// IsStatusValid() — the paired _STATUS parameter, if any, is OK. 3.
// HasValidValueType() — the current value has the expected type. 4.
// IsCurrentValueInRange() — the current value is within declared bounds.
//
// When all four conditions hold the data point is considered valid for
// north-bound exposure (REST, MQTT, UI). A false result should suppress
// publishing or flag the entity as unavailable.
func (d *DataPoint[T]) IsValid() bool {
	return !d.RefreshedAt().IsZero() &&
		d.IsStatusValid() &&
		d.HasValidValueType() &&
		d.IsCurrentValueInRange()
}

// RequiresPolling reports whether the background scheduler should
// Actively poll this parameter from the CCU.
// two-part `requires_polling` (data_point.py:1033):
//
//	return (
//	 not client.capabilities.push_updates
//	 or (product_group in (HM, HMW) and paramset_key == MASTER)
//	)
//
// Condition 1 — NoPushUpdates: when the backend interface cannot push
// event callbacks (e.g. a poll-only transport), every parameter must
// be polled regardless of paramset. Driven by [Spec.NoPushUpdates];
// default false keeps the old behaviour for push-capable interfaces.
//
// Condition 2 — HM/HMW MASTER: BidCos-RF ("HM") and BidCos-Wired
// ("HMW") CCUs do not push MASTER-paramset changes — those parameters
// must be polled even when VALUES arrive via callbacks. All other
// interface families (HmIP-RF, CUxD, VirtualDevices) are
// covered by Condition 1 when relevant, or have their own polling
// flags. The product-group check uses the interface-ID prefix exactly
// As
//
// previously only checked MASTER paramset (Condition 2).
// Now also checks Condition 1 so poll-only interfaces work correctly.
func (d *DataPoint[T]) RequiresPolling() bool {
	if d.NoPushUpdates {
		return true
	}
	// HM (BidCos-RF) and HMW (BidCos-Wired) require explicit polling
	// for MASTER-paramset parameters — the CCU never pushes them.
	isMasterParamset := d.Key.ParamsetKey == hmenum.ParamsetKeyMaster
	if !isMasterParamset {
		return false
	}
	ifaceID := d.Key.InterfaceID
	isHMorHMW := strings.HasPrefix(ifaceID, string(hmenum.ProductGroupHM)) ||
		strings.HasPrefix(ifaceID, string(hmenum.ProductGroupHmW))
	return isHMorHMW
}

// recentlyWindow is the threshold below which a timestamp counts as "recent".
const recentlyWindow = 500 * time.Millisecond

// ModifiedRecently reports whether the data point's value changed in
// the last 500 ms.
func (d *DataPoint[T]) ModifiedRecently() bool {
	d.mu.RLock()
	t := d.modifiedAt
	d.mu.RUnlock()
	if t.IsZero() {
		return false
	}
	return time.Since(t) < recentlyWindow
}

// RefreshedRecently reports whether the data point received a CCU
// confirmation in the last 500 ms (whether or not the value changed).
func (d *DataPoint[T]) RefreshedRecently() bool {
	d.mu.RLock()
	t := d.refreshedAt
	d.mu.RUnlock()
	if t.IsZero() {
		return false
	}
	return time.Since(t) < recentlyWindow
}

// IsRefreshed reports whether the data point has received at least one value
// from the CCU. Shadows [datapoint.BaseDataPointFields.IsRefreshed] because
// [DataPoint] carries its own `refreshedAt` field that the embedded base does
// not see.
//
// return self._refreshed_at > INIT_DATETIME
func (d *DataPoint[T]) IsRefreshed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return !d.refreshedAt.IsZero()
}

// OnAnyRollback is the type-erased counterpart to [OnRollback]. The
// callback receives the rolled-back and restored values as `any`,
// letting heterogeneous subscribers (event-bus publishers, audit
// recorders) hold a single registration across multiple typed DPs
// without switching on their concrete generic type. Returns an
// idempotent unsubscribe closure.
func (d *DataPoint[T]) OnAnyRollback(fn func(reason RollbackReason, rolledBack, restored any, restoredSet bool)) func() {
	if fn == nil {
		return func() {}
	}
	return d.OnRollback(func(reason RollbackReason, rolledBack, restored T, restoredSet bool) {
		fn(reason, rolledBack, restored, restoredSet)
	})
}

// OnRollback registers a callback fired when the optimistic state
// is rolled back (timeout or send error). The reason argument
// distinguishes those two paths so subscribers can route to the
// right telemetry channel. Returns an unsubscribe closure.
func (d *DataPoint[T]) OnRollback(fn func(reason RollbackReason, rolledBack, restored T, restoredSet bool)) func() {
	d.mu.Lock()
	d.rollbackCallbacks = append(d.rollbackCallbacks, fn)
	idx := len(d.rollbackCallbacks) - 1
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if idx < len(d.rollbackCallbacks) {
				d.rollbackCallbacks[idx] = nil
			}
		})
	}
}

// WaitForConfirmation blocks until the active optimistic round trip
// settles — either by a matching CCU confirmation event (final
// confirm clears the tracker), a value-mismatch (CCU value is
// authoritative, tracker also clears), or a rollback (timeout
// send error / explicit). Returns nil on settle, or ctx.Err() if
// the wait is cancelled before settle.
//
// When no optimistic round trip is in flight, returns immediately.
// The function is safe to call concurrently from multiple
// goroutines waiting on the same data point.
func (d *DataPoint[T]) WaitForConfirmation(ctx context.Context) error {
	if !d.optimistic.IsActive() {
		return nil
	}
	done := d.optimistic.Done()
	if done == nil {
		// Raced with confirm/rollback between isActive and
		// doneChan; treat as already settled.
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ApplyOptimistic stages an optimistic update on this data point
// without dispatching a wire call. The caller (typically a
// [CallParameterCollector]) is responsible for the actual SetValue/
// PutParamset roundtrip and for invoking the returned rollback
// closure on wire failure. Returns nil when:
//
// - the data point has [Spec.OptimisticDisabled] set, or
// - the parameter advertises no EVENT operation (Python's
// `apply_optimistic_value` skips when `not has_events` — without
// the CCU echo there is no signal that can confirm an optimistic
// update, so the tracker would always roll back and spam warnings),
// or
// - the burst-skip rule triggers (an active tracker already holds
// the same value), or
// - the value cannot be coerced into T.
//
// Otherwise the closure undoes the optimistic state and fires the
// rollback callbacks with [RollbackReasonSendError]. Calling the
// closure on a tracker that already confirmed (because the wire
// dispatch and CCU echo raced ahead of the failure handler) is a
// safe no-op.
func (d *DataPoint[T]) ApplyOptimistic(value any) func() {
	if d.OptimisticDisabled {
		return nil
	}
	if !d.HasEvents() {
		// Parameter has no EVENT-bit in OPERATIONS → no CCU echo will
		// ever confirm an optimistic value. Skip to avoid spurious
		// rollback warnings on every write (most relevant on CUxD /
		// CCU-Jack interfaces that don't carry the same callback
		// surface as XML-RPC HmIP-RF).
		return nil
	}
	typed, ok := value.(T)
	if !ok {
		conv, cok := coerceWire[T](value)
		if !cok {
			return nil
		}
		typed = conv
	}

	if snap := d.optimistic.Snapshot(); snap.Active && valuesClose(snap.Value, typed) {
		return func() {} // burst-skip: nothing to roll back.
	}

	d.mu.RLock()
	current := d.value
	currentSet := d.observed
	d.mu.RUnlock()

	d.optimistic.Apply(typed, current, currentSet)

	d.mu.Lock()
	d.modifiedAt = time.Now()
	updateCbs := make([]func(old, next T), len(d.updateCallbacks))
	copy(updateCbs, d.updateCallbacks)
	d.mu.Unlock()
	for _, cb := range updateCbs {
		if cb != nil {
			cb(current, typed)
		}
	}

	d.optimistic.ScheduleRollback(d.optimisticTimeout(), func() {
		d.rollback(RollbackReasonTimeout)
	})

	return func() {
		d.rollback(RollbackReasonSendError)
	}
}

// rollback drops the optimistic state, restores the previous
// value, and notifies subscribers. Public methods (the timeout
// goroutine, send-error path) use this rather than touching the
// tracker directly.
func (d *DataPoint[T]) rollback(reason RollbackReason) {
	rolledBack, restored, restoredSet, ok := d.optimistic.Rollback()
	if !ok {
		return
	}
	d.mu.Lock()
	if restoredSet {
		d.value = restored
		d.observed = true
	} else {
		var zero T
		d.value = zero
		d.observed = false
	}
	cbs := make([]func(reason RollbackReason, rolledBack, restored T, restoredSet bool), len(d.rollbackCallbacks))
	copy(cbs, d.rollbackCallbacks)
	updateCbs := make([]func(old, next T), len(d.updateCallbacks))
	copy(updateCbs, d.updateCallbacks)
	d.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(reason, rolledBack, restored, restoredSet)
		}
	}
	// Also notify regular update subscribers so the UI re-renders
	// when the displayed value flips back from optimistic to
	// confirmed (or to "unobserved").
	for _, cb := range updateCbs {
		if cb != nil {
			cb(rolledBack, restored)
		}
	}
}

// sendAndObserve is the shared send path every concrete setter
// uses. It coordinates the optimistic tracker with the wire-level
// SetValue:
//
// 1. Burst-skip — if a tracker is already active and holds the
// Same `typed` value, we silently return.
// issue #3049: the same value sent twice would otherwise drive
// pendingSends to 2 with only one CCU confirm event arriving,
// leading to a spurious rollback after the timeout.
// 2. Apply optimistically — the tracker captures `previous`
// (current confirmed value) on the first send, otherwise just
// bumps pendingSends. The data point's `Value()` already
// reports the optimistic value.
// 3. Schedule a rollback timer — if the CCU never confirms within
// OptimisticTimeout (default 30 s), the timer rolls back to
// the captured previous value and fires OnRollback.
// 4. Dispatch the wire call. On error, immediately roll back so
// the user-visible state stays truthful.
//
// `OptimisticDisabled` short-circuits steps 1–3 — the wire send
// runs and the cached value is updated synchronously without any
// Tracker interaction. This matches
// `rpc_callback == False` branch (CUxD / CCU-Jack channels).
func (d *DataPoint[T]) sendAndObserve(
	ctx context.Context,
	wireValue any,
	typed T,
	priority hmenum.CommandPriority,
) error {
	if d.Writer == nil {
		return ErrNoWriter
	}

	if d.OptimisticDisabled {
		if err := d.Writer.SetValue(
			ctx,
			d.Key.ChannelAddress,
			hmenum.Parameter(d.Key.Parameter),
			wireValue,
			priority,
		); err != nil {
			return err
		}
		now := time.Now()
		d.mu.Lock()
		d.value = typed
		d.observed = true
		d.modifiedAt = now
		// CUxD/CCU-Jack channels: the CCU will not echo a confirmation back, so
		// this synchronous post-send is our only chance to refresh — bump
		// refreshedAt explicitly.
		d.refreshedAt = now
		d.mu.Unlock()
		return nil
	}

	// Burst-skip — silently no-op when the same value is in flight.
	if snap := d.optimistic.Snapshot(); snap.Active && valuesClose(snap.Value, typed) {
		return nil
	}

	// Capture current confirmed value as the rollback anchor.
	d.mu.RLock()
	current := d.value
	currentSet := d.observed
	d.mu.RUnlock()

	d.optimistic.Apply(typed, current, currentSet)

	// Notify update subscribers immediately so the UI/MQTT layer
	// shows the optimistic value without waiting for the CCU.
	d.mu.Lock()
	d.modifiedAt = time.Now()
	updateCbs := make([]func(old, next T), len(d.updateCallbacks))
	copy(updateCbs, d.updateCallbacks)
	d.mu.Unlock()
	for _, cb := range updateCbs {
		if cb != nil {
			cb(current, typed)
		}
	}

	// Arm the rollback timer.
	d.optimistic.ScheduleRollback(d.optimisticTimeout(), func() {
		d.rollback(RollbackReasonTimeout)
	})

	// When a CallParameterCollector is present in the context, route through it
	// so multiple parameters targeting the same (channel, paramset) tuple are
	// batched into one put_paramset call. The collector's Send is responsible for
	// the actual wire dispatch; rollback on error is handled by the optimistic
	// tracker that was already Armed above.
	if coll := CollectorFromContext(ctx); coll != nil {
		if err := coll.Add(d, wireValue, 0); err == nil {
			return nil
		}
		// If Add fails (e.g. collector consumed), fall through to direct dispatch.
	}

	if err := d.Writer.SetValue(
		ctx,
		d.Key.ChannelAddress,
		hmenum.Parameter(d.Key.Parameter),
		wireValue,
		priority,
	); err != nil {
		// Wire failed → roll back immediately, before the timeout
		// so observers see the truthful state right away.
		d.rollback(RollbackReasonSendError)
		return err
	}
	return nil
}

// optimisticTimeout returns the rollback grace period configured
// for this data point, falling back to the package default.
func (d *DataPoint[T]) optimisticTimeout() time.Duration {
	if d.OptimisticTimeout > 0 {
		return d.OptimisticTimeout
	}
	return OptimisticDefaultTimeout
}

// ─── Status parameter auto-detection ────────────────────

// SetStatusParameter installs the paired status parameter name and
// pre-populates the value-list cache. Mirrors the auto-detection
// Performed
// `BaseParameterDataPoint.__init__` (model/data_point.py:756). The
// caller (device constructor) passes the status parameter name discovered
// via [DetectStatusParameter] and the cached value list from the paramset
// description.
//
// Once set, [UpdateStatus] can map CCU integer-index updates to
// [hmenum.ParameterStatus] string values using the cached list.
func (d *DataPoint[T]) SetStatusParameter(paramName string, valueList []string) {
	d.mu.Lock()
	d.statusParameter = paramName
	d.statusParamSet = paramName != ""
	if paramName != "" {
		d.statusValueList = make([]string, len(valueList))
		copy(d.statusValueList, valueList)
	} else {
		d.statusValueList = nil
	}
	d.mu.Unlock()
}

// StatusParameter returns the name of the paired status parameter (e.g.
// "LEVEL_STATUS" for "LEVEL") and whether one has been detected.
func (d *DataPoint[T]) StatusParameter() (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.statusParameter, d.statusParamSet
}

// StatusValueList returns the cached VALUE_LIST for the paired status
// parameter. The returned slice is a copy; callers may not mutate it. Returns
// nil when no status parameter is set.
func (d *DataPoint[T]) StatusValueList() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.statusValueList) == 0 {
		return nil
	}
	out := make([]string, len(d.statusValueList))
	copy(out, d.statusValueList)
	return out
}

// ─── LastNonDefaultValue ──────────────────────────────────────

// LastNonDefaultValue returns the last CCU-confirmed value that differed from
// the parameter's default, together with a flag indicating whether any such
// value has been tracked yet.
//
// Used for "restore last value" scenarios (e.g. a dimmer that should restore
// its last brightness level after a power cycle). The value is updated by
// [OnEvent] whenever the confirmed value differs from the previous one.
func (d *DataPoint[T]) LastNonDefaultValue() (T, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.lastNonDefaultValue == nil {
		var zero T
		return zero, false
	}
	return *d.lastNonDefaultValue, d.lastNonDefaultObserved
}

// SetLastNonDefaultValue explicitly installs a last-non-default value. Used
// by test fixtures and the initial cache-restore path to pre-populate the
// slot without going through [OnEvent].
func (d *DataPoint[T]) SetLastNonDefaultValue(v T) {
	d.mu.Lock()
	d.lastNonDefaultValue = &v
	d.lastNonDefaultObserved = true
	d.mu.Unlock()
}

// UnconfirmedValue returns the pending unconfirmed value and whether
// one is set.
// Returns (zero, false) when no unconfirmed write is pending.
func (d *DataPoint[T]) UnconfirmedValue() (T, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.unconfirmedValue == nil {
		var zero T
		return zero, false
	}
	return *d.unconfirmedValue, true
}

// ─── EnumValueIsIndex + IgnoreOnInitialLoad ───────────────────

// EnumValueIsIndex reports whether ENUM values for this parameter should be
// sent as integer indices (HM devices) rather than string values (HmIP
// devices).
//
// Computed at construction time from the descriptor: true when TYPE == ENUM,
// a VALUE_LIST is declared, and MIN is an integer.
func (d *DataPoint[T]) EnumValueIsIndex() bool {
	return d.enumValueIsIndex
}

// IgnoreOnInitialLoad reports whether fetching this parameter during the
// initial device load should be skipped. See
// [hmenum.Parameter.IgnoreOnInitialLoad] for the full semantics.
func (d *DataPoint[T]) IgnoreOnInitialLoad() bool {
	return d.ignoreOnInitialLoad
}

// ─── package-level helpers ───────────────────────────────────────────────────

// enumValueIsIndexFromDescriptor computes the `_enum_value_is_index` flag
// from the descriptor.
//
// self._enum_value_is_index = ( self._type == ParameterType.ENUM and
// self._values is not None and isinstance(raw_min, int) )
//
// "raw_min is int" maps to the descriptor's Min being a JSON integer (not a
// JSON string). We parse Min as a number; if it parses as an integer (no
// decimal point), the flag is true.
func enumValueIsIndexFromDescriptor(desc hmproto.ParameterData) bool {
	if desc.Type != hmenum.ParameterTypeEnum {
		return false
	}
	if len(desc.ValueList) == 0 {
		return false
	}
	// Try to parse Min as a plain integer (no ".").
	_, ok := parseFloat(desc.Min)
	if !ok {
		return false
	}
	// Distinguish "123" (int-like) from "123.0" (float-like) by checking
	// for a decimal point in the raw bytes.
	for _, b := range desc.Min {
		if b == '.' || b == 'e' || b == 'E' {
			return false // float literal → string-based enum
		}
	}
	return true
}

// DetectStatusParameter returns the name of the paired status parameter for
// `parameter` if it exists in the provided paramset.
//
// status_param = f"{self._parameter}_STATUS" if
// self._paramset_description_provider.has_parameter(...): return status_param
// return None
//
// Callers (device constructors) pass the paramset map for the channel and the
// parameter name to auto-detect the paired STATUS parameter. The second
// return value is true when a status parameter was found.
func DetectStatusParameter(parameter string, paramset map[string]struct{}) (string, bool) {
	status := parameter + "_STATUS"
	if _, ok := paramset[status]; ok {
		return status, true
	}
	return "", false
}

// ─── DataPointType ───────────────────────────────────────────────────

// DataPointType returns the consumer-facing functional type for this data
// point.
//
// return DATA_POINT_TYPE_CATEGORIES.get(self.category)
//
// When the Kind is unknown or has no canonical type mapping the empty string
// is returned.
func (d *DataPoint[T]) DataPointType() hmenum.DataPointType {
	return d.Kind.DataPointType()
}

// ─── IsStateChange ───────────────────────────────────────────────────

// IsStateChange reports whether sending v to the CCU would represent an actual
// state change — i.e. the new value differs from the current confirmed value,
// or the data point is currently in an uncertain (optimistic) state.
// (model/generic/data_point.py:125):
//
//	return value != self._value or self.state_uncertain
//
// When [validateStateChange] is false (Actions, Buttons) every call returns
// True so sends always proceed — mirroring
// flag (data_point.py:38).
func (d *DataPoint[T]) IsStateChange(v T) bool {
	if !d.validateStateChange {
		return true
	}
	if d.StateUncertain() {
		return true
	}
	current, observed := d.ConfirmedValue()
	if !observed {
		return true
	}
	return !valuesClose(current, v)
}

// ─── IsStateChangeWith ─────────────────────────────────────────────────

// StateChangeOpt is a functional option that sets one dimension of a
// multi-field state-change check on [DataPoint.IsStateChangeWith].
// Each option receives the current confirmed value and the observed
// flag; it returns false when the incoming value for its dimension
// matches the confirmed state, true when a write must go through.
type StateChangeOpt[T comparable] func(current T, observed bool) bool

// IsStateChangeWith is the functional-options companion to [IsStateChange].
// It lets callers test several independent dimensions in one call
// without constructing an intermediate aggregate struct.
//
// Rules (same as [IsStateChange] for the typed path):
//   - When [validateStateChange] is false (Actions, Buttons) always
//     returns true.
//   - When the data point is in [StateUncertain] always returns true.
//   - Otherwise applies each option in order; returns true as soon as
//     one option signals a change. Returns false only when no option
//     signals a change (and at least one option was provided).
//
// When called with zero options, returns the same value as
// [IsStateChange] with the zero value of T.
func (d *DataPoint[T]) IsStateChangeWith(opts ...StateChangeOpt[T]) bool {
	if !d.validateStateChange {
		return true
	}
	if d.StateUncertain() {
		return true
	}
	current, observed := d.ConfirmedValue()
	for _, opt := range opts {
		if opt(current, observed) {
			return true
		}
	}
	return false
}

// WithValue returns a [StateChangeOpt] that signals a state change when
// the provided candidate value v differs from the confirmed value.
// This is the typed functional-options equivalent of [IsStateChange](v).
func WithValue[T comparable](v T) StateChangeOpt[T] {
	return func(current T, observed bool) bool {
		if !observed {
			return true
		}
		return !valuesClose(current, v)
	}
}

// ─── IsRetryable ─────────────────────────────────────────────────────

// IsRetryable reports whether a failed write on this data point may be
// retried. Actions and Buttons return false by default — a retry would fire
// the action twice. All other kinds return true.
func (d *DataPoint[T]) IsRetryable() bool {
	return d.retryable
}

// ─── ValidatesStateChange ───────────────────────────────────────────

// ValidatesStateChange reports whether [IsStateChange] actually performs the
// value-equality check (true) or always returns true (false, for Actions
// Buttons).
func (d *DataPoint[T]) ValidatesStateChange() bool {
	return d.validateStateChange
}

// ─── GetCommandPriority ─────────────────────────────────────────────

// GetCommandPriority returns the command priority this data point uses for
// Southbound writes.
// (model/data_point.py:1101):
//
//	return CommandPriority.HIGH
//
// All generic parameter DPs use HIGH priority; callers that need CRITICAL
// (e.g. Cover.Stop) set the priority explicitly at the call site rather than
// Relying on per-DP override — that matches how
// case.
func (d *DataPoint[T]) GetCommandPriority() hmenum.CommandPriority {
	return hmenum.CommandPriorityHigh
}

// ─── GetEventData ────────────────────────────────────────────────────

// EventData carries the structured fields that the event coordinator attaches
// to a published event.
//
// @dataclass class EventData: channel_address: str parameter: str value: any
//
// The additional fields (interface_id, device_address, full_name) are
// included for north-bound consumers (MQTT event payload, WS event payload).
type EventData struct {
	InterfaceID    string
	ChannelAddress string
	DeviceAddress  string
	Parameter      string
	Value          any
}

// GetEventData constructs an [EventData] from the data point's current state.
//
// return EventData( channel_address=self._channel_address,
// parameter=self._parameter, value=self.value, )
//
// Value is taken from [Value] (optimistic-aware), returning nil when the data
// point has not been observed yet.
func (d *DataPoint[T]) GetEventData() EventData {
	v, observed := d.Value()
	var value any
	if observed {
		value = v
	}
	return d.GetEventDataFor(value)
}

// GetEventDataFor constructs an [EventData] using the explicitly supplied value
// instead of reading from the DP's internal state.
//
// Mirrors (model/data_point.py:1128-1137) where `get_event_data` accepts an
// optional `value` keyword argument so callers can inject a synthetic or
// pre-coerced value into the event payload without mutating the DP.
func (d *DataPoint[T]) GetEventDataFor(value any) EventData {
	key := d.Key
	// Derive device address from channel address by stripping the ":N" suffix.
	deviceAddr := key.ChannelAddress
	if i := lastColon(deviceAddr); i >= 0 {
		deviceAddr = deviceAddr[:i]
	}
	return EventData{
		InterfaceID:    key.InterfaceID,
		ChannelAddress: key.ChannelAddress,
		DeviceAddress:  deviceAddr,
		Parameter:      key.Parameter,
		Value:          value,
	}
}

// lastColon returns the index of the last ':' in s, or -1 when absent.
// Inlined to avoid importing strings for a single call.
func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// ─── AllowedInternalParameters ──────────────────────────────────────

// AllowedInternalParameters is the set of INTERNAL-parameter names that the
// visibility pipeline permits to surface in the model even though their Flags
// field includes FLAG.INTERNAL.
//
// _ALLOWED_INTERNAL_PARAMETERS: Final[Mapping[Field, Parameter]] = {
// Field.DIRECTION: Parameter.DIRECTION, Field.ON_TIME_LIST:
// Parameter.ON_TIME_LIST_1, Field.REPETITIONS: Parameter.REPETITIONS, }
//
// CHANNEL_OPERATION_MODE is included on the Go side because the operation-
// mode gating pipeline reads it directly to derive the effective channel
// usage. Extend with care: every entry exposes an INTERNAL-flagged parameter
// to north-bound adapters.
var AllowedInternalParameters = map[string]struct{}{
	"CHANNEL_OPERATION_MODE": {},
	"DIRECTION":              {},
	"ON_TIME_LIST_1":         {},
	"REPETITIONS":            {},
}
