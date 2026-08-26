// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func resolverDesc(t hmenum.ParameterType, ops hmenum.Operations) hmproto.ParameterData {
	return hmproto.ParameterData{Type: t, Operations: ops}
}

func TestResolveReadOnlySensorVsBinarySensor(t *testing.T) {
	plain := ResolveDataPointKind(ResolveInput{
		Descriptor: resolverDesc(hmenum.ParameterTypeFloat, hmenum.OperationsRead),
	})
	if plain != KindSensor {
		t.Fatalf("read-only float = %s want sensor", plain)
	}
	binary := ResolveDataPointKind(ResolveInput{
		Descriptor:     resolverDesc(hmenum.ParameterTypeBool, hmenum.OperationsRead),
		IsBinarySensor: true,
	})
	if binary != KindBinarySensor {
		t.Fatalf("binary sensor flagged = %s want binary_sensor", binary)
	}
}

func TestResolveReadOnlyClickEventReturnsUnknown(t *testing.T) {
	got := ResolveDataPointKind(ResolveInput{
		Parameter:    "PRESS_SHORT",
		Descriptor:   resolverDesc(hmenum.ParameterTypeAction, hmenum.OperationsRead|hmenum.OperationsEvent),
		IsClickEvent: true,
	})
	if got != KindUnknown {
		t.Fatalf("click event read-only = %s want unknown (event subsystem handles it)", got)
	}
}

func TestResolveWritableStandardShapes(t *testing.T) {
	rw := hmenum.OperationsRead | hmenum.OperationsWrite
	cases := map[hmenum.ParameterType]ResolvedKind{
		hmenum.ParameterTypeBool:    KindSwitch,
		hmenum.ParameterTypeEnum:    KindSelect,
		hmenum.ParameterTypeFloat:   KindNumberFloat,
		hmenum.ParameterTypeInteger: KindNumberInteger,
		hmenum.ParameterTypeString:  KindText,
	}
	for ptype, want := range cases {
		got := ResolveDataPointKind(ResolveInput{
			Descriptor: resolverDesc(ptype, rw),
		})
		if got != want {
			t.Fatalf("%s rw = %s want %s", ptype, got, want)
		}
	}
}

func TestResolveWriteOnlyNonActionUsesActionShapes(t *testing.T) {
	wo := hmenum.OperationsWrite
	cases := map[hmenum.ParameterType]ResolvedKind{
		hmenum.ParameterTypeFloat:   KindActionFloat,
		hmenum.ParameterTypeInteger: KindActionInteger,
		hmenum.ParameterTypeBool:    KindActionBoolean,
		hmenum.ParameterTypeString:  KindActionString,
	}
	for ptype, want := range cases {
		got := ResolveDataPointKind(ResolveInput{
			Descriptor: resolverDesc(ptype, wo),
		})
		if got != want {
			t.Fatalf("%s write-only = %s want %s", ptype, got, want)
		}
	}
}

func TestResolveWriteOnlyValueListMakesActionSelect(t *testing.T) {
	got := ResolveDataPointKind(ResolveInput{
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsWrite,
			ValueList:  []string{"OFF", "ON", "AUTO"},
		},
	})
	if got != KindActionSelect {
		t.Fatalf("write-only with value-list = %s want action_select", got)
	}
}

