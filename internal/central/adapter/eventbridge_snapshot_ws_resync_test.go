// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestInitialSnapshotSignalsResyncInsteadOfBroadcasting asserts how the
// boot snapshot reaches each north-bound plane: MQTT gets the values,
// WebSocket subscribers get one resync signal.
//
// It used to broadcast the walk to both. MQTT filtered each publish
// through buildPublishEvent — whose MASTER visibility is a default-deny
// whitelist — while the WebSocket side published whatever the walk
// touched, including the ~780 MASTER slots a channel with a week
// program carries (WP_LEVEL, WP_FIXED_HOUR, … per slot). The SPA
// subscribes to "*", so on a 1000-device installation a daemon restart
// pushed every session past its outbound queue during boot.
//
// Filtering alone would not have been enough: the visible set of a
// large fleet still exceeds any per-client queue. The plane's semantics
// differ — retained state versus live stream — so the snapshot serves
// them differently.
func TestInitialSnapshotSignalsResyncInsteadOfBroadcasting(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	state := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(state)
	if !state.OnWireValue(true) {
		t.Fatal("OnWireValue refused to seed the VALUES data point")
	}

	// A week program's worth of MASTER slots, seeded the way the
	// paramset load seeds them.
	const slots = 20
	for slot := 1; slot <= slots; slot++ {
		for _, param := range []string{"WP_LEVEL", "WP_FIXED_HOUR", "WP_FIXED_MINUTE"} {
			name := strconv.Itoa(slot) + "_" + param
			dp := generic.NewDataPoint[float64](generic.Spec{
				Key: hmtypes.DataPointKey{
					InterfaceID:    "HmIP-RF",
					ChannelAddress: "0001ABCD:1",
					ParamsetKey:    hmenum.ParamsetKeyMaster,
					Parameter:      name,
				},
				Descriptor: hmproto.ParameterData{
					Type:       hmenum.ParameterTypeFloat,
					Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
				},
			})
			ch.PutMaster(dp)
			if !dp.OnWireValue(float64(slot)) {
				t.Fatalf("OnWireValue refused to seed %s", name)
			}
		}
	}

	wsHub := ws.NewHub()
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, wsHub, mqtt.NewWiring(bridge, nil)).
		WithVisibility(filter.NewAdapter(visibility.NewRegistry()))
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	// The WebSocket plane carries no per-data-point frames at all: the
	// snapshot is MQTT's job (retained state) and WS subscribers are
	// told to resync instead.
	var wsFrames int
	for _, ev := range wsHub.Replay(0, nil).Events {
		if strings.HasPrefix(ev.Topic, "device.") {
			wsFrames++
		}
	}
	if wsFrames != 0 {
		t.Errorf("boot snapshot broadcast %d device frames on the WebSocket plane; "+
			"it must signal a resync instead of replaying the model", wsFrames)
	}
	if got := wsHub.ResyncSignals(); got != 1 {
		t.Errorf("resync signals = %d, want 1 per central snapshot", got)
	}

	// MQTT still gets the visible state, and still declines what the
	// visibility gate refuses.
	var mqttState, mqttWeekProfile int
	for _, p := range pub.Published() {
		switch {
		case strings.Contains(p.Topic, "_WP_"):
			mqttWeekProfile++
		case strings.HasSuffix(p.Topic, "/values/STATE"):
			mqttState++
		}
	}
	if mqttWeekProfile != 0 {
		t.Errorf("MQTT published %d week-profile MASTER slots; MASTER visibility is default-deny", mqttWeekProfile)
	}
	if mqttState == 0 {
		t.Error("MQTT published no VALUES state; the snapshot must still write retained state")
	}
}
