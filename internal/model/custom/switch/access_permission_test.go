// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newTestAccessPermission builds an ACCESS_RECEIVER channel carrying a
// read-only STATE binary sensor and a write-only ACCESS_AUTHORIZATION
// action-select (VALUE_LIST DISABLE/ENABLE, backed by w), then calls
// NewAccessPermission(ch). It returns the custom DP together with the
// resolved STATE and ACCESS_AUTHORIZATION wire DPs for assertions.
func newTestAccessPermission(t *testing.T, addr string, w custom.Writer) (*AccessPermission, *generic.BinarySensor, *generic.ActionSelect) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0002"})
	ch := d.AddChannel(addr, 2, "ACCESS_RECEIVER", hmenum.ParamsetKeyValues)

	state := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	auth := generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterAccessAuthorization),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"DISABLE", "ENABLE"},
		},
		Writer: w,
	})
	ch.Put(state)
	ch.Put(auth)
	return NewAccessPermission(ch), state, auth
}

func TestAccessPermissionValueReflectsState(t *testing.T) {
	ap, state, _ := newTestAccessPermission(t, "VCU0002:2", &stubWriter{})
	if ap == nil {
		t.Fatal("NewAccessPermission returned nil")
	}
	if on, ok := ap.Value(); ok || on {
		t.Fatalf("unobserved: Value()=(%v,%v), want (false,false)", on, ok)
	}
	state.OnEvent(true)
	if on, ok := ap.IsOn(); !on || !ok {
		t.Fatalf("after STATE=true: IsOn()=(%v,%v), want (true,true)", on, ok)
	}
	state.OnEvent(false)
	if on, ok := ap.IsOn(); on || !ok {
		t.Fatalf("after STATE=false: IsOn()=(%v,%v), want (false,true)", on, ok)
	}
}

func TestAccessPermissionTurnOnSendsEnable(t *testing.T) {
	w := &stubWriter{}
	ap, state, _ := newTestAccessPermission(t, "VCU0002:3", w)
	state.OnEvent(false) // permission currently revoked
	if err := ap.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.lastParm != hmenum.ParameterAccessAuthorization || w.lastVal != "ENABLE" {
		t.Fatalf("TurnOn wrote %s=%v, want ACCESS_AUTHORIZATION=ENABLE", w.lastParm, w.lastVal)
	}
	if w.lastAddr != "VCU0002:3" {
		t.Fatalf("TurnOn wrote to %q, want VCU0002:3", w.lastAddr)
	}
}

func TestAccessPermissionTurnOffSendsDisable(t *testing.T) {
	w := &stubWriter{}
	ap, state, _ := newTestAccessPermission(t, "VCU0002:4", w)
	state.OnEvent(true) // permission currently granted
	if err := ap.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.lastParm != hmenum.ParameterAccessAuthorization || w.lastVal != "DISABLE" {
		t.Fatalf("TurnOff wrote %s=%v, want ACCESS_AUTHORIZATION=DISABLE", w.lastParm, w.lastVal)
	}
}

func TestAccessPermissionStateChangeGating(t *testing.T) {
	t.Run("turn_on suppressed when already granted", func(t *testing.T) {
		w := &stubWriter{}
		ap, state, _ := newTestAccessPermission(t, "VCU0002:5", w)
		state.OnEvent(true)
		if err := ap.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatal(err)
		}
		if w.lastVal != nil {
			t.Fatalf("TurnOn on already-granted permission wrote %s=%v, want no write", w.lastParm, w.lastVal)
		}
	})
	t.Run("turn_off suppressed when already revoked", func(t *testing.T) {
		w := &stubWriter{}
		ap, state, _ := newTestAccessPermission(t, "VCU0002:6", w)
		state.OnEvent(false)
		if err := ap.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatal(err)
		}
		if w.lastVal != nil {
			t.Fatalf("TurnOff on already-revoked permission wrote %s=%v, want no write", w.lastParm, w.lastVal)
		}
	})
	t.Run("first command goes through when unobserved", func(t *testing.T) {
		w := &stubWriter{}
		ap, _, _ := newTestAccessPermission(t, "VCU0002:7", w)
		if err := ap.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatal(err)
		}
		if w.lastVal != "ENABLE" {
			t.Fatalf("first TurnOn wrote %v, want ENABLE", w.lastVal)
		}
	})
}

func TestAccessPermissionAuthorizationConsumedHidden(t *testing.T) {
	_, _, auth := newTestAccessPermission(t, "VCU0002:8", &stubWriter{})
	usage, set := auth.ForcedUsage()
	if !set {
		t.Fatal("ACCESS_AUTHORIZATION has no forced usage; expected NO_CREATE")
	}
	if usage != hmenum.DataPointUsageNoCreate {
		t.Fatalf("ACCESS_AUTHORIZATION forced usage = %v, want %v", usage, hmenum.DataPointUsageNoCreate)
	}
}

func TestAccessPermissionKeyAndCategory(t *testing.T) {
	ap, _, _ := newTestAccessPermission(t, "VCU0002:9", &stubWriter{})
	if ap.Category() != hmenum.DataPointCategorySwitch {
		t.Fatalf("Category()=%v, want switch", ap.Category())
	}
	// The DP is keyed on ACCESS_AUTHORIZATION so per-channel naming appends a
	// distinguishing ` chN` suffix on the otherwise device-name-only channels.
	if ap.DataPointKey().Parameter != string(hmenum.ParameterAccessAuthorization) {
		t.Fatalf("DataPointKey().Parameter=%q, want ACCESS_AUTHORIZATION", ap.DataPointKey().Parameter)
	}
}

func TestNewAccessPermissionNilWhenFieldsAbsent(t *testing.T) {
	// A channel with only STATE (no un-ignored ACCESS_AUTHORIZATION) yields no
	// custom DP — mirrors the materializer skip for a missing required field.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0003"})
	ch := d.AddChannel("VCU0003:2", 2, "ACCESS_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0003:2",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	}))
	if ap := NewAccessPermission(ch); ap != nil {
		t.Fatal("NewAccessPermission returned non-nil with ACCESS_AUTHORIZATION absent")
	}
}
