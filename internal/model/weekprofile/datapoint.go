// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// datapoint.go — ProfileDataPoint: the device-level descriptor that
// couples a week profile to its CCU metadata (schedule type, entry limits,
// temperature bounds) and exposes a small API for querying and writing the
// schedule.
//
// This is the Go port of the key non-infrastructure parts of
// `WeekProfileDataPoint` and `ClimateWeekProfileDataPoint` from the
// Python reference implementation. The HA- and BaseDataPoint-specific
// wiring (unique_id, publish_data_point_updated_event, channels, event
// bus, etc.) is intentionally omitted here — those concerns live in
// the north/rest and central layers. What remains is the
// schedule-domain logic: metadata, active-entry counting, schedule-enabled
// state management, and profile-pointer tracking.

package weekprofile

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// profileKeyName is the canonical key segment used by
// [ProfileDataPoint]'s promoted [datapoint.BaseDataPointFields.UniqueID].
// Mirrors the `WEEKPROFILE` family identifier — the
// daemon surfaces every device-level week profile under a stable,
// family-prefixed token regardless of whether the underlying CCU
// parameter set differs between IP and RF devices.
const profileKeyName = "WEEKPROFILE"

// ScheduleType identifies whether a ProfileDataPoint wraps a climate or
// a non-climate schedule.
//
// Mirrors `ScheduleType`.
type ScheduleType int

// ScheduleType constants.
const (
	ScheduleTypeDefault ScheduleType = iota // switches, lights, covers, valves
	ScheduleTypeClimate                     // thermostats
)

// maxSimpleEntries is the maximum number of groups in a simple schedule.
// A specific device may declare fewer — see [schedule.SimpleMaxSlot].
const maxSimpleEntries = schedule.SimpleMaxSlot

// maxClimateSlots is the max total slot count across all profiles + weekdays.
// 13 slots × 7 weekdays × 6 profiles = 546.
const maxClimateSlots = 13 * 7 * 6

