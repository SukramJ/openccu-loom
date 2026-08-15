// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// cover_functions_test.go — unit tests for internal helpers and unexported
// functions across cover.go, blind.go, garage.go, payload.go, topology.go,
// matter.go, and init.go.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// cover.go: NamePostfix, SubDataPointKeys, CurrentChannelPosition,
// SetGroupLevel, observedLevel (group-level path), toFloat, toCoverDirection,
// Subscribe, IsRefreshed
// ---------------------------------------------------------------------------

func TestCoverNamePostfix(t *testing.T) {
	c, _, _ := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	if got := c.NamePostfix(); got != "" {
		t.Errorf("Cover.NamePostfix() = %q, want empty", got)
	}
}

func TestCoverSubDataPointKeys(t *testing.T) {
	c, _, _ := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	keys := c.SubDataPointKeys()
	if len(keys) != 1 {
		t.Fatalf("SubDataPointKeys len=%d, want 1", len(keys))
	}
	if keys[0] != c.DataPointKey() {
		t.Errorf("SubDataPointKeys[0] mismatch")
	}
	// Nil-Float path.
	var nilCover *Cover
	_ = nilCover // compile check only; nil cover not used to call SubDataPointKeys
	// Non-nil cover, nil Float.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x:2", 1, "BLIND", hmenum.ParamsetKeyValues)
	c2 := New(Config{Channel: ch, Writer: &stubWriter{}}) // no LEVEL DP
	if keys2 := c2.SubDataPointKeys(); keys2 != nil {
		t.Errorf("SubDataPointKeys with nil Float = %v, want nil", keys2)
	}
}

// TestCoverIsRefreshed also pins the availability gate to its primary state
// carrier (LEVEL); see notes/parity/by_design.md.
func TestCoverIsRefreshed(t *testing.T) {
	c, _, level := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	if c.IsRefreshed() {
		t.Fatal("IsRefreshed() must be false before first event")
	}
	level.OnEvent(0.5)
	if !c.IsRefreshed() {
		t.Fatal("IsRefreshed() must be true after first event")
	}
	// Nil-Float cover.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x:2", 1, "BLIND", hmenum.ParamsetKeyValues)
	c2 := New(Config{Channel: ch, Writer: &stubWriter{}})
	if c2.IsRefreshed() {
		t.Error("nil-Float cover must not be refreshed")
	}
}

func TestCoverCurrentChannelPosition(t *testing.T) {
	c, _, level := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	// Not observed.
	if _, ok := c.CurrentChannelPosition(); ok {
		t.Fatal("CurrentChannelPosition() must not be observed before event")
	}
	level.OnEvent(0.6)
	pos, ok := c.CurrentChannelPosition()
	if !ok || pos.Level() != 0.6 {
		t.Errorf("CurrentChannelPosition() = (%v, %v), want (0.6, true)", pos.Level(), ok)
	}
	// Inverted.
	c2, _, level2 := newRig(t, "x:2", &stubWriter{}, custom.CoverCapabilities{InvertedControl: true})
	level2.OnEvent(0.7)
	pos2, ok2 := c2.CurrentChannelPosition()
	if !ok2 {
		t.Fatal("inverted CurrentChannelPosition() not observed")
	}
	// 1-0.7 in float64 may be 0.30000000000000004 — accept small delta.
	want := 1 - 0.7
	if d := pos2.Level() - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("inverted CurrentChannelPosition() = %v, want ~%v", pos2.Level(), want)
	}
	// Nil-Float.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x:3", 1, "BLIND", hmenum.ParamsetKeyValues)
	c3 := New(Config{Channel: ch, Writer: &stubWriter{}})
	if _, ok3 := c3.CurrentChannelPosition(); ok3 {
		t.Error("nil-Float CurrentChannelPosition() must return not-observed")
	}
}

func TestCoverSetGroupLevelAndObservedLevel(t *testing.T) {
	// Build two channels: a "sub" channel and a "group master" channel.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	sub := d.AddChannel("ABC0001:3", 3, "BLIND", hmenum.ParamsetKeyValues)
	mkFloat := func(addr string, param hmenum.Parameter) *generic.Float {
		return generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(param),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: &stubWriter{},
		})
	}
	subLevel := mkFloat("ABC0001:3", hmenum.ParameterLevel)
	sub.Put(subLevel)
	groupLevel := mkFloat("ABC0001:0", hmenum.ParameterLevel)

	c := New(Config{Channel: sub, Writer: &stubWriter{}})
	// Before group binding: uses sub LEVEL.
	subLevel.OnEvent(0.4)
	pos, ok := c.Position()
	if !ok || pos.Level() != 0.4 {
		t.Fatalf("before SetGroupLevel: position=%v ok=%v, want 0.4/true", pos.Level(), ok)
	}

	// Bind group level; use group channel for state.
	c.SetGroupLevel(groupLevel, true)
	groupLevel.OnEvent(0.9)
	pos2, ok2 := c.Position()
	if !ok2 || pos2.Level() != 0.9 {
		t.Errorf("after SetGroupLevel: position=%v ok=%v, want 0.9/true", pos2.Level(), ok2)
	}

	// Set nil to clear group binding.
	c.SetGroupLevel(nil, false)
	pos3, ok3 := c.Position()
	if !ok3 || pos3.Level() != 0.4 {
		t.Errorf("after clear SetGroupLevel: position=%v ok=%v, want 0.4/true", pos3.Level(), ok3)
	}
}

