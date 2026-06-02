// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package datapoint

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// BaseDataPoint is the common parent contract for every data point
// Family in the daemon. It mirrors
// (model/data_point.py:520) — the identity / visibility / enablement
// surface every DP exposes regardless of whether its underlying value
// originates from an XML-RPC parameter, a system variable, a program,
// a calculated formula, a combined aggregate, a week-profile or a
// hub-level entity.
//
// Concrete DP types satisfy this interface either directly or by
// embedding [BaseDataPointFields].
type BaseDataPoint interface {
	// UniqueID returns a stable identifier for this data point that is
	// idempotent across the daemon's lifecycle. North-bound adapters (MQTT
	// discovery, REST resource paths, WS event keys) rely on it as the canonical
	// addressing token.
	UniqueID() string

	// Visible reports whether the data point should be exposed to north-bound
	// surfaces (REST / MQTT / UI). Defaults to true; a forced
	// [hmenum.DataPointUsageNoCreate] flips it to false.
	Visible() bool

	// EnabledByDefault reports whether the DP is enabled by default (subscribers
	// attached automatically). Same semantics as [Visible] for the foundation
	// layer; concrete DP families may diverge by overriding the embedded helper.
	EnabledByDefault() bool
}

// EventPublisher is the DI abstraction concrete DP types invoke when
// they want to publish a value-changed event. The contract is
// intentionally narrow: a single method that takes the DP's UniqueID
// and the new value. The `internal/central/events` bus, the
// `pkg/hmevent` bus, or any test fake can implement it.
//
// The abstraction is defined here (rather than importing hmevent.Bus
// directly) to avoid an import cycle: hub / generic / custom / etc.
// already pull `pkg/hmevent` indirectly, and the bus would in turn
// want to reference DP types.
type EventPublisher interface {
	PublishUpdate(ctx context.Context, key string, value any)
}

// BaseDataPointFields is the canonical embedding struct concrete data
// point implementations compose to satisfy [BaseDataPoint]. It carries
// only the shared parent state — central scope, address, name —, the
// optional forced-usage override, and an optional [EventPublisher].
//
// Concrete DP types embed [BaseDataPointFields] (typically by value)
// so the [UniqueID] / [Visible] / [EnabledByDefault] methods are
// promoted into their own surface. They keep their own value, status,
// and observation state outside of this struct — this struct
// captures only what is universally shared across all data-point families.
//
// Multi-CCU safety: the `central` field scopes the unique identifier;
// callers MUST pass the per-Unit name so two CCUs cannot
// produce colliding identifiers.
//
// Foundation timestamps: [modifiedAt] and [refreshedAt] mirror
// _modified_at /._refreshed_at
// (model/data_point.py:262-265). Hub, Combined, Calculated, and
// WeekProfile DPs inherit [MarkModified] / [MarkRefreshed]
// [ModifiedAt] / [RefreshedAt] / [ModifiedRecently] / [RefreshedRecently]
// via promotion. [generic.DataPoint[T]] carries its own shadowing
// versions of these methods so its behaviour is unchanged.
type BaseDataPointFields struct {
	central string
	address string
	keyName string

	mu                   sync.RWMutex
	forcedUsage          *hmenum.DataPointUsage
	forcedSensor         bool
	unIgnored            bool
	registered           bool
	publisher            EventPublisher
	availabilityProvider func() bool // optional; nil → always available
	publishedEventAt     time.Time
	modifiedAt           time.Time
	refreshedAt          time.Time

	// unconfirmed timestamp slots. These.
	// `_unconfirmed_refreshed_at` slots declared in
	// CallbackDataPoint.__slots__ (model/data_point.py:233-234).
	// They are updated only by optimistic / write_unconfirmed_value
	// paths and are reset when a CCU-confirmed value arrives.
	// [ModifiedAt] and [RefreshedAt] blend the confirmed and unconfirmed
	// slots, returning the more recent of the two.
	unconfirmedModifiedAt  time.Time
	unconfirmedRefreshedAt time.Time

	// inFlightCommandsCount tracks the number of write commands currently
	// in flight to the CCU for this data point. It is incremented by
	// [IncInFlightCommands] when a write is dispatched and decremented by
	// [DecInFlightCommands] on rollback or confirmation. The counter
	// is accessed atomically so callers on separate goroutines do not
	// need to hold the data point lock.
	inFlightCommandsCount int64

	// unconfirmedLastValueSend holds the last value written for each
	// sub-parameter of a composite/custom data point before the CCU has
	// confirmed the write via a callback event. The map key is the
	// wire-level [hmtypes.DataPointKey] so composite custom DPs that
	// aggregate multiple CCU parameters can track each slot independently.
	//
	// Set by [WriteUnconfirmedValueForKey]; cleared for the matching key
	// by [ConfirmUnconfirmedValueForKey]. North-bound adapters (REST
	// state, MQTT retained payload) surface these optimistic values while
	// the round-trip is in flight so the UI does not flicker back to the
	// last-confirmed value during the latency window.
	//
	// Guarded by mu (the same lock that protects all other mutable base
	// fields). Lazy-allocated — nil until the first write.
	unconfirmedLastValueSend map[hmtypes.DataPointKey]any

	// Cached presentation surface. These fields mirror
	// `_data_point_name_data`, `_path_data`, and
	// `_is_in_multiple_channels` slots in BaseDataPoint
	// (model/data_point.py:534/228/537). They are written once during
	// the DP construction phase via the Set* helpers below and read
	// many times thereafter (every MQTT event, every REST list
	// rendering). Setting them after the DP is shared with concurrent
	// readers is undefined; callers MUST install them before
	// publishing the DP to subscribers.
	nameData             naming.NameData
	pathData             naming.PathData
	isInMultipleChannels bool
}

