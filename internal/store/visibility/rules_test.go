// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ignoredParameters
// ---------------------------------------------------------------------------

func TestIgnoredParametersContainment(t *testing.T) {
	t.Parallel()
	positives := []string{
		"AES_KEY",
		"INHIBIT",
		"INSTALL_MODE",
		"WEEK_PROGRAM_POINTER",
		"BOOST_STATE",
		"PARTY_TEMPERATURE",
		"SUBMIT",
		"CMD_RETL",
		"CMD_RETS",
		"WIN_RELEASE",
	}
	for _, p := range positives {
		t.Run("positive/"+p, func(t *testing.T) {
			t.Parallel()
			if _, ok := ignoredParameters[p]; !ok {
				t.Errorf("ignoredParameters must contain %q", p)
			}
		})
	}

	negatives := []string{
		"STATE",
		"LEVEL",
		"TEMPERATURE",
		"ON_TIME",
		"LOWBAT",
		"OPERATING_VOLTAGE",
	}
	for _, p := range negatives {
		t.Run("negative/"+p, func(t *testing.T) {
			t.Parallel()
			if _, ok := ignoredParameters[p]; ok {
				t.Errorf("ignoredParameters must NOT contain %q", p)
			}
		})
	}
}

func TestIgnoredParametersMinimumSize(t *testing.T) {
	t.Parallel()
	const want = 62
	if got := len(ignoredParameters); got < want {
		t.Errorf("ignoredParameters: got %d entries, want >= %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// parameterIsWildcardIgnored
// ---------------------------------------------------------------------------

func TestParameterIsWildcardIgnored(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		// End-pattern matches
		{"FOO_OVERFLOW", true},
		{"BAR_OVERRUN", true},
		{"FOO_REPORTING", true},
		{"BAR_RESULT", true},
		{"FOO_STATUS", true},
		{"BAR_SUBMIT", true},
		// Start-pattern matches
		{"ADJUSTING_SOMETHING", true},
		{"ERR_TTM_SOMETHING", true},
		{"HANDLE_SOMETHING", true},
		// The only two HANDLE_* names any device is known to declare. The raw
		// pattern still matches them — the carve-out that keeps them alive is
		// the per-device un-ignore entry, checked one level up in the decider.
		{"HANDLE_LOCK", true},
		{"HANDLE_LED_MODE", true},
		{"IDENTIFY_SOMETHING", true},
		{"PARTY_START_SOMETHING", true},
		{"PARTY_STOP_SOMETHING", true},
		{"STATUS_FLAG_SOMETHING", true},
		// No match
		{"STATE", false},
		{"LEVEL", false},
		{"TEMPERATURE", false},
		{"PARTY_TEMPERATURE", false},
		{"OVERFLOW", false}, // no underscore prefix → not matched by end pattern
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parameterIsWildcardIgnored(tc.name); got != tc.want {
				t.Errorf("parameterIsWildcardIgnored(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hiddenParameters
// ---------------------------------------------------------------------------

func TestHiddenParametersContainment(t *testing.T) {
	t.Parallel()
	positives := []hmenum.Parameter{
		hmenum.ParameterActivityState,
		hmenum.ParameterChannelOperationMode,
		hmenum.ParameterConfigPending,
		hmenum.ParameterDirection,
		hmenum.ParameterError,
		hmenum.ParameterHeatingValveType,
		hmenum.ParameterLowBatLimit,
		hmenum.ParameterMinMaxNotRelevantForManuMode,
		hmenum.ParameterOptimumStartStop,
		hmenum.ParameterSection,
		hmenum.ParameterStickyUnreach,
		hmenum.ParameterTemperatureMaximum,
		hmenum.ParameterTemperatureMinimum,
		hmenum.ParameterTemperatureOffset,
		hmenum.ParameterUnreach,
		hmenum.ParameterUpdatePending,
		hmenum.ParameterWorking,
	}
	for _, p := range positives {
		t.Run("positive/"+string(p), func(t *testing.T) {
			t.Parallel()
			if _, ok := hiddenParameters[p]; !ok {
				t.Errorf("hiddenParameters must contain %q", p)
			}
		})
	}

	negatives := []hmenum.Parameter{
		hmenum.ParameterState,
		hmenum.ParameterLevel,
		hmenum.ParameterTemperature,
	}
	for _, p := range negatives {
		t.Run("negative/"+string(p), func(t *testing.T) {
			t.Parallel()
			if _, ok := hiddenParameters[p]; ok {
				t.Errorf("hiddenParameters must NOT contain %q", p)
			}
		})
	}
}

func TestHiddenParametersMinimumSize(t *testing.T) {
	t.Parallel()
	const want = 17
	if got := len(hiddenParameters); got < want {
		t.Errorf("hiddenParameters: got %d entries, want >= %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// unIgnoreParametersByDevice
// ---------------------------------------------------------------------------

func TestUnIgnoreParametersByDevice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model     string
		parameter hmenum.Parameter
		want      bool
	}{
		// Positives
		{"HmIP-DLD", hmenum.ParameterErrorJammed, true},
		{"HmIP-DLP", hmenum.ParameterErrorJammed, true},
		{"HmIP-SWSD", hmenum.ParameterSmokeLevel, true},
		{"HmIP-SWSD", hmenum.ParameterDirtLevel, true},
		{"HmIP-SWSD", hmenum.ParameterSmokeDetectorAlarmStatus, true},
		{"HmIP-WRCD", hmenum.ParameterDisplayDataCommit, true},
		{"HmIP-WRCD", hmenum.ParameterDisplayDataString, true},
		{"HmIP-WRCD", hmenum.ParameterInterval, true},
		{"HM-OU-LED16", hmenum.ParameterLEDStatus, true},
		{"HM-Sec-Win", hmenum.ParameterDirection, true},
		{"HM-Sec-Win", hmenum.ParameterWorking, true},
		{"HM-Sec-Win", hmenum.ParameterError, true},
		{"HM-Sec-Key", hmenum.ParameterDirection, true},
		{"HmIP-PCBS-BAT", hmenum.ParameterOperatingVoltage, true},
		{"HmIP-PCBS-BAT", hmenum.ParameterLowBat, true},
		{"BC-RT-TRX-CyG", hmenum.ParameterWeekProgramPointer, true},
		{"BC-RT-TRX-CyN", hmenum.ParameterWeekProgramPointer, true},
		{"BC-TC-C-WM", hmenum.ParameterWeekProgramPointer, true},
		{"HM-CC-RT-DN", hmenum.ParameterWeekProgramPointer, true},
		{"HM-CC-VG-1", hmenum.ParameterWeekProgramPointer, true},
		{"HM-TC-IT-WM-W-EU", hmenum.ParameterWeekProgramPointer, true},
		// Negatives
		{"HmIP-DLD", hmenum.ParameterState, false},
		{"HmIP-BS2", hmenum.ParameterErrorJammed, false},
		{"HmIP-STH", hmenum.ParameterWeekProgramPointer, false},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+string(tc.parameter), func(t *testing.T) {
			t.Parallel()
			params, ok := unIgnoreParametersByDevice[tc.model]
			var got bool
			if ok {
				_, got = params[tc.parameter]
			}
			if got != tc.want {
				t.Errorf("unIgnoreParametersByDevice[%q][%q] = %v, want %v", tc.model, tc.parameter, got, tc.want)
			}
		})
	}
}

func TestUnIgnoreParametersByDeviceMinimumSize(t *testing.T) {
	t.Parallel()
	const want = 13
	if got := len(unIgnoreParametersByDevice); got < want {
		t.Errorf("unIgnoreParametersByDevice: got %d model entries, want >= %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// ignoreParametersByDevice
// ---------------------------------------------------------------------------

func TestIgnoreParametersByDevice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		parameter string
		model     string
		want      bool
	}{
		// Positives
		{"CURRENT_ILLUMINATION", "HmIP-SMI", true},
		{"CURRENT_ILLUMINATION", "HmIP-SMO", true},
		{"CURRENT_ILLUMINATION", "HmIP-SPI", true},
		{"CURRENT_ILLUMINATION", "HmIP-UDI-SMI", true},
		{"LOWBAT", "HM-LC-Sw1-DR", true},
		{"LOWBAT", "HM-LC-Sw1-FM", true},
		{"LOWBAT", "HM-SwI-3-FM", true},
		{"LOW_BAT", "HmIP-BWTH", true},
		{"LOW_BAT", "HmIP-PCBS", true},
		{"OPERATING_VOLTAGE", "HmIP-PS", true},
		{"OPERATING_VOLTAGE", "HmIP-BWTH", true},
		{"VALVE_STATE", "HmIP-FALMOT-C8", true},
		{"VALVE_STATE", "HmIPW-FALMOT-C12", true},
		{"VALVE_STATE", "HmIP-FALMOT-C12", true},
		// Negatives
		{"CURRENT_ILLUMINATION", "HmIP-STH", false},
		{"LOWBAT", "HmIP-BS2", false},
		{"STATE", "HmIP-SMI", false},
	}
	for _, tc := range cases {
		t.Run(tc.parameter+"/"+tc.model, func(t *testing.T) {
			t.Parallel()
			models, ok := ignoreParametersByDevice[tc.parameter]
			var got bool
			if ok {
				_, got = models[tc.model]
			}
			if got != tc.want {
				t.Errorf("ignoreParametersByDevice[%q][%q] = %v, want %v", tc.parameter, tc.model, got, tc.want)
			}
		})
	}
}

func TestIgnoreParametersByDeviceMinimumSize(t *testing.T) {
	t.Parallel()
	const want = 5
	if got := len(ignoreParametersByDevice); got < want {
		t.Errorf("ignoreParametersByDevice: got %d parameter entries, want >= %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// relevantMasterParamsetsByDevice
// ---------------------------------------------------------------------------

func TestRelevantMasterParamsetsByDeviceContainment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model   string
		channel int
		param   hmenum.Parameter
		want    bool
	}{
		// Climate devices
		{"HmIP-STH", 1, hmenum.ParameterTemperatureOffset, true},
		{"HmIP-eTRV", 1, hmenum.ParameterHeatingValveType, true},
		{"HmIP-WTH", 1, hmenum.ParameterWeekProgramPointer, true},
		{"HmIP-BWTH", 1, hmenum.ParameterTemperatureMaximum, true},
		{"HM-CC-RT-DN", 0, hmenum.ParameterTemperatureMinimum, false}, // nil-channel model uses empty map
		// ChannelOperationMode devices
		{"HmIP-DRSI1", 1, hmenum.ParameterChannelOperationMode, true},
		{"HmIP-FCI6", 3, hmenum.ParameterChannelOperationMode, true},
		{"HmIP-RGBW", 0, hmenum.ParameterDeviceOperationMode, true},
		// Negatives
		{"HmIP-STH", 2, hmenum.ParameterTemperatureOffset, false},
		{"HmIP-BS2", 1, hmenum.ParameterChannelOperationMode, false},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/ch"+string(rune('0'+tc.channel))+"/"+string(tc.param), func(t *testing.T) { //nolint:gosec // G115: tc.channel is a small channel number (0..9); '0'+channel is 48..57
			t.Parallel()
			entry, ok := relevantMasterParamsetsByDevice[tc.model]
			var got bool
			if ok {
				_, hasChannel := entry.Channels[tc.channel]
				_, hasParam := entry.Parameters[tc.param]
				got = hasChannel && hasParam
			}
			if got != tc.want {
				t.Errorf("relevantMasterParamsetsByDevice[%q] ch=%d param=%q: got %v, want %v",
					tc.model, tc.channel, tc.param, got, tc.want)
			}
		})
	}
}

func TestRelevantMasterParamsetsByDeviceMinimumSize(t *testing.T) {
	t.Parallel()
	const want = 26
	if got := len(relevantMasterParamsetsByDevice); got < want {
		t.Errorf("relevantMasterParamsetsByDevice: got %d entries, want >= %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// ignoreDevicesForDataPointEvents
// ---------------------------------------------------------------------------

func TestIgnoreDevicesForDataPointEventsContainment(t *testing.T) {
	t.Parallel()
	entry, ok := ignoreDevicesForDataPointEvents["HmIP-PS"]
	if !ok {
		t.Fatal("ignoreDevicesForDataPointEvents must contain HmIP-PS")
	}
	// ClickEvents must be present.
	if len(entry) == 0 {
		t.Fatal("HmIP-PS entry must contain at least one click event")
	}
	for p := range hmenum.ClickEvents {
		if _, found := entry[p]; !found {
			t.Errorf("HmIP-PS entry must contain click event %q", p)
		}
	}
	// Negative: non-click model must not be present.
	if _, ok := ignoreDevicesForDataPointEvents["HmIP-STH"]; ok {
		t.Error("ignoreDevicesForDataPointEvents must NOT contain HmIP-STH")
	}
}

// ---------------------------------------------------------------------------
// acceptParameterOnlyOnChannel
// ---------------------------------------------------------------------------

func TestAcceptParameterOnlyOnChannel(t *testing.T) {
	t.Parallel()
	ch, ok := acceptParameterOnlyOnChannel["LOWBAT"]
	if !ok {
		t.Fatal("acceptParameterOnlyOnChannel must contain LOWBAT")
	}
	if ch != 0 {
		t.Errorf("LOWBAT must be accepted only on channel 0, got %d", ch)
	}
	if _, ok := acceptParameterOnlyOnChannel["STATE"]; ok {
		t.Error("acceptParameterOnlyOnChannel must NOT contain STATE")
	}
}

// ---------------------------------------------------------------------------
// climateMasterParameters
// ---------------------------------------------------------------------------

func TestClimateMasterParameters(t *testing.T) {
	t.Parallel()
	must := []hmenum.Parameter{
		hmenum.ParameterHeatingValveType,
		hmenum.ParameterMinMaxNotRelevantForManuMode,
		hmenum.ParameterOptimumStartStop,
		hmenum.ParameterTemperatureMaximum,
		hmenum.ParameterTemperatureMinimum,
		hmenum.ParameterTemperatureOffset,
		hmenum.ParameterWeekProgramPointer,
	}
	for _, p := range must {
		t.Run("positive/"+string(p), func(t *testing.T) {
			t.Parallel()
			if _, ok := climateMasterParameters[p]; !ok {
				t.Errorf("climateMasterParameters must contain %q", p)
			}
		})
	}
	if _, ok := climateMasterParameters[hmenum.ParameterState]; ok {
		t.Error("climateMasterParameters must NOT contain STATE")
	}
}

// ---------------------------------------------------------------------------
// TestVisibilityRulesAlignedWithAiohomematic — structural cross-check
// ---------------------------------------------------------------------------

// TestVisibilityRulesAlignedWithAiohomematic asserts that all static rule
// maps have at least the minimum sizes documented in the Python reference
// and spot-checks key values that are unlikely to change.
func TestVisibilityRulesAlignedWithAiohomematic(t *testing.T) {
	t.Parallel()

	t.Run("ignoredParameters_count", func(t *testing.T) {
		t.Parallel()
		if n := len(ignoredParameters); n < 62 {
			t.Errorf("expected >= 62 ignoredParameters, got %d", n)
		}
	})

	t.Run("hiddenParameters_count", func(t *testing.T) {
		t.Parallel()
		if n := len(hiddenParameters); n < 17 {
			t.Errorf("expected >= 17 hiddenParameters, got %d", n)
		}
	})

	t.Run("relevantMasterParamsetsByDevice_count", func(t *testing.T) {
		t.Parallel()
		if n := len(relevantMasterParamsetsByDevice); n < 26 {
			t.Errorf("expected >= 26 relevantMasterParamsetsByDevice, got %d", n)
		}
	})

	t.Run("unIgnoreParametersByDevice_count", func(t *testing.T) {
		t.Parallel()
		if n := len(unIgnoreParametersByDevice); n < 13 {
			t.Errorf("expected >= 13 unIgnoreParametersByDevice, got %d", n)
		}
	})

	t.Run("ignoreParametersByDevice_count", func(t *testing.T) {
		t.Parallel()
		if n := len(ignoreParametersByDevice); n < 5 {
			t.Errorf("expected >= 5 ignoreParametersByDevice, got %d", n)
		}
	})

	t.Run("HmIP-PS_in_eventSuppression", func(t *testing.T) {
		t.Parallel()
		if _, ok := ignoreDevicesForDataPointEvents["HmIP-PS"]; !ok {
			t.Error("HmIP-PS must be in ignoreDevicesForDataPointEvents")
		}
	})

	t.Run("AES_KEY_in_ignoredParameters", func(t *testing.T) {
		t.Parallel()
		if _, ok := ignoredParameters["AES_KEY"]; !ok {
			t.Error("AES_KEY must be in ignoredParameters")
		}
	})

	t.Run("UNREACH_in_hiddenParameters", func(t *testing.T) {
		t.Parallel()
		if _, ok := hiddenParameters[hmenum.ParameterUnreach]; !ok {
			t.Error("UNREACH must be in hiddenParameters")
		}
	})

	t.Run("HmIPW-DRI32_channels_1_to_32", func(t *testing.T) {
		t.Parallel()
		entry, ok := relevantMasterParamsetsByDevice["HmIPW-DRI32"]
		if !ok {
			t.Fatal("HmIPW-DRI32 must be in relevantMasterParamsetsByDevice")
		}
		for ch := 1; ch <= 32; ch++ {
			if _, ok := entry.Channels[ch]; !ok {
				t.Errorf("HmIPW-DRI32: channel %d must be in Channels set", ch)
			}
		}
	})

	t.Run("HmIP-DRBLI4_channels_spot", func(t *testing.T) {
		t.Parallel()
		entry, ok := relevantMasterParamsetsByDevice["HmIP-DRBLI4"]
		if !ok {
			t.Fatal("HmIP-DRBLI4 must be in relevantMasterParamsetsByDevice")
		}
		for _, ch := range []int{1, 5, 9, 13, 17, 21} {
			if _, ok := entry.Channels[ch]; !ok {
				t.Errorf("HmIP-DRBLI4: channel %d must be in Channels set", ch)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Decider integration: static rules wired correctly
// ---------------------------------------------------------------------------

// TestDeciderIgnoresStaticIgnoredParameter verifies that a parameter from
// ignoredParameters is ignored by the ParameterDecider for VALUES paramset.
func TestDeciderIgnoresStaticIgnoredParameter(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// AES_KEY is in ignoredParameters; default model has no un-ignore.
	if !d.IsParameterIgnored("HmIP-STH", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.Parameter("AES_KEY")) {
		t.Error("AES_KEY must be ignored for VALUES paramset")
	}
}

// TestDeciderUnIgnoreByDeviceOverridesStaticIgnore verifies that a parameter
// in ignoredParameters is allowed when the model appears in
// unIgnoreParametersByDevice.
func TestDeciderUnIgnoreByDeviceOverridesStaticIgnore(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// WEEK_PROGRAM_POINTER is in ignoredParameters but is un-ignored for HM-CC-RT-DN.
	if d.IsParameterIgnored("HM-CC-RT-DN", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterWeekProgramPointer) {
		t.Error("WEEK_PROGRAM_POINTER must NOT be ignored for HM-CC-RT-DN (device un-ignore)")
	}
}

// TestDeciderIgnoreParametersByDeviceSuppressesForSpecificModel verifies that
// OPERATING_VOLTAGE is suppressed for HmIP-PS (in ignoreParametersByDevice)
// but allowed for a model not in the list.
func TestDeciderIgnoreParametersByDeviceSuppressesForSpecificModel(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	if !d.IsParameterIgnored("HmIP-PS", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE must be ignored for HmIP-PS (ignoreParametersByDevice)")
	}
	if d.IsParameterIgnored("HmIP-STH", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE must be allowed for HmIP-STH (not in suppress list)")
	}
}

// TestDeciderHiddenParametersAddedToRules verifies that hiddenParameters
// (e.g. UNREACH) are hidden by the default Rules (merged in NewRules).
func TestDeciderHiddenParametersAddedToRules(t *testing.T) {
	t.Parallel()
	r := NewRules()
	// UNREACH is in hiddenParameters; it must be in hiddenGlobal after NewRules().
	r.mu.RLock()
	_, ok := r.hiddenGlobal[hmenum.ParameterUnreach]
	r.mu.RUnlock()
	if !ok {
		t.Error("UNREACH must be in hiddenGlobal after NewRules() (from hiddenParameters)")
	}
}
