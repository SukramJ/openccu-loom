// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// TestNamingContract is a parametrised table-test that asserts the
// naming-contract for custom-DP channels across the axes that caused
// real bugs in the naming algorithm:
//
//   - displayChannelName suffix: empty / ch<N> / vch<N>
//   - enabled_by_default flag: true for primary channels, false for secondaries
//
// The logic under test lives in [device.Channel] (IsCustomDPPrimaryChannel /
// IsCustomDPSecondaryChannel / HasSinglePrimaryCustomDP) and is consumed by
// the MQTT discovery builder's displayChannelName function
// (internal/north/mqtt/discovery_aggregate.go:388–411).
//
// Because [internal/model/naming] is a leaf package (device imports naming, not
// the other way around) the test uses "package naming_test" so it can freely
// import both [naming] and [device] without creating an import cycle.
package naming_test

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

// fakeCustomDP is the minimal custom-DP fake that satisfies
// [device.AttachableDataPoint] and the unexported
// [device.haComponentProvider] interface by implementing HAComponent() —
// required for [device.Channel.HasSinglePrimaryCustomDP].
type fakeCustomDP struct {
	key         hmtypes.DataPointKey
	haComponent string
}

func (f *fakeCustomDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeCustomDP) HAComponent() string                { return f.haComponent }

// ─── displayChannelName ───────────────────────────────────────────────────────
// Replica of the unexported displayChannelName function in
// internal/north/mqtt/discovery_aggregate.go. Kept in sync with the production
// implementation; the multi-primary IgnoreMultipleChannelsForName opt-out branch
// is additionally exercised directly against the real device.Channel methods in
// ignore_multiple_channels_name_test.go.
func displayChannelName(ch *device.Channel, channelNo int) string {
	if ch.IsCustomDPSecondaryChannel() {
		return fmt.Sprintf("vch%d", channelNo)
	}
	if ch.IsCustomDPPrimaryChannel() {
		if ch.HasSinglePrimaryCustomDP() {
			return ""
		}
		// A custom DP may opt out of the ch<N> suffix even on a multi-primary
		// device (locks render as "<Lock>" / "<Lock>", not "<Lock> ch1" / ch2).
		if ch.IgnoreMultipleChannelsForName() {
			return ""
		}
		return fmt.Sprintf("ch%d", channelNo)
	}
	if channelNo > 0 {
		return strconv.Itoa(channelNo)
	}
	return ""
}

// enabledByDefault mirrors the MQTT bridge logic:
// secondary custom-DP channels are hidden by default in HA.
func enabledByDefault(ch *device.Channel) bool {
	return !ch.IsCustomDPSecondaryChannel()
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func newDevice(name, model, address string) *device.Device {
	return device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     address,
		Model:       model,
		Name:        name,
	})
}

func attachSwitchDP(ch *device.Channel) {
	ch.SetCustomDataPoint(&fakeCustomDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "switch"},
		haComponent: "switch",
	})
}

func attachClimateDP(ch *device.Channel) {
	ch.SetCustomDataPoint(&fakeCustomDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "climate"},
		haComponent: "climate",
	})
}

func attachLightDP(ch *device.Channel) {
	ch.SetCustomDataPoint(&fakeCustomDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "light"},
		haComponent: "light",
	})
}

func attachCoverDP(ch *device.Channel) {
	ch.SetCustomDataPoint(&fakeCustomDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "cover"},
		haComponent: "cover",
	})
}

func attachLockDP(ch *device.Channel) {
	ch.SetCustomDataPoint(&fakeCustomDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "lock"},
		haComponent: "lock",
	})
}

// ─── Contract table ──────────────────────────────────────────────────────────

