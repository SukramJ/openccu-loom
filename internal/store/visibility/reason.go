// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// HiddenReason names the rule that suppressed a parameter from the
// user-visible surface. It exists so the un-ignore picker can group
// several thousand candidate patterns into a handful of buckets an
// operator recognises ("diagnostic bits", "MASTER knobs") instead of
// presenting one flat alphabetical list.
//
// The reasons are derived from the same package-level rule sets the
// suppression passes in operation_mode.go consult, so a new entry in
// ignoredParameters or a changed wildcard regex is reflected here
// without a second list to maintain.
type HiddenReason string

const (
	// ReasonOperationMode — the channel's CHANNEL_OPERATION_MODE excludes
	// this parameter. The most actionable reason: reconfiguring the
	// channel surfaces the parameter without any un-ignore entry.
	ReasonOperationMode HiddenReason = "operation_mode"
	// ReasonWeekProfile — one cell of a device week profile
	// ("P1_ENDTIME_MONDAY_1", "01_WP_LEVEL"). Ranked above the plain
	// MASTER gate because the cells are the largest MASTER family by
	// far — a climate device carries up to 6 × 7 × 13 × 2 of them — and
	// because they already have a first-class editor, which is the
	// answer an operator looking at one of these actually wants.
	ReasonWeekProfile HiddenReason = "week_profile"
	// ReasonMasterGate — a MASTER parameter outside the per-model /
	// per-channel whitelist (relevantMasterParamsetsBy{Channel,Device}).
	ReasonMasterGate HiddenReason = "master_gate"
	// ReasonDeviceSpecific — suppressed for this model via
	// ignoreParametersByDevice.
	ReasonDeviceSpecific HiddenReason = "device_specific"
	// ReasonIgnoreList — listed verbatim in ignoredParameters.
	ReasonIgnoreList HiddenReason = "ignore_list"
	// ReasonWildcardPrefix — matched ignoredParametersStartPattern
	// (ADJUSTING_, ERR_TTM_, HANDLE_, IDENTIFY_, PARTY_START_,
	// PARTY_STOP_, STATUS_FLAG_).
	ReasonWildcardPrefix HiddenReason = "wildcard_prefix"
	// ReasonWildcardSuffix — matched ignoredParametersEndPattern
	// (_OVERFLOW, _OVERRUN, _REPORTING, _RESULT, _STATUS, _SUBMIT).
	ReasonWildcardSuffix HiddenReason = "wildcard_suffix"
	// ReasonHidden — in hiddenParameters: the DP is created and consumed
	// elsewhere (maintenance aggregator, custom DP) but not surfaced
	// standalone.
	ReasonHidden HiddenReason = "hidden"
	// ReasonChannelRestricted — accepted only on a different channel
	// number (IsAcceptedOnlyOnChannel).
	ReasonChannelRestricted HiddenReason = "channel_restricted"
	// ReasonEventSuppressed — the model's events for this parameter are
	// filtered (ignoreDevicesForDataPointEvents).
	ReasonEventSuppressed HiddenReason = "event_suppressed"
	// ReasonInternalFlag — the wire description carries FLAGS=INTERNAL
	// and the parameter is not allow-listed.
	ReasonInternalFlag HiddenReason = "internal_flag"
	// ReasonReadOnly — neither EVENT nor WRITE in OPERATIONS: the CCU
	// never pushes it and it cannot be set. The largest bucket in
	// practice; ERR_* and STICKY_* diagnostic bits land here.
	ReasonReadOnly HiddenReason = "read_only"
	// ReasonUnknown — no known rule matched. A candidate carrying this
	// reason means the classifier has drifted from the suppression
	// passes. Two checks fail on it: the
	// `every_candidate_has_a_known_reason` subtest of
	// TestVisibilityCandidateGroups (tests/integration), which needs
	// `-tags=integration`, and TestClassifyExplainsEveryValuesSuppression
	// in this package, which cross-multiplies a fixed model / channel /
	// parameter corpus against [ParameterDecider.computeIgnoredValues] and
	// so runs on every unit build.
	ReasonUnknown HiddenReason = "unknown"
)

