// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// startedBridgeWS boots an EventBridge over one registered central,
// with or without MQTT wiring, and returns the WS hub plus the
// central's bus for publishing.
func startedBridgeWS(t *testing.T, withMQTT bool) (*ws.Hub, *central.Unit, *device.Device, func()) {
	t.Helper()
	reg, d := registryWithDevice(t)

	var mw *mqtt.Wiring
	if withMQTT {
		bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, mqtt.NewNoopClient())
		mw = mqtt.NewWiring(bridge, nil)
	}

	wsHub := ws.NewHub()
	eb := NewEventBridge(reg, wsHub, mw)
	eb.Start(context.Background())

	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("registry size %d", len(list))
	}
	return wsHub, list[0], d, func() { eb.Flush(); eb.Stop() }
}

// TestEventBridgeWSDeliveryWithoutMQTT drives every WS-emitting bus
// subscription of the EventBridge against a bridge constructed WITHOUT
// MQTT wiring and asserts the push reaches the WS hub. dispatchLive
// gates only its MQTT arm on the wiring being present; a handler that
// instead returns early on a nil wiring silently cuts the SPA off in an
// MQTT-less deployment — exactly the onSourceChanged defect this table
// pins. A new WS-emitting subscription in EventBridge.Start belongs in
// this table.
func TestEventBridgeWSDeliveryWithoutMQTT(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		publish  func(u *central.Unit, d *device.Device)
		wantType hmevent.EventType
		wantKind string
	}{
		{
			name: "value_changed",
			publish: func(u *central.Unit, d *device.Device) {
				events.Publish(u.EventBus, hmevent.DataPointValueChangedEvent{
					Base: hmevent.NewBase(),
					Key: hmtypes.DataPointKey{
						InterfaceID:    "HmIP-RF",
						ChannelAddress: d.Address + ":1",
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      "STATE",
					},
					NewValue: hmtypes.BoolValue(true),
				})
			},
			wantType: hmevent.EventTypeDataPointValueChanged,
			wantKind: ws.KindChange,
		},
		{
			name: "source_changed_refresh",
			publish: func(u *central.Unit, d *device.Device) {
				events.Publish(u.EventBus, hmevent.DataPointSourceChangedEvent{
					Base:           hmevent.NewBase(),
					CentralName:    "ccu-01",
					InterfaceID:    "HmIP-RF",
					ChannelAddress: d.Address + ":1",
					Parameter:      "STATE",
					OldSource:      hmenum.ValueSourceCache,
					NewSource:      hmenum.ValueSourceLive,
					Value:          true,
				})
			},
			wantType: hmevent.EventTypeDataPointValueChanged,
			wantKind: ws.KindRefresh,
		},
		{
			name: "central_state",
			publish: func(u *central.Unit, _ *device.Device) {
				events.Publish(u.EventBus, hmevent.CentralStateChangedEvent{
					Base:        hmevent.NewBase(),
					CentralName: "ccu-01",
					From:        hmenum.CentralStateRunning,
					To:          hmenum.CentralStateDegraded,
				})
			},
			wantType: hmevent.EventTypeCentralStateChanged,
		},
		{
			name: "central_readiness",
			publish: func(u *central.Unit, _ *device.Device) {
				events.Publish(u.EventBus, hmevent.CentralReadinessChangedEvent{
					Base:        hmevent.NewBase(),
					CentralName: "ccu-01",
					Phase:       hmenum.ReadinessReady,
				})
			},
			wantType: hmevent.EventTypeCentralReadinessChanged,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wsHub, unit, d, done := startedBridgeWS(t, false)
			defer done()

			tc.publish(unit, d)

			for _, e := range wsHub.Replay(0, nil).Events {
				if e.Type != string(tc.wantType) {
					continue
				}
				if tc.wantKind != "" && e.Kind != tc.wantKind {
					continue
				}
				return
			}
			t.Fatalf("no WS push of type %q (kind %q) without MQTT wiring", tc.wantType, tc.wantKind)
		})
	}
}

// TestSourceChangedRefreshReachesWSWithMQTT is the control case for the
// source-changed row above: with the MQTT wiring present the same
// transition must keep producing exactly one KindRefresh push.
func TestSourceChangedRefreshReachesWSWithMQTT(t *testing.T) {
	t.Parallel()
	wsHub, unit, d, done := startedBridgeWS(t, true)
	defer done()

	events.Publish(unit.EventBus, hmevent.DataPointSourceChangedEvent{
		Base:           hmevent.NewBase(),
		CentralName:    "ccu-01",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: d.Address + ":1",
		Parameter:      "STATE",
		OldSource:      hmenum.ValueSourceCache,
		NewSource:      hmenum.ValueSourceLive,
		Value:          true,
	})

	got := 0
	for _, e := range wsHub.Replay(0, nil).Events {
		if e.Kind == ws.KindRefresh && e.Type == string(hmevent.EventTypeDataPointValueChanged) {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("WS refresh pushes with MQTT = %d, want 1", got)
	}
}
