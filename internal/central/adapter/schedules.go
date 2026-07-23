// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// SchedulesDomain implements interfaces.ScheduleService. It reads/
// writes climate week profiles from/to the MASTER paramset of a
// thermostat channel, converting between the CCU's flat
// P<n>_<FIELD>_<WEEKDAY>_<slot> format and the structured
// ClimateSchedule DTO the SPA renders.
//
// openccu-loom supports up to 6 profiles per device; most HmIP
// thermostats expose P1..P3.
type SchedulesDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
	audit    audit.Recorder

	// cache holds the most recently fetched [schedule.Climate] per
	// (device, channel) pair for the typed I/O path in
	// schedule_io.go. The DTO-pathed methods do not use it. Allocated
	// lazily through [climateCache] so the zero-value SchedulesDomain
	// stays cheap.
	cacheOnce sync.Once
	cache     *scheduleCache
}

// NewSchedulesDomain wires the adapter.
func NewSchedulesDomain(r *central.Registry, w *client.ValueWriter) *SchedulesDomain {
	return &SchedulesDomain{registry: r, writer: w, audit: audit.NoopRecorder()}
}

// SetAuditRecorder rewires the audit recorder. Returns the receiver
// so call sites can chain.
func (s *SchedulesDomain) SetAuditRecorder(rec audit.Recorder) *SchedulesDomain {
	if rec == nil {
		rec = audit.NoopRecorder()
	}
	s.audit = rec
	return s
}

// ErrNoScheduleBackend bubbles up when the CCU backend cannot be
// resolved for the requested channel.
var ErrNoScheduleBackend = errors.New("schedules: no backend for device")

// ErrNoSchedule is returned when the MASTER paramset of a channel
// carries no recognisable P<n>_ENDTIME/TEMPERATURE keys. The SPA
// treats this as "device does not support climate scheduling".
var ErrNoSchedule = errors.New("schedules: channel exposes no climate schedule parameters")

// Wochentage in der CCU-Reihenfolge. Muss mit dem Frontend-Ordering
// synchron bleiben (das Grid rendert in dieser Reihenfolge).
var scheduleWeekdays = []string{
	"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY", "SUNDAY",
}

// slotPattern matches a single schedule-paramset key. Capture groups:
//  1. Profile index (1..6) — EMPTY for the prefix-less schema
//  2. Field (ENDTIME or TEMPERATURE)
//  3. Weekday name
//  4. Slot number (1..13)
//
// The P<n>_ prefix is optional: classic BidCos thermostats
// (HM-CC-RT-DN, HM-CC-RT-DN-BoM) carry a single week profile as bare
// ENDTIME_<DAY>_<N> / TEMPERATURE_<DAY>_<N> keys in the device-level
// MASTER paramset, with no profile prefix and no dedicated channel.
// A bare key is treated as profile P1 throughout. The weekday
// alternation is pinned so bare device-master keys like
// TEMPERATURE_OFFSET never match.
var slotPattern = regexp.MustCompile(
	`^(?:P([1-6])_)?(ENDTIME|TEMPERATURE)_(MONDAY|TUESDAY|WEDNESDAY|THURSDAY|FRIDAY|SATURDAY|SUNDAY)_([0-9]+)$`,
)

// climateScheduleChannelTypes lists the channel types that carry the
// climate week-profile paramset on supported thermostats. Used as
// the candidate set when no dedicated WEEK_PROFILE channel exists
var climateScheduleChannelTypes = map[string]struct{}{
	"CLIMATECONTROL_RT_TRANSCEIVER":      {},
	"CLIMATECONTROL_REGULATOR":           {},
	"HEATING_CLIMATECONTROL_TRANSCEIVER": {},
}

// weekProfileChannelPattern matches every channel type that ends in
// WEEK_PROFILE — examples observed on real CCUs:
//
//	WEEK_PROFILE                  (BidCos heating regulator)
//	SWITCH_WEEK_PROFILE           (HmIP-PSM, HmIP-FSM, HmIP-PS, …)
//	HEATING_WEEK_PROFILE          (HmIP-FALMOT, HmIP-eTRV-CL)
//	COVER_WEEK_PROFILE            (HmIP-BBL, HmIP-BROLL)
//
// 1:1 port of
// (`.*WEEK_PROFILE$` in const.py).
var weekProfileChannelPattern = regexp.MustCompile(`WEEK_PROFILE$`)

// isWeekProfileChannel reports whether the channel type points at a
// dedicated WEEK_PROFILE channel (Path 1 in
// `_resolve_climate_schedule_channel`). Schedules carried by such
// channels use the simple `<NN>_WP_<FIELD>` paramset — never the
// climate `P<n>_*` keys.
func isWeekProfileChannel(channelType string) bool {
	return weekProfileChannelPattern.MatchString(channelType)
}

// FindScheduleChannel locates the channel that carries the climate week
// profile for `deviceAddress`. Returns (channelNo, true) when a candidate is
// found, (0, false) otherwise.
//
// 1. A channel with type == "WEEK_PROFILE" — non-climate devices (covers,
// switches) use this dedicated channel. 2. A climate-control channel that
// carries the P<n>_*_<day>_<slot> MASTER paramset directly (HmIP-eTRV,
// HM-CC-RT-DN, …). We probe the MASTER paramset of the candidate
// channel-types and take the first one that has any P<n>_ENDTIME_* key.
//
// The probe runs serially and stops at the first hit; on a typical thermostat
// that's exactly one MASTER read.
func (s *SchedulesDomain) FindScheduleChannel(ctx context.Context, deviceAddress string) (int, error) {
	if s.registry == nil {
		return 0, ErrNoScheduleBackend
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		// Path 1: explicit *_WEEK_PROFILE channel — used by switches,
		// covers, lights and BidCos heating regulators. Picked first
		// because it's a name-based decision, no MASTER read needed.
		for _, ch := range dev.Channels() {
			if isWeekProfileChannel(ch.Type) {
				return ch.Number, nil
			}
		}
		// Path 2: climate channels carry the schedule directly in
		// their MASTER paramset (P<n>_*). Probe the candidate types
		// in order; return the first match.
		backend, ok := s.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return 0, fmt.Errorf("%w: %s/%s", ErrNoScheduleBackend, u.Name(), dev.InterfaceID)
		}
		for _, ch := range dev.Channels() {
			if _, isClimate := climateScheduleChannelTypes[ch.Type]; !isClimate {
				continue
			}
			values, err := backend.GetParamset(ctx, ch.Address, hmenum.ParamsetKeyMaster)
			if err != nil {
				continue
			}
			if hasScheduleParams(values) || hasSimpleScheduleParams(values) {
				return ch.Number, nil
			}
		}
		// Path 3: classic BidCos thermostats (HM-CC-RT-DN, HM-CC-RT-DN-BoM)
		// carry their single week profile as bare ENDTIME_/TEMPERATURE_
		// keys directly in the device-level MASTER paramset — no P<n>_
		// prefix and no dedicated channel. Probe the device-root MASTER
		// as a last resort and address it via the synthetic device
		// channel number.
		if root, err := backend.GetParamset(ctx, deviceAddress, hmenum.ParamsetKeyMaster); err == nil {
			if hasScheduleParams(root) || hasSimpleScheduleParams(root) {
				return device.ChannelNumberDevice, nil
			}
		}
		return 0, ErrNoSchedule
	}
	// Device exists in no central — distinct from "central wired but
	// no backend": north-bound mappers translate this to 404 via
	// hmerr.ErrDescriptionNotFound.
	return 0, fmt.Errorf("%w: device %s", hmerr.ErrDescriptionNotFound, deviceAddress)
}

// simpleSlotPattern matches one entry of the SimpleSchedule paramset shape:
// "<NN>_WP_<FIELD>" with NN ∈ 01..99 and FIELD any of the well-known
// WP-prefix names (WEEKDAY, FIXED_HOUR, FIXED_MINUTE, LEVEL, …).
var simpleSlotPattern = regexp.MustCompile(`^(\d+)_WP_([A-Z_0-9]+)$`)

// hasScheduleParams returns true when raw contains at least one
// P<n>_ENDTIME_<weekday>_<slot> key — the cheap shape check for
// "this channel carries a climate schedule".
func hasScheduleParams(raw map[string]any) bool {
	for k := range raw {
		if slotPattern.MatchString(k) {
			return true
		}
	}
	return false
}