// ScheduleWriter is the outbound contract a non-climate ProfileDataPoint
// uses to dispatch SetScheduleEnabled writes onto the wire. Identical
// shape to Writer / generic.Writer so the same backend
// implementation backs all three.
type ScheduleWriter interface {
	SetValue(
		ctx context.Context,
		channelAddress string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}

// TargetChannelInfo describes one schedule-controllable channel for the
// non-climate WEEK_PROGRAM_CHANNEL_LOCKS surface. Populated by the
// pipeline from device channel topology; surfaced via
// [ProfileDataPoint.AvailableTargetChannels] for the Zeitplan sensor's
// `available_target_channels` attribute.
type TargetChannelInfo struct {
	ChannelNo      int
	ChannelAddress string
	Name           string
	// ChannelType is "primary" or "secondary".
	ChannelType string
}

// ProfileDataPoint is the domain-level descriptor for a device's week-profile
// data point. It tracks the schedule type, temperature bounds (climate only),
// channel-lock state, and active-profile selection. UniqueID format:
// <central>:<channelAddress>:WEEKPROFILE.
//
// Thread-safe: all mutable fields are guarded by mu. The embedded
// [datapoint.BaseDataPointFields] has its own internal lock independent of mu.
type ProfileDataPoint struct {
	datapoint.BaseDataPointFields

	mu sync.RWMutex

	scheduleType ScheduleType

	// climate-only temperature bounds; zero for non-climate.
	minTemp float64
	maxTemp float64

	// scheduleEnabled maps channel key (e.g. "1_1") to whether the weekly
	// program is active on that channel. nil means the feature is not supported
	// by this device.
	scheduleEnabled map[string]bool

	// availableTargetChannels is the ordered key → channel-info map used
	// for the Zeitplan sensor and ScheduleChannelSwitch creation. Empty
	// for climate devices.
	availableTargetChannels map[string]TargetChannelInfo

	// writer dispatches COMBINED_PARAMETER writes for SetScheduleEnabled.
	// Nil for fixtures that don't need a real CCU path; the in-memory
	// state still updates but no wire-write fires.
	writer ScheduleWriter
	// scheduleChannelAddress is the channel address used as the write
	// target for COMBINED_PARAMETER. Same as the channel the
	// ProfileDataPoint is attached to.
	scheduleChannelAddress string

	// writeHoldUntil maps a channel key to the timestamp until which
	// incoming SyncScheduleEnabled updates for *that key* are ignored
	// after a user-driven write. Wire-read events that arrive within the
	// window are stale pre-write echoes and would otherwise revert the
	// optimistic UI state.
	//
	// The hold is per key because WEEK_PROGRAM_CHANNEL_LOCKS is one
	// device-wide bitfield: a device-wide hold would discard the state of
	// every other channel carried in the same event, and the CCU only
	// pushes the parameter on change, so nothing re-delivers it.
	writeHoldUntil map[string]time.Time

	// currentProfile is the active profile key ("P1".."P6"). Climate only.
	currentProfile string

	// profileCount is how many distinct profile slots this device exposes (1..6).
	profileCount int

	// changeCallbacks are fired after scheduleEnabled or currentProfile changes.
	changeCallbacks []func()

	// climateProfile is the cached schedule wrapper for climate devices.
	// Installed after construction via [AttachClimateProfile] (typically by the
	// device pipeline once a backend is wired). Nil when the descriptor is for a
	// non-climate device or before the backend is bound.
	climateProfile *ClimateProfile

	// simpleProfile is the equivalent for non-climate devices.
	simpleProfile *DefaultProfile

	// scheduleCategory is the device-level DataPointCategory for non-climate
	// devices whose category falls within the schedule-capable set
	// (switch, light, cover, valve, lock). Zero value means "no specific
	// schedule domain" — which is always the case for climate devices and for
	// non-climate devices whose category is not schedule-capable.
	//
	// Read via [ScheduleDomain]; set once at construction via
	// [ProfileDataPointConfig.ScheduleCategory].
	scheduleCategory hmenum.DataPointCategory

	// supportedScheduleFields caches the fields extracted from the MASTER
	// paramset by [SetSupportedScheduleFields]. Nil until the pipeline
	// populates it. Read by [SupportedScheduleFields].
	supportedScheduleFields []hmenum.ScheduleField
}

// ProfileDataPointConfig holds construction parameters for [ProfileDataPoint].
type ProfileDataPointConfig struct {
	// CentralName is the Unit name used to scope the embedded
	// [datapoint.BaseDataPointFields.UniqueID]. Empty is permitted at the
	// type level (test fixtures) but production callers MUST set it; ADR
	// 0002 (multi-CCU first-class) requires the central segment so two
	// CCUs cannot collide on the same channel address.
	CentralName string

	// ChannelAddress is the device / channel address (e.g. "VCU0123:1")
	// the profile is bound to. Used as the address segment of the
	// embedded [datapoint.BaseDataPointFields.UniqueID].
	ChannelAddress string

	// ScheduleType controls which schedule model (climate vs. default) is used.
	ScheduleType ScheduleType

	// MinTemp / MaxTemp are the device's temperature boundaries (climate only).
	// They are used when validating temperature values during write operations.
	MinTemp float64
	MaxTemp float64

	// ProfileCount is the number of named profiles the device supports (1..6).
	// Only relevant for climate devices. Defaults to 1 when zero.
	ProfileCount int

	// InitialProfile is the profile key active on construction. Defaults to
	// "P1" when empty (climate only).
	InitialProfile string

	// ScheduleCategory is the device-level [hmenum.DataPointCategory] for
	// non-climate devices that belong to a schedule-capable domain
	// (switch, light, cover, valve, lock). Leave zero for climate devices
	// and for non-climate devices outside those categories.
	//
	// Propagated to [ProfileDataPoint.ScheduleDomain].
	ScheduleCategory hmenum.DataPointCategory
}

// NewProfileDataPoint constructs a [ProfileDataPoint] from the supplied config.
//
// The constructor signature is unchanged for backwards compatibility —
// the new identity-relevant fields ([ProfileDataPointConfig.CentralName],
// [ProfileDataPointConfig.ChannelAddress]) are additive. Callers that
// want a meaningful UniqueID set them; otherwise the embedded
// [datapoint.BaseDataPointFields] renders `::WEEKPROFILE`.
func NewProfileDataPoint(cfg ProfileDataPointConfig) *ProfileDataPoint {
	dp := &ProfileDataPoint{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(
			cfg.CentralName, cfg.ChannelAddress, profileKeyName,
		),
		scheduleType:     cfg.ScheduleType,
		scheduleCategory: cfg.ScheduleCategory,
		minTemp:          cfg.MinTemp,
		maxTemp:          cfg.MaxTemp,
		profileCount:     cfg.ProfileCount,
	}
	if dp.profileCount < 1 {
		dp.profileCount = 1
	}
	if cfg.ScheduleType == ScheduleTypeClimate {
		dp.currentProfile = cfg.InitialProfile
		if dp.currentProfile == "" {
			dp.currentProfile = "P1"
		}
	}
	// Default-NoCreate: ProfileDataPoint surfaces are internal book-
	// Keeping for the climate / simple schedule UI
	// week-profile model never exposes them as standalone HA entities.
	// Callers that want this DP visible (e.g. a future REST endpoint)
	// must opt in via SetForcedUsage.
	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	return dp
}

// AttachClimateProfile binds a [Profile] of climate type to this DP.
// The pipeline wires this once a backend with read / write access is
// available; downstream consumers (REST, MQTT, UI) then call
// [Climate] to obtain the active wrapper instead of going through
// the SchedulesDomain. nil detaches.
func (dp *ProfileDataPoint) AttachClimateProfile(p *ClimateProfile) {
	dp.mu.Lock()
	dp.climateProfile = p
	dp.mu.Unlock()
}

// AttachSimpleProfile is the non-climate counterpart.
func (dp *ProfileDataPoint) AttachSimpleProfile(p *DefaultProfile) {
	dp.mu.Lock()
	dp.simpleProfile = p
	dp.mu.Unlock()
}

// Climate returns the attached climate profile wrapper or nil when
// the descriptor is for a non-climate device or no backend has been
// bound yet.
func (dp *ProfileDataPoint) Climate() *ClimateProfile {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.climateProfile
}

// Simple returns the attached default-schedule wrapper or nil when
// the descriptor is for a climate device or no backend has been
// bound yet.
func (dp *ProfileDataPoint) Simple() *DefaultProfile {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.simpleProfile
}

// DeviceMetadata bundles the device-level facts the profile DP needs
// to render correctly: temperature bounds and the per-device profile
// cap. Set together via [ProfileDataPoint.ApplyDeviceMetadata] once
// the pipeline has hydrated the relevant VALUES / MASTER descriptors,
// because the pipeline-time `attachWeekProfileToChannel` only knows
// the conservative defaults (0/0/6).
type DeviceMetadata struct {
	MinTemp      float64
	MaxTemp      float64
	ProfileCount int
}

// ApplyDeviceMetadata updates the temperature bounds and profile cap
// in a single locked block. Zero / negative ProfileCount leaves the
// existing value untouched (defending against partial refinements
// where only one of the two source DPs was found).
func (dp *ProfileDataPoint) ApplyDeviceMetadata(meta DeviceMetadata) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	dp.minTemp = meta.MinTemp
	dp.maxTemp = meta.MaxTemp
	if meta.ProfileCount > 0 {
		dp.profileCount = meta.ProfileCount
	}
}

