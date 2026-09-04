// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrChannelNotWired is returned by the channel-scoped loader / saver
// adapters when the underlying [device.Channel.Refresher] /
// [device.Channel.Writer] is nil — the channel was hydrated but no backend
// has been bound yet.
var ErrChannelNotWired = errors.New("schedule: channel has no backend bound")

// climateChannelLoader implements [weekprofile.Loader[*schedule.Climate]]
// against a [device.Channel]. It pulls the channel's MASTER paramset
// via the installed refresher, parses the slot parameters into a
// [schedule.Climate], and is reused for every Load call (no caching —
// the surrounding [weekprofile.Profile] holds the snapshot).
type climateChannelLoader struct {
	ch *device.Channel
}

// Load reads the channel's MASTER paramset and returns a parsed
// [schedule.Climate]. Returns [ErrChannelNotWired] when no refresher
// is installed on the channel.
func (l *climateChannelLoader) Load(ctx context.Context) (*schedule.Climate, error) {
	r := l.ch.Refresher()
	if r == nil {
		return nil, ErrChannelNotWired
	}
	values, err := r.GetParamset(ctx, l.ch.Address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedule.load: GetParamset: %w", err)
	}
	rawSched, err := weekprofile.ParseClimateRawParamset(values)
	if err != nil {
		return nil, fmt.Errorf("schedule.load: parse raw paramset: %w", err)
	}
	c, err := weekprofile.RawToClimate(rawSched)
	if err != nil {
		return nil, fmt.Errorf("schedule.load: convert to climate: %w", err)
	}
	return c, nil
}

// climateChannelSaver implements [weekprofile.Saver[*schedule.Climate]].
// Encodes the schedule into the wire-form raw paramset and writes it
// back via the installed [device.ChannelWriter.PutParamset].
type climateChannelSaver struct {
	ch       *device.Channel
	priority hmenum.CommandPriority
}

// Save converts c into the CCU MASTER paramset wire form and writes
// it through the channel's writer. Returns [ErrChannelNotWired] when
// no writer has been installed.
func (s *climateChannelSaver) Save(ctx context.Context, c *schedule.Climate) error {
	w := s.ch.Writer()
	if w == nil {
		return ErrChannelNotWired
	}
	rawSched, err := weekprofile.ClimateToRawWire(c)
	if err != nil {
		return fmt.Errorf("schedule.save: convert from climate: %w", err)
	}
	values, err := weekprofile.BuildClimateRawParamset(rawSched)
	if err != nil {
		return fmt.Errorf("schedule.save: build raw paramset: %w", err)
	}
	if err := w.PutParamset(ctx, s.ch.Address, hmenum.ParamsetKeyMaster, values, s.priority); err != nil {
		return fmt.Errorf("schedule.save: PutParamset: %w", err)
	}
	return nil
}

// bindClimateScheduleIO constructs and attaches a fully-wired
// [weekprofile.ClimateProfile] (a `Profile[*schedule.Climate]`) to wp,
// using ch's installed refresher / writer. Subsequent
// `wp.Climate().Load(ctx)` and `wp.Climate().Save(ctx, sched)` calls
// then route directly through ch without going through SchedulesDomain.
//
// Idempotent: replaces a previously attached profile, so a daemon
// restart cycle (re-hydration) updates the wiring cleanly. The two
// pathways (this DP-bound profile + the SchedulesDomain) remain
// independent for now; SchedulesDomain still keeps its own cache,
// but new code that prefers the DP-centric API can use the bound
// profile directly.
func bindClimateScheduleIO(ch *device.Channel, wp *weekprofile.ProfileDataPoint) {
	if ch == nil || wp == nil {
		return
	}
	loader := &climateChannelLoader{ch: ch}
	saver := &climateChannelSaver{ch: ch, priority: hmenum.CommandPriorityHigh}
	profile := weekprofile.NewClimate(loader, saver)
	wp.AttachClimateProfile(profile)
}

// defaultChannelLoader implements [weekprofile.Loader[*schedule.Simple]]
// against a [device.Channel]. Reads the channel's MASTER paramset via the
// installed refresher and parses the `<NN>_WP_<FIELD>` keys into a
// [schedule.Simple].
//
// domain is the resolved schedule bucket (see [resolveScheduleDomain]). It is
// only load-bearing for "lock": [weekprofile.ParseSimpleRawParamset] surfaces
// the raw LEVEL / DURATION / TARGET_CHANNELS values, but a lock device encodes
// lock_mode / lock_action / permission as combinations of those, so the loader
// has to decode them explicitly for the read surfaces (MQTT Zeitplan attrs) to
// show the real lock action rather than three permanent nulls.
// targetChannelBits resolves the TARGET_CHANNELS bit positions for a schedule
// channel from the device's own channel numbers.
//
// The map is the one already built for `available_target_channels`
// ([deriveTargetChannels]), which is the same resolution the CCU's weekly-
// program editor performs. Returns nil when the device yields none — the
// encoder then withholds the field instead of computing a position that would
// switch a channel the operator did not pick.
func targetChannelBits(ch *device.Channel) weekprofile.TargetChannelBits {
	if ch == nil {
		return nil
	}
	wp := ch.WeekProfile()
	if wp == nil {
		return nil
	}
	return weekprofile.TargetChannelBitsFrom(wp.AvailableTargetChannels())
}