func TestCoverToCoverDirection(t *testing.T) {
	cases := []struct {
		in   any
		want CoverDirection
	}{
		{int(1), DirectionUp},
		{int(2), DirectionDown},
		{int32(1), DirectionUp},
		{int64(2), DirectionDown},
		{float64(0), DirectionNone},
		{"UP", DirectionUp},
		{"DOWN", DirectionDown},
		{"NONE", DirectionNone},
		{"UNKNOWN_STRING", DirectionUnknown},
		{nil, DirectionUnknown},
		{true, DirectionUnknown},
	}
	for _, tc := range cases {
		got := toCoverDirection(tc.in)
		if got != tc.want {
			t.Errorf("toCoverDirection(%v [%T]) = %v, want %v", tc.in, tc.in, got, tc.want)
		}
	}
}

func TestCoverToFloat(t *testing.T) {
	cases := []struct {
		in    any
		want  float64
		valid bool
	}{
		{float64(0.5), 0.5, true},
		{float32(0.25), 0.25, true},
		{int(3), 3.0, true},
		{int32(7), 7.0, true},
		{int64(9), 9.0, true},
		{"hello", 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := toFloat(tc.in)
		if ok != tc.valid {
			t.Errorf("toFloat(%v [%T]) ok=%v, want %v", tc.in, tc.in, ok, tc.valid)
		}
		if ok && got != tc.want {
			t.Errorf("toFloat(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCoverSubscribeNil(t *testing.T) {
	c, _, _ := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	unsub := c.Subscribe(nil)
	if unsub != nil {
		t.Error("Subscribe(nil) must return nil")
	}
}

func TestCoverSubscribeDirectionAndLevel2(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)

	levelDP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &stubWriter{},
	})
	level2DP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel2)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &stubWriter{},
	})
	dirDP := generic.NewIntegerSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterDirection)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(levelDP)
	ch.Put(level2DP)
	ch.Put(dirDP)

	c := New(Config{Channel: ch, Writer: &stubWriter{}})
	unsub := c.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe must return non-nil unsubscribe when DPs present")
	}
	defer unsub()

	// Trigger DIRECTION update.
	dirDP.OnEvent(1)
	if dir, ok := c.Direction(); !ok || dir != DirectionUp {
		t.Errorf("Subscribe: direction after event = %v ok=%v", dir, ok)
	}

	// Trigger LEVEL_2 update (feeds OnLevel).
	level2DP.OnEvent(0.8)
	// Level2 feeds OnLevel via toFloat. Position() comes from LEVEL (not LEVEL_2),
	// so we just verify the call doesn't panic.
}

// ---------------------------------------------------------------------------
// cover.go: WindowDrive position mapping
// ---------------------------------------------------------------------------

func TestCoverWindowDrivePositionMapping(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "WIN0001"})
	ch := d.AddChannel("WIN0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "WIN0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &stubWriter{},
	})
	ch.Put(level)
	c := New(Config{Channel: ch, Writer: &stubWriter{}, WindowDrive: true})

	// Wire -0.005 → domain 0.0 (fully closed).
	level.OnEvent(wdClosedLevel)
	pos, ok := c.Position()
	if !ok || pos.Level() != 0.0 {
		t.Errorf("WindowDrive: -0.005 wire → %v (ok=%v), want 0.0", pos.Level(), ok)
	}
	// Wire 0.0 → domain 0.01 (slightly open).
	level.OnEvent(0.0)
	pos2, ok2 := c.Position()
	if !ok2 || pos2.Level() != 0.01 {
		t.Errorf("WindowDrive: 0.0 wire → %v (ok=%v), want 0.01", pos2.Level(), ok2)
	}
	// Normal passthrough.
	level.OnEvent(0.5)
	pos3, ok3 := c.Position()
	if !ok3 || pos3.Level() != 0.5 {
		t.Errorf("WindowDrive: 0.5 wire → %v (ok=%v), want 0.5", pos3.Level(), ok3)
	}
}

func TestCoverWindowDriveSetPosition(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "WIN0001"})
	ch := d.AddChannel("WIN0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	w := &stubWriter{}
	level := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "WIN0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(level)
	c := New(Config{Channel: ch, Writer: w, WindowDrive: true})

	// target=0 → wire wdClosedLevel (-0.005).
	if err := c.SetPosition(context.Background(), 0.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last.(float64) != wdClosedLevel {
		t.Errorf("WindowDrive SetPosition(0) → wire=%v, want %v", w.last, wdClosedLevel)
	}

	// target=0.01 → wire 0.0 (slightly open).
	if err := c.SetPosition(context.Background(), 0.01, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last.(float64) != 0.0 {
		t.Errorf("WindowDrive SetPosition(0.01) → wire=%v, want 0.0", w.last)
	}
}

// ---------------------------------------------------------------------------
// blind.go: NamePostfix, Stop, StopTilt, IsStateChange,
// CurrentChannelTiltPosition, Subscribe (blind path)
// ---------------------------------------------------------------------------

func TestBlindNamePostfix(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{}, BlindKindHM)
	if got := b.NamePostfix(); got != "" {
		t.Errorf("Blind.NamePostfix() = %q, want empty", got)
	}
}

func TestBlindStop(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsStop: true}, BlindKindHM)
	if err := b.Stop(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.stopCount() != 1 {
		t.Errorf("Blind.Stop() stopCount=%d, want 1", w.stopCount())
	}
}

