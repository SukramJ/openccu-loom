// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// doorStateValueList is the VALUE_LIST the firmware declares for
// DOOR_STATE, read from the HmIP-MOD-HO VALUES paramset descriptor
// (channel 1): index 2 is VENTILATION_POSITION.
func doorStateValueList() []string {
	return []string{"CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"}
}

// enumStateDP is a VALUES data point that reports a raw ENUM value in
// whichever Go shape a transport handed it over. It carries the narrow
// contract [device.Channel] stores and EnumSelect.Subscribe reads.
type enumStateDP struct {
	param hmenum.Parameter
	raw   any
}

func (d *enumStateDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{Parameter: string(d.param)}
}
func (d *enumStateDP) Parameter() hmenum.Parameter { return d.param }
func (d *enumStateDP) ParameterData() hmproto.ParameterData {
	return hmproto.ParameterData{Type: "ENUM", ValueList: doorStateValueList()}
}
func (d *enumStateDP) RawValue() (any, bool)                    { return d.raw, true }
func (d *enumStateDP) ModifiedAt() time.Time                    { return time.Time{} }
func (d *enumStateDP) OnAnyUpdate(_ func(old, next any)) func() { return func() {} }

// TestEnumSelectResolvesEveryIntegerShapeOfAnEnumIndex drives
// EnumSelect.Subscribe — the production seam a channel invokes from
// AttachCalculatedDataPoint — with the same ENUM index delivered in the
// integer shapes different transports produce. All of them name the same
// VALUE_LIST token, so all of them must land the same mode.
//
// A resolver that type-switches over a subset silently drops the shapes
// it does not list: the value never lands and nothing logs.
func TestEnumSelectResolvesEveryIntegerShapeOfAnEnumIndex(t *testing.T) {
	t.Parallel()
	const idx = 2 // VENTILATION_POSITION in the firmware's VALUE_LIST
	cases := []struct {
		name string
		raw  any
	}{
		{"int32", int32(idx)},
		{"uint32", uint32(idx)},
		{"float32", float32(idx)},
		{"float64", float64(idx)},
		{"int", idx},
		{"label", "VENTILATION_POSITION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := &device.Channel{Address: "VCU0000001:1", Number: 1}
			ch.Put(&enumStateDP{param: "DOOR_STATE", raw: tc.raw})

			e := newGarageSelect(&recordingWriter{})
			if unsub := e.Subscribe(ch); unsub == nil {
				t.Fatal("Subscribe returned nil — the state parameter was not found")
			} else {
				defer unsub()
			}

			got, ok := e.Value()
			if !ok {
				t.Fatalf("no value landed for raw %T(%v)", tc.raw, tc.raw)
			}
			if got != "VENTILATION_POSITION" {
				t.Errorf("Value()=%q for raw %T(%v), want VENTILATION_POSITION", got, tc.raw, tc.raw)
			}
		})
	}
}
