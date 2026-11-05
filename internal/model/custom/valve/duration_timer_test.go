// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package valve_test

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingParamsetWriter records every paramset write and every single write.
type recordingParamsetWriter struct {
	mu   sync.Mutex
	puts []map[string]any
	sets []hmenum.Parameter
}

func (w *recordingParamsetWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sets = append(w.sets, p)
	return nil
}

func (w *recordingParamsetWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.puts = append(w.puts, maps.Clone(values))
	return nil
}

// newWSMValve builds the valve through the real registry, with the write-only
// timer parameters that the channel carries on the firmware under test.
func newWSMValve(t *testing.T, w custom.Writer, timerParams ...hmenum.Parameter) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "000A1BE9957799",
		Model:        "HmIP-WSM",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	const addr = "000A1BE9957799:4"
	ch := dev.AddChannel(addr, 4, "WATER_SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)}
	}
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: key(hmenum.ParameterState),
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	for _, p := range timerParams {
		ch.Put(generic.NewInteger(generic.Spec{
			Key:        key(p),
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsWrite},
			Writer:     w,
		}))
	}
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return ch
}

// TestIrrigationOpenUsesTheTimerParametersTheFirmwareCarries pins both firmware
// generations of the valve. A channel read from an HmIP-WSM with firmware 1.4.12
// carries the write-only DURATION_VALUE and DURATION_UNIT and no ON_TIME, as does
// section 341 (WSM_V2) of the eQ-3 device documentation, crRFD 1.60.1. Section
// 340 (WSM_V1) carries ON_TIME.
func TestIrrigationOpenUsesTheTimerParametersTheFirmwareCarries(t *testing.T) {
	t.Parallel()

	const (
		onTime = string(hmenum.ParameterOnTime)
		value  = string(hmenum.ParameterDurationValue)
		unit   = string(hmenum.ParameterDurationUnit)
		state  = string(hmenum.ParameterState)
	)

	tests := []struct {
		name        string
		timerParams []hmenum.Parameter
		duration    time.Duration
		want        map[string]any
	}{
		{
			name:        "duration pair in seconds",
			timerParams: []hmenum.Parameter{hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit},
			duration:    10 * time.Minute,
			want:        map[string]any{value: int32(600), unit: int32(hmenum.TimerUnitSeconds), state: true},
		},
		{
			// The live description caps DURATION_VALUE at 16343.
			name:        "duration pair promoted to minutes",
			timerParams: []hmenum.Parameter{hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit},
			duration:    5 * time.Hour,
			want:        map[string]any{value: int32(300), unit: int32(hmenum.TimerUnitMinutes), state: true},
		},
		{
			name:        "older firmware with ON_TIME",
			timerParams: []hmenum.Parameter{hmenum.ParameterOnTime},
			duration:    10 * time.Minute,
			want:        map[string]any{onTime: float64(600), state: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingParamsetWriter{}
			v := irrigationOn(t, newWSMValve(t, w, tt.timerParams...))

			if err := v.Open(context.Background(), tt.duration, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("Open: %v", err)
			}

			if len(w.puts) != 1 || len(w.sets) != 0 {
				t.Fatalf("want one atomic paramset write, got puts=%v sets=%v", w.puts, w.sets)
			}
			if !maps.Equal(w.puts[0], tt.want) {
				t.Errorf("paramset = %#v, want %#v", w.puts[0], tt.want)
			}
		})
	}
}