func TestResolveActionVariants(t *testing.T) {
	wo := hmenum.OperationsWrite
	rwe := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent

	// Write-only ACTION → Action.
	if got := ResolveDataPointKind(ResolveInput{
		Descriptor: resolverDesc(hmenum.ParameterTypeAction, wo),
	}); got != KindAction {
		t.Fatalf("write-only action = %s want action", got)
	}
	// Write-only ACTION + IsButtonAction → Button.
	if got := ResolveDataPointKind(ResolveInput{
		Descriptor:     resolverDesc(hmenum.ParameterTypeAction, wo),
		IsButtonAction: true,
	}); got != KindButton {
		t.Fatalf("button action = %s want button", got)
	}
	// Write-only ACTION + value_list → ActionSelect.
	if got := ResolveDataPointKind(ResolveInput{
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: wo,
			ValueList:  []string{"A", "B"},
		},
	}); got != KindActionSelect {
		t.Fatalf("action with value-list = %s want action_select", got)
	}
	// Read+Write+Event ACTION + click event → Button.
	if got := ResolveDataPointKind(ResolveInput{
		Parameter:    "PRESS_SHORT",
		Descriptor:   resolverDesc(hmenum.ParameterTypeAction, rwe),
		IsClickEvent: true,
	}); got != KindButton {
		t.Fatalf("click-event action = %s want button", got)
	}
	// Read+Write ACTION (no click) → Switch.
	if got := ResolveDataPointKind(ResolveInput{
		Descriptor: resolverDesc(hmenum.ParameterTypeAction, hmenum.OperationsRead|hmenum.OperationsWrite),
	}); got != KindSwitch {
		t.Fatalf("rw action = %s want switch", got)
	}
}

func TestResolvedKindCategoryAndDataPointType(t *testing.T) {
	cases := []struct {
		kind     ResolvedKind
		category hmenum.DataPointCategory
		dpType   hmenum.DataPointType
		isAction bool
	}{
		{KindSwitch, hmenum.DataPointCategorySwitch, hmenum.DataPointTypeSwitch, false},
		{KindBinarySensor, hmenum.DataPointCategoryBinarySensor, hmenum.DataPointTypeBinarySensor, false},
		{KindSensor, hmenum.DataPointCategorySensor, hmenum.DataPointTypeSensor, false},
		{KindButton, hmenum.DataPointCategoryButton, hmenum.DataPointTypeButton, true},
		{KindAction, hmenum.DataPointCategoryAction, hmenum.DataPointTypeButton, true},
		{KindActionFloat, hmenum.DataPointCategoryActionNumber, hmenum.DataPointTypeNumber, true},
		{KindActionInteger, hmenum.DataPointCategoryActionNumber, hmenum.DataPointTypeNumber, true},
		{KindActionSelect, hmenum.DataPointCategoryActionSelect, hmenum.DataPointTypeSelect, true},
		{KindNumberFloat, hmenum.DataPointCategoryNumber, hmenum.DataPointTypeNumber, false},
		{KindSelect, hmenum.DataPointCategorySelect, hmenum.DataPointTypeSelect, false},
		{KindText, hmenum.DataPointCategoryText, hmenum.DataPointTypeText, false},
	}
	for _, c := range cases {
		if got := c.kind.Category(); got != c.category {
			t.Fatalf("%s.Category() = %s want %s", c.kind, got, c.category)
		}
		if got := c.kind.DataPointType(); got != c.dpType {
			t.Fatalf("%s.DataPointType() = %s want %s", c.kind, got, c.dpType)
		}
		if got := c.kind.IsAction(); got != c.isAction {
			t.Fatalf("%s.IsAction() = %v want %v", c.kind, got, c.isAction)
		}
	}
}

func TestResolvedKindUnknownReturnsZeroValues(t *testing.T) {
	if got := KindUnknown.Category(); got != hmenum.DataPointCategoryUndefined {
		t.Fatalf("KindUnknown.Category() = %s want undefined", got)
	}
	if got := KindUnknown.DataPointType(); got != "" {
		t.Fatalf("KindUnknown.DataPointType() = %s want empty", got)
	}
	if got := KindUnknown.IsAction(); got {
		t.Fatalf("KindUnknown.IsAction() = true want false")
	}
}

func TestResolveUnknownTypeReturnsUnknown(t *testing.T) {
	got := ResolveDataPointKind(ResolveInput{
		Descriptor: resolverDesc(hmenum.ParameterTypeEmpty, hmenum.OperationsRead|hmenum.OperationsWrite),
	})
	if got != KindUnknown {
		t.Fatalf("empty type rw = %s want unknown", got)
	}
}