// hasSimpleScheduleParams returns true when raw contains at least
// one NN_WP_* key — the shape used by switch / cover / light week
// profiles.
func hasSimpleScheduleParams(raw map[string]any) bool {
	for k := range raw {
		if simpleSlotPattern.MatchString(k) {
			return true
		}
	}
	return false
}

// GetClimateScheduleAuto resolves the schedule channel automatically
// before reading. Use this when the SPA does not know which channel
// to ask — e.g. when the schedule tab is rendered on the device level.
func (s *SchedulesDomain) GetClimateScheduleAuto(
	ctx context.Context, deviceAddress string,
) (*hmapi.ClimateSchedule, error) {
	channelNo, err := s.FindScheduleChannel(ctx, deviceAddress)
	if err != nil {
		return nil, err
	}
	return s.GetClimateSchedule(ctx, deviceAddress, channelNo)
}

// PutClimateScheduleAuto resolves the schedule channel before writing.
func (s *SchedulesDomain) PutClimateScheduleAuto(
	ctx context.Context, deviceAddress string, sched *hmapi.ClimateSchedule,
) error {
	channelNo, err := s.FindScheduleChannel(ctx, deviceAddress)
	if err != nil {
		return err
	}
	return s.PutClimateSchedule(ctx, deviceAddress, channelNo, sched)
}

// SetActiveProfileAuto resolves the schedule channel before writing
// the active-profile data point.
func (s *SchedulesDomain) SetActiveProfileAuto(
	ctx context.Context, deviceAddress, profile string,
) error {
	channelNo, err := s.FindScheduleChannel(ctx, deviceAddress)
	if err != nil {
		return err
	}
	return s.SetActiveProfile(ctx, deviceAddress, channelNo, profile)
}

// ErrScheduleCopyNoOp is returned when a copy would read and write the
// same channel/profile (or device), which is always a no-op and almost
// always a caller mistake.
var ErrScheduleCopyNoOp = errors.New("schedules: copy source and destination are identical")

// ErrScheduleCopyProfileRange is returned when a profile index is
// outside the supported 1..6 range.
var ErrScheduleCopyProfileRange = errors.New("schedules: profile index out of range (1..6)")

// CopyClimateProfile copies a single climate week-profile from the
// source channel/profile to the destination channel/profile. It reads
// the full climate schedule of the source channel, lifts out profile
// P<srcProfile>, and writes it under P<dstProfile> on the destination
// channel — composed from the existing get + put primitives so the
// CCU-side filtering and false-positive handling are reused as-is.
//
// Mirrors the Python reference's copy_schedule_profile
// (model/week_profile_data_point.py:556).
func (s *SchedulesDomain) CopyClimateProfile(
	ctx context.Context,
	srcChannelAddress string, srcProfile int,
	dstChannelAddress string, dstProfile int,
) error {
	if srcProfile < 1 || srcProfile > 6 || dstProfile < 1 || dstProfile > 6 {
		return ErrScheduleCopyProfileRange
	}
	if srcChannelAddress == dstChannelAddress && srcProfile == dstProfile {
		return ErrScheduleCopyNoOp
	}
	srcDevice, srcChannelNo := splitChannelAddress(srcChannelAddress)
	dstDevice, dstChannelNo := splitChannelAddress(dstChannelAddress)

	src, err := s.GetClimateSchedule(ctx, srcDevice, srcChannelNo)
	if err != nil {
		return fmt.Errorf("schedules: copy_profile read source: %w", err)
	}
	if src.Kind != "" && src.Kind != "climate" {
		return fmt.Errorf("schedules: copy_profile source is not a climate schedule (kind=%q)", src.Kind)
	}
	srcKey := fmt.Sprintf("P%d", srcProfile)
	profile, ok := src.Profiles[srcKey]
	if !ok {
		return fmt.Errorf("schedules: copy_profile source has no profile %s", srcKey)
	}

	// Write only the destination profile so unrelated profiles on the
	// destination channel stay intact.
	dst := &hmapi.ClimateSchedule{
		Kind: "climate",
		Profiles: map[string]hmapi.ClimateProfile{
			fmt.Sprintf("P%d", dstProfile): profile,
		},
	}
	if err := s.PutClimateSchedule(ctx, dstDevice, dstChannelNo, dst); err != nil {
		return fmt.Errorf("schedules: copy_profile write destination: %w", err)
	}
	return nil
}

// CopySchedule copies the entire week schedule of the source device to
// the destination device. The schedule channel is auto-resolved on both
// sides (mirrors the device-level get/put convenience path), so the
// caller only supplies device addresses.
//
// Mirrors the Python reference's copy_schedule
// (model/week_profile_data_point.py:548).
func (s *SchedulesDomain) CopySchedule(
	ctx context.Context, srcDeviceAddress, dstDeviceAddress string,
) error {
	if srcDeviceAddress == dstDeviceAddress {
		return ErrScheduleCopyNoOp
	}
	src, err := s.GetClimateScheduleAuto(ctx, srcDeviceAddress)
	if err != nil {
		return fmt.Errorf("schedules: copy read source: %w", err)
	}
	// Clear the destination-resolved channel reference; PutClimateScheduleAuto
	// resolves the destination's own channel and serialises by kind.
	src.Channel = hmapi.ScheduleChannelRef{}
	if err := s.PutClimateScheduleAuto(ctx, dstDeviceAddress, src); err != nil {
		return fmt.Errorf("schedules: copy write destination: %w", err)
	}
	return nil
}

// GetClimateSchedule reads the MASTER paramset of the channel and
// returns either a climate (P<n>_*) or a simple (NN_WP_*) schedule
// in the unified DTO. The "kind" field disambiguates.
func (s *SchedulesDomain) GetClimateSchedule(
	ctx context.Context, deviceAddress string, channelNo int,
) (*hmapi.ClimateSchedule, error) {
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return nil, err
	}
	values, err := backend.GetParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedules: read paramset: %w", err)
	}
	chRef := hmapi.ScheduleChannelRef{
		Address: channelAddr,
		Number:  channelNo,
		Device:  deviceAddress,
	}
	if hasScheduleParams(values) {
		dto, err := parseClimateSchedule(values)
		if err != nil {
			return nil, err
		}
		dto.Kind = "climate"
		dto.Channel = chRef
		// ACTIVE_PROFILE lives as a VALUES data point. Best-effort.
		if active, ok := s.readActiveProfile(ctx, backend, channelAddr); ok {
			dto.ActiveProfile = active
		}
		return dto, nil
	}
	if hasSimpleScheduleParams(values) {
		domain := s.detectScheduleDomain(deviceAddress, channelNo)
		entries := parseSimpleScheduleWithDomain(values, domain)
		return &hmapi.ClimateSchedule{
			Channel:       chRef,
			Kind:          "simple",
			Domain:        domain,
			SimpleEntries: entries,
			ColorCapable:  hasColorScheduleParams(values),
		}, nil
	}
	return nil, ErrNoSchedule
}

// hasColorScheduleParams reports whether the MASTER values carry any
// per-switch-point colour/effect field (universal lights) or an
// OUTPUT_BEHAVIOUR field (HmIP-BSL) — the signal the SPA gates its
// colour summary on.
func hasColorScheduleParams(raw map[string]any) bool {
	for k := range raw {
		if strings.HasSuffix(k, "_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE") ||
			strings.HasSuffix(k, "_WP_OUTPUT_BEHAVIOUR") {
			return true
		}
	}
	return false
}

// detectScheduleDomain inspects the device's main channel types to
// classify the schedule into one of the user-facing buckets. Same
// Idea as
// which fields the SPA editor shows.
func (s *SchedulesDomain) detectScheduleDomain(deviceAddress string, scheduleChannelNo int) string {
	if s.registry == nil {
		return ""
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		// The schedule channel itself carries a type like
		// SWITCH_WEEK_PROFILE — the prefix is the strongest hint.
		for _, ch := range dev.Channels() {
			if ch.Number != scheduleChannelNo {
				continue
			}
			if d := domainFromWeekProfileType(ch.Type); d != "" {
				return d
			}
		}
		// Fallback: scan main channels for a known actor type.
		for _, ch := range dev.Channels() {
			if d := domainFromActorType(ch.Type); d != "" {
				return d
			}
		}
		return ""
	}
	return ""
}

