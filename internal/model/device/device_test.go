// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func newBoolDP(channel string, p hmenum.Parameter) *generic.BinarySensor {
	return generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
}

func newFloatDP(channel string, p hmenum.Parameter) *generic.Sensor[float64] {
	return generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
}

func newTestDevice(t *testing.T) *Device {
	t.Helper()
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Name:        "Wohnzimmer Sensor",
		Updatable:   true,
		Firmware:    FirmwareInfo{Current: "2.0.0", UpdateState: hmenum.DeviceFirmwareStateUpToDate},
	})
	d.AddChannel("0001ABCD:0", 0, "", hmenum.ParamsetKeyValues)
	d.AddChannel("0001ABCD:1", 1, "", hmenum.ParamsetKeyValues)
	return d
}

func TestDeviceChannelAndDataPointLookup(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	if ch == nil {
		t.Fatal("channel :1 should exist")
	}
	dp := newBoolDP("0001ABCD:1", hmenum.ParameterLowBat)
	ch.Put(dp)

	got := d.DataPoint("0001ABCD:1", hmenum.ParameterLowBat)
	if got == nil {
		t.Fatal("DataPoint lookup should hit")
	}
	if got.Parameter() != hmenum.ParameterLowBat {
		t.Fatalf("got parameter %s", got.Parameter())
	}
	if d.Channel("missing") != nil {
		t.Fatal("missing channel must be nil")
	}
	if d.DataPoint("0001ABCD:0", hmenum.ParameterLowBat) != nil {
		t.Fatal("unrelated channel DP lookup must be nil")
	}
}

func TestAvailabilityReachabilityFromUnreach(t *testing.T) {
	d := newTestDevice(t)
	ch0 := d.Channel("0001ABCD:0")

	unreach := newBoolDP("0001ABCD:0", hmenum.ParameterUnreach)
	ch0.Put(unreach)

	// No event yet → default reachable.
	if !d.Availability().IsReachable() {
		t.Fatal("unknown UNREACH → reachable")
	}

	unreach.OnEvent(true)
	if d.Availability().IsReachable() {
		t.Fatal("UNREACH=true → not reachable")
	}
	unreach.OnEvent(false)
	if !d.Availability().IsReachable() {
		t.Fatal("UNREACH=false → reachable")
	}
}

func TestAvailabilityStickyFallback(t *testing.T) {
	d := newTestDevice(t)
	sticky := newBoolDP("0001ABCD:0", hmenum.ParameterStickyUnreach)
	sticky.OnEvent(true)
	d.Channel("0001ABCD:0").Put(sticky)
	if d.Availability().IsReachable() {
		t.Fatal("sticky unreach=true → not reachable (fallback)")
	}
}

func TestAvailabilityForceOverride(t *testing.T) {
	d := newTestDevice(t)
	a := d.Availability()
	if changed := a.SetForced(hmenum.ForcedDeviceAvailabilityForceFalse); !changed {
		t.Fatal("forcing false should flip from reachable")
	}
	if a.IsReachable() {
		t.Fatal("forced false")
	}
	if changed := a.SetForced(hmenum.ForcedDeviceAvailabilityForceFalse); changed {
		t.Fatal("second identical force must be no-op")
	}
	a.SetForced(hmenum.ForcedDeviceAvailabilityNotSet)
	if !a.IsReachable() {
		t.Fatal("cleared force → reachable again")
	}
}

