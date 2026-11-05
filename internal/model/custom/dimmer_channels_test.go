// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestWGTBindsDimmerAndSwitchToTheirOwnChannels drives the HmIP-WGT family through
// the production catalogue with its layout from the eQ-3 device documentation,
// crRFD 1.60.1 (WGT_V1, WGTC_V1) and the HmIP-WGTC fixture. The backlight on channel
// 2 uses the dimmer schema, whose secondary offsets land on the switch transmitter
// and the first switch receiver. They must not bind there: a dimmer on channel 4
// would also take the channel out of the switch's group, because the first group
// assignment wins.
func TestWGTBindsDimmerAndSwitchToTheirOwnChannels(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"HmIP-WGT", "HmIP-WGT-A", "HmIP-WGTC", "HmIP-WGTC-A"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			r := NewRegistry()
			RegisterProfiles(r)
			dimmerCtor, dimmers := fakeCtor("IPDimmer")
			r.MustRegisterConstructor("IPDimmer", dimmerCtor)
			switchCtor, switches := fakeCtor("IPSwitch")
			r.MustRegisterConstructor("IPSwitch", switchCtor)

			dev := device.New(device.Config{
				InterfaceID:  "HmIP-RF",
				Interface:    hmenum.InterfaceHmIPRF,
				Address:      "0001WG",
				Model:        model,
				ProductGroup: hmenum.ProductGroupHmIP,
			})
			for i, channelType := range []string{
				"MAINTENANCE", "KEY_TRANSCEIVER", "BACKLIGHTING_RECEIVER", "SWITCH_TRANSMITTER",
				"SWITCH_VIRTUAL_RECEIVER", "SWITCH_VIRTUAL_RECEIVER", "SWITCH_VIRTUAL_RECEIVER",
				"SWITCH_WEEK_PROFILE",
			} {
				dev.AddChannel("0001WG:"+itoaSmall(i), i, channelType, hmenum.ParamsetKeyValues)
			}

			if err := CreateCustomDataPoints(dev, r); err != nil {
				t.Fatalf("CreateCustomDataPoints: %v", err)
			}

			bound := func(dps []*fakeCustomDP) []int {
				out := make([]int, 0, len(dps))
				for _, dp := range dps {
					out = append(out, dp.channel.Number)
				}
				slices.Sort(out)
				return out
			}
			if got := bound(*dimmers); !slices.Equal(got, []int{2}) {
				t.Errorf("dimmer bound to channels %v, want [2]", got)
			}
			if got := bound(*switches); !slices.Equal(got, []int{4, 5, 6}) {
				t.Errorf("switch bound to channels %v, want [4 5 6]", got)
			}
			if got := dev.GroupChannels(2); !slices.Equal(got, []int{2}) {
				t.Errorf("backlight group members %v, want [2]", got)
			}
			if got := dev.GroupChannels(4); !slices.Equal(got, []int{3, 4, 5, 6}) {
				t.Errorf("switch group members %v, want [3 4 5 6]", got)
			}
		})
	}
}

// TestBDTVariantsBindDimmerToTheirOwnChannels drives the production catalogue
// through the materializer: HmIP-BDT-I shares the HmIP-BDT prefix but has every
// dimmer channel one higher, so a prefix hit on the shorter entry would bind the
// light to its state channel. The channel numbers of HmIP-BDT-I come from the
// eQ-3 device documentation, crRFD 1.60.1, section 10 (BDT_V1).
func TestBDTVariantsBindDimmerToTheirOwnChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model     string
		receivers []int
		state     int
	}{
		{model: "HmIP-BDT", receivers: []int{4, 5, 6}, state: 3},
		{model: "HmIP-BDT-I", receivers: []int{5, 6, 7}, state: 4},
		// A hypothetical variant must take the longer prefix, not HmIP-BDT.
		{model: "HmIP-BDT-I2", receivers: []int{5, 6, 7}, state: 4},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			r := NewRegistry()
			RegisterProfiles(r)
			ctor, captured := fakeCtor("IPDimmer")
			r.MustRegisterConstructor("IPDimmer", ctor)

			dev := device.New(device.Config{
				InterfaceID:  "HmIP-RF",
				Interface:    hmenum.InterfaceHmIPRF,
				Address:      "0001BD",
				Model:        tt.model,
				ProductGroup: hmenum.ProductGroupHmIP,
			})
			for i := range 9 {
				channelType := "KEY_TRANSCEIVER"
				switch {
				case i == 0:
					channelType = "MAINTENANCE"
				case i == tt.state:
					channelType = "DIMMER_TRANSMITTER"
				case slices.Contains(tt.receivers, i):
					channelType = "DIMMER_VIRTUAL_RECEIVER"
				}
				dev.AddChannel("0001BD:"+itoaSmall(i), i, channelType, hmenum.ParamsetKeyValues)
			}

			if err := CreateCustomDataPoints(dev, r); err != nil {
				t.Fatalf("CreateCustomDataPoints: %v", err)
			}

			bound := make([]int, 0, len(*captured))
			for _, dp := range *captured {
				bound = append(bound, dp.channel.Number)
			}
			slices.Sort(bound)
			if !slices.Equal(bound, tt.receivers) {
				t.Errorf("light bound to channels %v, want %v", bound, tt.receivers)
			}

			primary := tt.receivers[0]
			for _, ch := range dev.Channels() {
				if ch.Number == tt.state && ch.GroupNumber() != primary {
					t.Errorf("state channel %d in group %d, want %d", tt.state, ch.GroupNumber(), primary)
				}
			}
		})
	}
}
