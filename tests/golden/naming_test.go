// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	WantDpName      string `json:"want_dp_name"`
	WantDpFullName  string `json:"want_dp_full_name"`
	WantUniqueID    string `json:"want_unique_id"`
	WantTranslation string `json:"want_translation_key"`
}

// TestNamingPipelineGoldfiles freezes the device/channel/parameter
// → name pipeline against a golden table. Each case mirrors a
// Concrete
// regression net for `M-P0-16` (Naming-Parität).
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

			WantDpName:      "Kanal LEVEL",
			WantDpFullName:  "Wohnzimmer Licht Kanal LEVEL",
			WantUniqueID:    "abc0001_1_level",
			WantTranslation: "wohnzimmer_licht_kanal_level",
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

			WantDpName:      "LOW_BAT",
			WantDpFullName:  "Sensor LOW_BAT",
			WantUniqueID:    "abc0001_0_low_bat",
			WantTranslation: "sensor_low_bat",
		},
		// 3: No parameter → device-only fallback for full name.
		{
			DeviceName:    "Heizkoerper Bad",
			DeviceAddress: "ABC0001",
			ChannelAddr:   "ABC0001:1",
			ChannelNo:     1,
			ChannelType:   "CLIMATE",
			CentralID:     "central1",

			WantDpName:      "",
			WantDpFullName:  "Heizkoerper Bad",
			WantUniqueID:    "abc0001_1",
			WantTranslation: "heizkoerper_bad",
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

			WantDpName:      "Kanal COLOR",
			WantDpFullName:  "Wohnzimmer Licht Kanal COLOR",
			WantUniqueID:    "abc0001_1_level",
			WantTranslation: "wohnzimmer_licht_kanal_color",
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

			WantDpName:      "BUTTON_LOCK",
			WantDpFullName:  "Aussentuere BUTTON_LOCK",
			WantUniqueID:    "def0002_1_button_lock",
			WantTranslation: "aussentuere_button_lock",
		},
		// 6: Hub-style address gets the central_id prefix in unique_id.
		{
			DeviceName:    "System",
			DeviceAddress: "Sysvar",
			ChannelAddr:   "Sysvar",
			ChannelNo:     0,
			Parameter:     "PRES",
			CentralID:     "central42",

			WantDpName:      "PRES",
			WantDpFullName:  "System PRES",
			WantUniqueID:    "central42_sysvar_pres",
			WantTranslation: "system_pres",
		},
		// 7: Internal address (INT000…) namespaced by central_id.
		{
			DeviceName:    "Virtual",
			DeviceAddress: "INT0000001",
			ChannelAddr:   "INT0000001",
			ChannelNo:     0,
			Parameter:     "STATE",
			CentralID:     "central42",

			WantDpName:      "STATE",
			WantDpFullName:  "Virtual STATE",
			WantUniqueID:    "central42_int0000001_state",
			WantTranslation: "virtual_state",
		},
		// 8: Address with dash gets normalised to underscore.
		{
			DeviceName:    "Garage",
			DeviceAddress: "ABC-0003",
			ChannelAddr:   "ABC-0003:1",
			ChannelNo:     1,
			Parameter:     "DOOR_STATE",
			CentralID:     "central3",

			WantDpName:      "DOOR_STATE",
			WantDpFullName:  "Garage DOOR_STATE",
			WantUniqueID:    "abc_0003_1_door_state",
			WantTranslation: "garage_door_state",
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

			WantDpName:      "PRESS_SHORT",
			WantDpFullName:  "Funk-Taster PRESS_SHORT",
			WantUniqueID:    "event_abc0004_1_press_short",
			WantTranslation: "funk_taster_press_short",
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

			WantDpName:      "COLOR_TEMP",
			WantDpFullName:  "Tisch Lampe COLOR_TEMP",
			WantUniqueID:    "ghi0005_2_level",
			WantTranslation: "tisch_lampe_color_temp",
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

			WantDpName:      "EFFECT",
			WantDpFullName:  "RGB-Strip EFFECT",
			WantUniqueID:    "jkl0006_1_program",
			WantTranslation: "rgb_strip_effect",
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

			WantDpName:      "HS",
			WantDpFullName:  "Decken-Spot HS",
			WantUniqueID:    "mno0007_3_hue",
			WantTranslation: "decken_spot_hs",
		},
		// 13: Cover with no parameter and no channel name.
		{
			DeviceName:    "Rolladen",
			DeviceAddress: "PQR0008",
			ChannelAddr:   "PQR0008:1",
			ChannelNo:     1,
			ChannelType:   "BLIND",
			CentralID:     "central8",

			WantDpName:      "",
			WantDpFullName:  "Rolladen",
			WantUniqueID:    "pqr0008_1",
			WantTranslation: "rolladen",
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

			WantDpName:      "Wind WIND_SPEED",
			WantDpFullName:  "Wetterstation Wind WIND_SPEED",
			WantUniqueID:    "stu0009_5_wind_speed",
			WantTranslation: "wetterstation_wind_wind_speed",
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

			WantDpName:      "STATE",
			WantDpFullName:  "Steckdose STATE",
			WantUniqueID:    "vwx0010_1_state",
			WantTranslation: "steckdose_state",
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

			WantDpName:      "ACTUAL_TEMPERATURE",
			WantDpFullName:  "Smart-Home-Heizung-1 ACTUAL_TEMPERATURE",
			WantUniqueID:    "yza0011_1_actual_temperature",
			WantTranslation: "smart_home_heizung_1_actual_temperature",
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

			WantDpName:      "SOUND",
			WantDpFullName:  "MP3 Player SOUND",
			WantUniqueID:    "bcd0012_2_level",
			WantTranslation: "mp3_player_sound",
		},
		// 18: BidCoS-RF special-prefix address gets central_id namespace.
		{
			DeviceName:    "BidCoS-RF",
			DeviceAddress: "BidCoS-RF",
			ChannelAddr:   "BidCoS-RF",
			ChannelNo:     0,
			Parameter:     "PRESS_SHORT",
			CentralID:     "central13",

			WantDpName:      "PRESS_SHORT",
			WantDpFullName:  "BidCoS-RF PRESS_SHORT",
			WantUniqueID:    "central13_bidcos_rf_press_short",
			WantTranslation: "bidcos_rf_press_short",
		},
		// 19: Display row.
		{
			DeviceName:    "Display",
			DeviceAddress: "EFG0014",
			ChannelAddr:   "EFG0014:3",
			ChannelNo:     3,
			Parameter:     "DISPLAY_DATA_STRING",
			CentralID:     "central14",

			WantDpName:      "DISPLAY_DATA_STRING",
			WantDpFullName:  "Display DISPLAY_DATA_STRING",
			WantUniqueID:    "efg0014_3_display_data_string",
			WantTranslation: "display_display_data_string",
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

			WantDpName:      "Kanal",
			WantDpFullName:  "Wohnung Kanal",
			WantUniqueID:    "hij0015_1",
			WantTranslation: "wohnung_kanal",
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
			ch.Name = c.ChannelName
		}

		var dpName, fullName string
		if c.Postfix != "" {
			dpName = ch.CustomDataPointName(c.Parameter, c.Postfix)
			fullName = ch.CustomDataPointFullName(c.Parameter, c.Postfix)
		} else {
			dpName = ch.DataPointName(c.Parameter)
			fullName = ch.DataPointFullName(c.Parameter)
		}
		uid := device.GenerateUniqueID(c.CentralID, c.ChannelAddr, c.Parameter, c.Prefix)
		// Translation key is the slug of the user-facing full name — including the
		// postfix slot for custom DPs.
		tk := device.GenerateTranslationKey(fullName)

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
			"unique_id":            uid,
			"translation_key":      tk,
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
		if uid != c.WantUniqueID {
			t.Errorf("case %d (%s): UniqueID=%q, want %q",
				i+1, c.DeviceName, uid, c.WantUniqueID)
		}
		if tk != c.WantTranslation {
			t.Errorf("case %d (%s): TranslationKey=%q, want %q",
				i+1, c.DeviceName, tk, c.WantTranslation)
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