func TestAvailabilityInfoFromObservedValues(t *testing.T) {
	d := newTestDevice(t)
	ch0 := d.Channel("0001ABCD:0")

	low := newBoolDP("0001ABCD:0", hmenum.ParameterLowBat)
	low.OnEvent(false)
	ch0.Put(low)

	rssi := newFloatDP("0001ABCD:0", hmenum.ParameterRSSIDevice)
	rssi.OnEvent(-68)
	ch0.Put(rssi)

	bat := newFloatDP("0001ABCD:0", hmenum.Parameter(hmenum.CalculatedParameterOperatingVoltageLevel))
	bat.OnEvent(82)
	ch0.Put(bat)

	info := d.Availability().Info()
	if !info.IsReachable {
		t.Fatal("reachable by default")
	}
	if info.BatteryLevel == nil || *info.BatteryLevel != 82 {
		t.Fatalf("battery level: %+v", info.BatteryLevel)
	}
	if info.LowBattery == nil || *info.LowBattery {
		t.Fatalf("low battery: %+v", info.LowBattery)
	}
	if info.SignalStrength == nil || *info.SignalStrength != -68 {
		t.Fatalf("signal: %+v", info.SignalStrength)
	}
	if !info.HasBattery() || !info.HasSignalInfo() {
		t.Fatal("info flags")
	}
	if info.LastUpdated.IsZero() {
		t.Fatal("LastUpdated should track modifiedAt")
	}
}

func TestFirmwareOnChangeFires(t *testing.T) {
	f := newFirmware(FirmwareInfo{Current: "1.0"})
	var fired int
	f.OnChange(func(FirmwareInfo) { fired++ })
	if changed := f.Set(FirmwareInfo{Current: "1.0", UpdateState: hmenum.DeviceFirmwareStateUnknown}); changed {
		t.Fatal("identical set should not fire")
	}
	if changed := f.Set(FirmwareInfo{Current: "1.1"}); !changed {
		t.Fatal("version bump should fire")
	}
	if fired != 1 {
		t.Fatalf("fired=%d, want 1", fired)
	}
}

func TestDeviceNotifyRemoved(t *testing.T) {
	d := newTestDevice(t)
	var fired int
	unsub := d.OnRemoved(func() { fired++ })
	_ = unsub
	d.NotifyRemoved()
	if fired != 1 {
		t.Fatalf("fired=%d", fired)
	}
	// Second notify → no more fires (handlers cleared).
	d.NotifyRemoved()
	if fired != 1 {
		t.Fatalf("fired=%d", fired)
	}
}

func TestChannelDataPointsSorted(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:0")
	ch.Put(newBoolDP("0001ABCD:0", hmenum.ParameterUnreach))
	ch.Put(newBoolDP("0001ABCD:0", hmenum.ParameterLowBat))
	ch.Put(newBoolDP("0001ABCD:0", hmenum.ParameterConfigPending))
	dps := ch.DataPoints()
	if len(dps) != 3 {
		t.Fatalf("len=%d", len(dps))
	}
	// Sorted alphabetically by parameter name: CONFIG_PENDING < LOW_BAT < UNREACH.
	want := []hmenum.Parameter{hmenum.ParameterConfigPending, hmenum.ParameterLowBat, hmenum.ParameterUnreach}
	for i, p := range want {
		if dps[i].Parameter() != p {
			t.Fatalf("[%d] got %s want %s", i, dps[i].Parameter(), p)
		}
	}
}

func newMasterParam(channel, name string) *generic.DataPoint[float64] {
	return generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channel,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      name,
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	})
}

func TestDeviceHasWeekProfile(t *testing.T) {
	d := newTestDevice(t)
	if d.HasWeekProfile() {
		t.Fatal("device with no attached week profile must not report week profile")
	}
	// Add an irrelevant MASTER parameter — still no week profile.
	d.Channel("0001ABCD:1").PutMaster(newMasterParam("0001ABCD:1", "TRANSMIT_TRY_MAX"))
	if d.HasWeekProfile() {
		t.Fatal("non-schedule MASTER param must not flip the gate")
	}
	// Adding a slot-named MASTER parameter does NOT flip the gate any
	// more — the pipeline filters those out and instead attaches a
	// dedicated descriptor via [Channel.AttachWeekProfile]. Adding the
	// parameter directly here proves that the bare DP is no longer the
	// signal; only the attached descriptor counts.
	d.Channel("0001ABCD:1").PutMaster(newMasterParam("0001ABCD:1", "ENDTIME_MONDAY_1"))
	if d.HasWeekProfile() {
		t.Fatal("ENDTIME_MONDAY_1 master param alone must not indicate a week profile any more — only AttachWeekProfile does")
	}
	// Attach a descriptor — gate trips.
	d.Channel("0001ABCD:1").AttachWeekProfile(weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "test",
		ChannelAddress: "0001ABCD:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   6,
	}))
	if !d.HasWeekProfile() {
		t.Fatal("attached week-profile descriptor must indicate a week profile")
	}
}