// Signature returns the stable cross-stack identifier for this data point in
// the format "week_profile/{model}/WEEK_PROFILE". The model segment is always
// empty on [ProfileDataPoint] because the struct does not hold a reference to
// the parent device; callers that need the device-scoped form should derive it
// from the parent device's model.
func (dp *ProfileDataPoint) Signature() string {
	return "week_profile//" + profileKeyName
}

// ScheduleType returns the schedule type (climate or default).
func (dp *ProfileDataPoint) ScheduleType() ScheduleType {
	return dp.scheduleType
}

// scheduleDomains is the set of non-climate categories that support
// schedule configuration. Mirrors SCHEDULE_DOMAINS in schedule_models.py.
var scheduleDomains = map[hmenum.DataPointCategory]struct{}{
	hmenum.DataPointCategorySwitch: {},
	hmenum.DataPointCategoryLight:  {},
	hmenum.DataPointCategoryCover:  {},
	hmenum.DataPointCategoryValve:  {},
	hmenum.DataPointCategoryLock:   {},
}

// ScheduleDomain returns the schedule-capable [hmenum.DataPointCategory] for
// this profile data point, plus a boolean that is true when a domain applies.
//
// Climate devices and non-climate devices whose category is not one of
// switch / light / cover / valve / lock return ("", false). Non-climate
// devices with a schedule-capable category return (category, true).
//
// Mirrors the `schedule_domain` property on `WeekProfileDataPoint`:
// returns None for climate and for categories outside SCHEDULE_DOMAINS;
// returns the concrete category otherwise.
func (dp *ProfileDataPoint) ScheduleDomain() (hmenum.DataPointCategory, bool) {
	if dp.scheduleType == ScheduleTypeClimate {
		return "", false
	}
	if _, ok := scheduleDomains[dp.scheduleCategory]; ok {
		return dp.scheduleCategory, true
	}
	return "", false
}

