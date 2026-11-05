// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestRGBWVariantsBindLightToTheirOwnChannels drives the production catalogue
// through the materializer with the channel layouts of the eQ-3 device
// documentation, crRFD 1.60.1, section 180 (LSS_V1) and of the HmIP-RGBW fixture.
// HmIP-RGBW has four UNIVERSAL_LIGHT_RECEIVER channels. HmIP-LSC has one, and its
// channel 2 is UNIVERSAL_LIGHT_WEEK_PROFILE, so the three secondary channels of
// HmIP-RGBW would put a light on the week-profile channel.
func TestRGBWVariantsBindLightToTheirOwnChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model    string
		channels int
		bound    []int
		members  []int
	}{
		{model: "HmIP-LSC", channels: 3, bound: []int{1}, members: []int{1}},
		{model: "HmIP-RGBW", channels: 6, bound: []int{1, 2, 3, 4}, members: []int{1, 2, 3, 4}},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			r := NewRegistry()
			RegisterProfiles(r)
			ctor, captured := fakeCtor("IPRGBW")
			r.MustRegisterConstructor("IPRGBW", ctor)

			dev := device.New(device.Config{
				InterfaceID:  "HmIP-RF",
				Interface:    hmenum.InterfaceHmIPRF,
				Address:      "0001LS",
				Model:        tt.model,
				ProductGroup: hmenum.ProductGroupHmIP,
			})
			for i := range tt.channels {
				dev.AddChannel("0001LS:"+itoaSmall(i), i, "T", hmenum.ParamsetKeyValues)
			}

			if err := CreateCustomDataPoints(dev, r); err != nil {
				t.Fatalf("CreateCustomDataPoints: %v", err)
			}

			bound := make([]int, 0, len(*captured))
			for _, dp := range *captured {
				bound = append(bound, dp.channel.Number)
			}
			slices.Sort(bound)
			if !slices.Equal(bound, tt.bound) {
				t.Errorf("light bound to channels %v, want %v", bound, tt.bound)
			}
			if got := dev.GroupChannels(1); !slices.Equal(got, tt.members) {
				t.Errorf("group 1 members %v, want %v", got, tt.members)
			}
		})
	}
}
