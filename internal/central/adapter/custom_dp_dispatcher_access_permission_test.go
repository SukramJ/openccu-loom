// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildAccessPermissionDP assembles the ACCESS_RECEIVER channel shape the
// access-permission switch is composed from: a read-only STATE binary sensor
// and the write-only ACCESS_AUTHORIZATION action-select.
func buildAccessPermissionDP(addr string, w *dispatchWriter) *switchdev.AccessPermission {
	chAddr := addr + ":2"
	dev := device.New(device.Config{Address: addr, InterfaceID: "test", Model: "HmIP-DLD"})
	ch := dev.AddChannel(chAddr, 2, "ACCESS_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterAccessAuthorization),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite,
			ValueList:  []string{"DISABLE", "ENABLE"},
		},
		Writer: w,
	}))
	return switchdev.NewAccessPermission(ch)
}

// TestDispatchAccessPermission verifies the switch operations reach the
// access-permission data point. It reports the SWITCH category, so every
// switch surface sends turn_on / turn_off / toggle at it — before the
// dispatcher knew the type, all three answered "unsupported data point type"
// and the tile did nothing.
func TestDispatchAccessPermission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   string
		want any
	}{
		{"turn on", "turn_on", "ENABLE"},
		{"turn off", "turn_off", "DISABLE"},
		// Unobserved STATE reads as "off", so a toggle grants the permission.
		{"toggle", "toggle", "ENABLE"},
	} {
		w := &dispatchWriter{}
		ap := buildAccessPermissionDP("ACC00"+tc.op, w)
		if ap == nil {
			t.Fatal("NewAccessPermission returned nil for a complete ACCESS_RECEIVER channel")
		}
		disp, spy := buildDispatcher(t, "ACC00"+tc.op, "ACCESS_AUTHORIZATION", ap)

		if err := disp.InvokeCustomDP(context.Background(), "ACC00"+tc.op, "ACCESS_AUTHORIZATION",
			tc.op, nil, hmenum.CommandPriorityHigh, "test"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		sc, ok := w.lastSet()
		if !ok {
			t.Fatalf("%s: no wire write", tc.name)
		}
		if sc.value != tc.want {
			t.Errorf("%s: wrote %v, want %v", tc.name, sc.value, tc.want)
		}
		if spy.count() != 1 {
			t.Errorf("%s: audit entries = %d, want 1", tc.name, spy.count())
		}
	}
}

// TestDispatchAccessPermissionUnknownOperation verifies an operation the
// switch does not carry is reported as an unknown operation rather than as an
// unsupported type.
func TestDispatchAccessPermissionUnknownOperation(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	ap := buildAccessPermissionDP("ACC099", w)
	disp, _ := buildDispatcher(t, "ACC099", "ACCESS_AUTHORIZATION", ap)

	err := disp.InvokeCustomDP(context.Background(), "ACC099", "ACCESS_AUTHORIZATION",
		"turn_on_for", map[string]any{"seconds": 10}, hmenum.CommandPriorityHigh, "test")
	if err == nil {
		t.Fatal("expected an error for an unsupported operation")
	}
}