// SupportedScheduleFields returns the schedule fields that this device
// exposes, derived from the device's MASTER paramset. The result is
// cached on first call; subsequent calls return the cached slice.
//
// Mirrors `supported_schedule_fields` on `WeekProfileDataPoint`.
// The underlying extraction logic lives in [ExtractSupportedScheduleFields].
func (dp *ProfileDataPoint) SupportedScheduleFields() []hmenum.ScheduleField {
	dp.mu.RLock()
	cached := dp.supportedScheduleFields
	dp.mu.RUnlock()
	return cached
}

// SetSupportedScheduleFields stores the supported schedule fields extracted
// from the MASTER paramset. Called by the pipeline once the MASTER paramset
// is available. Subsequent calls to [SupportedScheduleFields] return this value.
func (dp *ProfileDataPoint) SetSupportedScheduleFields(fields []hmenum.ScheduleField) {
	dp.mu.Lock()
	dp.supportedScheduleFields = fields
	dp.mu.Unlock()
}

// Value returns the number of active schedule entries for the currently
// active profile (climate) or across all entry groups (non-climate).
// Returns 0 before the first schedule load.
//
// Mirrors `value` on `WeekProfileDataPoint` (returns the active slot count).
// Uses the cached [Profile.Current] snapshot to avoid a CCU round-trip;
// callers that need a live count should call [ReloadSchedule] first.
func (dp *ProfileDataPoint) Value() int {
	dp.mu.RLock()
	cp := dp.climateProfile
	sp := dp.simpleProfile
	dp.mu.RUnlock()
	if cp != nil {
		if sched, err := cp.Current(); err == nil {
			return CountClimateEntries(sched)
		}
	}
	if sp != nil {
		if sched, err := sp.Current(); err == nil {
			return CountSimpleEntries(sched)
		}
	}
	return 0
}

// MaxEntries returns the maximum number of schedulable entries for this device.
//
// Mirrors `max_entries` on `WeekProfileDataPoint`.
func (dp *ProfileDataPoint) MaxEntries() int {
	if dp.scheduleType == ScheduleTypeClimate {
		return maxClimateSlots
	}
	return maxSimpleEntries
}

// MinTemp returns the device's minimum temperature bound (climate only; 0 otherwise).
//
// Mirrors `min_temp` on `WeekProfileDataPoint`.
func (dp *ProfileDataPoint) MinTemp() float64 {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.minTemp
}

// MaxTemp returns the device's maximum temperature bound (climate only; 0 otherwise).
//
// Mirrors `max_temp` on `WeekProfileDataPoint`.
func (dp *ProfileDataPoint) MaxTemp() float64 {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.maxTemp
}

// ProfileCount returns how many distinct profiles this device supports.
// Always 1 for non-climate devices.
//
// Mirrors `schedule_profile_nos` on `ClimateWeekProfileDataPoint`.
func (dp *ProfileDataPoint) ProfileCount() int {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.profileCount
}

// AvailableProfiles returns the set of valid profile keys for this device
// ("P1".."PN" where N == ProfileCount). Non-climate devices return nil.
//
// Mirrors `available_profiles` on `ClimateWeekProfileDataPoint`.
func (dp *ProfileDataPoint) AvailableProfiles() []string {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	if dp.scheduleType != ScheduleTypeClimate {
		return nil
	}
	profiles := make([]string, dp.profileCount)
	for i := range profiles {
		profiles[i] = fmt.Sprintf("P%d", i+1)
	}
	return profiles
}

// CurrentProfile returns the active profile key. Returns "" for non-climate
// devices.
//
// Mirrors `current_schedule_profile` on `ClimateWeekProfileDataPoint`.
func (dp *ProfileDataPoint) CurrentProfile() string {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.currentProfile
}

// SetCurrentProfile sets the active profile key and fires change callbacks if
// the value changed. Returns an error if the key is not in [AvailableProfiles].
//
// Mirrors `set_current_schedule_profile`.
func (dp *ProfileDataPoint) SetCurrentProfile(key string) error {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if dp.scheduleType != ScheduleTypeClimate {
		return errors.New("weekprofile: SetCurrentProfile not applicable to non-climate data points")
	}
	if err := dp.validateProfileKey(key); err != nil {
		return err
	}
	if dp.currentProfile == key {
		return nil
	}
	dp.currentProfile = key
	dp.notifyLocked()
	return nil
}

