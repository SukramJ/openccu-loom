// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// schedule_io.go — coordinator-layer calls for climate week profiles,
// separated from the DTO-centric path in schedules.go.
//
// This file ports the async I/O methods from
// (`get_schedule`
// `set_schedule`, `reload_and_cache_schedule`, `copy_profile_to`,
// `copy_schedule_to`) onto [SchedulesDomain]. It works with the structured
// [schedule.Climate] type instead of the SPA DTO so that module-internal
// callers (coordinator, cache-refresh) do not need a JSON round-trip
// through handlers.ClimateSchedule.
//
// The DTO methods in schedules.go remain for SPA/REST paths; the type path
// exposed here is intended for internal consumers: coordinator triggers
// (CONFIG_PENDING reload), bulk-copy operations, and tests that verify the
// wire form directly.

// ErrCopyToSelf bubbles up when a CopyScheduleTo / CopyProfileTo call targets
// the exact same (device, channel, profile) tuple as its source.
var ErrCopyToSelf = errors.New("schedules: copy source equals target")

// ErrInvalidProfileID is returned when a profile key ("P1".."P<max>") is
// outside the per-device profile cap. It wraps a human-readable message
// that includes the device address and the actual cap so callers can
// surface it directly.
var ErrInvalidProfileID = errors.New("schedules: invalid profile id for device")

// ErrProfileCountMismatch is returned by [CopyScheduleTo] when the source
// device exposes fewer profile slots than are present in the schedule being
// copied, which would silently drop profiles on the destination device.
var ErrProfileCountMismatch = errors.New("schedules: source and destination profile counts differ")

// defaultProfileCap is the maximum profile count assumed for devices
// whose ACTIVE_PROFILE / WEEK_PROGRAM_POINTER paramset description
// cannot be found.
const defaultProfileCap = 6

// MaxProfilesForDevice returns the number of profile slots the device
// at deviceAddress exposes (1..6). The count is derived from the
// ParameterData.Max of the ACTIVE_PROFILE (IP) or WEEK_PROGRAM_POINTER
// (RF) VALUES data point on the device's channels:
//
//   - ACTIVE_PROFILE is a 1-based integer; Max == 6 → P1..P6.
//   - WEEK_PROGRAM_POINTER is a 0-based integer; Max == 2 → P1..P3.
//
// When neither parameter is present in the device model (e.g. the
// device has not been hydrated yet, or the registry is nil), the method
// returns ([defaultProfileCap], nil) so callers can always write an
// unconstrained range as a safe fallback rather than blocking the write
// with an ErrUnknownDevice sentinel: devices without an explicit profile
// descriptor are treated as having 6 profiles.
//
// Mirrors `schedule_profile_nos` derived from `_dp_active_profile.max`
// / `_dp_week_program_pointer.max`.
func (s *SchedulesDomain) MaxProfilesForDevice(
	_ context.Context, deviceAddress string,
) (int, error) {
	if s.registry == nil {
		return defaultProfileCap, nil
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		// Walk every channel and probe for the profile-pointer DPs.
		for _, ch := range dev.Channels() {
			// IP path: ACTIVE_PROFILE is a 1-based INTEGER (Min=1, Max=N).
			if dp := ch.Parameter(hmenum.ParameterActiveProfile); dp != nil {
				pd := dp.ParameterData()
				if n, ok := rawJSONInt(pd.Max); ok && n >= 1 && n <= defaultProfileCap {
					return n, nil
				}
			}
			// RF path: WEEK_PROGRAM_POINTER is a 0-based INTEGER (Min=0, Max=N-1).
			if dp := ch.Parameter(hmenum.ParameterWeekProgramPointer); dp != nil {
				pd := dp.ParameterData()
				if n, ok := rawJSONInt(pd.Max); ok && n >= 0 && n < defaultProfileCap {
					return n + 1, nil
				}
			}
		}
		// Device found but neither DP present (e.g. simple cover schedule).
		return defaultProfileCap, nil
	}
	// Device not in any registry → safe default, not an error.
	return defaultProfileCap, nil
}

// rawJSONInt decodes a JSON-encoded numeric raw value (json.RawMessage)
// into an int. Returns (0, false) for empty or non-numeric payloads.
// Used to extract Min/Max from hmproto.ParameterData without pulling in
// a full JSON decoder at every call site.
func rawJSONInt(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return int(f), true
}

