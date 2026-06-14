// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// note (PR 6, partial)
// ----------------------------------
// This file currently carries only the week-profile slot filter used
// during MASTER paramset hydration. The full work
// surfacing the 6×7×N week-program slots as a single
// WeekProfileDataPoint with structured read / write semantics.
// in a follow-up PR. The filter alone is enough to keep the gross
// churn (~84 ghost MQTT topics per HmIP thermostat) out of the
// distributed surface, which is the user-visible blocker; the deeper
// data-model work can land independently without further breaking
// changes to consumers.
//
// When the deeper PR lands, this filter is replaced with an actual
// channel-scoped DP that owns the slots. Until then, P*_ MASTER
// parameters are intentionally **invisible** to REST / WS / MQTT.
//
// The attach helper [attachWeekProfileToChannel] runs alongside the
// filter to install a single [weekprofile.ProfileDataPoint] as the
// canonical schedule entity for any channel where slots were seen.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// parseFloat decodes a JSON-RawMessage as a float64. Returns ok=false
// for empty / null / non-numeric payloads. Used to extract MIN / MAX
// from [hmproto.ParameterData] descriptors.
func parseFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// attachWeekProfileToChannel installs a [weekprofile.ProfileDataPoint]
// on ch when none is attached yet. Called from the MASTER paramset
// hydration loop the first time a P*_* slot parameter is seen for
// the channel — subsequent slot encounters short-circuit on the
// no-op branch.
//
// The descriptor is constructed with conservative defaults
// (ScheduleType=Climate, ProfileCount=6, MinTemp/MaxTemp=0). Future
// passes that learn the device's actual temperature bounds (from the
// SET_POINT_TEMPERATURE descriptor MIN/MAX) can refine those fields
// in place — the descriptor is mutable through the
// [weekprofile.ProfileDataPoint] API. Defaults are cheap to override
// and never observed by the wire, so getting them later is safe.
func attachWeekProfileToChannel(ch *device.Channel, centralName string) {
	if ch == nil || ch.HasWeekProfile() {
		return
	}
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    centralName,
		ChannelAddress: ch.Address,
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		// 6 == max profiles per CCU (P1..P6); the actual cap for the
		// device may be lower (RF thermostats expose only P1..P3) but
		// that is refined later by [SchedulesDomain.MaxProfilesForDevice]
		// once the ACTIVE_PROFILE / WEEK_PROGRAM_POINTER descriptor
		// has been hydrated.
		ProfileCount: 6,
	})
	// Override the constructor's NoCreate default: a pipeline-attached
	// week profile is the canonical schedule entity for the channel
	// and should surface on REST + MQTT-Discovery + UI. Test fixtures
	// that build a DP via NewProfileDataPoint directly keep the
	// NoCreate default — only the pipeline path opts in here.
	wp.SetForcedUsage(hmenum.DataPointUsageDataPoint)
	ch.AttachWeekProfile(wp)
}

