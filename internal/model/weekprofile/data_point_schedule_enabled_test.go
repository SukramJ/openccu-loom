// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ProfileDataPoint.ScheduleEnabled domain-level tests
// ---------------------------------------------------------------------------

func TestScheduleEnabledNilWithoutRegisteredChannels(t *testing.T) {
	// A fresh ProfileDataPoint with no channels registered returns nil.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	got := dp.ScheduleEnabled()
	if got != nil {
		t.Errorf("ScheduleEnabled() = %v, want nil (no channels registered)", got)
	}
}

func TestScheduleEnabledAfterRegister(t *testing.T) {
	// Once channels are registered, ScheduleEnabled returns a per-channel map.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	dp.RegisterChannel("1_2", false)

	got := dp.ScheduleEnabled()
	if got == nil {
		t.Fatal("ScheduleEnabled() = nil after RegisterChannel")
	}
	if got["1_1"] != true {
		t.Errorf("1_1: expected true, got %v", got["1_1"])
	}
	if got["1_2"] != false {
		t.Errorf("1_2: expected false, got %v", got["1_2"])
	}
}

func TestSetScheduleEnabledSingleChannel(t *testing.T) {
	// SetScheduleEnabled("1_1", false) only disables that channel.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	dp.RegisterChannel("1_2", true)

	_ = dp.SetScheduleEnabled(context.Background(), "1_1", false, hmenum.CommandPriorityHigh)
	got := dp.ScheduleEnabled()
	if got["1_1"] != false {
		t.Errorf("1_1: expected false after disabling")
	}
	if got["1_2"] != true {
		t.Errorf("1_2: expected still true")
	}
}

func TestScheduleEnabledReturnsNilForClimate(t *testing.T) {
	// Climate devices do not register channels; ScheduleEnabled returns nil.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		MinTemp:      5,
		MaxTemp:      30,
		ProfileCount: 3,
	})
	if dp.ScheduleEnabled() != nil {
		t.Error("ScheduleEnabled() for climate must return nil")
	}
}

func TestScheduleEnabledCopiedOnRead(t *testing.T) {
	// ScheduleEnabled returns a defensive copy; mutations must not affect the DP.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)

	got := dp.ScheduleEnabled()
	got["1_1"] = false

	got2 := dp.ScheduleEnabled()
	if !got2["1_1"] {
		t.Error("mutating returned map must not affect DP internal state")
	}
}

// ---------------------------------------------------------------------------
// ChannelSwitch (ScheduleChannelSwitch)
// ---------------------------------------------------------------------------

func TestChannelSwitchValueNilWithoutRegisteredChannel(t *testing.T) {
	// Value() returns nil when parent DP has no schedule-enabled map.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)
	if cs.Value() != nil {
		t.Errorf("Value() = %v, want nil (no channels registered on dp)", cs.Value())
	}
}

func TestChannelSwitchValueAfterRegister(t *testing.T) {
	// After registering the channel on the DP, Value() reflects the enabled state.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)
	v := cs.Value()
	if v == nil {
		t.Fatal("Value() = nil, want non-nil")
	}
	if !*v {
		t.Errorf("Value() = false, want true (channel initially enabled)")
	}
}

func TestChannelSwitchTurnOn(t *testing.T) {
	// TurnOn sets the channel enabled in the parent DP.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", false)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)

	_ = cs.TurnOn(context.Background(), hmenum.CommandPriorityHigh)

	v := cs.Value()
	if v == nil || !*v {
		t.Errorf("Value() after TurnOn = %v, want true", v)
	}
}

func TestChannelSwitchTurnOff(t *testing.T) {
	// TurnOff sets the channel disabled in the parent DP.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)

	_ = cs.TurnOff(context.Background(), hmenum.CommandPriorityHigh)

	v := cs.Value()
	if v == nil || *v {
		t.Errorf("Value() after TurnOff = %v, want false", v)
	}
}

func TestChannelSwitchChannelKey(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)
	if cs.ChannelKey() != "1_1" {
		t.Errorf("ChannelKey() = %q, want 1_1", cs.ChannelKey())
	}
}

