// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/SukramJ/go-fabric/cluster/spec"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// The opt-in thermostat surface: ThermostatRunningState and
// TemperatureSetpointHold (north.matter.thermostat_hold_and_running_state)
// and the MatterScheduleConfiguration feature
// (north.matter.thermostat_schedules), served on go-fabric's Thermostat
// server (cluster/thermo). matter.js models MSCH but does not implement it
// (packages/node/src/behaviors/thermostat/ThermostatServer.ts:82 at
// 862ad75f); go-fabric's server follows connectedhomeip's
// ThermostatClusterSchedules.cpp, and the HM mapping is recorded in
// notes/parity/by_design.md.

var _ interfaces.MatterConfiguredDeviceSource = (*Climate)(nil)

// matterExtras is what a Climate's Thermostat serves beyond the base
// surface, decided once when the device is built.
type matterExtras struct {
	// hold serves TemperatureSetpointHold: Off while the device follows
	// its week program (HM AUTO), On otherwise.
	hold bool
	// running serves ThermostatRunningState from [Climate.Activity].
	running bool
	// schedules serves MSCH from the channel's climate week profile.
	schedules bool
	// writer writes a committed week program to the CCU; nil refuses.
	writer interfaces.ClimateScheduleWriter
}

const (
	// matterScheduleTransitionsPerDay is the CCU's slot count per weekday
	// (schedule.MaxClimateSlots): each slot starts one transition, so a
	// day read from the CCU never carries more.
	matterScheduleTransitionsPerDay = schedule.MaxClimateSlots
	// matterScheduleTransitions bounds one schedule: seven weekdays of
	// matterScheduleTransitionsPerDay each, the case where no two
	// weekdays share their transitions.
	matterScheduleTransitions = 7 * matterScheduleTransitionsPerDay
	// matterScheduleLoadTimeout bounds the one week-profile read the
	// Matter projection starts when nothing loaded the profile yet.
	matterScheduleLoadTimeout = 30 * time.Second
)

// matterWeekdays pairs every HM weekday with its ScheduleDayOfWeekBitmap
// bit, in the bitmap's order (Sunday is bit 0 —
// thermostat-cluster.element.ts ScheduleDayOfWeekBitmap, as the generated
// definition spells it).
var matterWeekdays = [7]struct {
	day schedule.Weekday
	bit thermostatspec.ScheduleDayOfWeekBitmap
}{
	{schedule.WeekdaySunday, thermostatspec.ScheduleDayOfWeekSunday},
	{schedule.WeekdayMonday, thermostatspec.ScheduleDayOfWeekMonday},
	{schedule.WeekdayTuesday, thermostatspec.ScheduleDayOfWeekTuesday},
	{schedule.WeekdayWednesday, thermostatspec.ScheduleDayOfWeekWednesday},
	{schedule.WeekdayThursday, thermostatspec.ScheduleDayOfWeekThursday},
	{schedule.WeekdayFriday, thermostatspec.ScheduleDayOfWeekFriday},
	{schedule.WeekdaySaturday, thermostatspec.ScheduleDayOfWeekSaturday},
}

// matterExtrasFor decides what the device built under build serves. The
// hold needs a device that can follow a week program (an AUTO mode), the
// running state a wire source for the activity, and the schedules a
// climate week profile with at least one program.
func (c *Climate) matterExtrasFor(build interfaces.MatterBuildOptions) *matterExtras {
	x := &matterExtras{}
	if build.ThermostatHoldAndRunningState {
		x.hold = c.Capabilities.SupportsAuto
		x.running = c.HasActivitySource()
	}
	if build.ThermostatSchedules {
		if wp := c.climateWeekProfile(); wp != nil {
			x.schedules = true
			x.writer = build.ClimateSchedules
		}
	}
	return x
}

// matterChannelAddress is the climate channel's address, for the log.
func (c *Climate) matterChannelAddress() string {
	if c.channelRef == nil {
		return ""
	}
	return c.channelRef.Address
}

// matterDeviceAddress is the address of the device the climate channel
// belongs to, the one the week-program writer resolves its schedule
// channel from.
func (c *Climate) matterDeviceAddress() string {
	if c.channelRef == nil || c.channelRef.Device() == nil {
		return ""
	}
	return c.channelRef.Device().Address
}