// TestNamingContract is the main parametrised table.
func TestNamingContract(t *testing.T) {
	t.Parallel()

	type tc struct {
		name             string
		setupDevice      func() (*device.Channel, int) // returns (channel, channelNo)
		wantSuffix       string
		wantEnabledByDef bool
	}

	cases := []tc{
		// ── HmIP-PSM: single Switch primary on ch3 ────────────────────────
		// The device has 3 switch channels (ch3/ch4/ch5) structured as one
		// primary (ch3) + two secondaries (ch4, ch5). Because only ONE
		// primary exists in the Switch category, HasSinglePrimaryCustomDP
		// must return true → no suffix.
		{
			name: "HmIP-PSM ch3 single-switch-primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Steckdose", "HmIP-PSM", "VCU0001000")
				ch3 := d.AddChannel("VCU0001000:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch4 := d.AddChannel("VCU0001000:4", 4, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch5 := d.AddChannel("VCU0001000:5", 5, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				// ch3 is the group master (primary), ch4+ch5 are secondaries.
				ch3.AssignGroupNumber(3)
				ch4.AssignGroupNumber(3)
				ch5.AssignGroupNumber(3)
				attachSwitchDP(ch3)
				attachSwitchDP(ch4)
				attachSwitchDP(ch5)
				return ch3, 3
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── HmIP-PSM: secondary ch4 → vch suffix + disabled ──────────────
		{
			name: "HmIP-PSM ch4 secondary → vch4, disabled by default",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Steckdose", "HmIP-PSM", "VCU0001001")
				ch3 := d.AddChannel("VCU0001001:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch4 := d.AddChannel("VCU0001001:4", 4, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch5 := d.AddChannel("VCU0001001:5", 5, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch3.AssignGroupNumber(3)
				ch4.AssignGroupNumber(3)
				ch5.AssignGroupNumber(3)
				attachSwitchDP(ch3)
				attachSwitchDP(ch4)
				attachSwitchDP(ch5)
				return ch4, 4
			},
			wantSuffix:       "vch4",
			wantEnabledByDef: false,
		},
		// ── HmIP-PSM: secondary ch5 → vch5 ──────────────────────────────
		{
			name: "HmIP-PSM ch5 secondary → vch5, disabled by default",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Steckdose", "HmIP-PSM", "VCU0001002")
				ch3 := d.AddChannel("VCU0001002:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch4 := d.AddChannel("VCU0001002:4", 4, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch5 := d.AddChannel("VCU0001002:5", 5, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch3.AssignGroupNumber(3)
				ch4.AssignGroupNumber(3)
				ch5.AssignGroupNumber(3)
				attachSwitchDP(ch3)
				attachSwitchDP(ch4)
				attachSwitchDP(ch5)
				return ch5, 5
			},
			wantSuffix:       "vch5",
			wantEnabledByDef: false,
		},
		// ── HmIP-FSM16: single fixed-switch primary → no suffix ───────────
		// One switch group: ch1 is primary, ch2 is secondary.
		// Only one Switch primary → no ch suffix on primary.
		{
			name: "HmIP-FSM16 single-switch-primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Schalter", "HmIP-FSM16", "VCU0002000")
				ch1 := d.AddChannel("VCU0002000:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch2 := d.AddChannel("VCU0002000:2", 2, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				ch2.AssignGroupNumber(1)
				attachSwitchDP(ch1)
				attachSwitchDP(ch2)
				return ch1, 1
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── HmIP-DRSI4: 4 Switch primaries → ch suffix on every primary ───
		// Four independent switch groups → is_multi_channel_device → ch<N>.
		{
			name: "HmIP-DRSI4 ch1 of 4 switch primaries → ch1",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Dimmer 4", "HmIP-DRSI4", "VCU0003000")
				ch1 := d.AddChannel("VCU0003000:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch2 := d.AddChannel("VCU0003000:2", 2, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch3 := d.AddChannel("VCU0003000:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch4 := d.AddChannel("VCU0003000:4", 4, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				// All four are independent primaries (GroupNo == own Number).
				ch1.AssignGroupNumber(1)
				ch2.AssignGroupNumber(2)
				ch3.AssignGroupNumber(3)
				ch4.AssignGroupNumber(4)
				attachSwitchDP(ch1)
				attachSwitchDP(ch2)
				attachSwitchDP(ch3)
				attachSwitchDP(ch4)
				return ch1, 1
			},
			wantSuffix:       "ch1",
			wantEnabledByDef: true,
		},
		{
			name: "HmIP-DRSI4 ch3 of 4 switch primaries → ch3",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Dimmer 4", "HmIP-DRSI4", "VCU0003001")
				ch1 := d.AddChannel("VCU0003001:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch2 := d.AddChannel("VCU0003001:2", 2, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch3 := d.AddChannel("VCU0003001:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch4 := d.AddChannel("VCU0003001:4", 4, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				ch2.AssignGroupNumber(2)
				ch3.AssignGroupNumber(3)
				ch4.AssignGroupNumber(4)
				attachSwitchDP(ch1)
				attachSwitchDP(ch2)
				attachSwitchDP(ch3)
				attachSwitchDP(ch4)
				return ch3, 3
			},
			wantSuffix:       "ch3",
			wantEnabledByDef: true,
		},
		// ── HmIP-eTRV-2: single climate → no suffix ───────────────────────
		// One climate channel (ch1), no group → primary, HasSinglePrimary.
		{
			name: "HmIP-eTRV-2 single climate primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Heizung", "HmIP-eTRV-2", "VCU0004000")
				ch1 := d.AddChannel("VCU0004000:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
				// No GroupNo → GroupNo stays 0 → primary path (single DP, no group)
				attachClimateDP(ch1)
				return ch1, 1
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── HmIP-WTH-2: climate on ch1, switch on ch6 ─────────────────────
		// Two different categories; climate ch1 is the sole climate primary
		// → no suffix; the switch on ch6 is the sole switch primary → also
		// no suffix.
		{
			name: "HmIP-WTH-2 climate ch1 single primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Wandthermostat", "HmIP-WTH-2", "VCU0005000")
				ch1 := d.AddChannel("VCU0005000:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
				ch6 := d.AddChannel("VCU0005000:6", 6, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				ch6.AssignGroupNumber(6)
				attachClimateDP(ch1)
				attachSwitchDP(ch6)
				return ch1, 1
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		{
			name: "HmIP-WTH-2 switch ch6 single primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Wandthermostat", "HmIP-WTH-2", "VCU0005001")
				ch1 := d.AddChannel("VCU0005001:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
				ch6 := d.AddChannel("VCU0005001:6", 6, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				ch6.AssignGroupNumber(6)
				attachClimateDP(ch1)
				attachSwitchDP(ch6)
				return ch6, 6
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── HmIP-BSL: two independent light primaries → ch suffix ─────────
		// Bicolor LED: ch8 and ch12 are independent light primaries.
		{
			name: "HmIP-BSL ch8 of 2 light primaries → ch8",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Statusanzeige", "HmIP-BSL", "VCU0006000")
				ch8 := d.AddChannel("VCU0006000:8", 8, "OPTICAL_SIGNAL_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch12 := d.AddChannel("VCU0006000:12", 12, "OPTICAL_SIGNAL_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch8.AssignGroupNumber(8)
				ch12.AssignGroupNumber(12)
				attachLightDP(ch8)
				attachLightDP(ch12)
				return ch8, 8
			},
			wantSuffix:       "ch8",
			wantEnabledByDef: true,
		},
		{
			name: "HmIP-BSL ch12 of 2 light primaries → ch12",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Statusanzeige", "HmIP-BSL", "VCU0006001")
				ch8 := d.AddChannel("VCU0006001:8", 8, "OPTICAL_SIGNAL_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch12 := d.AddChannel("VCU0006001:12", 12, "OPTICAL_SIGNAL_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch8.AssignGroupNumber(8)
				ch12.AssignGroupNumber(12)
				attachLightDP(ch8)
				attachLightDP(ch12)
				return ch12, 12
			},
			wantSuffix:       "ch12",
			wantEnabledByDef: true,
		},
		// ── HmIP-RC8: 8 button channels, no custom DP ─────────────────────
		// Button / press-event channels have no custom DP (they use generic
		// events, not an aggregated custom-DP). Channel 1 > 0 → legacy path
		// → suffix is the channel number string.
		{
			name: "HmIP-RC8 ch1 no custom DP → legacy '1' suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Fernbedienung 8", "HmIP-RC8", "VCU0007000")
				ch1 := d.AddChannel("VCU0007000:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
				_ = d.AddChannel("VCU0007000:2", 2, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
				// No custom DP attached → IsCustomDPPrimaryChannel=false.
				_ = ch1
				return ch1, 1
			},
			wantSuffix:       "1",
			wantEnabledByDef: true,
		},
		{
			name: "HmIP-RC8 ch8 no custom DP → legacy '8' suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Fernbedienung 8", "HmIP-RC8", "VCU0007001")
				ch8 := d.AddChannel("VCU0007001:8", 8, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
				return ch8, 8
			},
			wantSuffix:       "8",
			wantEnabledByDef: true,
		},
		// ── Channel 0 / device root: no suffix (special sentinel) ─────────
		{
			name: "channel 0 no custom DP → empty suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Device", "HmIP-X", "VCU0008000")
				ch0 := d.AddChannel("VCU0008000:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
				return ch0, 0
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── Single cover primary → no suffix ──────────────────────────────
		{
			name: "HmIP-BROLL single cover primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Rollladenantrieb", "HmIP-BROLL", "VCU0009000")
				ch1 := d.AddChannel("VCU0009000:1", 1, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				attachCoverDP(ch1)
				return ch1, 1
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── Two independent cover primaries → ch suffix ───────────────────
		{
			name: "dual-cover ch1 of 2 → ch1",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Zweifach Rolllade", "HmIP-2BROLL", "VCU0009100")
				ch1 := d.AddChannel("VCU0009100:1", 1, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch2 := d.AddChannel("VCU0009100:2", 2, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				ch2.AssignGroupNumber(2)
				attachCoverDP(ch1)
				attachCoverDP(ch2)
				return ch1, 1
			},
			wantSuffix:       "ch1",
			wantEnabledByDef: true,
		},
		// ── Single lock primary → no suffix ───────────────────────────────
		{
			name: "HmIP-DLD single lock primary → no suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Türschloss", "HmIP-DLD", "VCU0010000")
				ch1 := d.AddChannel("VCU0010000:1", 1, "DOOR_LOCK_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch1.AssignGroupNumber(1)
				attachLockDP(ch1)
				return ch1, 1
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── press-events: must retain channel-name context, no override ────
		// Generic events live in their own channel without a custom DP.
		// The legacy fallback uses channelNo as suffix (parity
		// event entity naming which appends the CCU channel number).
		{
			name: "press-event ch1 no custom DP → legacy '1' suffix (CCU channel name retained)",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Taster 1", "HM-PB-2-WM55", "VCU0011000")
				ch1 := d.AddChannel("VCU0011000:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
				ch1.SetName("Taster 1 Taste 1") // explicit CCU channel name
				// No custom DP → falls through to legacy path.
				return ch1, 1
			},
			wantSuffix:       "1",
			wantEnabledByDef: true,
		},
		{
			name: "press-event ch2 no custom DP → legacy '2' suffix",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Taster 2", "HM-PB-2-WM55", "VCU0011001")
				ch2 := d.AddChannel("VCU0011001:2", 2, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
				ch2.SetName("Taster 2 Taste 2")
				return ch2, 2
			},
			wantSuffix:       "2",
			wantEnabledByDef: true,
		},
		// ── Multi-primary lock: IgnoreMultipleChannelsForName opt-out ─────
		// Two lock primaries (ch1, ch2) share the "lock" HA component, so
		// HasSinglePrimaryCustomDP is false. Without the opt-out this would
		// render "ch1"/"ch2"; the lock DP opts out via
		// IgnoreMultipleChannelsForName, so the suffix is suppressed.
		{
			name: "multi-lock ch1 with IgnoreMultipleChannelsForName → no suffix",
			setupDevice: func() (*device.Channel, int) {
				ch1, _ := newMultiLockDevice("VCU0013000")
				return ch1, 1
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		{
			name: "multi-lock ch2 with IgnoreMultipleChannelsForName → no suffix",
			setupDevice: func() (*device.Channel, int) {
				_, ch2 := newMultiLockDevice("VCU0013001")
				return ch2, 2
			},
			wantSuffix:       "",
			wantEnabledByDef: true,
		},
		// ── HmIP-PSM regression: secondary ch4 named "vch4" not "ch4" ─────
		// Regression: secondary channels of HmIP-PSM were incorrectly named
		// "ch4"/"ch5" instead of "vch4"/"vch5".
		// The test ensures the secondary check runs BEFORE HasSinglePrimaryCustomDP.
		{
			name: "regression: HmIP-PSM ch4 secondary must not be named ch4",
			setupDevice: func() (*device.Channel, int) {
				d := newDevice("Steckdose", "HmIP-PSM", "VCU0012000")
				ch3 := d.AddChannel("VCU0012000:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch4 := d.AddChannel("VCU0012000:4", 4, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
				ch3.AssignGroupNumber(3)
				ch4.AssignGroupNumber(3)
				attachSwitchDP(ch3)
				attachSwitchDP(ch4)
				return ch4, 4
			},
			wantSuffix:       "vch4", // NOT "ch4"
			wantEnabledByDef: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch, no := tc.setupDevice()
			gotSuffix := displayChannelName(ch, no)
			gotEnabled := enabledByDefault(ch)
			if gotSuffix != tc.wantSuffix {
				t.Errorf("displayChannelName suffix = %q, want %q\n  IsCustomDPPrimaryChannel=%v IsCustomDPSecondaryChannel=%v HasSinglePrimaryCustomDP=%v",
					gotSuffix, tc.wantSuffix,
					ch.IsCustomDPPrimaryChannel(), ch.IsCustomDPSecondaryChannel(), ch.HasSinglePrimaryCustomDP())
			}
			if gotEnabled != tc.wantEnabledByDef {
				t.Errorf("enabledByDefault = %v, want %v", gotEnabled, tc.wantEnabledByDef)
			}
		})
	}
}