// reasonPrecedence orders the reasons from most to least explanatory
// for an operator. [Classify] returns matches in this order, so the
// first element is the one worth putting on a badge.
//
// The order is deliberately NOT the pipeline's pass order: the passes
// overwrite one shared forced-usage field, so "which pass wrote last"
// is an implementation detail, while "which rule would an operator act
// on" is what the picker needs. A parameter that is both read-only and
// excluded by the channel's operation mode is reported as
// operation_mode first, because changing the mode is a fix and
// "read-only" is not.
var reasonPrecedence = []HiddenReason{
	ReasonOperationMode,
	ReasonWeekProfile,
	ReasonMasterGate,
	ReasonDeviceSpecific,
	ReasonIgnoreList,
	ReasonWildcardPrefix,
	ReasonWildcardSuffix,
	ReasonHidden,
	ReasonChannelRestricted,
	ReasonEventSuppressed,
	ReasonInternalFlag,
	ReasonReadOnly,
}

// AllHiddenReasons returns every reason [Classify] can emit, in
// precedence order, excluding [ReasonUnknown]. It is shipped over the REST
// schema so a reason the SPA does not recognise still renders instead of
// being dropped.
//
// It is not the UI's single enumeration. Measured: the SPA keeps its own chip
// order (REASON_ORDER in assets/ui/src/lib/visibility/candidates.ts, which
// puts master_gate before week_profile and hidden right after
// device_specific, unlike the precedence order below), its own noise subset
// (NOISE_REASONS in the same file), its own TypeScript union
// (assets/ui/src/lib/api/visibility-types.ts), a hand-written enum in
// assets/openapi.yaml, and one `unignore.reason.*` label per reason in both
// catalogues of assets/ui/src/lib/i18n.ts. A reason added here alone reaches
// the UI appended last and labelled with its raw key until those follow.
func AllHiddenReasons() []HiddenReason {
	out := make([]HiddenReason, len(reasonPrecedence))
	copy(out, reasonPrecedence)
	return out
}

// ClassifyInput carries everything the rule sets need to explain why a
// parameter is suppressed. It is deliberately a plain struct of wire
// facts rather than a data-point interface: the classifier must be
// callable from the candidate builder, from tests, and from a snapshot
// tool without materialising a model.
type ClassifyInput struct {
	Model       string
	ChannelType string
	// ChannelNo is the channel number, or a negative value when unknown.
	ChannelNo int
	Paramset  hmenum.ParamsetKey
	Parameter hmenum.Parameter
	// ParameterData is the wire description. The zero value is tolerated
	// (the FLAGS / OPERATIONS branches simply do not fire).
	ParameterData hmproto.ParameterData
	// OperationMode is the channel's current CHANNEL_OPERATION_MODE
	// value, empty when the channel has none or it has not been read.
	OperationMode string
}