func TestBlindStopTilt(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsStop: true}, BlindKindHM)
	if err := b.StopTilt(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.stopCount() != 1 {
		t.Errorf("Blind.StopTilt() stopCount=%d, want 1", w.stopCount())
	}
}

func TestBlindIsStateChange(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("VCU:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	mk := func(p hmenum.Parameter) *generic.Float {
		return generic.NewFloat(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: "VCU:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
			Writer:     w,
		})
	}
	levelDP := mk(hmenum.ParameterLevel)
	level2DP := mk(hmenum.ParameterLevel2)
	ch.Put(levelDP)
	ch.Put(level2DP)
	b := NewBlind(BlindConfig{Channel: ch, Writer: w, Capabilities: custom.CoverCapabilities{SupportsTilt: true}, Kind: BlindKindHM})

	// No state observed → always true.
	if !b.IsStateChange(0.5, 0.5) {
		t.Error("IsStateChange with no position observed must be true")
	}
	levelDP.OnEvent(0.5)
	level2DP.OnEvent(0.5)
	// Same targets → false.
	if b.IsStateChange(0.5, 0.5) {
		t.Error("IsStateChange with matching targets must be false")
	}
	// Different level → true.
	if !b.IsStateChange(0.8, 0.5) {
		t.Error("IsStateChange with different level must be true")
	}
	// Different tilt → true.
	if !b.IsStateChange(0.5, 0.9) {
		t.Error("IsStateChange with different tilt must be true")
	}
}

func TestBlindCurrentChannelTiltPosition(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{}, BlindKindHM)

	// No tilt observed.
	if _, ok := b.CurrentChannelTiltPosition(); ok {
		t.Fatal("CurrentChannelTiltPosition() must not be observed before event")
	}
	// Simulate level2 update via blind's level2 pointer.
	b.level2.OnEvent(0.3)
	pos, ok := b.CurrentChannelTiltPosition()
	if !ok || pos.Level() != 0.3 {
		t.Errorf("CurrentChannelTiltPosition() = (%v, %v), want (0.3, true)", pos.Level(), ok)
	}
	// Inverted.
	b2 := newBlindRig(t, "VCU:2", &putWriter{}, custom.CoverCapabilities{InvertedControl: true}, BlindKindHM)
	b2.level2.OnEvent(0.7)
	pos2, ok2 := b2.CurrentChannelTiltPosition()
	if !ok2 {
		t.Fatal("inverted CurrentChannelTiltPosition() not observed")
	}
	want := 1 - 0.7
	if d := pos2.Level() - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("inverted CurrentChannelTiltPosition() = %v, want ~%v", pos2.Level(), want)
	}
}

func TestBlindTiltPositionInverted(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{InvertedControl: true, SupportsTilt: true}, BlindKindHM)
	b.level2.OnEvent(0.8)
	pos, ok := b.TiltPosition()
	if !ok {
		t.Fatal("TiltPosition not observed")
	}
	want := 1 - 0.8
	if d := pos.Level() - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("inverted TiltPosition = %v, want ~%v", pos.Level(), want)
	}
}

func TestBlindSetTiltNoLevel2(t *testing.T) {
	// Blind without LEVEL_2 — SetTilt must return an error.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("VCU:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	levelDP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "VCU:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &putWriter{},
	})
	ch.Put(levelDP)
	b := NewBlind(BlindConfig{Channel: ch, Writer: &putWriter{}, Kind: BlindKindHM})
	err := b.SetTilt(context.Background(), 0.5, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("SetTilt without LEVEL_2 must return an error")
	}
}

func TestBlindSubscribe(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	levelDP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &putWriter{},
	})
	level2DP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel2)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &putWriter{},
	})
	ch.Put(levelDP)
	ch.Put(level2DP)

	b := NewBlind(BlindConfig{Channel: ch, Writer: &putWriter{}, Kind: BlindKindHM})
	unsub := b.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Blind.Subscribe must return non-nil")
	}
	unsub()
}

// ---------------------------------------------------------------------------
// garage.go: Stop, IsStateChange, NamePostfix, Subscribe
// ---------------------------------------------------------------------------