// SyncProfilePointer maps a raw device parameter value to a profile key and
// calls SetCurrentProfile if it differs from the current value.
//
// IP devices: ACTIVE_PROFILE is a 1-based int (1..6) → "P1".."P6".
// RF devices: WEEK_PROGRAM_POINTER is a 0-based string ("0".."5") → "P1".."P6".
//
// Mirrors `set_profile_pointer_data_point` / `_on_profile_pointer_updated`
// In.
func (dp *ProfileDataPoint) SyncProfilePointer(rawValue any) error {
	key := mapToProfileKey(rawValue)
	if key == "" {
		return nil
	}
	return dp.SetCurrentProfile(key)
}

// mapToProfileKey converts a raw CCU profile pointer value to a "P1".."P6"
// key.  Returns "" if the value cannot be mapped.
func mapToProfileKey(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case int:
		// IP: 1-based
		if val >= 1 && val <= 6 {
			return fmt.Sprintf("P%d", val)
		}
	case int32:
		// Wire form for IP devices — generic.Integer DPs surface
		// values as int32. Same 1-based semantics as `int`.
		n := int(val)
		if n >= 1 && n <= 6 {
			return fmt.Sprintf("P%d", n)
		}
	case int64:
		n := int(val)
		if n >= 1 && n <= 6 {
			return fmt.Sprintf("P%d", n)
		}
	case float64:
		n := int(val)
		if n >= 1 && n <= 6 {
			return fmt.Sprintf("P%d", n)
		}
	case string:
		// RF: 0-based numeric string
		var idx int
		if _, err := fmt.Sscanf(val, "%d", &idx); err == nil {
			p := idx + 1
			if p >= 1 && p <= 6 {
				return fmt.Sprintf("P%d", p)
			}
		}
	}
	return ""
}

// validateProfileKey returns an error if key is not one of the available
// profiles. Must be called with dp.mu held.
func (dp *ProfileDataPoint) validateProfileKey(key string) error {
	for i := 1; i <= dp.profileCount; i++ {
		if key == fmt.Sprintf("P%d", i) {
			return nil
		}
	}
	return fmt.Errorf("weekprofile: unknown profile key %q (device has %d profiles)", key, dp.profileCount)
}

// ---------------------------------------------------------------------------
// Schedule-enabled state (non-climate devices only)
// ---------------------------------------------------------------------------

// scheduleWriteHoldWindow is how long a channel key ignores incoming
// WEEK_PROGRAM_CHANNEL_LOCKS updates after a local write. The CCU echoes
// the pre-write bit value for roughly one to two seconds before the new
// one lands.
const scheduleWriteHoldWindow = 3 * time.Second

// ScheduleEnabled returns the per-channel schedule-enabled map, or nil if the
// feature is not supported by this device.
//
// Mirrors `schedule_enabled` on `WeekProfileDataPoint`.
func (dp *ProfileDataPoint) ScheduleEnabled() map[string]bool {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	if dp.scheduleEnabled == nil {
		return nil
	}
	out := make(map[string]bool, len(dp.scheduleEnabled))
	maps.Copy(out, dp.scheduleEnabled)
	return out
}