// domainFromWeekProfileType maps a "<X>_WEEK_PROFILE" channel type to
// the user-facing domain bucket.
func domainFromWeekProfileType(channelType string) string {
	switch {
	case strings.HasPrefix(channelType, "SWITCH_WEEK"):
		return "switch"
	case strings.HasPrefix(channelType, "DIMMER_WEEK"), strings.HasPrefix(channelType, "LIGHT_WEEK"):
		return "light"
	case strings.HasPrefix(channelType, "BLIND_WEEK"), strings.HasPrefix(channelType, "COVER_WEEK"),
		strings.HasPrefix(channelType, "SHUTTER_WEEK"):
		return "cover"
	case strings.HasPrefix(channelType, "LOCK_WEEK"), strings.HasPrefix(channelType, "DOOR_LOCK_WEEK"):
		return "lock"
	case strings.HasPrefix(channelType, "VALVE_WEEK"), strings.HasPrefix(channelType, "WATER_WEEK"):
		return "valve"
	case strings.HasPrefix(channelType, "HEATING_WEEK"), strings.HasPrefix(channelType, "CLIMATECONTROL_WEEK"):
		return "climate"
	}
	return ""
}

// domainFromActorType maps a regular channel type to a domain
// bucket. Used when the WEEK_PROFILE-type prefix is generic.
func domainFromActorType(channelType string) string {
	switch {
	case strings.HasPrefix(channelType, "SWITCH"), strings.HasPrefix(channelType, "ENERGIE_METER"):
		return "switch"
	case strings.HasPrefix(channelType, "DIMMER"),
		strings.Contains(channelType, "DIMMER"),
		strings.HasPrefix(channelType, "LIGHT"),
		strings.HasPrefix(channelType, "OPTICAL_SIGNAL"):
		return "light"
	case strings.HasPrefix(channelType, "BLIND"),
		strings.HasPrefix(channelType, "SHUTTER"),
		strings.HasPrefix(channelType, "COVER"):
		return "cover"
	case strings.HasPrefix(channelType, "DOOR_LOCK"), strings.HasPrefix(channelType, "KEYMATIC"):
		return "lock"
	case strings.HasPrefix(channelType, "WATER"), strings.HasPrefix(channelType, "VALVE"):
		return "valve"
	}
	return ""
}

// --- Simple-Schedule parser / serializer --------------------------
// Covers the full field
// set: weekday bitmask, condition, astro type + offset, target
// channels, level + level_2, duration, ramp_time. Unknown / future
// fields are preserved on writes (we only set keys we know).

// --- Lock-domain encoding -----------------------------------------
// 1:1 port of the lock encoding tables.
// model/schedule_models.py. Lock schedules don't have dedicated
// wire fields — the CCU encodes lock_mode / lock_action / permission
// via specific (LEVEL, DURATION_BASE, DURATION_FACTOR, TARGET_CHANNELS)
// combinations. This adapter translates back and forth so the SPA
// can render a friendly "Lock | Unlock | Auto-relock"-picker.

// lockActionToRaw converts a door-lock action string to its wire encoding.
// The canonical table lives in [schedule.LockActionTable]; this helper bridges
// the string-keyed REST surface to the typed domain constants.
func lockActionRawFor(action string) (level float64, durBase, durFactor int, ok bool) {
	return schedule.EncodeLockAction(schedule.LockAction(action))
}

const (
	lockPermissionDurationBase   = 7  // HOUR_1
	lockPermissionDurationFactor = 31 // -> sentinel "always"
)

// detectLockMode: channels with "1_" prefix indicate door_lock mode.
func detectLockMode(targetChannels []string) string {
	for _, ch := range targetChannels {
		if strings.HasPrefix(ch, "1_") {
			return "door_lock"
		}
	}
	return "user_permission"
}

// detectLockAction reverses the wire encoding to its canonical label.
// Falls back to "lock_autorelock_start" (the zero-value encoding) when nothing matches.
func detectLockAction(level float64, durBase, durFactor int) string {
	return string(schedule.DetectLockAction(level, durBase, durFactor))
}

// detectLockPermission reads the LEVEL flag (>= 0.5 → granted).
func detectLockPermission(level float64) string {
	if level >= 0.5 {
		return "granted"
	}
	return "not_granted"
}

// scheduleConditionByID maps the CCU's CONDITION integer to the
// Human-readable string the SPA uses. 1:1 port.
// ScheduleCondition IntEnum.
var scheduleConditionByID = map[int]string{
	0: "fixed_time",
	1: "astro",
	2: "fixed_if_before_astro",
	3: "astro_if_before_fixed",
	4: "fixed_if_after_astro",
	5: "astro_if_after_fixed",
	6: "earliest_of_fixed_and_astro",
	7: "latest_of_fixed_and_astro",
}

// scheduleConditionIDByName is the reverse table.
var scheduleConditionIDByName = func() map[string]int {
	out := make(map[string]int, len(scheduleConditionByID))
	for id, name := range scheduleConditionByID {
		out[name] = id
	}
	return out
}()

// maxScheduleFactor is the highest DURATION_FACTOR / RAMP_TIME_FACTOR
// the CCU firmware accepts via put_paramset. factor=31 is reserved as
// the internal "permanent" sentinel (also the firmware default for
// unset duration slots) and is rejected on write with xml-rpc fault -5.
const maxScheduleFactor = 30

// TimeBaseSecondsScheduleField mirrors
// duration (in seconds) one factor unit represents. Reused for both
// DURATION and RAMP_TIME pairs.
var timeBaseSecondsScheduleField = []float64{
	0.1,  // MS_100
	1,    // SEC_1
	5,    // SEC_5
	10,   // SEC_10
	60,   // MIN_1
	300,  // MIN_5
	600,  // MIN_10
	3600, // HOUR_1
}

// scheduleActorChannelByBit decodes the TARGET_CHANNELS bitmask into
// The "X_Y" channel-function strings Layout
// CHANNEL_1_1=1, CHANNEL_1_2=2, CHANNEL_1_3=4, CHANNEL_2_1=8, …
var scheduleActorChannelByBit = func() []struct {
	bit  int
	name string
} {
	out := make([]struct {
		bit  int
		name string
	}, 0, 24)
	for ch := 1; ch <= 8; ch++ {
		for fn := 1; fn <= 3; fn++ {
			bit := 1 << ((ch-1)*3 + (fn - 1))
			out = append(out, struct {
				bit  int
				name string
			}{bit: bit, name: fmt.Sprintf("%d_%d", ch, fn)})
		}
	}
	return out
}()

// weekdayBits maps weekday names to the bitmask the CCU's
// `<NN>_WP_WEEKDAY` parameter expects. Same layout as Python's
// Weekday IntEnum: Monday=2, Tuesday=4, … Sunday=64.
var weekdayBits = map[string]int{
	"MONDAY":    1 << 1,
	"TUESDAY":   1 << 2,
	"WEDNESDAY": 1 << 3,
	"THURSDAY":  1 << 4,
	"FRIDAY":    1 << 5,
	"SATURDAY":  1 << 6,
	"SUNDAY":    1 << 7,
}

// weekdayNamesByBit is the reverse table for parsing.
var weekdayNamesByBit = []struct {
	bit  int
	name string
}{
	{1 << 1, "MONDAY"},
	{1 << 2, "TUESDAY"},
	{1 << 3, "WEDNESDAY"},
	{1 << 4, "THURSDAY"},
	{1 << 5, "FRIDAY"},
	{1 << 6, "SATURDAY"},
	{1 << 7, "SUNDAY"},
}

// parseSimpleSchedule extracts active slots from the raw paramset.
// A slot counts as "active" when its WEEKDAY bitmask is non-zero
// All fields
// recognises are decoded; absent fields default to zero values.
// `domain` enables domain-specific post-processing — currently used
// for "lock" to derive lock_mode / lock_action / permission from
// the level/duration/target_channels combination.
// simpleScheduleUnsupportedFields names the SimpleScheduleEntry fields
// the per-domain validator in schedule.SimpleEntry.ValidateFor rejects.
// Used to clear fields the read path picked up from the CCU MASTER
// paramset that don't apply to the device category — without this,
// devices like HmIP-PSMCO surface RAMP_TIME_BASE/FACTOR on a SWITCH
// channel, the read emits RampTime, and a subsequent set_schedule
// then fails the validator.
//
// Field names mirror the FQ identifiers ValidateFor checks:
//
//	level_2 / ramp_time / duration
//
// Keep this table in sync with internal/model/schedule/simple.go::ValidateFor.
var simpleScheduleUnsupportedFields = map[string]map[string]struct{}{
	"switch": {"level_2": {}, "ramp_time": {}},
	"light":  {"level_2": {}},
	"cover":  {"ramp_time": {}, "duration": {}},
	"valve":  {"level_2": {}, "ramp_time": {}},
	"lock":   {"level_2": {}, "ramp_time": {}, "duration": {}},
}

