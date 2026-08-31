// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"maps"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// weekprofileLocksTargets is the target-channel set both the live profile and
// the reference twin are configured with, so the two decode the same key set.
func weekprofileLocksTargets() map[string]weekprofile.TargetChannelInfo {
	return map[string]weekprofile.TargetChannelInfo{
		"1_1": {ChannelNo: 4, ChannelType: "primary"},
		"1_2": {ChannelNo: 5, ChannelType: "secondary"},
		"2_1": {ChannelNo: 6, ChannelType: "primary"},
	}
}

// TestWeekprofileLocksWireDecodeGoesThroughTheModel pins the event bridge's
// WEEK_PROGRAM_CHANNEL_LOCKS handling to the week-profile data point's own
// decode.
//
// The bridge used to convert the wire value itself and hand the model only a
// pre-decoded map, so the two decodes could disagree on inputs neither side
// documents — a negative value made the bridge report every schedule switch
// OFF while the model's own rule leaves the state alone. The observation here
// comes from the live snapshot path (adapter.NewEventBridge →
// PublishCentralSnapshot → the locks subscription); the expectation comes
// from a twin data point fed the identical sequence through the model method.
// Neither side carries a literal bitfield verdict.
func TestWeekprofileLocksWireDecodeGoesThroughTheModel(t *testing.T) {
	t.Parallel()
	const (
		centralName = "ccu-01"
		interfaceID = "HmIP-RF"
		address     = "00WPDECODE1"
	)
	ctx := context.Background()

	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	d := device.New(device.Config{
		Address:     address,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
		Model:       "HmIP-BSM",
	})
	d.AddChannel(address+":4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	schedCh := d.AddChannel(address+":8", 8, "SWITCH_WEEK_PROFILE", hmenum.ParamsetKeyMaster)
	locksDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    interfaceID,
			ChannelAddress: schedCh.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterWeekProgramChannelLocks),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	schedCh.Put(locksDP)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    centralName,
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	wp.SetAvailableTargetChannels(weekprofileLocksTargets())
	schedCh.AttachWeekProfile(wp)
	c.ModelRegistry.Put(d)
	// The snapshot walk skips a central that has not latched its
	// southbound bring-up, which is the production gate — not a shortcut.
	c.MarkSouthboundReady()

	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: centralName, RawEnabled: true,
	}, mqtt.NewNoopClient())
	eb := adapter.NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	t.Cleanup(eb.Stop)
	// Start before the snapshot: the live subscriptions the snapshot wires
	// are only retained on a started bridge, which is the daemon's order.
	eb.Start(ctx)
	eb.PublishCentralSnapshot(ctx, centralName)

	// The twin never touches the bridge; it is the model's answer to the
	// same sequence of wire values.
	twin := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    centralName,
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	twin.SetAvailableTargetChannels(weekprofileLocksTargets())

	// int32 is what the typed data point stores whatever the transport
	// surfaced; the float64 row proves wire coercion still reaches the same
	// decode. -1 is the out-of-contract value the two copies disagreed on.
	for _, raw := range []any{int32(0), int32(1), int32(5), float64(3), int32(-1), int32(2)} {
		if !locksDP.OnWireValue(raw) {
			t.Fatalf("OnWireValue(%v) rejected: the guard never reaches the decode", raw)
		}
		stored, ok := locksDP.RawValue()
		if !ok {
			t.Fatalf("OnWireValue(%v): data point holds no value", raw)
		}
		twin.SyncChannelLocksFromWire(stored)
		got, want := wp.ScheduleEnabled(), twin.ScheduleEnabled()
		if !maps.Equal(got, want) {
			t.Errorf("wire %v (stored %#v): bridge decoded %v, model decodes %v", raw, stored, got, want)
		}
	}
}