// PublishedEventWindow matches
// used for `published_event_recently` (model/data_point.py:373).
const publishedEventWindow = 500 * time.Millisecond

// NewBaseDataPointFields constructs a [BaseDataPointFields].
//
// - `central` — the Unit name for multi-CCU scoping. Empty
// is permitted at the type level (some test fixtures), but
// production callers MUST set it.
// - `address` — the device or channel address (e.g. "VCU0123:1").
// For hub-level DPs (sysvar / program) this is the empty string;
// the `keyName` then carries the full identity.
// - `keyName` — the parameter name, the program/sysvar name, or
// the calculated/combined identifier. Required.
func NewBaseDataPointFields(central, address, keyName string) BaseDataPointFields {
	return BaseDataPointFields{
		central: central,
		address: address,
		keyName: keyName,
	}
}

// UniqueID returns the stable identifier in the form
// "<central>:<address>:<keyName>". Empty segments collapse cleanly so hub DPs
// (no address) produce "<central>::<keyName>" and test fixtures that omit the
// central produce "::<keyName>" — both still round-trip uniquely as long as
// callers fill the required fields.
//
// When the data point has been flagged via [MarkForcedSensor], a "_sensor"
// suffix is appended so MQTT discovery distinguishes the read-only sensor
// entity from the original Number/Switch entity the same wire parameter would
// otherwise produce.
//
// return f"{base_unique_id}_{DataPointCategory.SENSOR}"
//
// The format is deliberately simple and ASCII-safe so it passes through MQTT
// topic segments, REST URL paths, and SQL primary keys without escaping.
// North-bound adapters that need a different surface (e.g. MQTT topic with
// slashes) translate from this canonical form rather than each DP family
// inventing its own.
func (b *BaseDataPointFields) UniqueID() string {
	var sb strings.Builder
	// Pre-size: central + address + keyName + 2 separators (+ optional 7-byte "_sensor" suffix).
	sb.Grow(len(b.central) + len(b.address) + len(b.keyName) + 9)
	sb.WriteString(b.central)
	sb.WriteByte(':')
	sb.WriteString(b.address)
	sb.WriteByte(':')
	sb.WriteString(b.keyName)
	if b.IsForcedSensor() {
		sb.WriteString("_sensor")
	}
	return sb.String()
}

// Central returns the Unit name passed at construction.
func (b *BaseDataPointFields) Central() string { return b.central }

// Address returns the device / channel address passed at construction.
func (b *BaseDataPointFields) Address() string { return b.address }

// KeyName returns the parameter / program / sysvar identifier passed
// at construction.
func (b *BaseDataPointFields) KeyName() string { return b.keyName }

