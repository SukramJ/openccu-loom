// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

import (
	"fmt"
	"sort"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CandidateModelScope is one device model on which a hidden parameter
// occurs, together with the channel numbers it occurs on.
type CandidateModelScope struct {
	// Model is the device model name, e.g. "HmIP-eTRV-2".
	Model string
	// Channels are the channel numbers carrying the parameter, ascending.
	Channels []int
	// Devices counts the distinct devices of this model that carry it.
	Devices int
	// WildcardPattern re-enables the parameter on every channel of the
	// model, e.g. "LOW_BAT:VALUES@HmIP-eTRV-2:all". Empty for MASTER,
	// which has no wildcard form.
	WildcardPattern string
	// ChannelPatterns maps each channel number to its full pattern, e.g.
	// 0 → "LOW_BAT:VALUES@HmIP-eTRV-2:0".
	ChannelPatterns map[int]string
}

// CandidateGroup collects every occurrence of one hidden parameter in
// one paramset across the fleet. It replaces the flat candidate list as
// the picker's primary shape: a 399-device fleet yields ~2800 flat
// pattern strings but only ~45 groups, because the flat list is the
// cross-product of parameter × model × channel in three redundant
// formats.
type CandidateGroup struct {
	// Parameter is the bare parameter name, e.g. "LOW_BAT".
	Parameter string
	// Paramset is VALUES or MASTER.
	Paramset hmenum.ParamsetKey
	// Reason is the most explanatory rule that hid the parameter; it
	// drives the badge and the filter chip.
	Reason HiddenReason
	// ReasonDetail is the concrete rule text behind Reason — the matched
	// prefix or suffix — or empty when the reason has no such text. It
	// turns the badge from a rule category into the rule itself.
	ReasonDetail string
	// Reasons lists every rule that matched anywhere in the fleet, in
	// precedence order.
	Reasons []HiddenReason
	// SimplePattern re-enables the parameter on every device and channel,
	// e.g. "LOW_BAT". Empty for MASTER, which has no short form.
	SimplePattern string
	// Models are the affected device models, sorted by name.
	Models []CandidateModelScope
	// Devices counts distinct devices across all models.
	Devices int
	// Channels counts distinct (model, channel) pairs.
	Channels int
}

// CandidateCollector accumulates hidden-parameter occurrences and emits
// both the grouped shape and the legacy flat pattern list. Both come
// from the same accumulated state so the two representations cannot
// drift — a candidate reachable in one is reachable in the other.
//
// Not safe for concurrent use; build one per query.
type CandidateCollector struct {
	groups map[groupKey]*groupAccumulator
}

type groupKey struct {
	parameter string
	paramset  hmenum.ParamsetKey
}

type groupAccumulator struct {
	reasons map[HiddenReason]struct{}
	models  map[string]*modelAccumulator
	devices map[string]struct{}
}

type modelAccumulator struct {
	channels map[int]struct{}
	devices  map[string]struct{}
}

// NewCandidateCollector returns an empty collector.
func NewCandidateCollector() *CandidateCollector {
	return &CandidateCollector{groups: make(map[groupKey]*groupAccumulator)}
}

// Add records one occurrence of a hidden parameter. deviceAddress
// identifies the physical device so the collector can report how many
// devices — not merely how many models — an un-ignore entry would
// affect. Occurrences with a negative ChannelNo contribute to the
// parameter's simple and wildcard forms but to no channel-specific one,
// matching the pattern formats the un-ignore parser accepts.
func (c *CandidateCollector) Add(in ClassifyInput, deviceAddress string) {
	if in.Parameter == "" {
		return
	}
	gk := groupKey{parameter: string(in.Parameter), paramset: in.Paramset}
	g, ok := c.groups[gk]
	if !ok {
		g = &groupAccumulator{
			reasons: make(map[HiddenReason]struct{}, 2),
			models:  make(map[string]*modelAccumulator, 8),
			devices: make(map[string]struct{}, 8),
		}
		c.groups[gk] = g
	}
	reasons := Classify(in)
	if len(reasons) == 0 {
		g.reasons[ReasonUnknown] = struct{}{}
	}
	for _, r := range reasons {
		g.reasons[r] = struct{}{}
	}
	if deviceAddress != "" {
		g.devices[deviceAddress] = struct{}{}
	}
	if in.Model == "" {
		return
	}
	m, ok := g.models[in.Model]
	if !ok {
		m = &modelAccumulator{
			channels: make(map[int]struct{}, 4),
			devices:  make(map[string]struct{}, 4),
		}
		g.models[in.Model] = m
	}
	if in.ChannelNo >= 0 {
		m.channels[in.ChannelNo] = struct{}{}
	}
	if deviceAddress != "" {
		m.devices[deviceAddress] = struct{}{}
	}
}

// Groups returns the accumulated groups sorted by paramset then
// parameter name, with models sorted by name and channels ascending.
func (c *CandidateCollector) Groups() []CandidateGroup {
	out := make([]CandidateGroup, 0, len(c.groups))
	for gk, g := range c.groups {
		group := CandidateGroup{
			Parameter: gk.parameter,
			Paramset:  gk.paramset,
			Reasons:   mergeReasonSet(g.reasons),
			Devices:   len(g.devices),
		}
		group.Reason = ReasonUnknown
		if len(group.Reasons) > 0 {
			group.Reason = group.Reasons[0]
		}
		group.ReasonDetail = ReasonDetail(group.Reason, gk.parameter)
		if gk.paramset == hmenum.ParamsetKeyValues {
			group.SimplePattern = gk.parameter
		}
		group.Models = make([]CandidateModelScope, 0, len(g.models))
		for model, m := range g.models {
			scope := CandidateModelScope{
				Model:           model,
				Devices:         len(m.devices),
				Channels:        make([]int, 0, len(m.channels)),
				ChannelPatterns: make(map[int]string, len(m.channels)),
			}
			for ch := range m.channels {
				scope.Channels = append(scope.Channels, ch)
				scope.ChannelPatterns[ch] = ChannelPattern(gk.parameter, gk.paramset, model, ch)
			}
			sort.Ints(scope.Channels)
			if gk.paramset == hmenum.ParamsetKeyValues {
				scope.WildcardPattern = WildcardPattern(gk.parameter, gk.paramset, model)
			}
			group.Channels += len(scope.Channels)
			group.Models = append(group.Models, scope)
		}
		sort.Slice(group.Models, func(i, j int) bool {
			return group.Models[i].Model < group.Models[j].Model
		})
		out = append(out, group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Paramset != out[j].Paramset {
			return out[i].Paramset < out[j].Paramset
		}
		return out[i].Parameter < out[j].Parameter
	})
	return out
}

// Patterns returns the flat candidate list in the legacy format: for
// VALUES the simple name, the per-model wildcard and the per-channel
// form; for MASTER the per-channel form only. Sorted, deduplicated.
func (c *CandidateCollector) Patterns() []string {
	seen := make(map[string]struct{})
	groups := c.Groups()
	for i := range groups {
		g := &groups[i]
		if g.SimplePattern != "" {
			seen[g.SimplePattern] = struct{}{}
		}
		for _, m := range g.Models {
			if m.WildcardPattern != "" {
				seen[m.WildcardPattern] = struct{}{}
			}
			for _, p := range m.ChannelPatterns {
				seen[p] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// WildcardPattern builds the "every channel of this model" un-ignore
// pattern, e.g. "LOW_BAT:VALUES@HmIP-eTRV-2:all".
func WildcardPattern(parameter string, paramset hmenum.ParamsetKey, model string) string {
	return fmt.Sprintf("%s:%s@%s:%s", parameter, paramset, model, UnIgnoreWildcard)
}

// ChannelPattern builds the channel-specific un-ignore pattern, e.g.
// "LOW_BAT:VALUES@HmIP-eTRV-2:0".
func ChannelPattern(parameter string, paramset hmenum.ParamsetKey, model string, channelNo int) string {
	return fmt.Sprintf("%s:%s@%s:%d", parameter, paramset, model, channelNo)
}

// mergeReasonSet turns an accumulated reason set into a precedence-
// ordered slice.
func mergeReasonSet(set map[HiddenReason]struct{}) []HiddenReason {
	list := make([]HiddenReason, 0, len(set))
	for r := range set {
		list = append(list, r)
	}
	return MergeReasons(list)
}