// climateWeekProfile is the channel's week profile when it carries climate
// programs, nil otherwise.
func (c *Climate) climateWeekProfile() *weekprofile.ProfileDataPoint {
	if c.channelRef == nil {
		return nil
	}
	wp := c.channelRef.WeekProfile()
	if wp == nil || wp.Climate() == nil || wp.ProfileCount() < 1 {
		return nil
	}
	return wp
}

// matterExtrasServed is what the built device serves; the zero value
// before a build.
func (c *Climate) matterExtrasServed() matterExtras {
	if x := c.matterServes.Load(); x != nil {
		return *x
	}
	return matterExtras{}
}

// applyMatterExtras adds the opt-in attributes and the schedule port to the
// Thermostat declaration.
func (c *Climate) applyMatterExtras(x *matterExtras, decl *thermostatspec.Declare) {
	if x.hold {
		h := c.matterHold()
		decl.Initial.TemperatureSetpointHold = &h
	}
	if x.running {
		r := c.matterRunningState()
		decl.Initial.ThermostatRunningState = &r
	}
	if !x.schedules {
		return
	}
	wp := c.climateWeekProfile()
	n := uint8(min(wp.ProfileCount(), 255)) //nolint:gosec // bounded to uint8 on the line
	decl.Features |= thermostatspec.FeatureMatterScheduleConfiguration
	types := []thermostatspec.ScheduleTypeStruct{{
		SystemMode:           thermostatspec.SystemModeHeat,
		NumberOfSchedules:    n,
		ScheduleTypeFeatures: thermostatspec.ScheduleTypeFeaturesSupportsSetpoints,
	}}
	if decl.Features&thermostatspec.FeatureCooling != 0 {
		types = append(types, thermostatspec.ScheduleTypeStruct{
			SystemMode:           thermostatspec.SystemModeCool,
			NumberOfSchedules:    n,
			ScheduleTypeFeatures: thermostatspec.ScheduleTypeFeaturesSupportsSetpoints,
		})
	}
	total, perDay := uint8(matterScheduleTransitions), uint8(matterScheduleTransitionsPerDay)
	list, active := c.matterScheduleState(wp)
	decl.Initial.ScheduleTypes = &types
	decl.Initial.NumberOfSchedules = &n
	decl.Initial.NumberOfScheduleTransitions = &total
	decl.Initial.NumberOfScheduleTransitionPerDay = &perDay
	decl.Initial.Schedules = &list
	if active != nil {
		decl.Initial.ActiveScheduleHandle = &active
	}
	decl.SchedulePort = climateSchedulePort{c: c}
}

// wireMatterExtras keeps the opt-in attributes in step with the device
// once d is built: the mode recorders report the hold
// ([Climate.syncMatterModeTo]), the activity recorder the running state,
// and the week profile its programs and the active one. A profile nothing
// loaded yet is read once in the background.
func (c *Climate) wireMatterExtras(x *matterExtras) {
	if !x.schedules {
		return
	}
	wp := c.climateWeekProfile()
	_ = wp.OnChange(c.syncMatterSchedules)
	cp := wp.Climate()
	_ = cp.OnChange(func(_, _ *schedule.Climate) { c.syncMatterSchedules() })
	if _, err := cp.Current(); err != nil {
		// Bounded by matterScheduleLoadTimeout; a load that lands
		// publishes through cp.OnChange above.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), matterScheduleLoadTimeout)
			defer cancel()
			if _, err := cp.Load(ctx); err != nil {
				slog.Warn("matter.thermostat.week_profile_load_failed",
					slog.String("channel", c.matterChannelAddress()), slog.String("err", err.Error()))
			}
		}()
	}
}

// matterHold is the TemperatureSetpointHold the device's mode implies:
// Off while it follows its week program (HM AUTO), On in every other mode.
func (c *Climate) matterHold() thermostatspec.TemperatureSetpointHoldEnum {
	if m, ok := c.Mode(); ok && m == ModeAuto {
		return thermostatspec.TemperatureSetpointHoldSetpointHoldOff
	}
	return thermostatspec.TemperatureSetpointHoldSetpointHoldOn
}