// attachNonClimateWeekProfileToDevice scans dev for a non-climate
// schedule channel (one carrying WEEK_PROGRAM_CHANNEL_LOCKS) and
// installs a ScheduleType=Default ProfileDataPoint plus the
// per-channel ScheduleChannelSwitches.
//
// Idempotent: re-running on a device that already carries
// ScheduleChannelSwitches replaces them in place. No-op when no
// schedule channel is detected.
//
// Writer is the same Channel.Writer the wire pipeline already attaches
// — same Writer satisfies [weekprofile.ScheduleWriter] because the
// SetValue signature matches.
func attachNonClimateWeekProfileToDevice(dev *device.Device, centralName string) {
	if dev == nil {
		return
	}
	scheduleCh := findScheduleChannel(dev)
	if scheduleCh == nil {
		return
	}
	// Skip when a climate WeekProfile already lives on the device — the
	// two surfaces are mutually exclusive.
	for _, ch := range dev.Channels() {
		if wp := ch.WeekProfile(); wp != nil && wp.ScheduleType() == weekprofile.ScheduleTypeClimate {
			return
		}
	}
	// Suppress the redundant target-lock wire DPs on the schedule channel
	// in every case — even when no CDP-backed target map can be derived
	// below (e.g. HmIP-MIO16-PCB, which carries no custom DPs): the
	// reference stack never surfaces the target-lock select/number as
	// entities, regardless of whether schedule switches exist.
	suppressRedundantScheduleDPs(scheduleCh)
	targets := deriveTargetChannels(dev)
	if len(targets) == 0 {
		return
	}
	wp := scheduleCh.WeekProfile()
	if wp == nil {
		wp = weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
			CentralName:    centralName,
			ChannelAddress: scheduleCh.Address,
			ScheduleType:   weekprofile.ScheduleTypeDefault,
			ProfileCount:   1,
		})
		wp.SetForcedUsage(hmenum.DataPointUsageDataPoint)
		scheduleCh.AttachWeekProfile(wp)
	}
	wp.SetAvailableTargetChannels(targets)
	wp.AttachWriter(&scheduleWriteForwarder{ch: scheduleCh}, scheduleCh.Address)
	// Wire MASTER-paramset Load/Save so the Zeitplan sensor can surface
	// the actual schedule entries decoded from `<NN>_WP_<FIELD>` slots.
	bindDefaultScheduleIO(scheduleCh, wp)
	// Register every target key on the DP so SetScheduleEnabled has the
	// full enumeration when broadcasting (empty channelKey).
	for k := range targets {
		wp.RegisterChannel(k, true)
	}
	// Build a stable, ordered list of switches mirroring
	// `_create_schedule_channel_switches`.
	keys := make([]string, 0, len(targets))
	for k := range targets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	switches := make([]*weekprofile.ChannelSwitch, 0, len(keys))
	for _, k := range keys {
		switches = append(switches, weekprofile.NewChannelSwitch(centralName, dev.Address, k, wp))
	}
	dev.SetScheduleChannelSwitches(switches)
}

// suppressRedundantScheduleDPs marks the WEEK_PROGRAM_TARGET_CHANNEL_LOCK
// and WEEK_PROGRAM_TARGET_CHANNEL_LOCKS generic DPs as NoCreate so HA
// only sees the per-channel ScheduleChannelSwitch surface. Idempotent.
//
// WEEK_PROGRAM_CHANNEL_LOCKS is deliberately NOT suppressed here: the
// reference stack surfaces the bitfield as a regular sensor on devices
// without a custom DP (HmIP-MIO16-PCB). On CDP-carrying devices the
// undefined-generic-DP suppression pass already marks it NoCreate, so
// no extra forcing is needed in either case.
func suppressRedundantScheduleDPs(ch *device.Channel) {
	if ch == nil {
		return
	}
	for _, param := range scheduleSurfaceSuppressed {
		dp := ch.Parameter(param)
		if dp == nil {
			continue
		}
		if f, ok := dp.(interface {
			SetForcedUsage(hmenum.DataPointUsage)
		}); ok {
			f.SetForcedUsage(hmenum.DataPointUsageNoCreate)
		}
	}
}

// scheduleSurfaceSuppressed enumerates the wire parameters the
// non-climate ScheduleChannelSwitch surface replaces. Sourced from the
// MIO16-PCB channel 49 inventory; the same names apply to every other
// schedule-channel device family in the CCU catalogue.
var scheduleSurfaceSuppressed = []hmenum.Parameter{
	"WEEK_PROGRAM_TARGET_CHANNEL_LOCK",
	"WEEK_PROGRAM_TARGET_CHANNEL_LOCKS",
}

// findScheduleChannel returns the channel that carries the
// WEEK_PROGRAM_CHANNEL_LOCKS parameter — the canonical signal that the
// device exposes a non-climate week schedule. Returns nil for climate
// devices and for devices without a schedule surface.
func findScheduleChannel(dev *device.Device) *device.Channel {
	if dev == nil {
		return nil
	}
	for _, ch := range dev.Channels() {
		if ch.Parameter(hmenum.ParameterWeekProgramChannelLocks) != nil {
			return ch
		}
	}
	return nil
}

