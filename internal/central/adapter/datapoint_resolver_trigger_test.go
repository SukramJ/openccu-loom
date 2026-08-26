// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestButtonActionParametersResolveToActionTrigger pins the contract the
// alarm domain's motion reset depends on: every parameter classified as
// a button action must resolve to a data point that can be fired
// through [generic.ActionTrigger].
//
// The guard exists because the classification and the consumer live in
// different packages and are joined only by a runtime type assertion.
// RESET_MOTION shipped classified as a button action while the resetter
// asserted the concrete [generic.Action] shape, so the assertion was
// false for every real detector — no compile error, no failing test,
// and a feature that was inert on real hardware while looking correct
// in every unit test that supplied its own port fake.
func TestButtonActionParametersResolveToActionTrigger(t *testing.T) {
	t.Parallel()

	if len(buttonActionParameters) == 0 {
		t.Fatal("buttonActionParameters is empty — the guard would pass vacuously")
	}

	for param := range buttonActionParameters {
		t.Run(string(param), func(t *testing.T) {
			t.Parallel()

			dp := resolveDataPoint(generic.Spec{
				Key: hmtypes.DataPointKey{
					ChannelAddress: "0001D3C99C1234:1",
					Parameter:      string(param),
				},
				CentralName: "TestCentral",
				Descriptor: hmproto.ParameterData{
					Type:       hmenum.ParameterTypeAction,
					Operations: hmenum.OperationsWrite,
				},
			})
			if dp == nil {
				t.Fatalf("%s resolved to no data point", param)
			}
			if _, ok := dp.(generic.ActionTrigger); !ok {
				t.Errorf("%s resolved to %T, which does not implement generic.ActionTrigger — "+
					"any consumer firing this parameter silently does nothing", param, dp)
			}
		})
	}
}

// TestResetParametersAreClassifiedAsButtonActions pins the other half:
// the alarm reset maps a watched state parameter onto a reset action, so
// each reset action it can name has to be in the classification the test
// above covers. A reset parameter missing here resolves to a plain
// Action and still works, but the two lists drifting apart is how the
// original defect started.
func TestResetParametersAreClassifiedAsButtonActions(t *testing.T) {
	t.Parallel()

	for _, param := range []hmenum.Parameter{
		hmenum.ParameterResetMotion,
		hmenum.ParameterResetPresence,
	} {
		if _, ok := buttonActionParameters[param]; !ok {
			t.Errorf("%s is not in buttonActionParameters", param)
		}
	}
}