// SetScheduleEnabled updates the enabled state for a single channel key
// and dispatches a CCU write through the configured writer when one is
// attached:
//
//   - Resolves channelKey → bitmask via [ChannelKeyToBitmask].
//   - Renders the COMBINED_PARAMETER value as
//     "WPTCLS=<bit>,WPTCL=<0|2>".
//   - Writes to `<scheduleChannelAddress>.COMBINED_PARAMETER` with the
//     supplied priority.
//   - Updates the local scheduleEnabled map and fires change callbacks.
//
// Passing an empty channelKey ("") applies the change to every key
// already registered in the in-memory map and writes the bitwise-OR of
// every available bitmask in a single CCU call. This is the canonical
// "enable/disable all channels at once" path; it is the Go equivalent
// of calling the Python helper with no channel_key argument.
//
// The optimistic state is rolled back when the wire write fails: the
// CCU value never changed, so it never pushes a correcting
// WEEK_PROGRAM_CHANNEL_LOCKS event, and the daemon, MQTT and the SPA
// would keep reporting the opposite of what the device holds until an
// unrelated refresh happens to re-seed it.
//
// When no writer is attached (test fixtures, unattached DPs) the wire
// write is skipped silently and only the in-memory state changes.
func (dp *ProfileDataPoint) SetScheduleEnabled(
	ctx context.Context, channelKey string, enabled bool, priority hmenum.CommandPriority,
) error {
	dp.mu.Lock()
	if dp.scheduleEnabled == nil {
		dp.scheduleEnabled = make(map[string]bool)
	}
	previousEnabled := maps.Clone(dp.scheduleEnabled)
	previousHold := maps.Clone(dp.writeHoldUntil)
	if channelKey == "" {
		for k := range dp.scheduleEnabled {
			dp.scheduleEnabled[k] = enabled
		}
	} else {
		dp.scheduleEnabled[channelKey] = enabled
	}
	writer := dp.writer
	sca := dp.scheduleChannelAddress
	// Arm the wire-read hold window so the post-write
	// WEEK_PROGRAM_CHANNEL_LOCKS echo (which may carry the pre-write
	// bit value for ~1-2s) does not revert our optimistic state. Only
	// the keys this write touches are held — the bitfield carries every
	// other channel too, and those bits must keep flowing.
	until := time.Now().Add(scheduleWriteHoldWindow)
	if dp.writeHoldUntil == nil {
		dp.writeHoldUntil = make(map[string]time.Time, len(dp.scheduleEnabled))
	}
	if channelKey == "" {
		for k := range dp.scheduleEnabled {
			dp.writeHoldUntil[k] = until
		}
	} else {
		dp.writeHoldUntil[channelKey] = until
	}
	dp.notifyLocked()
	dp.mu.Unlock()

	if writer == nil || sca == "" {
		return nil
	}
	var bitmask uint32
	if channelKey == "" {
		// Aggregate write: OR of every known key.
		for k := range dp.ScheduleEnabled() {
			if bit, ok := ChannelKeyToBitmask(k); ok {
				bitmask |= bit
			}
		}
	} else {
		bit, ok := ChannelKeyToBitmask(channelKey)
		if !ok {
			dp.restoreScheduleEnabled(previousEnabled, previousHold)
			return fmt.Errorf("weekprofile: unknown channel key %q", channelKey)
		}
		bitmask = bit
	}
	if bitmask == 0 {
		return nil
	}
	value := BuildCombinedParameterValue(bitmask, enabled)
	if err := writer.SetValue(ctx, sca, hmenum.ParameterCombinedParameter, value, priority); err != nil {
		dp.restoreScheduleEnabled(previousEnabled, previousHold)
		return err
	}
	return nil
}

// restoreScheduleEnabled undoes the optimistic update of a failed
// [SetScheduleEnabled] and re-fires the change callbacks so every
// north-bound surface converges back on the state the CCU still holds.
func (dp *ProfileDataPoint) restoreScheduleEnabled(enabled map[string]bool, hold map[string]time.Time) {
	dp.mu.Lock()
	dp.scheduleEnabled = enabled
	dp.writeHoldUntil = hold
	dp.notifyLocked()
	dp.mu.Unlock()
}

// SyncScheduleEnabled is the read-side counterpart to
// [SetScheduleEnabled]: it updates the in-memory scheduleEnabled map
// without firing a wire write. Used by the EventBridge when a
// WEEK_PROGRAM_CHANNEL_LOCKS event arrives from the CCU and the local
// view needs to mirror the new bitfield.
//
// Fires the change callbacks so subscribers (ChannelSwitch listeners,
// MQTT state publishers) pick up the new state.
//
// Replaces the entire enabled map (mirrors the bitfield's all-channels
// semantics). Keys not present in `state` keep their last value.
func (dp *ProfileDataPoint) SyncScheduleEnabled(state map[string]bool) {
	if state == nil {
		return
	}
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if dp.scheduleEnabled == nil {
		dp.scheduleEnabled = make(map[string]bool, len(state))
	}
	now := time.Now()
	changed := false
	for k, v := range state {
		// Drop wire-read echoes that arrive within the optimistic-hold
		// window — they reflect the pre-write bit value and would
		// otherwise revert a user-driven SetScheduleEnabled toggle. The
		// window is armed per key by [SetScheduleEnabled]; every other
		// key in the same bitfield is applied unconditionally, as is
		// every key outside a window (boot-time initial sync, external
		// CCU edits, periodic refresh).
		if until, held := dp.writeHoldUntil[k]; held {
			if now.Before(until) {
				continue
			}
			delete(dp.writeHoldUntil, k)
		}
		if prev, ok := dp.scheduleEnabled[k]; !ok || prev != v {
			dp.scheduleEnabled[k] = v
			changed = true
		}
	}
	if changed {
		dp.notifyLocked()
	}
}

