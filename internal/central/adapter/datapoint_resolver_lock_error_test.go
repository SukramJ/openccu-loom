// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestResolveDataPointWithUnIgnoreSuppressesErrorByDefault pins the
// default behaviour: ERROR-prefixed parameters return nil so the
// HmIP-SMI / HmIP-SWDO / HmIP-eTRV ERROR_CODE-class parameters do not
// surface as standalone DPs.
func TestResolveDataPointWithUnIgnoreSuppressesErrorByDefault(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: "DEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ERROR_CODE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}
	if dp := resolveDataPointWithUnIgnore(cfg, false); dp != nil {
		t.Fatalf("ERROR_CODE without un_ignore must be nil, got %T", dp)
	}
}

// TestResolveDataPointWithUnIgnoreCreatesErrorWhenUnIgnored pins the
// Fix: HM-Sec-Key ERROR / HmIP-DLD ERROR_JAMMED are
// flagged as un_ignored by `unIgnoreParametersByDevice`, so the
// resolver must produce a DP — otherwise 4 lock-family devices lose
// Their ERROR* DP and the snapshot drifts vs.
func TestResolveDataPointWithUnIgnoreCreatesErrorWhenUnIgnored(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: "VCU0000146:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ERROR",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}
	dp := resolveDataPointWithUnIgnore(cfg, true)
	if dp == nil {
		t.Fatalf("ERROR with un_ignore must return a DP, got nil (HM-Sec-Key parity regression)")
	}
}

// TestResolveDataPointWithUnIgnoreImpulseAlwaysSuppressed verifies
// IMPULSE_EVENTS (SEQUENCE_OK) is suppressed regardless of the
// un_ignore flag — they have no DP equivalent on either side.
func TestResolveDataPointWithUnIgnoreImpulseAlwaysSuppressed(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: "DEV002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "SEQUENCE_OK",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}
	if dp := resolveDataPointWithUnIgnore(cfg, true); dp != nil {
		t.Fatalf("SEQUENCE_OK must be nil even with un_ignore, got %T", dp)
	}
}

// TestResolveReadonlyEnumIsIndexSensor pins the invariant every custom DP
// that reads an ENUM status depends on: a read-only ENUM parameter resolves
// to *generic.Sensor[int32] carrying the raw 0-based VALUE_LIST index, NOT
// *generic.Sensor[string]. Lock (LOCK_STATE / DIRECTION), SmokeSiren
// (SMOKE_DETECTOR_ALARM_STATUS) and peers therefore read it through
// custom.EnumSensorField + custom.EnumLabelValue. The former *Sensor[string]
// cast (custom.StringSensorField) never matched an int32 sensor, silently
// resolved to nil, and left the projected Matter attribute TLV-null forever —
// this test guards that regression at the resolver boundary.
func TestResolveReadonlyEnumIsIndexSensor(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "VCU0000001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LOCK_STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "LOCKED", "UNLOCKED"},
		},
	}
	dp := resolveDataPointWithUnIgnore(cfg, false)
	if _, ok := dp.(*generic.Sensor[int32]); !ok {
		t.Fatalf("read-only ENUM must resolve to *generic.Sensor[int32], got %T", dp)
	}
	if _, ok := dp.(*generic.Sensor[string]); ok {
		t.Fatal("read-only ENUM must NOT resolve to *generic.Sensor[string] (custom.StringSensorField nil-cast regression)")
	}
}

// TestResolveDataPointWithUnIgnoreErrorJammedHmIP verifies the HmIP
// lock branch: HmIP-DLD/HmIP-DLP ERROR_JAMMED on channel 0 must
// receive a DP when un_ignored.
func TestResolveDataPointWithUnIgnoreErrorJammedHmIP(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "VCU9724704:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ERROR_JAMMED",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}
	dp := resolveDataPointWithUnIgnore(cfg, true)
	if dp == nil {
		t.Fatalf("ERROR_JAMMED with un_ignore must return a DP, got nil (HmIP-DLD parity regression)")
	}
}
