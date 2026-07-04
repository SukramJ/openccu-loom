// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

// CrossValidationConstraint expresses a relation between several
// parameters that must hold for the form to be considered valid (e.g.
// "MAX_TEMPERATURE must be ≥ MIN_TEMPERATURE"). Consumed by
// [Session.Validate] and friends; the WS layer feeds the constraints
// extracted from the embedded metadata archives.
type CrossValidationConstraint struct {
	RuleID          string   `json:"rule_id"`
	Rule            string   `json:"rule"`
	AppliesToParams []string `json:"applies_to_params"`
	ErrorKey        string   `json:"error_key"`
	ParamA          string   `json:"param_a,omitempty"`
	ParamB          string   `json:"param_b,omitempty"`
	Param           string   `json:"param,omitempty"`
	MinParam        string   `json:"min_param,omitempty"`
	MaxParam        string   `json:"max_param,omitempty"`
}
