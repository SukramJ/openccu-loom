// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
//
// The sentinel itself is [hmerr.ErrNoSchedule] so the REST layer can
// classify it with errors.Is; see the doc there for why it lives in
// pkg/hmerr rather than here.
var ErrNoSchedule = hmerr.ErrNoSchedule

// scheduleWeekdays is the CCU paramset spelling of the weekday set, in
// the domain's Monday-first order. It is derived, not restated: the CCU
// key spells the day in full uppercase English, and which seven words
// those are is a fact of the schedule domain, not of this adapter.
var scheduleWeekdays = weekdayNames()

func weekdayNames() []string {
	out := make([]string, 0, len(schedule.Weekdays))
	for _, w := range schedule.Weekdays {
		out = append(out, string(w))
	}
	return out
}

// slotPattern matches a single schedule-paramset key. Capture groups:
//  1. Profile index (1..6) — EMPTY for the prefix-less schema
//  2. Field (ENDTIME or TEMPERATURE)
//  3. Weekday name
//  4. Slot number — the ordinal is matched open-ended here and
//     bounded by [schedule.MaxClimateSlots] in [parseClimateSchedule]
//
// The P<n>_ prefix is optional: classic BidCos thermostats
// (HM-CC-RT-DN, HM-CC-RT-DN-BoM) carry a single week profile as bare
// ENDTIME_<DAY>_<N> / TEMPERATURE_<DAY>_<N> keys in the device-level
// MASTER paramset, with no profile prefix and no dedicated channel.
// A bare key is treated as profile P1 throughout. The weekday
// alternation is pinned — it is built from [scheduleWeekdays] rather
// than a wildcard — so bare device-master keys like TEMPERATURE_OFFSET
// never match.
var slotPattern = regexp.MustCompile(
	`^(?:P([1-6])_)?(ENDTIME|TEMPERATURE)_(` +
		strings.Join(weekdayNames(), "|") +
		`)_([0-9]+)$`,
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
// WEEK_PROFILE. The types real devices carry are enumerated in
// [weekProfileDomains]; the pattern stays open-ended so a firmware that
// adds one is still recognised as a schedule channel.
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
		backend, ok := s.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
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
// one `<NN>_WP_<FIELD>` key — the shape used by switch / cover / light
// week profiles.
//
// The grammar comes from [weekprofile.SimpleGroupNo], the same one the
// parser applies, so a channel is reported as carrying a schedule
// exactly when the parser would read cells from it.
func hasSimpleScheduleParams(raw map[string]any) bool {
	for k := range raw {
		if _, ok := weekprofile.SimpleGroupNo(k); ok {
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
) ([]hmapi.ClimateTimeCorrection, error) {
	channelNo, err := s.FindScheduleChannel(ctx, deviceAddress)
	if err != nil {
		return nil, err
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
// always a caller mistake. Aliases [hmerr.ErrScheduleCopyNoOp] so the
// REST layer can classify it with errors.Is.
var ErrScheduleCopyNoOp = hmerr.ErrScheduleCopyNoOp

// ErrScheduleCopyProfileRange is returned when a profile index is
// outside the supported 1..6 range. Aliases
// [hmerr.ErrScheduleCopyProfileRange] for the same reason as
// [ErrScheduleCopyNoOp].
var ErrScheduleCopyProfileRange = hmerr.ErrScheduleCopyProfileRange

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
	if !weekprofile.ValidProfileIndex(srcProfile) || !weekprofile.ValidProfileIndex(dstProfile) {
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
	if _, err := s.PutClimateSchedule(ctx, dstDevice, dstChannelNo, dst); err != nil {
		return fmt.Errorf("schedules: copy_profile write destination: %w", err)
	}
	return nil
}

// ListScheduleDevices returns every device that carries a week schedule,
// across every central, sorted by central then address.
//
// Type-derived on purpose. FindScheduleChannel resolves the same question
// exactly for one device and pays a MASTER read for the climate case; run
// that over a fleet and opening an overview would cost one CCU round-trip
// per thermostat. The channel types are a reliable proxy — a
// CLIMATECONTROL_RT_TRANSCEIVER without a week profile is a device that
// does not exist — and the detail view resolves the truth on click.
func (s *SchedulesDomain) ListScheduleDevices(_ context.Context) ([]hmapi.ScheduleDeviceSummary, error) {
	if s.registry == nil {
		return nil, ErrNoScheduleBackend
	}
	var out []hmapi.ScheduleDeviceSummary
	for _, u := range s.registry.List() {
		for _, dev := range u.ModelRegistry.List() {
			ch, kind, ok := scheduleChannelByType(dev)
			if !ok {
				continue
			}
			out = append(out, hmapi.ScheduleDeviceSummary{
				Central: u.Name(),
				Address: dev.Address,
				Name:    dev.Name(),
				Model:   dev.Model,
				Channel: hmapi.ScheduleChannelRef{
					Address: ch.Address,
					Number:  ch.Number,
					Device:  dev.Address,
				},
				Kind: kind,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Central != out[j].Central {
			return out[i].Central < out[j].Central
		}
		return out[i].Address < out[j].Address
	})
	return out, nil
}

// scheduleChannelByType picks the schedule-carrying channel from the
// device's channel types, mirroring the three paths of
// FindScheduleChannel minus its live MASTER probe on the climate
// candidate channels — the device-root check below substitutes an
// in-memory equivalent so the fleet-wide summary stays a cheap,
// no-CCU-round-trip listing. A dedicated WEEK_PROFILE channel wins, as
// there.
func scheduleChannelByType(dev *device.Device) (*device.Channel, string, bool) {
	for _, ch := range dev.Channels() {
		if isWeekProfileChannel(ch.Type) {
			return ch, "week_profile", true
		}
	}
	// Classic bare-schema thermostats (HM-CC-RT-DN, HM-CC-RT-DN-BoM,
	// HM-CC-VG-1) and devices with no dedicated climate channel at all
	// (HM-TC-IT-WM-W-EU) carry their week profile on the device-root
	// MASTER paramset, never on a CLIMATECONTROL_* channel — even when
	// one of those channel types is also present. hydrateDeviceRoot only
	// materialises the root channel when the device genuinely has
	// device-level MASTER content, and rootChannelCarriesSchedule checks
	// the already-materialised parameter names against the same slot
	// pattern FindScheduleChannel's live probe would match, so this stays
	// a cheap in-memory check. Checked before the CLIMATECONTROL_* match
	// below so it wins over pointing at a channel that carries no
	// schedule for exactly these models.
	if root := dev.RootChannel(); rootChannelCarriesSchedule(root) {
		return root, "climate", true
	}
	for _, ch := range dev.Channels() {
		if _, isClimate := climateScheduleChannelTypes[ch.Type]; isClimate {
			return ch, "climate", true
		}
	}
	return nil, "", false
}

// rootChannelCarriesSchedule reports whether root's already-materialised
// MASTER data points include at least one week-profile slot key
// (P<n>_ENDTIME_*/TEMPERATURE_* or the bare ENDTIME_/TEMPERATURE_ form).
// nil-safe: a device with no device-root channel returns false.
func rootChannelCarriesSchedule(root *device.Channel) bool {
	if root == nil {
		return false
	}
	for _, dp := range root.MasterDataPoints() {
		if slotPattern.MatchString(dp.DataPointKey().Parameter) {
			return true
		}
	}
	return false
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
	if _, err := s.PutClimateScheduleAuto(ctx, dstDeviceAddress, src); err != nil {
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
		dto, err := parseClimateSchedule(ctx, values)
		if err != nil {
			return nil, err
		}
		dto.Kind = "climate"
		dto.Channel = chRef
		// ACTIVE_PROFILE lives as a VALUES data point. Best-effort.
		if active, idx, ok := s.readActiveProfile(ctx, backend, channelAddr); ok {
			dto.ActiveProfile = active
			dto.ActiveProfileIndex = &idx
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
		return resolveScheduleDomain(dev, scheduleChannelNo)
	}
	return ""
}

// resolveScheduleDomain classifies a device's schedule into one of the
// user-facing domain buckets. It is the single resolution both the REST read
// path ([SchedulesDomain.detectScheduleDomain]) and the MQTT publisher share so
// the two surfaces never disagree.
//
// A lock actor channel (type starting with DOOR_LOCK or equal to KEYMATIC) wins
// over the week-profile channel type. Door locks (HmIP-DLD carries
// DOOR_LOCK_STATE_TRANSMITTER, HmIP-DLP DOOR_LOCK_TRANSCEIVER) expose their
// schedule on a generic SWITCH_WEEK_PROFILE channel, which would otherwise
// resolve to "switch" — the SPA would then render an on/off switch instead of
// the lock action picker and a save would corrupt the slot. DOOR_LOCK_* and
// KEYMATIC channels only ever appear on locks, so this precedence cannot
// misclassify a real switch/cover/light/climate device.
func resolveScheduleDomain(dev *device.Device, scheduleChannelNo int) string {
	// The lock actor is the strongest signal — take it first.
	for _, ch := range dev.Channels() {
		if domainFromActorType(ch.Type) == "lock" {
			return "lock"
		}
	}
	// The schedule channel itself carries a type like SWITCH_WEEK_PROFILE —
	// the prefix is the next strongest hint.
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

// weekProfileDomains maps each "<X>_WEEK_PROFILE" channel type onto the
// user-facing domain bucket. Keyed by the exact type, not by a prefix:
// the real names do not share a prefix with the domain they belong to.
// UNIVERSAL_LIGHT_WEEK_PROFILE (HmIP-RGBW / LSC / DRG-DALI) and
// SHADING_WEEK_PROFILE (HmIP-HDM1 / HDM2) start with neither DIMMER_
// nor BLIND_, so a prefix rule set leaves them unclassified and the
// resolution falls through to the actor-channel scan — which answers
// with whichever actor channel sorts first and therefore hands two
// devices carrying the same schedule channel type two different
// domains. An unresolved domain reaches the operator as a schedule
// editor without the brightness slider, without the ramp-time field
// and without the slat control, and as an MQTT `schedule_domain` of
// "switch" on a device that is not one.
//
// SERVO_WEEK_PROFILE carries no device in the simulated fleet; it is
// declared by the CCU WebUI's channel-description table and drives a
// percentage level actor (SERVO_TRANSMITTER.LEVEL + RAMP_TIME), which
// puts it in the same bucket as the other non-light level actors.
var weekProfileDomains = map[string]string{
	"SWITCH_WEEK_PROFILE":                  "switch",
	"DIMMER_WEEK_PROFILE":                  "light",
	"DIMMER_OUTPUT_BEHAVIOUR_WEEK_PROFILE": "light",
	"UNIVERSAL_LIGHT_WEEK_PROFILE":         "light",
	"BLIND_WEEK_PROFILE":                   "cover",
	"SHADING_WEEK_PROFILE":                 "cover",
	"WATER_SWITCH_WEEK_PROFILE":            "valve",
	"SERVO_WEEK_PROFILE":                   "valve",
}

// domainFromWeekProfileType maps a "<X>_WEEK_PROFILE" channel type to
// the user-facing domain bucket. Returns "" for a type no shipped
// firmware declares, leaving the decision to the actor-type scan.
func domainFromWeekProfileType(channelType string) string {
	return weekProfileDomains[channelType]
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

// detectLockAction reverses the wire encoding to its canonical label.
// Falls back to "lock_autorelock_start" (the zero-value encoding) when nothing matches.
func detectLockAction(level float64, durBase, durFactor int) string {
	return string(schedule.DetectLockAction(level, durBase, durFactor))
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
		mode := schedule.DetectLockMode(e.TargetChannels)
		e.LockMode = string(mode)
		dBase, dFactor := lookupSlotDuration(raw, e.SlotNo)
		switch mode {
		case schedule.LockModeDoorLock:
			e.LockAction = detectLockAction(e.Level, dBase, dFactor)
		case schedule.LockModeUserPermission:
			e.Permission = string(schedule.DetectLockPermission(e.Level))
		}
	}
	return entries
}

// lookupSlotDuration reads DURATION_BASE/FACTOR for the named slot
// directly from the raw paramset. Called by the lock branch after
// stripUnsupportedFields cleared the Duration string on the entry.
// Slot keys follow the `<NN>_WP_<FIELD>` grammar; the slot
// number is zero-padded to two digits in the wire shape.
func lookupSlotDuration(raw map[string]any, slotNo int) (durationBase, durationFactor int) {
	prefix := fmt.Sprintf("%02d_WP_", slotNo)
	dBase, _ := coerceInt(raw[prefix+"DURATION_BASE"])
	dFactor, _ := coerceInt(raw[prefix+"DURATION_FACTOR"])
	return dBase, dFactor
}

// parseSimpleSchedule decodes the `<NN>_WP_<FIELD>` MASTER paramset into
// the REST/WS DTO shape, ascending by slot.
//
// The wire translation itself lives in
// [weekprofile.ParseSimpleRawParamset]; this is the projection of its
// result onto [hmapi.SimpleScheduleEntry]. The two used to be separate
// implementations of the same format, which meant every defect in it had
// to be found twice — Sunday's bit, the group limit and six of the eight
// condition names each cost a release that way.
func parseSimpleSchedule(raw map[string]any) []hmapi.SimpleScheduleEntry {
	s, err := weekprofile.ParseSimpleRawParamset(raw)
	if err != nil {
		// The parser reports the CCU's data as it finds it and has no
		// failure mode of its own; an error here means the paramset was
		// unreadable, which reads to the caller as "no schedule".
		return nil
	}
	out := make([]hmapi.SimpleScheduleEntry, 0, len(s.Entries))
	for _, slotNo := range s.Slots() {
		e := s.Entries[slotNo]
		weekdays := make([]string, 0, len(e.Weekdays))
		for _, d := range e.Weekdays {
			weekdays = append(weekdays, string(d))
		}
		out = append(out, hmapi.SimpleScheduleEntry{
			SlotNo:             slotNo,
			Weekdays:           weekdays,
			Time:               e.Time,
			Condition:          string(e.Condition),
			AstroType:          string(e.AstroType),
			AstroOffsetMinutes: e.AstroOffsetMinutes,
			TargetChannels:     e.TargetChannels,
			Level:              e.Level,
			Level2:             e.Level2,
			Duration:           e.Duration,
			RampTime:           e.RampTime,
			ColorType:          e.ColorType,
			ColorValue:         e.ColorValue,
			OutputBehaviour:    e.OutputBehaviour,
		})
	}
	return out
}

// simpleScheduleToDomain maps the REST/WS DTO list onto the domain model
// the wire encoder consumes.
//
// The list-level rules live here because they are properties of the
// request rather than of the schedule: a slot named twice is a malformed
// payload, and the condition / astro vocabularies arrive as free strings
// that have to be rejected by name for the caller to get a usable 4xx.
func simpleScheduleToDomain(entries []hmapi.SimpleScheduleEntry) (*schedule.Simple, error) {
	s := schedule.NewSimple()
	for i := range entries {
		e := entries[i]
		// The read path has never capped, so a schedule the CCU holds
		// past this point comes back in full. Rejecting it on the way
		// out turned every such schedule into one an operator could open
		// but not save.
		if e.SlotNo < 1 || e.SlotNo > schedule.SimpleMaxSlot {
			return nil, fmt.Errorf("slot_no out of range: %d (1..%d)", e.SlotNo, schedule.SimpleMaxSlot)
		}
		if _, dup := s.Entries[e.SlotNo]; dup {
			return nil, fmt.Errorf("duplicate slot_no %d", e.SlotNo)
		}
		weekdays := make([]schedule.Weekday, 0, len(e.Weekdays))
		for _, d := range e.Weekdays {
			weekdays = append(weekdays, schedule.Weekday(strings.ToUpper(d)))
		}
		condition := schedule.Condition(e.Condition)
		if condition != "" && !weekprofile.ConditionIsKnown(condition) {
			return nil, fmt.Errorf("slot %d: unknown condition %q", e.SlotNo, e.Condition)
		}
		var astro schedule.Astro
		switch strings.ToLower(e.AstroType) {
		case "":
		case string(schedule.AstroSunrise):
			astro = schedule.AstroSunrise
		case string(schedule.AstroSunset):
			astro = schedule.AstroSunset
		default:
			return nil, fmt.Errorf("slot %d: unknown astro_type %q", e.SlotNo, e.AstroType)
		}
		s.Entries[e.SlotNo] = schedule.SimpleEntry{
			Weekdays:           weekdays,
			Time:               e.Time,
			Condition:          condition,
			AstroType:          astro,
			AstroOffsetMinutes: e.AstroOffsetMinutes,
			TargetChannels:     e.TargetChannels,
			Level:              e.Level,
			Level2:             e.Level2,
			Duration:           e.Duration,
			RampTime:           e.RampTime,
			ColorType:          e.ColorType,
			ColorValue:         e.ColorValue,
			OutputBehaviour:    e.OutputBehaviour,
		}
	}
	return s, nil
}

// serializeSimpleSchedule emits a flat paramset patch. When `domain`
// is "lock", lock_mode/lock_action/permission overwrite the entry's
// raw level/duration/target_channels with the canonical encoding —
// SPA users edit the friendly fields, the wire ends up consistent
// With.
func serializeSimpleScheduleWithDomain(
	entries []hmapi.SimpleScheduleEntry, domain string, deactivateUpTo int,
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
	return serializeSimpleSchedule(entries, deactivateUpTo)
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
		e.Duration = weekprofile.FormatTimeBaseFactor(durBase, durFactor)
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
		e.Duration = weekprofile.FormatTimeBaseFactor(lockPermissionDurationBase, lockPermissionDurationFactor)
		// Permission slots target channels >= 2_x.
		if len(e.TargetChannels) == 0 {
			e.TargetChannels = []string{"2_1"}
		}
	}
	return e
}

// serializeSimpleSchedule emits the `<NN>_WP_<FIELD>` MASTER paramset
// for the given entries, deactivating every unused slot up to
// deactivateUpTo so deleted entries vanish on the CCU.
//
// Like [parseSimpleSchedule] this is a projection: the DTO list is
// mapped onto [schedule.Simple] and encoded by
// [weekprofile.BuildSimpleRawParamset], which is the daemon's only
// encoder for this format.
func serializeSimpleSchedule(entries []hmapi.SimpleScheduleEntry, deactivateUpTo int) (map[string]any, error) {
	s, err := simpleScheduleToDomain(entries)
	if err != nil {
		return nil, fmt.Errorf("schedules: %w", err)
	}
	raw, err := weekprofile.BuildSimpleRawParamset(s, deactivateUpTo)
	if err != nil {
		return nil, fmt.Errorf("schedules: %w", err)
	}
	return raw, nil
}

// highestScheduleGroup reports the highest `<NN>_WP_*` group present in a
// MASTER paramset description, or 0 when the description is unavailable
// or names none.
func highestScheduleGroup(descKeys map[string]struct{}) int {
	highest := 0
	for key := range descKeys {
		if no, ok := weekprofile.SimpleGroupNo(key); ok && no > highest {
			highest = no
		}
	}
	return highest
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
) ([]hmapi.ClimateTimeCorrection, error) {
	backend, channelAddr, err := s.resolve(deviceAddress, channelNo)
	if err != nil {
		return nil, err
	}
	if sched == nil {
		return nil, errors.New("schedules: nil payload")
	}
	// Before serialisation: the corrections have to be collected while the
	// submitted representation is still intact.
	corrections := normalizeClimateScheduleTimes(sched)
	var raw map[string]any
	// The MASTER paramset description drives two decisions below:
	// which schedule fields the device advertises (unsupported keys are
	// filtered out), and whether a climate device uses the bare
	// (prefix-less) schema.
	descKeys, err := scheduleDescKeys(ctx, backend, channelAddr)
	if err != nil {
		return nil, err
	}
	switch sched.Kind {
	case "simple":
		raw, err = serializeSimpleScheduleWithDomain(sched.SimpleEntries, sched.Domain, highestScheduleGroup(descKeys))
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
		// keys — sending the P<n>_ form silently no-ops on the CCU. This
		// branch is load-bearing regardless of which filter runs below.
		if climateScheduleIsBare(descKeys) {
			raw, err = serializeClimateScheduleBare(sched)
		} else {
			raw, err = serializeClimateSchedule(sched)
		}
		if err == nil && len(raw) > 0 && len(descKeys) > 0 {
			// Filter out ENDTIME_/TEMPERATURE_ slot keys the device does not
			// declare in its MASTER paramset description. A climate slot key IS
			// the exact paramset parameter name (no group-number abstraction the
			// way WP_ fields have), so an exact-membership check against
			// descKeys is the correct filter — [weekprofile.ExtractSupportedScheduleFields]
			// only recognises the `_WP_` shape and would never match here.
			// Devices declare fewer than the 13×7 slots the serializer always
			// emits; sending the undeclared ones causes the CCU to reject the
			// write and leave CONFIG_PENDING set.
			raw = filterClimateScheduleByDescKeys(raw, descKeys)
		}
	default:
		return nil, fmt.Errorf("schedules: unknown kind %q", sched.Kind)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("schedules: empty payload")
	}
	if err := backend.PutParamset(ctx, channelAddr, hmenum.ParamsetKeyMaster, raw,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); err != nil {
		if !isCCUScheduleFalsePositive(err) {
			return nil, err
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
	return corrections, nil
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
	}, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); err != nil {
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
// The returned index is the 0-based profile index matching
// [hmapi.ClimateSchedule.ActiveProfileIndex]'s documented contract — the
// CCU's own ACTIVE_PROFILE value is the 1-based P<n> slot number.
func (s *SchedulesDomain) readActiveProfile(
	ctx context.Context, backend paramsetBackend, channelAddr string,
) (profileID string, index int, ok bool) {
	values, err := backend.GetParamset(ctx, channelAddr, hmenum.ParamsetKeyValues)
	if err != nil {
		return "", 0, false
	}
	raw, ok := values["ACTIVE_PROFILE"]
	if !ok {
		return "", 0, false
	}
	idx, ok := coerceInt(raw)
	if !ok || idx < 1 || idx > 6 {
		return "", 0, false
	}
	return fmt.Sprintf("P%d", idx), idx - 1, true
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
		b, ok := s.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
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
//
// A cell whose ordinal lies outside 1..[schedule.MaxClimateSlots] is not
// part of the schedule: it has nowhere to be written back, and the
// week-profile reader in [weekprofile.ParseClimateRawParamset] already
// discards it. Keeping it here would let the REST/WS surface and the MQTT
// climate payload describe the same channel differently. The ordinal
// bound is not enforced by [slotPattern], so it is applied here, and the
// dropped key is logged rather than discarded in silence — on the fleet
// the in-process CCU simulator models no paramset carries such an
// ordinal, so the log line is the only evidence a firmware outside that
// corpus would leave.
func parseClimateSchedule(ctx context.Context, raw map[string]any) (*hmapi.ClimateSchedule, error) {
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
		if slot < 1 || slot > schedule.MaxClimateSlots {
			slog.DebugContext(
				ctx, "schedules.parse_climate.slot_ordinal_out_of_range",
				slog.String("parameter", name),
				slog.Int("slot", slot),
				slog.Int("max_slot", schedule.MaxClimateSlots),
			)
			continue
		}
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

	// Group by (profile, weekday), then simplify.
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
// shape). The base temperature is the temperature holding the most
// minutes of the day, decided by
// [schedule.IdentifyBaseTemperatureFromSegments] so that this read path
// and the week-profile read path cannot report different bases for the
// same paramset; every stretch that is not the base becomes an explicit
// period.
func simplifyWeekday(slots map[int]*slotVals) hmapi.ClimateWeekday {
	// Slot numbers in ascending order.
	nums := make([]int, 0, len(slots))
	for n := range slots {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	// Flatten to time-ordered stretches. A cell without both wire values,
	// or one that does not advance past the previous end, carries no
	// stretch at all — this normalisation is specific to the flat
	// paramset and stays here; only the winner rule below is shared.
	prevEnd := 0
	flatSlots := make([]schedule.TempSegment, 0, len(nums))
	for _, n := range nums {
		sv := slots[n]
		if !sv.hasEnd || !sv.hasTemp {
			continue
		}
		if sv.endtime <= prevEnd {
			continue
		}
		flatSlots = append(flatSlots, schedule.TempSegment{
			StartMin:    prevEnd,
			EndMin:      sv.endtime,
			Temperature: sv.temperature,
		})
		prevEnd = sv.endtime
	}

	base := schedule.IdentifyBaseTemperatureFromSegments(flatSlots)

	// Periods are all slot ranges whose temperature is NOT the base,
	// merged into contiguous blocks.
	periods := make([]hmapi.ClimatePeriod, 0)
	for i := 0; i < len(flatSlots); {
		if math.Abs(flatSlots[i].Temperature-base) < 1e-6 {
			i++
			continue
		}
		start := flatSlots[i].StartMin
		end := flatSlots[i].EndMin
		temp := flatSlots[i].Temperature
		j := i + 1
		for j < len(flatSlots) && math.Abs(flatSlots[j].Temperature-temp) < 1e-6 && flatSlots[j].StartMin == end {
			end = flatSlots[j].EndMin
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
// and returns its key set.
//
// The error is propagated rather than folded into an empty key set: both
// decisions the set drives — the bare-vs-prefixed schema and the
// unsupported-field filter — degrade into writes the CCU discards silently, so
// a caller that cannot read the description must fail loudly instead of
// reporting a save it did not perform.
func scheduleDescKeys(
	ctx context.Context, backend paramsetBackend, channelAddr string,
) (map[string]struct{}, error) {
	desc, err := backend.GetParamsetDescription(ctx, channelAddr, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedules: master paramset description of %s: %w", channelAddr, err)
	}
	keys := make(map[string]struct{}, len(desc))
	for k := range desc {
		keys[k] = struct{}{}
	}
	return keys, nil
}

// filterClimateScheduleByDescKeys keeps only the ENDTIME_/TEMPERATURE_ slot
// keys the device's own MASTER paramset description declares. Every key in
// raw (built by [serializeClimateSchedule] / [serializeClimateScheduleBare])
// is itself a CCU paramset parameter name, so an exact membership check
// against descKeys is the whole filter — unlike the WP_-style simple
// schedule, a climate slot has no group-number indirection to abstract over.
func filterClimateScheduleByDescKeys(raw map[string]any, descKeys map[string]struct{}) map[string]any {
	if len(descKeys) == 0 || len(raw) == 0 {
		return raw
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if _, ok := descKeys[k]; ok {
			out[k] = v
		}
	}
	return out
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

// expandWeekday fills [schedule.MaxClimateSlots] slots from base
// temperature + periods. The
// simple form may have gaps (times where no period is defined); those
// default to the base temperature. Overlapping periods abort.
func expandWeekday(wd hmapi.ClimateWeekday) ([]rawSlot, error) {
	// Walk the periods in start-time order.
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
	// pad to exactly [schedule.MaxClimateSlots] slots by repeating the
	// last (24:00, base) entry.
	type stretch struct {
		end  int
		temp float64
	}
	stretches := make([]stretch, 0, schedule.MaxClimateSlots+1)
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
	if len(stretches) > schedule.MaxClimateSlots {
		return nil, fmt.Errorf("too many periods: yielded %d slots, max %d",
			len(stretches), schedule.MaxClimateSlots)
	}
	// Pad with (24:00, base) up to the full slot count.
	out := make([]rawSlot, schedule.MaxClimateSlots)
	for i := range schedule.MaxClimateSlots {
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

// formatMinutes renders a raw ENDTIME the device reported. It clamps
// rather than failing, because this is the READ direction: a device that
// already holds an out-of-range minute count still has to render. The
// write direction must not clamp — see [minutesFromTime].
func formatMinutes(m int) string {
	m = max(m, 0)
	m = min(m, schedule.ClimateEndOfDayMinutes)
	out, err := schedule.FormatClimateTime(m)
	if err != nil {
		return schedule.ClimateEndOfDay
	}
	return out
}

// minutesFromTime is the -1-sentinel form of [schedule.ParseClimateTime].
// The sentinel is kept because [expandWeekday] sorts periods by start time
// before it validates them, and a comparator has to stay total.
func minutesFromTime(s string) int {
	m, err := schedule.ParseClimateTime(s)
	if err != nil {
		return -1
	}
	return m
}