// Visible reports whether the data point should be exposed to the
// north-bound. The default is true; a forced
// [hmenum.DataPointUsageNoCreate], [hmenum.DataPointUsageCDPSecondary],
// or [hmenum.DataPointUsageIgnored] (set via [SetForcedUsage]) flips
// it to false.
//
//   - NoCreate marks a generic DP that is consumed by an aggregating
//     parent (Custom / Combined / Week-Profile) and must not surface
//     as its own entity.
//   - CDPSecondary marks the default-disabled HmIP replica channels
//     (Switch 3/4 of a multi-channel HmIP-PSM); MQTT-Discovery
//     translates this to `enabled_by_default: false` so the HA user
//     can activate them through the HA entity registry.
//   - Ignored marks a DP suppressed by the visibility gate's static
//     rules (`IGNORED_PARAMETERS`, `HIDDEN_PARAMETERS`, wildcards,
//     channel-operation-mode mask). User-toggleable through the
//     un-ignore feature; see ADR 0015.
func (b *BaseDataPointFields) Visible() bool {
	b.mu.RLock()
	forced := b.forcedUsage
	b.mu.RUnlock()
	if forced == nil {
		return true
	}
	switch *forced { //nolint:exhaustive // only hidden usages return false; all user-facing usages are visible
	case hmenum.DataPointUsageNoCreate,
		hmenum.DataPointUsageCDPSecondary,
		hmenum.DataPointUsageIgnored:
		return false
	}
	return true
}

// EnabledByDefault reports whether the DP is enabled by default. When no
// usage is forced, the foundation layer assumes the user-facing default and
// returns true; concrete DP families can override the promoted method if they
// need finer-grained classification.
func (b *BaseDataPointFields) EnabledByDefault() bool {
	b.mu.RLock()
	forced := b.forcedUsage
	b.mu.RUnlock()
	if forced == nil {
		return true
	}
	switch *forced { //nolint:exhaustive // CDPSecondary, NoCreate and Ignored are non-user-visible usages; EnabledByDefault correctly returns false for them.
	case hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageCDPVisible,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageEvent:
		return true
	}
	return false
}

// SetForcedUsage installs a forced [hmenum.DataPointUsage] override
// that drives [Visible] / [EnabledByDefault] in subsequent calls.
func (b *BaseDataPointFields) SetForcedUsage(usage hmenum.DataPointUsage) {
	b.mu.Lock()
	v := usage
	b.forcedUsage = &v
	b.mu.Unlock()
}

// ForcedUsage returns the forced usage value installed via
// [SetForcedUsage] together with whether one is set. The boolean is
// false when no forcing has been applied; in that case the returned
// usage value is the zero value.
func (b *BaseDataPointFields) ForcedUsage() (hmenum.DataPointUsage, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.forcedUsage == nil {
		return "", false
	}
	return *b.forcedUsage, true
}

// MarkForcedSensor flips the data point into read-only sensor mode. Concrete
// `IsWritable()` overrides on `*generic.DataPoint[T]` consult
// [IsForcedSensor] so REST/WS adapters reject writes already at the adapter
// layer instead of letting them fail downstream on the CCU.
//
// The call is idempotent: a second invocation is a no-op when the flag is
// already set. This mirrors the guard in force_to_sensor
// (model/data_point.py:1088) that returns early when category == SENSOR.
func (b *BaseDataPointFields) MarkForcedSensor() {
	b.mu.Lock()
	already := b.forcedSensor
	b.forcedSensor = true
	b.mu.Unlock()
	_ = already // idempotent; callers that need the "was already forced" signal can read IsForcedSensor before calling
}

// IsForcedSensor reports whether [MarkForcedSensor] has been called.
// Used by `*generic.DataPoint[T].IsWritable` to deny writes that the
// upstream `_SWITCH_DP_TO_SENSOR` map declares non-writable.
func (b *BaseDataPointFields) IsForcedSensor() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.forcedSensor
}

// MarkUnIgnored flags the data point as operator-un-ignored: the CCU's
// `un_ignore` configuration carries the parameter back into the visible set
// even when the static visibility decider would hide it.
func (b *BaseDataPointFields) MarkUnIgnored() {
	b.mu.Lock()
	b.unIgnored = true
	b.mu.Unlock()
}

// IsUnIgnored reports whether [MarkUnIgnored] has been called. The
// flag is read by [SuppressUndefinedGenericDataPointsWith] in the
// custom package so operator-un-ignored DPs survive the suppression
// pass.
func (b *BaseDataPointFields) IsUnIgnored() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.unIgnored
}

