// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- compile-time assertion ------------------------------------

// Verify that *Channel satisfies the full payload.Source interface.
// This mirrors the assertion in channel_payload.go but is kept here so
// the test binary catches regressions independently.
var _ payload.Source = (*Channel)(nil)

// ---------- helpers ---------------------------------------------------

// newMinimalChannel builds a Channel with only Address, Number and Type
// set — no Name, Rooms, Functions, GroupNo or data points.
func newMinimalChannel(addr string, number int, chanType string) *Channel {
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TESTDEV",
		Model:       "HmIP-TEST",
	})
	return d.AddChannel(addr, number, chanType, "")
}

// newMasterSelectDP creates a MASTER-paramset Select (enum) data point
// for CHANNEL_OPERATION_MODE with a VALUE_LIST, so OperationMode() can
// resolve an integer index to a label.
func newMasterSelectDP(addr string, valueList []string) *fakeRawDP {
	return &fakeRawDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterChannelOperationMode),
		},
		param: hmenum.ParameterChannelOperationMode,
		desc: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: valueList,
		},
	}
}

// fakeRawDP is a minimal ParameterDataPoint that lets tests inject an
// arbitrary RawValue without pulling in the full generic machinery.
// It has no OnAnyUpdate fan-out and no ModifiedAt tracking — sufficient
// for payload tests that only call RawValue() and ParameterData().
type fakeRawDP struct {
	key      hmtypes.DataPointKey
	param    hmenum.Parameter
	desc     hmproto.ParameterData
	rawValue any
	observed bool
}

func (f *fakeRawDP) DataPointKey() hmtypes.DataPointKey   { return f.key }
func (f *fakeRawDP) Parameter() hmenum.Parameter          { return f.param }
func (f *fakeRawDP) ParameterData() hmproto.ParameterData { return f.desc }
func (f *fakeRawDP) RawValue() (any, bool)                { return f.rawValue, f.observed }
func (f *fakeRawDP) ModifiedAt() time.Time                { return time.Time{} }
func (f *fakeRawDP) OnAnyUpdate(fn func(old, next any)) func() {
	return func() {}
}

// ---------- InfoPayload -----------------------------------------------

func TestChannelInfoPayloadMinimal(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:1", 1, "SWITCH_VIRTUAL_RECEIVER")
	info, ok := ch.Info().(*payload.ChannelInfo)
	if !ok || info == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo, got %T", ch.Info())
	}

	if info.Address != "DEV:1" {
		t.Errorf("address: got %v", info.Address)
	}
	if info.ChannelNo != 1 {
		t.Errorf("channel_no: got %v", info.ChannelNo)
	}
	if info.Type != "SWITCH_VIRTUAL_RECEIVER" {
		t.Errorf("type: got %v", info.Type)
	}
	// Optional fields must be zero/empty.
	if info.Name != "" {
		t.Errorf("unexpected name %q in minimal InfoPayload", info.Name)
	}
	if len(info.Rooms) != 0 {
		t.Errorf("unexpected rooms in minimal InfoPayload: %v", info.Rooms)
	}
	if len(info.Functions) != 0 {
		t.Errorf("unexpected functions in minimal InfoPayload: %v", info.Functions)
	}
	if info.Room != "" {
		t.Errorf("unexpected room in minimal InfoPayload: %q", info.Room)
	}
	if info.GroupNo != 0 {
		t.Errorf("unexpected group_no in minimal InfoPayload: %d", info.GroupNo)
	}
	if info.IsGroupMaster {
		t.Errorf("unexpected is_group_master in minimal InfoPayload")
	}
}

func TestChannelInfoPayloadWithOptionals(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:3", 3, "HEATING_CLIMATECONTROL_TRANSCEIVER")
	ch.SetName("Wohnzimmer")
	ch.SetRooms([]string{"Wohnzimmer"})
	ch.SetFunctions([]string{"Heizen"})
	ch.GroupNo = 3 // is group master (GroupNo == Number)

	info, ok := ch.Info().(*payload.ChannelInfo)
	if !ok || info == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo")
	}

	if info.Name != "Wohnzimmer" {
		t.Errorf("name: got %v", info.Name)
	}
	if len(info.Rooms) == 0 {
		t.Error("rooms must be present")
	}
	if len(info.Functions) == 0 {
		t.Error("functions must be present")
	}
	if info.GroupNo == 0 {
		t.Error("group_no must be present")
	}
	if !info.IsGroupMaster && !info.IsInMultiGroup {
		// GroupNo == Number means group master.
		t.Error("is_group_master or is_in_multi_group must be set")
	}
}