// SyncChannelLocksFromWire decodes the raw WEEK_PROGRAM_CHANNEL_LOCKS integer
// value and applies the result to the in-memory schedule-enabled map.
//
// This is the boot-time and event-update path: the CCU delivers a bitmask
// integer for the WEEK_PROGRAM_CHANNEL_LOCKS parameter (inverted: a SET bit
// means LOCKED/disabled, a CLEAR bit means ENABLED). This method decodes the
// bitmask via [ParseChannelLocks] using the keys registered in
// [AvailableTargetChannels], then delegates to [SyncScheduleEnabled].
//
// rawValue is the wire value as delivered by the CCU callback — typically
// uint32, int, or float64 depending on the transport and HmIP generation.
// Unrecognised types and nil are silently ignored.
func (dp *ProfileDataPoint) SyncChannelLocksFromWire(rawValue any) {
	dp.mu.RLock()
	atc := dp.availableTargetChannels
	dp.mu.RUnlock()

	var keys []string
	if len(atc) > 0 {
		keys = make([]string, 0, len(atc))
		for k := range atc {
			keys = append(keys, k)
		}
	} else {
		keys = AllChannelKeys()
	}

	var locks uint32
	switch v := rawValue.(type) {
	case uint32:
		locks = v
	case int:
		if v >= 0 {
			locks = uint32(v) //nolint:gosec // non-negative check above prevents overflow; see #20
		}
	case int32:
		if v >= 0 {
			locks = uint32(v) //nolint:gosec // non-negative check above prevents overflow; see #20
		}
	case int64:
		if v >= 0 {
			locks = uint32(v) //nolint:gosec // non-negative check above prevents overflow; see #20
		}
	case float64:
		if v >= 0 {
			locks = uint32(v)
		}
	default:
		return
	}

	state := ParseChannelLocks(locks, keys)
	dp.SyncScheduleEnabled(state)
}

// AttachWriter binds the schedule writer + target channel address used
// by [SetScheduleEnabled] for CCU writes. Called by the pipeline once a
// real backend is wired.
func (dp *ProfileDataPoint) AttachWriter(w ScheduleWriter, scheduleChannelAddress string) {
	dp.mu.Lock()
	dp.writer = w
	dp.scheduleChannelAddress = scheduleChannelAddress
	dp.mu.Unlock()
}

// SetAvailableTargetChannels registers the schedule-controllable target
// channels for this DP. Pipeline-owned. The keys correspond to
// [AllChannelKeys]; each value carries the device channel address +
// human label that surface as `available_target_channels` on the
// Zeitplan sensor.
func (dp *ProfileDataPoint) SetAvailableTargetChannels(channels map[string]TargetChannelInfo) {
	dp.mu.Lock()
	if channels == nil {
		dp.availableTargetChannels = nil
	} else {
		dp.availableTargetChannels = make(map[string]TargetChannelInfo, len(channels))
		maps.Copy(dp.availableTargetChannels, channels)
		// Pre-populate scheduleEnabled with the registered keys so
		// SyncScheduleEnabled has something to merge into and the
		// Zeitplan sensor's schedule_enabled attribute lists every key
		// even before the first WEEK_PROGRAM_CHANNEL_LOCKS event.
		if dp.scheduleEnabled == nil {
			dp.scheduleEnabled = make(map[string]bool, len(channels))
		}
		for k := range channels {
			if _, exists := dp.scheduleEnabled[k]; !exists {
				dp.scheduleEnabled[k] = true
			}
		}
	}
	dp.mu.Unlock()
}

// AvailableTargetChannels returns a snapshot of the registered target
// channels for the Zeitplan sensor.
func (dp *ProfileDataPoint) AvailableTargetChannels() map[string]TargetChannelInfo {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	if dp.availableTargetChannels == nil {
		return nil
	}
	out := make(map[string]TargetChannelInfo, len(dp.availableTargetChannels))
	maps.Copy(out, dp.availableTargetChannels)
	return out
}

// ScheduleChannelAddress returns the channel address used as the write
// target for COMBINED_PARAMETER.
func (dp *ProfileDataPoint) ScheduleChannelAddress() string {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.scheduleChannelAddress
}

