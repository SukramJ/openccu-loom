// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ProfileDataPoint construction
// ---------------------------------------------------------------------------

func TestNewProfileDataPointClimateDefaults(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		MinTemp:      5,
		MaxTemp:      30,
		ProfileCount: 3,
	})
	if dp.ScheduleType() != ScheduleTypeClimate {
		t.Errorf("ScheduleType = %v, want climate", dp.ScheduleType())
	}
	if dp.CurrentProfile() != "P1" {
		t.Errorf("CurrentProfile = %q, want P1", dp.CurrentProfile())
	}
	if dp.MaxEntries() != maxClimateSlots {
		t.Errorf("MaxEntries = %d, want %d", dp.MaxEntries(), maxClimateSlots)
	}
	if dp.ProfileCount() != 3 {
		t.Errorf("ProfileCount = %d, want 3", dp.ProfileCount())
	}
	if dp.MinTemp() != 5 {
		t.Errorf("MinTemp = %v, want 5", dp.MinTemp())
	}
	if dp.MaxTemp() != 30 {
		t.Errorf("MaxTemp = %v, want 30", dp.MaxTemp())
	}
}

func TestNewProfileDataPointDefaultType(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	if dp.ScheduleType() != ScheduleTypeDefault {
		t.Errorf("ScheduleType = %v, want default", dp.ScheduleType())
	}
	if dp.MaxEntries() != maxSimpleEntries {
		t.Errorf("MaxEntries = %d, want %d", dp.MaxEntries(), maxSimpleEntries)
	}
	if dp.CurrentProfile() != "" {
		t.Errorf("CurrentProfile must be empty for non-climate, got %q", dp.CurrentProfile())
	}
}

func TestNewProfileDataPointProfileCountMinimumOne(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeClimate, ProfileCount: 0})
	if dp.ProfileCount() != 1 {
		t.Errorf("ProfileCount must default to 1, got %d", dp.ProfileCount())
	}
}

// ---------------------------------------------------------------------------
// AvailableProfiles
// ---------------------------------------------------------------------------

func TestAvailableProfilesClimate(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	got := dp.AvailableProfiles()
	if len(got) != 6 {
		t.Fatalf("expected 6 profiles, got %v", got)
	}
	for i, want := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		if got[i] != want {
			t.Errorf("profile[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestAvailableProfilesDefault(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	if got := dp.AvailableProfiles(); got != nil {
		t.Errorf("non-climate AvailableProfiles must be nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SetCurrentProfile
// ---------------------------------------------------------------------------

func TestSetCurrentProfileAcceptsValidKey(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	for _, key := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		if err := dp.SetCurrentProfile(key); err != nil {
			t.Errorf("SetCurrentProfile(%q): %v", key, err)
		}
		if got := dp.CurrentProfile(); got != key {
			t.Errorf("CurrentProfile = %q, want %q", got, key)
		}
	}
}

func TestSetCurrentProfileRejectsInvalidKey(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 3,
	})
	for _, bad := range []string{"P0", "P4", "X1", "", "p1"} {
		if err := dp.SetCurrentProfile(bad); err == nil {
			t.Errorf("SetCurrentProfile(%q) expected error", bad)
		}
	}
}

func TestSetCurrentProfileNonClimateErrors(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	if err := dp.SetCurrentProfile("P1"); err == nil {
		t.Fatal("SetCurrentProfile must error for non-climate")
	}
}

func TestSetCurrentProfileSameValueNoCallback(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	var count atomic.Int32
	dp.OnChange(func() { count.Add(1) })

	_ = dp.SetCurrentProfile("P1") // same as default
	if count.Load() != 0 {
		t.Errorf("callback fired for no-op change: %d", count.Load())
	}
	_ = dp.SetCurrentProfile("P2")
	if count.Load() != 1 {
		t.Errorf("callback must fire on real change: %d", count.Load())
	}
}

// ---------------------------------------------------------------------------
// SyncProfilePointer
// ---------------------------------------------------------------------------

func TestScheduleEnabledNilByDefault(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	if dp.ScheduleEnabled() != nil {
		t.Error("ScheduleEnabled must be nil when no channels registered")
	}
}

func TestRegisterAndSetScheduleEnabled(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	dp.RegisterChannel("1_2", false)

	state := dp.ScheduleEnabled()
	if state == nil {
		t.Fatal("ScheduleEnabled must not be nil after RegisterChannel")
	}
	if !state["1_1"] {
		t.Error("1_1 must be enabled")
	}
	if state["1_2"] {
		t.Error("1_2 must be disabled")
	}

	_ = dp.SetScheduleEnabled(context.Background(), "1_1", false, hmenum.CommandPriorityHigh)
	state2 := dp.ScheduleEnabled()
	if state2["1_1"] {
		t.Error("1_1 must now be disabled")
	}
}

func TestSetScheduleEnabledAllChannels(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", false)
	dp.RegisterChannel("2_1", false)

	_ = dp.SetScheduleEnabled(context.Background(), "", true, hmenum.CommandPriorityHigh) // empty key = all channels
	for key, enabled := range dp.ScheduleEnabled() {
		if !enabled {
			t.Errorf("channel %q must be enabled after SetScheduleEnabled(\"\", true)", key)
		}
	}
}

func TestRegisterChannelIsIdempotent(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	dp.RegisterChannel("1_1", false) // second call must not overwrite
	state := dp.ScheduleEnabled()
	if !state["1_1"] {
		t.Error("second RegisterChannel call must not overwrite existing state")
	}
}

// ---------------------------------------------------------------------------
// CountClimateEntries / CountSimpleEntries
// ---------------------------------------------------------------------------

func TestCountClimateEntries(t *testing.T) {
	t.Parallel()
	c := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	_ = prof.Put(schedule.WeekdayMonday, schedule.ClimateWeekday{
		BaseTemperature: 18,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "07:00", EndTime: "22:00", Temperature: 21},
			{StartTime: "00:00", EndTime: "07:00", Temperature: 18},
			{StartTime: "22:00", EndTime: "24:00", Temperature: 18},
		},
	})
	_ = prof.Put(schedule.WeekdayTuesday, schedule.ClimateWeekday{
		BaseTemperature: 20,
	})
	_ = c.Put("P1", prof)
	// Monday has 3 periods, Tuesday has 0 → total 3.
	if got := CountClimateEntries(c); got != 3 {
		t.Errorf("CountClimateEntries = %d, want 3", got)
	}
}