func newStringDP(channel string, p hmenum.Parameter) *generic.DataPoint[string] {
	return generic.NewDataPoint[string](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeString, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
}

func TestChannelOperationMode(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	if got := ch.OperationMode(); got != "" {
		t.Fatalf("no DP → empty, got %q", got)
	}
	mode := newStringDP("0001ABCD:1", hmenum.ParameterChannelOperationMode)
	ch.Put(mode)
	if got := ch.OperationMode(); got != "" {
		t.Fatalf("DP without observed value → empty, got %q", got)
	}
	mode.OnEvent("PWM")
	if got := ch.OperationMode(); got != "PWM" {
		t.Fatalf("OperationMode = %q want PWM", got)
	}
}

func TestChannelIsGroupMaster(t *testing.T) {
	d := newTestDevice(t)
	ch1 := d.Channel("0001ABCD:1")
	if ch1.IsGroupMaster() {
		t.Fatal("GroupNo == 0 must not be a group master")
	}
	ch1.GroupNo = 1
	if !ch1.IsGroupMaster() {
		t.Fatal("GroupNo == Number must be a group master")
	}
	ch1.GroupNo = 5
	if ch1.IsGroupMaster() {
		t.Fatal("GroupNo != Number must not be a group master")
	}
}

func TestChannelGroupMasterLookup(t *testing.T) {
	d := newTestDevice(t)
	// Add a third channel that will act as the group master.
	master := d.AddChannel("0001ABCD:3", 3, "", hmenum.ParamsetKeyValues)
	master.GroupNo = 3

	slave := d.Channel("0001ABCD:1")
	slave.GroupNo = 3
	got := slave.GroupMaster()
	if got != master {
		t.Fatalf("GroupMaster lookup = %v want master channel", got)
	}
	// The master itself returns itself.
	if master.GroupMaster() != master {
		t.Fatal("master must return itself as GroupMaster")
	}
	// Channel without group → nil.
	other := d.Channel("0001ABCD:0")
	if other.GroupMaster() != nil {
		t.Fatal("ungrouped channel must return nil GroupMaster")
	}
}

func TestChannelRoomFallbackToMaster(t *testing.T) {
	d := newTestDevice(t)
	master := d.AddChannel("0001ABCD:3", 3, "", hmenum.ParamsetKeyValues)
	master.GroupNo = 3
	master.Rooms = []string{"Wohnzimmer"}

	slave := d.Channel("0001ABCD:1")
	slave.GroupNo = 3
	if got := slave.Room(); got != "Wohnzimmer" {
		t.Fatalf("slave.Room = %q want Wohnzimmer (fallback to master)", got)
	}
	// Slave with own room wins over master fallback.
	slave.Rooms = []string{"Küche"}
	if got := slave.Room(); got != "Küche" {
		t.Fatalf("own single room wins: got %q", got)
	}
	// Multi-room assignment on the slave → fall back to master's
	// single room. Mirrors Python where len != 1 escapes the
	// short-circuit and proceeds to the group_master lookup.
	slave.Rooms = []string{"A", "B"}
	if got := slave.Room(); got != "Wohnzimmer" {
		t.Fatalf("multi-room slave falls back to master room: got %q", got)
	}
	// Master with multiple rooms → empty (no unique answer).
	master.Rooms = []string{"R1", "R2"}
	slave.Rooms = []string{"S1", "S2"}
	if got := slave.Room(); got != "" {
		t.Fatalf("master also ambiguous → empty, got %q", got)
	}
}

func TestDeviceAllDataPointsSortedByChannelThenParameter(t *testing.T) {
	d := newTestDevice(t)
	ch0 := d.Channel("0001ABCD:0")
	ch1 := d.Channel("0001ABCD:1")

	ch1.Put(newBoolDP("0001ABCD:1", hmenum.ParameterLowBat))
	ch1.Put(newFloatDP("0001ABCD:1", hmenum.ParameterActualTemperature))
	ch0.Put(newBoolDP("0001ABCD:0", hmenum.ParameterUnreach))

	all := d.AllDataPoints()
	// Channels sorted alphabetically, parameters sorted within each.
	want := []struct {
		channel string
		param   hmenum.Parameter
	}{
		{"0001ABCD:0", hmenum.ParameterUnreach},
		{"0001ABCD:1", hmenum.ParameterActualTemperature},
		{"0001ABCD:1", hmenum.ParameterLowBat},
	}
	if len(all) != len(want) {
		t.Fatalf("len=%d want %d", len(all), len(want))
	}
	for i, w := range want {
		if all[i].DataPointKey().ChannelAddress != w.channel || all[i].Parameter() != w.param {
			t.Fatalf("[%d] got %s/%s want %s/%s", i,
				all[i].DataPointKey().ChannelAddress, all[i].Parameter(),
				w.channel, w.param)
		}
	}
}

func TestDeviceDataPointCountAndHasReadable(t *testing.T) {
	d := newTestDevice(t)
	if d.DataPointCount() != 0 {
		t.Fatalf("empty device count=%d", d.DataPointCount())
	}
	if d.HasReadableDataPoint() {
		t.Fatal("empty device must not have readable DPs")
	}

	ch := d.Channel("0001ABCD:1")
	ch.Put(newBoolDP("0001ABCD:1", hmenum.ParameterLowBat))
	ch.Put(newFloatDP("0001ABCD:1", hmenum.ParameterActualTemperature))

	if got := d.DataPointCount(); got != 2 {
		t.Fatalf("count=%d want 2", got)
	}
	if !d.HasReadableDataPoint() {
		t.Fatal("device with read+event DPs must report readable")
	}
}

func TestChannelLinkPeerChangedSubscribeAndPublish(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")

	var fired int
	unsub := ch.OnLinkPeerChanged(func() { fired++ })
	ch.NotifyLinkPeerChanged()
	ch.NotifyLinkPeerChanged()
	if fired != 2 {
		t.Fatalf("fired=%d after two notifies", fired)
	}
	unsub()
	ch.NotifyLinkPeerChanged()
	if fired != 2 {
		t.Fatal("after unsub no further fires")
	}
}

func TestChannelLinkPeerChangedNilHandlerIgnored(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	unsub := ch.OnLinkPeerChanged(nil)
	if unsub == nil {
		t.Fatal("nil handler must still return a non-nil unsub closure")
	}
	// Calling Notify must not panic when only nil handlers are queued.
	ch.NotifyLinkPeerChanged()
	unsub()
}

func TestDeviceAllMasterDataPoints(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	ch.PutMaster(newMasterParam("0001ABCD:1", "TRANSMIT_TRY_MAX"))
	ch.PutMaster(newMasterParam("0001ABCD:1", "TEMPERATURE_OFFSET"))
	if len(d.AllDataPoints()) != 0 {
		t.Fatal("MASTER must not bleed into VALUES list")
	}
	got := d.AllMasterDataPoints()
	if len(got) != 2 {
		t.Fatalf("got %d MASTER DPs, want 2", len(got))
	}
}

func TestDeviceIdentifyChannel(t *testing.T) {
	d := newTestDevice(t)
	if d.IdentifyChannel("") != nil {
		t.Fatal("empty hint must yield nil")
	}
	if d.IdentifyChannel("0001ABCD:1") == nil {
		t.Fatal("exact-suffix match must hit")
	}
	if d.IdentifyChannel("Service-Message on 0001ABCD:1") == nil {
		t.Fatal("suffix-match in free text must hit")
	}
	if d.IdentifyChannel("Service-Message on 0001ABCD:1 (sticky)") != nil {
		t.Fatal("non-suffix match must NOT hit (regression guard against substring search)")
	}
	if d.IdentifyChannel("0009XYZ:9") != nil {
		t.Fatal("unknown address must yield nil")
	}
}

// ─── ChannelGroups / GetChannelGroupNo ────────────────────────────────

// TestChannelGroupsEmpty verifies that ChannelGroups returns nil when no
// groups have been registered.
func TestChannelGroupsEmpty(t *testing.T) {
	d := newTestDevice(t)
	if got := d.ChannelGroups(); got != nil {
		t.Fatalf("ChannelGroups() = %v, want nil", got)
	}
}

// TestChannelGroupsAfterAddChannelToGroup verifies the round-trip:
// AddChannelToGroup → ChannelGroups returns correct sorted slice.
func TestChannelGroupsAfterAddChannelToGroup(t *testing.T) {
	d := newTestDevice(t)
	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(1, 2)
	d.AddChannelToGroup(2, 3)

	groups := d.ChannelGroups()
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(groups))
	}
	g1 := groups[0]
	if g1.GroupNumber != 1 || len(g1.ChannelNumbers) != 2 || g1.ChannelNumbers[0] != 1 || g1.ChannelNumbers[1] != 2 {
		t.Fatalf("group[0] = %+v, want {GroupNumber:1 ChannelNumbers:[1 2]}", g1)
	}
	g2 := groups[1]
	if g2.GroupNumber != 2 || len(g2.ChannelNumbers) != 1 || g2.ChannelNumbers[0] != 3 {
		t.Fatalf("group[1] = %+v, want {GroupNumber:2 ChannelNumbers:[3]}", g2)
	}
}

