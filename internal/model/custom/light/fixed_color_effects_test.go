// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The signal-light rig mirrors the HmIPW-WRC6 LED channel as the simulator
// describes it (godevccu paramset_descriptions/HmIPW-WRC6.json, channel :7):
// LEVEL, COLOR and COLOR_BEHAVIOUR enums with label-typed bounds, and a
// DURATION_VALUE / DURATION_UNIT timer. The HmIP-WRC6-230 of the report
// resolves to the same IPSimpleFixedColorLightWired profile.
var (
	wrc6ColorList = []string{
		"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE", "OLD_VALUE", "DO_NOT_CARE",
	}
	wrc6ColorBehaviourList = []string{
		"OFF", "ON", "BLINKING_SLOW", "BLINKING_MIDDLE", "BLINKING_FAST", "FLASH_SLOW", "FLASH_MIDDLE",
		"FLASH_FAST", "BILLOW_SLOW", "BILLOW_MIDDLE", "BILLOW_FAST", "OLD_VALUE", "DO_NOT_CARE",
	}
	// wrc6Effects is wrc6ColorBehaviourList without OFF, OLD_VALUE and
	// DO_NOT_CARE.
	wrc6Effects = []string{
		"ON", "BLINKING_SLOW", "BLINKING_MIDDLE", "BLINKING_FAST", "FLASH_SLOW", "FLASH_MIDDLE",
		"FLASH_FAST", "BILLOW_SLOW", "BILLOW_MIDDLE", "BILLOW_FAST",
	}
)

const signalAddr = "SIG0127:7"

// signalWriter records every SetValue and PutParamset with its address.
type signalWriter struct {
	mu   sync.Mutex
	sets []signalSet
	puts []signalPut
}

type signalSet struct {
	addr  string
	param hmenum.Parameter
	value any
}

type signalPut struct {
	addr   string
	values map[string]any
}

func (w *signalWriter) SetValue(_ context.Context, addr string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sets = append(w.sets, signalSet{addr: addr, param: p, value: v})
	return nil
}

func (w *signalWriter) PutParamset(_ context.Context, addr string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.puts = append(w.puts, signalPut{addr: addr, values: maps.Clone(values)})
	return nil
}

func (w *signalWriter) setsOf(p hmenum.Parameter) []signalSet {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []signalSet
	for _, s := range w.sets {
		if s.param == p {
			out = append(out, s)
		}
	}
	return out
}

// labelEnum adds an ENUM data point whose bounds are labels, as every HmIP
// signal-light enum declares them.
func labelEnum(ch *device.Channel, address string, p hmenum.Parameter, w Writer, valueList []string) {
	quote := func(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
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
			Min:        quote(valueList[0]),
			Max:        quote(valueList[len(valueList)-1]),
			Default:    quote(valueList[0]),
		},
		Writer: w,
	}))
}

// newSignalLight builds the light through the registered profile
// constructor, so the capability flags are the ones a running daemon sees.
func newSignalLight(t *testing.T, w *signalWriter, withBehaviour bool) *FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SIG0127"})
	ch := d.AddChannel(signalAddr, 7, "UNIVERSAL_LIGHT_RECEIVER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, signalAddr, hmenum.ParameterLevel, w)
	labelEnum(ch, signalAddr, hmenum.ParameterColor, w, wrc6ColorList)
	if withBehaviour {
		labelEnum(ch, signalAddr, hmenum.ParameterColorBehaviour, w, wrc6ColorBehaviourList)
	}
	putWritableFloat(ch, signalAddr, hmenum.ParameterDurationValue, w)
	putWritableInteger(ch, signalAddr, hmenum.ParameterDurationUnit, w)

	profile := hmenum.DeviceProfile("IPSimpleFixedColorLightWired")
	cfg := custom.ProfileConfigs[profile]
	ctor, ok := custom.DefaultRegistry().Constructor(profile)
	if cfg == nil || !ok {
		t.Fatalf("%s is not registered", profile)
	}
	dp, err := ctor(ch, custom.RebaseChannelGroup(*cfg, ch.Number))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	l, ok := dp.(*FixedColorLight)
	if !ok {
		t.Fatalf("constructor returned %T, want *FixedColorLight", dp)
	}
	return l
}

// observe sets the observed label of an enum data point of the light.
func observe(t *testing.T, dp *generic.Select, label string) {
	t.Helper()
	idx := slices.Index(dp.Descriptor.ValueList, label)
	if idx < 0 {
		t.Fatalf("%q is not in %v", label, dp.Descriptor.ValueList)
	}
	dp.OnEvent(int32(idx)) //nolint:gosec // test value list is short
}

