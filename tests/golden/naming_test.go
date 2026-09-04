// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// updateNaming refreshes the golden file when run as
//
//	go test -update-naming ./tests/golden/...
//
// The flag is local so it does not collide with the session replay's
// own -update flag.
var updateNaming = flag.Bool("update-naming", false, "rewrite naming.golden.json")

// namingCase is one device/channel/parameter triple plus the expected
// Outputs from See
// And
// For the upstream rules.
type namingCase struct {
	// Inputs.
	DeviceName    string `json:"device_name"`
	DeviceAddress string `json:"device_address"`
	InterfaceID   string `json:"interface_id,omitempty"`
	ChannelName   string `json:"channel_name,omitempty"`
	ChannelAddr   string `json:"channel_address"`
	ChannelNo     int    `json:"channel_no"`
	ChannelType   string `json:"channel_type,omitempty"`
	Parameter     string `json:"parameter,omitempty"`
	Postfix       string `json:"postfix,omitempty"`
	CentralID     string `json:"central_id,omitempty"`
	Prefix        string `json:"prefix,omitempty"`

	// Expected outputs.
	WantDpName     string `json:"want_dp_name"`
	WantDpFullName string `json:"want_dp_full_name"`
}

// TestNamingPipelineGoldfiles freezes the device/channel/parameter →
// name pipeline against a golden table.
//
// It drives [device.BuildDataPointName] and
// [device.BuildCustomDataPointName] — the functions the device pipeline,
// the event bridge and the REST handlers actually call. It used to drive
// a second naming family on *Channel that no production code reached,
// and the two had drifted apart in eighteen of these twenty cases: the
// dead one emitted raw wire parameter names ("LOW_BAT") where the daemon
// emits title-cased ones ("Low Bat"), and it collapsed every custom-DP
// postfix onto the postfix word where the daemon emits the channel-group
// marker. So this table froze names no operator ever saw. The dead
// family is gone; these expectations are what the daemon produces.
//
// Two of them are recorded rather than endorsed. Case 14 yields "Wind
// Wind Speed" and case 19 "Display Display Data String": a channel or
// device name that is also the first word of the parameter stutters.
// That is what the pipeline does today, and freezing it is how a change
// to it becomes visible.
//
// Refresh with: go test -update-naming ./tests/golden/...
func TestNamingPipelineGoldfiles(t *testing.T) {
	cases := []namingCase{
		// 1: Plain HmIP dimmer LEVEL — channel name carries the device prefix.
		{
			DeviceName:    "Wohnzimmer Licht",
			DeviceAddress: "ABC0001",
			ChannelName:   "Wohnzimmer Licht-Kanal",
			ChannelAddr:   "ABC0001:1",
			ChannelNo:     1,
			ChannelType:   "DIMMER",
			Parameter:     "LEVEL",
			CentralID:     "central1",

			WantDpName:     "Kanal Level",
			WantDpFullName: "Wohnzimmer Licht Kanal Level",
		},
		// 2: Channel without explicit name → falls back to parameter alone.
		{
			DeviceName:    "Sensor",
			DeviceAddress: "ABC0001",
			ChannelAddr:   "ABC0001:0",
			ChannelNo:     0,
			ChannelType:   "MAINTENANCE",
			Parameter:     "LOW_BAT",
			CentralID:     "central1",

			WantDpName:     "Low Bat",
			WantDpFullName: "Sensor Low Bat",
		},
		// 3: No parameter → device-only fallback for full name.
		{
			DeviceName:    "Heizkoerper Bad",
			DeviceAddress: "ABC0001",
			ChannelAddr:   "ABC0001:1",
			ChannelNo:     1,
			ChannelType:   "CLIMATE",
			CentralID:     "central1",

			WantDpName:     "",
			WantDpFullName: "Heizkoerper Bad",
		},
		// 4: Custom-DP with postfix — base name overridden by upper-cased postfix.
		{
			DeviceName:    "Wohnzimmer Licht",
			DeviceAddress: "ABC0001",
			ChannelName:   "Wohnzimmer Licht-Kanal",
			ChannelAddr:   "ABC0001:1",
			ChannelNo:     1,
			ChannelType:   "DIMMER",
			Parameter:     "LEVEL",
			Postfix:       "color",
			CentralID:     "central1",

			WantDpName:     "Kanal",
			WantDpFullName: "Wohnzimmer Licht Kanal",
		},
		// (Translation derived from fullName "Wohnzimmer Licht Kanal COLOR" → "wohnzimmer_licht_kanal_color".)
		// 5: ButtonLock postfix.
		{
			DeviceName:    "Aussentuere",
			DeviceAddress: "DEF0002",
			ChannelAddr:   "DEF0002:1",
			ChannelNo:     1,
			ChannelType:   "BUTTON_LOCK",
			Parameter:     "BUTTON_LOCK",
			Postfix:       "BUTTON_LOCK",
			CentralID:     "central2",

			WantDpName:     "vch1",
			WantDpFullName: "Aussentuere vch1",
		},
		// 6: Hub-style address gets the central_id prefix in unique_id.
		{
			DeviceName:    "System",
			DeviceAddress: "Sysvar",
			ChannelAddr:   "Sysvar",
			ChannelNo:     0,
			Parameter:     "PRES",
			CentralID:     "central42",

			WantDpName:     "Pres",
			WantDpFullName: "System Pres",
		},
		// 7: Internal address (INT000…) namespaced by central_id.
		{
			DeviceName:    "Virtual",
			DeviceAddress: "INT0000001",
			ChannelAddr:   "INT0000001",
			ChannelNo:     0,
			Parameter:     "STATE",
			CentralID:     "central42",

			WantDpName:     "State",
			WantDpFullName: "Virtual State",
		},
		// 8: Address with dash gets normalised to underscore.
		{
			DeviceName:    "Garage",
			DeviceAddress: "ABC-0003",
			ChannelAddr:   "ABC-0003:1",
			ChannelNo:     1,
			Parameter:     "DOOR_STATE",
			CentralID:     "central3",

			WantDpName:     "Door State",
			WantDpFullName: "Garage Door State",
		},
		// 9: Prefix-based unique_id (events / button presses).
		{
			DeviceName:    "Funk-Taster",
			DeviceAddress: "ABC0004",
			ChannelAddr:   "ABC0004:1",
			ChannelNo:     1,
			Parameter:     "PRESS_SHORT",
			Prefix:        "event",
			CentralID:     "central4",

			WantDpName:     "Press Short",
			WantDpFullName: "Funk-Taster Press Short",
		},
		// 10: ColorTemp postfix — postfix replaces parameter slot.
		{
			DeviceName:    "Tisch Lampe",
			DeviceAddress: "GHI0005",
			ChannelAddr:   "GHI0005:2",
			ChannelNo:     2,
			ChannelType:   "DIMMER",
			Parameter:     "LEVEL",
			Postfix:       "color_temp",
			CentralID:     "central5",

			WantDpName:     "vch2",
			WantDpFullName: "Tisch Lampe vch2",
		},
		// 11: Effect postfix.
		{
			DeviceName:    "RGB-Strip",
			DeviceAddress: "JKL0006",
			ChannelAddr:   "JKL0006:1",
			ChannelNo:     1,
			ChannelType:   "DIMMER",
			Parameter:     "PROGRAM",
			Postfix:       "effect",
			CentralID:     "central6",

			WantDpName:     "vch1",
			WantDpFullName: "RGB-Strip vch1",
		},
		// 12: HS postfix (RGBW light).
		{
			DeviceName:    "Decken-Spot",
			DeviceAddress: "MNO0007",
			ChannelAddr:   "MNO0007:3",
			ChannelNo:     3,
			Parameter:     "HUE",
			Postfix:       "hs",
			CentralID:     "central7",

			WantDpName:     "vch3",
			WantDpFullName: "Decken-Spot vch3",
		},
		// 13: Cover with no parameter and no channel name.
		{
			DeviceName:    "Rolladen",
			DeviceAddress: "PQR0008",
			ChannelAddr:   "PQR0008:1",
			ChannelNo:     1,
			ChannelType:   "BLIND",
			CentralID:     "central8",

			WantDpName:     "",
			WantDpFullName: "Rolladen",
		},
		// 14: Channel name without device prefix passes through.
		{
			DeviceName:    "Wetterstation",
			DeviceAddress: "STU0009",
			ChannelName:   "Wind",
			ChannelAddr:   "STU0009:5",
			ChannelNo:     5,
			Parameter:     "WIND_SPEED",
			CentralID:     "central9",

			WantDpName:     "Wind Wind Speed",
			WantDpFullName: "Wetterstation Wind Wind Speed",
		},
		// 15: Lower-case device name + uppercase parameter.
		{
			DeviceName:    "Steckdose",
			DeviceAddress: "VWX0010",
			ChannelAddr:   "VWX0010:1",
			ChannelNo:     1,
			ChannelType:   "SWITCH",
			Parameter:     "STATE",
			CentralID:     "central10",

			WantDpName:     "State",
			WantDpFullName: "Steckdose State",
		},
		// 16: Mehrere Bindestriche im Device-Namen.
		{
			DeviceName:    "Smart-Home-Heizung-1",
			DeviceAddress: "YZA0011",
			ChannelAddr:   "YZA0011:1",
			ChannelNo:     1,
			ChannelType:   "CLIMATE",
			Parameter:     "ACTUAL_TEMPERATURE",
			CentralID:     "central11",

			WantDpName:     "Actual Temperature",
			WantDpFullName: "Smart-Home-Heizung-1 Actual Temperature",
		},
		// 17: Sound postfix (MP3P) — postfix replaces parameter slot.
		{
			DeviceName:    "MP3 Player",
			DeviceAddress: "BCD0012",
			ChannelAddr:   "BCD0012:2",
			ChannelNo:     2,
			Parameter:     "LEVEL",
			Postfix:       "sound",
			CentralID:     "central12",

			WantDpName:     "vch2",
			WantDpFullName: "MP3 Player vch2",
		},
		// 18: BidCoS-RF special-prefix address gets central_id namespace.
		{
			DeviceName:    "BidCoS-RF",
			DeviceAddress: "BidCoS-RF",
			ChannelAddr:   "BidCoS-RF",
			ChannelNo:     0,
			Parameter:     "PRESS_SHORT",
			CentralID:     "central13",

			WantDpName:     "Press Short",
			WantDpFullName: "BidCoS-RF Press Short",
		},
		// 19: Display row.
		{
			DeviceName:    "Display",
			DeviceAddress: "EFG0014",
			ChannelAddr:   "EFG0014:3",
			ChannelNo:     3,
			Parameter:     "DISPLAY_DATA_STRING",
			CentralID:     "central14",

			WantDpName:     "Display Data String",
			WantDpFullName: "Display Display Data String",
		},
		// 20: Empty parameter on a named channel — channel only.
		{
			DeviceName:    "Wohnung",
			DeviceAddress: "HIJ0015",
			ChannelName:   "Wohnung-Kanal",
			ChannelAddr:   "HIJ0015:1",
			ChannelNo:     1,
			ChannelType:   "MAINTENANCE",
			CentralID:     "central15",

			WantDpName:     "Kanal",
			WantDpFullName: "Wohnung Kanal",
		},
	}

	got := make([]map[string]any, 0, len(cases))
	for i, c := range cases {
		d := device.New(device.Config{
			InterfaceID: c.InterfaceID,
			Address:     c.DeviceAddress,
			Name:        c.DeviceName,
		})
		ch := d.AddChannel(c.ChannelAddr, c.ChannelNo, c.ChannelType, hmenum.ParamsetKeyValues)
		if c.ChannelName != "" {
			ch.SetName(c.ChannelName)
		}

		nd := device.BuildDataPointName(ch, c.Parameter, "")
		if c.Postfix != "" {
			nd = device.BuildCustomDataPointName(ch, c.Postfix, "")
		}
		dpName, fullName := nd.Name(), nd.FullName()

		row := map[string]any{
			"case_no":              i + 1,
			"device":               c.DeviceName,
			"channel_address":      c.ChannelAddr,
			"parameter":            c.Parameter,
			"postfix":              c.Postfix,
			"prefix":               c.Prefix,
			"central_id":           c.CentralID,
			"data_point_name":      dpName,
			"data_point_full_name": fullName,
			"channel_postfix":      nd.ChannelPostfix,
		}
		got = append(got, row)

		if dpName != c.WantDpName {
			t.Errorf("case %d (%s): DataPointName=%q, want %q",
				i+1, c.DeviceName, dpName, c.WantDpName)
		}
		if fullName != c.WantDpFullName {
			t.Errorf("case %d (%s): DataPointFullName=%q, want %q",
				i+1, c.DeviceName, fullName, c.WantDpFullName)
		}
	}

	goldenPath := filepath.Join("testdata", "naming.golden.json")
	if *updateNaming {
		_ = os.MkdirAll(filepath.Dir(goldenPath), 0o755)
		blob, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(goldenPath, blob, 0o644); err != nil { //nolint:gosec // G304: path is constructed from test package root, not user input
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath) //nolint:gosec // G304: path is constructed from test package root, not user input
	if err != nil {
		t.Skipf("naming goldfile missing — run `go test -update-naming ./tests/golden/...` first: %v", err)
		return
	}
	gotBlob, _ := json.MarshalIndent(got, "", "  ")
	if !bytes.Equal(gotBlob, stripCR(want)) {
		t.Fatal("naming.golden.json drift; rerun with -update-naming after intentional changes")
	}
}

// stripCR removes \r bytes from b. Windows checkouts with
// core.autocrlf=true rewrite LF→CRLF on text files; the daemon's
// JSON encoder always emits LF, so byte-exact golden comparisons
// must normalise the on-disk fixture before comparing.
func stripCR(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{'\r'}, nil)
}