// stripUnsupportedFields nulls fields the domain validator rejects so
// the parsed entry survives a Parse → Validate → Build round-trip.
func stripUnsupportedFields(entries []hmapi.SimpleScheduleEntry, domain string) {
	unsupported, ok := simpleScheduleUnsupportedFields[domain]
	if !ok {
		return
	}
	for i := range entries {
		e := &entries[i]
		if _, drop := unsupported["level_2"]; drop {
			e.Level2 = nil
		}
		if _, drop := unsupported["ramp_time"]; drop {
			e.RampTime = ""
		}
		if _, drop := unsupported["duration"]; drop {
			e.Duration = ""
		}
	}
}

func parseSimpleScheduleWithDomain(raw map[string]any, domain string) []hmapi.SimpleScheduleEntry {
	entries := parseSimpleSchedule(raw)
	stripUnsupportedFields(entries, domain)
	if domain != "lock" {
		return entries
	}
	// Lock devices encode mode / action / permission via combinations
	// of LEVEL, DURATION and TARGET_CHANNELS. Surface them as
	// dedicated fields so the SPA can render the friendly picker.
	// Note: the duration string was already cleared by
	// stripUnsupportedFields above (LOCK forbids it on validation),
	// so we decode from the original RAW paramset, not from the
	// stripped entry.
	for i := range entries {
		e := &entries[i]
		e.LockMode = detectLockMode(e.TargetChannels)
		dBase, dFactor := lookupSlotDuration(raw, e.SlotNo)
		switch e.LockMode {
		case "door_lock":
			e.LockAction = detectLockAction(e.Level, dBase, dFactor)
		case "user_permission":
			e.Permission = detectLockPermission(e.Level)
		}
	}
	return entries
}

// lookupSlotDuration reads DURATION_BASE/FACTOR for the named slot
// directly from the raw paramset. Called by the lock branch after
// stripUnsupportedFields cleared the Duration string on the entry.
// Slot keys follow [simpleSlotPattern] (`NN_WP_<FIELD>`); the slot
// number is zero-padded to two digits in the wire shape.
func lookupSlotDuration(raw map[string]any, slotNo int) (durationBase, durationFactor int) {
	prefix := fmt.Sprintf("%02d_WP_", slotNo)
	dBase, _ := coerceInt(raw[prefix+"DURATION_BASE"])
	dFactor, _ := coerceInt(raw[prefix+"DURATION_FACTOR"])
	return dBase, dFactor
}

func parseSimpleSchedule(raw map[string]any) []hmapi.SimpleScheduleEntry { //nolint:gocognit,gocyclo,funlen // single-purpose schedule parsing logic with many branches
	type slot struct {
		weekday          int
		hour             int
		minute           int
		condition        int
		conditionSeen    bool
		astroType        int
		astroTypeSeen    bool
		astroOffset      int
		targetChannels   int
		targetChannelsOK bool
		level            float64
		level2           float64
		level2Seen       bool
		durationBase     int
		durationBaseSeen bool
		durationFactor   int
		durationFactorOK bool
		rampBase         int
		rampBaseSeen     bool
		rampFactor       int
		rampFactorOK     bool
		colorType        int
		colorTypeSeen    bool
		colorValue       int
		colorValueSeen   bool
		outputBehaviour  int
		outputBehSeen    bool
		seen             bool
	}
	bySlot := make(map[int]*slot)
	for k, v := range raw {
		m := simpleSlotPattern.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		slotNo, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		field := m[2]
		s, ok := bySlot[slotNo]
		if !ok {
			s = &slot{}
			bySlot[slotNo] = s
		}
		s.seen = true
		switch field {
		case "WEEKDAY":
			if i, ok := coerceInt(v); ok {
				s.weekday = i
			}
		case "FIXED_HOUR":
			if i, ok := coerceInt(v); ok {
				s.hour = i
			}
		case "FIXED_MINUTE":
			if i, ok := coerceInt(v); ok {
				s.minute = i
			}
		case "CONDITION":
			if i, ok := coerceInt(v); ok {
				s.condition = i
				s.conditionSeen = true
			}
		case "ASTRO_TYPE":
			if i, ok := coerceInt(v); ok {
				s.astroType = i
				s.astroTypeSeen = true
			}
		case "ASTRO_OFFSET":
			if i, ok := coerceInt(v); ok {
				s.astroOffset = i
			}
		case "TARGET_CHANNELS":
			if i, ok := coerceInt(v); ok {
				s.targetChannels = i
				s.targetChannelsOK = true
			}
		case "LEVEL":
			if f, ok := coerceFloat(v); ok {
				s.level = f
			}
		case "LEVEL_2":
			if f, ok := coerceFloat(v); ok {
				s.level2 = f
				s.level2Seen = true
			}
		case "DURATION_BASE":
			if i, ok := coerceInt(v); ok {
				s.durationBase = i
				s.durationBaseSeen = true
			}
		case "DURATION_FACTOR":
			if i, ok := coerceInt(v); ok {
				s.durationFactor = i
				s.durationFactorOK = true
			}
		case "RAMP_TIME_BASE":
			if i, ok := coerceInt(v); ok {
				s.rampBase = i
				s.rampBaseSeen = true
			}
		case "RAMP_TIME_FACTOR":
			if i, ok := coerceInt(v); ok {
				s.rampFactor = i
				s.rampFactorOK = true
			}
		case "HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE":
			if i, ok := coerceInt(v); ok {
				s.colorType = i
				s.colorTypeSeen = true
			}
		case "HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE":
			if i, ok := coerceInt(v); ok {
				s.colorValue = i
				s.colorValueSeen = true
			}
		case "OUTPUT_BEHAVIOUR":
			if i, ok := coerceInt(v); ok {
				s.outputBehaviour = i
				s.outputBehSeen = true
			}
		}
	}
	keys := make([]int, 0, len(bySlot))
	for k := range bySlot {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]hmapi.SimpleScheduleEntry, 0, len(keys))
	for _, k := range keys {
		s := bySlot[k]
		if !s.seen || s.weekday == 0 {
			continue // inactive slot
		}
		entry := hmapi.SimpleScheduleEntry{
			SlotNo:             k,
			Weekdays:           weekdayBitsToNames(s.weekday),
			Time:               fmt.Sprintf("%02d:%02d", s.hour, s.minute),
			Level:              s.level,
			AstroOffsetMinutes: s.astroOffset,
		}
		if s.conditionSeen {
			if name, ok := scheduleConditionByID[s.condition]; ok {
				entry.Condition = name
			}
		}
		if entry.Condition == "" {
			entry.Condition = "fixed_time"
		}
		if entry.Condition != "fixed_time" && s.astroTypeSeen {
			switch s.astroType {
			case 0:
				entry.AstroType = "sunrise"
			case 1:
				entry.AstroType = "sunset"
			}
		}
		if s.targetChannelsOK && s.targetChannels != 0 {
			entry.TargetChannels = decodeTargetChannels(s.targetChannels)
		}
		if s.level2Seen {
			lvl := s.level2
			entry.Level2 = &lvl
		}
		// Skip duration emission for the CCU-side "permanent" sentinel
		// (factor == 31) and any other value the device might have parked
		// above the writable cap of 30. The CCU stores 31 as a firmware
		// default on unused duration slots but rejects writes of factor>30
		// via put_paramset (xml-rpc fault -5). Surfacing "31h" to the SPA
		// would create a round-trip the CCU then refuses to accept.
		if s.durationBaseSeen && s.durationFactorOK && s.durationFactor > 0 && s.durationFactor <= maxScheduleFactor {
			entry.Duration = formatTimeBaseFactor(s.durationBase, s.durationFactor)
		}
		if s.rampBaseSeen && s.rampFactorOK && s.rampFactor > 0 && s.rampFactor <= maxScheduleFactor {
			entry.RampTime = formatTimeBaseFactor(s.rampBase, s.rampFactor)
		}
		// Universal-light colour / effect (opaque, lossless). 0 is a
		// legitimate value, so presence is tracked separately from value.
		if s.colorTypeSeen {
			ct := s.colorType
			entry.ColorType = &ct
		}
		if s.colorValueSeen {
			cv := s.colorValue
			entry.ColorValue = &cv
		}
		if s.outputBehSeen {
			ob := s.outputBehaviour
			entry.OutputBehaviour = &ob
		}
		out = append(out, entry)
	}
	return out
}