func lastPut(t *testing.T, w *signalWriter) signalPut {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.puts) != 1 {
		t.Fatalf("put_paramset calls = %d, want exactly 1 (the switch-on bundle): %+v", len(w.puts), w.puts)
	}
	return w.puts[0]
}

// TestSignalLightAdvertisesTheEffectsItCarries pins the effect vocabulary of
// a signal light to its COLOR_BEHAVIOUR value list: the label list the
// device describes, without OFF, OLD_VALUE and DO_NOT_CARE. The light used to
// advertise effects unconditionally while its state carried no list and no
// set_effect existed (the 400 "Unknown operation" of the report).
func TestSignalLightAdvertisesTheEffectsItCarries(t *testing.T) {
	t.Parallel()
	l := newSignalLight(t, &signalWriter{}, true)

	if got := l.Effects(); !slices.Equal(got, wrc6Effects) {
		t.Fatalf("Effects() = %v, want %v", got, wrc6Effects)
	}
	if !l.Capabilities.SupportsEffects {
		t.Error("SupportsEffects = false on a channel that offers effects")
	}
	cfg, ok := l.Config().(*payload.EffectLightConfig)
	if !ok {
		t.Fatalf("Config() = %T, want *payload.EffectLightConfig", l.Config())
	}
	if !slices.Equal(cfg.Effects, wrc6Effects) || !cfg.SupportsEffects {
		t.Errorf("Config() = %+v, want the effect list and supports_effects", cfg)
	}
	if !slices.Contains(l.ServiceMethodNames(), "set_effect") {
		t.Errorf("set_effect is not registered: %v", l.ServiceMethodNames())
	}
}

// TestSignalLightWithoutColorBehaviourAdvertisesNoEffects is the other half:
// a fixed-colour channel without COLOR_BEHAVIOUR (the IPSimpleFixedColorLight
// family) advertises no effects and offers no set_effect.
func TestSignalLightWithoutColorBehaviourAdvertisesNoEffects(t *testing.T) {
	t.Parallel()
	l := newSignalLight(t, &signalWriter{}, false)

	if got := l.Effects(); got != nil {
		t.Errorf("Effects() = %v, want nil", got)
	}
	if l.Capabilities.SupportsEffects {
		t.Error("SupportsEffects = true on a channel without COLOR_BEHAVIOUR")
	}
	if _, ok := l.Config().(*payload.LightConfig); !ok {
		t.Errorf("Config() = %T, want the plain *payload.LightConfig", l.Config())
	}
	if slices.Contains(l.ServiceMethodNames(), "set_effect") {
		t.Error("set_effect is registered on a light that has no effects")
	}
	_, body := haEntity(t, l.HADiscoveryEntity(), discoveryCtx{})
	if _, ok := body["effect_list"]; ok {
		t.Errorf("discovery carries effect_list without effects: %v", body["effect_list"])
	}
}

// TestSignalLightStateReportsTheCurrentEffect: the state names the current
// COLOR_BEHAVIOUR when it is an effect, and nothing when it is not.
func TestSignalLightStateReportsTheCurrentEffect(t *testing.T) {
	t.Parallel()
	l := newSignalLight(t, &signalWriter{}, true)

	observe(t, l.colorBehaviour, "BLINKING_SLOW")
	st, ok := l.State().(*payload.FixedColorLightState)
	if !ok {
		t.Fatalf("State() = %T", l.State())
	}
	if st.Effect != "BLINKING_SLOW" {
		t.Errorf("state effect = %q, want BLINKING_SLOW", st.Effect)
	}

	observe(t, l.colorBehaviour, "OLD_VALUE")
	st, _ = l.State().(*payload.FixedColorLightState)
	if st.Effect != "" {
		t.Errorf("state effect = %q for OLD_VALUE, want none", st.Effect)
	}
}

// TestSignalLightSetEffectWritesColorBehaviour drives set_effect the way
// each plane sends it: a label (Home Assistant), an index as a numeric
// string (bare-scalar MQTT) and an index (REST / SPA).
func TestSignalLightSetEffectWritesColorBehaviour(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		arg  any
		want string
	}{
		{"label", "FLASH_FAST", "FLASH_FAST"},
		{"numeric string", "2", wrc6Effects[2]},
		{"index", 1, wrc6Effects[1]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &signalWriter{}
			l := newSignalLight(t, w, true)
			if err := l.Invoke(context.Background(), "set_effect", map[string]any{"effect": tc.arg}, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("set_effect(%v): %v", tc.arg, err)
			}
			sets := w.setsOf(hmenum.ParameterColorBehaviour)
			if len(sets) != 1 || sets[0].value != tc.want {
				t.Fatalf("COLOR_BEHAVIOUR writes = %+v, want one write of %q", sets, tc.want)
			}
		})
	}

	l := newSignalLight(t, &signalWriter{}, true)
	for _, bad := range []any{"OFF", "NOT_AN_EFFECT", 99} {
		err := l.Invoke(context.Background(), "set_effect", map[string]any{"effect": bad}, hmenum.CommandPriorityHigh)
		if !errors.Is(err, payload.ErrServiceInvalidParam) {
			t.Errorf("set_effect(%v) = %v, want ErrServiceInvalidParam", bad, err)
		}
	}
}

