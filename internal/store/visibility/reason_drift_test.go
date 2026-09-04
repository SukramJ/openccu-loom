// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestClassifyExplainsEveryValuesSuppression cross-multiplies a fixed model /
// channel / parameter corpus and asserts the one property that ties the two
// rule walks together: whenever [ParameterDecider.computeIgnoredValues]
// suppresses a VALUES parameter, [Classify] returns at least one reason for
// it.
//
// The two walks are deliberately not merged — Classify omits the decider's
// step-0 un-ignore early exit and its Rules.Evaluate branch, because a
// parameter an operator re-enabled is no longer a candidate to explain — so
// the only thing that keeps them from drifting apart is a check like this
// one. Before it existed the check lived in TestVisibilityCandidateGroups,
// which runs only under `-tags=integration`, so a unit run stayed green while
// the drift was present.
//
// The implication is one-directional on purpose: Classify may match a rule
// the decider does not act on (a required-whitelist parameter, for instance),
// and that is not drift.
func TestClassifyExplainsEveryValuesSuppression(t *testing.T) {
	t.Parallel()

	models := []string{"HmIP-PCBS", "HmIP-PCBS2", "HmIP-BSM", "HM-Sec-Key", "HmIP-PS", "HM-CC-RT-DN"}
	channelNos := []int{channelNoUnknown, 0, 1, 4}

	// Names built from the literal alternatives the two wildcard regexes
	// carry. Each is asserted to actually match, so a renamed alternative
	// cannot leave a name in the corpus that no longer exercises the branch.
	wildcardNames := []string{
		"ADJUSTING_X", "ERR_TTM_X", "HANDLE_X", "IDENTIFY_X",
		"PARTY_START_X", "PARTY_STOP_X", "STATUS_FLAG_X",
		"X_OVERFLOW", "X_OVERRUN", "X_REPORTING",
		"X_RESULT", "X_STATUS", "X_SUBMIT",
	}
	for _, name := range wildcardNames {
		if !parameterIsWildcardIgnored(name) {
			t.Fatalf("corpus name %q no longer matches either wildcard pattern — the pattern's alternatives moved", name)
		}
	}

	seen := make(map[hmenum.Parameter]struct{})
	add := func(name string) {
		seen[hmenum.Parameter(name)] = struct{}{}
	}
	for name := range ignoredParameters {
		add(name)
	}
	for name := range ignoreParametersByDevice {
		add(name)
	}
	for p := range hiddenParameters {
		seen[p] = struct{}{}
	}
	for name := range acceptParameterOnlyOnChannel {
		add(name)
	}
	for _, params := range ignoreDevicesForDataPointEvents {
		for p := range params {
			seen[p] = struct{}{}
		}
	}
	for _, name := range wildcardNames {
		add(name)
	}

	// A zero-value ParameterData carries OPERATIONS == 0, and Classify then
	// answers read_only for every input — which would make the assertion
	// below true whatever the rule tables say. Measured: with the zero value
	// the check stays green even after the ignoreParametersByDevice branch is
	// deleted from Classify. Giving every probe EVENT|WRITE keeps the
	// read_only and internal_flag branches silent so only the rule-table
	// branches can answer.
	wireDescription := hmproto.ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
	}

	d := NewParameterDecider(nil)
	for _, model := range models {
		for _, channelNo := range channelNos {
			for p := range seen {
				if !d.computeIgnoredValues("", model, channelNo, hmenum.ParamsetKeyValues, p) {
					continue
				}
				reasons := Classify(ClassifyInput{
					Model:         model,
					ChannelNo:     channelNo,
					Paramset:      hmenum.ParamsetKeyValues,
					Parameter:     p,
					ParameterData: wireDescription,
				})
				if len(reasons) == 0 {
					t.Errorf("drift: decider suppresses %s on model %q channel %d, Classify returns no reason",
						p, model, channelNo)
				}
			}
		}
	}
}