func TestGarageStop(t *testing.T) {
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	if err := g.Stop(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last.(string) != string(DoorCommandStop) {
		t.Errorf("Garage.Stop() sent %v, want %q", w.last, DoorCommandStop)
	}
}

func TestGarageIsStateChange(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	// No state → always true.
	if !g.IsStateChange(0.5) {
		t.Error("IsStateChange with no state must be true")
	}
	g.OnState(DoorStateOpen) // position 1.0
	if g.IsStateChange(1.0) {
		t.Error("IsStateChange(1.0) when open must be false")
	}
	if !g.IsStateChange(0.5) {
		t.Error("IsStateChange(0.5) when open must be true")
	}
}

func TestGarageNamePostfix(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	if got := g.NamePostfix(); got != "" {
		t.Errorf("Garage.NamePostfix() = %q, want empty", got)
	}
}

func TestGarageSubscribeNilChannel(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	unsub := g.Subscribe(nil)
	if unsub == nil {
		t.Fatal("Garage.Subscribe(nil) must return non-nil no-op func")
	}
	unsub() // must not panic
}

func TestGarageSubscribeWiresDoorStateAndSection(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-MOD-HO:1", 1, "GARAGE_DOOR", hmenum.ParamsetKeyValues)

	stateDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterDoorState)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "OPEN", "CLOSED", "VENTILATION_POSITION"},
		},
	})
	sectionDP := generic.NewIntegerSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSection)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(stateDP)
	ch.Put(sectionDP)

	g := NewGarage(GarageConfig{Channel: ch, Writer: &stubWriter{}})
	unsub := g.Subscribe(ch)
	defer unsub()

	// Fire DOOR_STATE.
	fireDoorState(t, stateDP, "CLOSED")
	st, ok := g.DoorState()
	if !ok || st != DoorStateClosed {
		t.Errorf("Subscribe: state after event = %v ok=%v, want CLOSED/true", st, ok)
	}

	// Fire SECTION = 2 (opening).
	sectionDP.OnEvent(int32(sectionOpening))
	if !g.IsOpening() {
		t.Error("Subscribe: IsOpening must be true after SECTION=2")
	}

	// Fire SECTION = 5 (closing).
	sectionDP.OnEvent(int32(sectionClosing))
	if !g.IsClosing() {
		t.Error("Subscribe: IsClosing must be true after SECTION=5")
	}
}

// TestGarageAvailabilityGatesOnDoorState pins the observed-state gate to its
// primary state carrier (DOOR_STATE); see notes/parity/by_design.md. Garage
// has no IsRefreshed method — DoorState's ok return is the observed-flag
// accessor the north-bound surface relies on.
func TestGarageAvailabilityGatesOnDoorState(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-MOD-HO:1", 1, "GARAGE_DOOR", hmenum.ParamsetKeyValues)

	stateDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterDoorState)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "OPEN", "CLOSED", "VENTILATION_POSITION"},
		},
	})
	ch.Put(stateDP)

	g := NewGarage(GarageConfig{Channel: ch, Writer: &stubWriter{}})
	unsub := g.Subscribe(ch)
	defer unsub()

	if _, ok := g.DoorState(); ok {
		t.Fatal("DoorState() must be unobserved before any wire event")
	}
	fireDoorState(t, stateDP, "CLOSED")
	if _, ok := g.DoorState(); !ok {
		t.Fatal("DoorState() must be observed after DOOR_STATE event")
	}
}

// ---------------------------------------------------------------------------
// payload.go: InfoPayload, ConfigPayload for Cover / Blind / Garage;
// StatePayload (SupportsPosition path, direction path, nil guards);
// directionString, subDPKeysAsStrings
// ---------------------------------------------------------------------------

func TestCoverInfoPayload(t *testing.T) {
	c, _, level := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	level.OnEvent(0.5)
	out, ok := c.Info().(*payload.CoverInfo)
	if !ok || out == nil {
		t.Fatal("InfoPayload must not be nil")
	}
	if out.Address != "HmIP-BROLL:3" {
		t.Errorf("InfoPayload address=%v, want HmIP-BROLL:3", out.Address)
	}
	if out.Category != "cover" {
		t.Errorf("InfoPayload category=%v, want cover", out.Category)
	}
	// nil Cover.
	var nilC *Cover
	if p := nilC.Info(); p != nil {
		t.Errorf("nil Cover InfoPayload = %v, want nil", p)
	}
}

func TestCoverConfigPayload(t *testing.T) {
	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{SupportsStop: true, SupportsTilt: false, InvertedControl: true})
	out, _ := c.Config().(*payload.CoverConfig)
	if out == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if !out.InvertedControl {
		t.Errorf("ConfigPayload inverted_control=%v, want true", out.InvertedControl)
	}
	if !out.SupportsStop {
		t.Errorf("ConfigPayload supports_stop=%v, want true", out.SupportsStop)
	}
	var nilC *Cover
	if p := nilC.Config(); p != nil {
		t.Errorf("nil Cover ConfigPayload = %v, want nil", p)
	}
}

func TestCoverStatePayloadWithPosition(t *testing.T) {
	c, _, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{SupportsPosition: true, SupportsStop: true})
	// Not observed → SupportsPosition emits default 0.
	out, ok := c.State().(*payload.CoverState)
	if !ok || out == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if out.CurrentPosition == nil || *out.CurrentPosition != 0 {
		t.Errorf("StatePayload (unobserved, SupportsPosition) current_position=%v, want 0", out.CurrentPosition)
	}
	level.OnEvent(0.75)
	out2, _ := c.State().(*payload.CoverState)
	if out2.CurrentPosition == nil || *out2.CurrentPosition != 75 {
		t.Errorf("StatePayload current_position=%v, want 75", out2.CurrentPosition)
	}
	// nil guard.
	var nilC *Cover
	if p := nilC.State(); p != nil {
		t.Errorf("nil Cover StatePayload = %v, want nil", p)
	}
}

// TestCoverStatePayloadRoundsPositionToPercent covers the three LEVEL
// values whose ×100 product falls just below an exact percent in
// binary64. Truncating them published a current_position one percent
// below the value the operator had just commanded, permanently, on the
// retained state topic HA's position_template reads.
func TestCoverStatePayloadRoundsPositionToPercent(t *testing.T) {
	cases := []struct {
		level float64
		want  int
	}{
		{0.29, 29},
		{0.57, 57},
		{0.58, 58},
		{0.75, 75},
	}
	for _, tc := range cases {
		c, _, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{SupportsPosition: true})
		level.OnEvent(tc.level)
		out, _ := c.State().(*payload.CoverState)
		if out.CurrentPosition == nil || *out.CurrentPosition != tc.want {
			t.Errorf("LEVEL=%v: current_position=%v, want %d", tc.level, out.CurrentPosition, tc.want)
		}
	}
}

