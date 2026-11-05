// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestBDTVariantsBindDimmerToTheirOwnChannels drives the production catalogue
// through the materializer: HmIP-BDT-I shares the HmIP-BDT prefix but has every
// dimmer channel one higher, so a prefix hit on the shorter entry would bind the
// light to its state channel. The channel numbers of HmIP-BDT-I come from the
// owner's channel table, not from a device export.
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
				dev.AddChannel("0001BD:"+itoaSmall(i), i, "T", hmenum.ParamsetKeyValues)
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