func TestChannelSwitchUniqueIDContainsKey(t *testing.T) {
	// UniqueID must contain the channel key.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)
	uid := cs.UniqueID()
	if uid == "" {
		t.Fatal("UniqueID() is empty")
	}
	if !strings.Contains(uid, "1_1") {
		t.Errorf("UniqueID() = %q does not contain channel key 1_1", uid)
	}
}

func TestChannelSwitchValueNilWhenUnknownKey(t *testing.T) {
	// If the DP has channels registered but not the key this switch controls,
	// Value() returns nil.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_2", true)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp) // key not registered
	if cs.Value() != nil {
		t.Errorf("Value() = %v, want nil for unregistered key", cs.Value())
	}
}

func TestChannelSwitchSetEnablesAndDisables(t *testing.T) {
	// Set(true) enables, Set(false) disables.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", false)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)

	if err := cs.Set(context.Background(), true); err != nil {
		t.Fatalf("Set(true): unexpected error: %v", err)
	}
	v := cs.Value()
	if v == nil || !*v {
		t.Errorf("Value() after Set(true) = %v, want true", v)
	}

	if err := cs.Set(context.Background(), false); err != nil {
		t.Fatalf("Set(false): unexpected error: %v", err)
	}
	v = cs.Value()
	if v == nil || *v {
		t.Errorf("Value() after Set(false) = %v, want false", v)
	}
}

func TestChannelSwitchSubscribeReceivesUpdate(t *testing.T) {
	// Subscribe callback fires when the parent DP schedule-enabled state changes
	// for this channel key.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)

	var got []bool
	var mu sync.Mutex
	unsubscribe := cs.Subscribe(func(enabled bool) {
		mu.Lock()
		got = append(got, enabled)
		mu.Unlock()
	})
	defer unsubscribe()

	// Toggle twice: true→false→true.
	_ = cs.Set(context.Background(), false)
	_ = cs.Set(context.Background(), true)

	mu.Lock()
	n := len(got)
	vals := append([]bool(nil), got...)
	mu.Unlock()

	if n < 2 {
		t.Fatalf("Subscribe: got %d callbacks, want at least 2", n)
	}
	if vals[0] != false {
		t.Errorf("callback[0] = %v, want false", vals[0])
	}
	if vals[1] != true {
		t.Errorf("callback[1] = %v, want true", vals[1])
	}
}