// SetPublisher installs the [EventPublisher] used by [PublishUpdate].
// nil is a valid argument — it disables publishing on this DP.
func (b *BaseDataPointFields) SetPublisher(p EventPublisher) {
	b.mu.Lock()
	b.publisher = p
	b.mu.Unlock()
}

// SetAvailabilityProvider installs the func that [Available] delegates to.
// Typically wired to the owning Device's Available() during DP construction
// so every DP correctly reflects the device's reachability without holding a
// hard back-reference to the device. Pass nil to restore the "always
// available" default.
func (b *BaseDataPointFields) SetAvailabilityProvider(fn func() bool) {
	b.mu.Lock()
	b.availabilityProvider = fn
	b.mu.Unlock()
}

// Available reports whether the data point's owning device is currently
// reachable. When no [SetAvailabilityProvider] has been called, the DP is
// considered always available (returns true). Concrete DP families (hub DPs,
// combined DPs, generic DPs) all inherit this implementation via embedding;
// each is wired to its device's availability during construction.
func (b *BaseDataPointFields) Available() bool {
	b.mu.RLock()
	fn := b.availabilityProvider
	b.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn()
}

// PublishUpdate routes a value-changed notification through the installed
// [EventPublisher], if any. nil-publisher is a silent no-op — concrete DP
// families that want stricter semantics override the promoted method or
// invoke the publisher themselves.
//
// The key passed to the publisher is the DP's [UniqueID] so subscribers can
// dispatch by identity without resolving the DP instance. On each successful
// publish the [PublishedEventAt] timestamp is updated.
func (b *BaseDataPointFields) PublishUpdate(ctx context.Context, value any) {
	b.mu.RLock()
	p := b.publisher
	b.mu.RUnlock()
	if p == nil {
		return
	}
	p.PublishUpdate(ctx, b.UniqueID(), value)
	b.mu.Lock()
	b.publishedEventAt = time.Now()
	b.mu.Unlock()
}

// PublishDataPointUpdatedEvent publishes a value-changed notification
// that includes the previous (old) and next (new) values in the payload.
// This is the implementation — it mirrors
// `publish_data_point_updated_event(old_value=, new_value=)` method
// (model/data_point.py:430-458).
//
// The base-layer implementation routes through the installed
// [EventPublisher] using a structured [UpdatedPayload] so
// north-bound adapters (MQTT, WS) can surface old→new transitions in
// their event payloads without an extra lookup. Pass nil for either
// argument when the value is unknown (e.g. initial load where there
// is no prior confirmed value).
//
// When no publisher is installed (nil) this is a silent no-op.
func (b *BaseDataPointFields) PublishDataPointUpdatedEvent(ctx context.Context, oldValue, newValue any) {
	b.mu.RLock()
	p := b.publisher
	b.mu.RUnlock()
	if p == nil {
		return
	}
	p.PublishUpdate(ctx, b.UniqueID(), UpdatedPayload{
		OldValue: oldValue,
		NewValue: newValue,
	})
	b.mu.Lock()
	b.publishedEventAt = time.Now()
	b.mu.Unlock()
}

// UpdatedPayload is the structured value delivered to
// [EventPublisher.PublishUpdate] by [PublishDataPointUpdatedEvent]. It
// carries both the previous and the new value so MQTT / WS bridges can
// include the full transition in their event payloads.
type UpdatedPayload struct {
	OldValue any
	NewValue any
}

// PublishedEventAt returns the timestamp of the most recent [PublishUpdate]
// or [PublishDataPointUpdatedEvent] call that dispatched to a non-nil
// publisher, or the zero [time.Time] when no event has been published yet.
func (b *BaseDataPointFields) PublishedEventAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.publishedEventAt
}

// PublishedEventRecently reports whether [PublishUpdate] dispatched to a
// non-nil publisher within the last 500 ms.
//
// return self.published_event_at is not None and (datetime.now() -
// self.published_event_at).total_seconds() < 0.5
func (b *BaseDataPointFields) PublishedEventRecently() bool {
	b.mu.RLock()
	t := b.publishedEventAt
	b.mu.RUnlock()
	if t.IsZero() {
		return false
	}
	return time.Since(t) < publishedEventWindow
}