// isProfileIDWithinCap reports whether profileID ("P1".."PN") is valid
// for a device whose profile cap is maxProfiles.
func isProfileIDWithinCap(profileID string, maxProfiles int) bool {
	if len(profileID) != 2 || profileID[0] != 'P' {
		return false
	}
	n := int(profileID[1] - '0')
	return n >= 1 && n <= maxProfiles
}

// scheduleCacheEntry stores the most recently fetched climate schedule
// for one (device, channel) pair. Lifetime is bounded by the
// [SchedulesDomain] instance — this is a "hot snapshot" buffer, not a
// persistent cache.
type scheduleCacheEntry struct {
	value *schedule.Climate
}

// scheduleCache is a tiny in-memory map keyed by the channel address
// ("device:no"). It mirrors the per-WeekProfile `_schedule_cache`
// Field on
type scheduleCache struct {
	mu      sync.RWMutex
	entries map[string]scheduleCacheEntry
}

func (c *scheduleCache) get(channelAddr string) (*schedule.Climate, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entries == nil {
		return nil, false
	}
	e, ok := c.entries[channelAddr]
	if !ok {
		return nil, false
	}
	return e.value, true
}

func (c *scheduleCache) put(channelAddr string, value *schedule.Climate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]scheduleCacheEntry, 1)
	}
	c.entries[channelAddr] = scheduleCacheEntry{value: value}
}

func (c *scheduleCache) invalidate(channelAddr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, channelAddr)
}

// climateCache lazily initialises and returns the per-domain cache.
// Allocated on first access so the zero-value [SchedulesDomain] keeps
// working for callers that only use the DTO-pathed methods.
func (s *SchedulesDomain) climateCache() *scheduleCache {
	s.cacheOnce.Do(func() {
		s.cache = &scheduleCache{}
	})
	return s.cache
}

// GetSchedule reads the MASTER paramset for `(deviceAddress, channelNo)`
// and returns the structured [schedule.Climate] schedule. The result is
// memoised in the per-domain cache; pass force=true to bypass the
// cache. Mirrors `ClimateWeekProfile.get_schedule(force_load=…)`.
//
// Returns [ErrNoSchedule] when the channel exposes no climate schedule
// keys, [ErrNoScheduleBackend] when the backend cannot be resolved.
func (s *SchedulesDomain) GetSchedule(
	ctx context.Context, deviceAddress string, channelNo int, force bool,
) (*schedule.Climate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channelAddr := fmt.Sprintf("%s:%d", deviceAddress, channelNo)
	if !force {
		if cached, ok := s.climateCache().get(channelAddr); ok {
			return cached, nil
		}
	}
	return s.ReloadAndCacheSchedule(ctx, deviceAddress, channelNo)
}

// SetWeekday is a convenience helper that writes a single weekday slice
// of a single profile back to the CCU. It mirrors
// `WeekProfile.set_weekday(day, profile_key, days_data)`
// (`week_profile.py:1231`) — useful when a UI lets the user edit one
// day in isolation. Internally it loads the full schedule via
// [GetSchedule], replaces `Profiles[profileKey].Days[day]`, and writes
// the modified schedule back via [SetSchedule]. Returns
// [ErrInvalidProfileID] when profileKey is outside P1..PN for the
// device, and surfaces validation errors from the schedule layer.
func (s *SchedulesDomain) SetWeekday(
	ctx context.Context,
	deviceAddress string,
	channelNo int,
	profileKey string,
	day schedule.Weekday,
	weekday schedule.ClimateWeekday,
) error {
	if err := weekday.Validate(); err != nil {
		return fmt.Errorf("schedules.SetWeekday: %w", err)
	}
	sched, err := s.GetSchedule(ctx, deviceAddress, channelNo, false)
	if err != nil {
		return fmt.Errorf("schedules.SetWeekday: load: %w", err)
	}
	if sched == nil {
		sched = schedule.NewClimate()
	}
	prof, ok := sched.Profiles[profileKey]
	if !ok || prof == nil {
		prof = schedule.NewClimateProfile()
		if err := sched.Put(profileKey, prof); err != nil {
			return fmt.Errorf("schedules.SetWeekday: put profile: %w", err)
		}
	}
	if err := prof.Put(day, weekday); err != nil {
		return fmt.Errorf("schedules.SetWeekday: put weekday: %w", err)
	}
	return s.SetSchedule(ctx, deviceAddress, channelNo, sched)
}

