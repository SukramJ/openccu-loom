// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putMasterSensorDP plants a MASTER-paramset sensor on the channel and
// returns it, so a test can read the usage the visibility passes derive.
func putMasterSensorDP(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[string] {
	dp := generic.NewSensor[string](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	})
	ch.PutMaster(dp)
	return dp
}

// parseEntry is a test helper that turns one operator un-ignore line into
// the entry the decider consumes, failing loudly on a malformed line.
func parseEntry(t *testing.T, line string) visibility.UnIgnoreEntry {
	t.Helper()
	res := visibility.ParseUnIgnoreLine(line)
	if res.Err != "" || res.Entry == nil {
		t.Fatalf("ParseUnIgnoreLine(%q) failed: %q", line, res.Err)
	}
	return *res.Entry
}

// TestApplyUnIgnoredMarksPromotesMasterParameter pins that a MASTER
// un-ignore pattern reaches the data point. The hidden-parameter pass
// force-ignores every member of the hidden set unless the DP carries the
// operator mark, so a MASTER pattern that never produces a mark is inert:
// the whole climate MASTER set the candidate picker offers stays
// usage=ignored however the operator configures it.
func TestApplyUnIgnoredMarksPromotesMasterParameter(t *testing.T) {
	t.Parallel()

	dec := visibility.NewParameterDecider(nil)
	dec.LoadUnIgnore([]visibility.UnIgnoreEntry{
		parseEntry(t, "TEMPERATURE_OFFSET:MASTER@HmIP-BWTH:1"),
	})

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TESTM01",
		Model:       "HmIP-BWTH",
	})
	ch := dev.AddChannel("TESTM01:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dp := putMasterSensorDP(ch, hmenum.ParameterTemperatureOffset)

	// Production pass order at boot.
	visibility.ApplyUnIgnoredMarks(dev, dec)
	visibility.ApplyIgnoredParameterMarks(dev, dec)
	visibility.ApplyHiddenParameterMarksWithDecider(dev, dec)

	if !dp.IsUnIgnored() {
		t.Error("MASTER un-ignore pattern did not set the DP-level mark")
	}
	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Errorf("TEMPERATURE_OFFSET (MASTER) Usage() = %v, want DataPoint", got)
	}
}

// TestApplyUnIgnoredMarksWithdrawsMasterMark is the inverse: removing the
// pattern must put the MASTER parameter back under the hidden verdict
// instead of leaving it promoted until the daemon restarts.
func TestApplyUnIgnoredMarksWithdrawsMasterMark(t *testing.T) {
	t.Parallel()

	dec := visibility.NewParameterDecider(nil)
	dec.LoadUnIgnore([]visibility.UnIgnoreEntry{
		parseEntry(t, "TEMPERATURE_OFFSET:MASTER@HmIP-BWTH:1"),
	})

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TESTM02",
		Model:       "HmIP-BWTH",
	})
	ch := dev.AddChannel("TESTM02:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dp := putMasterSensorDP(ch, hmenum.ParameterTemperatureOffset)

	visibility.ApplyUnIgnoredMarks(dev, dec)
	visibility.ApplyIgnoredParameterMarks(dev, dec)
	visibility.ApplyHiddenParameterMarksWithDecider(dev, dec)

	dec.LoadUnIgnore(nil)
	visibility.ApplyUnIgnoredMarks(dev, dec)

	if got := dp.Usage(); got == hmenum.DataPointUsageDataPoint {
		t.Error("MASTER parameter still surfaces after its un-ignore rule was removed")
	}
}

// TestApplyUnIgnoredMarksHonoursConcreteChannel pins that a channel-scoped
// VALUES pattern — the shape the SPA candidate picker emits — promotes the
// parameter on the selected channel only. Querying the decider without a
// channel number makes an unknown channel match every entry, which leaks
// the promotion onto every channel of the model.
func TestApplyUnIgnoredMarksHonoursConcreteChannel(t *testing.T) {
	t.Parallel()

	dec := visibility.NewParameterDecider(nil)
	dec.LoadUnIgnore([]visibility.UnIgnoreEntry{
		parseEntry(t, "LOW_BAT:VALUES@HmIP-eTRV-2:0"),
	})

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TESTC01",
		Model:       "HmIP-eTRV-2",
	})
	ch0 := dev.AddChannel("TESTC01:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch1 := dev.AddChannel("TESTC01:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dp0 := putValuesBoolDP(ch0, hmenum.ParameterLowBat)
	dp1 := putValuesBoolDP(ch1, hmenum.ParameterLowBat)

	visibility.ApplyUnIgnoredMarks(dev, dec)

	if !dp0.IsUnIgnored() {
		t.Error("channel 0 (the selected channel) must carry the un-ignore mark")
	}
	if dp1.IsUnIgnored() {
		t.Error("channel 1 must not be un-ignored by a pattern scoped to channel 0")
	}
}
