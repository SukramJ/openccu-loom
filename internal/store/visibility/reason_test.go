// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility_test

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// eventAndWrite is the operations bitmask of an ordinary operable
// parameter. Tests that target a rule other than read-only set it so
// the read-only branch does not fire and mask the reason under test.
const eventAndWrite = hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsWrite

// TestClassifyNamesTheRuleThatHidThePparameter pins one input per rule
// set to its reason. Each case is built from a real entry of the table
// it exercises, so a table edit that removes the entry breaks the case
// rather than silently reclassifying operators' parameters.
func TestClassifyNamesTheRuleThatHidTheParameter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   visibility.ClassifyInput
		want visibility.HiddenReason
		// alsoWant are further reasons the same input must report; the
		// primary is `want`.
		alsoWant []visibility.HiddenReason
	}{
		{
			name: "static ignore list",
			in: visibility.ClassifyInput{
				Model:         "HmIP-PSM",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.Parameter("BOOT"),
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonIgnoreList,
		},
		{
			name: "wildcard suffix _STATUS",
			in: visibility.ClassifyInput{
				Model:         "HmIP-SWSD",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.Parameter("SMOKE_DETECTOR_ALARM_STATUS"),
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonWildcardSuffix,
		},
		{
			name: "wildcard prefix ERR_TTM_",
			in: visibility.ClassifyInput{
				Model:         "HM-Sec-Win",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.Parameter("ERR_TTM_INTERNAL"),
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonWildcardPrefix,
		},
		{
			name: "device-specific suppression",
			in: visibility.ClassifyInput{
				Model:         "HmIP-SMI",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.Parameter("CURRENT_ILLUMINATION"),
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonDeviceSpecific,
		},
		{
			name: "hidden parameter",
			in: visibility.ClassifyInput{
				Model:         "HmIP-BROLL",
				ChannelNo:     4,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.ParameterActivityState,
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonHidden,
		},
		{
			name: "channel-restricted LOWBAT on the wrong channel",
			in: visibility.ClassifyInput{
				Model:         "HM-LC-Bl1-FM",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.Parameter("LOWBAT"),
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonChannelRestricted,
		},
		{
			name: "event-suppressed click parameter",
			in: visibility.ClassifyInput{
				Model:         "HmIP-PSM",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.ParameterPressShort,
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
			},
			want: visibility.ReasonEventSuppressed,
		},
		{
			name: "operation-mode gated STATE on a key transceiver",
			in: visibility.ClassifyInput{
				Model:         "HmIP-FCI1",
				ChannelType:   "KEY_TRANSCEIVER",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.ParameterState,
				ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
				OperationMode: "KEY_BEHAVIOR",
			},
			want: visibility.ReasonOperationMode,
		},
		{
			name: "MASTER outside the whitelist",
			in: visibility.ClassifyInput{
				Model:         "HmIP-STE2-PCB",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyMaster,
				Parameter:     hmenum.Parameter("CYCLIC_INFO_MSG"),
				ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			},
			want: visibility.ReasonMasterGate,
		},
		{
			name: "climate week-profile cell",
			in: visibility.ClassifyInput{
				Model:         "HmIP-eTRV-2",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyMaster,
				Parameter:     hmenum.Parameter("P1_ENDTIME_MONDAY_1"),
				ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			},
			want:     visibility.ReasonWeekProfile,
			alsoWant: []visibility.HiddenReason{visibility.ReasonMasterGate},
		},
		{
			name: "simple week-profile cell",
			in: visibility.ClassifyInput{
				Model:         "HmIP-BSM",
				ChannelNo:     4,
				Paramset:      hmenum.ParamsetKeyMaster,
				Parameter:     hmenum.Parameter("01_WP_WEEKDAY"),
				ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			},
			want:     visibility.ReasonWeekProfile,
			alsoWant: []visibility.HiddenReason{visibility.ReasonMasterGate},
		},
		{
			name: "INTERNAL flag",
			in: visibility.ClassifyInput{
				Model:     "HmIP-PSM",
				ChannelNo: 0,
				Paramset:  hmenum.ParamsetKeyValues,
				Parameter: hmenum.ParameterRSSIDevice,
				ParameterData: hmproto.ParameterData{
					Operations: eventAndWrite,
					Flags:      hmenum.FlagInternal,
				},
			},
			want: visibility.ReasonInternalFlag,
		},
		{
			name: "read-only diagnostic bit",
			in: visibility.ClassifyInput{
				Model:         "HmIP-SWDO",
				ChannelNo:     0,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.Parameter("STICKY_SABOTAGE"),
				ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
			},
			want: visibility.ReasonReadOnly,
		},
		{
			name: "operation mode outranks read-only on the same parameter",
			in: visibility.ClassifyInput{
				Model:         "HmIP-FCI1",
				ChannelType:   "KEY_TRANSCEIVER",
				ChannelNo:     1,
				Paramset:      hmenum.ParamsetKeyValues,
				Parameter:     hmenum.ParameterState,
				ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
				OperationMode: "KEY_BEHAVIOR",
			},
			want:     visibility.ReasonOperationMode,
			alsoWant: []visibility.HiddenReason{visibility.ReasonReadOnly},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := visibility.ClassifyPrimary(tc.in)
			if got != tc.want {
				t.Errorf("ClassifyPrimary = %q, want %q (all: %v)",
					got, tc.want, visibility.Classify(tc.in))
			}
			all := visibility.Classify(tc.in)
			for _, want := range tc.alsoWant {
				if !slices.Contains(all, want) {
					t.Errorf("Classify = %v, want it to contain %q", all, want)
				}
			}
		})
	}
}

// TestClassifyReportsUnknownForAnOrdinaryParameter guards the negative
// case: a writable, event-capable parameter that no rule set names must
// not be dressed up with a reason. Without this the classifier could
// return a plausible-looking category for everything and the picker's
// badges would be decoration.
func TestClassifyReportsUnknownForAnOrdinaryParameter(t *testing.T) {
	t.Parallel()

	in := visibility.ClassifyInput{
		Model:         "HmIP-BSM",
		ChannelNo:     4,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.ParameterState,
		ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
	}
	if got := visibility.Classify(in); len(got) != 0 {
		t.Errorf("Classify = %v, want no reason for an ordinary parameter", got)
	}
	if got := visibility.ClassifyPrimary(in); got != visibility.ReasonUnknown {
		t.Errorf("ClassifyPrimary = %q, want %q", got, visibility.ReasonUnknown)
	}
}

// TestClassifyRecognisesEveryWeekProfileKeyShape pins the two wire
// grammars a week profile uses. Getting one of them wrong leaves that
// half of the cells scattered through the picker — on a fleet with a
// few thermostats that is hundreds of rows, which is exactly the state
// the category exists to prevent.
func TestClassifyRecognisesEveryWeekProfileKeyShape(t *testing.T) {
	t.Parallel()

	weekProfileKeys := []string{
		"P1_ENDTIME_MONDAY_1",
		"P1_TEMPERATURE_MONDAY_1",
		"P6_TEMPERATURE_SUNDAY_13",
		"01_WP_WEEKDAY",
		"01_WP_FIXED_HOUR",
		"01_WP_FIXED_MINUTE",
		"01_WP_LEVEL",
		"24_WP_LEVEL",
		// The CCU declares 75 groups on a switch/dimmer/blind channel
		// and 69 on the models its web UI special-cases, and edits every
		// one of them (`_getMaxEntries` in the CCU's
		// WebUI/www/config/easymodes/js/HmIPWeeklyProgram.js). A cell is
		// a week-profile cell at any of those numbers; the lower cap
		// this project applies when parsing is its own storage limit and
		// must not leak into the classification.
		"25_WP_LEVEL",
		"69_WP_LEVEL",
		"75_WP_LEVEL",
	}
	for _, key := range weekProfileKeys {
		got := visibility.ClassifyPrimary(visibility.ClassifyInput{
			Model:         "HmIP-eTRV-2",
			ChannelNo:     1,
			Paramset:      hmenum.ParamsetKeyMaster,
			Parameter:     hmenum.Parameter(key),
			ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		})
		if got != visibility.ReasonWeekProfile {
			t.Errorf("ClassifyPrimary(%q) = %q, want %q", key, got, visibility.ReasonWeekProfile)
		}
	}

	// Near-misses must not be swept into the category: an ordinary
	// MASTER knob that merely starts with a digit or a P is a real
	// setting the operator may want.
	notWeekProfile := []string{
		"P7_ENDTIME_MONDAY_1", // no seventh profile
		"P1_LEVEL_MONDAY_1",   // not a profile field
		"00_WP_LEVEL",         // group numbers start at 1
		"AA_WP_LEVEL",         // not a group number
		"01_XP_LEVEL",         // not the WP marker
		"TEMPERATURE_OFFSET",  // a plain MASTER knob
		"WEEK_PROGRAM_CHANNEL_LOCKS",
	}
	for _, key := range notWeekProfile {
		got := visibility.Classify(visibility.ClassifyInput{
			Model:         "HmIP-eTRV-2",
			ChannelNo:     1,
			Paramset:      hmenum.ParamsetKeyMaster,
			Parameter:     hmenum.Parameter(key),
			ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		})
		if slices.Contains(got, visibility.ReasonWeekProfile) {
			t.Errorf("Classify(%q) = %v, want no %q", key, got, visibility.ReasonWeekProfile)
		}
	}
}

// TestClassifyDoesNotTreatValuesParametersAsWeekProfile pins the
// paramset guard: week profiles live in MASTER, so a VALUES parameter
// that happens to share the shape is not one.
func TestClassifyDoesNotTreatValuesParametersAsWeekProfile(t *testing.T) {
	t.Parallel()

	got := visibility.Classify(visibility.ClassifyInput{
		Model:         "HmIP-eTRV-2",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.Parameter("01_WP_LEVEL"),
		ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
	})
	if slices.Contains(got, visibility.ReasonWeekProfile) {
		t.Errorf("Classify = %v, want no %q for a VALUES parameter",
			got, visibility.ReasonWeekProfile)
	}
}

// TestClassifySkipsValuesRulesForMaster pins the paramset split: the
// VALUES-only rule sets must not fire on a MASTER parameter, mirroring
// computeIgnoredMaster, which gates MASTER on the whitelist alone.
func TestClassifySkipsValuesRulesForMaster(t *testing.T) {
	t.Parallel()

	// BOOT is on the static VALUES ignore list. Asked about as a MASTER
	// parameter it must not come back as ignore_list.
	got := visibility.Classify(visibility.ClassifyInput{
		Model:         "HmIP-BWTH",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyMaster,
		Parameter:     hmenum.Parameter("BOOT"),
		ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	})
	if slices.Contains(got, visibility.ReasonIgnoreList) {
		t.Errorf("Classify = %v, want no %q for a MASTER parameter",
			got, visibility.ReasonIgnoreList)
	}
}

// TestClassifyIgnoresOperationModeWhenUnread pins that an unread
// CHANNEL_OPERATION_MODE produces no operation_mode reason. The gating
// pass leaves usage untouched in that state, so claiming the mode hid
// the parameter would be wrong.
func TestClassifyIgnoresOperationModeWhenUnread(t *testing.T) {
	t.Parallel()

	got := visibility.Classify(visibility.ClassifyInput{
		Model:         "HmIP-FCI1",
		ChannelType:   "KEY_TRANSCEIVER",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.ParameterState,
		ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
		OperationMode: "",
	})
	if slices.Contains(got, visibility.ReasonOperationMode) {
		t.Errorf("Classify = %v, want no %q when the mode has not been read",
			got, visibility.ReasonOperationMode)
	}
}

// TestClassifyIgnoresOperationModeOnANonConfigurableChannel pins the
// second pre-condition of the gate: the channel type must be one the
// gate applies to.
func TestClassifyIgnoresOperationModeOnANonConfigurableChannel(t *testing.T) {
	t.Parallel()

	got := visibility.Classify(visibility.ClassifyInput{
		Model:         "HmIP-BSM",
		ChannelType:   "SWITCH_VIRTUAL_RECEIVER",
		ChannelNo:     4,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.ParameterState,
		ParameterData: hmproto.ParameterData{Operations: eventAndWrite},
		OperationMode: "KEY_BEHAVIOR",
	})
	if slices.Contains(got, visibility.ReasonOperationMode) {
		t.Errorf("Classify = %v, want no %q on a non-configurable channel type",
			got, visibility.ReasonOperationMode)
	}
}

// TestClassifyAllowsWhitelistedInternalParameters pins the
// AllowedInternalParameters exemption that markIfInternal honours.
func TestClassifyAllowsWhitelistedInternalParameters(t *testing.T) {
	t.Parallel()

	got := visibility.Classify(visibility.ClassifyInput{
		Model:     "HmIP-BROLL",
		ChannelNo: 4,
		Paramset:  hmenum.ParamsetKeyValues,
		Parameter: hmenum.Parameter("REPETITIONS"),
		ParameterData: hmproto.ParameterData{
			Operations: eventAndWrite,
			Flags:      hmenum.FlagInternal,
		},
	})
	if slices.Contains(got, visibility.ReasonInternalFlag) {
		t.Errorf("Classify = %v, want no %q for an allow-listed INTERNAL parameter",
			got, visibility.ReasonInternalFlag)
	}
}

// TestClassifyOrdersReasonsByPrecedence pins that the returned slice is
// ordered, not merely a set: the UI takes element 0 as the badge.
func TestClassifyOrdersReasonsByPrecedence(t *testing.T) {
	t.Parallel()

	// LOWBAT on HM-LC-Sw1-Pl channel 1: device-specific suppression,
	// channel restriction (accepted only on 0), and read-only.
	got := visibility.Classify(visibility.ClassifyInput{
		Model:         "HM-LC-Sw1-Pl",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.Parameter("LOWBAT"),
		ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	})
	want := []visibility.HiddenReason{
		visibility.ReasonDeviceSpecific,
		visibility.ReasonChannelRestricted,
		visibility.ReasonReadOnly,
	}
	if !slices.Equal(got, want) {
		t.Errorf("Classify = %v, want %v", got, want)
	}
}

// TestMergeReasonsDeduplicatesAndOrders pins the fold used when the
// same parameter is hidden for different reasons on different models.
func TestMergeReasonsDeduplicatesAndOrders(t *testing.T) {
	t.Parallel()

	got := visibility.MergeReasons(
		[]visibility.HiddenReason{visibility.ReasonReadOnly, visibility.ReasonHidden},
		[]visibility.HiddenReason{visibility.ReasonHidden, visibility.ReasonOperationMode},
	)
	want := []visibility.HiddenReason{
		visibility.ReasonOperationMode,
		visibility.ReasonHidden,
		visibility.ReasonReadOnly,
	}
	if !slices.Equal(got, want) {
		t.Errorf("MergeReasons = %v, want %v", got, want)
	}
}

// TestMergeReasonsKeepsUnknown pins that a drifted classification stays
// visible instead of being dropped by the precedence fold.
func TestMergeReasonsKeepsUnknown(t *testing.T) {
	t.Parallel()

	got := visibility.MergeReasons(
		[]visibility.HiddenReason{visibility.ReasonUnknown, visibility.ReasonReadOnly},
	)
	want := []visibility.HiddenReason{visibility.ReasonReadOnly, visibility.ReasonUnknown}
	if !slices.Equal(got, want) {
		t.Errorf("MergeReasons = %v, want %v", got, want)
	}
}

// TestAllHiddenReasonsExcludesUnknown pins the vocabulary the REST
// surface publishes: unknown is a drift signal, not a filter chip.
func TestAllHiddenReasonsExcludesUnknown(t *testing.T) {
	t.Parallel()

	all := visibility.AllHiddenReasons()
	if slices.Contains(all, visibility.ReasonUnknown) {
		t.Errorf("AllHiddenReasons = %v, want it to exclude %q", all, visibility.ReasonUnknown)
	}
	if len(all) == 0 {
		t.Fatal("AllHiddenReasons is empty")
	}
	// The caller must not be able to mutate the package's order.
	all[0] = visibility.ReasonUnknown
	if visibility.AllHiddenReasons()[0] == visibility.ReasonUnknown {
		t.Error("AllHiddenReasons returns a shared slice; want a copy")
	}
}

// TestReasonDetailNamesTheMatchedPattern pins the badge contract: a
// wildcard reason carries the pattern that actually matched, so the
// operator reads the rule rather than its category.
func TestReasonDetailNamesTheMatchedPattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		parameter string
		reason    visibility.HiddenReason
		want      string
	}{
		{"STATUS_FLAG_LOW_BAT", visibility.ReasonWildcardPrefix, "STATUS_FLAG_"},
		{"PARTY_START_TIME", visibility.ReasonWildcardPrefix, "PARTY_START_"},
		{"ERR_TTM_SOMETHING", visibility.ReasonWildcardPrefix, "ERR_TTM_"},
		{"HUMIDITY_STATUS", visibility.ReasonWildcardSuffix, "_STATUS"},
		{"ENERGY_COUNTER_OVERFLOW", visibility.ReasonWildcardSuffix, "_OVERFLOW"},
		{"CONFIG_SUBMIT", visibility.ReasonWildcardSuffix, "_SUBMIT"},
		// Reasons whose rule is a membership list carry no detail: the
		// parameter name on the row already is the list entry.
		{"BOOTED", visibility.ReasonIgnoreList, ""},
		{"UPDATE_PENDING", visibility.ReasonHidden, ""},
		{"01_WP_LEVEL", visibility.ReasonWeekProfile, ""},
		// A parameter that does not match the reason's pattern yields no
		// detail rather than a wrong one.
		{"LOW_BAT", visibility.ReasonWildcardPrefix, ""},
	}
	for _, tc := range cases {
		if got := visibility.ReasonDetail(tc.reason, tc.parameter); got != tc.want {
			t.Errorf("ReasonDetail(%q, %q) = %q, want %q", tc.reason, tc.parameter, got, tc.want)
		}
	}
}
