// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The colour / kelvin / effect / on-time / ramp-time half of
// [Light.IsStateChangeFull] cannot suppress anything: the commanded-*
// accessors it consults are declared on *Light, the call resolves
// statically to those, and no subtype supplies the state — Go has no
// virtual dispatch, so a method on ColorLight could not be reached from
// inside (*Light).IsStateChangeFull either.
//
// This test pins that property so the doc comment and the code cannot
// drift apart again. It is deliberately a tripwire: implementing real
// suppression (a hook field on Light set by the subtype constructor, or
// an interface) must land together with an update here and to the
// comments at light.go's hook block, color.go SetColor and
// effect.go SetEffect.
func TestHmLgtColourStateChangeFullAlwaysReportsAChange(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := newColorRig(t, "x", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})

	// Drive level, hue and saturation so every on/off/brightness input
	// of the suppression rule IS observed and reports "no change".
	l.OnEvent(0.8)
	l.hue.OnEvent(int32(120))
	l.saturation.OnEvent(0.8)

	brightness := custom.NewBrightness(0.8).Byte()
	if l.IsStateChange(true, false, &brightness) {
		t.Fatal("precondition: the on/off + brightness half must report no change here")
	}

	hs := HSColor{Hue: 120, Saturation: 0.8}
	if !l.IsStateChangeFull(StateChangeArgsFull{TurnOn: true, Brightness: &brightness, HSColor: &hs}) {
		t.Fatal("IsStateChangeFull suppressed a colour command — the commanded-colour hooks now carry state; update the doc comments in light.go, color.go and effect.go, and this test")
	}

	// The measured consequence: a repeat of an already-observed colour
	// still reaches the CCU.
	before := len(w.calls)
	if err := l.SetColor(context.Background(), 120, 80, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("repeat SetColor: %v", err)
	}
	if len(w.calls) == before {
		t.Fatal("repeat SetColor no longer writes — suppression is implemented; update the doc comments and this test")
	}
}
