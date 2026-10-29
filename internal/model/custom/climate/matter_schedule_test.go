// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster/spec"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	"github.com/SukramJ/go-fabric/cluster/thermo"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// climateLoader is a week-profile loader that answers with a fixed
// schedule, counting its reads.
type climateLoader struct {
	mu    sync.Mutex
	sched *schedule.Climate
	loads int
}

func (l *climateLoader) Load(context.Context) (*schedule.Climate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads++
	return l.sched, nil
}

// profileWriter records the week programs written to the CCU.
type profileWriter struct {
	mu     sync.Mutex
	writes map[string]hmapi.ClimateProfile
	err    error
}

func (w *profileWriter) PutClimateProfile(_ context.Context, _, profile string, p hmapi.ClimateProfile) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.writes == nil {
		w.writes = map[string]hmapi.ClimateProfile{}
	}
	w.writes[profile] = p
	return nil
}

// day is a weekday at base with one warmer period.
func day(base float64, start, end string, temp float64) schedule.ClimateWeekday {
	return schedule.ClimateWeekday{BaseTemperature: base, Periods: []schedule.ClimatePeriod{{StartTime: start, EndTime: end, Temperature: temp}}}
}

// twoProgramSchedule is P1 with a working-week and a weekend shape and
// P2 flat at 18 °C, in the form weekprofile reads a CCU paramset into.
func twoProgramSchedule() *schedule.Climate {
	p1 := &schedule.ClimateProfile{Days: map[schedule.Weekday]schedule.ClimateWeekday{}}
	for _, d := range []schedule.Weekday{schedule.WeekdayMonday, schedule.WeekdayTuesday, schedule.WeekdayWednesday, schedule.WeekdayThursday, schedule.WeekdayFriday} {
		p1.Days[d] = day(17, "06:00", "22:00", 21)
	}
	p1.Days[schedule.WeekdaySaturday] = day(17, "08:00", "23:00", 21.5)
	p1.Days[schedule.WeekdaySunday] = day(17, "08:00", "23:00", 21.5)
	p2 := &schedule.ClimateProfile{Days: map[schedule.Weekday]schedule.ClimateWeekday{}}
	for _, wd := range matterWeekdays {
		p2.Days[wd.day] = schedule.ClimateWeekday{BaseTemperature: 18}
	}
	return &schedule.Climate{Profiles: map[string]*schedule.ClimateProfile{"P1": p1, "P2": p2}}
}

// scheduleRig is an IP thermostat whose channel carries a two-program
// climate week profile behind loader.
func scheduleRig(t *testing.T, loader *climateLoader, loaded bool) (*rig, *putWriter, *weekprofile.ProfileDataPoint) {
	t.Helper()
	w := &putWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{SupportsAuto: true, SupportsHeat: true, SupportsProfile: true})
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		ChannelAddress: "HmIP-BWTH:1", ScheduleType: weekprofile.ScheduleTypeClimate,
		MinTemp: 5, MaxTemp: 30, ProfileCount: 2,
	})
	wp.AttachClimateProfile(weekprofile.NewClimate(loader, nil))
	r.channel.AttachWeekProfile(wp)
	if loaded {
		if _, err := wp.Climate().Load(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return r, w, wp
}

func buildWith(t *testing.T, c *Climate, build interfaces.MatterBuildOptions) *thermo.ThermostatServer {
	t.Helper()
	d, err := c.MatterDeviceWith(fabricdevice.Options{StableKey: fabricdevice.StringKey("climate-test"), Name: "climate"}, build)
	if err != nil {
		t.Fatalf("MatterDeviceWith: %v", err)
	}
	srv, ok := d.Server(thermostatspec.ClusterID).(*thermo.ThermostatServer)
	if !ok {
		t.Fatal("no Thermostat server")
	}
	return srv
}

var schedulesOn = interfaces.MatterBuildOptions{ThermostatHoldAndRunningState: true, ThermostatSchedules: true}

// TestMatterHoldAndRunningStateAloneServeNoSchedules pins the split: the
// hold flag serves the hold and the running state and leaves MSCH off.
func TestMatterHoldAndRunningStateAloneServeNoSchedules(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, interfaces.MatterBuildOptions{ThermostatHoldAndRunningState: true})
	for _, attr := range []uint32{thermostatspec.AttrTemperatureSetpointHold, thermostatspec.AttrThermostatRunningState} {
		if !slices.Contains(srv.MatterAttributes(), attr) {
			t.Errorf("attribute 0x%04X not served with the hold flag", attr)
		}
	}
	if slices.Contains(srv.MatterAttributes(), thermostatspec.AttrSchedules) {
		t.Error("Schedules served with the hold flag alone")
	}
	if fm := readFeatureMap(t, srv); fm&uint32(thermostatspec.FeatureMatterScheduleConfiguration) != 0 {
		t.Errorf("FeatureMap 0x%X carries MSCH with the hold flag alone", fm)
	}
}