// decodeTargetChannels expands the TARGET_CHANNELS bitmask into
// "X_Y" notation. Same encoding as
// ScheduleActorChannel IntEnum.
func decodeTargetChannels(bits int) []string {
	out := make([]string, 0, 4)
	for _, e := range scheduleActorChannelByBit {
		if bits&e.bit != 0 {
			out = append(out, e.name)
		}
	}
	return out
}

// encodeTargetChannels packs an "X_Y" list into the bitmask.
// Returns 0 (CCU default) when the list is empty.
func encodeTargetChannels(names []string) int {
	bits := 0
	for _, n := range names {
		for _, e := range scheduleActorChannelByBit {
			if e.name == n {
				bits |= e.bit
				break
			}
		}
	}
	return bits
}

// formatTimeBaseFactor renders a (base, factor) pair as a compact
// human string ("100ms", "5s", "10min", "1h"). Used for DURATION /
// RAMP_TIME wire values.
func formatTimeBaseFactor(base, factor int) string {
	if factor <= 0 || base < 0 || base >= len(timeBaseSecondsScheduleField) {
		return ""
	}
	seconds := timeBaseSecondsScheduleField[base] * float64(factor)
	switch {
	case seconds < 1:
		return fmt.Sprintf("%dms", int(math.Round(seconds*1000)))
	case seconds < 60:
		return fmt.Sprintf("%ds", int(math.Round(seconds)))
	case seconds < 3600:
		return fmt.Sprintf("%dmin", int(math.Round(seconds/60)))
	default:
		return fmt.Sprintf("%dh", int(math.Round(seconds/3600)))
	}
}

// parseTimeBaseFactor maps a human duration ("10s", "500ms", "1h")
// onto a (base, factor) pair. Picks the largest base that yields a
// Positive integer factor.
func parseTimeBaseFactor(s string) (base, factor int, ok bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, 0, false
	}
	// Order matters: "ms" before "m" so 500ms doesn't parse as 500m.
	suffixes := []struct {
		suffix  string
		seconds float64
	}{
		{"ms", 0.001},
		{"min", 60},
		{"h", 3600},
		{"s", 1},
		{"m", 60},
	}
	var seconds float64
	for _, suf := range suffixes {
		if !strings.HasSuffix(s, suf.suffix) {
			continue
		}
		numStr := strings.TrimSuffix(s, suf.suffix)
		n, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, 0, false
		}
		seconds = n * suf.seconds
		ok = true
		break
	}
	if !ok {
		// Bare number → seconds.
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			seconds = n
			ok = true
		}
	}
	if !ok || seconds <= 0 {
		return 0, 0, false
	}
	// Pick the largest base whose unit divides `seconds` into a small
	// integer factor (1..30). The CCU firmware caps DURATION_FACTOR /
	// RAMP_TIME_FACTOR at maxScheduleFactor — values above are rejected
	// by put_paramset (xml-rpc fault -5). The encoder therefore promotes
	// to a larger base instead of emitting the same seconds with a higher
	// factor.
	for i, unit := range slices.Backward(timeBaseSecondsScheduleField) {

		f := seconds / unit
		if f >= 1 && f <= maxScheduleFactor && math.Abs(f-math.Round(f)) < 1e-6 {
			return i, int(math.Round(f)), true
		}
	}
	// Sentinel pass-through: (HOUR_1, 31) is the documented "permanent"
	// pair that formatTimeBaseFactor renders as "31h". Accept it round-trip
	// so that re-saving a schedule read back from the CCU (lock auto-relock
	// actions, switch slots with the firmware default) does not fail.
	const sentinelBaseIdx = 7 // HOUR_1
	const sentinelFactor = 31
	if math.Abs(seconds-timeBaseSecondsScheduleField[sentinelBaseIdx]*sentinelFactor) < 1e-6 {
		return sentinelBaseIdx, sentinelFactor, true
	}
	return 0, 0, false
}

func weekdayBitsToNames(bits int) []string {
	out := make([]string, 0, 7)
	for _, e := range weekdayNamesByBit {
		if bits&e.bit != 0 {
			out = append(out, e.name)
		}
	}
	return out
}

func weekdayNamesToBits(names []string) int {
	bits := 0
	for _, n := range names {
		if b, ok := weekdayBits[strings.ToUpper(n)]; ok {
			bits |= b
		}
	}
	return bits
}

// serializeSimpleSchedule emits a flat paramset patch. When `domain`
// is "lock", lock_mode/lock_action/permission overwrite the entry's
// raw level/duration/target_channels with the canonical encoding —
// SPA users edit the friendly fields, the wire ends up consistent
// With.
func serializeSimpleScheduleWithDomain(
	entries []hmapi.SimpleScheduleEntry, domain string,
) (map[string]any, error) {
	if domain == "lock" {
		// Apply the lock encoding *before* serialising so the
		// downstream code only ever sees the wire shape.
		mapped := make([]hmapi.SimpleScheduleEntry, len(entries))
		for i := range entries {
			mapped[i] = applyLockEncoding(entries[i])
		}
		entries = mapped
	}
	return serializeSimpleSchedule(entries)
}

// applyLockEncoding rewrites a lock slot's level / duration / target_channels
// from the high-level lock_mode + (lock_action | permission) fields.
func applyLockEncoding(e hmapi.SimpleScheduleEntry) hmapi.SimpleScheduleEntry {
	switch e.LockMode {
	case "door_lock":
		level, durBase, durFactor, ok := lockActionRawFor(e.LockAction)
		if !ok {
			return e
		}
		e.Level = level
		e.Duration = formatTimeBaseFactor(durBase, durFactor)
		// door_lock always targets channel 1_1 unless caller
		// overrode TargetChannels explicitly.
		if len(e.TargetChannels) == 0 {
			e.TargetChannels = []string{"1_1"}
		}
	case "user_permission":
		switch e.Permission {
		case "granted":
			e.Level = 1.0
		case "not_granted":
			e.Level = 0.0
		}
		e.Duration = formatTimeBaseFactor(lockPermissionDurationBase, lockPermissionDurationFactor)
		// Permission slots target channels >= 2_x.
		if len(e.TargetChannels) == 0 {
			e.TargetChannels = []string{"2_1"}
		}
	}
	return e
}

