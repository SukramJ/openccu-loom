// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// A bounded light switch-on carries its duration in the command: `seconds`
// on turn_on and set_level becomes one put_paramset of LEVEL + ON_TIME. The
// alternative — staging the duration with set_timer_on_time and switching
// on in a second call — leaves a window in which a switch-on from another
// plane consumes the stage, and a set_level with brightness never consumes
// it at all.

func boundedOnRig(t *testing.T) (*Light, *putWriter) {
	t.Helper()
	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	return l, w
}

func onlyPut(t *testing.T, w *putWriter) map[string]any {
	t.Helper()
	if len(w.puts) != 1 {
		t.Fatalf("got %d put_paramset calls, want exactly 1 (LEVEL and ON_TIME in one write): %v", len(w.puts), w.puts)
	}
	return w.puts[0]
}

func assertOnTime(t *testing.T, put map[string]any, want float64) {
	t.Helper()
	if v, ok := put[string(hmenum.ParameterOnTime)].(float64); !ok || v != want {
		t.Errorf("ON_TIME=%v (%T), want %v: %v", put[string(hmenum.ParameterOnTime)], put[string(hmenum.ParameterOnTime)], want, put)
	}
	if _, ok := put[string(hmenum.ParameterLevel)]; !ok {
		t.Errorf("LEVEL missing — without it the CCU reads the write as a stand-alone timer update: %v", put)
	}
}

func TestLightTurnOnSecondsIsOneBoundedWrite(t *testing.T) {
	l, w := boundedOnRig(t)
	if err := l.Invoke(context.Background(), "turn_on", map[string]any{"seconds": 120.0}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	assertOnTime(t, onlyPut(t, w), 120)
}

func TestLightTurnOnSecondsSupersedesAStagedOnTime(t *testing.T) {
	l, w := boundedOnRig(t)
	l.SetTimerOnTime(30 * time.Second)
	if err := l.Invoke(context.Background(), "turn_on", map[string]any{"seconds": 120.0}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	assertOnTime(t, onlyPut(t, w), 120)

	// The staged 30 s must not survive to bound a later, unbounded switch-on.
	l.OnEvent(0.0)
	w.puts = nil
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn: %v", err)
	}
	for _, put := range w.puts {
		if _, ok := put[string(hmenum.ParameterOnTime)]; ok {
			t.Fatalf("a later plain TurnOn carried ON_TIME — the superseded stage leaked: %v", put)
		}
	}
}

func TestLightSetLevelBrightnessWithSecondsIsOneBoundedWrite(t *testing.T) {
	l, w := boundedOnRig(t)
	params := map[string]any{"state": "ON", "brightness": 128.0, "seconds": 60.0}
	if err := l.Invoke(context.Background(), "set_level", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	put := onlyPut(t, w)
	assertOnTime(t, put, 60)
	if lvl, ok := put[string(hmenum.ParameterLevel)].(float64); !ok || lvl != 128.0/255.0 {
		t.Errorf("LEVEL=%v, want %v", put[string(hmenum.ParameterLevel)], 128.0/255.0)
	}
}

func TestLightSetLevelSwitchOffIgnoresSeconds(t *testing.T) {
	l, w := boundedOnRig(t)
	l.OnEvent(0.8)
	if err := l.Invoke(context.Background(), "set_level", map[string]any{"brightness": 0.0, "seconds": 60.0}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	for _, put := range w.puts {
		if _, ok := put[string(hmenum.ParameterOnTime)]; ok {
			t.Fatalf("a switch-off carried ON_TIME: %v", put)
		}
	}
}

func TestLightSecondsMustBeAPositiveNumber(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service string
		params  map[string]any
	}{
		{"turn_on zero", "turn_on", map[string]any{"seconds": 0.0}},
		{"turn_on negative", "turn_on", map[string]any{"seconds": -5.0}},
		{"turn_on not a number", "turn_on", map[string]any{"seconds": "soon"}},
		{"set_level zero", "set_level", map[string]any{"state": "ON", "seconds": 0.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, w := boundedOnRig(t)
			err := l.Invoke(context.Background(), tc.service, tc.params, hmenum.CommandPriorityHigh)
			if !errors.Is(err, payload.ErrServiceInvalidParam) {
				t.Fatalf("err=%v, want ErrServiceInvalidParam", err)
			}
			if len(w.puts) != 0 {
				t.Fatalf("a rejected command reached the wire: %v", w.puts)
			}
		})
	}
}
