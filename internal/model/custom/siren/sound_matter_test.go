// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"
	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// newSpeakerRig builds a SoundPlayer whose DIRECTION and LEVEL are real data
// points, so the Speaker projection reads observed wire values rather than
// zero values.
func newSpeakerRig(t *testing.T) (*SoundPlayer, *generic.Sensor[int32], *generic.Float, *stubWriter) {
	t.Helper()
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3PSPK"})
	ch := d.AddChannel("MP3PSPK:2", 2, "SOUND_PLAYER", hmenum.ParamsetKeyValues)
	direction := attachEnumSensor(ch, hmenum.ParameterDirection, []string{"NONE", "UP", "DOWN"})
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Writer: w,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsWrite,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("1.0"),
		},
	})
	ch.Put(level)
	sp := NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: w})
	return sp, direction, level, w
}

func speakerServers(t *testing.T, sp *SoundPlayer) (onOff, level interfaces.MatterClusterServer) {
	t.Helper()
	servers := sp.MatterClusterServers()
	if len(servers) != 2 {
		t.Fatalf("MatterClusterServers() returned %d servers, want 2 (OnOff + LevelControl)", len(servers))
	}
	var on, lvl interfaces.MatterClusterServer
	for _, s := range servers {
		switch s.MatterClusterID() {
		case onoff.ClusterID:
			on = s
		case levelcontrol.ClusterID:
			lvl = s
		default:
			t.Fatalf("unexpected cluster 0x%04X on the Speaker endpoint", s.MatterClusterID())
		}
	}
	if on == nil || lvl == nil {
		t.Fatal("Speaker endpoint is missing one of its two mandatory clusters")
	}
	return on, lvl
}

// TestSoundPlayerProjectsSpeakerDeviceType pins the device type and its exact
// cluster set: matter.js speaker.element.ts:18-19 mandates OnOff and
// LevelControl and requires no Groups / ScenesManagement stubs.
func TestSoundPlayerProjectsSpeakerDeviceType(t *testing.T) {
	t.Parallel()

	sp, _, _, _ := newSpeakerRig(t)
	if got := sp.MatterDeviceType(); got != 0x0022 {
		t.Errorf("MatterDeviceType() = 0x%04X, want 0x0022 (Speaker)", got)
	}
	speakerServers(t, sp)
}

// TestSoundPlayerOnOffReadsDirection verifies the OnOff attribute is backed by
// a real wire value: DIRECTION UP/DOWN means sound is coming out.
func TestSoundPlayerOnOffReadsDirection(t *testing.T) {
	t.Parallel()

	sp, direction, _, _ := newSpeakerRig(t)
	on, _ := speakerServers(t, sp)

	v, ok := on.MatterRead(onoff.AttrOnOff)
	if !ok || v != false {
		t.Fatalf("unobserved OnOff read = (%v, %v), want (false, true)", v, ok)
	}
	fireDirection(t, direction, "UP")
	if v, ok = on.MatterRead(onoff.AttrOnOff); !ok || v != true {
		t.Fatalf("OnOff read after DIRECTION=UP = (%v, %v), want (true, true)", v, ok)
	}
	fireDirection(t, direction, "NONE")
	if v, ok = on.MatterRead(onoff.AttrOnOff); !ok || v != false {
		t.Fatalf("OnOff read after DIRECTION=NONE = (%v, %v), want (false, true)", v, ok)
	}
}

// TestSoundPlayerOnOffFeatureMapIsEmpty guards the conformance pair: Speaker
// mandates no OnOff feature, so the LT-gated attributes and commands must not
// be advertised alongside a FeatureMap that does not carry LT.
func TestSoundPlayerOnOffFeatureMapIsEmpty(t *testing.T) {
	t.Parallel()

	sp, _, _, _ := newSpeakerRig(t)
	on, _ := speakerServers(t, sp)

	fm, ok := on.MatterRead(matterAttrFeatureMap)
	if !ok || fm != uint32(0) {
		t.Fatalf("OnOff FeatureMap = (%v, %v), want (0, true)", fm, ok)
	}
	lister, ok := on.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("OnOff server does not list its attributes")
	}
	if got := lister.MatterAttributes(); len(got) != 1 || got[0] != onoff.AttrOnOff {
		t.Errorf("MatterAttributes() = %v, want [0x0000] only", got)
	}
	cmds, ok := on.(interfaces.MatterClusterCommandLister)
	if !ok {
		t.Fatal("OnOff server does not list its commands")
	}
	want := []uint32{onoff.CmdOff, onoff.CmdOn, onoff.CmdToggle}
	got := cmds.MatterAcceptedCommands()
	if len(got) != len(want) {
		t.Fatalf("MatterAcceptedCommands() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MatterAcceptedCommands() = %v, want %v", got, want)
		}
	}
}

