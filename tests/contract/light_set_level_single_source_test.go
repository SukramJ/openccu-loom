// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestLightSetLevelPayloadIsSingleSourced pins the two north-bound seams a
// light command can arrive through against each other.
//
// A light's on/off/brightness/colour command reaches the domain two ways:
// Home Assistant posts the JSON-schema payload to the MQTT command topic,
// which lands on [adapter.MQTTCommandSink.InvokeChannelService] and from
// there on the light model's own service registry; the SPA, REST, the
// WebSocket API and the MQTT cdp-invoke topic all land on
// [adapter.CustomDPDispatcher.InvokeCustomDP]. Those two used to carry
// independent implementations of the same payload grammar and had drifted:
// the dispatcher's copy dropped colour, colour temperature and effect on
// the floor, so a colour pick over REST only toggled the lamp.
//
// The check drives the SAME payload through BOTH production seams against
// two freshly built, identical devices and compares what actually reached
// the wire — parameter names, values and order — plus how the error was
// classified. Each row additionally declares the write it must produce, so
// a fold that made both seams equally inert would not pass: a pure
// differential is green when both sides do nothing.
func TestLightSetLevelPayloadIsSingleSourced(t *testing.T) {
	t.Parallel()

	const prio = hmenum.CommandPriorityHigh

	cases := []struct {
		name string
		// led selects the HmIP-MP3P status LED instead of a plain
		// colour light; its on/off writes are an atomic put_paramset.
		led bool
		// seedLevel is the CCU-reported LEVEL the device starts from.
		// It matters: the model suppresses a write that would not
		// change state, so an "ON" row has to start off and vice versa.
		seedLevel float64
		params    map[string]any
		// wantParams is the set of wire parameters the command must
		// touch. Empty is only allowed together with wantErrClass.
		wantParams []string
		// wantErrClass is "" (success), "bad-param" or
		// "unknown-operation".
		wantErrClass string
	}{
		{
			name:       "state ON with brightness",
			params:     map[string]any{"state": "ON", "brightness": 128},
			wantParams: []string{"LEVEL"},
		},
		{
			name:       "state OFF",
			seedLevel:  0.8,
			params:     map[string]any{"state": "OFF"},
			wantParams: []string{"LEVEL"},
		},
		{
			name:       "brightness only",
			params:     map[string]any{"brightness": 200},
			wantParams: []string{"LEVEL"},
		},
		{
			name:       "state ON with colour",
			params:     map[string]any{"state": "ON", "color": map[string]any{"h": 120.0, "s": 80.0}},
			wantParams: []string{"HUE", "LEVEL", "SATURATION"},
		},
		{
			name:       "colour only",
			seedLevel:  0.5,
			params:     map[string]any{"color": map[string]any{"h": 10.0, "s": 50.0}},
			wantParams: []string{"HUE", "SATURATION"},
		},
		{
			name:         "colour temperature on a light with no colour-temperature axis",
			params:       map[string]any{"state": "ON", "color_temp_kelvin": 3000.0},
			wantParams:   []string{"LEVEL"},
			wantErrClass: "bad-param",
		},
		{
			name:         "effect on a light with no effect axis",
			params:       map[string]any{"state": "ON", "effect": "X"},
			wantParams:   []string{"LEVEL"},
			wantErrClass: "bad-param",
		},
		{
			name:       "legacy scalar level",
			params:     map[string]any{"level": 0.7},
			wantParams: []string{"LEVEL"},
		},
		{
			name:       "state matched case-insensitively",
			params:     map[string]any{"state": "oN"},
			wantParams: []string{"LEVEL"},
		},
		{
			name:       "brightness as a string",
			params:     map[string]any{"brightness": "128"},
			wantParams: []string{"LEVEL"},
		},
		{
			name:         "empty payload",
			params:       map[string]any{},
			wantErrClass: "bad-param",
		},
		{
			name:         "legacy level out of range",
			params:       map[string]any{"level": 5.0},
			wantErrClass: "bad-param",
		},
		{
			name:       "LED state ON",
			led:        true,
			params:     map[string]any{"state": "ON"},
			wantParams: []string{"COLOR", "LEVEL", "ON_TIME", "ON_TIME_LIST_1", "RAMP_TIME", "REPETITIONS"},
		},
		{
			name:       "LED state OFF",
			led:        true,
			seedLevel:  0.8,
			params:     map[string]any{"state": "OFF"},
			wantParams: []string{"COLOR", "ON_TIME"},
		},
		{
			name:       "LED state ON with colour",
			led:        true,
			params:     map[string]any{"state": "ON", "color": map[string]any{"h": 120.0, "s": 80.0}},
			wantParams: []string{"COLOR", "LEVEL", "ON_TIME", "ON_TIME_LIST_1", "RAMP_TIME", "REPETITIONS"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if len(tc.wantParams) == 0 && tc.wantErrClass == "" {
				t.Fatal("row declares neither a wire write nor an error class — it would assert nothing")
			}
			ctx := context.Background()

			cdpWriter := &lightSetLevelWriter{}
			cdpReg, addr := lightSetLevelRegistry(t, cdpWriter, tc.led, tc.seedLevel)
			svcWriter := &lightSetLevelWriter{}
			svcReg, _ := lightSetLevelRegistry(t, svcWriter, tc.led, tc.seedLevel)

			// Seam 1: SPA / REST / WebSocket / MQTT cdp-invoke.
			cdpErr := adapter.NewCustomDPDispatcher(cdpReg).InvokeCustomDP(
				ctx, addr, "LEVEL", "set_level", maps.Clone(tc.params), prio, "contract",
			)
			// Seam 2: the Home Assistant MQTT command topic.
			svcErr := adapter.NewMQTTCommandSink(svcReg, nil).InvokeChannelService(
				ctx, "ccu-01", "HmIP-RF", addr, 1, "set_level", maps.Clone(tc.params), prio,
			)

			cdpWrites := cdpWriter.rendered()
			svcWrites := svcWriter.rendered()
			if !slices.Equal(cdpWrites, svcWrites) {
				t.Errorf("the two seams wrote different things for %v:\n  cdp-invoke plane: %v\n  HA command topic: %v",
					tc.params, cdpWrites, svcWrites)
			}
			if got, want := lightSetLevelErrClass(cdpErr), lightSetLevelErrClass(svcErr); got != want {
				t.Errorf("error class differs for %v: cdp-invoke plane %q (%v), HA command topic %q (%v)",
					tc.params, got, cdpErr, want, svcErr)
			}

			if got := lightSetLevelErrClass(cdpErr); got != tc.wantErrClass {
				t.Errorf("error class = %q (%v), want %q", got, cdpErr, tc.wantErrClass)
			}
			// The REST layer classifies a malformed payload as 422 by
			// matching hmapi.ErrBadParam alone, so the dispatcher seam
			// has to translate the model's sentinel; without that the
			// same request degrades to a 502.
			if tc.wantErrClass == "bad-param" && !errors.Is(cdpErr, hmapi.ErrBadParam) {
				t.Errorf("cdp-invoke plane returned %v, which does not match hmapi.ErrBadParam — REST would answer 502, not 422", cdpErr)
			}

			if got := cdpWriter.params(); !slices.Equal(got, tc.wantParams) {
				t.Errorf("wire parameters = %v, want %v (writes: %v)", got, tc.wantParams, cdpWrites)
			}
		})
	}
}

