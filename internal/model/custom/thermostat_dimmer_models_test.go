// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Channel layouts from the eQ-3 device documentation crRFD 1.60.1 (the section
// named per layout). None of these models is in the simulated fleet, so the
// documentation is the only source; each model's layout was compared with its
// section, not taken from a sibling model.
var (
	// §355 WT_V1: HmIP-WT, laid out as HmIP-WTH-2 (§349 WTH-2), which KW-STATH
	// shares.
	wallThermostatLayout = []string{
		"MAINTENANCE_BAT_CL", "HEATING_CLIMATECONTROL_TRANSCEIVER", "HEATING_CLIMATECONTROL_RECEIVER",
		"HEATING_CLIMATECONTROL_CL_TRANSMITTER", "HEATING_SHUTTER_CONTACT_RECEIVER",
		"HEATING_CLIMATECONTROL_SWITCH_TRANSMITTER", "HEATING_KEY_RECEIVER", "CLIMATECONTROL_FLOOR_TRANSMITTER",
	}
	// §300 TRV-B_V1: KW-UKETRV and KW-UKETRV-2.
	radiatorThermostatLayout = []string{
		"MAINTENANCE_BAT_BL", "HEATING_CLIMATECONTROL_TRANSCEIVER", "HEATING_CLIMATECONTROL_RECEIVER",
		"HEATING_CLIMATECONTROL_CL_RECEIVER", "HEATING_SHUTTER_CONTACT_RECEIVER", "HEATING_ROOM_TH_TRANSCEIVER",
		"HEATING_ROOM_TH_RECEIVER", "HEATING_KEY_RECEIVER",
	}
	// §356 WUA_V1: HmIP-WUA and ELV-SH-WUA.
	universalDimmerLayout = []string{
		"MAINTENANCE", "DIMMER_TRANSMITTER", "DIMMER_VIRTUAL_RECEIVER", "DIMMER_VIRTUAL_RECEIVER",
		"DIMMER_VIRTUAL_RECEIVER", "DIMMER_WEEK_PROFILE",
	}
	// §313 UDI_PB2_V1: HmIP-UDI-PB2 and HmIP-UDI-PB2-A.
	pushButtonDimmerLayout = []string{
		"MAINTENANCE", "KEY_TRANSCEIVER", "KEY_TRANSCEIVER", "MULTI_MODE_INPUT_TRANSMITTER", "DIMMER_TRANSMITTER",
		"DIMMER_VIRTUAL_RECEIVER", "DIMMER_VIRTUAL_RECEIVER", "DIMMER_VIRTUAL_RECEIVER", "DIMMER_WEEK_PROFILE",
		"ALARM_COND_SWITCH_TRANSMITTER",
	}
	// §161 HMIPW_WGT_V1 and §160 HMIPW_WGTC_V1: the wired glass wall
	// thermostats, without switch channels.
	wiredGlassThermostatLayout = []string{
		"MAINTENANCE", "KEY_TRANSCEIVER", "BACKLIGHTING_RECEIVER", "SWITCH_WEEK_PROFILE",
		"HEATING_CLIMATECONTROL_TRANSCEIVER", "HEATING_CLIMATECONTROL_RECEIVER",
		"HEATING_CLIMATECONTROL_CL_TRANSMITTER", "HEATING_SHUTTER_CONTACT_RECEIVER", "HEATING_KEY_RECEIVER",
		"CLIMATECONTROL_FLOOR_TRANSMITTER", "KWL_CONTROLLER_TRANSMITTER", "LEVEL_COMMAND_TRANSMITTER_TEMPERATURE",
		"LEVEL_COMMAND_TRANSMITTER_HUMIDITY",
	}
)