// MarkRegistered flags this data point as registered with the platform (e.g.
// Home Assistant or the internal event subscription table). Used by the
// cleanup path to detect DPs that need to be de-registered on removal.
func (b *BaseDataPointFields) MarkRegistered() {
	b.mu.Lock()
	b.registered = true
	b.mu.Unlock()
}

// UnmarkRegistered clears the registered flag set by [MarkRegistered]. Called
// during entity lifecycle cleanup.
func (b *BaseDataPointFields) UnmarkRegistered() {
	b.mu.Lock()
	b.registered = false
	b.mu.Unlock()
}

// IsRegistered reports whether [MarkRegistered] has been called and
// [UnmarkRegistered] has not subsequently cleared it. Used by the north-bound
// cleanup path to decide whether a DP needs a platform-level de-registration
// call.
func (b *BaseDataPointFields) IsRegistered() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.registered
}

// ─── Foundation timestamps ────────────────────────────────────────────

// MarkModified sets the data point's modified timestamp. Callers (concrete DP
// setter paths for hub/combined/calculated/weekprofile DPs) invoke this when
// the upstream value changes.
//
// Note: [generic.DataPoint[T]] carries its own `modifiedAt` field and shadows
// this method — generic DPs are unaffected.
func (b *BaseDataPointFields) MarkModified(t time.Time) {
	b.mu.Lock()
	b.modifiedAt = t
	b.mu.Unlock()
}

// MarkRefreshed sets the data point's refreshed timestamp. Callers invoke
// this when a new value is received from the CCU regardless of whether the
// value itself changed.
//
// Note: [generic.DataPoint[T]] carries its own `refreshedAt` field and
// shadows this method — generic DPs are unaffected.
func (b *BaseDataPointFields) MarkRefreshed(t time.Time) {
	b.mu.Lock()
	b.refreshedAt = t
	b.mu.Unlock()
}

// ModifiedAt returns the timestamp of the most recent [MarkModified]
// call, blended with the unconfirmed modified timestamp — the later of
// the two is returned. This implements the blend rule:
//
//	if self._unconfirmed_modified_at > self._modified_at:
//	 return self._unconfirmed_modified_at
//	return self._modified_at
//
// Returns the zero [time.Time] when the data point has never been
// modified by either path.
//
// Note: [generic.DataPoint[T]] shadows this with its own implementation.
func (b *BaseDataPointFields) ModifiedAt() time.Time {
	b.mu.RLock()
	confirmed := b.modifiedAt
	unconfirmed := b.unconfirmedModifiedAt
	b.mu.RUnlock()
	if unconfirmed.After(confirmed) {
		return unconfirmed
	}
	return confirmed
}

// RefreshedAt returns the timestamp of the most recent [MarkRefreshed]
// call, blended with the unconfirmed refreshed timestamp — the later of
// the two is returned. This implements the blend rule:
//
//	if self._unconfirmed_refreshed_at > self._refreshed_at:
//	 return self._unconfirmed_refreshed_at
//	return self._refreshed_at
//
// Returns the zero [time.Time] when the data point has never been
// refreshed by either path.
//
// Note: [generic.DataPoint[T]] shadows this with its own implementation.
func (b *BaseDataPointFields) RefreshedAt() time.Time {
	b.mu.RLock()
	confirmed := b.refreshedAt
	unconfirmed := b.unconfirmedRefreshedAt
	b.mu.RUnlock()
	if unconfirmed.After(confirmed) {
		return unconfirmed
	}
	return confirmed
}

// ─── Unconfirmed timestamp slots ───────────────────────────────

// MarkUnconfirmedModified sets the unconfirmed modified timestamp. This is
// invoked by the write_unconfirmed_value path when the value changes.
//
// Note: [generic.DataPoint[T]] shadows this via its own WriteUnconfirmedValue
// implementation that calls the base layer.
func (b *BaseDataPointFields) MarkUnconfirmedModified(t time.Time) {
	b.mu.Lock()
	b.unconfirmedModifiedAt = t
	b.unconfirmedRefreshedAt = t
	b.mu.Unlock()
}

// MarkUnconfirmedRefreshed sets the unconfirmed refreshed timestamp only
// (without touching the modified timestamp). Invoked when the value is the
// same as before (refresh without change).
func (b *BaseDataPointFields) MarkUnconfirmedRefreshed(t time.Time) {
	b.mu.Lock()
	b.unconfirmedRefreshedAt = t
	b.mu.Unlock()
}

