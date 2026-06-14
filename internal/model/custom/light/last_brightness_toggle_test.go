// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newLightRigWithBehavior builds a dimmable Light whose owning device has
// the light-last-brightness toggle stamped. The toggle is read in
// [New], so the device must be configured before the Light is built.
func newLightRigWithBehavior(t *testing.T, w Writer, lightLastBrightness bool) (*Light, *generic.Float) {
	t.Helper()
	const address = "HmIP-BDT:4"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	d.SetCustomDPBehavior(lightLastBrightness, true)
	ch := d.AddChannel(address, 1, "DIMMER", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(level)
	l := New(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
	return l, level
}

// With the toggle on (default), a plain TurnOn after the light was
// dimmed-then-off restores the last non-zero level.
func TestLightTurnOnLastBrightnessEnabledRestoresLevel(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	l, level := newLightRigWithBehavior(t, w, true)

	level.OnEvent(0.4)
	level.OnEvent(0)
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 0.4 {
		t.Fatalf("enabled TurnOn wrote %v, want restored 0.4", w.last)
	}
}

// With the toggle off, the same sequence turns on at full (1.0)
// regardless of the tracked last level.
func TestLightTurnOnLastBrightnessDisabledUsesFull(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	l, level := newLightRigWithBehavior(t, w, false)

	level.OnEvent(0.4)
	level.OnEvent(0)
	// LastLevel still tracks 0.4 — only the turn-on target is forced full.
	if got := l.LastLevel(); got != 0.4 {
		t.Fatalf("LastLevel=%v, want 0.4 (tracking is independent of the toggle)", got)
	}
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 1.0 {
		t.Fatalf("disabled TurnOn wrote %v, want full 1.0", w.last)
	}
}