func TestCoverStatePayloadWithDirection(t *testing.T) {
	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	c.OnDirection(DirectionUp)
	out, _ := c.State().(*payload.CoverState)
	if out.Direction != "opening" {
		t.Errorf("StatePayload direction=%v, want opening", out.Direction)
	}
	c.OnDirection(DirectionDown)
	out2, _ := c.State().(*payload.CoverState)
	if out2.Direction != "closing" {
		t.Errorf("StatePayload direction=%v, want closing", out2.Direction)
	}
	c.OnDirection(DirectionNone)
	out3, _ := c.State().(*payload.CoverState)
	if out3.Direction != "stopped" {
		t.Errorf("StatePayload DirectionNone=%v, want stopped", out3.Direction)
	}
	c.OnDirection(DirectionUnknown)
	// hasDir becomes false → Direction is empty string (omitempty in JSON).
	out4, _ := c.State().(*payload.CoverState)
	if out4.Direction != "" {
		t.Errorf("StatePayload with DirectionUnknown must not emit direction, got %q", out4.Direction)
	}
}

func TestBlindInfoPayload(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{}, BlindKindHM)
	out, ok := b.Info().(*payload.BlindInfo)
	if !ok || out == nil {
		t.Fatal("Blind InfoPayload must not be nil")
	}
	if out.Kind != "blind" {
		t.Errorf("Blind InfoPayload kind=%v, want blind", out.Kind)
	}
	var nilB *Blind
	if p := nilB.Info(); p != nil {
		t.Errorf("nil Blind InfoPayload = %v, want nil", p)
	}
}

func TestBlindConfigPayload(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{SupportsStop: true}, BlindKindHM)
	out, _ := b.Config().(*payload.BlindConfig)
	if out == nil {
		t.Fatal("Blind ConfigPayload must not be nil")
	}
	if !out.SupportsTilt {
		t.Errorf("Blind ConfigPayload supports_tilt=%v, want true", out.SupportsTilt)
	}
	var nilB *Blind
	if p := nilB.Config(); p != nil {
		t.Errorf("nil Blind ConfigPayload = %v, want nil", p)
	}
}

func TestBlindStatePayload(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{SupportsPosition: true, SupportsTilt: true}, BlindKindHM)
	// Unobserved — defaults.
	out, ok := b.State().(*payload.BlindState)
	if !ok || out == nil {
		t.Fatal("Blind StatePayload must not be nil")
	}
	if out.CurrentTiltPosition != 0 {
		t.Errorf("Blind StatePayload current_tilt_position=%v, want 0", out.CurrentTiltPosition)
	}
	// Observe tilt.
	b.level2.OnEvent(0.4)
	out2, _ := b.State().(*payload.BlindState)
	if out2.CurrentTiltPosition != 40 {
		t.Errorf("Blind StatePayload current_tilt_position=%v, want 40", out2.CurrentTiltPosition)
	}
	// Direction.
	b.OnDirection(DirectionDown)
	out3, _ := b.State().(*payload.BlindState)
	if out3.Direction != "closing" {
		t.Errorf("Blind StatePayload direction=%v, want closing", out3.Direction)
	}
	var nilB *Blind
	if p := nilB.State(); p != nil {
		t.Errorf("nil Blind StatePayload = %v, want nil", p)
	}
}

func TestGarageInfoPayload(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	out, ok := g.Info().(*payload.GarageInfo)
	if !ok || out == nil {
		t.Fatal("Garage InfoPayload must not be nil")
	}
	if out.Kind != "garage" {
		t.Errorf("Garage InfoPayload kind=%v, want garage", out.Kind)
	}
	var nilG *Garage
	if p := nilG.Info(); p != nil {
		t.Errorf("nil Garage InfoPayload = %v, want nil", p)
	}
}

func TestGarageConfigPayload(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	out, _ := g.Config().(*payload.GarageConfig)
	if out == nil {
		t.Fatal("Garage ConfigPayload must not be nil")
	}
	var nilG *Garage
	if p := nilG.Config(); p != nil {
		t.Errorf("nil Garage ConfigPayload = %v, want nil", p)
	}
}

func TestGarageStatePayloadWithState(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	g.OnState(DoorStateClosed)
	g.OnSection(int32(sectionOpening))
	out, ok := g.State().(*payload.GarageState)
	if !ok || out == nil {
		t.Fatal("Garage StatePayload must not be nil")
	}
	if out.DoorState != string(DoorStateClosed) {
		t.Errorf("Garage StatePayload door_state=%v, want CLOSED", out.DoorState)
	}
	// current_position is always present (int field, zero-valued when not observed).
	_ = out.CurrentPosition
	var nilG *Garage
	if p := nilG.State(); p != nil {
		t.Errorf("nil Garage StatePayload = %v, want nil", p)
	}
}

