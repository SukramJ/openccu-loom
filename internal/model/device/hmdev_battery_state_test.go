// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmDevBatteryStateIsNeverReadAsAPercentage pins that BATTERY_STATE is
// not a source for [AvailabilityInfo.BatteryLevel], whatever its magnitude.
//
// The parameter is declared as a cell voltage on every device type that
// carries it — `<logical type="float" min="1.5" max="4.6" unit="V"/>` on
// ../OpenCCU-Base/src/devicetypes/rftypes/rf_cc_rt_dn.xml:2350-2359,
// rf_cc_rt_dn_bom.xml:2313-2319 and rf_tc_it_wm-w-eu.xml:5211-5220, which is
// the complete set in the shipped descriptor corpus. A previous
// `BATTERY_STATE > 10` test read the value as a percentage above that
// threshold; the descriptor says the reading is volts regardless, so a large
// value is a decoding fault, not a percentage.
//
// The 85 case is the one that bites: it is the value the removed threshold
// accepted, and it is out of the declared range on every declaring model.
func TestHmDevBatteryStateIsNeverReadAsAPercentage(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  float64
	}{
		{name: "declared minimum", raw: 1.5},
		{name: "mid range", raw: 3.0},
		{name: "declared maximum", raw: 4.6},
		{name: "above the removed threshold", raw: 85},
		{name: "millivolt-shaped", raw: 3000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New(Config{InterfaceID: "BidCos-RF", Address: "BATSTATE01", Model: "HM-CC-RT-DN"})
			ch0 := d.AddChannel("BATSTATE01:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
			ch0.Put(&fakeParameterDP{param: hmenum.ParameterBatteryState, raw: tc.raw})

			if got := d.Availability().Info().BatteryLevel; got != nil {
				t.Errorf("BATTERY_STATE = %v produced BatteryLevel = %d — BATTERY_STATE is a cell "+
					"voltage (unit V, 1.5-4.6), never a 0-100 percentage", tc.raw, *got)
			}
		})
	}
}