// TestGetChannelGroupNoKnownChannel verifies that GetChannelGroupNo returns the
// correct group number.
func TestGetChannelGroupNoKnownChannel(t *testing.T) {
	d := newTestDevice(t)
	d.AddChannelToGroup(5, 3)
	if got := d.GetChannelGroupNo(3); got != 5 {
		t.Fatalf("GetChannelGroupNo(3) = %d, want 5", got)
	}
}

// TestGetChannelGroupNoUnknownChannelReturnsZero verifies return 0 for
// unknown channels.
func TestGetChannelGroupNoUnknownChannelReturnsZero(t *testing.T) {
	d := newTestDevice(t)
	if got := d.GetChannelGroupNo(99); got != 0 {
		t.Fatalf("GetChannelGroupNo(99) = %d, want 0", got)
	}
}

// ─── DefaultScheduleChannel ──────────────────────────────────────────

// TestDefaultScheduleChannelNone verifies nil when no channel has a week
// profile.
func TestDefaultScheduleChannelNone(t *testing.T) {
	d := newTestDevice(t)
	if got := d.DefaultScheduleChannel(); got != nil {
		t.Fatalf("DefaultScheduleChannel() = %v, want nil", got)
	}
}

// TestDefaultScheduleChannelFindsFirst verifies the first schedule channel is
// returned.
func TestDefaultScheduleChannelFindsFirst(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	ch.AttachWeekProfile(&weekprofile.ProfileDataPoint{})
	got := d.DefaultScheduleChannel()
	if got == nil {
		t.Fatal("DefaultScheduleChannel() must return the channel with week profile")
	}
	if got.Address != "0001ABCD:1" {
		t.Fatalf("DefaultScheduleChannel().Address = %q, want 0001ABCD:1", got.Address)
	}
}