func TestChannelInfoPayloadEmptyOptionalsAreOmitted(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:2", 2, "TYPE_X")
	ch.SetName("")
	ch.SetRooms([]string{})
	ch.SetFunctions([]string{})
	ch.GroupNo = 0

	info, ok := ch.Info().(*payload.ChannelInfo)
	if !ok || info == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo")
	}

	if info.Name != "" {
		t.Errorf("name should be empty when zero")
	}
	if len(info.Rooms) != 0 {
		t.Errorf("rooms should be empty, got %v", info.Rooms)
	}
	if len(info.Functions) != 0 {
		t.Errorf("functions should be empty, got %v", info.Functions)
	}
	if info.GroupNo != 0 {
		t.Errorf("group_no should be 0, got %d", info.GroupNo)
	}
	if info.IsGroupMaster {
		t.Errorf("is_group_master should be false")
	}
}

func TestChannelInfoPayloadGroupMaster(t *testing.T) {
	t.Parallel()

	// Channel is group master when GroupNo == Number.
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "GRPDEV",
		Model:       "HmIP-TEST",
	})
	master := d.AddChannel("GRPDEV:5", 5, "TYPE_A", "")
	master.GroupNo = 5

	infoMaster, ok := master.Info().(*payload.ChannelInfo)
	if !ok || infoMaster == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo")
	}
	if !infoMaster.IsGroupMaster {
		t.Errorf("master: is_group_master should be true, got %v", infoMaster.IsGroupMaster)
	}

	// Member channel: GroupNo set but != Number.
	member := d.AddChannel("GRPDEV:6", 6, "TYPE_A", "")
	member.GroupNo = 5

	infoMember, ok := member.Info().(*payload.ChannelInfo)
	if !ok || infoMember == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo")
	}
	if infoMember.IsGroupMaster {
		t.Errorf("member: is_group_master should be false, got %v", infoMember.IsGroupMaster)
	}
}

func TestChannelInfoPayloadRoomsCopied(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:1", 1, "TYPE_X")
	ch.SetRooms([]string{"Kitchen", "Hallway"})

	info, ok := ch.Info().(*payload.ChannelInfo)
	if !ok || info == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo")
	}

	// Mutate the returned slice.
	info.Rooms[0] = "MUTATED"

	// A second call must still see the original value.
	info2, ok := ch.Info().(*payload.ChannelInfo)
	if !ok || info2 == nil {
		t.Fatalf("InfoPayload must return *payload.ChannelInfo")
	}
	if info2.Rooms[0] != "Kitchen" {
		t.Errorf("rooms slice leaked: got %q, want %q", info2.Rooms[0], "Kitchen")
	}
}

func TestChannelInfoPayloadNilReceiver(t *testing.T) {
	t.Parallel()

	var c *Channel
	if got := c.Info(); got != nil {
		t.Errorf("nil receiver: want nil, got %v", got)
	}
}

func TestChannelInfoPayloadRoomFromGroupMaster(t *testing.T) {
	t.Parallel()

	// Arrange: parent device with two channels.
	// Channel 2 is the group master and has one room.
	// Channel 3 belongs to the same group but has no rooms itself;
	// it should inherit the master's room via the fallback.
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "FALLBACKDEV",
		Model:       "HmIP-TEST",
	})
	master := d.AddChannel("FALLBACKDEV:2", 2, "TYPE_A", "")
	master.GroupNo = 2
	master.SetRooms([]string{"LivingRoom"})

	member := d.AddChannel("FALLBACKDEV:3", 3, "TYPE_A", "")
	member.GroupNo = 2 // belongs to group 2; master is FALLBACKDEV:2

	info, ok := member.Info().(*payload.ChannelInfo)
	if !ok || info == nil {
		t.Fatal("InfoPayload must return *payload.ChannelInfo")
	}
	if info.Room == "" {
		t.Error("room key absent — group-master fallback did not fire")
		return
	}
	if info.Room != "LivingRoom" {
		t.Errorf("room: got %q, want %q", info.Room, "LivingRoom")
	}
}

// ---------- ConfigPayload ---------------------------------------------

func TestChannelConfigPayloadNilReceiver(t *testing.T) {
	t.Parallel()

	var c *Channel
	if got := c.Config(); got != nil {
		t.Errorf("nil receiver: want nil, got %v", got)
	}
}

func TestChannelConfigPayloadAllFieldsAbsent(t *testing.T) {
	t.Parallel()

	// ParamsetIn is empty, no CHANNEL_OPERATION_MODE DP — must return nil.
	ch := newMinimalChannel("DEV:1", 1, "TYPE_X")
	// AddChannel sets ParamsetIn to "" (we pass "").
	if cfg := ch.Config(); cfg != nil {
		t.Errorf("want nil, got %v", cfg)
	}
}

func TestChannelConfigPayloadParamsetIn(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:1", 1, "TYPE_X")
	ch.ParamsetIn = hmenum.ParamsetKeyMaster

	cfg, ok := ch.Config().(*payload.ChannelConfig)
	if !ok || cfg == nil {
		t.Fatal("ConfigPayload should not be nil when ParamsetIn is set")
	}
	if cfg.ParamsetIn != string(hmenum.ParamsetKeyMaster) {
		t.Errorf("paramset_in: got %v, want %q", cfg.ParamsetIn, string(hmenum.ParamsetKeyMaster))
	}
}

