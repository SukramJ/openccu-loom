// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// TestLastLevelComesFromTheSpecialValueNotFromMax pins where the
// "supports last known level" decision is read from.
//
// The rule used to be MAX > 1.0. A device never reports that: the firmware
// declares these level parameters max="1.0" and carries the sentinel as a
// separate SPECIAL member — `<special_value id="OLD_LEVEL" value="1.005"/>`
// in src/devicetypes/rftypes/rf_d.xml:608 — exported as its own field. Every
// dimmer in the descriptor corpus that offers the feature shows MAX 1.0 with
// SPECIAL {"OLD_LEVEL": 1.005}, so the branch could not fire.
//
// Nothing was missing in practice: the classification grants the feature to
// every level parameter regardless. What was wrong was the stated reason, and
// with it the only route a parameter outside that classification could take.
func TestLastLevelComesFromTheSpecialValueNotFromMax(t *testing.T) {
	t.Parallel()

	param := func(name, maxRaw string) *hmapi.UISchemaParameter {
		return &hmapi.UISchemaParameter{
			Name: name, Type: "FLOAT", Unit: "100%", Max: json.RawMessage(maxRaw),
		}
	}
	oldLevel := json.RawMessage(`{"OLD_LEVEL": 1.005}`)

	// A level parameter as a device declares it: MAX 1.0 plus the sentinel.
	p := param("SHORT_ON_LEVEL", "1.0")
	enrichLinkParameter(p, oldLevel, "en")
	if !p.HasLastValue {
		t.Error("SHORT_ON_LEVEL with SPECIAL OLD_LEVEL must offer last-known-level")
	}

	// The classification covers level parameters on its own, so the feature
	// does not depend on the descriptor being present.
	p = param("SHORT_ON_LEVEL", "1.0")
	enrichLinkParameter(p, nil, "en")
	if !p.HasLastValue {
		t.Error("a level parameter must keep the feature without a declared SPECIAL")
	}

	// A parameter the classification does not treat as a level: the declared
	// sentinel is the only route, and it is the one the old MAX test could not
	// take.
	p = param("SHORT_COND_VALUE_LO", "1.0")
	enrichLinkParameter(p, nil, "en")
	before := p.HasLastValue
	p = param("SHORT_COND_VALUE_LO", "1.0")
	enrichLinkParameter(p, oldLevel, "en")
	if before && p.HasLastValue {
		t.Skip("classification already grants the feature here; the descriptor route is not observable")
	}
	if before == p.HasLastValue {
		t.Errorf("declaring SPECIAL OLD_LEVEL changed nothing (%v -> %v)", before, p.HasLastValue)
	}
}
