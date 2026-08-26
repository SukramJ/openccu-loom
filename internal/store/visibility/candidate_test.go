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

// readOnly is the operations mask of a diagnostic bit — the shape most
// candidates have in a real fleet.
var readOnly = hmproto.ParameterData{Operations: hmenum.OperationsRead}

func valuesInput(model string, channel int, parameter string) visibility.ClassifyInput {
	return visibility.ClassifyInput{
		Model:         model,
		ChannelNo:     channel,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.Parameter(parameter),
		ParameterData: readOnly,
	}
}

// TestCollectorGroupsOccurrencesByParameter pins the collapse that the
// whole picker rests on: many (model, channel) occurrences of one
// parameter become one group, not one row each.
func TestCollectorGroupsOccurrencesByParameter(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(valuesInput("HmIP-SWDO", 0, "STICKY_SABOTAGE"), "0001")
	c.Add(valuesInput("HmIP-SWDO", 1, "STICKY_SABOTAGE"), "0001")
	c.Add(valuesInput("HmIP-SWDO", 0, "STICKY_SABOTAGE"), "0002")
	c.Add(valuesInput("HmIP-SCI", 0, "STICKY_SABOTAGE"), "0003")

	groups := c.Groups()
	if len(groups) != 1 {
		t.Fatalf("Groups() = %d groups, want 1", len(groups))
	}
	g := groups[0]
	if g.Parameter != "STICKY_SABOTAGE" {
		t.Errorf("Parameter = %q", g.Parameter)
	}
	if g.Devices != 3 {
		t.Errorf("Devices = %d, want 3 distinct addresses", g.Devices)
	}
	if len(g.Models) != 2 {
		t.Fatalf("Models = %d, want 2", len(g.Models))
	}
	if g.Models[0].Model != "HmIP-SCI" || g.Models[1].Model != "HmIP-SWDO" {
		t.Errorf("models not sorted by name: %v", g.Models)
	}
	if g.Models[1].Devices != 2 {
		t.Errorf("HmIP-SWDO devices = %d, want 2", g.Models[1].Devices)
	}
	if !slices.Equal(g.Models[1].Channels, []int{0, 1}) {
		t.Errorf("HmIP-SWDO channels = %v, want [0 1]", g.Models[1].Channels)
	}
	if g.Channels != 3 {
		t.Errorf("Channels = %d, want 3 (model,channel) pairs", g.Channels)
	}
}

// TestCollectorBuildsEveryPatternForm pins the three VALUES pattern
// formats the un-ignore parser accepts. The picker offers one control
// per form, so a missing form is a scope the operator cannot select.
func TestCollectorBuildsEveryPatternForm(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(valuesInput("HmIP-eTRV-2", 1, "LOW_BAT"), "0001")

	g := c.Groups()[0]
	if g.SimplePattern != "LOW_BAT" {
		t.Errorf("SimplePattern = %q, want %q", g.SimplePattern, "LOW_BAT")
	}
	m := g.Models[0]
	if m.WildcardPattern != "LOW_BAT:VALUES@HmIP-eTRV-2:all" {
		t.Errorf("WildcardPattern = %q", m.WildcardPattern)
	}
	if m.ChannelPatterns[1] != "LOW_BAT:VALUES@HmIP-eTRV-2:1" {
		t.Errorf("ChannelPatterns[1] = %q", m.ChannelPatterns[1])
	}
}

// TestCollectorOmitsValuesOnlyFormsForMaster pins that MASTER offers
// only the channel-specific form. The parser has no MASTER short form,
// so emitting one would produce a pattern the server rejects.
func TestCollectorOmitsValuesOnlyFormsForMaster(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(visibility.ClassifyInput{
		Model:         "HmIP-BWTH",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyMaster,
		Parameter:     hmenum.ParameterTemperatureOffset,
		ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	}, "0001")

	g := c.Groups()[0]
	if g.SimplePattern != "" {
		t.Errorf("SimplePattern = %q, want empty for MASTER", g.SimplePattern)
	}
	if g.Models[0].WildcardPattern != "" {
		t.Errorf("WildcardPattern = %q, want empty for MASTER", g.Models[0].WildcardPattern)
	}
	if got := g.Models[0].ChannelPatterns[1]; got != "TEMPERATURE_OFFSET:MASTER@HmIP-BWTH:1" {
		t.Errorf("ChannelPattern = %q", got)
	}
}

// TestCollectorSeparatesParamsets pins that the same parameter name in
// VALUES and MASTER stays two groups — they carry different patterns
// and different reasons.
func TestCollectorSeparatesParamsets(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(valuesInput("HmIP-BWTH", 1, "CHANNEL_OPERATION_MODE"), "0001")
	c.Add(visibility.ClassifyInput{
		Model:         "HmIP-BWTH",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyMaster,
		Parameter:     hmenum.ParameterChannelOperationMode,
		ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	}, "0001")

	groups := c.Groups()
	if len(groups) != 2 {
		t.Fatalf("Groups() = %d, want 2 (one per paramset)", len(groups))
	}
	if groups[0].Paramset != hmenum.ParamsetKeyMaster {
		t.Errorf("groups[0].Paramset = %q, want MASTER first (sorted)", groups[0].Paramset)
	}
	if groups[1].Paramset != hmenum.ParamsetKeyValues {
		t.Errorf("groups[1].Paramset = %q, want VALUES", groups[1].Paramset)
	}
}