// deriveTargetChannels builds the `<actor>_<sub>` target-channel map
// from the device's custom-DP channel groups, mirroring the reference
// stack's `_build_target_channel_map`:
//
//   - actor index = 1-based position in the sorted set of
//     schedule-relevant channel groups
//     ([custom.ScheduleRelevantChannelGroups]),
//   - sub index = 1-based position within the group: primary channel
//     first, then the profile's secondary channels.
//
// Devices without any custom DP yield an empty map — the reference
// stack derives the non-climate week profile from a custom DP, so a
// CDP-less schedule channel (HmIP-MIO16-PCB) carries no schedule
// switches there either; only the raw WEEK_PROGRAM_CHANNEL_LOCKS
// sensor remains.
//
// The earlier 3-receivers-per-actor heuristic over *_VIRTUAL_RECEIVER
// channel types is gone: it produced no targets for ELV-SH-WSM /
// HmIP-WSM (WATER_SWITCH_VIRTUAL_RECEIVER channels) and wrong keys for
// HmIP-MP3P and HmIP-WRC6-230 (mixed single-channel groups).
func deriveTargetChannels(dev *device.Device) map[string]weekprofile.TargetChannelInfo {
	if dev == nil {
		return nil
	}
	groups := custom.ScheduleRelevantChannelGroups(dev, custom.DefaultRegistry())
	if len(groups) == 0 {
		return nil
	}
	out := make(map[string]weekprofile.TargetChannelInfo)
	for actorIdx, group := range groups {
		for subIdx, member := range group.Channels {
			key := fmt.Sprintf("%d_%d", actorIdx+1, subIdx+1)
			chType := "secondary"
			if member.Primary {
				chType = "primary"
			}
			address := fmt.Sprintf("%s:%d", dev.Address, member.ChannelNo)
			name := fmt.Sprintf("Channel %d", member.ChannelNo)
			if ch := dev.Channel(address); ch != nil && ch.Name != "" {
				name = ch.Name
			}
			out[key] = weekprofile.TargetChannelInfo{
				ChannelNo:      member.ChannelNo,
				ChannelAddress: address,
				Name:           name,
				ChannelType:    chType,
			}
		}
	}
	return out
}

// scheduleWriteForwarder exposes [device.Channel]'s installed writer
// through the [weekprofile.ScheduleWriter] interface. The signatures
// match the [device.ChannelWriter]'s SetValue, so the call is a direct
// forward. Naming distinguishes this from the existing
// [channelWriterAdapter] in bound_writer.go, which is the inbound
// adapter (boundWriter+backend → ChannelWriter).
type scheduleWriteForwarder struct {
	ch *device.Channel
}

// SetValue delegates to the channel-attached writer.
func (a *scheduleWriteForwarder) SetValue(
	ctx context.Context,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	if a == nil || a.ch == nil {
		return errors.New("weekprofile: channel writer not attached")
	}
	w := a.ch.Writer()
	if w == nil {
		return fmt.Errorf("weekprofile: channel %s carries no writer", a.ch.Address)
	}
	return w.SetValue(ctx, channelAddress, parameter, value, priority)
}

// Compile-time satisfaction of the ScheduleWriter contract.
var _ generic.Writer = (*scheduleWriteForwarder)(nil)

// attachNonClimateWeekProfiles walks every device on interfaceID and
// runs [attachNonClimateWeekProfileToDevice]. Idempotent.
func (p *DevicePipeline) attachNonClimateWeekProfiles(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.unit.ModelRegistry.List() {
		if d.InterfaceID != interfaceID {
			continue
		}
		attachNonClimateWeekProfileToDevice(d, p.unit.Name())
	}
}

// normalizeClimateWeekProfiles reconciles the slot-parameter-heuristic climate
// week-profile attachment with the reference has_schedule gate AND the channel
// the loom-client probes:
//
//   - A device that declares no schedule channel (no registered
//     schedule_channel_no, no WEEK_PROFILE channel — e.g. ALPHA-IP-RBG) gets
//     its spurious climate week profile detached.
//   - A device that does declare one has its climate week profile relocated to
//     the canonical schedule channel — the WEEK_PROFILE-suffix channel, else
//     the climate custom-DP channel. That is exactly the channel the
//     loom-client probes for a week profile (adapter._bootstrap_schedules), so
//     a profile the heuristic parked on the wrong channel (HM-TC-IT on the
//     device root, HmIP-WGTC on the climate channel instead of its
//     SWITCH_WEEK_PROFILE channel) is no longer a 404.
//
// Idempotent: a profile already on the canonical channel is left untouched.
func (p *DevicePipeline) normalizeClimateWeekProfiles(interfaceID string) {
	if p.unit == nil {
		return
	}
	reg := custom.DefaultRegistry()
	for _, d := range p.unit.ModelRegistry.List() {
		if d.InterfaceID != interfaceID {
			continue
		}
		// Only act on a CLIMATE week profile the slot-parameter heuristic
		// already attached. Devices with no climate profile (non-climate
		// schedule devices handled by attachNonClimateWeekProfiles) are left
		// untouched — attaching a climate profile here would make that pass
		// skip them and drop their ScheduleChannelSwitches.
		existing := existingClimateWeekProfileChannel(d)
		if existing == nil {
			continue
		}
		if !deviceHasRegisteredScheduleChannel(d, reg) {
			existing.AttachWeekProfile(nil)
			continue
		}
		canonical := canonicalScheduleChannel(d)
		if canonical != nil && existing.Address != canonical.Address {
			existing.AttachWeekProfile(nil)
			attachWeekProfileToChannel(canonical, p.unit.Name())
		}
	}
}