// TestMatterSchedulesAloneServeNoHold pins the other half: the schedules
// flag serves MSCH and neither the hold nor the running state.
func TestMatterSchedulesAloneServeNoHold(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, interfaces.MatterBuildOptions{ThermostatSchedules: true})
	if fm := readFeatureMap(t, srv); fm&uint32(thermostatspec.FeatureMatterScheduleConfiguration) == 0 {
		t.Errorf("FeatureMap 0x%X lacks MSCH with the schedules flag", fm)
	}
	for _, attr := range []uint32{thermostatspec.AttrTemperatureSetpointHold, thermostatspec.AttrThermostatRunningState} {
		if slices.Contains(srv.MatterAttributes(), attr) {
			t.Errorf("attribute 0x%04X served with the schedules flag alone", attr)
		}
	}
}

// TestMatterThermostatSchedulesOffServesNoneOfIt pins the opt-in: built
// without it, the Thermostat serves neither the hold, nor the running
// state, nor MSCH, whatever the channel carries.
func TestMatterThermostatSchedulesOffServesNoneOfIt(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, interfaces.MatterBuildOptions{})
	for _, attr := range []uint32{thermostatspec.AttrTemperatureSetpointHold, thermostatspec.AttrThermostatRunningState, thermostatspec.AttrSchedules, thermostatspec.AttrActiveScheduleHandle} {
		if slices.Contains(srv.MatterAttributes(), attr) {
			t.Errorf("attribute 0x%04X served without the opt-in", attr)
		}
	}
	if fm := readFeatureMap(t, srv); fm&uint32(thermostatspec.FeatureMatterScheduleConfiguration) != 0 {
		t.Errorf("FeatureMap 0x%X carries MSCH without the opt-in", fm)
	}
}

func readFeatureMap(t *testing.T, srv *thermo.ThermostatServer) uint32 {
	t.Helper()
	v, ok := srv.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("FeatureMap not served")
	}
	fm, ok := v.(uint32)
	if !ok {
		t.Fatalf("FeatureMap read %T", v)
	}
	return fm
}

// TestMatterThermostatSchedulesServesTheWeekPrograms pins the projection
// of a loaded week profile: one built-in Heat schedule per program, its
// handle the program number, weekdays with the same day sharing one
// bitmap, a transition at every temperature change, and the active
// program as the active handle.
func TestMatterThermostatSchedulesServesTheWeekPrograms(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, schedulesOn)

	if fm := readFeatureMap(t, srv); fm&uint32(thermostatspec.FeatureMatterScheduleConfiguration) == 0 {
		t.Fatalf("FeatureMap 0x%X lacks MSCH", fm)
	}
	list := srv.Schedules()
	if len(list) != 2 {
		t.Fatalf("Schedules = %d, want 2", len(list))
	}
	p1 := list[0]
	if !slices.Equal(p1.ScheduleHandle.Value, []byte{1}) || p1.SystemMode != thermostatspec.SystemModeHeat || !p1.BuiltIn.Value {
		t.Fatalf("P1 = handle %x mode %d builtIn %v", p1.ScheduleHandle.Value, p1.SystemMode, p1.BuiltIn)
	}
	weekend := thermostatspec.ScheduleDayOfWeekSunday | thermostatspec.ScheduleDayOfWeekSaturday
	workdays := thermostatspec.ScheduleDayOfWeekMonday | thermostatspec.ScheduleDayOfWeekTuesday | thermostatspec.ScheduleDayOfWeekWednesday |
		thermostatspec.ScheduleDayOfWeekThursday | thermostatspec.ScheduleDayOfWeekFriday
	want := []struct {
		days thermostatspec.ScheduleDayOfWeekBitmap
		at   uint16
		sp   int16
	}{
		{weekend, 0, 1700},
		{weekend, 8 * 60, 2150},
		{weekend, 23 * 60, 1700},
		{workdays, 0, 1700},
		{workdays, 6 * 60, 2100},
		{workdays, 22 * 60, 1700},
	}
	if len(p1.Transitions) != len(want) {
		t.Fatalf("P1 transitions = %+v", p1.Transitions)
	}
	for i, w := range want {
		got := p1.Transitions[i]
		if got.DayOfWeek != w.days || got.TransitionTime != w.at || got.HeatingSetpoint == nil || *got.HeatingSetpoint != w.sp || got.CoolingSetpoint != nil {
			t.Errorf("P1 transition %d = %+v, want %+v", i, got, w)
		}
	}
	if p2 := list[1]; len(p2.Transitions) != 1 || p2.Transitions[0].DayOfWeek != 0x7F || *p2.Transitions[0].HeatingSetpoint != 1800 {
		t.Errorf("P2 = %+v, want one all-week transition at 18 °C", p2.Transitions)
	}
	if got := srv.ActiveScheduleHandle(); !slices.Equal(got, []byte{1}) {
		t.Errorf("ActiveScheduleHandle = %x, want 01", got)
	}
}