// SetSchedule writes a structured [schedule.Climate] back to the CCU's
// MASTER paramset. The on-disk schedule is converted to the flat
// `P<n>_<FIELD>_<weekday>_<slot>` shape via
// [weekprofile.ClimateToRaw] + [weekprofile.BuildClimateRawParamset].
//
// The local cache is invalidated rather than overwritten optimistically
// Same policy as, which expects the CCU to fire
// CONFIG_PENDING after a paramset write and waits for the reload to
// reflect the persisted shape. Mirrors `set_schedule` from
func (s *SchedulesDomain) SetSchedule(
	ctx context.Context, deviceAddress string, channelNo int, sched *schedule.Climate,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sched == nil {
		return errors.New("schedules: nil schedule")
	}
	if err := sched.Validate(); err != nil {
		return fmt.Errorf("schedules: invalid schedule: %w", err)
	}
	// Validate every profile key against the per-device cap before writing.
	maxProfiles, err := s.MaxProfilesForDevice(ctx, deviceAddress)
	if err != nil {
		return err
	}
	for profileID := range sched.Profiles {
		if !isProfileIDWithinCap(profileID, maxProfiles) {
			return fmt.Errorf("%w: profile %q exceeds device cap P1..P%d for %s",
				ErrInvalidProfileID, profileID, maxProfiles, deviceAddress)
		}
	}
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return err
	}
	rawSched, err := weekprofile.ClimateToRaw(sched)
	if err != nil {
		return fmt.Errorf("schedules: convert to raw: %w", err)
	}
	values, err := weekprofile.BuildClimateRawParamset(rawSched)
	if err != nil {
		return fmt.Errorf("schedules: build paramset: %w", err)
	}
	if len(values) == 0 {
		return errors.New("schedules: empty schedule payload")
	}
	if err := backend.PutParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster, values,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); err != nil {
		return err
	}
	// Invalidate cache; the next GetSchedule call will re-fetch.
	s.climateCache().invalidate(channelAddr)
	s.audit.Record(audit.Entry{
		Action:        audit.ActionScheduleWrite,
		DeviceAddress: deviceAddress,
		ChannelNo:     channelNo,
		Note:          "climate",
	})
	return nil
}

// ReloadAndCacheSchedule unconditionally re-reads the MASTER paramset
// from the backend, parses it via [weekprofile.ParseClimateRawParamset]
// + [weekprofile.RawToClimate], and replaces the cached snapshot.
// Mirrors `reload_and_cache_schedule(force=True)`.
//
// Returns the freshly-loaded value so callers can chain on the result
// without a second cache lookup.
func (s *SchedulesDomain) ReloadAndCacheSchedule(
	ctx context.Context, deviceAddress string, channelNo int,
) (*schedule.Climate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return nil, err
	}
	values, err := backend.GetParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedules: read paramset: %w", err)
	}
	if !hasScheduleParams(values) {
		// Cache the absence too so subsequent (non-force) calls don't
		// re-probe the backend.
		s.climateCache().invalidate(channelAddr)
		return nil, ErrNoSchedule
	}
	rawSched, err := weekprofile.ParseClimateRawParamset(values)
	if err != nil {
		return nil, fmt.Errorf("schedules: parse raw paramset: %w", err)
	}
	sched, err := weekprofile.RawToClimate(rawSched)
	if err != nil {
		return nil, fmt.Errorf("schedules: convert to climate: %w", err)
	}
	s.climateCache().put(channelAddr, sched)
	return sched, nil
}

