// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type colorStubWriter struct {
	mu    sync.Mutex
	calls []colorCall
}

type colorCall struct {
	param hmenum.Parameter
	value any
}

func (s *colorStubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, colorCall{p, v})
	return nil
}

func (s *colorStubWriter) last() colorCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func putWritableFloat(ch *device.Channel, address string, p hmenum.Parameter, w Writer) {
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
}

func putWritableInteger(ch *device.Channel, address string, p hmenum.Parameter, w Writer) {
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
}

// fixedColorValueList lists the eight COLOR labels in [FixedColor] index
// order (BLACK=0 .. WHITE=7). A CCU orders its own descriptor by the RGB bit
// pattern instead, which is why the wire value is the label and never an
// index — see [FixedColorLight.SetColorByName].
var fixedColorValueList = []string{
	"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE",
}

// putWritableSelect adds a *generic.Select DP to ch. Used for enum parameters
// like COLOR and EFFECT whose wire value is a string label on IP devices.
func putWritableSelect(ch *device.Channel, address string, p hmenum.Parameter, w Writer, valueList []string) {
	ch.Put(generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
		Writer: w,
	}))
}

func newColorRig(t *testing.T, address string, w Writer, caps custom.LightCapabilities) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	_ = caps // capabilities only matter to the Light wrapper
	return ch
}

// newColorTempRig constructs a channel with LEVEL + COLOR_TEMPERATURE data
// points — the minimum set a ColorTempLight needs. minK/maxK are unused at
// channel level but forwarded to callers that construct the Light.
func newColorTempRig(t *testing.T, address string, w Writer, _ custom.LightCapabilities, _, _ int32) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "COLOR_TEMP_DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterColorTemperature, w)
	return ch
}

// newEffectRig constructs a channel with LEVEL + HUE + SATURATION + PROGRAM
// data points for an EffectLight.
func newEffectRig(t *testing.T, address string, w Writer, _ custom.LightCapabilities) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "COLOR_DIMMER_EFFECT", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableInteger(ch, address, hmenum.ParameterProgram, w)
	return ch
}

// newRGBWRig constructs a channel with LEVEL + HUE + SATURATION +
// COLOR_TEMPERATURE + EFFECT data points for an RGBWLight.
func newRGBWRig(t *testing.T, address string, w Writer, _ custom.LightCapabilities) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableInteger(ch, address, hmenum.ParameterColorTemperature, w)
	putWritableInteger(ch, address, hmenum.ParameterEffect, w)
	return ch
}

func TestColorLightSetColorWraps(t *testing.T) {
	w := &colorStubWriter{}
	ch := newColorRig(t, "HmIP-RGBW:3", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	if err := l.SetColor(context.Background(), 720, 0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// HUE must be 0 after double wrap.
	for _, c := range w.calls {
		if c.param == hmenum.ParameterHue && c.value.(int32) != 0 {
			t.Fatalf("hue=%v", c.value)
		}
	}
}

func TestColorLightSaturationClamp(t *testing.T) {
	w := &colorStubWriter{}
	ch := newColorRig(t, "x", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	// Over-range input (150 > 100) must clamp to 100; the wire value written to
	// the SATURATION DP is saturation/100 = 1.0.
	if err := l.SetColor(context.Background(), 120, 150, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// Saturation clamped to 1.0 on wire: the writer's last SATURATION call records the wire value.
	for _, c := range w.calls {
		if c.param == hmenum.ParameterSaturation && c.value.(float64) != 1 {
			t.Fatalf("sat=%v", c.value)
		}
	}
}

func TestColorTempLightClamps(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
	putWritableInteger(ch, "x", hmenum.ParameterColorTemperature, w)

	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true}}, 2700, 6500)
	if err := l.SetKelvin(context.Background(), 100, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.value.(int32) != 2700 {
		t.Fatalf("low clamp=%v", got.value)
	}
	if err := l.SetKelvin(context.Background(), 99_999, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.value.(int32) != 6500 {
		t.Fatalf("high clamp=%v", got.value)
	}
}

func TestFixedColorLightSet(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "x", hmenum.ParameterColor, w, fixedColorValueList)

	l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})
	if err := l.SetColor(context.Background(), FixedColorMagenta, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// SetColor sends the string label "PURPLE" (FixedColorMagenta → "PURPLE").
	if got := w.last(); got.param != hmenum.ParameterColor || got.value.(string) != "PURPLE" {
		t.Fatalf("last=%+v", got)
	}
}

func TestFixedColorLightSetByName(t *testing.T) {
	// The descriptor a CCU reports is ordered by the RGB bit pattern, not by
	// FixedColor — a caller holding only that list has to address the slot by
	// name, and the name must reach the wire unchanged.
	ccuOrder := []string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE"}

	for _, name := range ccuOrder {
		w := &colorStubWriter{}
		d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
		ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
		putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
		putWritableSelect(ch, "x", hmenum.ParameterColor, w, ccuOrder)

		l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})
		if err := l.SetColorByName(context.Background(), name, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("SetColorByName(%q): %v", name, err)
		}
		if got := w.last(); got.param != hmenum.ParameterColor || got.value.(string) != name {
			t.Fatalf("SetColorByName(%q) wrote %+v", name, got)
		}
	}
}

func TestFixedColorLightSetByNameRejectsUnknown(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "x", hmenum.ParameterColor, w, fixedColorValueList)

	l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})
	if err := l.SetColorByName(context.Background(), "MAUVE", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("SetColorByName with an unknown name: want error, got nil")
	}
}

// TestColorLightAttachesHSColorDP verifies that NewColorLight attaches an HSColor
// combined DP that appears in channel.CombinedDataPoints().
func TestColorLightAttachesHSColorDP(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	ch := newColorRig(t, "HmIP-RGBW:3", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	_ = NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})

	cdps := ch.CombinedDataPoints()
	var found bool
	for _, cdp := range cdps {
		if m, ok := cdp.(device.CombinedDataPoint); ok && m.IsCombined() {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an HSColor combined DP in CombinedDataPoints(), got %d DPs", len(cdps))
	}
}