// TestMatterThermostatSchedulesFollowThePointerAndTheProfile pins the
// inbound side: the active handle follows the device's profile pointer,
// and a profile the CCU reports changed replaces the list.
func TestMatterThermostatSchedulesFollowThePointerAndTheProfile(t *testing.T) {
	loader := &climateLoader{sched: twoProgramSchedule()}
	r, _, wp := scheduleRig(t, loader, true)
	srv := buildWith(t, r.climate, schedulesOn)

	if err := wp.SetCurrentProfile("P2"); err != nil {
		t.Fatal(err)
	}
	if got := srv.ActiveScheduleHandle(); !slices.Equal(got, []byte{2}) {
		t.Fatalf("ActiveScheduleHandle after the pointer moved = %x, want 02", got)
	}

	changed := twoProgramSchedule()
	changed.Profiles["P2"].Days[schedule.WeekdayMonday] = day(18, "07:00", "09:00", 22)
	loader.mu.Lock()
	loader.sched = changed
	loader.mu.Unlock()
	if _, err := wp.Climate().Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	p2 := srv.Schedules()[1]
	if len(p2.Transitions) != 4 {
		t.Fatalf("P2 after the reload = %+v, want Monday's three and the rest's one", p2.Transitions)
	}
}

// TestMatterThermostatSchedulesLoadAProfileNothingLoaded pins the one
// background read: without MQTT nothing loads the week profile, so the
// projection does, and the programs arrive once it lands.
func TestMatterThermostatSchedulesLoadAProfileNothingLoaded(t *testing.T) {
	loader := &climateLoader{sched: twoProgramSchedule()}
	r, _, _ := scheduleRig(t, loader, false)
	srv := buildWith(t, r.climate, schedulesOn)
	deadline := time.Now().Add(5 * time.Second)
	for len(srv.Schedules()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("Schedules = %d after the background load, want 2", len(srv.Schedules()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	loader.mu.Lock()
	defer loader.mu.Unlock()
	if loader.loads != 1 {
		t.Errorf("loads = %d, want 1", loader.loads)
	}
}

// TestMatterHoldFollowsTheMode pins the hold as the projection of HM
// AUTO: Off while the device follows its week program, On otherwise.
func TestMatterHoldFollowsTheMode(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, schedulesOn)
	r.climate.OnMode(ModeAuto)
	if got := readU8(t, srv, thermostatspec.AttrTemperatureSetpointHold); got != uint8(thermostatspec.TemperatureSetpointHoldSetpointHoldOff) {
		t.Fatalf("hold in AUTO = %d, want Off", got)
	}
	r.climate.OnMode(ModeHeat)
	if got := readU8(t, srv, thermostatspec.AttrTemperatureSetpointHold); got != uint8(thermostatspec.TemperatureSetpointHoldSetpointHoldOn) {
		t.Fatalf("hold in MANU = %d, want On", got)
	}
}

// TestMatterHoldWriteSetsTheMode pins the controller's side: a hold write
// becomes the HM mode, AUTO for Off and MANU for On.
func TestMatterHoldWriteSetsTheMode(t *testing.T) {
	r, w, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, schedulesOn)
	r.climate.OnMode(ModeHeat)
	if err := srv.MatterWrite(context.Background(), thermostatspec.AttrTemperatureSetpointHold, uint8(thermostatspec.TemperatureSetpointHoldSetpointHoldOff)); err != nil {
		t.Fatalf("hold write: %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.puts) == 0 || fmt.Sprint(w.puts[len(w.puts)-1][string(hmenum.ParameterControlMode)]) != "0" {
		t.Fatalf("puts = %v, want CONTROL_MODE 0 (AUTO)", w.puts)
	}
}

// TestMatterRunningStateFollowsTheActivity pins the relay bitmap: Heat
// while the device heats, nothing while it idles.
func TestMatterRunningStateFollowsTheActivity(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, schedulesOn)
	r.climate.OnActivity(ActivityHeating)
	if v, _ := srv.MatterRead(thermostatspec.AttrThermostatRunningState); v != uint16(thermostatspec.RelayStateHeat) {
		t.Fatalf("running state while heating = %v, want Heat", v)
	}
	r.climate.OnActivity(ActivityIdle)
	if v, _ := srv.MatterRead(thermostatspec.AttrThermostatRunningState); v != uint16(0) {
		t.Fatalf("running state while idle = %v, want 0", v)
	}
}

// TestMatterSetActiveScheduleRequestSwitchesTheProgram pins the command:
// the program a controller names becomes ACTIVE_PROFILE, with the device
// brought into AUTO.
func TestMatterSetActiveScheduleRequestSwitchesTheProgram(t *testing.T) {
	r, w, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, schedulesOn)
	if _, err := srv.MatterInvoke(context.Background(), thermostatspec.CmdSetActiveScheduleRequest,
		thermostatspec.SetActiveScheduleRequestRequest{ScheduleHandle: []byte{2}}); err != nil {
		t.Fatalf("SetActiveScheduleRequest: %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.puts) == 0 || fmt.Sprint(w.puts[len(w.puts)-1][string(hmenum.ParameterActiveProfile)]) != "2" {
		t.Fatalf("puts = %v, want ACTIVE_PROFILE 2", w.puts)
	}
	if got := srv.ActiveScheduleHandle(); !slices.Equal(got, []byte{2}) {
		t.Errorf("ActiveScheduleHandle = %x, want 02", got)
	}
}

// TestMatterCommitWritesOnlyTheChangedProgram pins the commit: the
// program whose transitions changed is written, as a full 24-hour day per
// weekday, and the unchanged one is not.
func TestMatterCommitWritesOnlyTheChangedProgram(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	pw := &profileWriter{}
	srv := buildWith(t, r.climate, interfaces.MatterBuildOptions{ThermostatSchedules: true, ClimateSchedules: pw})
	list := srv.Schedules()
	hot := int16(2300)
	list[1].Transitions = append(list[1].Transitions, thermostatspec.ScheduleTransitionStruct{
		DayOfWeek: thermostatspec.ScheduleDayOfWeekMonday, TransitionTime: 18 * 60, HeatingSetpoint: &hot,
	})
	if err := (climateSchedulePort{c: r.climate}).CommitSchedules(context.Background(), list); err != nil {
		t.Fatalf("CommitSchedules: %v", err)
	}
	if _, wrote := pw.writes["P1"]; wrote || len(pw.writes) != 1 {
		t.Fatalf("writes = %v, want P2 only", pw.writes)
	}
	mon := pw.writes["P2"].Weekdays["MONDAY"]
	want := []hmapi.ClimatePeriod{{StartTime: "00:00", EndTime: "18:00", Temperature: 18}, {StartTime: "18:00", EndTime: "24:00", Temperature: 23}}
	if !slices.Equal(mon.Periods, want) || mon.BaseTemperature != 18 {
		t.Errorf("Monday = %+v, want %+v at base 18", mon, want)
	}
	// Tuesday keeps its own 00:00 transition, so Monday's evening does
	// not carry into it.
	if tue := pw.writes["P2"].Weekdays["TUESDAY"]; !slices.Equal(tue.Periods, []hmapi.ClimatePeriod{{StartTime: "00:00", EndTime: "24:00", Temperature: 18}}) {
		t.Errorf("Tuesday = %+v, want one 18 °C day", tue)
	}
}

// TestMatterCommitRefusesWithoutAWriter pins the refusal when no writer
// was wired: nothing is stored that the CCU would not hold.
func TestMatterCommitRefusesWithoutAWriter(t *testing.T) {
	r, _, _ := scheduleRig(t, &climateLoader{sched: twoProgramSchedule()}, true)
	srv := buildWith(t, r.climate, schedulesOn)
	list := srv.Schedules()
	list[0].Transitions[0].TransitionTime = 5
	err := (climateSchedulePort{c: r.climate}).CommitSchedules(context.Background(), list)
	var se *spec.StatusError
	if !errors.As(err, &se) || se.Status != im.StatusUnsupportedWrite {
		t.Fatalf("CommitSchedules without a writer = %v, want UNSUPPORTED_WRITE", err)
	}
}

// TestClimateProfileFromMatterCarriesTheWeekAcrossMidnight pins the
// conversion back: a day without a transition at 00:00 starts at the
// setpoint the week left off at, Saturday's last for Sunday.
func TestClimateProfileFromMatterCarriesTheWeekAcrossMidnight(t *testing.T) {
	sp := func(v int16) *int16 { return &v }
	sc := thermostatspec.ScheduleStruct{
		SystemMode: thermostatspec.SystemModeHeat,
		Transitions: []thermostatspec.ScheduleTransitionStruct{
			{DayOfWeek: 0x7F, TransitionTime: 6 * 60, HeatingSetpoint: sp(2100)},
			{DayOfWeek: 0x7F, TransitionTime: 22 * 60, HeatingSetpoint: sp(1650)},
		},
	}
	got, err := climateProfileFromMatter(sc)
	if err != nil {
		t.Fatal(err)
	}
	want := []hmapi.ClimatePeriod{
		{StartTime: "00:00", EndTime: "06:00", Temperature: 16.5},
		{StartTime: "06:00", EndTime: "22:00", Temperature: 21},
		{StartTime: "22:00", EndTime: "24:00", Temperature: 16.5},
	}
	for _, wd := range matterWeekdays {
		if d := got.Weekdays[string(wd.day)]; !slices.Equal(d.Periods, want) || d.BaseTemperature != 21 {
			t.Errorf("%s = %+v, want %+v at base 21", wd.day, d, want)
		}
	}
}

// TestClimateProfileFromMatterRefusesWhatTheCCUCannotStore pins the
// refusals: an away day, and a day that needs more slots than the CCU has.
func TestClimateProfileFromMatterRefusesWhatTheCCUCannotStore(t *testing.T) {
	sp := func(v int16) *int16 { return &v }
	away := thermostatspec.ScheduleStruct{SystemMode: thermostatspec.SystemModeHeat, Transitions: []thermostatspec.ScheduleTransitionStruct{
		{DayOfWeek: thermostatspec.ScheduleDayOfWeekAway, HeatingSetpoint: sp(1800)},
	}}
	if _, err := climateProfileFromMatter(away); !hasStatus(err, im.StatusConstraintError) {
		t.Errorf("away day = %v, want CONSTRAINT_ERROR", err)
	}
	// Thirteen transitions after 00:00, each at a new setpoint, plus the
	// start carried in from the last of them need fourteen slots.
	ts := make([]thermostatspec.ScheduleTransitionStruct, 0, 13)
	for i := range 13 {
		ts = append(ts, thermostatspec.ScheduleTransitionStruct{
			DayOfWeek: thermostatspec.ScheduleDayOfWeekMonday, TransitionTime: uint16(60 + i*60), HeatingSetpoint: sp(int16(1700 + 10*i)),
		})
	}
	full := thermostatspec.ScheduleStruct{SystemMode: thermostatspec.SystemModeHeat, Transitions: ts}
	if _, err := climateProfileFromMatter(full); !hasStatus(err, im.StatusResourceExhausted) {
		t.Errorf("fourteen-slot day = %v, want RESOURCE_EXHAUSTED", err)
	}
}

func hasStatus(err error, status im.StatusCode) bool {
	var se *spec.StatusError
	return errors.As(err, &se) && se.Status == status
}