// existingClimateWeekProfileChannel returns the channel (including the device
// root) that currently carries a climate week profile, or nil.
func existingClimateWeekProfileChannel(dev *device.Device) *device.Channel {
	channels := append([]*device.Channel(nil), dev.Channels()...)
	if root := dev.RootChannel(); root != nil {
		channels = append(channels, root)
	}
	for _, ch := range channels {
		if wp := ch.WeekProfile(); wp != nil && wp.ScheduleType() == weekprofile.ScheduleTypeClimate {
			return ch
		}
	}
	return nil
}

// canonicalScheduleChannel returns the channel the loom-client probes for a
// week profile: the WEEK_PROFILE-suffix channel if present, else the climate
// custom-DP channel. Returns nil when neither exists.
func canonicalScheduleChannel(dev *device.Device) *device.Channel {
	for _, ch := range dev.Channels() {
		if strings.HasSuffix(ch.Type, "WEEK_PROFILE") {
			return ch
		}
	}
	for _, ch := range dev.Channels() {
		cdp := ch.CustomDataPoint()
		if cdp == nil {
			continue
		}
		if c, ok := cdp.(device.CategorisedDataPoint); ok && c.Category() == hmenum.DataPointCategoryClimate {
			return ch
		}
	}
	return nil
}

// deviceHasRegisteredScheduleChannel reports whether the device declares a
// schedule channel — a custom profile with a non-nil ScheduleChannelNo or a
// WEEK_PROFILE-suffix channel. Mirrors the reference has_schedule gate
// (schedule_channel_address != None).
func deviceHasRegisteredScheduleChannel(dev *device.Device, reg *custom.Registry) bool {
	if dev == nil || reg == nil {
		return false
	}
	for _, prof := range reg.GetConfigs(dev.Model) {
		if prof.ScheduleChannelNo != nil {
			return true
		}
	}
	for _, ch := range dev.Channels() {
		if strings.HasSuffix(ch.Type, "WEEK_PROFILE") {
			return true
		}
	}
	return false
}

// refineAttachedWeekProfiles walks every channel of every device on
// interfaceID that has a previously-attached
// [weekprofile.ProfileDataPoint] and updates its temperature bounds
// + profile cap with values pulled from the now-hydrated VALUES
// Descriptors.
// `max_temp`, `schedule_profile_nos` from the underlying generic DPs.
//
// Three sources are consulted, in priority order:
//
// - SET_POINT_TEMPERATURE descriptor MIN/MAX → MinTemp / MaxTemp
// - ACTIVE_PROFILE descriptor MAX → ProfileCount (HmIP, 1-based: 6 → 6).
// - WEEK_PROGRAM_POINTER descriptor MAX → ProfileCount (RF, 0-based: 2 → 3).
//
// When neither pointer DP is present the helper leaves ProfileCount
// at its construction default (6) — the same conservative fallback
func (p *DevicePipeline) refineAttachedWeekProfiles(interfaceID string, logger *slog.Logger) {
	if p.unit == nil {
		return
	}
	refined := 0
	for _, d := range p.unit.ModelRegistry.List() {
		if d.InterfaceID != interfaceID {
			continue
		}
		// Walk both real channels AND the device-root pseudo-channel
		// (classic HM-CC-RT-DN carries its week profile there).
		channels := append([]*device.Channel(nil), d.Channels()...)
		if root := d.RootChannel(); root != nil {
			channels = append(channels, root)
		}
		for _, ch := range channels {
			wp := ch.WeekProfile()
			if wp == nil {
				continue
			}
			meta := deriveWeekProfileMetadata(d, ch)
			wp.ApplyDeviceMetadata(meta)
			subscribeProfilePointer(d, wp)
			// Attach a backend-bound Profile[*schedule.Climate] so
			// callers can Load / Save directly through the DP without
			// going through SchedulesDomain. Channel.Refresher
			// Channel.Writer are wired by the pipeline before this
			// pass runs (in [DevicePipeline.hydrateChannel]).
			bindClimateScheduleIO(ch, wp)
			refined++
		}
	}
	if logger != nil && refined > 0 {
		logger.Debug("pipeline.weekprofile.refined",
			slog.String("interface", interfaceID),
			slog.Int("count", refined))
	}
}

