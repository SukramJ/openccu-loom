// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestW2GenPressFamilyHasOneDefinition pins every press-family answer in this
// package to [isPressParameter] and [isLongPressParameter].
//
// Why it matters: the Matter assembler collects a data point into the press
// group when its MatterMeasurementClass is MomentarySwitch, and
// [NewButtonGroup] then drops any member [isPressParameter] rejects. If a
// classifier says "press" where the group filter says "not press", the group
// comes back nil and the channel gets no GenericSwitch endpoint at all —
// nothing logs, nothing errors, the button is simply absent. The long-press
// pair fails the other way round: the endpoint advertises a feature whose
// events never arrive.
//
// The candidate parameters are read from [hmenum.ClickEvents] rather than
// listed here, so the guard cannot agree with whichever copy it was written
// from. Two non-press controls are added to keep the "everything is a press"
// answer from passing.
func TestW2GenPressFamilyHasOneDefinition(t *testing.T) {
	t.Parallel()

	candidates := make([]hmenum.Parameter, 0, len(hmenum.ClickEvents)+2)
	for p := range hmenum.ClickEvents {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, hmenum.ParameterLevel, hmenum.ParameterState)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })

	pressCount, longCount := 0, 0
	for _, p := range candidates {
		wantPress := isPressParameter(p)
		wantLong := isLongPressParameter(p)
		if wantPress {
			pressCount++
		}
		if wantLong {
			longCount++
		}

		button := NewButton(w2GenPressSpec(p))
		action := NewAction(w2GenPressSpec(p))

		if got := button.MatterMeasurementClass() == interfaces.MatterMeasurementMomentarySwitch; got != wantPress {
			t.Errorf("%s: Button.MatterMeasurementClass momentary=%v, isPressParameter=%v — "+
				"the press family has one definition and this is not reading it", p, got, wantPress)
		}
		if got := action.MatterMeasurementClass() == interfaces.MatterMeasurementMomentarySwitch; got != wantPress {
			t.Errorf("%s: Action.MatterMeasurementClass momentary=%v, isPressParameter=%v — "+
				"a data point classified here but rejected by NewButtonGroup produces no endpoint at all",
				p, got, wantPress)
		}
		if got := matterMeasurementForParameter(p) == interfaces.MatterMeasurementMomentarySwitch; got != wantPress {
			t.Errorf("%s: matterMeasurementForParameter momentary=%v, isPressParameter=%v", p, got, wantPress)
		}
		if got := button.MatterSwitchSupportsLongPress(); got != wantLong {
			t.Errorf("%s: Button.MatterSwitchSupportsLongPress=%v, isLongPressParameter=%v", p, got, wantLong)
		}
		if got := action.MatterSwitchSupportsLongPress(); got != wantLong {
			t.Errorf("%s: Action.MatterSwitchSupportsLongPress=%v, isLongPressParameter=%v — "+
				"a long-press feature advertised without a source emits nothing", p, got, wantLong)
		}

		// Group membership is the consumer that makes disagreement expensive.
		group := NewButtonGroup(button)
		if (group != nil) != wantPress {
			t.Errorf("%s: NewButtonGroup accepted=%v, isPressParameter=%v", p, group != nil, wantPress)
		}
	}

	if pressCount == 0 || longCount == 0 || pressCount == len(candidates) {
		t.Fatalf("candidate set does not separate the answers (press=%d long=%d of %d); the guard "+
			"would pass whatever the classifiers returned", pressCount, longCount, len(candidates))
	}
}

// TestW2GenLockParametersStayOutsideTheMomentarySwitchProjection pins the
// documented divergence between [hmenum.ClickEvents] and the press family:
// PRESS_LOCK / PRESS_UNLOCK are click events with keymatic lock semantics and
// must not project onto GenericSwitch.
func TestW2GenLockParametersStayOutsideTheMomentarySwitchProjection(t *testing.T) {
	t.Parallel()

	for _, p := range []hmenum.Parameter{hmenum.ParameterPressLock, hmenum.ParameterPressUnlock} {
		if !p.IsClickEvent() {
			t.Fatalf("%s is no longer a click event; this guard's subject moved", p)
		}
		if isPressParameter(p) {
			t.Errorf("%s must stay outside the momentary-switch projection: it carries lock "+
				"semantics, not a press position", p)
		}
	}
}

func w2GenPressSpec(p hmenum.Parameter) Spec {
	return Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "VCU0000001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		CentralName: "ccu-a",
		Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeAction, Operations: hmenum.OperationsWrite | hmenum.OperationsEvent},
	}
}