// ResetUnconfirmedTimestamps clears both unconfirmed timestamps back to zero.
// Called when a CCU-confirmed value arrives so the blended [ModifiedAt] /
// [RefreshedAt] switch back to the confirmed values.
func (b *BaseDataPointFields) ResetUnconfirmedTimestamps() {
	b.mu.Lock()
	b.unconfirmedModifiedAt = time.Time{}
	b.unconfirmedRefreshedAt = time.Time{}
	b.mu.Unlock()
}

// UnconfirmedModifiedAt returns the last timestamp set by
// [MarkUnconfirmedModified], or zero when no unconfirmed write has
// occurred. Used by tests and diagnostic surfaces.
func (b *BaseDataPointFields) UnconfirmedModifiedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.unconfirmedModifiedAt
}

// UnconfirmedRefreshedAt returns the last timestamp set by
// [MarkUnconfirmedRefreshed] or [MarkUnconfirmedModified], or zero
// when no unconfirmed write has occurred.
func (b *BaseDataPointFields) UnconfirmedRefreshedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.unconfirmedRefreshedAt
}

// ModifiedRecently reports whether [MarkModified] was called within the last
// 500 ms. Returns false when [ModifiedAt] is the zero value (never modified).
//
// return (datetime.now() - self._modified_at).total_seconds() < 0.5
//
// Note: [generic.DataPoint[T]] shadows this with its own implementation.
func (b *BaseDataPointFields) ModifiedRecently() bool {
	b.mu.RLock()
	t := b.modifiedAt
	b.mu.RUnlock()
	if t.IsZero() {
		return false
	}
	return time.Since(t) < publishedEventWindow
}

// RefreshedRecently reports whether [MarkRefreshed] was called within the
// last 500 ms. Returns false when [RefreshedAt] is the zero value (never
// refreshed).
//
// return (datetime.now() - self._refreshed_at).total_seconds() < 0.5
//
// Note: [generic.DataPoint[T]] shadows this with its own implementation.
func (b *BaseDataPointFields) RefreshedRecently() bool {
	b.mu.RLock()
	t := b.refreshedAt
	b.mu.RUnlock()
	if t.IsZero() {
		return false
	}
	return time.Since(t) < publishedEventWindow
}

// IsRefreshed reports whether the data point has received at least one value
// from the CCU.
//
// return self._refreshed_at > INIT_DATETIME
//
// Note: [generic.DataPoint[T]] shadows this with its own implementation
// because it carries an independent `refreshedAt` field. Hub, Combined,
// Calculated, and WeekProfile DPs inherit this implementation via promotion.
func (b *BaseDataPointFields) IsRefreshed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.refreshedAt.IsZero()
}

// ─── Cluster 1 — Cached presentation surface ────────────────────────────────
//
// The Set* helpers install the cached name, path, and multi-channel
// flag once during DP construction. They are NOT safe to call after
// the DP has been published to subscribers — there is no lock around
// The read accessors because every counterpart is
// `Final` (immutable post-init).

// SetNameData installs the cached name quadruple. Construction-phase setter;
// not safe under concurrent reads.
func (b *BaseDataPointFields) SetNameData(nd naming.NameData) {
	b.nameData = nd
}

// NameData returns the cached name quadruple.
func (b *BaseDataPointFields) NameData() naming.NameData {
	return b.nameData
}

// Name returns the entity name without the device prefix. Returns the empty
// string when the DP has no cached NameData installed (test fixtures, hub DPs
// handled via their own type).
func (b *BaseDataPointFields) Name() string {
	return b.nameData.Name()
}

// FullName returns the device-prefixed entity name.
func (b *BaseDataPointFields) FullName() string {
	return b.nameData.FullName()
}

// TranslatedName returns the locale-aware entity name (OCCU-translated
// parameter label) without the device prefix.
func (b *BaseDataPointFields) TranslatedName() string {
	return b.nameData.TranslatedName()
}

// TranslatedFullName returns the locale-aware device-prefixed entity name.
func (b *BaseDataPointFields) TranslatedFullName() string {
	return b.nameData.TranslatedFullName()
}

