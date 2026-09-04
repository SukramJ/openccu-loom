// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// colorDimmerWriter records every wire write of the RF colour dimmer.
type colorDimmerWriter struct {
	mu    sync.Mutex
	calls []struct {
		param hmenum.Parameter
		value any
	}
}

func (w *colorDimmerWriter) SetValue(
	_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, struct {
		param hmenum.Parameter
		value any
	}{p, v})
	return nil
}

// last returns the most recent value written to parameter.
func (w *colorDimmerWriter) last(parameter hmenum.Parameter) (any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out any
	found := false
	for _, c := range w.calls {
		if c.param == parameter {
			out, found = c.value, true
		}
	}
	return out, found
}

// loadRGBWWMChannel decodes one HM-LC-RGBW-WM VALUES paramset descriptor
// (testdata, extracted verbatim from the simulator's embedded
// description for VCU3747418).
func loadRGBWWMChannel(t *testing.T, name string) map[string]hmproto.ParameterData {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var out map[string]hmproto.ParameterData
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out
}

// newRGBWWMEffectLight builds the HM-LC-RGBW-WM three-channel group —
// LEVEL on :1, COLOR on :2, PROGRAM on :3 — from the device's own
// descriptors and runs it through the registered RfDimmer_Color
// constructor with the profile's real rebased channel fields.
//
// Both halves matter. The descriptors decide what the light can do:
// PROGRAM carries no VALUE_LIST and there is no HUE and no SATURATION on
// any channel, so a fixture that invents either makes the test agree
// with a surface the device cannot serve. The profile rebase decides
// where the constructor looks: COLOR and PROGRAM sit on sibling
// channels, and a light that only inspects its own finds neither.
func newRGBWWMEffectLight(t *testing.T) (*EffectLight, *colorDimmerWriter) {
	t.Helper()
	w := &colorDimmerWriter{}
	dev := device.New(device.Config{
		Address: "VCU3747418", InterfaceID: "BidCos-RF",
		Interface: hmenum.InterfaceBidCosRF, Model: "HM-LC-RGBW-WM",
	})

	put := func(chNo int, chType, file string, params ...hmenum.Parameter) *device.Channel {
		descriptors := loadRGBWWMChannel(t, file)
		addr := dev.Address + ":" + strconv.Itoa(chNo)
		ch := dev.AddChannel(addr, chNo, chType, hmenum.ParamsetKeyValues)
		for _, p := range params {
			desc, ok := descriptors[string(p)]
			if !ok {
				t.Fatalf("%s carries no %s", file, p)
			}
			key := hmtypes.DataPointKey{
				InterfaceID:    dev.InterfaceID,
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			}
			switch desc.Type {
			case hmenum.ParameterTypeFloat:
				ch.Put(generic.NewFloat(generic.Spec{Key: key, Descriptor: desc, Writer: w}))
			case hmenum.ParameterTypeInteger:
				ch.Put(generic.NewInteger(generic.Spec{Key: key, Descriptor: desc, Writer: w}))
			default:
				t.Fatalf("%s: unexpected descriptor type %v for %s", file, desc.Type, p)
			}
		}
		return ch
	}

	lightCh := put(1, "DIMMER", "hm_lc_rgbw_wm_ch1_dimmer_values.json", hmenum.ParameterLevel)
	put(2, "RGBW_COLOR", "hm_lc_rgbw_wm_ch2_color_values.json", hmenum.ParameterColor)
	put(3, "RGBW_AUTOMATIC", "hm_lc_rgbw_wm_ch3_automatic_values.json", hmenum.ParameterProgram)

	profileCfg, ok := custom.ProfileConfigs[hmenum.DeviceProfile("RfDimmer_Color")]
	if !ok || profileCfg == nil {
		t.Fatal("RfDimmer_Color has no profile config")
	}
	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfile("RfDimmer_Color"))
	if !ok {
		t.Fatal("RfDimmer_Color constructor not registered")
	}
	dp, err := ctor(lightCh, custom.RebaseChannelGroup(*profileCfg, lightCh.Number))
	if err != nil {
		t.Fatalf("RfDimmer_Color constructor: %v", err)
	}
	el, ok := dp.(*EffectLight)
	if !ok {
		t.Fatalf("RfDimmer_Color constructor returned %T, want *EffectLight", dp)
	}
	return el, w
}

// TestEveryAdvertisedEffectCanBeSelected drives the real set_effect
// service with every label the discovery payload offers Home Assistant.
//
// The device's PROGRAM parameter is a bare integer with no VALUE_LIST,
// so a payload that advertises names the lookup cannot resolve produces
// an effect picker in which every choice is rejected — the whole feature
// looks wired and is dead.
func TestEveryAdvertisedEffectCanBeSelected(t *testing.T) {
	t.Parallel()

	el, w := newRGBWWMEffectLight(t)
	_, body := el.HADiscoveryPayload(discoveryCtx{})
	list, _ := body["effect_list"].([]string)
	if len(list) == 0 {
		t.Fatal("the discovery payload advertises no effects for a device whose profile declares them")
	}
	for i, label := range list {
		if err := el.Invoke(context.Background(), "set_effect",
			map[string]any{"effect": label}, hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("set_effect(%q): %v — the payload advertises this effect and the command surface "+
				"refuses it", label, err)
			continue
		}
		got, ok := w.last(hmenum.ParameterProgram)
		if !ok {
			t.Errorf("set_effect(%q) wrote no PROGRAM value", label)
			continue
		}
		if got != int32(i) { //nolint:gosec // i is bounded by len(list)
			t.Errorf("set_effect(%q) wrote PROGRAM=%v, want %d — the index is the label's position in "+
				"the advertised list", label, got, i)
		}
	}
}