// matterRunningState is the RelayStateBitmap the device's activity
// implies: Heat while it heats, Cool while it cools, nothing otherwise.
func (c *Climate) matterRunningState() thermostatspec.RelayStateBitmap {
	a, _ := c.Activity()
	switch a {
	case ActivityHeating:
		return thermostatspec.RelayStateHeat
	case ActivityCooling:
		return thermostatspec.RelayStateCool
	default:
		return 0
	}
}

// thermostatExtrasInbound is the inbound side of the opt-in attributes on
// the kit's Thermostat server: values the device took on itself, stored
// and reported without the Sink or the port.
type thermostatExtrasInbound interface {
	SetTemperatureSetpointHold(hold uint8)
	SetThermostatRunningState(state uint16)
	SetSchedules(schedules []thermostatspec.ScheduleStruct) error
	SetActiveScheduleHandle(handle []byte) error
}

// matterExtrasInbound is the built device's inbound side for the opt-in
// attributes, nil before a build or for a server without it.
func (c *Climate) matterExtrasInbound() thermostatExtrasInbound {
	return thermostatExtrasInboundOf(c.matterDevice.Current())
}

func thermostatExtrasInboundOf(d *fabricdevice.Device) thermostatExtrasInbound {
	if d == nil {
		return nil
	}
	srv, _ := d.Server(thermostatspec.ClusterID).(thermostatExtrasInbound)
	return srv
}

// syncMatterHold stores the hold the device's mode implies on srv, when
// the device serves it.
func (c *Climate) syncMatterHold(srv thermostatInbound) {
	if !c.matterExtrasServed().hold {
		return
	}
	if in, ok := srv.(thermostatExtrasInbound); ok {
		in.SetTemperatureSetpointHold(uint8(c.matterHold()))
	}
}

// syncMatterRunningState stores the running state the device's activity
// implies, when the built device serves it.
func (c *Climate) syncMatterRunningState() {
	if !c.matterExtrasServed().running {
		return
	}
	if in := c.matterExtrasInbound(); in != nil {
		in.SetThermostatRunningState(uint16(c.matterRunningState()))
	}
}

// syncMatterSchedules stores the week programs and the active one, when
// the built device serves them: after a profile load or save, a pointer
// change, and a hybrid device's heating/cooling switch (which moves every
// program between the Heat and Cool schedule types).
func (c *Climate) syncMatterSchedules() {
	if !c.matterExtrasServed().schedules {
		return
	}
	in := c.matterExtrasInbound()
	wp := c.climateWeekProfile()
	if in == nil || wp == nil {
		return
	}
	list, active := c.matterScheduleState(wp)
	if err := in.SetSchedules(list); err != nil {
		slog.Warn("matter.thermostat.schedules_refused",
			slog.String("channel", c.matterChannelAddress()), slog.String("err", err.Error()))
		return
	}
	if err := in.SetActiveScheduleHandle(active); err != nil {
		slog.Warn("matter.thermostat.active_schedule_refused",
			slog.String("channel", c.matterChannelAddress()), slog.String("err", err.Error()))
	}
}

// matterScheduleState is the Schedules list of the loaded week profile and
// the handle of the active program, nil when that program is not in the
// list. Nothing loaded yet is an empty list.
func (c *Climate) matterScheduleState(wp *weekprofile.ProfileDataPoint) (list []thermostatspec.ScheduleStruct, active []byte) {
	list = []thermostatspec.ScheduleStruct{}
	sched, err := wp.Climate().Current()
	if err != nil || sched == nil {
		return list, nil
	}
	mode := c.matterScheduleMode()
	for _, key := range wp.AvailableProfiles() {
		p := sched.Profiles[key]
		if p == nil {
			continue
		}
		h, ok := matterScheduleHandle(key)
		if !ok {
			continue
		}
		sc, ok := matterScheduleFrom(h, p, mode)
		if ok {
			list = append(list, sc)
		}
	}
	h, ok := matterScheduleHandle(wp.CurrentProfile())
	if !ok || !slices.ContainsFunc(list, func(sc thermostatspec.ScheduleStruct) bool { return slices.Equal(sc.ScheduleHandle.Value, h) }) {
		return list, nil
	}
	return list, h
}