// SetPathData installs the cached set/state path strings. Construction-phase
// setter; not safe under concurrent reads.
func (b *BaseDataPointFields) SetPathData(pd naming.PathData) {
	b.pathData = pd
}

// PathData returns the cached path quadruple — read [naming.PathData.SetPath]
// and [naming.PathData.StatePath] from the result. Convenience getters
// [BaseDataPointFields.SetPath] and [BaseDataPointFields.StatePath] are also
// exposed for direct access.
func (b *BaseDataPointFields) PathData() naming.PathData {
	return b.pathData
}

// SetPath returns the cached write-side path.
//
// Note on naming: the `Set` prefix here is a noun (the path used for SETTING
// the value), not a verb. The actual setter for the path data is
// [BaseDataPointFields.SetPathData].
func (b *BaseDataPointFields) SetPath() string {
	return b.pathData.SetPath
}

// StatePath returns the cached read-side path.
func (b *BaseDataPointFields) StatePath() string {
	return b.pathData.StatePath
}

// SetIsInMultipleChannels stores the multi-channel flag. Construction-phase
// setter.
func (b *BaseDataPointFields) SetIsInMultipleChannels(v bool) {
	b.isInMultipleChannels = v
}

// IsInMultipleChannels reports whether this DP's parameter exists on multiple
// channels of the same device. Used by the name-resolution pipeline (chN
// postfix) and by north-bound adapters that need to disambiguate per channel.
func (b *BaseDataPointFields) IsInMultipleChannels() bool {
	return b.isInMultipleChannels
}

// ─── In-flight command counter ────────────────────────────────────────

// InFlightCommandsCount returns the current number of write commands in flight
// to the CCU for this data point. Zero means no write is pending.
func (b *BaseDataPointFields) InFlightCommandsCount() int {
	return int(atomic.LoadInt64(&b.inFlightCommandsCount))
}

// IncInFlightCommands increments the in-flight command counter by one.
// Called by the write path immediately before dispatching a value to the CCU.
func (b *BaseDataPointFields) IncInFlightCommands() {
	atomic.AddInt64(&b.inFlightCommandsCount, 1)
}

// DecInFlightCommands decrements the in-flight command counter by one, floored
// at zero. Called by rollback and confirm paths when the CCU write round-trip
// completes. Clamping prevents the counter from going negative if Dec is called
// without a prior Inc.
func (b *BaseDataPointFields) DecInFlightCommands() {
	for {
		cur := atomic.LoadInt64(&b.inFlightCommandsCount)
		if cur <= 0 {
			return
		}
		if atomic.CompareAndSwapInt64(&b.inFlightCommandsCount, cur, cur-1) {
			return
		}
	}
}

// ─── Unconfirmed last-value map ────────────────────────────────────────

// WriteUnconfirmedValueForKey stores value as the pending optimistic write for
// the given sub-parameter key. North-bound adapters surface this value in place
// of the last CCU-confirmed value until [ConfirmUnconfirmedValueForKey] clears
// it. The map is lazy-allocated on the first call.
//
// Thread-safe via the embedded mu lock.
func (b *BaseDataPointFields) WriteUnconfirmedValueForKey(key hmtypes.DataPointKey, value any) {
	b.mu.Lock()
	if b.unconfirmedLastValueSend == nil {
		b.unconfirmedLastValueSend = make(map[hmtypes.DataPointKey]any)
	}
	b.unconfirmedLastValueSend[key] = value
	b.mu.Unlock()
}

// ConfirmUnconfirmedValueForKey removes the pending optimistic entry for key,
// indicating that a CCU callback has confirmed the write. It is a no-op when
// no entry exists for the given key.
//
// Thread-safe via the embedded mu lock.
func (b *BaseDataPointFields) ConfirmUnconfirmedValueForKey(key hmtypes.DataPointKey) {
	b.mu.Lock()
	delete(b.unconfirmedLastValueSend, key)
	b.mu.Unlock()
}

// UnconfirmedValueForKey returns the pending optimistic value for key together
// with a presence boolean. Returns (nil, false) when no unconfirmed write is
// pending for that key.
//
// Thread-safe via the embedded mu lock.
func (b *BaseDataPointFields) UnconfirmedValueForKey(key hmtypes.DataPointKey) (any, bool) {
	b.mu.RLock()
	v, ok := b.unconfirmedLastValueSend[key]
	b.mu.RUnlock()
	return v, ok
}