// ─── AllowUndefinedGenericDataPoints ──────────────────────────────────

// TestAllowUndefinedFalseWhenNoCustomDP verifies false when no custom DP.
func TestAllowUndefinedFalseWhenNoCustomDP(t *testing.T) {
	d := newTestDevice(t)
	if d.AllowUndefinedGenericDataPoints() {
		t.Fatal("AllowUndefinedGenericDataPoints() must be false when no custom DP attached")
	}
}

// ─── HasSubDevices ────────────────────────────────────────────────────

// TestHasSubDevicesFalseNoGroups verifies false when no groups.
func TestHasSubDevicesFalseNoGroups(t *testing.T) {
	d := newTestDevice(t)
	if d.HasSubDevices() {
		t.Fatal("HasSubDevices() must be false with no groups")
	}
}

// TestHasSubDevicesFalseOneGroup verifies false when only one group.
func TestHasSubDevicesFalseOneGroup(t *testing.T) {
	d := newTestDevice(t)
	d.AddChannelToGroup(1, 1)
	if d.HasSubDevices() {
		t.Fatal("HasSubDevices() must be false with only one group")
	}
}

// TestHasSubDevicesFalseTwoSingletonGroups verifies that two groups of one
// member each do NOT trigger sub-device splitting — splitting would yield
// single-DP children with no added structure.
func TestHasSubDevicesFalseTwoSingletonGroups(t *testing.T) {
	d := newTestDevice(t)
	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(2, 2)
	if d.HasSubDevices() {
		t.Fatal("HasSubDevices() must be false when both groups are singletons")
	}
}

