// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestBuiltInHidesParty(t *testing.T) {
	r := NewRules()
	if r.IsAllowed("HmIP-STH", hmenum.ParameterPartyTemperature) {
		t.Fatal("party temperature must be hidden")
	}
}

func TestModelSpecificHide(t *testing.T) {
	r := NewRules()
	r.HideForModel("HmIP-BROLL", hmenum.ParameterState)
	if r.IsAllowed("HmIP-BROLL", hmenum.ParameterState) {
		t.Fatal("model-specific rule must hide")
	}
	if !r.IsAllowed("HmIP-STH", hmenum.ParameterState) {
		t.Fatal("unrelated model must pass")
	}
}

func TestAllowUnlisted(t *testing.T) {
	r := NewRules()
	if !r.IsAllowed("anything", hmenum.ParameterLevel) {
		t.Fatal("level should default-allow")
	}
}
