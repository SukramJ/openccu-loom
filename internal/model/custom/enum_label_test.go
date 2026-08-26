// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestEnumLabelResolution pins the read-only-ENUM label accessors custom DPs
// depend on (Lock's LOCK_STATE / DIRECTION, SmokeSiren's status, …). The
// resolver projects a read-only ENUM onto a raw-index *generic.Sensor[int32];
// EnumSensorField must find it (where the old StringSensorField's *Sensor[string]
// cast silently returned nil) and EnumLabelValue / EnumLabelIndex must convert
// between the index and its VALUE_LIST label.
func TestEnumLabelResolution(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0000001"})
	ch := d.AddChannel("VCU0000001:1", 1, "DOOR_LOCK_STATE_TRANSMITTER", hmenum.ParamsetKeyValues)
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0000001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLockState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "LOCKED", "UNLOCKED"},
		},
	})
	ch.Put(dp)

	got := EnumSensorField(ch, hmenum.ParameterLockState)
	if got == nil {
		t.Fatal("EnumSensorField returned nil for a read-only ENUM index sensor")
	}

	// Unobserved → no label.
	if label, ok := EnumLabelValue(got); ok {
		t.Errorf("EnumLabelValue on unobserved sensor = (%q, true), want (\"\", false)", label)
	}

	// Wire index 1 resolves to its VALUE_LIST label.
	got.OnEvent(1)
	if label, ok := EnumLabelValue(got); !ok || label != "LOCKED" {
		t.Errorf("EnumLabelValue after index 1 = (%q, %v), want (LOCKED, true)", label, ok)
	}

	// Inverse: label → index.
	if idx, ok := EnumLabelIndex(got, "UNLOCKED"); !ok || idx != 2 {
		t.Errorf("EnumLabelIndex(UNLOCKED) = (%d, %v), want (2, true)", idx, ok)
	}
	if idx, ok := EnumLabelIndex(got, "NOT_A_STATE"); ok {
		t.Errorf("EnumLabelIndex for a label outside VALUE_LIST = (%d, true), want (_, false)", idx)
	}

	// Nil safety for both directions.
	if _, ok := EnumLabelValue(nil); ok {
		t.Error("EnumLabelValue(nil) must be (\"\", false)")
	}
	if _, ok := EnumLabelIndex(nil, "LOCKED"); ok {
		t.Error("EnumLabelIndex(nil, …) must be (-1, false)")
	}
}
