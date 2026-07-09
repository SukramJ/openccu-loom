// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// The runtime read-path bounds checks (checkFloatBounds / checkIntBounds) let a
// declared SPECIAL sentinel bypass MIN/MAX, using byte-for-byte the same rule
// (parameter.MatchesSpecialValue) as the write-coerce and validation paths — so
// a value written through Coerce and later read back reaches the identical
// verdict. The bypass matches the CCU server keeping declared specials
// unclamped (the reference CCU simulator ccu.py _clamp_numeric_value). A value
// that is neither in range nor a declared special is rejected.

func TestCheckFloatBoundsSpecialBypassBothWireFormats(t *testing.T) {
	t.Parallel()
	// NOT_USED = 0.0 sits below MIN = 0.5 in both encodings.
	object := hmproto.ParameterData{
		Min:     json.RawMessage(`0.5`),
		Max:     json.RawMessage(`15.5`),
		Special: json.RawMessage(`{"NOT_USED": 0.0}`),
	}
	list := hmproto.ParameterData{
		Min:     json.RawMessage(`0.5`),
		Max:     json.RawMessage(`15.5`),
		Special: json.RawMessage(`[{"ID": "NOT_USED", "VALUE": 0.0}]`),
	}
	if err := checkFloatBounds(object, 0.0); err != nil {
		t.Errorf("object-format SPECIAL 0.0 must bypass MIN: %v", err)
	}
	if err := checkFloatBounds(list, 0.0); err != nil {
		t.Errorf("list-format SPECIAL 0.0 must bypass MIN: %v", err)
	}
}

func TestCheckFloatBoundsRejectsNonSpecialOutOfRange(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Min:     json.RawMessage(`0.5`),
		Max:     json.RawMessage(`15.5`),
		Special: json.RawMessage(`{"NOT_USED": 0.0}`),
	}
	if err := checkFloatBounds(desc, 0.4); err == nil {
		t.Error("0.4 is neither in range nor a declared special: expected error")
	}
	if err := checkFloatBounds(desc, 10.0); err != nil {
		t.Errorf("10.0 is within [0.5, 15.5]: %v", err)
	}
}

func TestCheckIntBoundsSpecialBypass(t *testing.T) {
	t.Parallel()
	// PERMANENT = 255 sits above MAX = 254 (HM CONF_BUTTON_TIME shape).
	desc := hmproto.ParameterData{
		Min:     json.RawMessage(`1`),
		Max:     json.RawMessage(`254`),
		Special: json.RawMessage(`{"PERMANENT": 255}`),
	}
	if err := checkIntBounds(desc, 255); err != nil {
		t.Errorf("SPECIAL PERMANENT 255 must bypass MAX: %v", err)
	}
	if err := checkIntBounds(desc, 300); err == nil {
		t.Error("300 is neither in range nor a declared special: expected error")
	}
}