func TestSubDPKeysAsStrings(t *testing.T) {
	keys := []hmtypes.DataPointKey{
		{ChannelAddress: "ABC:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"},
	}
	got := subDPKeysAsStrings(keys)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0] == "" {
		t.Error("subDPKeysAsStrings[0] must not be empty")
	}
	// Empty.
	if s := subDPKeysAsStrings(nil); len(s) != 0 {
		t.Errorf("subDPKeysAsStrings(nil)=%v, want empty", s)
	}
}

func TestDirectionString(t *testing.T) {
	cases := []struct {
		d    CoverDirection
		want string
	}{
		{DirectionUp, "opening"},
		{DirectionDown, "closing"},
		{DirectionUnknown, "unknown"},
		{DirectionNone, "stopped"},
		{CoverDirection(99), "stopped"}, // default
	}
	for _, tc := range cases {
		if got := directionString(tc.d); got != tc.want {
			t.Errorf("directionString(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// topology.go: HAComponent, TopicSlot
// ---------------------------------------------------------------------------

func TestHAComponent(t *testing.T) {
	c, _, _ := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	if got := c.HAComponent(); got != "cover" {
		t.Errorf("Cover.HAComponent() = %q, want cover", got)
	}
	b := newBlindRig(t, "x:1", &putWriter{}, custom.CoverCapabilities{}, BlindKindHM)
	if got := b.HAComponent(); got != "cover" {
		t.Errorf("Blind.HAComponent() = %q, want cover", got)
	}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	if got := g.HAComponent(); got != "cover" {
		t.Errorf("Garage.HAComponent() = %q, want cover", got)
	}
}

func TestTopicSlot(t *testing.T) {
	c, _, _ := newRig(t, "ABC0001:3", &stubWriter{}, custom.CoverCapabilities{})
	slot := c.TopicSlot()
	if slot.Parameter != "cover" {
		t.Errorf("Cover.TopicSlot().Parameter = %q, want cover", slot.Parameter)
	}
	if slot.Channel != 3 {
		t.Errorf("Cover.TopicSlot().Channel = %d, want 3", slot.Channel)
	}

	b := newBlindRig(t, "ABC0001:3", &putWriter{}, custom.CoverCapabilities{}, BlindKindHM)
	bSlot := b.TopicSlot()
	if bSlot.Parameter != "blind" {
		t.Errorf("Blind.TopicSlot().Parameter = %q, want blind", bSlot.Parameter)
	}

	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	gSlot := g.TopicSlot()
	if gSlot.Parameter != "garage" {
		t.Errorf("Garage.TopicSlot().Parameter = %q, want garage", gSlot.Parameter)
	}
}

func TestTopicSlotInvalidAddress(t *testing.T) {
	// Address without a colon — SplitChannelAddress returns ok=false.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "NODECOLON"})
	ch := d.AddChannel("NODECOLON", 0, "BLIND", hmenum.ParamsetKeyValues)
	levelDP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "NODECOLON", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &stubWriter{},
	})
	ch.Put(levelDP)
	c := New(Config{Channel: ch, Writer: &stubWriter{}})
	slot := c.TopicSlot()
	if slot.Address == "" {
		t.Error("TopicSlot with unparseable address must still produce a non-empty address")
	}
}

// ---------------------------------------------------------------------------
// matter.go: coverTypeFor, hmLevelToMatterPct100ths / matterPct100thsToHMLevel
// edge cases, MatterClusterID, MatterWrite, MatterAttributes,
// extractGoToPercentage (map path + error paths), Blind MatterRead/Invoke,
// Garage MatterRead/Invoke/MatterClusterID/MatterWrite
// ---------------------------------------------------------------------------

func TestCoverTypeFor(t *testing.T) {
	cases := []struct {
		v    CoverVariant
		want uint8
	}{
		{VariantAwning, matterWCTypeAwning},
		{VariantCurtain, matterWCTypeDrapery},
		{VariantShutter, matterWCTypeShutter},
		{VariantWindow, matterWCTypeShutter},
		{VariantShade, matterWCTypeRollerShade},
		{VariantDamper, matterWCTypeRollerShade},
		{VariantBlind, matterWCTypeRollerShade},
		{VariantGarage, matterWCTypeRollerShade},
		{CoverVariant(99), matterWCTypeRollerShade}, // default
	}
	for _, tc := range cases {
		if got := coverTypeFor(tc.v); got != tc.want {
			t.Errorf("coverTypeFor(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

// TestCoverEndProductTypeFor pins the variant → EndProductType mapping.
// EndProductType uses its own enum (matter.js
// window-covering-cluster.element.ts:166-192) — it must never reuse
// the TypeEnum code the same variant maps to in coverTypeFor.
func TestCoverEndProductTypeFor(t *testing.T) {
	cases := []struct {
		v    CoverVariant
		want uint8
	}{
		{VariantAwning, matterWCEndProductAwningTerracePatio},
		{VariantCurtain, matterWCEndProductCentralCurtain},
		{VariantShutter, matterWCEndProductRollerShutter},
		{VariantWindow, matterWCEndProductRollerShutter},
		{VariantShade, matterWCEndProductRollerShade},
		{VariantDamper, matterWCEndProductRollerShade},
		{VariantBlind, matterWCEndProductInteriorBlind},
		{VariantGarage, matterWCEndProductRollerShade},
		{CoverVariant(99), matterWCEndProductRollerShade}, // default
	}
	for _, tc := range cases {
		if got := coverEndProductTypeFor(tc.v); got != tc.want {
			t.Errorf("coverEndProductTypeFor(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

func TestHmLevelToMatterPct100thsEdgeCases(t *testing.T) {
	// Over-clamp.
	if v := hmLevelToMatterPct100ths(2.0); v != 0 {
		t.Errorf("hmLevelToMatterPct100ths(2.0) = %d, want 0 (clamped open)", v)
	}
	// Under-clamp (negative).
	if v := hmLevelToMatterPct100ths(-1.0); v != matterCoverPctMax {
		t.Errorf("hmLevelToMatterPct100ths(-1.0) = %d, want %d (clamped closed)", v, matterCoverPctMax)
	}
}

func TestMatterPct100thsToHMLevelEdge(t *testing.T) {
	// max → 0.
	if v := matterPct100thsToHMLevel(matterCoverPctMax); v != 0 {
		t.Errorf("matterPct100thsToHMLevel(10000) = %v, want 0", v)
	}
	// 0 → 1.
	if v := matterPct100thsToHMLevel(0); v != 1 {
		t.Errorf("matterPct100thsToHMLevel(0) = %v, want 1", v)
	}
}

func TestCoverMatterClusterID(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if srv.MatterClusterID() != matterClusterWindowCovering {
		t.Errorf("Cover MatterClusterID = 0x%04X, want 0x%04X", srv.MatterClusterID(), matterClusterWindowCovering)
	}
}

func TestCoverMatterWrite(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	err := srv.MatterWrite(context.Background(), matterAttrType, uint8(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite must return error (writes not supported)")
	}
}

func TestCoverMatterAttributes(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("Cover MatterClusterServer must implement MatterClusterAttributeLister")
	}
	attrs := lister.MatterAttributes()
	if len(attrs) == 0 {
		t.Error("Cover MatterAttributes must not be empty")
	}
}

func TestCoverMatterReadAllAttributes(t *testing.T) {
	c, _, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	level.OnEvent(0.6)
	c.OnDirection(DirectionUp)
	srv := c.MatterClusterServers()[0]

	readableAttrs := []uint32{
		matterAttrType,
		matterAttrEndProductType,
		matterAttrConfigStatus,
		matterAttrOperationalStatus,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrMode,
		matterAttrFeatureMap,
		matterAttrClusterRevision,
	}
	for _, id := range readableAttrs {
		v, ok := srv.MatterRead(id)
		if !ok {
			t.Errorf("MatterRead(0x%04X) not ok", id)
		}
		_ = v
	}
	// Unknown.
	if _, ok := srv.MatterRead(0xDEAD); ok {
		t.Error("MatterRead(unknown) must return ok=false")
	}
}

func TestExtractGoToPercentageMap(t *testing.T) {
	// map[string]any with "percent" key.
	m := map[string]any{"percent": uint16(5000)}
	pct, err := extractGoToPercentage(m)
	if err != nil {
		t.Fatalf("extractGoToPercentage(map) error: %v", err)
	}
	if pct != 5000 {
		t.Errorf("extractGoToPercentage(map) = %d, want 5000", pct)
	}
	// map without "percent" key.
	_, err2 := extractGoToPercentage(map[string]any{})
	if err2 == nil {
		t.Error("extractGoToPercentage(empty map) must error")
	}
	// map with wrong type.
	_, err3 := extractGoToPercentage(map[string]any{"percent": "bad"})
	if err3 == nil {
		t.Error("extractGoToPercentage(map, wrong type) must error")
	}
}

func TestBlindMatterClusterID(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]
	if srv.MatterClusterID() != matterClusterWindowCovering {
		t.Errorf("Blind MatterClusterID = 0x%04X, want 0x%04X", srv.MatterClusterID(), matterClusterWindowCovering)
	}
}

func TestBlindMatterWrite(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]
	err := srv.MatterWrite(context.Background(), matterAttrType, uint8(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("Blind MatterWrite must return error")
	}
}

func TestBlindMatterAttributes(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("Blind MatterClusterServer must implement MatterClusterAttributeLister")
	}
	attrs := lister.MatterAttributes()
	if len(attrs) == 0 {
		t.Error("Blind MatterAttributes must not be empty")
	}
}

func TestBlindMatterReadAllAttributes(t *testing.T) {
	b := newBlindRig(t, "VCU:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	// Observe tilt position.
	b.level2.OnEvent(0.4)
	b.OnDirection(DirectionDown)
	srv := b.MatterClusterServers()[0]

	readableAttrs := []uint32{
		matterAttrType,
		matterAttrEndProductType,
		matterAttrConfigStatus,
		matterAttrOperationalStatus,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrCurrentPositionTiltPercent100ths,
		matterAttrMode,
		matterAttrFeatureMap,
		matterAttrClusterRevision,
	}
	for _, id := range readableAttrs {
		_, ok := srv.MatterRead(id)
		if !ok {
			t.Errorf("Blind MatterRead(0x%04X) not ok", id)
		}
	}
	// Unknown.
	if _, ok := srv.MatterRead(0xDEAD); ok {
		t.Error("Blind MatterRead(unknown) must return ok=false")
	}
}

func TestBlindMatterInvokeAllCommands(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsTilt: true, SupportsStop: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]

	// UpOrOpen.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Blind UpOrOpen: %v", err)
	}
	// DownOrClose.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Blind DownOrClose: %v", err)
	}
	// StopMotion.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Blind StopMotion: %v", err)
	}
	// GoToLiftPercentage.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(5000), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Blind GoToLift: %v", err)
	}
	// Unknown.
	if _, err := srv.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("Blind unknown command must error")
	}
}

func TestGarageMatterClusterID(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]
	if srv.MatterClusterID() != matterClusterWindowCovering {
		t.Errorf("Garage MatterClusterID = 0x%04X, want 0x%04X", srv.MatterClusterID(), matterClusterWindowCovering)
	}
}

func TestGarageMatterWrite(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]
	err := srv.MatterWrite(context.Background(), matterAttrType, uint8(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("Garage MatterWrite must return error")
	}
}

func TestGarageMatterAttributes(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("Garage MatterClusterServer must implement MatterClusterAttributeLister")
	}
	attrs := lister.MatterAttributes()
	if len(attrs) == 0 {
		t.Error("Garage MatterAttributes must not be empty")
	}
}

func TestGarageMatterReadAllAttributes(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	g.OnState(DoorStateOpen)
	g.OnSection(int32(sectionOpening))
	srv := g.MatterClusterServers()[0]

	readableAttrs := []uint32{
		matterAttrType,
		matterAttrEndProductType,
		matterAttrConfigStatus,
		matterAttrOperationalStatus,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrMode,
		matterAttrFeatureMap,
		matterAttrClusterRevision,
	}
	for _, id := range readableAttrs {
		_, ok := srv.MatterRead(id)
		if !ok {
			t.Errorf("Garage MatterRead(0x%04X) not ok", id)
		}
	}
	// Unknown.
	if _, ok := srv.MatterRead(0xDEAD); ok {
		t.Error("Garage MatterRead(unknown) must return ok=false")
	}
}

func TestGarageMatterInvokeAllCommands(t *testing.T) {
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	srv := g.MatterClusterServers()[0]

	// UpOrOpen.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Garage UpOrOpen: %v", err)
	}
	// DownOrClose.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Garage DownOrClose: %v", err)
	}
	// StopMotion.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Garage StopMotion: %v", err)
	}
	// GoToLiftPercentage.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(5000), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Garage GoToLift: %v", err)
	}
	// Unknown.
	if _, err := srv.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("Garage unknown command must error")
	}
}

