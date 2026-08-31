// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// brightnessPctGroupLevelDP installs a group-level float DP on its own channel
// and attaches it to l, the same shape the group-brightness cases use.
func brightnessPctGroupLevelDP(t *testing.T, l *Light) *generic.Float {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GRPPCT01"})
	ch := d.AddChannel("GRPPCT01:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	gl := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "GRPPCT01:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(gl)
	l.SetGroupLevel(gl)
	return gl
}

// TestBrightnessPctAccessorsAgreeAcrossTheGrid pins that every level→percent
// accessor answers with the one rule the model owns.
//
// The three used to be three expressions: Brightness.Pct dropped the fraction
// while both Light accessors added 0.5 first, so 0.29, 0.57, 0.58 and roughly
// five hundred further levels on the CCU's 0.001 grid came back one percent
// apart depending on which accessor a surface happened to call.
//
// Two limits this cannot cover, stated rather than implied. It compares the
// accessors against each other, so a change to the shared rule moves all three
// together and stays green — it enforces a single source, not a particular
// rounding. And none of the three has a production caller today, so it pins a
// rule rather than protecting a live path.
func TestBrightnessPctAccessorsAgreeAcrossTheGrid(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	groupLevel := brightnessPctGroupLevelDP(t, l)

	// The CCU reports LEVEL on a 0.01 grid, so walk all 101 of its values.
	for i := range 101 {
		lv := float64(i) / 100
		level.OnEvent(lv)
		groupLevel.OnEvent(lv)

		want := custom.NewBrightness(lv).Pct()
		got, ok := l.BrightnessPct()
		if !ok {
			t.Fatalf("level=%v: BrightnessPct() not observed", lv)
		}
		if got != want {
			t.Errorf("level=%v: BrightnessPct()=%d, Brightness.Pct()=%d", lv, got, want)
		}
		grp, ok := l.GroupBrightnessPct()
		if !ok {
			t.Fatalf("level=%v: GroupBrightnessPct() not observed", lv)
		}
		if grp != want {
			t.Errorf("level=%v: GroupBrightnessPct()=%d, Brightness.Pct()=%d", lv, grp, want)
		}
	}
}

// TestGroupBrightnessPctClampsOutOfRangeLevel pins the clamp the group
// percentage gained by going through the value object: a CCU that reports a
// LEVEL outside [0, 1] must not produce a percentage outside 0..100.
func TestGroupBrightnessPctClampsOutOfRangeLevel(t *testing.T) {
	t.Parallel()

	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	groupLevel := brightnessPctGroupLevelDP(t, l)

	groupLevel.OnEvent(1.5)
	if pct, ok := l.GroupBrightnessPct(); !ok || pct != 100 {
		t.Errorf("GroupBrightnessPct(1.5)=%d ok=%v, want 100", pct, ok)
	}
	groupLevel.OnEvent(-0.5)
	if pct, ok := l.GroupBrightnessPct(); !ok || pct != 0 {
		t.Errorf("GroupBrightnessPct(-0.5)=%d ok=%v, want 0", pct, ok)
	}
}