func serializeSimpleSchedule(entries []hmapi.SimpleScheduleEntry) (map[string]any, error) { //nolint:funlen // single-purpose schedule serialization logic with many branches
	// Size hint omitted deliberately: deriving it from len(entries) (a
	// request-controlled length) risks an integer-overflowing allocation
	// size. The map grows on demand; schedules are small.
	out := make(map[string]any)
	used := make(map[int]bool, len(entries))
	for i := range entries {
		e := entries[i]
		if e.SlotNo < 1 || e.SlotNo > 24 {
			return nil, fmt.Errorf("schedules: slot_no out of range: %d", e.SlotNo)
		}
		if used[e.SlotNo] {
			return nil, fmt.Errorf("schedules: duplicate slot_no %d", e.SlotNo)
		}
		used[e.SlotNo] = true
		bits := weekdayNamesToBits(e.Weekdays)
		if bits == 0 {
			return nil, fmt.Errorf("schedules: slot %d: no weekday selected", e.SlotNo)
		}
		hh, mm, err := splitTime(e.Time)
		if err != nil {
			return nil, fmt.Errorf("schedules: slot %d: %w", e.SlotNo, err)
		}
		prefix := fmt.Sprintf("%02d_WP_", e.SlotNo)
		out[prefix+"WEEKDAY"] = bits
		out[prefix+"FIXED_HOUR"] = hh
		out[prefix+"FIXED_MINUTE"] = mm
		out[prefix+"LEVEL"] = e.Level

		// CONDITION: default to fixed_time when blank.
		condID := 0
		if e.Condition != "" {
			id, ok := scheduleConditionIDByName[e.Condition]
			if !ok {
				return nil, fmt.Errorf("schedules: slot %d: unknown condition %q", e.SlotNo, e.Condition)
			}
			condID = id
		}
		out[prefix+"CONDITION"] = condID

		// ASTRO_TYPE / ASTRO_OFFSET — only meaningful when the
		// condition involves astro events; we still write zeros for
		// the inactive case so the CCU does not retain stale data.
		var astroID int
		switch strings.ToLower(e.AstroType) {
		case "sunrise", "":
			astroID = 0
		case "sunset":
			astroID = 1
		default:
			return nil, fmt.Errorf("schedules: slot %d: unknown astro_type %q", e.SlotNo, e.AstroType)
		}
		out[prefix+"ASTRO_TYPE"] = astroID
		offset := e.AstroOffsetMinutes
		if offset < -720 || offset > 720 {
			return nil, fmt.Errorf("schedules: slot %d: astro_offset_minutes out of range", e.SlotNo)
		}
		out[prefix+"ASTRO_OFFSET"] = offset

		// TARGET_CHANNELS bitmask. Empty list keeps the CCU default.
		out[prefix+"TARGET_CHANNELS"] = encodeTargetChannels(e.TargetChannels)

		// LEVEL_2 (cover slat) — write only when the caller supplied
		// an explicit value; otherwise the CCU keeps its stored one.
		if e.Level2 != nil {
			out[prefix+"LEVEL_2"] = *e.Level2
		}

		// DURATION (base + factor) — only emit when the caller actually
		// set a duration. Writing DURATION_BASE/FACTOR=0 to a CCU switch
		// channel triggers xml-rpc fault -5 because (0, 0) is below the
		// param description's MIN — the field is left untouched so the
		// CCU keeps the existing wire value.
		if e.Duration != "" {
			b, f, ok := parseTimeBaseFactor(e.Duration)
			if !ok {
				return nil, fmt.Errorf("schedules: slot %d: invalid duration %q", e.SlotNo, e.Duration)
			}
			out[prefix+"DURATION_BASE"] = b
			out[prefix+"DURATION_FACTOR"] = f
		}

		// RAMP_TIME (base + factor) — same emit-only-when-set rule as
		// DURATION above. Switch channels often have no RAMP_TIME param
		// at all and reject any write to it.
		if e.RampTime != "" {
			b, f, ok := parseTimeBaseFactor(e.RampTime)
			if !ok {
				return nil, fmt.Errorf("schedules: slot %d: invalid ramp_time %q", e.SlotNo, e.RampTime)
			}
			out[prefix+"RAMP_TIME_BASE"] = b
			out[prefix+"RAMP_TIME_FACTOR"] = f
		}

		// Universal-light colour / effect (opaque). Emit only when the
		// caller carried a value (nil = leave the CCU's stored value
		// untouched via the sparse merge). Writing the read-back value
		// re-glues the colour to this entry's current slot, so it survives
		// reorder / insert / delete deterministically. 0 is legitimate and
		// is written when present.
		if e.ColorType != nil {
			out[prefix+"HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"] = *e.ColorType
		}
		if e.ColorValue != nil {
			out[prefix+"HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE"] = *e.ColorValue
		}
		if e.OutputBehaviour != nil {
			out[prefix+"OUTPUT_BEHAVIOUR"] = *e.OutputBehaviour
		}
	}
	// Deactivate every unused slot (1..24) so deleted entries vanish on the CCU.
	for n := 1; n <= 24; n++ {
		if used[n] {
			continue
		}
		out[fmt.Sprintf("%02d_WP_WEEKDAY", n)] = 0
		out[fmt.Sprintf("%02d_WP_TARGET_CHANNELS", n)] = 0
	}
	return out, nil
}

func splitTime(hhmm string) (hour, minute int, err error) {
	if len(hhmm) < 4 || len(hhmm) > 5 {
		return 0, 0, fmt.Errorf("invalid time %q", hhmm)
	}
	before, after, ok := strings.Cut(hhmm, ":")
	if !ok {
		return 0, 0, fmt.Errorf("invalid time %q", hhmm)
	}
	h, err := strconv.Atoi(before)
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", hhmm)
	}
	m, err := strconv.Atoi(after)
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", hhmm)
	}
	return h, m, nil
}

// isCCUScheduleFalsePositive reports whether err is the documented
// CCU-side false-positive on putParamset against schedule channels:
// the firmware returns XMLRPC fault -5 ("Invalid parameter or value")
// even when the write actually landed. Verified against ReGaHss
// 3.87.6.20260509 on HmIP-PSM SWITCH_WEEK_PROFILE channels by reading
// the changed field back immediately after the fault.
func isCCUScheduleFalsePositive(err error) bool {
	if err == nil {
		return false
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		return false
	}
	return fault.FaultCode() == hmerr.XMLRPCFaultInvalidParameter
}

// PutClimateSchedule serialises the schedule (climate or simple,
// driven by the `kind` field on the payload) and writes it as a
// MASTER paramset patch. For climate schedules every weekday is
// expanded to 13 slots; for simple schedules unused slots (1..24)
// are zeroed out so deletions take effect.
func (s *SchedulesDomain) PutClimateSchedule(
	ctx context.Context, deviceAddress string, channelNo int, sched *hmapi.ClimateSchedule,
) error {
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return err
	}
	if sched == nil {
		return errors.New("schedules: nil payload")
	}
	var raw map[string]any
	// The MASTER paramset description drives two decisions below:
	// which schedule fields the device advertises (unsupported keys are
	// filtered out), and whether a climate device uses the bare
	// (prefix-less) schema.
	descKeys := scheduleDescKeys(ctx, backend, channelAddr)
	switch sched.Kind {
	case "simple":
		raw, err = serializeSimpleScheduleWithDomain(sched.SimpleEntries, sched.Domain)
		if err == nil && len(raw) > 0 && len(descKeys) > 0 {
			// Filter out schedule fields the device does not advertise in its
			// MASTER paramset description. Devices like HmIP-DLD expose only a
			// subset of WP_* keys; sending unsupported fields causes the CCU to
			// silently reject the write and leave CONFIG_PENDING set.
			supported := weekprofile.ExtractSupportedScheduleFields(descKeys)
			raw = weekprofile.FilterRawScheduleByFields(raw, supported)
		}
	case "climate", "":
		// A bare-schema thermostat (HM-CC-RT-DN) must receive prefix-less
		// keys — sending the P<n>_ form silently no-ops on the CCU, and
		// the field filter below does not catch it (ExtractSupportedScheduleFields
		// only recognises WP_ keys), so this branch is load-bearing.
		if climateScheduleIsBare(descKeys) {
			raw, err = serializeClimateScheduleBare(sched)
		} else {
			raw, err = serializeClimateSchedule(sched)
		}
		if err == nil && len(raw) > 0 && len(descKeys) > 0 {
			// Filter out climate-schedule fields the device does not expose in
			// its MASTER paramset description. Devices differ in which
			// ENDTIME_*/TEMPERATURE_* keys they support; sending unsupported
			// keys causes the CCU to reject the write and leave CONFIG_PENDING
			// set.
			supported := weekprofile.ExtractSupportedScheduleFields(descKeys)
			raw = weekprofile.FilterRawScheduleByFields(raw, supported)
		}
	default:
		return fmt.Errorf("schedules: unknown kind %q", sched.Kind)
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("schedules: empty payload")
	}
	if err := backend.PutParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster, raw, hmenum.CommandRxModeUnset); err != nil {
		if !isCCUScheduleFalsePositive(err) {
			return err
		}
		// CCU bug: putParamset on SWITCH_WEEK_PROFILE / climate channels
		// returns fault -5 ("Invalid parameter or value") even when the
		// underlying write succeeded — verified empirically by reading
		// the changed field back via getParamset. We surface the fault
		// as a warning so the operator can spot CCU-side weirdness, and
		// report success to the SPA caller because the wire effect
		// actually landed.
		slog.WarnContext(
			ctx, "schedules.put_paramset.ccu_false_positive_fault",
			slog.String("device", deviceAddress),
			slog.Int("channel", channelNo),
			slog.String("kind", sched.Kind),
			slog.String("err", err.Error()),
		)
	}
	s.audit.Record(audit.Entry{
		Action:        audit.ActionScheduleWrite,
		DeviceAddress: deviceAddress,
		ChannelNo:     channelNo,
		Note:          sched.Kind,
	})
	return nil
}