// TestHasSubDevicesFalseOnlyOneMultiGroup verifies false when only one group
// carries multiple channels and the others are singletons (HmIP-WRC6-230:
// one IP_SWITCH multi-member group + seven singleton LED groups).
func TestHasSubDevicesFalseOnlyOneMultiGroup(t *testing.T) {
	d := newTestDevice(t)
	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(1, 2)
	d.AddChannelToGroup(2, 3)
	if d.HasSubDevices() {
		t.Fatal("HasSubDevices() must be false with only one multi-channel group")
	}
}

// TestHasSubDevicesTrueTwoMultiGroups verifies true when at least two groups
// each carry more than one channel.
func TestHasSubDevicesTrueTwoMultiGroups(t *testing.T) {
	d := newTestDevice(t)
	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(1, 2)
	d.AddChannelToGroup(2, 3)
	d.AddChannelToGroup(2, 4)
	if !d.HasSubDevices() {
		t.Fatal("HasSubDevices() must be true with two multi-channel groups")
	}
}

// ─── RelevantForCentralLinkManagement ────────────────────────────────

// TestRelevantForCentralLinkManagementBidCosRF verifies BidCos-RF is relevant.
func TestRelevantForCentralLinkManagementBidCosRF(t *testing.T) {
	d := New(Config{Interface: hmenum.InterfaceBidCosRF, Address: "X"})
	if !d.RelevantForCentralLinkManagement() {
		t.Fatal("BidCos-RF must be relevant for central link management")
	}
}

// TestRelevantForCentralLinkManagementHmIPRF verifies HmIP-RF participates
// in central link management (parity with the Python reference's
// RELEVANT_LINK_INTERFACES set, which includes BidCos-RF, BidCos-Wired and
// HmIP-RF).
func TestRelevantForCentralLinkManagementHmIPRF(t *testing.T) {
	d := New(Config{Interface: hmenum.InterfaceHmIPRF, Address: "X"})
	if !d.RelevantForCentralLinkManagement() {
		t.Fatal("HmIP-RF must be relevant for central link management")
	}
}

// TestRelevantForCentralLinkManagementCUxDFalse verifies CUxD-backed
// pseudo-devices are excluded — they use a separate dispatch path.
func TestRelevantForCentralLinkManagementCUxDFalse(t *testing.T) {
	d := New(Config{Interface: hmenum.InterfaceCUxD, Address: "X"})
	if d.RelevantForCentralLinkManagement() {
		t.Fatal("CUxD must not be relevant for central link management")
	}
}