func TestCountClimateEntriesNil(t *testing.T) {
	t.Parallel()
	if got := CountClimateEntries(nil); got != 0 {
		t.Errorf("expected 0 for nil, got %d", got)
	}
}

func TestCountSimpleEntries(t *testing.T) {
	t.Parallel()
	s := schedule.NewSimple()
	// Entries with target channels count as active (mirrors Python criterion).
	_ = s.Put(1, schedule.SimpleEntry{
		Weekdays:       []schedule.Weekday{schedule.WeekdayMonday},
		TargetChannels: []string{"1_1"},
		Time:           "07:00",
		Level:          1,
	})
	_ = s.Put(3, schedule.SimpleEntry{
		Weekdays:       []schedule.Weekday{schedule.WeekdayFriday},
		TargetChannels: []string{"1_2"},
		Time:           "18:00",
		Level:          0,
	})
	// Entry without target channels does not count.
	_ = s.Put(5, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdaySaturday},
		Time:     "09:00",
		Level:    1,
	})
	if got := CountSimpleEntries(s); got != 2 {
		t.Errorf("CountSimpleEntries = %d, want 2", got)
	}
}

func TestCountSimpleEntriesNil(t *testing.T) {
	t.Parallel()
	if got := CountSimpleEntries(nil); got != 0 {
		t.Errorf("expected 0 for nil, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// OnChange subscription
// ---------------------------------------------------------------------------

func TestOnChangeFiresOnProfileChange(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	var count atomic.Int32
	dp.OnChange(func() { count.Add(1) })

	_ = dp.SetCurrentProfile("P2")
	if count.Load() != 1 {
		t.Errorf("OnChange count = %d, want 1", count.Load())
	}
}

func TestOnChangeFiresOnScheduleEnabled(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)

	var count atomic.Int32
	dp.OnChange(func() { count.Add(1) })

	_ = dp.SetScheduleEnabled(context.Background(), "1_1", false, hmenum.CommandPriorityHigh)
	if count.Load() != 1 {
		t.Errorf("OnChange count = %d, want 1", count.Load())
	}
}

func TestOnChangeUnsubscribeStopsFiring(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	var count atomic.Int32
	unsub := dp.OnChange(func() { count.Add(1) })

	_ = dp.SetCurrentProfile("P2")
	unsub()
	_ = dp.SetCurrentProfile("P3")
	if count.Load() != 1 {
		t.Errorf("after unsub: count = %d, want 1", count.Load())
	}
}

func TestOnChangeUnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	unsub := dp.OnChange(func() {})
	unsub()
	unsub() // must not panic
}

// ---------------------------------------------------------------------------
// Concurrent safety
// ---------------------------------------------------------------------------

func TestProfileDataPointConcurrentSafe(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 6,
	})
	dp.RegisterChannel("1_1", true)

	var wg sync.WaitGroup
	const goroutines = 20

	for range goroutines {
		wg.Go(func() {
			for i := range 10 {
				profile := []string{"P1", "P2", "P3"}[i%3]
				_ = dp.SetCurrentProfile(profile)
				_ = dp.CurrentProfile()
				_ = dp.ScheduleEnabled()
				_ = dp.SetScheduleEnabled(context.Background(), "1_1", i%2 == 0, hmenum.CommandPriorityHigh)
			}
		})
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// ProfileDataPoint — AttachClimateProfile / AttachSimpleProfile /
// ApplyDeviceMetadata / SetCurrentProfile(missing)
// ---------------------------------------------------------------------------

func TestProfileDataPointAttachClimate(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		MinTemp:      5,
		MaxTemp:      30,
		ProfileCount: 1,
	})

	if dp.Climate() != nil {
		t.Fatal("Climate() must be nil before attach")
	}
	p := NewClimate(nil, nil)
	dp.AttachClimateProfile(p)
	if dp.Climate() != p {
		t.Fatal("Climate() must return the attached profile")
	}
	dp.AttachClimateProfile(nil)
	if dp.Climate() != nil {
		t.Fatal("AttachClimateProfile(nil) must detach")
	}
}