// Classify returns every rule that suppresses the given parameter, in
// [reasonPrecedence] order. The slice is empty when no rule matches —
// callers that need a badge should use [ClassifyPrimary], which folds
// the empty case into [ReasonUnknown].
//
// Classify explains a suppression; it does not decide one. Whether a
// parameter is actually hidden is [ParameterDecider.IsParameterIgnored]
// plus the mark passes in operation_mode.go. Callers pass parameters
// already known to be suppressed (their data point carries
// forcedUsage=Ignored) and ask why.
//
// Operator overrides (un_ignore entries, the required-parameter
// whitelist) are intentionally not consulted: they govern whether a
// parameter is hidden at all, and a parameter they re-enabled is no
// longer a candidate to explain.
func Classify(in ClassifyInput) []HiddenReason {
	matched := make(map[HiddenReason]struct{}, 4)
	name := string(in.Parameter)

	if modes, gated := channelOperationModeVisibility[in.Parameter]; gated && in.OperationMode != "" {
		if _, isConfigurable := configurableChannelTypes[in.ChannelType]; isConfigurable {
			if _, allowed := modes[in.OperationMode]; !allowed {
				matched[ReasonOperationMode] = struct{}{}
			}
		}
	}

	if in.Paramset == hmenum.ParamsetKeyMaster {
		if checkMasterParameterIgnored(in.ChannelNo, in.Parameter, in.Model) {
			matched[ReasonMasterGate] = struct{}{}
		}
		if weekprofile.IsParameterName(name) {
			matched[ReasonWeekProfile] = struct{}{}
		}
	}

	// The VALUES-only rule sets below mirror computeIgnoredValues. They
	// are skipped for MASTER for the same reason the decider skips them:
	// a MASTER parameter is gated by the whitelist alone.
	if in.Paramset == hmenum.ParamsetKeyValues {
		if models, ok := ignoreParametersByDevice[name]; ok && modelMatchesByPrefix(in.Model, models) {
			matched[ReasonDeviceSpecific] = struct{}{}
		}
		if _, ok := ignoredParameters[name]; ok {
			matched[ReasonIgnoreList] = struct{}{}
		}
		if ignoredParametersStartPattern.MatchString(name) {
			matched[ReasonWildcardPrefix] = struct{}{}
		}
		if ignoredParametersEndPattern.MatchString(name) {
			matched[ReasonWildcardSuffix] = struct{}{}
		}
		if in.ChannelNo >= 0 && IsAcceptedOnlyOnChannel(name, in.ChannelNo) {
			matched[ReasonChannelRestricted] = struct{}{}
		}
		if IsParameterIgnoredForDataPointEvent(in.Model, in.Parameter) {
			matched[ReasonEventSuppressed] = struct{}{}
		}
	}

	// hiddenParameters is consulted for both paramsets: markIfHidden runs
	// over the VALUES and the MASTER data points alike.
	if _, hidden := hiddenParameters[in.Parameter]; hidden {
		matched[ReasonHidden] = struct{}{}
	}

	// The two wire-description rules likewise apply to both paramsets,
	// matching markIfInternal and markIfNoEventNoWrite.
	if in.ParameterData.Flags.IsInternal() {
		if _, allowed := generic.AllowedInternalParameters[name]; !allowed {
			matched[ReasonInternalFlag] = struct{}{}
		}
	}
	if !in.ParameterData.Operations.IsEvent() && !in.ParameterData.Operations.IsWritable() {
		matched[ReasonReadOnly] = struct{}{}
	}

	out := make([]HiddenReason, 0, len(matched))
	for _, r := range reasonPrecedence {
		if _, ok := matched[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

// ClassifyPrimary returns the single most explanatory reason, or
// [ReasonUnknown] when no rule matched.
func ClassifyPrimary(in ClassifyInput) HiddenReason {
	reasons := Classify(in)
	if len(reasons) == 0 {
		return ReasonUnknown
	}
	return reasons[0]
}

// ReasonDetail returns the concrete rule text behind a reason, or "" for
// reasons that have none.
//
// The wildcard reasons are the ones that need it. "Name prefix" tells an
// operator which *kind* of rule fired but not which of the seven
// prefixes matched, so the badge names a category the operator then has
// to go and look up. "STATUS_FLAG_" is the rule itself, and the caller
// already holds everything needed to say so.
//
// Reasons whose rule is a membership list rather than a pattern
// (ignore_list, hidden, device_specific) return "": the parameter name
// on the row already is the entry, so repeating it adds nothing.
func ReasonDetail(reason HiddenReason, parameter string) string {
	switch reason {
	case ReasonWildcardPrefix:
		return wildcardPrefixOf(parameter)
	case ReasonWildcardSuffix:
		return wildcardSuffixOf(parameter)
	default:
		return ""
	}
}

// MergeReasons folds per-occurrence reason lists into one deduplicated
// list in precedence order. The candidate builder uses it because the
// same parameter can be suppressed for different reasons on different
// models — LEVEL is operation-mode-gated on a HmIP-FCI6 and plain
// read-only elsewhere — and the picker shows one row for it.
func MergeReasons(lists ...[]HiddenReason) []HiddenReason {
	seen := make(map[HiddenReason]struct{}, len(reasonPrecedence))
	for _, list := range lists {
		for _, r := range list {
			seen[r] = struct{}{}
		}
	}
	out := make([]HiddenReason, 0, len(seen))
	for _, r := range reasonPrecedence {
		if _, ok := seen[r]; ok {
			out = append(out, r)
		}
	}
	// ReasonUnknown has no precedence slot; append it so a drifted
	// classification stays visible instead of being silently dropped.
	if _, ok := seen[ReasonUnknown]; ok {
		out = append(out, ReasonUnknown)
	}
	return out
}

// SortReasons orders an arbitrary reason slice by [reasonPrecedence].
// Unknown reasons sort last, alphabetically among themselves, so the
// output is deterministic even for values this package does not define.
func SortReasons(reasons []HiddenReason) {
	rank := make(map[HiddenReason]int, len(reasonPrecedence))
	for i, r := range reasonPrecedence {
		rank[r] = i
	}
	sort.SliceStable(reasons, func(i, j int) bool {
		ri, iOK := rank[reasons[i]]
		rj, jOK := rank[reasons[j]]
		switch {
		case iOK && jOK:
			return ri < rj
		case iOK:
			return true
		case jOK:
			return false
		default:
			return reasons[i] < reasons[j]
		}
	})
}
