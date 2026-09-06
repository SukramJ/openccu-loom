// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestScheduleSwitchLabelReadsNameSynthetic pins the discovery label to the
// producer's own verdict on whether a target-channel name is generated.
//
// The collision it guards: a channel an operator named "Channel 5" is a real
// name, and comparing the name against the placeholder's spelling cannot tell
// the two apart. Both rows below carry the identical Name, so only the
// NameSynthetic flag can separate them.
func TestScheduleSwitchLabelReadsNameSynthetic(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		synthetic bool
		wantLabel string
	}{
		// German is the locale that separates the two templates:
		// discovery.schedule_named renders "Zeitplan {name}" while
		// discovery.schedule_channel renders "Zeitplan Kanal {ch}". In
		// English both collapse to the same string, which is exactly why the
		// old literal comparison went unnoticed.
		{name: "operator named the channel", synthetic: false, wantLabel: "Zeitplan Channel 5"},
		{name: "placeholder name", synthetic: true, wantLabel: "Zeitplan Kanal 5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := central.NewRegistry()
			c, err := central.New(central.Config{Name: "ccu-01"})
			if err != nil {
				t.Fatalf("central.New: %v", err)
			}
			if err := reg.Register(c); err != nil {
				t.Fatalf("register: %v", err)
			}
			dev := device.New(device.Config{
				Address: "000SWL", InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF, Model: "HmIP-BSL",
			})
			schedCh := dev.AddChannel("000SWL:10", 10, "SWITCH_WEEK_PROFILE", hmenum.ParamsetKeyMaster)
			wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
				CentralName:    "ccu-01",
				ChannelAddress: schedCh.Address,
				ScheduleType:   weekprofile.ScheduleTypeDefault,
				ProfileCount:   1,
			})
			schedCh.AttachWeekProfile(wp)
			c.ModelRegistry.Put(dev)

			wp.SetAvailableTargetChannels(map[string]weekprofile.TargetChannelInfo{
				"1_1": {
					ChannelNo:      5,
					ChannelAddress: "000SWL:5",
					Name:           "Channel 5",
					NameSynthetic:  tc.synthetic,
					ChannelType:    "primary",
					Bit:            0,
					BitKnown:       true,
				},
			})

			pub := mqtt.NewNoopClient()
			bridge := mqtt.NewBridge(mqtt.BridgeConfig{
				Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true, HADiscoveryEnabled: true,
			}, pub)
			eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil)).WithLocale("de")
			eb.publishScheduleSwitchSnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, 10, wp)

			payloads := make([]string, 0, len(pub.Published()))
			for _, p := range pub.Published() {
				payloads = append(payloads, string(p.Payload))
			}
			joined := strings.Join(payloads, "\n")
			if !strings.Contains(joined, tc.wantLabel) {
				t.Fatalf("published discovery does not carry label %q; payloads:\n%s", tc.wantLabel, joined)
			}
		})
	}
}