// TestRelevantForCentralLinkManagementVirtualRemoteFalse verifies that
// virtual-remote pseudo-models are excluded even on an otherwise relevant
// interface.
func TestRelevantForCentralLinkManagementVirtualRemoteFalse(t *testing.T) {
	for _, model := range []string{"HM-RCV-50", "HMW-RCV-50", "HmIP-RCV-50"} {
		d := New(Config{Interface: hmenum.InterfaceHmIPRF, Address: "X", Model: model})
		if d.RelevantForCentralLinkManagement() {
			t.Fatalf("virtual-remote model %q must not be relevant for central link management", model)
		}
	}
}

// ─── GetReadableDataPoints ───────────────────────────────────────────

// TestGetReadableDataPointsFiltersReadable verifies that only readable DPs
// are returned.
func TestGetReadableDataPointsFiltersReadable(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	ch.Put(newBoolDP("0001ABCD:1", hmenum.ParameterUnreach))            // readable
	ch.Put(newFloatDP("0001ABCD:1", hmenum.ParameterActualTemperature)) // readable
	readable := d.GetReadableDataPoints("")
	if len(readable) != 2 {
		t.Fatalf("GetReadableDataPoints() = %d, want 2", len(readable))
	}
}

// ─── IsScheduleChannel ───────────────────────────────────────────────

// TestIsScheduleChannelFalseByDefault verifies false before week profile
// attachment.
func TestIsScheduleChannelFalseByDefault(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	if ch.IsScheduleChannel() {
		t.Fatal("IsScheduleChannel() must be false before AttachWeekProfile")
	}
}

// TestIsScheduleChannelTrueAfterAttach verifies true after week profile
// attachment.
func TestIsScheduleChannelTrueAfterAttach(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	ch.AttachWeekProfile(&weekprofile.ProfileDataPoint{})
	if !ch.IsScheduleChannel() {
		t.Fatal("IsScheduleChannel() must be true after AttachWeekProfile")
	}
}

// ─── Channel.UniqueID ────────────────────────────────────────────────

// TestChannelUniqueIDFormat verifies the stable "<device>_<channel>" format.
func TestChannelUniqueIDFormat(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	got := ch.UniqueID()
	want := "0001ABCD_1"
	if got != want {
		t.Fatalf("Channel.UniqueID() = %q, want %q", got, want)
	}
}

// TestChannelUniqueIDChannel0 verifies channel 0 unique ID.
func TestChannelUniqueIDChannel0(t *testing.T) {
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:0")
	got := ch.UniqueID()
	want := "0001ABCD_0"
	if got != want {
		t.Fatalf("Channel.UniqueID() = %q, want %q", got, want)
	}
}

// ─── Update.TranslationKey ───────────────────────────────────────────

// TestUpdateTranslationKeyReturnsDeviceUpdate verifies the translation key.
func TestUpdateTranslationKeyReturnsDeviceUpdate(t *testing.T) {
	d := newTestDevice(t)
	u := d.Update()
	if u == nil {
		t.Skip("Update is nil; device created without Updatable=true")
	}
	if got, want := u.TranslationKey(), "device_update"; got != want {
		t.Fatalf("Update.TranslationKey() = %q, want %q", got, want)
	}
}

// ─── SetScheduleChannelSwitches / ScheduleChannelSwitches ────────────

// TestSetScheduleChannelSwitchesEmpty verifies that an unset device returns nil.
func TestSetScheduleChannelSwitchesEmpty(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	if got := d.ScheduleChannelSwitches(); got != nil {
		t.Fatalf("ScheduleChannelSwitches() = %v, want nil before any set", got)
	}
}

// TestSetScheduleChannelSwitchesStoresAndReturns verifies round-trip storage.
func TestSetScheduleChannelSwitchesStoresAndReturns(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)

	profile := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "TEST",
		ChannelAddress: "0001ABCD",
	})
	sw1 := weekprofile.NewChannelSwitch("TEST", "0001ABCD", "1_1", profile)
	sw2 := weekprofile.NewChannelSwitch("TEST", "0001ABCD", "1_2", profile)

	d.SetScheduleChannelSwitches([]*weekprofile.ChannelSwitch{sw1, sw2})

	got := d.ScheduleChannelSwitches()
	if len(got) != 2 {
		t.Fatalf("ScheduleChannelSwitches() returned %d items, want 2", len(got))
	}
	if got[0].ChannelKey() != "1_1" {
		t.Errorf("got[0].ChannelKey() = %q, want %q", got[0].ChannelKey(), "1_1")
	}
	if got[1].ChannelKey() != "1_2" {
		t.Errorf("got[1].ChannelKey() = %q, want %q", got[1].ChannelKey(), "1_2")
	}
}

