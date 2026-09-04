// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2ParBoolTrueTokens is the truth set both copies claim to implement.
// Case is varied on purpose: both sides fold case before matching.
var w2ParBoolTrueTokens = []string{"y", "yes", "t", "true", "on", "1", "YES", "True", "ON"}

// w2ParBoolFalseTokens are strings neither copy may accept. "false", "no",
// "off" and "0" are the CCU's own false spellings; "" is the empty read;
// "maybe" is a token no decoder knows.
var w2ParBoolFalseTokens = []string{"false", "no", "off", "0", "", "maybe", "2"}

// TestW2ParBoolTruthSetsAgree pins the two declarations of one rule — which
// CCU-reported strings count as boolean true — against each other.
//
// The live one is internal/parameter.isBoolTrueString, reached through
// ConvertReadValue for every BOOL / ACTION parameter whose wire value arrives
// as a string. The second is hmtypes.ToBool on the published pkg/ surface: a
// grep for hmtypes.ToBool over all non-test .go files in the tree returns only
// its own declaration, so nothing inside the daemon consults it, and an
// external consumer reading pkg/hmtypes takes it for the contract. Widening or
// narrowing one set alone used to leave the other behind with nothing failing.
//
// The known, deliberate divergence is whitespace and it is asserted below
// rather than glossed: the live path trims (the firmware's own `<boolean>`
// reader parses the digit with strtol, which skips leading whitespace —
// ../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:429), hmtypes.ToBool does
// not. Whether the CCU ever emits a padded boolean is not established by
// anything in the repo; the trim is the more lenient of the two, which is the
// side a read path should be on.
func TestW2ParBoolTruthSetsAgree(t *testing.T) {
	t.Parallel()

	readBool := func(t *testing.T, s string) bool {
		t.Helper()
		got := parameter.ConvertReadValue(hmenum.ParameterTypeBool, s)
		b, ok := got.(bool)
		if !ok {
			t.Fatalf("ConvertReadValue(BOOL, %q) returned %T (%v), want bool — the read-path cast no longer "+
				"routes strings through the truth set", s, got, got)
		}
		return b
	}

	for _, tok := range w2ParBoolTrueTokens {
		live := readBool(t, tok)
		exported, err := hmtypes.ToBool(tok)
		if err != nil {
			t.Errorf("hmtypes.ToBool(%q) errored: %v", tok, err)
		}
		if !live {
			t.Errorf("the live read path (parameter.ConvertReadValue BOOL) reports %q as false; it is in the "+
				"six-token CCU truth set", tok)
		}
		if !exported {
			t.Errorf("hmtypes.ToBool(%q) is false while the live read path reports true — the published copy of "+
				"the truth set has drifted from internal/parameter.isBoolTrueString", tok)
		}
	}

	for _, tok := range w2ParBoolFalseTokens {
		live := readBool(t, tok)
		exported, err := hmtypes.ToBool(tok)
		if err != nil {
			t.Errorf("hmtypes.ToBool(%q) errored: %v", tok, err)
		}
		if live {
			t.Errorf("the live read path accepts %q as boolean true; it is in neither copy's truth set", tok)
		}
		if exported {
			t.Errorf("hmtypes.ToBool(%q) is true while the live read path reports false — the published copy of "+
				"the truth set has drifted from internal/parameter.isBoolTrueString", tok)
		}
	}

	// The whitespace divergence, stated as an assertion so a change on either
	// side is visible here rather than in a consumer.
	if !readBool(t, " true ") {
		t.Error("the live read path must trim a padded boolean: the CCU's own <boolean> reader parses with " +
			"strtol, which skips leading whitespace (XmlRpcValue.cpp:429)")
	}
	if padded, err := hmtypes.ToBool(" true "); err != nil || padded {
		t.Errorf("hmtypes.ToBool(%q) = (%v, %v); this guard records that the exported copy does NOT trim. "+
			"If that changed, delete this assertion and fold whitespace into the shared token loops above.",
			" true ", padded, err)
	}
}