// matterScheduleMode is the SystemMode the week programs run in: Cool on a
// hybrid device that cools right now, Heat otherwise — HM has one program
// set for both directions.
func (c *Climate) matterScheduleMode() thermostatspec.SystemModeEnum {
	if c.matterThermoFeatures()&thermostatspec.FeatureCooling != 0 && !c.IsHeating() {
		return thermostatspec.SystemModeCool
	}
	return thermostatspec.SystemModeHeat
}

// matterScheduleHandle is the ScheduleHandle of a week-profile key: the
// program number as one byte ("P3" is 0x03).
func matterScheduleHandle(key string) ([]byte, bool) {
	if len(key) < 2 || key[0] != 'P' {
		return nil, false
	}
	n, err := strconv.Atoi(key[1:])
	if err != nil || n < 1 || n > 255 {
		return nil, false
	}
	return []byte{byte(n)}, true
}

// matterScheduleKey is the week-profile key of a ScheduleHandle.
func matterScheduleKey(h []byte) (string, bool) {
	if len(h) != 1 || h[0] == 0 {
		return "", false
	}
	return fmt.Sprintf("P%d", h[0]), true
}

// climateSegment is one stretch of a weekday at one temperature, from
// start (minutes after midnight) to the next segment's start.
type climateSegment struct {
	start int
	temp  float64
}

// climateDaySegments is a weekday as consecutive segments from 00:00: the
// periods at their temperature, every gap at the base temperature (the
// form [weekprofile] reads a CCU day into), neighbours of equal
// temperature merged. It is false for a period whose times do not parse
// or that overlaps the one before.
func climateDaySegments(wd schedule.ClimateWeekday) ([]climateSegment, bool) {
	type period struct {
		start, end int
		temp       float64
	}
	ps := make([]period, 0, len(wd.Periods))
	for _, p := range wd.Periods {
		s, err1 := schedule.ParseClimateTime(p.StartTime)
		e, err2 := schedule.ParseClimateTime(p.EndTime)
		if err1 != nil || err2 != nil || s >= e {
			return nil, false
		}
		ps = append(ps, period{s, e, p.Temperature})
	}
	slices.SortFunc(ps, func(a, b period) int { return a.start - b.start })
	var segs []climateSegment
	add := func(start int, temp float64) {
		if n := len(segs); n > 0 && segs[n-1].temp == temp {
			return
		}
		segs = append(segs, climateSegment{start, temp})
	}
	at := 0
	for _, p := range ps {
		if p.start < at {
			return nil, false
		}
		if p.start > at {
			add(at, wd.BaseTemperature)
		}
		add(p.start, p.temp)
		at = p.end
	}
	if at < schedule.ClimateEndOfDayMinutes {
		add(at, wd.BaseTemperature)
	}
	return segs, true
}

// matterScheduleFrom is one week program as a built-in Schedule in mode:
// a transition at the start of every segment of every weekday, carrying
// the segment's temperature as the mode's setpoint. Weekdays with the same
// segments share their transitions under one DayOfWeek bitmap. It is false
// for a program with an unreadable weekday or none at all.
func matterScheduleFrom(handle []byte, p *schedule.ClimateProfile, mode thermostatspec.SystemModeEnum) (thermostatspec.ScheduleStruct, bool) {
	type group struct {
		days thermostatspec.ScheduleDayOfWeekBitmap
		segs []climateSegment
	}
	var groups []group
	for _, wd := range matterWeekdays {
		day, ok := p.Days[wd.day]
		if !ok {
			continue
		}
		segs, ok := climateDaySegments(day)
		if !ok {
			return thermostatspec.ScheduleStruct{}, false
		}
		i := slices.IndexFunc(groups, func(g group) bool { return slices.Equal(g.segs, segs) })
		if i < 0 {
			groups = append(groups, group{segs: segs})
			i = len(groups) - 1
		}
		groups[i].days |= wd.bit
	}
	if len(groups) == 0 {
		return thermostatspec.ScheduleStruct{}, false
	}
	var transitions []thermostatspec.ScheduleTransitionStruct
	for _, g := range groups {
		for _, s := range g.segs {
			t := thermostatspec.ScheduleTransitionStruct{DayOfWeek: g.days, TransitionTime: uint16(s.start)} //nolint:gosec // a segment starts within the day
			v := celsiusToMatter(s.temp)
			if mode == thermostatspec.SystemModeCool {
				t.CoolingSetpoint = &v
			} else {
				t.HeatingSetpoint = &v
			}
			transitions = append(transitions, t)
		}
	}
	return thermostatspec.ScheduleStruct{
		ScheduleHandle: spec.ValueOf(slices.Clone(handle)),
		SystemMode:     mode,
		Transitions:    transitions,
		BuiltIn:        spec.ValueOf(true),
	}, true
}

