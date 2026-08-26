// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newFleetBlind builds the HmIP-FBL blind receiver channel from the
// device's own VALUES paramset descriptor (testdata, extracted verbatim
// from the simulator's embedded description for VCU1223813:4) and runs
// it through the registered IPCover constructor — the same path the
// device pipeline takes. Building the channel from the real descriptor
// keeps the bounds, operations and units of the wire out of the test's
// hands: a fixture that invents them can agree with a defect the real
// device would expose.
func newFleetBlind(t *testing.T, w Writer) *Blind {
	t.Helper()
	const channelAddress = "VCU1223813:4"

	raw, err := os.ReadFile("testdata/hmip_fbl_blind_receiver_values.json")
	if err != nil {
		t.Fatalf("read blind descriptor: %v", err)
	}
	var descriptors map[string]hmproto.ParameterData
	if err := json.Unmarshal(raw, &descriptors); err != nil {
		t.Fatalf("decode blind descriptor: %v", err)
	}
	for _, p := range []hmenum.Parameter{hmenum.ParameterLevel, hmenum.ParameterLevel2} {
		if _, ok := descriptors[string(p)]; !ok {
			t.Fatalf("descriptor carries no %s — the fixture is not the blind receiver channel", p)
		}
	}

	dev := device.New(device.Config{
		Address: "VCU1223813", InterfaceID: "HmIP-RF",
		Interface: hmenum.InterfaceHmIPRF, Model: "HmIP-FBL",
	})
	ch := dev.AddChannel(channelAddress, 4, "BLIND_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	for _, p := range []hmenum.Parameter{hmenum.ParameterLevel, hmenum.ParameterLevel2} {
		ch.Put(generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    dev.InterfaceID,
				ChannelAddress: channelAddress,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: descriptors[string(p)],
			Writer:     w,
		}))
	}

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfile("IPCover"))
	if !ok {
		t.Fatal("IPCover constructor not registered")
	}
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPCover constructor: %v", err)
	}
	b, ok := dp.(*Blind)
	if !ok {
		t.Fatalf("IPCover constructor returned %T, want *Blind", dp)
	}
	if !b.Capabilities.SupportsStop {
		t.Fatal("the fleet blind does not advertise STOP — the spurious-STOP half cannot be observed")
	}
	neuterGoToTimers(&b.matterGoTo)
	return b
}

// confirmLevels feeds CCU-confirmed values for both axes, the way a push
// event from the device arrives.
func confirmLevels(t *testing.T, b *Blind, level, tilt float64) {
	t.Helper()
	if b.Float == nil || b.level2 == nil {
		t.Fatal("blind is missing LEVEL or LEVEL_2")
	}
	b.OnEvent(level)
	b.level2.OnEvent(tilt)
}

// stopCalls counts the STOP writes the blind emitted.
func stopCalls(w *putWriter) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.calls {
		if c.param == hmenum.ParameterStop {
			n++
		}
	}
	return n
}

// TestBlindPositionCommandCarriesTheObservedTiltAfterAnEarlierTiltCommand
// pins that a position-only command holds the slats where the device
// last reported them, not where a long-since-confirmed tilt command left
// its staged target.
//
// The staged target used to survive its own confirmation for the
// lifetime of the data point, so every later position move re-sent it:
// slats closed from the wall switch snapped back open the next time the
// operator touched the position slider, and the blind was additionally
// stopped mid-travel on a move it had not started.
func TestBlindPositionCommandCarriesTheObservedTiltAfterAnEarlierTiltCommand(t *testing.T) {
	w := &putWriter{}
	b := newFleetBlind(t, w)

	// Device reports position 30 %, slats 20 %.
	confirmLevels(t, b, 0.30, 0.20)

	// Operator opens the slats fully; the CCU confirms both axes.
	if err := b.SetTilt(context.Background(), 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTilt: %v", err)
	}
	confirmLevels(t, b, 0.30, 1.0)

	// Later the slats are closed from the wall switch.
	confirmLevels(t, b, 0.30, 0.0)

	// Operator now drags the position slider to 50 %.
	before := stopCalls(w)
	if err := b.SetPosition(context.Background(), 0.50, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}

	cc := w.combinedCalls()
	if len(cc) == 0 {
		t.Fatal("no COMBINED_PARAMETER write recorded")
	}
	last, _ := cc[len(cc)-1].value.(string)
	if last != "L2=0,L=50" {
		t.Errorf("COMBINED_PARAMETER = %q, want %q — the position move re-sent a tilt target the CCU "+
			"confirmed long ago instead of the slat position the device currently reports",
			last, "L2=0,L=50")
	}
	if got := stopCalls(w) - before; got != 0 {
		t.Errorf("STOP writes during the position move = %d, want 0 — the blind was standing still, so "+
			"nothing had to be stopped first", got)
	}
}