// TestSoundPlayerOnOffCommandsReachTheWire proves the OnOff surface is not a
// stand-in: On starts playback and Off silences it, both through the profile's
// own write paths.
func TestSoundPlayerOnOffCommandsReachTheWire(t *testing.T) {
	t.Parallel()

	sp, _, level, w := newSpeakerRig(t)
	on, _ := speakerServers(t, sp)
	ctx := context.Background()

	level.OnEvent(0.75)
	if _, err := on.MatterInvoke(ctx, onoff.CmdOn, nil); err != nil {
		t.Fatalf("On: %v", err)
	}
	v, ok := w.has(hmenum.ParameterLevel)
	if !ok {
		t.Fatal("On wrote no LEVEL")
	}
	if got, isFloat := v.(float64); !isFloat || got != 0.75 {
		t.Errorf("On wrote LEVEL=%v, want the observed volume 0.75", v)
	}

	w2 := &stubWriter{}
	sp.writer = w2
	if _, err := on.MatterInvoke(ctx, onoff.CmdOff, nil); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if v, ok = w2.has(hmenum.ParameterLevel); !ok || v != float64(0) {
		t.Errorf("Off wrote LEVEL=%v (present=%v), want 0", v, ok)
	}
	if _, ok = w2.has(hmenum.ParameterDurationValue); !ok {
		t.Error("Off wrote no DURATION_VALUE; StopSound must clear the timer with the level")
	}
}

// TestSoundPlayerOnOffAttributeIsReadOnly pins access "R V"
// (matter.js on-off.element.ts:29) — the state moves through commands.
func TestSoundPlayerOnOffAttributeIsReadOnly(t *testing.T) {
	t.Parallel()

	sp, _, _, _ := newSpeakerRig(t)
	on, _ := speakerServers(t, sp)
	if err := on.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Error("write to the OnOff attribute succeeded, want a rejection")
	}
}

// TestSoundPlayerLevelReadsVolume verifies CurrentLevel is the LEVEL knob,
// reported as TLV null until the CCU has confirmed one (quality X).
func TestSoundPlayerLevelReadsVolume(t *testing.T) {
	t.Parallel()

	sp, _, level, _ := newSpeakerRig(t)
	_, lvl := speakerServers(t, sp)

	if v, ok := lvl.MatterRead(levelcontrol.AttrCurrentLevel); !ok || v != nil {
		t.Fatalf("unobserved CurrentLevel = (%v, %v), want (nil, true)", v, ok)
	}
	level.OnEvent(1.0)
	if v, ok := lvl.MatterRead(levelcontrol.AttrCurrentLevel); !ok || v != levelcontrol.LevelMax {
		t.Fatalf("CurrentLevel at LEVEL=1.0 = (%v, %v), want (%d, true)", v, ok, levelcontrol.LevelMax)
	}
}

// TestSoundPlayerMoveToLevelIsGatedWhileSilent pins the ExecuteIfOff gate: a
// plain MoveToLevel must not touch a silent player, while the WithOnOff
// variant drives it.
func TestSoundPlayerMoveToLevelIsGatedWhileSilent(t *testing.T) {
	t.Parallel()

	sp, direction, _, w := newSpeakerRig(t)
	ctx := context.Background()

	if err := sp.MoveToLevel(ctx, levelcontrol.MoveToLevelRequest{Level: 127}); err != nil {
		t.Fatalf("MoveToLevel while silent: %v", err)
	}
	if _, ok := w.has(hmenum.ParameterLevel); ok {
		t.Error("plain MoveToLevel wrote LEVEL while the player was silent")
	}

	fireDirection(t, direction, "UP")
	if err := sp.MoveToLevel(ctx, levelcontrol.MoveToLevelRequest{Level: levelcontrol.LevelMax}); err != nil {
		t.Fatalf("MoveToLevel while playing: %v", err)
	}
	if v, ok := w.has(hmenum.ParameterLevel); !ok || v != 1.0 {
		t.Errorf("MoveToLevel while playing wrote LEVEL=%v (present=%v), want 1.0", v, ok)
	}
}

// TestSoundPlayerMoveToLevelWithOnOffCouplesSilence pins the coupling: the
// minimum level is silence, so the coupled variant stops the player.
func TestSoundPlayerMoveToLevelWithOnOffCouplesSilence(t *testing.T) {
	t.Parallel()

	sp, _, _, w := newSpeakerRig(t)
	ctx := context.Background()

	if err := sp.MoveToLevelWithOnOff(ctx, levelcontrol.MoveToLevelRequest{Level: levelcontrol.LevelMin}); err != nil {
		t.Fatalf("MoveToLevelWithOnOff(0): %v", err)
	}
	if v, ok := w.has(hmenum.ParameterLevel); !ok || v != float64(0) {
		t.Errorf("MoveToLevelWithOnOff(0) wrote LEVEL=%v (present=%v), want 0", v, ok)
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Error("MoveToLevelWithOnOff(0) must stop the player, not only lower the volume")
	}
}

// TestSoundPlayerEligibilityIsPartial records why the Speaker projection is
// not complete: the sound file and repetition count have no Matter cluster.
func TestSoundPlayerEligibilityIsPartial(t *testing.T) {
	t.Parallel()

	sp, _, _, _ := newSpeakerRig(t)
	v := sp.MatterEligibility()
	if v.State != interfaces.MatterEligibilityPartial {
		t.Errorf("MatterEligibility().State = %v, want partial", v.State)
	}
	if v.DeviceType != 0x0022 {
		t.Errorf("MatterEligibility().DeviceType = 0x%04X, want 0x0022", v.DeviceType)
	}
	if len(v.Clusters) != 2 {
		t.Errorf("MatterEligibility().Clusters = %v, want the two mandatory Speaker clusters", v.Clusters)
	}
}
