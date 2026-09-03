// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
//
// Authority and its limit. Each row's channel set is the set of channels on
// which that model's own paramset description declares the listed MASTER
// parameters, read from the recorded device-descriptor corpus; it is not a
// firmware statement about which channels *should* be whitelisted — no source
// carries our data-point policy. The firmware attaches
// CHANNEL_OPERATION_MODE to a channel specification, never to a
// (model, channel) pair: HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.configparameter.GeneralConfigurationParameterFactory
// registers it once per channel-specification subtype key, each with its own
// value list. A per-model channel table therefore cannot be complete by
// construction, and this one is knowingly incomplete: further models in the
// descriptor corpus declare MASTER CHANNEL_OPERATION_MODE (HmIP-DLP, HmIP-ESI,
// HmIP-SAM, HmIP-SMO230-A, HmIP-SPDR, HmIP-STV, HmIP-SWO-*, HmIP-UDI-SMI55,
// HmIPW-DRD3) or a climate MASTER parameter (HmIP-FAL230-C10, HmIPW-FAL230-C6,
// HmIPW-SCTHD, HmIPW-WTH, HmIP-SCTH230, ELV-SH-CTH) without matching any key
// here. Adding them is a policy decision, not a firmware correction.
//
// Three rows are unverified in those words — HmIP-FCI6, HmIP-FSI6 and
// HmIPW-DRI16 appear in neither the descriptor corpus nor the shipped BidCos
// device XMLs, so their channel sets rest on the Python sibling alone.
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
	// Two flavours of the same parameter name on one device: :1/:2/:3 are
	// MULTI_MODE_INPUT_TRANSMITTER channels whose CHANNEL_OPERATION_MODE is
	// the input-mode enum [INACTIVE, KEY_BEHAVIOR, SWITCH_BEHAVIOR,
	// BINARY_BEHAVIOR], while :4/:8/:12 are DIMMER_TRANSMITTER channels
	// carrying the channel-enable enum [OFF, ON]. All six are declared
	// OPERATIONS=3 FLAGS=1 in the device's own paramset description.
	"HmIP-DRDI3": {
		Channels:   channelSet(1, 2, 3, 4, 8, 12),
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
	"HmIP-FSI6": {
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

// ignoredParameters lists the VALUES-paramset parameter names that are
// force-marked [hmenum.DataPointUsageIgnored]. The data point is still
// created — every wire parameter becomes a DP — so north-bound adapters skip
// it and the un-ignore feature can offer it back; it is not withheld from the
// model. Pinned by
// internal/central/adapter/device_pipeline_visibility_test.go.
//
// These are raw string names (not hmenum.Parameter) because many appear only
// in older/niche firmware and are not allocated named constants.
//
// Authority. The list is a product decision about the entity surface, ported
// from the Python sibling, and the firmware does not ratify it: only
// INSTALL_TEST is declared internal by the CCU itself (ui_flags="internal" on
// every declaration in ../OpenCCU-Base/src/devicetypes/rftypes/, FLAGS with
// the INTERNAL bit on every descriptor occurrence). Entries checked against
// the firmware and found to be *visible* there — INHIBIT, SPEED, BOOST_TIME,
// BOOST_STATE, ADAPTION_DRIVE, RELOCK_DELAY, SELF_CALIBRATION, USER_PROGRAM,
// WEEK_PROGRAM_POINTER — carry FLAGS=1 (VISIBLE set, INTERNAL and SERVICE
// clear) and several carry operator labels in the CCU's own parameter string
// table (../OpenCCU-Base/src/webui/www/config/stringtable_de.txt). They stay
// on the list as a deliberate noise policy, and their suppression is
// therefore **unverified** against the firmware rather than grounded in it —
// the sources say what the device declares, not what our entity surface
// should show. The remaining names have not been measured at all.
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
//
// The HANDLE_ prefix over-reaches and is carved out per device below: the only
// two HANDLE_* parameters any model in the descriptor corpus declares are
// HANDLE_LOCK and HANDLE_LED_MODE on HM-ReSC-Win-PCB-xx, both operator-facing.
// The prefix is kept for names no device has been observed to declare.
var ignoredParametersStartPattern = regexp.MustCompile(`^(ADJUSTING_|ERR_TTM_|HANDLE_|IDENTIFY_|PARTY_START_|PARTY_STOP_|STATUS_FLAG_)`)

// parameterIsWildcardIgnored reports whether name matches any of the compiled
// wildcard patterns.
func parameterIsWildcardIgnored(name string) bool {
	return ignoredParametersEndPattern.MatchString(name) ||
		ignoredParametersStartPattern.MatchString(name)
}

// wildcardPrefixOf returns the suppressed prefix name starts with, or ""
// when it starts with none.
//
// The rule that hid the parameter is only actionable if the operator can
// see which of the seven prefixes applied: "name prefix" describes the
// kind of rule, "STATUS_FLAG_" describes the rule.
func wildcardPrefixOf(name string) string {
	m := ignoredParametersStartPattern.FindStringSubmatch(name)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// wildcardSuffixOf returns the suppressed suffix name ends with, or ""
// when it ends with none. Counterpart of [wildcardPrefixOf].
func wildcardSuffixOf(name string) string {
	m := ignoredParametersEndPattern.FindStringSubmatch(name)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// unIgnoreParametersByDevice lists parameters that are normally ignored but
// should be created for specific device models.
var unIgnoreParametersByDevice = map[string]map[hmenum.Parameter]struct{}{
	// ACCESS_AUTHORIZATION is globally ignored (see ignoredParameters);
	// un-ignore it on the access-control devices so the write-only control
	// exists as a generic data point for the IPAccessPermission custom DP to
	// resolve and consume hidden on the per-user ACCESS_RECEIVER channels.
	"HmIP-DLD": {hmenum.ParameterErrorJammed: {}, hmenum.ParameterAccessAuthorization: {}},
	"HmIP-DLP": {hmenum.ParameterErrorJammed: {}},
	"HmIP-FWI": {hmenum.ParameterAccessAuthorization: {}},
	"HmIP-SWSD": {
		hmenum.ParameterDirtLevel:                {},
		hmenum.ParameterSmokeLevel:               {},
		hmenum.ParameterSmokeDetectorAlarmStatus: {},
		// A soiled smoke chamber and a failed self-test are the two
		// conditions that make a smoke detector stop protecting without
		// announcing it. Suppressed, they had no data point at all and
		// could appear in no fault list.
		hmenum.ParameterErrorSmokeChamber: {},
		hmenum.ParameterErrorAlarmTest:    {},
	},
	"HmIP-WRCD": {
		hmenum.ParameterDisplayDataCommit: {},
		hmenum.ParameterDisplayDataID:     {},
		hmenum.ParameterDisplayDataString: {},
		hmenum.ParameterInterval:          {},
	},
	"HM-OU-LED16": {hmenum.ParameterLEDStatus: {}},
	// The window-handle actor's two operator controls, caught by the blanket
	// HANDLE_ prefix rule. The device declares both in VALUES with
	// OPERATIONS=7 (read+write+event) and FLAGS=1 (VISIBLE set, INTERNAL and
	// SERVICE clear), and the CCU labels both for the operator at
	// ../OpenCCU-Base/src/webui/www/config/stringtable_de.txt:95-99
	// (ACTOR_WINDOW|HANDLE_LED_MODE=OFF/DIMMED_ON/FULL_ON,
	// ACTOR_WINDOW|HANDLE_LOCK=TRUE/FALSE). Spelled as string conversions
	// because neither name has an hmenum constant.
	"HM-ReSC-Win-PCB-xx": {
		hmenum.Parameter("HANDLE_LOCK"):     {},
		hmenum.Parameter("HANDLE_LED_MODE"): {},
	},
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
//
// This is a de-duplication policy, not a device property, and the firmware
// contradicts the property reading: LOWBAT is declared off channel 0 too —
// 19 BidCos models carry 38 such declarations, with read+event operations and
// (unlike the FLAGS=9 service copy on channel 0) a visible flag set. Two of
// them are what the CCU's own control pages render: the siren arming control
// resolves DEVICE.LOWBAT on HM-Sec-Sir-WM channel 4
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_sec_sir_wm.xml:592, read by
// ../OpenCCU-Base/src/webui/rega/www/esp/controls/alarmsirene.fn:5) and the
// digital-state control resolves BATTERIE.LOWBAT on HM-MOD-EM-8Bit channel 3
// (rf_em_8_bit.xml:323, read by .../controls/digitalstate.fn:5). Both lookups
// are channel-scoped. Keeping only the channel-0 copy is therefore our choice
// of one battery data point per device, and the CCU-parity of that choice is
// **unverified**.
//
// Reach and the latent trap. The map keys the BidCos spelling; HmIP devices
// declare LOW_BAT, a different name, so the rule has no HmIP effect at all.
// No model in the descriptor corpus declares LOWBAT *only* off channel 0, so
// no device loses its battery signal outright today — but nothing here checks
// that, and a future device that did would lose it silently. Settling that
// needs a device-wide view the per-parameter decider does not have.
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