// CopyScheduleTo copies the entire climate schedule (all profiles,
// every weekday) from `(srcDevice, srcChannel)` to
// `(dstDevice, dstChannel)`. The source is read via [GetSchedule]
// (cache-honouring); the destination is written via [SetSchedule] so
// validation and cache-invalidation run.
//
// Mirrors `copy_schedule_to` from
// the Python overload that copies the entire schedule via raw
// paramset write. Returns [ErrCopyToSelf] when both channels resolve
// to the same address.
func (s *SchedulesDomain) CopyScheduleTo(
	ctx context.Context,
	srcDeviceAddress string, srcChannelNo int,
	dstDeviceAddress string, dstChannelNo int,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if srcDeviceAddress == dstDeviceAddress && srcChannelNo == dstChannelNo {
		return ErrCopyToSelf
	}
	// Verify the destination device can hold as many profiles as the source
	// device exposes. Copying a 6-profile schedule onto a 3-profile device
	// silently discards profiles on the wire — reject it early so the operator
	// gets a descriptive error instead of a silent truncation.
	srcCap, srcErr := s.MaxProfilesForDevice(ctx, srcDeviceAddress)
	dstCap, dstErr := s.MaxProfilesForDevice(ctx, dstDeviceAddress)
	if srcErr == nil && dstErr == nil && srcCap != dstCap {
		return fmt.Errorf("%w: source %s has %d profiles, destination %s has %d",
			ErrProfileCountMismatch, srcDeviceAddress, srcCap, dstDeviceAddress, dstCap)
	}
	src, err := s.GetSchedule(ctx, srcDeviceAddress, srcChannelNo, false)
	if err != nil {
		return fmt.Errorf("schedules: read source: %w", err)
	}
	if src == nil || len(src.Profiles) == 0 {
		return errors.New("schedules: source schedule is empty")
	}
	return s.SetSchedule(ctx, dstDeviceAddress, dstChannelNo, src)
}

// SetProfile writes a single profile slot (e.g. P2) to the CCU's MASTER
// paramset for the given device channel. Only the keys belonging to that
// one profile ("P<n>_TEMPERATURE_<day>_<slot>" and
// "P<n>_ENDTIME_<day>_<slot>") are included in the put_paramset call;
// other profile slots on the device are not touched.
//
// Mirrors `set_profile` from week_profile.py: construct a
// single-profile dict, convert to raw via convert_dict_to_raw_schedule,
// and write only those keys.
//
// The cache is invalidated after the write. The CCU will fire
// CONFIG_PENDING once the write lands; the next [GetSchedule] call
// re-fetches the authoritative state.
func (s *SchedulesDomain) SetProfile(
	ctx context.Context,
	deviceAddress string,
	channelNo int,
	profileKey string,
	prof *schedule.ClimateProfile,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if prof == nil {
		return errors.New("schedules.SetProfile: nil profile")
	}
	if !isValidProfileID(profileKey) {
		return fmt.Errorf("%w: %q is not a valid profile key (P1..P6)", ErrInvalidProfileID, profileKey)
	}
	// Per-device cap validation.
	maxProfiles, err := s.MaxProfilesForDevice(ctx, deviceAddress)
	if err != nil {
		return err
	}
	if !isProfileIDWithinCap(profileKey, maxProfiles) {
		return fmt.Errorf("%w: %q exceeds device cap P1..P%d for %s",
			ErrInvalidProfileID, profileKey, maxProfiles, deviceAddress)
	}
	// Validate every weekday via the full coverage check (user-supplied data
	// must satisfy the 24-hour rule, not just structural validity).
	for day, wd := range prof.Days {
		if err := wd.Validate(); err != nil {
			return fmt.Errorf("schedules.SetProfile: profile %q weekday %s: %w", profileKey, day, err)
		}
	}
	// Wrap the single profile in a Climate container so ClimateToRaw can
	// process it. The resulting raw map will only contain keys prefixed with
	// profileKey (e.g. "P2_TEMPERATURE_MONDAY_1"), leaving all other profile
	// slots untouched on the CCU side.
	single := schedule.NewClimate()
	single.Profiles[profileKey] = prof
	rawSched, err := weekprofile.ClimateToRaw(single)
	if err != nil {
		return fmt.Errorf("schedules.SetProfile: convert to raw: %w", err)
	}
	values, err := weekprofile.BuildClimateRawParamset(rawSched)
	if err != nil {
		return fmt.Errorf("schedules.SetProfile: build paramset: %w", err)
	}
	if len(values) == 0 {
		return errors.New("schedules.SetProfile: empty profile payload")
	}
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return err
	}
	if err := backend.PutParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster, values,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); err != nil {
		return err
	}
	s.climateCache().invalidate(channelAddr)
	s.audit.Record(audit.Entry{
		Action:        audit.ActionScheduleWrite,
		DeviceAddress: deviceAddress,
		ChannelNo:     channelNo,
		Note:          "climate-profile:" + profileKey,
	})
	return nil
}

