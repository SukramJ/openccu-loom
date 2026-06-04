// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

import (
	"maps"
	"regexp"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// relevantMasterParamsetsByChannel lists the extra MASTER parameters that
// should be created as data points for a given channel number. nil key means
// "any / unknown channel".
var relevantMasterParamsetsByChannel = map[*int]map[hmenum.Parameter]struct{}{
	nil:           {hmenum.ParameterGlobalButtonLock: {}, hmenum.ParameterLowBatLimit: {}},
	channelPtr(0): {hmenum.ParameterGlobalButtonLock: {}, hmenum.ParameterLowBatLimit: {}},
}

// climateMasterParameters is the set of MASTER parameters relevant for
// climate control devices.
var climateMasterParameters = map[hmenum.Parameter]struct{}{
	hmenum.ParameterHeatingValveType:             {},
	hmenum.ParameterMinMaxNotRelevantForManuMode: {},
	hmenum.ParameterOptimumStartStop:             {},
	hmenum.ParameterTemperatureMaximum:           {},
	hmenum.ParameterTemperatureMinimum:           {},
	hmenum.ParameterTemperatureOffset:            {},
	hmenum.ParameterWeekProgramPointer:           {},
}

// channelOperationModeSet is a single-element set reused across entries.
var channelOperationModeSet = map[hmenum.Parameter]struct{}{
	hmenum.ParameterChannelOperationMode: {},
}

// ModelMasterEntry describes which channels and MASTER parameters are relevant
// for a specific device model. Mirrors the tuple in
// Py:RELEVANT_MASTER_PARAMSETS_BY_DEVICE.
type ModelMasterEntry struct {
	// Channels is the set of channel numbers for which the MASTER paramset
	// should be fetched. A nil channel number means "any / unknown channel".
	Channels map[int]struct{}
	// Parameters is the set of MASTER parameters to create as data points.
	Parameters map[hmenum.Parameter]struct{}
}

// relevantMasterParamsetsByDevice maps device model names to their MASTER
// paramset rules.
var relevantMasterParamsetsByDevice = map[string]ModelMasterEntry{
	"ALPHA-IP-RBG": {
		Channels:   channelSet(1),
		Parameters: climateMasterParameters,
	},
	"ELV-SH-TACO": {
		Channels:   channelSet(2),
		Parameters: channelOperationModeSet,
	},
	"HM-CC-RT-DN": {
		Channels:   map[int]struct{}{},
		Parameters: climateMasterParameters,
	},
	"HM-CC-VG-1": {
		Channels:   map[int]struct{}{},
		Parameters: climateMasterParameters,
	},
	"HM-TC-IT-WM-W-EU": {
		Channels:   map[int]struct{}{},
		Parameters: climateMasterParameters,
	},
	"HmIP-BWTH": {
		Channels:   channelSet(1, 8),
		Parameters: climateMasterParameters,
	},
	"HmIP-DRBLI4": {
		Channels:   channelSet(1, 2, 3, 4, 5, 6, 7, 8, 9, 13, 17, 21),
		Parameters: channelOperationModeSet,
	},
	"HmIP-DRDI3": {
		Channels:   channelSet(1, 2, 3),
		Parameters: channelOperationModeSet,
	},
	"HmIP-DRSI1": {
		Channels:   channelSet(1),
		Parameters: channelOperationModeSet,
	},
	"HmIP-DRSI4": {
		Channels:   channelSet(1, 2, 3, 4),
		Parameters: channelOperationModeSet,
	},
	"HmIP-DSD-PCB": {
		Channels:   channelSet(1),
		Parameters: channelOperationModeSet,
	},
	"HmIP-FCI1": {
		Channels:   channelSet(1),
		Parameters: channelOperationModeSet,
	},
	"HmIP-FCI6": {
		Channels:   channelRange(1, 7),
		Parameters: channelOperationModeSet,
	},
	"HmIP-FSI16": {
		Channels:   channelSet(1),
		Parameters: channelOperationModeSet,
	},
	"HmIP-HEATING": {
		Channels:   channelSet(1),
		Parameters: climateMasterParameters,
	},
	"HmIP-MIO16-PCB": {
		Channels:   channelSet(13, 14, 15, 16),
		Parameters: channelOperationModeSet,
	},
	"HmIP-MOD-RC8": {
		Channels:   channelRange(1, 9),
		Parameters: channelOperationModeSet,
	},
	"HmIP-RGBW": {
		Channels:   channelSet(0),
		Parameters: map[hmenum.Parameter]struct{}{hmenum.ParameterDeviceOperationMode: {}},
	},
	"HmIP-STH": {
		Channels:   channelSet(1),
		Parameters: climateMasterParameters,
	},
	"HmIP-WGT": {
		Channels:   channelSet(8, 14),
		Parameters: climateMasterParameters,
	},
	"HmIP-WTH": {
		Channels:   channelSet(1),
		Parameters: climateMasterParameters,
	},
	"HmIP-eTRV": {
		Channels:   channelSet(1),
		Parameters: climateMasterParameters,
	},
	"HmIPW-DRBL4": {
		Channels:   channelSet(1, 5, 9, 13),
		Parameters: channelOperationModeSet,
	},
	"HmIPW-DRI16": {
		Channels:   channelRange(1, 17),
		Parameters: channelOperationModeSet,
	},
	"HmIPW-DRI32": {
		Channels:   channelRange(1, 33),
		Parameters: channelOperationModeSet,
	},
	"HmIPW-FIO6": {
		Channels:   channelRange(1, 7),
		Parameters: channelOperationModeSet,
	},
	"HmIPW-STH": {
		Channels:   channelSet(1),
		Parameters: climateMasterParameters,
	},
}

// ignoreDevicesForDataPointEvents maps model names to the set of parameters
// for which events should not be surfaced to reduce noise.
var ignoreDevicesForDataPointEvents = map[string]map[hmenum.Parameter]struct{}{
	"HmIP-PS": copyParamSet(hmenum.ClickEvents),
}

// hiddenParameters lists parameters that are created as data points but
// should be hidden from UI by default.
var hiddenParameters = map[hmenum.Parameter]struct{}{
	hmenum.ParameterActivityState:        {},
	hmenum.ParameterChannelOperationMode: {},
	hmenum.ParameterConfigPending:        {},
	hmenum.ParameterDirection:            {},
	hmenum.ParameterError:                {},
	// GLOBAL_BUTTON_LOCK is loaded as MASTER DP per
	// RELEVANT_MASTER_PARAMSETS_BY_CHANNEL but never surfaces
	// directly — it feeds the device-level lock toggle elsewhere.
	hmenum.ParameterGlobalButtonLock:             {},
	hmenum.ParameterHeatingValveType:             {},
	hmenum.ParameterLowBatLimit:                  {},
	hmenum.ParameterMinMaxNotRelevantForManuMode: {},
	hmenum.ParameterOptimumStartStop:             {},
	hmenum.ParameterSection:                      {},
	hmenum.ParameterStickyUnreach:                {},
	hmenum.ParameterTemperatureMaximum:           {},
	hmenum.ParameterTemperatureMinimum:           {},
	hmenum.ParameterTemperatureOffset:            {},
	hmenum.ParameterUnreach:                      {},
	hmenum.ParameterUpdatePending:                {},
	hmenum.ParameterWorking:                      {},
}

// ignoredParameters lists the VALUES-paramset parameter names for which no
// data points are created. These are raw string names (not hmenum.Parameter)
// because many appear only in older/niche firmware and are not allocated
// named constants.
var ignoredParameters = map[string]struct{}{
	"ACCESS_AUTHORIZATION":              {},
	"ADAPTION_DRIVE":                    {},
	"AES_KEY":                           {},
	"ALARM_COUNT":                       {},
	"ALL_LEDS":                          {},
	"ARROW_DOWN":                        {},
	"ARROW_UP":                          {},
	"BACKLIGHT":                         {},
	"BEEP":                              {},
	"BELL":                              {},
	"BLIND":                             {},
	"BOOST_STATE":                       {},
	"BOOST_TIME":                        {},
	"BOOT":                              {},
	"BOOTED":                            {},
	"BULB":                              {},
	"CLEAR_ERROR":                       {},
	"CLEAR_WINDOW_OPEN_SYMBOL":          {},
	"CLOCK":                             {},
	"CMD_RETL":                          {}, // CUxD
	"CMD_RETS":                          {}, // CUxD
	"CONTROL_DIFFERENTIAL_TEMPERATURE":  {},
	"DATE_TIME_UNKNOWN":                 {},
	"DECISION_VALUE":                    {},
	"DEVICE_IN_BOOTLOADER":              {},
	"DOOR":                              {},
	"EXTERNAL_CLOCK":                    {},
	"FROST_PROTECTION":                  {},
	"HUMIDITY_LIMITER":                  {},
	"IDENTIFICATION_MODE_KEY_VISUAL":    {},
	"IDENTIFICATION_MODE_LCD_BACKLIGHT": {},
	"INCLUSION_UNSUPPORTED_DEVICE":      {},
	"INHIBIT":                           {},
	"INSTALL_MODE":                      {},
	// INSTALL_TEST is a maintenance-channel parameter the CCU exposes for the
	// "Anlernmodus aktivieren"-Test workflow on every device. FLAGS=INTERNAL, no
	// operator value beyond the install routine.
	"INSTALL_TEST":                 {},
	"OLD_LEVEL":                    {},
	"OVERFLOW":                     {},
	"OVERRUN":                      {},
	"PARTY_SET_POINT_TEMPERATURE":  {},
	"PARTY_TEMPERATURE":            {},
	"PARTY_TIME_END":               {},
	"PARTY_TIME_START":             {},
	"PHONE":                        {},
	"PROCESS":                      {},
	"QUICK_VETO_TIME":              {},
	"RAMP_STOP":                    {},
	"RELOCK_DELAY":                 {},
	"SCENE":                        {},
	"SELF_CALIBRATION":             {},
	"SERVICE_COUNT":                {},
	"SET_SYMBOL_FOR_HEATING_PHASE": {},
	"SHADING_SPEED":                {},
	"SHEV_POS":                     {},
	"SPEED":                        {},
	"STATE_UNCERTAIN":              {},
	"SUBMIT":                       {},
	"SWITCH_POINT_OCCURED":         {},
	"TEMPERATURE_LIMITER":          {},
	"TEMPERATURE_OUT_OF_RANGE":     {},
	"TEXT":                         {},
	"USER_COLOR":                   {},
	"USER_PROGRAM":                 {},
	"VALVE_ADAPTION":               {},
	"WEEK_PROGRAM_POINTER":         {},
	"WINDOW":                       {},
	"WIN_RELEASE":                  {},
	"WIN_RELEASE_ACT":              {},
}

// ignoredParametersEndPattern matches parameters whose names end with one of
// the wildcard suffixes.
var ignoredParametersEndPattern = regexp.MustCompile(`.*(_OVERFLOW|_OVERRUN|_REPORTING|_RESULT|_STATUS|_SUBMIT)$`)

// ignoredParametersStartPattern matches parameters whose names start with one
// of the wildcard prefixes.
var ignoredParametersStartPattern = regexp.MustCompile(`^(ADJUSTING_|ERR_TTM_|HANDLE_|IDENTIFY_|PARTY_START_|PARTY_STOP_|STATUS_FLAG_)`)

// parameterIsWildcardIgnored reports whether name matches any of the compiled
// wildcard patterns.
func parameterIsWildcardIgnored(name string) bool {
	return ignoredParametersEndPattern.MatchString(name) ||
		ignoredParametersStartPattern.MatchString(name)
}

// unIgnoreParametersByDevice lists parameters that are normally ignored but
// should be created for specific device models.
var unIgnoreParametersByDevice = map[string]map[hmenum.Parameter]struct{}{
	"HmIP-DLD": {hmenum.ParameterErrorJammed: {}},
	"HmIP-DLP": {hmenum.ParameterErrorJammed: {}},
	"HmIP-SWSD": {
		hmenum.ParameterDirtLevel:                {},
		hmenum.ParameterSmokeLevel:               {},
		hmenum.ParameterSmokeDetectorAlarmStatus: {},
	},
	"HmIP-WRCD": {
		hmenum.ParameterDisplayDataCommit: {},
		hmenum.ParameterDisplayDataID:     {},
		hmenum.ParameterDisplayDataString: {},
		hmenum.ParameterInterval:          {},
	},
	"HM-OU-LED16": {hmenum.ParameterLEDStatus: {}},
	"HM-Sec-Win": {
		hmenum.ParameterDirection:   {},
		hmenum.ParameterWorking:     {},
		hmenum.ParameterError:       {},
		hmenum.ParameterStatusValue: {},
	},
	"HM-Sec-Key": {
		hmenum.ParameterDirection: {},
		hmenum.ParameterError:     {},
	},
	"HmIP-PCBS-BAT": {
		hmenum.ParameterOperatingVoltage: {},
		hmenum.ParameterLowBat:           {},
	},
	"BC-RT-TRX-CyG":    {hmenum.ParameterWeekProgramPointer: {}},
	"BC-RT-TRX-CyN":    {hmenum.ParameterWeekProgramPointer: {}},
	"BC-TC-C-WM":       {hmenum.ParameterWeekProgramPointer: {}},
	"HM-CC-RT-DN":      {hmenum.ParameterWeekProgramPointer: {}},
	"HM-CC-VG-1":       {hmenum.ParameterWeekProgramPointer: {}},
	"HM-TC-IT-WM-W-EU": {hmenum.ParameterWeekProgramPointer: {}},
}

// ignoreParametersByDevice maps a parameter name to the set of device model
// names for which that parameter must be suppressed even if normally visible.
//
// Note: keys are plain strings (matching hmenum.Parameter wire values) so
// the map can be indexed with arbitrary parameter names at runtime without
// converting to hmenum.Parameter first.
var ignoreParametersByDevice = map[string]map[string]struct{}{
	"CURRENT_ILLUMINATION": modelNameSet(
		"HmIP-SMI", "HmIP-SMO", "HmIP-SPI", "HmIP-UDI-SMI",
	),
	"LOWBAT": modelNameSet(
		"HM-LC-Sw1-DR",
		"HM-LC-Sw1-FM",
		"HM-LC-Sw1-PCB",
		"HM-LC-Sw1-Pl",
		"HM-LC-Sw1-Pl-DN-R1",
		"HM-LC-Sw1PBU-FM",
		"HM-LC-Sw2-FM",
		"HM-LC-Sw4-DR",
		"HM-SwI-3-FM",
	),
	"LOW_BAT": modelNameSet("HmIP-BWTH", "HmIP-PCBS"),
	"OPERATING_VOLTAGE": modelNameSet(
		"ELV-SH-BS2",
		"HmIP-BDT",
		"HmIP-BROLL",
		"HmIP-BS2",
		"HmIP-BSL",
		"HmIP-BSM",
		"HmIP-BWTH",
		"HmIP-DR",
		"HmIP-FDT",
		"HmIP-FROLL",
		"HmIP-FSM",
		"HmIP-MOD-OC8",
		"HmIP-PCBS",
		"HmIP-PDT",
		"HmIP-PMFS",
		"HmIP-PS",
		"HmIP-SFD",
		"HmIP-SMO230",
		"HmIP-WGT",
		"HmIP-WRC6-230",
	),
	"VALVE_STATE": modelNameSet(
		"HmIP-FALMOT-C8", "HmIPW-FALMOT-C12", "HmIP-FALMOT-C12",
	),
}

// acceptParameterOnlyOnChannel maps a parameter name to the single channel
// number on which it should be accepted.
var acceptParameterOnlyOnChannel = map[string]int{
	"LOWBAT": 0,
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// channelPtr returns a heap-allocated int for use as a map key.
func channelPtr(n int) *int { v := n; return &v }

// channelSet builds a set from the listed channel numbers.
func channelSet(ns ...int) map[int]struct{} {
	m := make(map[int]struct{}, len(ns))
	for _, n := range ns {
		m[n] = struct{}{}
	}
	return m
}

// channelRange builds a set for channel numbers [lo, hi). lo is currently
// always 1 in callers but kept parametric for forthcoming entries that need
// non-1 starts (e.g. dimmer/blind devices that expose channel 0 differently).
//
//nolint:unparam // F11 P1-10: lo intentionally parametric.
func channelRange(lo, hi int) map[int]struct{} {
	m := make(map[int]struct{}, hi-lo)
	for i := lo; i < hi; i++ {
		m[i] = struct{}{}
	}
	return m
}

// modelNameSet builds a set from the listed model name strings.
func modelNameSet(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// copyParamSet converts a map[hmenum.Parameter]struct{} to a new copy.
func copyParamSet(src map[hmenum.Parameter]struct{}) map[hmenum.Parameter]struct{} {
	m := make(map[hmenum.Parameter]struct{}, len(src))
	maps.Copy(m, src)
	return m
}