// astroOffsetLimits reads the ASTRO_OFFSET bounds the channel declares.
//
// The CCU's own weekly-program editor holds no constant for this: it reads
// ASTRO_OFFSET_MIN / ASTRO_OFFSET_MAX out of the paramset description and
// clamps its input to them. Slot 1 is representative — a device declares the
// same range on every slot.
func astroOffsetLimits(ch *device.Channel) weekprofile.AstroOffsetLimits {
	if ch == nil {
		return weekprofile.AstroOffsetLimits{}
	}
	lo, hi, ok := ch.MasterParameterIntRange("01_WP_ASTRO_OFFSET")
	if !ok {
		return weekprofile.AstroOffsetLimits{}
	}
	return weekprofile.AstroOffsetLimits{Min: lo, Max: hi, Declared: true}
}

type defaultChannelLoader struct {
	ch     *device.Channel
	domain string
}

// Load reads the channel's MASTER paramset and returns a parsed Simple
// schedule. Returns [ErrChannelNotWired] when no refresher is installed.
func (l *defaultChannelLoader) Load(ctx context.Context) (*schedule.Simple, error) {
	r := l.ch.Refresher()
	if r == nil {
		return nil, ErrChannelNotWired
	}
	values, err := r.GetParamset(ctx, l.ch.Address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedule.load.simple: GetParamset: %w", err)
	}
	s, err := weekprofile.ParseSimpleRawParamset(values, targetChannelBits(l.ch))
	if err != nil {
		return nil, fmt.Errorf("schedule.load.simple: parse raw paramset: %w", err)
	}
	if l.domain == "lock" {
		decodeLockScheduleFields(s, values)
	}
	return s, nil
}

// decodeLockScheduleFields enriches a lock device's parsed schedule with the
// lock_mode / lock_action / permission fields the CCU encodes via the
// (LEVEL, DURATION_BASE, DURATION_FACTOR, TARGET_CHANNELS) combination.
//
// Mirrors the REST read path (parseSimpleScheduleWithDomain in schedules.go):
// the duration base/factor are read from the raw paramset rather than the
// decoded entry, because [weekprofile.ParseSimpleRawParamset] drops the
// firmware "permanent" sentinel (factor 31) that distinguishes the
// auto-relock actions.
func decodeLockScheduleFields(s *schedule.Simple, raw map[string]any) {
	if s == nil {
		return
	}
	for slotNo := range s.Entries {
		entry := s.Entries[slotNo]
		entry.LockMode = schedule.DetectLockMode(entry.TargetChannels)
		dBase, dFactor := lookupSlotDuration(raw, slotNo)
		switch entry.LockMode {
		case schedule.LockModeDoorLock:
			entry.LockAction = schedule.DetectLockAction(entry.Level, dBase, dFactor)
		case schedule.LockModeUserPermission:
			entry.Permission = schedule.DetectLockPermission(entry.Level)
		}
		s.Entries[slotNo] = entry
	}
}

// defaultChannelSaver implements [weekprofile.Saver[*schedule.Simple]].
// Encodes the schedule into the wire-form raw paramset and writes it
// via the installed [device.ChannelWriter.PutParamset].
type defaultChannelSaver struct {
	ch       *device.Channel
	priority hmenum.CommandPriority
}

// Save converts s into the CCU MASTER paramset wire form and writes it
// through the channel's writer. Inactive groups are explicitly zeroed so
// the CCU deactivates deleted entries, bounded by what the channel
// declares — see [defaultChannelSaver.declaredGroups]. Returns
// [ErrChannelNotWired] when no writer has been installed.
func (sv *defaultChannelSaver) Save(ctx context.Context, s *schedule.Simple) error {
	w := sv.ch.Writer()
	if w == nil {
		return ErrChannelNotWired
	}
	values, err := weekprofile.BuildSimpleRawParamset(s, sv.declaredGroups(ctx), targetChannelBits(sv.ch), astroOffsetLimits(sv.ch))
	if err != nil {
		return fmt.Errorf("schedule.save.simple: %w", err)
	}
	if err = w.PutParamset(ctx, sv.ch.Address, hmenum.ParamsetKeyMaster, values, sv.priority); err != nil {
		return fmt.Errorf("schedule.save.simple: PutParamset: %w", err)
	}
	return nil
}

// declaredGroups reports the highest week-profile group the channel
// declares, for the deactivation sweep in
// [weekprofile.BuildSimpleRawParamset].
//
// The count is read from the device rather than assumed: channels carry
// 69 or 75 groups depending on model and firmware, and writing to one
// that does not exist fails the whole paramset with fault -5. A read
// that fails yields 0, which skips the sweep — the active groups are
// still written, so a save degrades to "does not clear deleted entries"
// instead of failing outright.
func (sv *defaultChannelSaver) declaredGroups(ctx context.Context) int {
	r := sv.ch.Refresher()
	if r == nil {
		return 0
	}
	values, err := r.GetParamset(ctx, sv.ch.Address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return 0
	}
	return weekprofile.HighestSimpleGroup(values)
}

// bindDefaultScheduleIO attaches a fully-wired
// [weekprofile.DefaultProfile] (a `Profile[*schedule.Simple]`) to wp,
// using ch's installed refresher / writer. After this call,
// `wp.Simple().Load(ctx)` and `wp.Simple().Save(ctx, sched)` route
// directly through ch.
//
// domain is the resolved schedule bucket (see [resolveScheduleDomain]); the
// loader uses it to decode lock-specific fields on the read path.
//
// Idempotent: replaces a previously attached profile so a daemon
// restart cycle re-wires cleanly.
func bindDefaultScheduleIO(ch *device.Channel, wp *weekprofile.ProfileDataPoint, domain string) {
	if ch == nil || wp == nil {
		return
	}
	loader := &defaultChannelLoader{ch: ch, domain: domain}
	saver := &defaultChannelSaver{ch: ch, priority: hmenum.CommandPriorityHigh}
	profile := weekprofile.NewDefault(loader, saver)
	wp.AttachSimpleProfile(profile)
}
