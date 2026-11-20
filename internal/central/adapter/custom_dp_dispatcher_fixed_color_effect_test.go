// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// buildSignalLightDP builds a fixed-colour signal light whose channel carries
// COLOR_BEHAVIOUR, the parameter its effects are. The value list is the
// HmIPW-WRC6 LED channel's (godevccu paramset_descriptions/HmIPW-WRC6.json,
// channel :7).
func buildSignalLightDP(t *testing.T, addr string, w *dispatchWriter) *light.FixedColorLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "UNIVERSAL_LIGHT_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(selectDP(addr+":1", hmenum.ParameterColor, w,
		[]string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE", "OLD_VALUE", "DO_NOT_CARE"}))
	ch.Put(selectDP(addr+":1", hmenum.ParameterColorBehaviour, w,
		[]string{
			"OFF", "ON", "BLINKING_SLOW", "BLINKING_MIDDLE", "BLINKING_FAST", "FLASH_SLOW", "FLASH_MIDDLE",
			"FLASH_FAST", "BILLOW_SLOW", "BILLOW_MIDDLE", "BILLOW_FAST", "OLD_VALUE", "DO_NOT_CARE",
		}))
	return light.NewFixedColorLight(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
}

// TestDispatchFixedColorLight_SetEffectWritesColorBehaviour is the reproducer
// of the HmIP-WRC6-230 report: set_effect on a signal light came back as
// ErrUnknownOperation (REST 400 "Unknown operation") although the light
// advertised effects. Both argument spellings reach COLOR_BEHAVIOUR: the
// service method's own `effect` (the client) and the `label` the other set_*
// operations of this dispatcher take.
func TestDispatchFixedColorLight_SetEffectWritesColorBehaviour(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"effect", map[string]any{"effect": "ON"}},
		{"label", map[string]any{"label": "ON"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addr := "SIGE" + tc.name
			w := &dispatchWriter{}
			l := buildSignalLightDP(t, addr, w)
			disp, _ := buildDispatcher(t, addr, "LEVEL", l)

			if err := disp.InvokeCustomDP(context.Background(), addr, "LEVEL", "set_effect", tc.params, hmenum.CommandPriorityHigh, "test"); err != nil {
				t.Fatalf("set_effect: %v", err)
			}
			sets := w.setsFor(hmenum.ParameterColorBehaviour)
			if len(sets) != 1 {
				t.Fatalf("COLOR_BEHAVIOUR writes = %d, want 1", len(sets))
			}
		})
	}
}

// TestDispatchFixedColorLight_SetEffectWithoutEffectsIsUnknown: a fixed-colour
// light whose channel has no COLOR_BEHAVIOUR has no effects, and set_effect
// stays an unknown operation for it.
func TestDispatchFixedColorLight_SetEffectWithoutEffectsIsUnknown(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildFixedColorLightDP(t, "FCLE001", w)
	disp, _ := buildDispatcher(t, "FCLE001", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "FCLE001", "LEVEL", "set_effect", map[string]any{"effect": "ON"}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("set_effect = %v, want ErrUnknownOperation", err)
	}
}