// deriveWeekProfileMetadata pulls the temperature bounds + profile cap
// from the device's hydrated VALUES descriptors. Returns the zero
// value (which [weekprofile.ProfileDataPoint.ApplyDeviceMetadata]
// treats as a no-op for ProfileCount) when no source is available.
//
// Looking at *every* channel of the device — not just the channel
// that carries the schedule — is intentional: SET_POINT_TEMPERATURE
// usually sits on the climate channel, but ACTIVE_PROFILE
// WEEK_PROGRAM_POINTER can be on the device-meta channel (channel 0)
// or a sibling.
func deriveWeekProfileMetadata(d *device.Device, _ *device.Channel) weekprofile.DeviceMetadata {
	var meta weekprofile.DeviceMetadata
	for _, ch := range d.Channels() {
		if dp := ch.Parameter(hmenum.ParameterSetPointTemperature); dp != nil {
			pd := dp.ParameterData()
			if pd.Min != nil {
				if v, ok := parseFloat(pd.Min); ok {
					meta.MinTemp = v
				}
			}
			if pd.Max != nil {
				if v, ok := parseFloat(pd.Max); ok {
					meta.MaxTemp = v
				}
			}
		}
		// HmIP: ACTIVE_PROFILE is 1-based, MAX == profile count.
		if dp := ch.Parameter(hmenum.ParameterActiveProfile); dp != nil {
			pd := dp.ParameterData()
			if pd.Max != nil {
				if v, ok := parseFloat(pd.Max); ok && int(v) >= 1 {
					meta.ProfileCount = int(v)
				}
			}
		}
		// Classic HM: WEEK_PROGRAM_POINTER is 0-based, count == MAX+1.
		// Only consult this when ACTIVE_PROFILE did not yield a value
		// to avoid double-counting on devices that carry both.
		if meta.ProfileCount == 0 {
			if dp := ch.Parameter(hmenum.ParameterWeekProgramPointer); dp != nil {
				pd := dp.ParameterData()
				if pd.Max != nil {
					if v, ok := parseFloat(pd.Max); ok && int(v) >= 0 {
						meta.ProfileCount = int(v) + 1
					}
				}
			}
		}
	}
	return meta
}

// subscribeProfilePointer wires the device's ACTIVE_PROFILE (HmIP) or
// WEEK_PROGRAM_POINTER (RF) DP — whichever exists — to the attached
// week-profile descriptor's [SyncProfilePointer] method, so a CCU push event
// for the pointer DP automatically updates the descriptor's CurrentProfile
// field.
//
// Idempotent at the DP level (the OnAnyUpdate registration is per-DP), but
// [refineAttachedWeekProfiles] only re-runs at boot so duplicate
// subscriptions are not a practical concern.
//
// Best-effort: if the subscription cannot be installed (DP not observed, type
// mismatch, etc.) the descriptor still works — the initial value will be
// applied during refinement and subsequent updates simply lag behind until
// the next ReloadAndCacheSchedule.
func subscribeProfilePointer(d *device.Device, wp *weekprofile.ProfileDataPoint) {
	if d == nil || wp == nil {
		return
	}
	for _, ch := range d.Channels() {
		// Try ACTIVE_PROFILE first (HmIP). Fall back to
		// WEEK_PROGRAM_POINTER (RF). If both exist, ACTIVE_PROFILE
		// wins — same precedence as deriveWeekProfileMetadata.
		for _, p := range []hmenum.Parameter{
			hmenum.ParameterActiveProfile,
			hmenum.ParameterWeekProgramPointer,
		} {
			dp := ch.Parameter(p)
			if dp == nil {
				continue
			}
			dp.OnAnyUpdate(func(_, next any) {
				_ = wp.SyncProfilePointer(next)
			})
			// Seed once with the current value so the descriptor's
			// CurrentProfile reflects the live state right after
			// boot, not just after the next push event.
			if v, observed := dp.RawValue(); observed {
				_ = wp.SyncProfilePointer(v)
			}
			return
		}
	}
}