// ---------------------------------------------------------------------------
// init.go: applyGroupLevel, newRfCoverConstructor (Blind path),
// isHmSecWin, coverVariantFromModel
// ---------------------------------------------------------------------------

func TestRfCoverConstructorProducesBlindWhenLevel2Present(t *testing.T) {
	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("RfCover"))
	if !ok {
		t.Fatal("RfCover constructor not registered")
	}
	ch := newChannelWithLevelAndLevel2(t, "HM-LC-Bl1-FM:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("RfCover (blind) constructor error: %v", err)
	}
	if _, ok := dp.(*Blind); !ok {
		t.Errorf("RfCover (blind) returned %T, want *Blind", dp)
	}
}

func TestIsHmSecWinDetection(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "WIN0001"})
	d.Model = "HM-Sec-Win"
	ch := d.AddChannel("WIN0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	if !isHmSecWin(ch) {
		t.Error("isHmSecWin must return true for HM-Sec-Win channel")
	}
	d2 := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "ROLL0001"})
	d2.Model = "HM-LC-Bl1-FM"
	ch2 := d2.AddChannel("ROLL0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	if isHmSecWin(ch2) {
		t.Error("isHmSecWin must return false for non-HM-Sec-Win device")
	}
	if isHmSecWin(nil) {
		t.Error("isHmSecWin(nil) must return false")
	}
}