// lightSetLevelErrClass folds an error into the class the north-bound
// layers act on. The two seams return different sentinel families by
// design — the dispatcher translates the model's payload sentinels into
// the hmapi ones REST and WebSocket classify on — so the comparison is on
// the class, not on the sentinel.
func lightSetLevelErrClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, hmapi.ErrUnknownOperation):
		return "unknown-operation"
	case errors.Is(err, hmapi.ErrBadParam),
		errors.Is(err, payload.ErrServiceMissingParam),
		errors.Is(err, payload.ErrServiceInvalidParam),
		errors.Is(err, payload.ErrUnknownServiceMethod):
		return "bad-param"
	default:
		return "other: " + err.Error()
	}
}

// lightSetLevelWriter records everything that reaches the wire, in order.
type lightSetLevelWriter struct {
	mu    sync.Mutex
	lines []string
	names []string
}

func (w *lightSetLevelWriter) SetValue(
	_ context.Context, addr string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, fmt.Sprintf("SET %s %s=%v", addr, p, v))
	w.names = append(w.names, string(p))
	return nil
}

func (w *lightSetLevelWriter) PutParamset(
	_ context.Context, addr string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := slices.Sorted(maps.Keys(values))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, values[k]))
		w.names = append(w.names, k)
	}
	w.lines = append(w.lines, fmt.Sprintf("PUT %s %s", addr, strings.Join(parts, ",")))
	return nil
}

// rendered returns the ordered wire calls.
func (w *lightSetLevelWriter) rendered() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}

// params returns the distinct wire parameter names, sorted, so a row can
// declare the effect it expects instead of only comparing two seams.
func (w *lightSetLevelWriter) params() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]string(nil), w.names...)
	sort.Strings(out)
	return slices.Compact(out)
}

// lightSetLevelRegistry builds a central registry holding one device whose
// channel carries a real light custom data point, wired to w.
func lightSetLevelRegistry(t *testing.T, w *lightSetLevelWriter, led bool, seedLevel float64) (registry *central.Registry, address string) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	addr := "LIGHT0001"
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "TestLight",
	})
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "DIMMER", hmenum.ParamsetKeyValues)
	levelDP := lightSetLevelFloatDP(chAddr, hmenum.ParameterLevel, w)
	ch.Put(levelDP)

	cfg := light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true}}
	var dp device.AttachableDataPoint
	if led {
		ch.Put(lightSetLevelSelectDP(chAddr, hmenum.ParameterColor, w,
			[]string{"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE"}))
		dp = light.NewSoundPlayerLED(cfg)
	} else {
		ch.Put(lightSetLevelIntDP(chAddr, hmenum.ParameterHue, w))
		ch.Put(lightSetLevelFloatDP(chAddr, hmenum.ParameterSaturation, w))
		dp = light.NewColorLight(cfg)
	}
	// Seed the CCU-reported level BEFORE the recording starts so the
	// state the model deduplicates against is real, not a write.
	levelDP.OnEvent(seedLevel)
	w.mu.Lock()
	w.lines, w.names = nil, nil
	w.mu.Unlock()

	ch.SetCustomDataPoint(dp)
	c.ModelRegistry.Put(dev)
	return reg, addr
}

func lightSetLevelFloatDP(address string, param hmenum.Parameter, w generic.Writer) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

func lightSetLevelIntDP(address string, param hmenum.Parameter, w generic.Writer) *generic.Integer {
	return generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

func lightSetLevelSelectDP(address string, param hmenum.Parameter, w generic.Writer, valueList []string) *generic.Select {
	return generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
		Writer: w,
	})
}