func TestChannelSwitchSubscribeUnsubscribeStopsCallbacks(t *testing.T) {
	// The returned unsubscribe function stops further callbacks.
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", false)
	cs := NewChannelSwitch("central1", "VCU:4", "1_1", dp)

	var count int
	var mu sync.Mutex
	unsubscribe := cs.Subscribe(func(_ bool) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	_ = cs.Set(context.Background(), true)
	unsubscribe()
	// This toggle must not increment count after unsubscribe.
	_ = cs.Set(context.Background(), false)

	mu.Lock()
	got := count
	mu.Unlock()
	if got != 1 {
		t.Errorf("callback count after unsubscribe = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// ChannelSwitch — Category and Signature
// ---------------------------------------------------------------------------

func TestChannelSwitchCategoryAndSignature(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeClimate, ProfileCount: 1})
	cs := NewChannelSwitch("ccu1", "DEV001", "P1", dp)

	if got := cs.Category(); got != hmenum.DataPointCategoryScheduleSwitch {
		t.Fatalf("Category() = %q, want schedule_switch", got)
	}
	sig := cs.Signature()
	if sig == "" {
		t.Fatal("Signature() must not be empty")
	}
}

// ---------------------------------------------------------------------------
// SetScheduleEnabled — empty channelKey broadcasts to all channels
// ---------------------------------------------------------------------------

func TestSetScheduleEnabledBroadcast(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("K1", false)
	dp.RegisterChannel("K2", false)

	// Broadcast enable.
	_ = dp.SetScheduleEnabled(context.Background(), "", true, hmenum.CommandPriorityHigh)
	for k, v := range dp.ScheduleEnabled() {
		if !v {
			t.Fatalf("after broadcast enable, key %q = false", k)
		}
	}
	// Broadcast disable.
	_ = dp.SetScheduleEnabled(context.Background(), "", false, hmenum.CommandPriorityHigh)
	for k, v := range dp.ScheduleEnabled() {
		if v {
			t.Fatalf("after broadcast disable, key %q = true", k)
		}
	}
}

// ---------------------------------------------------------------------------
// ChannelSwitch.Value — nil profile and missing key paths
// ---------------------------------------------------------------------------

func TestChannelSwitchValueNilProfile(t *testing.T) {
	t.Parallel()
	cs := &ChannelSwitch{channelKey: "K1", profile: nil}
	if cs.Value() != nil {
		t.Fatal("nil profile: Value() must be nil")
	}
}

func TestChannelSwitchValueMissingKey(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	// No channels registered → scheduleEnabled is nil.
	cs := NewChannelSwitch("ccu1", "DEV001", "K_MISSING", dp)
	if cs.Value() != nil {
		t.Fatal("missing key in scheduleEnabled: Value() must be nil")
	}
}

// ---------------------------------------------------------------------------
// CountClimateEntries — with data
// ---------------------------------------------------------------------------

func TestCountClimateEntriesWithData(t *testing.T) {
	t.Parallel()
	c := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	prof.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: 18,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "06:00", EndTime: "22:00", Temperature: 21},
		},
	}
	c.Profiles["P1"] = prof
	if got := CountClimateEntries(c); got != 1 {
		t.Fatalf("CountClimateEntries = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// SetScheduleEnabled — wire-failure rollback and per-key hold window
// ---------------------------------------------------------------------------

// failingScheduleWriter rejects every COMBINED_PARAMETER write, standing
// in for an unreachable device or a CCU that refuses the parameter.
type failingScheduleWriter struct{ err error }

func (w *failingScheduleWriter) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	return w.err
}

// recordingScheduleWriter accepts every write and records the values so a
// test can assert what reached the wire.
type recordingScheduleWriter struct {
	mu     sync.Mutex
	values []any
}

func (w *recordingScheduleWriter) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.values = append(w.values, value)
	return nil
}

func TestSetScheduleEnabledRollsBackWhenTheWireWriteFails(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	dp.AttachWriter(&failingScheduleWriter{err: errors.New("device unreachable")}, "DEV001:1")

	var notified int
	dp.OnChange(func() { notified++ })

	if err := dp.SetScheduleEnabled(context.Background(), "1_1", false, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("SetScheduleEnabled must surface the writer error")
	}
	// The CCU never changed, so it never pushes a correcting bitfield —
	// the model must not keep the optimistic value.
	if got := dp.ScheduleEnabled()["1_1"]; got != true {
		t.Errorf("1_1 = %v after a failed write, want the pre-write value true", got)
	}
	if notified < 2 {
		t.Errorf("OnChange fired %d times, want the optimistic update and its rollback", notified)
	}
	// The hold window must be released too, otherwise the next genuine
	// CCU push for this key is dropped as a stale echo.
	dp.SyncScheduleEnabled(map[string]bool{"1_1": false})
	if got := dp.ScheduleEnabled()["1_1"]; got != false {
		t.Errorf("1_1 = %v after a CCU push following the rollback, want false", got)
	}
}

func TestSyncScheduleEnabledHoldsOnlyTheWrittenChannel(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{ScheduleType: ScheduleTypeDefault})
	dp.RegisterChannel("1_1", true)
	dp.RegisterChannel("2_1", true)
	dp.AttachWriter(&recordingScheduleWriter{}, "DEV001:1")

	if err := dp.SetScheduleEnabled(context.Background(), "1_1", false, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetScheduleEnabled: %v", err)
	}

	// WEEK_PROGRAM_CHANNEL_LOCKS is one device-wide bitfield: the echo of
	// our own write for 1_1 arrives together with a genuine change on
	// 2_1 made from the CCU WebUI.
	dp.SyncScheduleEnabled(map[string]bool{"1_1": true, "2_1": false})

	got := dp.ScheduleEnabled()
	if got["1_1"] != false {
		t.Errorf("1_1 = %v, want false — the pre-write echo must not revert the optimistic write", got["1_1"])
	}
	if got["2_1"] != false {
		t.Errorf("2_1 = %v, want false — a genuine change on another channel must not be dropped", got["2_1"])
	}
}