func TestCoverVariantFromModel(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "WIN0001"})
	d.Model = "HM-Sec-Win"
	ch := d.AddChannel("WIN0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	if got := coverVariantFromModel(ch); got != VariantWindow {
		t.Errorf("coverVariantFromModel(HM-Sec-Win) = %v, want VariantWindow", got)
	}
	d2 := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "ROLL0001"})
	d2.Model = "HM-LC-Bl1-FM"
	ch2 := d2.AddChannel("ROLL0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	if got := coverVariantFromModel(ch2); got != VariantShutter {
		t.Errorf("coverVariantFromModel(other) = %v, want VariantShutter", got)
	}
	if got := coverVariantFromModel(nil); got != VariantShutter {
		t.Errorf("coverVariantFromModel(nil) = %v, want VariantShutter", got)
	}
}

func TestApplyGroupLevelNilHandling(t *testing.T) {
	// Should not panic on nil cover, nil channel, or nil device.
	applyGroupLevel(nil, nil, custom.RebasedChannelGroupConfig{})
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	c, _, _ := newRig(t, "x:1", &stubWriter{}, custom.CoverCapabilities{})
	applyGroupLevel(c, ch, custom.RebasedChannelGroupConfig{})
	// No panic = pass.
}

// ---------------------------------------------------------------------------
// Cover.Stop with nil writer (error path)
// ---------------------------------------------------------------------------

func TestCoverStopNilWriterError(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	levelDP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "x:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     &stubWriter{},
	})
	ch.Put(levelDP)
	c := New(Config{Channel: ch, Writer: nil, Capabilities: custom.CoverCapabilities{SupportsStop: true}})
	err := c.Stop(context.Background(), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("Cover.Stop with nil writer must return an error")
	}
}