func TestProfileDataPointAttachSimple(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})

	if dp.Simple() != nil {
		t.Fatal("Simple() must be nil before attach")
	}
	p := NewDefault(nil, nil)
	dp.AttachSimpleProfile(p)
	if dp.Simple() != p {
		t.Fatal("Simple() must return the attached profile")
	}
	dp.AttachSimpleProfile(nil)
	if dp.Simple() != nil {
		t.Fatal("AttachSimpleProfile(nil) must detach")
	}
}

func TestProfileDataPointApplyDeviceMetadata(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		MinTemp:      5,
		MaxTemp:      30,
		ProfileCount: 2,
	})

	dp.ApplyDeviceMetadata(DeviceMetadata{MinTemp: 8, MaxTemp: 28, ProfileCount: 4})
	if dp.MinTemp() != 8 {
		t.Fatalf("MinTemp = %v, want 8", dp.MinTemp())
	}
	if dp.MaxTemp() != 28 {
		t.Fatalf("MaxTemp = %v, want 28", dp.MaxTemp())
	}
	if dp.ProfileCount() != 4 {
		t.Fatalf("ProfileCount = %d, want 4", dp.ProfileCount())
	}

	// Zero ProfileCount must not overwrite the existing value.
	dp.ApplyDeviceMetadata(DeviceMetadata{MinTemp: 6, MaxTemp: 29, ProfileCount: 0})
	if dp.ProfileCount() != 4 {
		t.Fatalf("after zero ProfileCount, ProfileCount = %d, want 4 (unchanged)", dp.ProfileCount())
	}
}

func TestProfileDataPointSetCurrentProfileMissing(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 2,
	})
	// "P99" does not exist in the 2-profile set → should return error.
	if err := dp.SetCurrentProfile("P99"); err == nil {
		t.Fatal("SetCurrentProfile with unknown key must return error")
	}
}

// ---------------------------------------------------------------------------
// ReloadSchedule
// ---------------------------------------------------------------------------

type simpleLoaderStub struct {
	calls int
	sched *schedule.Simple
	err   error
}

func (s *simpleLoaderStub) Load(_ context.Context) (*schedule.Simple, error) {
	s.calls++
	return s.sched, s.err
}

func TestReloadScheduleSimpleProfileFiresUpdated(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu1",
		ChannelAddress: "VCU0001:1",
		ScheduleType:   ScheduleTypeDefault,
	})
	stub := &simpleLoaderStub{sched: &schedule.Simple{}}
	sp := NewDefault(stub, nil)
	dp.AttachSimpleProfile(sp)

	var fired int
	dp.OnChange(func() { fired++ })

	if err := dp.ReloadSchedule(context.Background()); err != nil {
		t.Fatalf("ReloadSchedule: unexpected error: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("loader.Load: called %d times, want 1", stub.calls)
	}
	if fired == 0 {
		t.Fatal("change callback must fire after ReloadSchedule")
	}
}

func TestReloadScheduleLoaderErrorPropagates(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu1",
		ChannelAddress: "VCU0001:1",
		ScheduleType:   ScheduleTypeDefault,
	})
	stub := &simpleLoaderStub{err: errors.New("CCU not reachable")}
	dp.AttachSimpleProfile(NewDefault(stub, nil))

	if err := dp.ReloadSchedule(context.Background()); err == nil {
		t.Fatal("ReloadSchedule: expected error, got nil")
	}
}