// TestBlindTiltCommandCarriesTheObservedPositionAfterAnEarlierPositionCommand
// is the symmetric case: a tilt-only command must hold the position axis
// at the device's reported level.
func TestBlindTiltCommandCarriesTheObservedPositionAfterAnEarlierPositionCommand(t *testing.T) {
	w := &putWriter{}
	b := newFleetBlind(t, w)

	confirmLevels(t, b, 0.20, 0.10)
	if err := b.SetPosition(context.Background(), 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	confirmLevels(t, b, 1.0, 0.10)
	// The cover is driven back down from a CCU program.
	confirmLevels(t, b, 0.0, 0.10)

	before := stopCalls(w)
	if err := b.SetTilt(context.Background(), 0.40, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTilt: %v", err)
	}

	cc := w.combinedCalls()
	last, _ := cc[len(cc)-1].value.(string)
	if last != "L2=40,L=0" {
		t.Errorf("COMBINED_PARAMETER = %q, want %q — the tilt move re-sent a stale position target",
			last, "L2=40,L=0")
	}
	if got := stopCalls(w) - before; got != 0 {
		t.Errorf("STOP writes during the tilt move = %d, want 0", got)
	}
}

// TestBlindStopsFirstWhileTheOtherAxisIsStillUnconfirmed pins the half of
// the reference behaviour that must survive: an axis the caller did not
// command, still carrying a write the CCU has not confirmed, means the
// blind is in motion — and blind actuators ignore new coordinates while
// moving, so they are stopped first.
func TestBlindStopsFirstWhileTheOtherAxisIsStillUnconfirmed(t *testing.T) {
	w := &putWriter{}
	b := newFleetBlind(t, w)
	confirmLevels(t, b, 0.30, 0.20)

	// A direct LEVEL_2 write (the slat number entity, or the combined
	// level data point) leaves an unconfirmed target on the tilt axis.
	if err := b.level2.Set(context.Background(), 0.90, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("LEVEL_2 set: %v", err)
	}
	before := stopCalls(w)
	if err := b.SetPosition(context.Background(), 0.50, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if got := stopCalls(w) - before; got != 1 {
		t.Errorf("STOP writes = %d, want 1 — the tilt axis still carried an unconfirmed target, so the "+
			"blind was moving and has to be stopped before new coordinates are accepted", got)
	}
	cc := w.combinedCalls()
	last, _ := cc[len(cc)-1].value.(string)
	if last != "L2=90,L=50" {
		t.Errorf("COMBINED_PARAMETER = %q, want %q — the in-flight tilt target is the one to hold",
			last, "L2=90,L=50")
	}
}

// TestBlindSetCombinedNeverStopsFirst pins that a command naming both
// axes goes straight to the wire: neither axis fell back to a pending
// target, so there is nothing to stop.
func TestBlindSetCombinedNeverStopsFirst(t *testing.T) {
	w := &putWriter{}
	b := newFleetBlind(t, w)
	confirmLevels(t, b, 0.30, 0.20)

	for range 3 {
		if err := b.SetCombined(context.Background(), 0.60, 0.70, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("SetCombined: %v", err)
		}
	}
	if got := stopCalls(w); got != 0 {
		t.Errorf("STOP writes = %d, want 0 — every command named both axes", got)
	}
}

// TestBlindFollowUpCommandCarriesTheJustCommandedOtherAxis pins the
// opposite failure from the staleness fix above: the axis the caller did
// not name in a follow-up command must carry the value the *previous*
// command just sent, even though the CCU has not echoed it back yet.
// Falling back to the last CCU-confirmed observation in that window
// treats "not yet confirmed" the same as "never commanded" and sends the
// untouched axis to 0 — closing a blind whose slats were just opened to
// 30 % closes the slats with it, because no wall-clock delay separates a
// command from its own echo in a fast client.
func TestBlindFollowUpCommandCarriesTheJustCommandedOtherAxis(t *testing.T) {
	w := &putWriter{}
	b := newFleetBlind(t, w)

	// Move to 50 % — no CCU echo is fed back, mirroring the real
	// round-trip delay between a command and its confirmation.
	if err := b.SetPosition(context.Background(), 0.50, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	// Tilt the slats before that write confirms — the level axis must
	// still read 50 %, not the device's pre-command default.
	if err := b.SetTilt(context.Background(), 0.30, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTilt: %v", err)
	}
	cc := w.combinedCalls()
	last, _ := cc[len(cc)-1].value.(string)
	if last != "L2=30,L=50" {
		t.Errorf("COMBINED_PARAMETER = %q, want %q — the level axis lost the value just commanded",
			last, "L2=30,L=50")
	}

	// Close — again before any echo — and check the symmetric case: the
	// tilt axis just commanded must carry forward into this write.
	if err := b.SetPosition(context.Background(), 0.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition (close): %v", err)
	}
	cc = w.combinedCalls()
	last, _ = cc[len(cc)-1].value.(string)
	if last != "L2=30,L=0" {
		t.Errorf("COMBINED_PARAMETER = %q, want %q — the tilt axis lost the value just commanded",
			last, "L2=30,L=0")
	}
}