func TestChannelConfigPayloadOperationModeString(t *testing.T) {
	t.Parallel()

	// Install a MASTER DP for CHANNEL_OPERATION_MODE that returns a
	// string raw value (the simpler firmware variant).
	ch := newMinimalChannel("DEV:4", 4, "TYPE_MULTIMODE")
	dp := &fakeRawDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:4",
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterChannelOperationMode),
		},
		param:    hmenum.ParameterChannelOperationMode,
		desc:     hmproto.ParameterData{Type: hmenum.ParameterTypeEnum},
		rawValue: "KEY_BEHAVIOR",
		observed: true,
	}
	ch.PutMaster(dp)

	cfg, ok := ch.Config().(*payload.ChannelConfig)
	if !ok || cfg == nil {
		t.Fatal("ConfigPayload should not be nil when operation_mode is observed")
	}
	if cfg.OperationMode != "KEY_BEHAVIOR" {
		t.Errorf("operation_mode: got %v, want %q", cfg.OperationMode, "KEY_BEHAVIOR")
	}
}

func TestChannelConfigPayloadOperationModeIndexResolved(t *testing.T) {
	t.Parallel()

	// Install a MASTER DP that returns an integer index — should be
	// resolved through the descriptor's VALUE_LIST to a label.
	valueList := []string{"INACTIVE", "KEY_BEHAVIOR", "SWITCH_BEHAVIOR"}
	ch := newMinimalChannel("DEV:5", 5, "TYPE_MULTIMODE")
	dp := newMasterSelectDP("DEV:5", valueList)
	dp.rawValue = float64(2) // index 2 → "SWITCH_BEHAVIOR"
	dp.observed = true
	ch.PutMaster(dp)

	cfg, ok := ch.Config().(*payload.ChannelConfig)
	if !ok || cfg == nil {
		t.Fatal("ConfigPayload nil with float64 index DP")
	}
	if cfg.OperationMode != "SWITCH_BEHAVIOR" {
		t.Errorf("operation_mode: got %v, want %q", cfg.OperationMode, "SWITCH_BEHAVIOR")
	}
}

func TestChannelConfigPayloadOperationModeUnobservedOmitted(t *testing.T) {
	t.Parallel()

	// DP exists but no value observed yet — key must be absent.
	ch := newMinimalChannel("DEV:6", 6, "TYPE_MULTIMODE")
	dp := &fakeRawDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:6",
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterChannelOperationMode),
		},
		param:    hmenum.ParameterChannelOperationMode,
		desc:     hmproto.ParameterData{Type: hmenum.ParameterTypeEnum},
		observed: false, // not yet observed
	}
	ch.PutMaster(dp)

	cfg, _ := ch.Config().(*payload.ChannelConfig)
	// ParamsetIn is also empty → should be nil or operation_mode must be empty.
	if cfg != nil && cfg.OperationMode != "" {
		t.Error("operation_mode should be absent when DP not yet observed")
	}
}

// ---------- StatePayload ----------------------------------------------

func TestChannelStatePayloadAlwaysNil(t *testing.T) {
	t.Parallel()

	cases := []*Channel{
		// bare channel
		newMinimalChannel("DEV:1", 1, "TYPE_X"),
		// with data points
		func() *Channel {
			ch := newMinimalChannel("DEV:2", 2, "TYPE_X")
			dp := newWritableFloatDP("DEV:2", hmenum.ParameterLevel, nil)
			ch.Put(dp)
			return ch
		}(),
		// with operation mode DP
		func() *Channel {
			ch := newMinimalChannel("DEV:3", 3, "TYPE_X")
			dp := &fakeRawDP{
				param:    hmenum.ParameterChannelOperationMode,
				rawValue: "KEY_BEHAVIOR",
				observed: true,
			}
			ch.PutMaster(dp)
			return ch
		}(),
		// nil receiver
		nil,
	}

	for i, ch := range cases {
		if got := ch.State(); got != nil {
			t.Errorf("case %d: StatePayload should always return nil, got %v", i, got)
		}
	}
}

// ---------- ServiceRegistry surface -----------------------------------

func TestChannelServiceMethodNamesEmpty(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:1", 1, "TYPE_X")
	if names := ch.ServiceMethodNames(); names != nil {
		t.Errorf("fresh channel should have nil ServiceMethodNames, got %v", names)
	}
}

func TestChannelInvokeUnknownService(t *testing.T) {
	t.Parallel()

	ch := newMinimalChannel("DEV:1", 1, "TYPE_X")
	err := ch.Invoke(context.Background(), "nonexistent_method", nil, hmenum.CommandPriorityCritical)
	if err == nil {
		t.Fatal("Invoke with unknown method should return an error")
	}
	if !errors.Is(err, payload.ErrUnknownServiceMethod) {
		t.Errorf("want ErrUnknownServiceMethod, got %v", err)
	}
}