// TestCollectorMergesReasonsAcrossModels pins the per-group reason
// union: the same parameter can be hidden by different rules on
// different models and the group has to name all of them.
func TestCollectorMergesReasonsAcrossModels(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	// Read-only only.
	c.Add(valuesInput("HmIP-BSM", 1, "STATE"), "0001")
	// Read-only plus operation-mode gating.
	c.Add(visibility.ClassifyInput{
		Model:         "HmIP-FCI1",
		ChannelType:   "KEY_TRANSCEIVER",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyValues,
		Parameter:     hmenum.ParameterState,
		ParameterData: readOnly,
		OperationMode: "KEY_BEHAVIOR",
	}, "0002")

	g := c.Groups()[0]
	want := []visibility.HiddenReason{
		visibility.ReasonOperationMode,
		visibility.ReasonReadOnly,
	}
	if !slices.Equal(g.Reasons, want) {
		t.Errorf("Reasons = %v, want %v", g.Reasons, want)
	}
	if g.Reason != visibility.ReasonOperationMode {
		t.Errorf("Reason = %q, want the first by precedence", g.Reason)
	}
}

// TestCollectorRecordsUnknownReason pins that an unexplained candidate
// is labelled rather than dropped — the picker renders `unknown` as a
// visible drift signal.
func TestCollectorRecordsUnknownReason(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(visibility.ClassifyInput{
		Model:     "HmIP-BSM",
		ChannelNo: 4,
		Paramset:  hmenum.ParamsetKeyValues,
		Parameter: hmenum.ParameterState,
		ParameterData: hmproto.ParameterData{
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsWrite,
		},
	}, "0001")

	g := c.Groups()[0]
	if g.Reason != visibility.ReasonUnknown {
		t.Errorf("Reason = %q, want %q", g.Reason, visibility.ReasonUnknown)
	}
}

// TestPatternsMatchesTheGroupedForms is the anti-drift pin between the
// two representations: every pattern the flat list offers must be
// reachable through a group, and vice versa. They are produced from one
// accumulated state precisely so a candidate cannot exist in one shape
// only.
func TestPatternsMatchesTheGroupedForms(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(valuesInput("HmIP-SWDO", 0, "STICKY_SABOTAGE"), "0001")
	c.Add(valuesInput("HmIP-SWDO", 1, "STICKY_SABOTAGE"), "0001")
	c.Add(valuesInput("HmIP-SCI", 0, "ERR_TTM_INTERNAL"), "0002")
	c.Add(visibility.ClassifyInput{
		Model:         "HmIP-BWTH",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyMaster,
		Parameter:     hmenum.ParameterTemperatureOffset,
		ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	}, "0003")

	fromGroups := make(map[string]struct{})
	for _, g := range c.Groups() {
		if g.SimplePattern != "" {
			fromGroups[g.SimplePattern] = struct{}{}
		}
		for _, m := range g.Models {
			if m.WildcardPattern != "" {
				fromGroups[m.WildcardPattern] = struct{}{}
			}
			for _, p := range m.ChannelPatterns {
				fromGroups[p] = struct{}{}
			}
		}
	}
	flat := c.Patterns()
	if len(flat) != len(fromGroups) {
		t.Errorf("Patterns() = %d entries, groups yield %d", len(flat), len(fromGroups))
	}
	for _, p := range flat {
		if _, ok := fromGroups[p]; !ok {
			t.Errorf("pattern %q is in the flat list but reachable through no group", p)
		}
	}
	if !slices.IsSorted(flat) {
		t.Errorf("Patterns() = %v, want sorted", flat)
	}
}

// TestPatternsRoundTripThroughTheParser pins that every pattern the
// collector offers is one the save path accepts. A pattern the picker
// can tick but the server rejects surfaces as a parse error the
// operator cannot act on.
func TestPatternsRoundTripThroughTheParser(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(valuesInput("HmIP-SWDO", 0, "STICKY_SABOTAGE"), "0001")
	c.Add(valuesInput("HmIP-eTRV-2", 1, "LOW_BAT"), "0002")
	c.Add(visibility.ClassifyInput{
		Model:         "HmIP-BWTH",
		ChannelNo:     1,
		Paramset:      hmenum.ParamsetKeyMaster,
		Parameter:     hmenum.ParameterTemperatureOffset,
		ParameterData: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	}, "0003")

	for _, pattern := range c.Patterns() {
		parsed := visibility.ParseUnIgnoreLine(pattern)
		if parsed.Entry == nil || parsed.Err != "" {
			t.Errorf("ParseUnIgnoreLine(%q) = err %q, want a parsed entry", pattern, parsed.Err)
		}
	}
}

// TestCollectorIgnoresEmptyParameter pins the defensive skip so a
// malformed occurrence cannot create a nameless group.
func TestCollectorIgnoresEmptyParameter(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	c.Add(valuesInput("HmIP-SWDO", 0, ""), "0001")
	if got := c.Groups(); len(got) != 0 {
		t.Errorf("Groups() = %v, want none", got)
	}
}

// TestEmptyCollectorReturnsEmptySlices pins that the zero case gives
// the REST layer an empty array rather than a JSON null.
func TestEmptyCollectorReturnsEmptySlices(t *testing.T) {
	t.Parallel()

	c := visibility.NewCandidateCollector()
	if got := c.Groups(); got == nil || len(got) != 0 {
		t.Errorf("Groups() = %v, want an empty non-nil slice", got)
	}
	if got := c.Patterns(); got == nil || len(got) != 0 {
		t.Errorf("Patterns() = %v, want an empty non-nil slice", got)
	}
}