// TestSignalLightTurnOnShowsLight: a switch-on of a light whose colour shows
// nothing and whose behaviour is no effect writes WHITE and ON with LEVEL in
// one put_paramset, as the reference turn_on does.
func TestSignalLightTurnOnShowsLight(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	l.OnLevel(0)
	observe(t, l.color, "BLACK")
	observe(t, l.colorBehaviour, "OFF")

	if err := l.Invoke(context.Background(), "turn_on", map[string]any{}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	put := lastPut(t, w)
	if put.addr != signalAddr {
		t.Errorf("bundle address = %q, want %q", put.addr, signalAddr)
	}
	if put.values["COLOR"] != "WHITE" || put.values["COLOR_BEHAVIOUR"] != "ON" {
		t.Errorf("bundle = %v, want COLOR=WHITE and COLOR_BEHAVIOUR=ON", put.values)
	}
	if lv, _ := put.values["LEVEL"].(float64); lv <= 0 {
		t.Errorf("bundle LEVEL = %v, want > 0", put.values["LEVEL"])
	}
}

// TestSignalLightTurnOnKeepsTheSetColourAndEffect: a colour that shows
// something is left alone, and an effect that runs is written again.
func TestSignalLightTurnOnKeepsTheSetColourAndEffect(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	l.OnLevel(0)
	observe(t, l.color, "RED")
	observe(t, l.colorBehaviour, "BLINKING_FAST")

	if err := l.Invoke(context.Background(), "set_level", map[string]any{"state": "ON", "brightness": 128}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	put := lastPut(t, w)
	if _, has := put.values["COLOR"]; has {
		t.Errorf("bundle rewrites COLOR = %v, want it untouched", put.values["COLOR"])
	}
	if put.values["COLOR_BEHAVIOUR"] != "BLINKING_FAST" {
		t.Errorf("bundle COLOR_BEHAVIOUR = %v, want BLINKING_FAST", put.values["COLOR_BEHAVIOUR"])
	}
}

// TestSignalLightHACommandCarriesColourAndEffectInTheSwitchOn: Home
// Assistant sends state, colour and effect on one topic; all of it reaches
// the device as one put_paramset, not as a switch-on followed by changes.
func TestSignalLightHACommandCarriesColourAndEffectInTheSwitchOn(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	l.OnLevel(0)
	observe(t, l.color, "BLACK")
	observe(t, l.colorBehaviour, "OFF")

	cmd := map[string]any{"state": "ON", "effect": "BILLOW_SLOW", "color": map[string]any{"h": 240.0, "s": 100.0}}
	if err := l.Invoke(context.Background(), "set_level", cmd, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	put := lastPut(t, w)
	if put.values["COLOR"] != "BLUE" || put.values["COLOR_BEHAVIOUR"] != "BILLOW_SLOW" {
		t.Errorf("bundle = %v, want COLOR=BLUE and COLOR_BEHAVIOUR=BILLOW_SLOW", put.values)
	}
	if n := len(w.setsOf(hmenum.ParameterColorBehaviour)) + len(w.setsOf(hmenum.ParameterColor)); n != 0 {
		t.Errorf("%d separate COLOR / COLOR_BEHAVIOUR writes, want none", n)
	}
}

// TestSignalLightEffectOnABurningLight: when the light already burns at that
// level the switch-on is skipped, and the effect is still written — but
// not again when it is the one already running.
func TestSignalLightEffectOnABurningLight(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	l.OnLevel(1)
	observe(t, l.color, "RED")
	observe(t, l.colorBehaviour, "ON")

	if err := l.Invoke(context.Background(), "set_level", map[string]any{"state": "ON", "effect": "FLASH_SLOW"}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	sets := w.setsOf(hmenum.ParameterColorBehaviour)
	if len(sets) != 1 || sets[0].value != "FLASH_SLOW" {
		t.Fatalf("COLOR_BEHAVIOUR writes = %+v, want one write of FLASH_SLOW", sets)
	}

	observe(t, l.colorBehaviour, "FLASH_SLOW")
	if err := l.Invoke(context.Background(), "set_level", map[string]any{"state": "ON", "effect": "FLASH_SLOW"}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if sets := w.setsOf(hmenum.ParameterColorBehaviour); len(sets) != 1 {
		t.Errorf("COLOR_BEHAVIOUR written again although FLASH_SLOW already runs: %+v", sets)
	}
}

// TestSignalLightBrightnessSwitchOnResetsTheTimer: a switch-on at a given
// brightness cancels a running device-side timer like a plain switch-on
// does, as the reference turn_on resets it on every call.
func TestSignalLightBrightnessSwitchOnResetsTheTimer(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	l.OnLevel(0)

	if err := l.Invoke(context.Background(), "set_level", map[string]any{"state": "ON", "brightness": 128}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	put := lastPut(t, w)
	if _, has := put.values["DURATION_VALUE"]; !has {
		t.Errorf("bundle = %v, want the DURATION timer reset", put.values)
	}
}

// TestSignalLightSwitchOffDropsTheEffect: an effect sent with a switch-off
// is not written.
func TestSignalLightSwitchOffDropsTheEffect(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	l.OnLevel(1)

	if err := l.Invoke(context.Background(), "set_level", map[string]any{"state": "OFF", "effect": "FLASH_SLOW"}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if sets := w.setsOf(hmenum.ParameterColorBehaviour); len(sets) != 0 {
		t.Errorf("COLOR_BEHAVIOUR written on a switch-off: %+v", sets)
	}
}

// TestSignalLightEveryAdvertisedEffectCanBeSelected drives set_effect with
// every label the discovery payload offers Home Assistant.
func TestSignalLightEveryAdvertisedEffectCanBeSelected(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	l := newSignalLight(t, w, true)
	_, body := haEntity(t, l.HADiscoveryEntity(), discoveryCtx{})
	list := haStrings(t, body, "effect_list")
	if !slices.Equal(list, wrc6Effects) {
		t.Fatalf("discovery effect_list = %v, want %v", list, wrc6Effects)
	}
	if body["effect"] != true {
		t.Errorf("discovery effect = %v, want true", body["effect"])
	}
	for _, label := range list {
		if err := l.Invoke(context.Background(), "set_effect", map[string]any{"effect": label}, hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("set_effect(%q): %v", label, err)
		}
	}
	if sels := l.LocalisableSelections(); len(sels) != 1 || sels[0].Parameter != string(hmenum.ParameterColorBehaviour) {
		t.Errorf("LocalisableSelections() = %+v, want the COLOR_BEHAVIOUR effect list", sels)
	}
}

// TestHBWColourDimmerBindsColourOnItsFixedChannel builds the
// HBW-LC-RGBWW-IN6-DR colour dimmer through its registered profile. Its
// colour is a COLOR integer on channel 15 (godevccu
// paramset_descriptions/HBW-LC-RGBWW-IN6-DR.json: :13 DIMMER with LEVEL,
// :15 RGBW_COLOR with COLOR INTEGER 0..255), declared by the registration's
// extended fixed-channel fields, so the profile has to carry them to the
// constructor and the constructor has to build a colour dimmer, not a
// fixed-colour signal light.
func TestHBWColourDimmerBindsColourOnItsFixedChannel(t *testing.T) {
	t.Parallel()
	w := &signalWriter{}
	d := device.New(device.Config{InterfaceID: "BidCos-Wired", Address: "HBW0001"})
	dimmer := d.AddChannel("HBW0001:13", 13, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(dimmer, "HBW0001:13", hmenum.ParameterLevel, w)
	colour := d.AddChannel("HBW0001:15", 15, "RGBW_COLOR", hmenum.ParamsetKeyValues)
	putWritableInteger(colour, "HBW0001:15", hmenum.ParameterColor, w)

	var profile *custom.Profile
	for _, p := range custom.DefaultRegistry().GetConfigs("HBW-LC-RGBWW-IN6-DR") {
		if p.Name == "RfDimmer_Color_Fixed" && len(p.Channels) == 1 && p.Channels[0].Channel == 13 {
			profile = &p
			break
		}
	}
	if profile == nil {
		t.Fatal("no RfDimmer_Color_Fixed registration on channel 13 of the HBW-LC-RGBWW-IN6-DR")
	}
	ctor, ok := custom.DefaultRegistry().Constructor(profile.Name)
	if !ok {
		t.Fatal("RfDimmer_Color_Fixed has no constructor")
	}
	dp, err := ctor(dimmer, profile.Rebase(dimmer.Number))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	cl, ok := dp.(*ColorLight)
	if !ok {
		t.Fatalf("constructor returned %T, want *ColorLight", dp)
	}
	if cl.Capabilities.SupportsEffects {
		t.Error("the colour dimmer advertises effects it does not have")
	}
	if err := cl.SetColor(context.Background(), 120, 100, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetColor: %v", err)
	}
	sets := w.setsOf(hmenum.ParameterColor)
	if len(sets) != 1 || sets[0].addr != "HBW0001:15" {
		t.Fatalf("COLOR writes = %+v, want one write on HBW0001:15", sets)
	}
}