// climateProfileFromMatter is a committed Schedule as the week program the
// CCU stores: every weekday from 00:00 to 24:00 in segments, a day that
// does not start with a transition carrying the setpoint the week left
// off at (the last transition of the nearest earlier day, Saturday before
// Sunday). The error carries the status the commit answers with.
func climateProfileFromMatter(sc thermostatspec.ScheduleStruct) (hmapi.ClimateProfile, error) {
	constraint := func(reason string) error {
		return spec.Errorf(im.StatusConstraintError, "climate: schedule: %s", reason)
	}
	type point struct {
		at   int
		temp float64
	}
	var days [7][]point
	for _, t := range sc.Transitions {
		if t.DayOfWeek&thermostatspec.ScheduleDayOfWeekAway != 0 {
			return hmapi.ClimateProfile{}, constraint("the CCU has no away day")
		}
		if t.SystemMode != nil && *t.SystemMode != sc.SystemMode {
			return hmapi.ClimateProfile{}, constraint("a transition switches the SystemMode")
		}
		sp := t.HeatingSetpoint
		if sc.SystemMode == thermostatspec.SystemModeCool {
			sp = t.CoolingSetpoint
		}
		if sp == nil {
			return hmapi.ClimateProfile{}, constraint("a transition carries no setpoint for its SystemMode")
		}
		for i, wd := range matterWeekdays {
			if t.DayOfWeek&wd.bit != 0 {
				days[i] = append(days[i], point{int(t.TransitionTime), matterToCelsius(*sp)})
			}
		}
	}
	var carry float64
	found := false
	for back := 6; back >= 0 && !found; back-- {
		if n := len(days[back]); n > 0 {
			last := slices.MaxFunc(days[back], func(a, b point) int { return a.at - b.at })
			carry, found = last.temp, true
		}
	}
	if !found {
		return hmapi.ClimateProfile{}, constraint("the schedule has no transition")
	}
	out := hmapi.ClimateProfile{Weekdays: make(map[string]hmapi.ClimateWeekday, len(matterWeekdays))}
	for i, wd := range matterWeekdays {
		pts := slices.Clone(days[i])
		slices.SortStableFunc(pts, func(a, b point) int { return a.at - b.at })
		var segs []climateSegment
		add := func(start int, temp float64) {
			if n := len(segs); n > 0 && segs[n-1].start == start {
				segs[n-1].temp = temp
				return
			}
			if n := len(segs); n > 0 && segs[n-1].temp == temp {
				return
			}
			segs = append(segs, climateSegment{start, temp})
		}
		if len(pts) == 0 || pts[0].at > 0 {
			add(0, carry)
		}
		for _, pt := range pts {
			add(pt.at, pt.temp)
		}
		if len(segs) > matterScheduleTransitionsPerDay {
			return hmapi.ClimateProfile{}, spec.Errorf(im.StatusResourceExhausted,
				"climate: schedule: %s needs %d slots, the CCU stores %d", wd.day, len(segs), matterScheduleTransitionsPerDay)
		}
		carry = segs[len(segs)-1].temp
		out.Weekdays[string(wd.day)] = climateWeekdayOf(segs)
	}
	return out, nil
}