// SetActiveProfile writes the ACTIVE_PROFILE data point. Most
// thermostats expose it on the climate channel itself; we find the
// writable channel by asking the backend for the paramset
// description.
func (s *SchedulesDomain) SetActiveProfile(
	ctx context.Context, deviceAddress string, channelNo int, profile string,
) error {
	if !isValidProfileID(profile) {
		return fmt.Errorf("%w: %q is not a valid profile key (P1..P6)", ErrInvalidProfileID, profile)
	}
	// Per-device cap check: P4 on a 3-profile device is rejected.
	maxProfiles, err := s.MaxProfilesForDevice(ctx, deviceAddress)
	if err != nil {
		return err
	}
	if !isProfileIDWithinCap(profile, maxProfiles) {
		return fmt.Errorf("%w: %q exceeds device cap P1..P%d for %s",
			ErrInvalidProfileID, profile, maxProfiles, deviceAddress)
	}
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return err
	}
	// Profile index is stored as 1..6 in ACTIVE_PROFILE.
	idx, _ := strconv.Atoi(strings.TrimPrefix(profile, "P"))
	if err := backend.PutParamset(ctx, channelAddr, hmenum.ParamsetKeyValues, map[string]any{
		"ACTIVE_PROFILE": idx,
	}, hmenum.CommandRxModeUnset); err != nil {
		return err
	}
	s.audit.Record(audit.Entry{
		Action:        audit.ActionActiveProfile,
		DeviceAddress: deviceAddress,
		ChannelNo:     channelNo,
		Note:          profile,
	})
	return nil
}

// readActiveProfile is best-effort: a missing ACTIVE_PROFILE DP is
// silently swallowed because non-HmIP thermostats do not expose it.
func (s *SchedulesDomain) readActiveProfile(
	ctx context.Context, backend paramsetBackend, channelAddr string,
) (string, bool) {
	values, err := backend.GetParamset(ctx, channelAddr, hmenum.ParamsetKeyValues)
	if err != nil {
		return "", false
	}
	raw, ok := values["ACTIVE_PROFILE"]
	if !ok {
		return "", false
	}
	idx, ok := coerceInt(raw)
	if !ok || idx < 1 || idx > 6 {
		return "", false
	}
	return fmt.Sprintf("P%d", idx), true
}

// resolve locates the backend and returns the fully-qualified channel
// address. Mirrors ParamsetsDomain.resolve but scoped to a channel
// number so the caller doesn't have to synthesise "addr:no" itself.
func (s *SchedulesDomain) resolve(
	deviceAddress string, channelNo int,
) (paramsetBackend, string, error) {
	if s.registry == nil || s.writer == nil {
		return nil, "", ErrNoScheduleBackend
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		b, ok := s.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return nil, "", fmt.Errorf("%w: %s/%s", ErrNoScheduleBackend, u.Name(), dev.InterfaceID)
		}
		return b, scheduleChannelAddress(deviceAddress, channelNo), nil
	}
	// Device exists in no central — see FindScheduleChannel for the
	// rationale; mapped to 404 by the REST handler.
	return nil, "", fmt.Errorf("%w: device %s", hmerr.ErrDescriptionNotFound, deviceAddress)
}

// scheduleChannelAddress builds the paramset address for a schedule
// channel. The synthetic device-root channel ([device.ChannelNumberDevice])
// addresses the bare device-level MASTER paramset used by classic
// BidCos thermostats (HM-CC-RT-DN); every other channel gets the usual
// "<device>:<channel>" form.
func scheduleChannelAddress(deviceAddress string, channelNo int) string {
	if channelNo == device.ChannelNumberDevice {
		return deviceAddress
	}
	return fmt.Sprintf("%s:%d", deviceAddress, channelNo)
}

// --- Parsing ------------------------------------------------------

// slotKey addresses one ClimatePeriod cell in the CCU's flat
// paramset. File-scoped so simplifyWeekday can accept it as input.
type slotKey struct {
	profile int
	weekday string
	slot    int
}

// slotVals aggregates the two wire values for a single schedule
// slot (ENDTIME in minutes since midnight, TEMPERATURE in Celsius).
// "has*" flags distinguish "slot not present" from "slot == 0".
type slotVals struct {
	endtime     int
	temperature float64
	hasEnd      bool
	hasTemp     bool
}

// parseClimateSchedule turns the flat CCU paramset into the
// structured DTO. Unknown keys are ignored. Raises ErrNoSchedule when
// no P<n>_ENDTIME/TEMPERATURE key is present so the caller can surface
// "no schedule support" as an HTTP 404.
func parseClimateSchedule(raw map[string]any) (*hmapi.ClimateSchedule, error) {
	collected := make(map[slotKey]*slotVals)
	for name, v := range raw {
		m := slotPattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		// A bare (prefix-less) key has an empty profile group and is
		// treated as the single profile P1.
		profile := 1
		if m[1] != "" {
			profile, _ = strconv.Atoi(m[1])
		}
		slot, _ := strconv.Atoi(m[4])
		k := slotKey{profile: profile, weekday: m[3], slot: slot}
		sv := collected[k]
		if sv == nil {
			sv = &slotVals{}
			collected[k] = sv
		}
		switch m[2] {
		case "ENDTIME":
			if i, ok := coerceInt(v); ok {
				sv.endtime = i
				sv.hasEnd = true
			}
		case "TEMPERATURE":
			if f, ok := coerceFloat(v); ok {
				sv.temperature = f
				sv.hasTemp = true
			}
		}
	}
	if len(collected) == 0 {
		return nil, ErrNoSchedule
	}

	// Gruppieren nach (profile, weekday), dann vereinfachen.
	type weekdayIn struct {
		slots map[int]*slotVals
	}
	type profileIn struct {
		days map[string]*weekdayIn
	}
	perProfile := make(map[int]*profileIn)
	for k, sv := range collected {
		prof := perProfile[k.profile]
		if prof == nil {
			prof = &profileIn{days: make(map[string]*weekdayIn)}
			perProfile[k.profile] = prof
		}
		wd := prof.days[k.weekday]
		if wd == nil {
			wd = &weekdayIn{slots: make(map[int]*slotVals)}
			prof.days[k.weekday] = wd
		}
		wd.slots[k.slot] = sv
	}

	out := &hmapi.ClimateSchedule{
		Profiles: make(map[string]hmapi.ClimateProfile),
	}
	for pid, prof := range perProfile {
		dto := hmapi.ClimateProfile{
			Weekdays: make(map[string]hmapi.ClimateWeekday),
		}
		for _, wd := range scheduleWeekdays {
			data := prof.days[wd]
			if data == nil {
				continue
			}
			dto.Weekdays[wd] = simplifyWeekday(data.slots)
		}
		out.Profiles[fmt.Sprintf("P%d", pid)] = dto
	}
	return out, nil
}