// CopyProfileTo copies a single profile slot (e.g. P1) from the source
// channel into the target channel under the target profile key (e.g.
// P3). Other profiles on the destination are left untouched — only the
// raw P<targetNum>_* keys are written.
//
// Mirrors `copy_profile_to` from
//
// Validation:
//   - source profile key must exist on the source.
//   - source/target profile keys must be valid ("P1".."P6").
//   - same-channel copy with identical profile keys is rejected as
//     [ErrCopyToSelf].
func (s *SchedulesDomain) CopyProfileTo(
	ctx context.Context,
	srcDeviceAddress string, srcChannelNo int, srcProfile string,
	dstDeviceAddress string, dstChannelNo int, dstProfile string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Basic syntax check (P1..P6) before the more expensive device lookup.
	if !isValidProfileID(srcProfile) {
		return fmt.Errorf("%w: source %q is not a valid profile key (P1..P6)", ErrInvalidProfileID, srcProfile)
	}
	if !isValidProfileID(dstProfile) {
		return fmt.Errorf("%w: target %q is not a valid profile key (P1..P6)", ErrInvalidProfileID, dstProfile)
	}
	// Per-device cap validation: probe the source device's profile count.
	srcCap, err := s.MaxProfilesForDevice(ctx, srcDeviceAddress)
	if err != nil {
		return err
	}
	if !isProfileIDWithinCap(srcProfile, srcCap) {
		return fmt.Errorf("%w: source %q exceeds device cap P1..P%d for %s",
			ErrInvalidProfileID, srcProfile, srcCap, srcDeviceAddress)
	}
	// Per-device cap validation: probe the destination device's profile count.
	dstCap, err := s.MaxProfilesForDevice(ctx, dstDeviceAddress)
	if err != nil {
		return err
	}
	if !isProfileIDWithinCap(dstProfile, dstCap) {
		return fmt.Errorf("%w: target %q exceeds device cap P1..P%d for %s",
			ErrInvalidProfileID, dstProfile, dstCap, dstDeviceAddress)
	}
	sameChannel := srcDeviceAddress == dstDeviceAddress && srcChannelNo == dstChannelNo
	if sameChannel && srcProfile == dstProfile {
		return ErrCopyToSelf
	}

	src, err := s.GetSchedule(ctx, srcDeviceAddress, srcChannelNo, false)
	if err != nil {
		return fmt.Errorf("schedules: read source: %w", err)
	}
	srcProf, ok := src.Profiles[srcProfile]
	if !ok || srcProf == nil {
		return fmt.Errorf("schedules: source profile %q missing", srcProfile)
	}

	// Build a dst-only Climate carrying just the renamed profile slot.
	// ValidateWire accepts partial-day period sets (no 24-hour coverage
	// required) so broken individual slots (endtime < starttime, overlap)
	// are still rejected, but a constant-temperature day or a partial
	// profile read from the CCU wire form passes.
	cloned := schedule.NewClimateProfile()
	for day, wd := range srcProf.Days {
		if err := wd.ValidateWire(); err != nil {
			return fmt.Errorf("schedules: source profile %q %s invalid: %w", srcProfile, day, err)
		}
		cloned.Days[day] = wd
	}
	out := schedule.NewClimate()
	out.Profiles[dstProfile] = cloned

	// Convert + write the patch under the destination key. Use the wire
	// variant so partial-day period sets (no 24h-coverage required) are
	// accepted — gaps are still expanded to base temperature, producing a
	// well-formed 13-slot CCU payload.
	rawSched, err := weekprofile.ClimateToRawWire(out)
	if err != nil {
		return fmt.Errorf("schedules: convert to raw: %w", err)
	}
	values, err := weekprofile.BuildClimateRawParamset(rawSched)
	if err != nil {
		return fmt.Errorf("schedules: build paramset: %w", err)
	}
	if len(values) == 0 {
		return errors.New("schedules: empty profile payload")
	}
	backend, channelAddr, err := s.resolve(dstDeviceAddress, dstChannelNo)
	if err != nil {
		return err
	}
	if err := backend.PutParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster, values,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); err != nil {
		return err
	}
	s.climateCache().invalidate(channelAddr)
	s.audit.Record(audit.Entry{
		Action:        audit.ActionScheduleWrite,
		DeviceAddress: dstDeviceAddress,
		ChannelNo:     dstChannelNo,
		Note:          "climate-profile-copy:" + srcProfile + "->" + dstProfile,
	})
	return nil
}
