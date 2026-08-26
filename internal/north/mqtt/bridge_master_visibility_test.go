// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestBridgeVisibilityGateUsesMasterParamsetKey pins the bridge-level
// visibility gates to the event's REAL paramset key. MASTER visibility is a
// default-deny whitelist; querying it with the VALUES key (the historical
// hard-coding) reported every MASTER parameter as visible, so a week-program
// or router-table parameter sailed through PublishState /
// PublishDiscoveryOnly whenever the per-DP suppression mark was not in place.
func TestBridgeVisibilityGateUsesMasterParamsetKey(t *testing.T) {
	t.Parallel()

	visReg := visibility.NewRegistry()
	newBridge := func() (*Bridge, *NoopClient) {
		pub := NewNoopClient()
		b := NewBridge(BridgeConfig{
			Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
			HADiscoveryEnabled: true,
			DiscoveryBuilder:   NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu-01"),
			Visibility:         filter.NewAdapter(visReg),
		}, pub)
		return b, pub
	}

	masterEvent := func(param string) Event {
		return Event{
			Central: "ccu-01", Interface: "HmIP-RF",
			DeviceAddress: "0001PSMX", DeviceName: "Trockner",
			Model: "HMIP-PSM", ChannelNo: 8,
			ChannelAddress: "0001PSMX:8", ChannelType: "SWITCH_WEEK_PROFILE",
			Parameter: param, Value: 0,
			Descriptor: &payload.GenericConfig{
				Type:     hmenum.ParameterTypeInteger,
				Paramset: hmenum.ParamsetKeyMaster,
			},
		}
	}

	t.Run("PublishState drops non-whitelisted MASTER parameter", func(t *testing.T) {
		t.Parallel()
		b, pub := newBridge()
		if err := b.PublishState(context.Background(), masterEvent("01_WP_WEEKDAY")); err != nil {
			t.Fatalf("PublishState: %v", err)
		}
		if got := len(pub.Published()); got != 0 {
			t.Fatalf("MASTER parameter leaked %d publishes (first: %s)", got, pub.Published()[0].Topic)
		}
	})

	t.Run("PublishDiscoveryOnly drops non-whitelisted MASTER parameter", func(t *testing.T) {
		t.Parallel()
		b, pub := newBridge()
		if err := b.PublishDiscoveryOnly(context.Background(), masterEvent("01_WP_WEEKDAY")); err != nil {
			t.Fatalf("PublishDiscoveryOnly: %v", err)
		}
		if got := len(pub.Published()); got != 0 {
			t.Fatalf("MASTER discovery leaked %d publishes (first: %s)", got, pub.Published()[0].Topic)
		}
	})

	t.Run("VALUES parameter stays publishable", func(t *testing.T) {
		t.Parallel()
		b, pub := newBridge()
		ev := masterEvent("ACTUAL_TEMPERATURE")
		ev.ChannelNo = 1
		ev.ChannelAddress = "0001PSMX:1"
		ev.ChannelType = "TEMPERATURE_SENSOR"
		ev.Category = hmenum.DataPointCategorySensor
		ev.Descriptor = &payload.GenericConfig{
			Type:     hmenum.ParameterTypeFloat,
			Paramset: hmenum.ParamsetKeyValues,
			Unit:     "°C",
		}
		ev.Value = 21.5
		if err := b.PublishState(context.Background(), ev); err != nil {
			t.Fatalf("PublishState: %v", err)
		}
		if got := len(pub.Published()); got == 0 {
			t.Fatalf("VALUES parameter must still publish")
		}
	})
}
