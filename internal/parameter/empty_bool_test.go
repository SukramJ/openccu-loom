// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestEmptyStringIsNotABooleanOnTheWriteBoundary pins that an empty string is
// refused rather than silently read as false.
//
// Coerce is the REST write boundary, so this decides what a client posting
// {"value": ""} sends to a device. The CCU's own value library rejects the
// empty string in both textual boolean readers — boolFromXml takes the
// decimal tokens 0 and 1 and fails when strtol consumed nothing
// (../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:425-437), boolFromText
// takes exactly "true" and "false" (:470-488) — so reading it as false turns
// an input the CCU would have refused into a confirmed switch-off.
func TestEmptyStringIsNotABooleanOnTheWriteBoundary(t *testing.T) {
	t.Parallel()

	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeBool}
	for _, in := range []string{"", "   "} {
		got, err := parameter.Coerce(desc, in)
		if err == nil {
			t.Errorf("Coerce(%q) = %v, want an error: the CCU refuses it", in, got.Unwrap())
		}
	}
	// The spellings the CCU does accept keep working.
	for _, tc := range []struct {
		in   string
		want bool
	}{{"1", true}, {"0", false}, {"true", true}, {"FALSE", false}} {
		got, err := parameter.Coerce(desc, tc.in)
		if err != nil {
			t.Errorf("Coerce(%q): %v", tc.in, err)
			continue
		}
		if b, ok := got.Unwrap().(bool); !ok || b != tc.want {
			t.Errorf("Coerce(%q) = %v, want %v", tc.in, got.Unwrap(), tc.want)
		}
	}
}