// simplifyWeekday compresses the 13-slot representation into a
// base-temperature + explicit-periods form (the "simple" schedule
// shape). The base temperature is picked as the most frequent
// temperature across the day, weighted by slot duration — same
// Heuristic
func simplifyWeekday(slots map[int]*slotVals) hmapi.ClimateWeekday {
	// Sortierte Slot-Nummern.
	nums := make([]int, 0, len(slots))
	for n := range slots {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	// Weight-by-duration, um die "base temperature" zu finden.
	prevEnd := 0
	weight := make(map[float64]int)
	type flat struct {
		startMin int
		endMin   int
		temp     float64
	}
	flatSlots := make([]flat, 0, len(nums))
	for _, n := range nums {
		sv := slots[n]
		if !sv.hasEnd || !sv.hasTemp {
			continue
		}
		if sv.endtime <= prevEnd {
			continue
		}
		duration := sv.endtime - prevEnd
		// Round temperature for grouping (0.5 °C grid is typical).
		rounded := math.Round(sv.temperature*2) / 2
		weight[rounded] += duration
		flatSlots = append(flatSlots, flat{
			startMin: prevEnd,
			endMin:   sv.endtime,
			temp:     sv.temperature,
		})
		prevEnd = sv.endtime
	}

	base := 0.0
	bestWeight := -1
	// Tie-break: niedrigere Temperatur gewinnt (klassische Heizungs-
	// night temperature), so that "setback" becomes the base and the
	// explicitly named periods are the heating phases.
	keys := make([]float64, 0, len(weight))
	for k := range weight {
		keys = append(keys, k)
	}
	sort.Float64s(keys)
	for _, k := range keys {
		if weight[k] > bestWeight {
			bestWeight = weight[k]
			base = k
		}
	}

	// Periods are all slot ranges whose temperature is NOT the base,
	// merged into contiguous blocks.
	periods := make([]hmapi.ClimatePeriod, 0)
	for i := 0; i < len(flatSlots); {
		if math.Abs(flatSlots[i].temp-base) < 1e-6 {
			i++
			continue
		}
		start := flatSlots[i].startMin
		end := flatSlots[i].endMin
		temp := flatSlots[i].temp
		j := i + 1
		for j < len(flatSlots) && math.Abs(flatSlots[j].temp-temp) < 1e-6 && flatSlots[j].startMin == end {
			end = flatSlots[j].endMin
			j++
		}
		periods = append(periods, hmapi.ClimatePeriod{
			StartTime:   formatMinutes(start),
			EndTime:     formatMinutes(end),
			Temperature: temp,
		})
		i = j
	}

	return hmapi.ClimateWeekday{
		BaseTemperature: base,
		Periods:         periods,
	}
}

// --- Serialisation ------------------------------------------------

// serializeClimateSchedule converts the structured DTO back into the
// flat CCU paramset shape. Every weekday is expanded to exactly 13
// slots; unused slots end at 24:00 with the base temperature.
// Non-overlapping period validation is applied; the call fails
// rather than silently dropping conflicting periods.
func serializeClimateSchedule(sched *hmapi.ClimateSchedule) (map[string]any, error) {
	// Size hint omitted deliberately: deriving it from len(sched.Profiles)
	// (a request-controlled length) risks an integer-overflowing
	// allocation size. The map grows on demand.
	out := make(map[string]any)
	for profileID, profile := range sched.Profiles {
		if !isValidProfileID(profileID) {
			return nil, fmt.Errorf("schedules: invalid profile id %q", profileID)
		}
		for weekday, wd := range profile.Weekdays {
			if !isValidWeekdayName(weekday) {
				return nil, fmt.Errorf("schedules: invalid weekday %q", weekday)
			}
			slots, err := expandWeekday(wd)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", profileID, weekday, err)
			}
			for n, slot := range slots {
				endKey := fmt.Sprintf("%s_ENDTIME_%s_%d", profileID, weekday, n+1)
				tempKey := fmt.Sprintf("%s_TEMPERATURE_%s_%d", profileID, weekday, n+1)
				out[endKey] = slot.endMin
				out[tempKey] = slot.temp
			}
		}
	}
	return out, nil
}

// serializeClimateScheduleBare converts the DTO into the bare
// (prefix-less) CCU paramset shape used by classic BidCos thermostats
// (HM-CC-RT-DN, HM-CC-RT-DN-BoM). These devices expose exactly one
// week profile; a non-P1 profile in the payload is rejected rather
// than silently dropped so callers get a clear error instead of a
// no-op write.
func serializeClimateScheduleBare(sched *hmapi.ClimateSchedule) (map[string]any, error) {
	out := make(map[string]any)
	for profileID, profile := range sched.Profiles {
		if profileID != "P1" {
			return nil, fmt.Errorf(
				"schedules: device exposes a single profile; cannot write %q", profileID,
			)
		}
		for weekday, wd := range profile.Weekdays {
			if !isValidWeekdayName(weekday) {
				return nil, fmt.Errorf("schedules: invalid weekday %q", weekday)
			}
			slots, err := expandWeekday(wd)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", profileID, weekday, err)
			}
			for n, slot := range slots {
				out[fmt.Sprintf("ENDTIME_%s_%d", weekday, n+1)] = slot.endMin
				out[fmt.Sprintf("TEMPERATURE_%s_%d", weekday, n+1)] = slot.temp
			}
		}
	}
	return out, nil
}

// scheduleDescKeys reads the MASTER paramset description of a channel
// and returns its key set. Returns nil on error so callers treat an
// unavailable description as "no filtering and prefixed schema".
func scheduleDescKeys(
	ctx context.Context, backend paramsetBackend, channelAddr string,
) map[string]struct{} {
	desc, err := backend.GetParamsetDescription(ctx, channelAddr, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil
	}
	keys := make(map[string]struct{}, len(desc))
	for k := range desc {
		keys[k] = struct{}{}
	}
	return keys
}

// climateScheduleIsBare reports whether a device carries its climate
// schedule as bare ENDTIME_/TEMPERATURE_ keys in the MASTER paramset
// (classic BidCos HM-CC-RT-DN) rather than the prefixed P<n>_ form.
// True iff at least one bare schedule key exists and no prefixed
// P<n>_ key does; a nil/empty key set (unreadable description) defaults
// to the prefixed schema.
func climateScheduleIsBare(descKeys map[string]struct{}) bool {
	hasBare := false
	for k := range descKeys {
		m := slotPattern.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		if m[1] != "" {
			return false // a prefixed P<n>_ key exists — not a bare-schema device
		}
		hasBare = true
	}
	return hasBare
}

type rawSlot struct {
	endMin int
	temp   float64
}

// expandWeekday fills 13 slots from base temperature + periods. The
// simple form may have gaps (times where no period is defined); those
// default to the base temperature. Overlapping periods abort.
func expandWeekday(wd hmapi.ClimateWeekday) ([]rawSlot, error) {
	// Sortierte Perioden prüfen.
	periods := append([]hmapi.ClimatePeriod(nil), wd.Periods...)
	sort.Slice(periods, func(i, j int) bool {
		return minutesFromTime(periods[i].StartTime) < minutesFromTime(periods[j].StartTime)
	})
	for i := range periods {
		start := minutesFromTime(periods[i].StartTime)
		end := minutesFromTime(periods[i].EndTime)
		if start < 0 || end < 0 {
			return nil, fmt.Errorf("period[%d]: invalid HH:MM", i)
		}
		if end <= start {
			return nil, fmt.Errorf("period[%d]: endtime must be after starttime", i)
		}
		if i > 0 && start < minutesFromTime(periods[i-1].EndTime) {
			return nil, fmt.Errorf("period[%d]: overlaps previous", i)
		}
	}

	// Walk through the day and collect (endMin, temp) stretches, then
	// pad to exactly 13 slots by repeating the last (24:00, base)
	// entry.
	type stretch struct {
		end  int
		temp float64
	}
	stretches := make([]stretch, 0, 14)
	cursor := 0
	for _, p := range periods {
		pStart := minutesFromTime(p.StartTime)
		pEnd := minutesFromTime(p.EndTime)
		if pStart > cursor {
			stretches = append(stretches, stretch{end: pStart, temp: wd.BaseTemperature})
		}
		stretches = append(stretches, stretch{end: pEnd, temp: p.Temperature})
		cursor = pEnd
	}
	if cursor < 1440 {
		stretches = append(stretches, stretch{end: 1440, temp: wd.BaseTemperature})
	}
	if len(stretches) == 0 {
		stretches = append(stretches, stretch{end: 1440, temp: wd.BaseTemperature})
	}
	if len(stretches) > 13 {
		return nil, fmt.Errorf("too many periods: yielded %d slots, max 13", len(stretches))
	}
	// Pad mit (24:00, base) bis zu 13 Slots.
	out := make([]rawSlot, 13)
	for i := range 13 {
		if i < len(stretches) {
			out[i] = rawSlot{endMin: stretches[i].end, temp: stretches[i].temp}
		} else {
			out[i] = rawSlot{endMin: 1440, temp: wd.BaseTemperature}
		}
	}
	return out, nil
}

// --- helpers ------------------------------------------------------

func isValidProfileID(s string) bool {
	if len(s) != 2 || s[0] != 'P' {
		return false
	}
	n := int(s[1] - '0')
	return n >= 1 && n <= 6
}

func isValidWeekdayName(s string) bool {
	return slices.Contains(scheduleWeekdays, s)
}

func coerceInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case float32:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n, true
		}
	}
	return 0, false
}

func coerceFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func formatMinutes(m int) string {
	if m < 0 {
		m = 0
	}
	if m > 24*60 {
		m = 24 * 60
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func minutesFromTime(s string) int {
	if s == "24:00" {
		return 1440
	}
	before, after, ok := strings.Cut(s, ":")
	if !ok {
		return -1
	}
	h, err := strconv.Atoi(before)
	if err != nil {
		return -1
	}
	m, err := strconv.Atoi(after)
	if err != nil {
		return -1
	}
	if h < 0 || h > 24 || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}