// RegisterChannel adds a channel key to the schedule-enabled map with an
// initial state. It is idempotent.
func (dp *ProfileDataPoint) RegisterChannel(channelKey string, initialEnabled bool) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if dp.scheduleEnabled == nil {
		dp.scheduleEnabled = make(map[string]bool)
	}
	if _, exists := dp.scheduleEnabled[channelKey]; !exists {
		dp.scheduleEnabled[channelKey] = initialEnabled
	}
}

// ---------------------------------------------------------------------------
// Active-entry counting
// ---------------------------------------------------------------------------

// CountClimateEntries counts the total number of non-base-temperature period
// entries across the supplied climate schedule.
//
// Mirrors `_count_climate_entries`.
func CountClimateEntries(c *schedule.Climate) int {
	if c == nil {
		return 0
	}
	count := 0
	for _, prof := range c.Profiles {
		if prof == nil {
			continue
		}
		for _, day := range prof.Days {
			count += len(day.Periods)
		}
	}
	return count
}

// CountSimpleEntries counts the active groups in a simple schedule.
// A group counts as active when it has at least one target channel,
// which matches the Python criterion: `if entry.target_channels`.
//
// Mirrors `_count_simple_entries` in week_profile_data_point.py.
func CountSimpleEntries(s *schedule.Simple) int {
	if s == nil {
		return 0
	}
	count := 0
	for _, entry := range s.Entries { //nolint:gocritic // rangeValCopy: map values cannot be addressed; copy is unavoidable
		if len(entry.TargetChannels) > 0 {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// Change notification
// ---------------------------------------------------------------------------

// OnChange registers a callback that is called whenever the profile pointer
// or schedule-enabled state changes. The returned closure unsubscribes
// idempotently.
func (dp *ProfileDataPoint) OnChange(fn func()) func() {
	dp.mu.Lock()
	dp.changeCallbacks = append(dp.changeCallbacks, fn)
	idx := len(dp.changeCallbacks) - 1
	dp.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			dp.mu.Lock()
			defer dp.mu.Unlock()
			if idx < len(dp.changeCallbacks) {
				dp.changeCallbacks[idx] = nil
			}
		})
	}
}

// FireScheduleUpdated notifies all north-bound subscribers that the week
// schedule payload has changed. It fires the internal change callbacks (so
// UI / MQTT state consumers see the updated entry count) and publishes a
// value-changed event through the attached [datapoint.EventPublisher] if one
// is installed. Pass the new value that should appear in the published event
// (typically the updated entry count or the active profile key).
func (dp *ProfileDataPoint) FireScheduleUpdated(ctx context.Context, value any) {
	dp.mu.Lock()
	dp.notifyLocked()
	dp.mu.Unlock()
	dp.PublishUpdate(ctx, value)
}

// ReloadSchedule reloads the underlying profile from the CCU and then fires
// [FireScheduleUpdated] so all north-bound subscribers see the updated
// schedule without needing to coordinate Load + Fire separately.
//
// For climate devices the active ClimateProfile is reloaded; for non-climate
// devices the active DefaultProfile is reloaded. When no profile is attached
// the method is a no-op. The value published via FireScheduleUpdated is the
// current entry count after reload.
func (dp *ProfileDataPoint) ReloadSchedule(ctx context.Context) error {
	dp.mu.RLock()
	cp := dp.climateProfile
	sp := dp.simpleProfile
	dp.mu.RUnlock()

	if cp != nil {
		sched, err := cp.Load(ctx)
		if err != nil {
			return err
		}
		dp.FireScheduleUpdated(ctx, CountClimateEntries(sched))
		return nil
	}
	if sp != nil {
		sched, err := sp.Load(ctx)
		if err != nil {
			return err
		}
		dp.FireScheduleUpdated(ctx, CountSimpleEntries(sched))
		return nil
	}
	return nil
}

// notifyLocked fires all registered change callbacks. Must be called with mu
// held for reading (we snapshot the slice while locked, then fire without lock).
func (dp *ProfileDataPoint) notifyLocked() {
	cbs := make([]func(), len(dp.changeCallbacks))
	copy(cbs, dp.changeCallbacks)
	// Unlock before firing to avoid deadlock if callback calls back into dp.
	dp.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb()
		}
	}
	dp.mu.Lock() // re-acquire so the caller's deferred Unlock is valid.
}