// TestThermostatAndDimmerModelsBindTheirChannels drives each newly catalogued
// model through the production catalogue and the materializer with its
// documented layout, and asserts which channel every profile binds to.
//
// The thermostats carry the setpoint and the P<n>_* schedule slots on the
// climate channel and GLOBAL_BUTTON_LOCK on channel 0 (§355 channel 1 and 0,
// §300 channel 1 and 0, §161 channel 4 and 0). The dimmers drive their first
// DIMMER_VIRTUAL_RECEIVER, with the DIMMER_TRANSMITTER before it as state
// channel; the two virtual receivers after it bind as the schema's secondary
// channels, as on HmIP-PDT. HmIP-WTH-2 is the control: "hmip-wt" is a prefix of it, and it must
// keep resolving to its own entry.
func TestThermostatAndDimmerModelsBindTheirChannels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model      string
		layout     []string
		deviceType string
		thermostat []int
		lock       []int
		dimmer     []int
		dimmerSet  []int // members of the channel group of the dimmer's primary channel
	}{
		{"HmIP-WT", wallThermostatLayout, "hmip-wt", []int{1}, []int{0}, nil, nil},
		{"HmIP-WTH-2", wallThermostatLayout, "hmip-wth", []int{1}, []int{0}, nil, nil},
		{"KW-STATH", wallThermostatLayout, "kw-stath", []int{1}, []int{0}, nil, nil},
		{"KW-UKETRV", radiatorThermostatLayout, "kw-uketrv", []int{1}, []int{0}, nil, nil},
		{"KW-UKETRV-2", radiatorThermostatLayout, "kw-uketrv", []int{1}, []int{0}, nil, nil},
		{"HmIP-WUA", universalDimmerLayout, "hmip-wua", nil, nil, []int{2, 3, 4}, []int{1, 2, 3, 4}},
		{"ELV-SH-WUA", universalDimmerLayout, "elv-sh-wua", nil, nil, []int{2, 3, 4}, []int{1, 2, 3, 4}},
		{"HmIP-UDI-PB2", pushButtonDimmerLayout, "hmip-udi-pb2", nil, nil, []int{5, 6, 7}, []int{4, 5, 6, 7}},
		{"HmIP-UDI-PB2-A", pushButtonDimmerLayout, "hmip-udi-pb2", nil, nil, []int{5, 6, 7}, []int{4, 5, 6, 7}},
		// The backlight's state candidate is the KEY_TRANSCEIVER on channel 1,
		// which carries no LEVEL; the dimmer schema does not accept it.
		{"HmIPW-WGT", wiredGlassThermostatLayout, "hmipw-wgt", []int{4}, []int{0}, []int{2}, []int{2}},
		{"HmIPW-WGTC-A", wiredGlassThermostatLayout, "hmipw-wgt", []int{4}, []int{0}, []int{2}, []int{2}},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()

			r := NewRegistry()
			RegisterProfiles(r)
			thermostatCtor, thermostats := fakeCtor("IPThermostat")
			r.MustRegisterConstructor("IPThermostat", thermostatCtor)
			lockCtor, locks := fakeCtor("IPButtonLock")
			r.MustRegisterConstructor("IPButtonLock", lockCtor)
			dimmerCtor, dimmers := fakeCtor("IPDimmer")
			r.MustRegisterConstructor("IPDimmer", dimmerCtor)

			for _, p := range r.GetConfigs(tc.model) {
				if p.DeviceType != tc.deviceType {
					t.Errorf("profile %s resolves to entry %q, want %q", p.Name, p.DeviceType, tc.deviceType)
				}
			}

			dev := device.New(device.Config{
				InterfaceID:  "HmIP-RF",
				Interface:    hmenum.InterfaceHmIPRF,
				Address:      "0001NM",
				Model:        tc.model,
				ProductGroup: hmenum.ProductGroupHmIP,
			})
			for i, channelType := range tc.layout {
				dev.AddChannel("0001NM:"+itoaSmall(i), i, channelType, hmenum.ParamsetKeyValues)
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
			if got := bound(*thermostats); !slices.Equal(got, tc.thermostat) {
				t.Errorf("thermostat bound to %v, want %v", got, tc.thermostat)
			}
			if got := bound(*locks); !slices.Equal(got, tc.lock) {
				t.Errorf("button lock bound to %v, want %v", got, tc.lock)
			}
			if got := bound(*dimmers); !slices.Equal(got, tc.dimmer) {
				t.Errorf("dimmer bound to %v, want %v", got, tc.dimmer)
			}
			if tc.dimmer != nil {
				if got := dev.GroupChannels(tc.dimmer[0]); !slices.Equal(got, tc.dimmerSet) {
					t.Errorf("dimmer group members %v, want %v", got, tc.dimmerSet)
				}
			}
		})
	}
}

// TestWGTHeatingScheduleIsOnTheClimateChannel pins the schedule channel of the
// HmIP-WGT thermostat: the P<n>_* slots are in the MASTER paramset of the
// climate channel 8 of the recorded HmIP-WGTC (1092 slot parameters there, none
// on channel 1, which is a KEY_TRANSCEIVER), and of channel 4 on the wired
// models (§161, channel 4 configuration parameters).
func TestWGTHeatingScheduleIsOnTheClimateChannel(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	RegisterProfiles(r)
	for model, want := range map[string]int{"HmIP-WGT": 8, "HmIP-WGTC": 8, "HmIPW-WGT": 4, "HmIPW-WGTC": 4} {
		var found bool
		for _, p := range r.GetConfigs(model) {
			if p.Name != "IPThermostat" {
				continue
			}
			found = true
			if p.ScheduleChannelNo == nil || *p.ScheduleChannelNo != want {
				t.Errorf("%s: schedule channel %v, want %d", model, p.ScheduleChannelNo, want)
			}
		}
		if !found {
			t.Errorf("%s: no IPThermostat registration", model)
		}
	}
}
