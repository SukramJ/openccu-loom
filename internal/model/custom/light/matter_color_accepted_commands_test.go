// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// contains reports whether id is present in cmds — a terse helper for the
// MatterAcceptedCommands assertions below.
func contains(cmds []uint32, id uint32) bool {
	for _, c := range cmds {
		if c == id {
			return true
		}
	}
	return false
}

// TestCTColorServerAcceptedCommandsIncludeMoveStepStop locks the
// regression: ctColorServer.MatterAcceptedCommands() must advertise the
// mandatory CT Move/Step/Stop commands (previously an empty list), on top
// of the pre-existing MoveToColorTemperature.
func TestCTColorServerAcceptedCommandsIncludeMoveStepStop(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	got := cc.MatterAcceptedCommands()
	for _, want := range []uint32{
		matterCmdColorMoveToColorTemperature,
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
	} {
		if !contains(got, want) {
			t.Errorf("ctColorServer.MatterAcceptedCommands() = %v, missing 0x%02X", got, want)
		}
	}
}

// TestCTColorServerNoOpCommandsAcceptedWithoutError locks the behavioral
// half of the fix: the mandatory CT Move/Step/Stop commands used to return
// errMatterUnknownCommand; they must now be accepted as no-ops.
func TestCTColorServerNoOpCommandsAcceptedWithoutError(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	for _, cmdID := range []uint32{
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
	} {
		resp, err := cc.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err != nil {
			t.Errorf("ctColorServer.MatterInvoke(0x%02X) err = %v, want nil (no-op)", cmdID, err)
		}
		if resp != nil {
			t.Errorf("ctColorServer.MatterInvoke(0x%02X) resp = %v, want nil", cmdID, resp)
		}
	}
}

// TestCTColorServerMoveToColorTemperatureStillDrivesWrite is the positive
// control: the accepted-command fix must not have disturbed the real
// MoveToColorTemperature command path.
func TestCTColorServerMoveToColorTemperatureStillDrivesWrite(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	if _, err := cc.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, uint16(370), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToColorTemperature err: %v", err)
	}
	if ch.Parameter(hmenum.ParameterColorTemperature) == nil {
		t.Fatal("COLOR_TEMPERATURE DP missing from channel")
	}
}

// TestHSColorServerAcceptedCommandsIncludeMoveStepStop locks the
// regression: hsColorServer.MatterAcceptedCommands() must advertise the
// mandatory HS Move/Step/Stop commands (previously an empty list), on top
// of the pre-existing MoveTo* trio.
func TestHSColorServerAcceptedCommandsIncludeMoveStepStop(t *testing.T) {
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	got := hs.MatterAcceptedCommands()
	for _, want := range []uint32{
		matterCmdColorMoveToHue,
		matterCmdColorMoveToSaturation,
		matterCmdColorMoveToHueAndSaturation,
		wire.ColorCtrlCmdMoveHue,
		wire.ColorCtrlCmdStepHue,
		wire.ColorCtrlCmdMoveSaturation,
		wire.ColorCtrlCmdStepSaturation,
		wire.ColorCtrlCmdStopMoveStep,
	} {
		if !contains(got, want) {
			t.Errorf("hsColorServer.MatterAcceptedCommands() = %v, missing 0x%02X", got, want)
		}
	}
}

// TestHSColorServerNoOpCommandsAcceptedWithoutError locks the behavioral
// half of the fix: the mandatory HS Move/Step/Stop commands used to return
// errMatterUnknownCommand; they must now be accepted as no-ops.
func TestHSColorServerNoOpCommandsAcceptedWithoutError(t *testing.T) {
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	for _, cmdID := range []uint32{
		wire.ColorCtrlCmdMoveHue,
		wire.ColorCtrlCmdStepHue,
		wire.ColorCtrlCmdMoveSaturation,
		wire.ColorCtrlCmdStepSaturation,
		wire.ColorCtrlCmdStopMoveStep,
	} {
		resp, err := hs.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err != nil {
			t.Errorf("hsColorServer.MatterInvoke(0x%02X) err = %v, want nil (no-op)", cmdID, err)
		}
		if resp != nil {
			t.Errorf("hsColorServer.MatterInvoke(0x%02X) resp = %v, want nil", cmdID, resp)
		}
	}
}

// TestRGBWColorServerAcceptedCommandsIncludeMoveStepStop locks the
// regression: rgbwColorServer.MatterAcceptedCommands() must advertise the
// full mandatory HS+CT Move/Step/Stop command set (previously an empty
// list), on top of the pre-existing four MoveTo* commands.
func TestRGBWColorServerAcceptedCommandsIncludeMoveStepStop(t *testing.T) {
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	got := rgbw.MatterAcceptedCommands()
	for _, want := range []uint32{
		matterCmdColorMoveToHue,
		matterCmdColorMoveToSaturation,
		matterCmdColorMoveToHueAndSaturation,
		matterCmdColorMoveToColorTemperature,
		wire.ColorCtrlCmdMoveHue,
		wire.ColorCtrlCmdStepHue,
		wire.ColorCtrlCmdMoveSaturation,
		wire.ColorCtrlCmdStepSaturation,
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
	} {
		if !contains(got, want) {
			t.Errorf("rgbwColorServer.MatterAcceptedCommands() = %v, missing 0x%02X", got, want)
		}
	}
	if got, want := len(got), 11; got != want {
		t.Errorf("rgbwColorServer.MatterAcceptedCommands() has %d entries, want %d", got, want)
	}
}

// TestRGBWColorServerNoOpCommandsAcceptedWithoutError locks the behavioral
// half of the fix: the mandatory HS+CT Move/Step/Stop commands used to
// return errMatterUnknownCommand; they must now be accepted as no-ops.
func TestRGBWColorServerNoOpCommandsAcceptedWithoutError(t *testing.T) {
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	for _, cmdID := range []uint32{
		wire.ColorCtrlCmdMoveHue,
		wire.ColorCtrlCmdStepHue,
		wire.ColorCtrlCmdMoveSaturation,
		wire.ColorCtrlCmdStepSaturation,
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
	} {
		resp, err := rgbw.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err != nil {
			t.Errorf("rgbwColorServer.MatterInvoke(0x%02X) err = %v, want nil (no-op)", cmdID, err)
		}
		if resp != nil {
			t.Errorf("rgbwColorServer.MatterInvoke(0x%02X) resp = %v, want nil", cmdID, resp)
		}
	}
}
