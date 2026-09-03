// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The two descriptors below are copied verbatim out of the paramset
// descriptions embedded in the simulator this repository pins
// (github.com/SukramJ/godevccu v0.2.2, go.mod:6 —
// internal/embed/data/paramset_descriptions/{HmIP-eTRV-B1,HM-RC-19-B}.json).
// They are the two shapes an ENUM parameter takes on the wire, and they
// decide the wire form of a label-typed write in opposite directions.
//
// Counted over that corpus (399 device files, decoded as JSON and classified
// in code by the Go type of the decoded MIN/MAX/DEFAULT, not by grep): of the
// 38144 ENUM parameters that declare a VALUE_LIST, 9186 declare MIN, MAX and
// DEFAULT all as JSON integers and 28958 declare all three as JSON strings.
// Zero mix the two. The parameter's own declared bounds are therefore the
// authority on which domain its value lives in, and the classification is not
// a convention supplied here.
const (
	// HmIP-eTRV-B1 VCU1530633:1 VALUES WINDOW_STATE — read+write+event,
	// string-valued bounds.
	w2GenWindowStateDescriptorJSON = `{"MIN": "CLOSED", "OPERATIONS": 7, "MAX": "OPEN", "FLAGS": 1,
	  "ID": "WINDOW_STATE", "TYPE": "ENUM", "DEFAULT": "CLOSED",
	  "VALUE_LIST": ["CLOSED", "OPEN"],
	  "CONTROL": "HEATING_CONTROL_HMIP.WINDOW_STATE"}`

	// HM-RC-19-B VCU0000198:18 VALUES BEEP — write-only, integer-valued bounds.
	w2GenBeepDescriptorJSON = `{"TYPE": "ENUM", "OPERATIONS": 2, "FLAGS": 1, "DEFAULT": 0,
	  "MAX": 3, "MIN": 0, "TAB_ORDER": 0,
	  "VALUE_LIST": ["NONE", "TONE1", "TONE2", "TONE3"],
	  "CONTROL": "RC19_DISPLAY.BEEP"}`
)

// w2GenSpecFromDescriptor builds a Spec around a verbatim device descriptor.
func w2GenSpecFromDescriptor(t *testing.T, parameter, raw string) Spec {
	t.Helper()
	var desc hmproto.ParameterData
	if err := json.Unmarshal([]byte(raw), &desc); err != nil {
		t.Fatalf("decode descriptor for %s: %v", parameter, err)
	}
	if desc.Type != hmenum.ParameterTypeEnum || len(desc.ValueList) == 0 {
		t.Fatalf("%s: fixture is not an ENUM with a VALUE_LIST: %+v", parameter, desc)
	}
	return Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "VCU0000001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      parameter,
		},
		CentralName: "ccu-a",
		Descriptor:  desc,
	}
}

// TestW2GenSelectSendsTheFormTheDescriptorDeclares pins the wire form of a
// label-typed write on a readable ENUM.
//
// A descriptor whose MIN/MAX/DEFAULT are strings declares a value domain of
// VALUE_LIST labels, so "OPEN" — not the index 1 — is what goes on the wire.
func TestW2GenSelectSendsTheFormTheDescriptorDeclares(t *testing.T) {
	t.Parallel()
	cfg := w2GenSpecFromDescriptor(t, "WINDOW_STATE", w2GenWindowStateDescriptorJSON)
	w := &stubWriter{}
	cfg.Writer = w
	s := NewSelect(cfg)

	if s.EnumValueIsIndex() {
		t.Fatal("string-valued MIN/MAX/DEFAULT must not be classified as an index enum")
	}
	if err := s.SetLabel(context.Background(), "OPEN", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	call, ok := w.last()
	if !ok {
		t.Fatal("SetLabel sent nothing")
	}
	if call.value != any("OPEN") {
		t.Errorf("WINDOW_STATE wire value = %#v, want the label %q — the descriptor declares "+
			"MIN/MAX/DEFAULT as strings, so the value domain is the VALUE_LIST label", call.value, "OPEN")
	}
}

// TestW2GenActionSelectSendsTheFormTheDescriptorDeclares is the same pin in
// the other direction: a write-only ENUM whose bounds are integers takes the
// index, not the label.
func TestW2GenActionSelectSendsTheFormTheDescriptorDeclares(t *testing.T) {
	t.Parallel()
	cfg := w2GenSpecFromDescriptor(t, "BEEP", w2GenBeepDescriptorJSON)
	w := &stubWriter{}
	cfg.Writer = w
	a := NewActionSelect(cfg)

	if !a.EnumValueIsIndex() {
		t.Fatal("integer-valued MIN/MAX/DEFAULT must be classified as an index enum")
	}
	if err := a.TriggerLabel(context.Background(), "TONE1", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TriggerLabel: %v", err)
	}
	call, ok := w.last()
	if !ok {
		t.Fatal("TriggerLabel sent nothing")
	}
	if call.value != any(int32(1)) {
		t.Errorf("BEEP wire value = %#v, want the index int32(1) — the descriptor declares "+
			"MIN/MAX/DEFAULT as integers, so the value domain is the VALUE_LIST index", call.value)
	}
}

// TestW2GenEnumWireFormIsDecidedInOnePlace pins that both label-typed write
// paths ask the same descriptor-derived question rather than each hard-coding
// one answer. Two data points over the same VALUE_LIST and the same label
// must disagree on the wire exactly when their descriptors disagree.
func TestW2GenEnumWireFormIsDecidedInOnePlace(t *testing.T) {
	t.Parallel()

	strCfg := w2GenSpecFromDescriptor(t, "WINDOW_STATE", w2GenWindowStateDescriptorJSON)
	intDesc := strCfg.Descriptor
	intDesc.Min = json.RawMessage(`0`)
	intDesc.Max = json.RawMessage(`1`)
	intDesc.Default = json.RawMessage(`0`)

	intCfg := strCfg
	intCfg.Descriptor = intDesc

	strW, intW := &stubWriter{}, &stubWriter{}
	strCfg.Writer, intCfg.Writer = strW, intW

	if err := NewSelect(strCfg).SetLabel(context.Background(), "OPEN", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("string-bounds SetLabel: %v", err)
	}
	if err := NewSelect(intCfg).SetLabel(context.Background(), "OPEN", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("integer-bounds SetLabel: %v", err)
	}
	strCall, _ := strW.last()
	intCall, _ := intW.last()
	if strCall.value == intCall.value {
		t.Errorf("both descriptors produced wire value %#v; the MIN/MAX/DEFAULT form must decide "+
			"between the label and the index", strCall.value)
	}
	if strCall.value != any("OPEN") || intCall.value != any(int32(1)) {
		t.Errorf("string-bounds=%#v integer-bounds=%#v, want %q and int32(1)", strCall.value, intCall.value, "OPEN")
	}
}
