// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDRSVariantsGroupOnlyTheirOwnChannels drives the production catalogue
// through the materializer with the channel layout of the eQ-3 device
// documentation, crRFD 1.60.1, sections 133 (HMIPW_DRS4_V1) and 134
// (HMIPW_DRS8_V1). Both models end in a SWITCH_WEEK_PROFILE channel, 17 on the
// four-channel actuator and 33 on the eight-channel one. A profile that assumes
// eight outputs on HmIPW-DRS4 would claim channel 17 as the state channel of a
// group whose primary channel 18 does not exist.
func TestDRSVariantsGroupOnlyTheirOwnChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model       string
		weekProfile int
		groups      []int
	}{
		{model: "HmIPW-DRS4", weekProfile: 17, groups: []int{2, 6, 10, 14}},
		{model: "HmIPW-DRS8", weekProfile: 33, groups: []int{2, 6, 10, 14, 18, 22, 26, 30}},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			r := NewRegistry()
			RegisterProfiles(r)
			ctor, captured := fakeCtor("IPSwitch")
			r.MustRegisterConstructor("IPSwitch", ctor)

			dev := device.New(device.Config{
				InterfaceID:  "HmIP-RF",
				Interface:    hmenum.InterfaceHmIPRF,
				Address:      "0001DR",
				Model:        tt.model,
				ProductGroup: hmenum.ProductGroupHmIP,
			})
			// Channel 0 is maintenance, then each output is a transmitter and three
			// receivers, and the last channel is the week profile.
			for i := range tt.weekProfile + 1 {
				channelType := "SWITCH_VIRTUAL_RECEIVER"
				switch {
				case i == 0:
					channelType = "MAINTENANCE"
				case i == tt.weekProfile:
					channelType = "SWITCH_WEEK_PROFILE"
				case i%4 == 1:
					channelType = "SWITCH_TRANSMITTER"
				}
				dev.AddChannel("0001DR:"+itoaSmall(i), i, channelType, hmenum.ParamsetKeyValues)
			}

			if err := CreateCustomDataPoints(dev, r); err != nil {
				t.Fatalf("CreateCustomDataPoints: %v", err)
			}

			groups := make([]int, 0, len(tt.groups))
			for _, g := range dev.ChannelGroups() {
				groups = append(groups, g.GroupNumber)
			}
			if !slices.Equal(groups, tt.groups) {
				t.Errorf("channel groups %v, want %v", groups, tt.groups)
			}

			// Every output is a primary channel plus two secondaries.
			bound := make([]int, 0, len(*captured))
			for _, dp := range *captured {
				bound = append(bound, dp.channel.Number)
			}
			slices.Sort(bound)
			want := make([]int, 0, 3*len(tt.groups))
			for _, g := range tt.groups {
				want = append(want, g, g+1, g+2)
			}
			if !slices.Equal(bound, want) {
				t.Errorf("switch bound to channels %v, want %v", bound, want)
			}

			for _, ch := range dev.Channels() {
				if ch.Number != tt.weekProfile {
					continue
				}
				if got := ch.GroupNumber(); got != 0 {
					t.Errorf("week profile channel %d in group %d, want none", tt.weekProfile, got)
				}
				if ch.IsInMultiGroup() {
					t.Errorf("week profile channel %d reports a multi-channel group", tt.weekProfile)
				}
			}
		})
	}
}