// TestSetScheduleChannelSwitchesReturnsCopy verifies that mutations on the
// returned slice do not affect the stored slice (defensive copy).
func TestSetScheduleChannelSwitchesReturnsCopy(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)

	profile := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "TEST",
		ChannelAddress: "0001ABCD",
	})
	sw := weekprofile.NewChannelSwitch("TEST", "0001ABCD", "1_1", profile)

	d.SetScheduleChannelSwitches([]*weekprofile.ChannelSwitch{sw})

	got := d.ScheduleChannelSwitches()
	got[0] = nil // mutate returned slice
	if got2 := d.ScheduleChannelSwitches(); got2[0] == nil {
		t.Fatal("ScheduleChannelSwitches() should return a defensive copy; internal state was mutated")
	}
}

// ─── ReloadDeviceConfig ──────────────────────────────────────────────

// TestReloadDeviceConfigNoRefresherSkips verifies that channels with no
// refresher installed are silently skipped (pre-bootstrap state).
func TestReloadDeviceConfigNoRefresherSkips(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	// No refresher installed on any channel — should return nil, not error.
	if err := d.ReloadDeviceConfig(t.Context()); err != nil {
		t.Fatalf("ReloadDeviceConfig() with no refreshers should return nil, got %v", err)
	}
}

// ─── GetDataPointValue ────────────────────────────────────────────────

// TestGetDataPointValueHits verifies that a VALUES-paramset data point is
// found and its RawValue is returned.
func TestGetDataPointValueHits(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	dp := newBoolDP("0001ABCD:1", hmenum.ParameterLowBat)
	dp.OnEvent(true)
	ch.Put(dp)

	v, ok := d.GetDataPointValue("0001ABCD:1", string(hmenum.ParameterLowBat))
	if !ok {
		t.Fatal("GetDataPointValue must return ok=true for a present, observed DP")
	}
	if v != true {
		t.Fatalf("GetDataPointValue = %v, want true", v)
	}
}

// TestGetDataPointValueMissingChannel verifies that an unknown channel address
// returns (nil, false).
func TestGetDataPointValueMissingChannel(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	v, ok := d.GetDataPointValue("NOSUCHADDR:9", "STATE")
	if ok || v != nil {
		t.Fatalf("unknown channel must return (nil, false), got (%v, %v)", v, ok)
	}
}

// TestGetDataPointValueMissingParameter verifies (nil, false) when the channel
// exists but carries no such parameter.
func TestGetDataPointValueMissingParameter(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	v, ok := d.GetDataPointValue("0001ABCD:1", "LEVEL")
	if ok || v != nil {
		t.Fatalf("missing parameter must return (nil, false), got (%v, %v)", v, ok)
	}
}

// TestGetDataPointValueFallsBackToMaster verifies that a MASTER-paramset
// data point is reachable when no matching VALUES DP exists.
func TestGetDataPointValueFallsBackToMaster(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	ch := d.Channel("0001ABCD:1")
	master := newMasterParam("0001ABCD:1", "TEMPERATURE_OFFSET")
	master.OnEvent(1.5)
	ch.PutMaster(master)

	v, ok := d.GetDataPointValue("0001ABCD:1", "TEMPERATURE_OFFSET")
	if !ok {
		t.Fatal("GetDataPointValue must find MASTER-paramset DP as fallback")
	}
	if v != float64(1.5) {
		t.Fatalf("GetDataPointValue = %v, want 1.5", v)
	}
}