// isWeekProfileSlotParameter reports whether name is one of the
// per-slot parameters of a CCU week-program. The CCU exposes two
// distinct slot schemas:
//
// - Schema A (HmIP, multi-profile): `P<N>_TEMPERATURE_<DAY>_<SLOT>`
// and `P<N>_ENDTIME_<DAY>_<SLOT>` with N ∈ {1..6}. Found on
// HmIP thermostat climate channels (HmIP-eTRV-*, HmIP-BWTH,
// HmIP-WGTC, HmIP-HEATING, HmIP-STHD …).
// - Schema B (classic HM, single-profile): bare
// `TEMPERATURE_<DAY>_<SLOT>` and `ENDTIME_<DAY>_<SLOT>` — no
// profile prefix because these devices only ship a single
// schedule. Found on classic HM-CC-RT-DN(-BoM) at the
// device-root MASTER paramset (verified against the in-process CCU
// simulator paramset descriptions).
//
// Both schemas surface as ~84-180+ leaves per channel that
// folds into a single [weekprofile.ProfileDataPoint]; we suppress
// them at hydration time so REST / WS / MQTT / UI never see the raw
// slot DPs. The patterns are highly specific (TEMPERATURE/ENDTIME +
// weekday name + 1..13 slot number), which makes false-positives on
// unrelated channels effectively impossible.
//
// Deliberately NOT matched:
//
// - P_NUMBER, PRESS_*, PARTY_* — different prefix shape.
// - WEEK_PROGRAM_POINTER, ACTIVE_PROFILE — top-level pointer
// parameters; they MUST remain visible.
// - TEMPERATURE_MINIMUM, TEMPERATURE_MAXIMUM, TEMPERATURE_OFFSET
// bare bounds parameters without the weekday/slot suffix.
func isWeekProfileSlotParameter(name string) bool {
	// Schema A: P<N>_TEMPERATURE_<DAY>_<SLOT> / P<N>_ENDTIME_<DAY>_<SLOT>.
	if len(name) >= 4 && name[0] == 'P' && name[1] >= '1' && name[1] <= '6' && name[2] == '_' {
		rest := name[3:]
		for i := range len(rest) {
			c := rest[i]
			switch {
			case c >= 'A' && c <= 'Z':
			case c >= '0' && c <= '9':
			case c == '_':
			default:
				return false
			}
		}
		return true
	}
	// Schema B: bare TEMPERATURE_<DAY>_<SLOT> / ENDTIME_<DAY>_<SLOT>.
	return matchesBareScheduleSlot(name)
}

// matchesBareScheduleSlot pins the Schema-B pattern:
// `^(TEMPERATURE|ENDTIME)_(MONDAY|TUESDAY|WEDNESDAY|THURSDAY|FRIDAY|SATURDAY|SUNDAY)_\d+$`.
// Walks the string manually to avoid the regex import cost on a hot
// hydration path that runs for every parameter of every channel.
func matchesBareScheduleSlot(name string) bool {
	rest, ok := stripPrefix(name, "TEMPERATURE_")
	if !ok {
		rest, ok = stripPrefix(name, "ENDTIME_")
	}
	if !ok {
		return false
	}
	day, after, ok := consumeWeekday(rest)
	if !ok {
		return false
	}
	_ = day
	if after == "" || after[0] != '_' {
		return false
	}
	slot := after[1:]
	if slot == "" {
		return false
	}
	for i := range len(slot) {
		if slot[i] < '0' || slot[i] > '9' {
			return false
		}
	}
	return true
}

func stripPrefix(s, prefix string) (rest string, ok bool) {
	if len(s) < len(prefix) {
		return s, false
	}
	if s[:len(prefix)] != prefix {
		return s, false
	}
	return s[len(prefix):], true
}

func consumeWeekday(s string) (day, rest string, ok bool) {
	for _, d := range [...]string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY", "SUNDAY"} {
		if rest, ok := stripPrefix(s, d); ok {
			return d, rest, true
		}
	}
	return "", s, false
}