// TestReportedEffectIsTheLabelForTheObservedProgram pins the read half:
// a PROGRAM value the device reports has to come back out as the name
// the operator picked, not as an empty string.
func TestReportedEffectIsTheLabelForTheObservedProgram(t *testing.T) {
	t.Parallel()

	el, _ := newRGBWWMEffectLight(t)
	effects := el.Effects()
	if len(effects) < 2 {
		t.Fatalf("the light reports %d effects", len(effects))
	}
	el.program.OnEvent(2)
	idx, label, observed := el.Effect()
	if !observed || idx != 2 || label != effects[2] {
		t.Errorf("Effect() = (%d, %q, %v), want (2, %q, true)", idx, label, observed, effects[2])
	}
}

// TestAdvertisedEffectsAreTheActuatorsOwnVocabulary pins the seven
// programs the RF colour dimmer implements, in the order the wire index
// follows. The list is the reference's; getting the order wrong plays a
// different program than the one the operator selected.
func TestAdvertisedEffectsAreTheActuatorsOwnVocabulary(t *testing.T) {
	t.Parallel()

	el, _ := newRGBWWMEffectLight(t)
	want := []string{
		"Off", "Slow cycle", "Normal cycle", "Fast cycle",
		"Bonfire", "Waterfall", "TV simulation",
	}
	if got := el.Effects(); !slices.Equal(got, want) {
		t.Errorf("Effects() = %v, want %v", got, want)
	}
}

// TestColourCommandsReachTheDevicesSingleColorParameter pins that the
// colour mode the discovery payload declares is one the device can
// serve.
//
// HM-LC-RGBW-WM has no HUE and no SATURATION on any channel: its colour
// is one COLOR integer on the sibling channel the profile names. A light
// that resolves only HUE / SATURATION advertises the colour wheel and
// then answers every command with "channel missing HUE or SATURATION".
func TestColourCommandsReachTheDevicesSingleColorParameter(t *testing.T) {
	t.Parallel()

	el, w := newRGBWWMEffectLight(t)
	_, body := el.HADiscoveryPayload(discoveryCtx{})
	modes, _ := body["supported_color_modes"].([]string)
	if !slices.Contains(modes, "hs") {
		t.Fatalf("supported_color_modes = %v, want the hs mode the device can serve", modes)
	}

	for _, tc := range []struct {
		name       string
		hue        int32
		saturation float64
		want       int32
	}{
		{"red", 0, 100, 0},
		{"green", 120, 100, 66},
		{"blue", 240, 100, 133},
		{"white", 0, 0, colorIndexWhite},
	} {
		if err := el.Invoke(context.Background(), "set_color",
			map[string]any{"hue": tc.hue, "saturation": tc.saturation},
			hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("%s: set_color: %v", tc.name, err)
			continue
		}
		got, ok := w.last(hmenum.ParameterColor)
		if !ok {
			t.Errorf("%s: no COLOR write reached the device", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: COLOR = %v, want %d", tc.name, got, tc.want)
		}
	}
}

// TestObservedColorIsReportedBack pins the read half of the single-COLOR
// projection: the device reports one integer and the north-bound surface
// has to turn it into the hue/saturation pair it advertises.
func TestObservedColorIsReportedBack(t *testing.T) {
	t.Parallel()

	el, _ := newRGBWWMEffectLight(t)
	if el.colorIndex == nil {
		t.Fatal("the light bound no COLOR data point — the device's only colour axis is unreachable")
	}
	if _, _, observed := el.Color(); observed {
		t.Error("Color() reports an observation before the device sent one")
	}
	for _, tc := range []struct {
		raw     int32
		wantHue int32
		wantSat float64
	}{
		{0, 0, 100},
		// COLOR=100 reads as 180 under either span, so it cannot tell
		// the two apart; the three cases below can. The CCU divides by
		// the 199-wide hue circle, never by the white point.
		{100, 180, 100},
		{150, 271, 100},
		{colorIndexSpan, 360, 100},
		{colorIndexWhite, 0, 0},
	} {
		el.colorIndex.OnEvent(tc.raw)
		hue, sat, observed := el.Color()
		if !observed || hue != tc.wantHue || sat != tc.wantSat {
			t.Errorf("COLOR=%d → (%d, %v, %v), want (%d, %v, true)",
				tc.raw, hue, sat, observed, tc.wantHue, tc.wantSat)
		}
	}
}