func TestReloadScheduleNoProfileIsNoOp(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{})
	if err := dp.ReloadSchedule(context.Background()); err != nil {
		t.Fatalf("ReloadSchedule with no profile: unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SyncChannelLocksFromWire
// ---------------------------------------------------------------------------

func TestSyncChannelLocksFromWireBitDecodingUint32(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{})
	// The decode is keyed by the channels the device advertises, so the
	// targets — not RegisterChannel — are what makes the keys eligible.
	dp.SetAvailableTargetChannels(map[string]TargetChannelInfo{
		"1_1": {ChannelNo: 1, ChannelType: "primary", Bit: 0, BitKnown: true},   // bit=1
		"1_2": {ChannelNo: 2, ChannelType: "secondary", Bit: 1, BitKnown: true}, // bit=2
	})

	// bit 0 set → channel "1_1" LOCKED (enabled=false); bit 1 clear → channel "1_2" ENABLED
	dp.SyncChannelLocksFromWire(uint32(1))

	state := dp.ScheduleEnabled()
	if state["1_1"] != false {
		t.Errorf("1_1: want false (locked), got %v", state["1_1"])
	}
	if state["1_2"] != true {
		t.Errorf("1_2: want true (enabled), got %v", state["1_2"])
	}
}

func TestSyncChannelLocksFromWireFloat64Input(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{})
	dp.SetAvailableTargetChannels(map[string]TargetChannelInfo{"1_1": {ChannelNo: 1, ChannelType: "primary", Bit: 0, BitKnown: true}})
	// Deliver as float64 (common in JSON-decoded wire values)
	dp.SyncChannelLocksFromWire(float64(1))
	state := dp.ScheduleEnabled()
	if state["1_1"] != false {
		t.Errorf("1_1: want false via float64 input, got %v", state["1_1"])
	}
}

func TestSyncChannelLocksFromWireNilInputIsNoOp(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{})
	dp.SetAvailableTargetChannels(map[string]TargetChannelInfo{"1_1": {ChannelNo: 1, ChannelType: "primary", Bit: 0, BitKnown: true}})
	dp.SyncChannelLocksFromWire(nil) // must not panic
	state := dp.ScheduleEnabled()
	if state["1_1"] != true {
		t.Errorf("1_1: want initial true after nil input, got %v", state["1_1"])
	}
}

// ---------------------------------------------------------------------------
// ProfileDataPoint.Signature
// ---------------------------------------------------------------------------

func TestProfileDataPointSignature(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{})
	got := dp.Signature()
	const want = "week_profile//WEEKPROFILE"
	if got != want {
		t.Fatalf("Signature() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// ExtractSupportedScheduleFields
// ---------------------------------------------------------------------------

func TestExtractSupportedScheduleFieldsParsesKnownFields(t *testing.T) {
	t.Parallel()
	master := map[string]struct{}{
		"01_WP_CONDITION":     {},
		"01_WP_LEVEL":         {},
		"01_WP_FIXED_HOUR":    {},
		"ignore_me":           {},
		"99_WP_UNKNOWN_FIELD": {}, // unknown field name — skip
	}
	fields := ExtractSupportedScheduleFields(master)
	fieldSet := make(map[hmenum.ScheduleField]struct{}, len(fields))
	for _, f := range fields {
		fieldSet[f] = struct{}{}
	}
	for _, want := range []hmenum.ScheduleField{
		hmenum.ScheduleFieldCondition,
		hmenum.ScheduleFieldLevel,
		hmenum.ScheduleFieldFixedHour,
	} {
		if _, ok := fieldSet[want]; !ok {
			t.Errorf("ExtractSupportedScheduleFields: missing field %s", want)
		}
	}
	// unknown field must not appear
	if _, ok := fieldSet[hmenum.ScheduleField("UNKNOWN_FIELD")]; ok {
		t.Error("ExtractSupportedScheduleFields: unexpected unknown field in result")
	}
}

// TestExtractSupportedScheduleFieldsRecognisesColorFields asserts the
// universal-light colour/effect and BSL output-behaviour fields are
// recognised (so FilterRawScheduleByFields keeps them for RGBW/BSL).
func TestExtractSupportedScheduleFieldsRecognisesColorFields(t *testing.T) {
	t.Parallel()
	master := map[string]struct{}{
		"03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE":  {},
		"03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE": {},
		"01_WP_OUTPUT_BEHAVIOUR":                              {},
	}
	fields := ExtractSupportedScheduleFields(master)
	set := make(map[hmenum.ScheduleField]struct{}, len(fields))
	for _, f := range fields {
		set[f] = struct{}{}
	}
	for _, want := range []hmenum.ScheduleField{
		hmenum.ScheduleFieldColorType,
		hmenum.ScheduleFieldColorValue,
		hmenum.ScheduleFieldOutputBehaviour,
	} {
		if _, ok := set[want]; !ok {
			t.Errorf("missing colour field %s", want)
		}
	}
}

func TestExtractSupportedScheduleFieldsEmptyParamset(t *testing.T) {
	t.Parallel()
	fields := ExtractSupportedScheduleFields(map[string]struct{}{})
	if len(fields) != 0 {
		t.Fatalf("expected empty result, got %v", fields)
	}
}