// climateWeekdayOf is a day's segments as a weekday covering 00:00 to
// 24:00, its base temperature the one held longest
// ([schedule.IdentifyBaseTemperatureFromSegments]).
func climateWeekdayOf(segs []climateSegment) hmapi.ClimateWeekday {
	ts := make([]schedule.TempSegment, 0, len(segs))
	periods := make([]hmapi.ClimatePeriod, 0, len(segs))
	for i, s := range segs {
		end := schedule.ClimateEndOfDayMinutes
		if i+1 < len(segs) {
			end = segs[i+1].start
		}
		ts = append(ts, schedule.TempSegment{StartMin: s.start, EndMin: end, Temperature: s.temp})
		start, _ := schedule.FormatClimateTime(s.start)
		stop, _ := schedule.FormatClimateTime(end)
		periods = append(periods, hmapi.ClimatePeriod{StartTime: start, EndTime: stop, Temperature: s.temp})
	}
	return hmapi.ClimateWeekday{
		BaseTemperature: schedule.IdentifyBaseTemperatureFromSegments(ts),
		Periods:         periods,
	}
}

// climateSchedulePort is the [thermostatspec.SchedulePort] of a Climate: a
// controller's SetActiveScheduleRequest becomes [Climate.SetProfile], a
// committed Schedules list writes every program it changed to the CCU.
type climateSchedulePort struct{ c *Climate }

// SetActiveSchedule implements [thermostatspec.SchedulePort]: the program
// becomes the active profile, which also brings the device into AUTO
// ([Climate.SetProfile]); the server stores the handle once this returns.
func (p climateSchedulePort) SetActiveSchedule(ctx context.Context, handle []byte) error {
	key, ok := matterScheduleKey(handle)
	if !ok {
		return spec.Errorf(im.StatusNotFound, "climate: schedule handle %x is no week program", handle)
	}
	return p.c.SetProfile(ctx, Profile(ProfilePrefix+key[1:]), matterDispatchPriority)
}

// CommitSchedules implements [thermostatspec.SchedulePort]: every program
// whose transitions differ from the stored one is written to the CCU, one
// program per write; the server stores the list once this returns. A
// write that fails refuses the rest, and programs written before it stay
// written — the CCU reports the change and the next profile load puts the
// stored list in step with the device.
func (p climateSchedulePort) CommitSchedules(ctx context.Context, list []thermostatspec.ScheduleStruct) error {
	x := p.c.matterExtrasServed()
	if x.writer == nil {
		return spec.Errorf(im.StatusUnsupportedWrite, "climate: no week-program writer")
	}
	wp := p.c.climateWeekProfile()
	if wp == nil {
		return spec.Errorf(im.StatusInvalidInState, "climate: no week profile")
	}
	stored, _ := p.c.matterScheduleState(wp)
	for _, sc := range list {
		if sc.ScheduleHandle.Null {
			return spec.Errorf(im.StatusConstraintError, "climate: schedule without a handle")
		}
		key, ok := matterScheduleKey(sc.ScheduleHandle.Value)
		if !ok || !slices.Contains(wp.AvailableProfiles(), key) {
			return spec.Errorf(im.StatusConstraintError, "climate: schedule handle %x is no week program", sc.ScheduleHandle.Value)
		}
		i := slices.IndexFunc(stored, func(s thermostatspec.ScheduleStruct) bool {
			return slices.Equal(s.ScheduleHandle.Value, sc.ScheduleHandle.Value)
		})
		if i >= 0 && stored[i].SystemMode == sc.SystemMode && slices.EqualFunc(stored[i].Transitions, sc.Transitions, equalTransition) {
			continue
		}
		prof, err := climateProfileFromMatter(sc)
		if err != nil {
			return err
		}
		if err := x.writer.PutClimateProfile(ctx, p.c.matterDeviceAddress(), key, prof); err != nil {
			return fmt.Errorf("climate: write %s: %w", key, err)
		}
	}
	return nil
}

// equalTransition reports whether two transitions carry the same values.
func equalTransition(a, b thermostatspec.ScheduleTransitionStruct) bool {
	return a.DayOfWeek == b.DayOfWeek && a.TransitionTime == b.TransitionTime &&
		equalOpt(a.HeatingSetpoint, b.HeatingSetpoint) && equalOpt(a.CoolingSetpoint, b.CoolingSetpoint) &&
		equalOpt(a.SystemMode, b.SystemMode) && equalOptBytes(a.PresetHandle, b.PresetHandle)
}

func equalOpt[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalOptBytes(a, b *[]byte) bool {
	if a == nil || b == nil {
		return a == b
	}
	return slices.Equal(*a, *b)
}
