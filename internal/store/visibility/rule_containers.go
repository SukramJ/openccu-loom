// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ChannelParamsetRules manages parameter rules indexed by (channelNo,
// paramsetKey).
type ChannelParamsetRules struct {
	// data maps (channelNo, paramsetKey) → set of parameter names.
	// channelNo == -1 is the sentinel for "any / unknown channel"
	// (mirrors Python's None).
	data map[channelParamsetKey]map[string]struct{}
}

// channelParamsetKey is the composite key for ChannelParamsetRules.
type channelParamsetKey struct {
	// channelNo is the channel number, or -1 for "any channel".
	channelNo   int
	paramsetKey hmenum.ParamsetKey
}

// anyChannelNo is the sentinel value for "any / unknown channel", mirroring
// Python's None channel. Consumed once ChannelParamsetRules gets exposed
// through Registry — F11 P1-10 follow-up.
//
//nolint:unused // see comment above
const anyChannelNo = -1

// NewChannelParamsetRules returns an empty container.
func NewChannelParamsetRules() *ChannelParamsetRules {
	return &ChannelParamsetRules{data: make(map[channelParamsetKey]map[string]struct{})}
}

// Add inserts parameter into the set for (channelNo, paramsetKey).
// Use anyChannelNo as channelNo to match any channel.
func (c *ChannelParamsetRules) Add(channelNo int, paramsetKey hmenum.ParamsetKey, parameter string) {
	k := channelParamsetKey{channelNo: channelNo, paramsetKey: paramsetKey}
	if _, ok := c.data[k]; !ok {
		c.data[k] = make(map[string]struct{})
	}
	c.data[k][parameter] = struct{}{}
}

// Contains reports whether parameter exists for (channelNo, paramsetKey).
func (c *ChannelParamsetRules) Contains(channelNo int, paramsetKey hmenum.ParamsetKey, parameter string) bool {
	k := channelParamsetKey{channelNo: channelNo, paramsetKey: paramsetKey}
	_, ok := c.data[k][parameter]
	return ok
}

// GetParameters returns the (possibly nil) set of parameters for
// (channelNo, paramsetKey).
func (c *ChannelParamsetRules) GetParameters(channelNo int, paramsetKey hmenum.ParamsetKey) map[string]struct{} {
	k := channelParamsetKey{channelNo: channelNo, paramsetKey: paramsetKey}
	return c.data[k]
}

// Update adds all parameters from the slice into the set for
// (channelNo, paramsetKey).
func (c *ChannelParamsetRules) Update(channelNo int, paramsetKey hmenum.ParamsetKey, parameters []string) {
	k := channelParamsetKey{channelNo: channelNo, paramsetKey: paramsetKey}
	if _, ok := c.data[k]; !ok {
		c.data[k] = make(map[string]struct{}, len(parameters))
	}
	for _, p := range parameters {
		c.data[k][p] = struct{}{}
	}
}

// ---------------------------------------------------------------------------

// ModelRules manages parameter rules indexed by model name. Each model
// has its own ChannelParamsetRules and a set of relevant channel numbers.
type ModelRules struct {
	channelRules     map[string]*ChannelParamsetRules
	relevantChannels map[string]map[int]struct{}
}

// NewModelRules returns an empty container.
func NewModelRules() *ModelRules {
	return &ModelRules{
		channelRules:     make(map[string]*ChannelParamsetRules),
		relevantChannels: make(map[string]map[int]struct{}),
	}
}

// AddParameter inserts a parameter rule for (model, channelNo, paramsetKey).
func (m *ModelRules) AddParameter(model string, channelNo int, paramsetKey hmenum.ParamsetKey, parameter string) {
	if _, ok := m.channelRules[model]; !ok {
		m.channelRules[model] = NewChannelParamsetRules()
	}
	m.channelRules[model].Add(channelNo, paramsetKey, parameter)
}

// AddRelevantChannel marks channelNo as relevant for MASTER paramset
// fetching for model.
func (m *ModelRules) AddRelevantChannel(model string, channelNo int) {
	if _, ok := m.relevantChannels[model]; !ok {
		m.relevantChannels[model] = make(map[int]struct{})
	}
	m.relevantChannels[model][channelNo] = struct{}{}
}

// Contains reports whether parameter is registered for
// (model, channelNo, paramsetKey).
func (m *ModelRules) Contains(model string, channelNo int, paramsetKey hmenum.ParamsetKey, parameter string) bool {
	cr, ok := m.channelRules[model]
	if !ok {
		return false
	}
	return cr.Contains(channelNo, paramsetKey, parameter)
}

// GetModels returns all model names that have rules.
func (m *ModelRules) GetModels() []string {
	models := make([]string, 0, len(m.channelRules))
	for k := range m.channelRules {
		models = append(models, k)
	}
	return models
}

// GetRelevantChannels returns the set of relevant channel numbers for model.
// Returns nil if the model has no channel rules.
func (m *ModelRules) GetRelevantChannels(model string) map[int]struct{} {
	return m.relevantChannels[model]
}

// HasRelevantChannel reports whether channelNo is marked relevant for model.
func (m *ModelRules) HasRelevantChannel(model string, channelNo int) bool {
	_, ok := m.relevantChannels[model][channelNo]
	return ok
}

// UpdateParameters adds all parameters from the slice into the rule set for
// (model, channelNo, paramsetKey).
func (m *ModelRules) UpdateParameters(model string, channelNo int, paramsetKey hmenum.ParamsetKey, parameters []string) {
	if _, ok := m.channelRules[model]; !ok {
		m.channelRules[model] = NewChannelParamsetRules()
	}
	m.channelRules[model].Update(channelNo, paramsetKey, parameters)
}
