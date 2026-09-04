// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ccuFixedColorValueList is the COLOR value list a CCU actually reports. The
// order is the RGB bit pattern (bit 0 blue, bit 1 green, bit 2 red), not the
// order [FixedColor] enumerates, so the slot a raw index denotes can only be
// resolved through the label.
var ccuFixedColorValueList = []string{
	"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE",
}

// TestFixedColorLightReadsSlotByLabelNotByIndex pins that an observed COLOR
// index is resolved through the device's own VALUE_LIST. Four of the eight
// slots differ between the CCU's order and [FixedColor]'s, so an
// implementation that casts the raw index reports the wrong colour for each
// of them.
func TestFixedColorLightReadsSlotByLabelNotByIndex(t *testing.T) {
	for _, tc := range []struct {
		wireIndex int32
		want      FixedColor
		wantName  string
	}{
		{0, FixedColorBlack, "BLACK"},
		{1, FixedColorBlue, "BLUE"},
		{2, FixedColorGreen, "GREEN"},
		{3, FixedColorCyan, "TURQUOISE"},
		{4, FixedColorRed, "RED"},
		{5, FixedColorMagenta, "PURPLE"},
		{6, FixedColorYellow, "YELLOW"},
		{7, FixedColorWhite, "WHITE"},
	} {
		w := &colorStubWriter{}
		d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
		ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
		putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
		putWritableSelect(ch, "x", hmenum.ParameterColor, w, ccuFixedColorValueList)
		l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})

		l.color.OnEvent(tc.wireIndex)

		got, ok := l.Color()
		if !ok {
			t.Fatalf("index %d: no value observed", tc.wireIndex)
		}
		if got != tc.want {
			t.Errorf("index %d: Color() = %v, want %v", tc.wireIndex, got, tc.want)
		}
		name, ok := l.ColorName()
		if !ok || name != tc.wantName {
			t.Errorf("index %d: ColorName() = %q,%v, want %q", tc.wireIndex, name, ok, tc.wantName)
		}
	}
}

// TestFixedColorLightRejectsSlotOutsideTheKnownEight pins that a label the
// eight-colour set does not carry — the CCU appends RANDOM, OLD_VALUE and
// DO_NOT_CARE to the writable list — is reported as unobserved rather than
// cast to a nonsense ordinal.
func TestFixedColorLightRejectsSlotOutsideTheKnownEight(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "x", hmenum.ParameterColor, w,
		append(append([]string{}, ccuFixedColorValueList...), "RANDOM", "OLD_VALUE", "DO_NOT_CARE"))
	l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})

	l.color.OnEvent(8) // RANDOM

	if got, ok := l.Color(); ok {
		t.Errorf("Color() = %v,true for RANDOM, want ok=false", got)
	}
	if name, ok := l.ColorName(); ok {
		t.Errorf("ColorName() = %q,true for RANDOM, want ok=false", name)
	}
}

// TestFixedColorLightOptimisticUpdateUsesTheDeviceIndex pins that the
// optimistic local update after a write stores the index the device will echo
// — the position of the label in the device's VALUE_LIST — rather than the
// [FixedColor] ordinal.
func TestFixedColorLightOptimisticUpdateUsesTheDeviceIndex(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "x", hmenum.ParameterColor, w, ccuFixedColorValueList)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})

	if err := l.SetColor(context.Background(), FixedColorRed, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.Color(); got != FixedColorRed {
		t.Errorf("after SetColor(RED): Color() = %v, want RED", got)
	}
	// The stored raw index must be the device's slot for RED, which is 4.
	if raw, _ := l.color.Value(); raw != 4 {
		t.Errorf("stored raw index = %d, want 4 (the device's RED slot)", raw)
	}
}
