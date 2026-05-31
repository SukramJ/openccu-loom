// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildDeviceWithChannel0DP creates a Device with one channel-0 VALUES DP.
func buildDeviceWithChannel0DP(t *testing.T, addr string, param hmenum.Parameter) *Device {
	t.Helper()
	d := New(Config{InterfaceID: "HmIP-RF", Address: "VCU0001"})
	ch := d.AddChannel(addr, 0, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return d
}

// TestLoadInitialDataPoints_OnlyChannel0Relevant verifies that
// LoadInitialDataPoints only fetches RELEVANT_INIT_PARAMETERS on channel-0.
func TestLoadInitialDataPoints_OnlyChannel0Relevant(t *testing.T) {
	const ch0Addr = "VCU0001:0"
	d := buildDeviceWithChannel0DP(t, ch0Addr, hmenum.ParameterConfigPending)

	loader := newFakeLoader()
	loader.setGetValue(ch0Addr, hmenum.ParameterConfigPending, true, nil)
	d.SetValueLoader(loader)

	loaded, errored, err := d.LoadInitialDataPoints(context.Background(), hmenum.CallSourceHMInit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if errored != 0 {
		t.Fatalf("unexpected errored count: %d", errored)
	}
	if loaded != 1 {
		t.Fatalf("expected 1 loaded, got %d", loaded)
	}
}

// TestLoadInitialDataPoints_SkipsNonChannel0 verifies that a DP on channel 1
// with a relevant parameter is NOT fetched.
func TestLoadInitialDataPoints_SkipsNonChannel0(t *testing.T) {
	const ch1Addr = "VCU0001:1"
	d := buildDeviceWithChannel0DP(t, ch1Addr, hmenum.ParameterConfigPending)

	loader := newFakeLoader()
	d.SetValueLoader(loader)

	loaded, errored, err := d.LoadInitialDataPoints(context.Background(), hmenum.CallSourceHMInit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if errored != 0 || loaded != 0 {
		t.Fatalf("expected (0 loaded, 0 errored), got (%d, %d)", loaded, errored)
	}
}

// TestLoadInitialDataPoints_SkipsAlreadyObserved verifies that a DP that
// already has an observed value is not re-fetched.
func TestLoadInitialDataPoints_SkipsAlreadyObserved(t *testing.T) {
	const ch0Addr = "VCU0001:0"
	d := buildDeviceWithChannel0DP(t, ch0Addr, hmenum.ParameterConfigPending)

	// Pre-observe the DP via the channel so it's already populated.
	if ch := d.Channel(ch0Addr); ch != nil {
		if dp := ch.Parameter(hmenum.ParameterConfigPending); dp != nil {
			type wireValuer interface{ OnWireValue(any) bool }
			if wv, ok := dp.(wireValuer); ok {
				wv.OnWireValue(false)
			}
		}
	}

	loader := newFakeLoader()
	d.SetValueLoader(loader)

	loaded, _, err := d.LoadInitialDataPoints(context.Background(), hmenum.CallSourceHMInit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded != 0 {
		t.Fatalf("expected 0 loaded (already observed), got %d", loaded)
	}
}
