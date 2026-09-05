// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestAssemble_ChannelLabelReachesTheNodeLabel pins the channel word an
// assembled endpoint falls back to when its channel carries no name of its
// own. The assembler resolves no translation catalogue itself — the host
// hands it the finished word — so an ignored [matteradapter.Config.ChannelLabel]
// would silently leave every locale on the English fallback, which is
// exactly the kind of drop that shows up first on an operator's Matter
// controller rather than in a test.
func TestAssemble_ChannelLabelReachesTheNodeLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		label string
		want  string
	}{
		{name: "supplied label is used verbatim", label: "Kanal", want: "Lampe Kanal 1"},
		{name: "empty label falls back to English", label: "", want: "Lampe Channel 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const chAddr = "ABC0001:1"
			dev := newDevice("ABC0001", "Lampe")
			ch := addChannel(dev, chAddr, 1)
			ch.SetCustomDataPoint(&stubEndpointSource{
				key:        dpKey(chAddr, "RGBW_LIGHT"),
				deviceType: 0x0101,
			})

			cfg := validConfig()
			cfg.ChannelLabel = tc.label
			a, err := matteradapter.New(newFakeStore(), cfg, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			top, err := a.AssembleDevices(context.Background(), []matteradapter.DeviceSnapshot{
				{CentralName: "ccu1", Devices: []*device.Device{dev}},
			})
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			if len(top.Endpoints) != 3 {
				t.Fatalf("expected root + aggregator + bridged, got %d endpoints", len(top.Endpoints))
			}
			if got := top.Endpoints[2].FriendlyName; got != tc.want {
				t.Errorf("FriendlyName = %q, want %q", got, tc.want)
			}
			if strings.Contains(top.Endpoints[2].FriendlyName, "  ") {
				t.Errorf("FriendlyName %q contains a doubled space", top.Endpoints[2].FriendlyName)
			}
		})
	}
}
