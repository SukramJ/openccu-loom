// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_OptionPresetVal_LabelKey_InEasymodeDecoder pins that
// OptionPresetVal in ccudata/easymode.go declares LabelKey AND binds it
// to the `label_key` JSON field.
//
// Both halves are load-bearing, and the second one is the one that fails
// quietly: deleting the field breaks the build at every reader, while a
// mistyped tag compiles and simply decodes nothing, so every preset
// value that resolves its label through i18n falls back to an empty
// string with no error anywhere.
func TestPin_OptionPresetVal_LabelKey_InEasymodeDecoder(t *testing.T) {
	// A file-wide identifier search would stay green even with this field
	// deleted: MasterProfileDef declares its own unrelated LabelKey field
	// in the same file, so the pin must be scoped to the OptionPresetVal
	// struct declaration specifically.
	contract.MustFindStructFieldDecl(
		t,
		"internal/ccudata/easymode.go",
		"OptionPresetVal", "LabelKey",
	)
	contract.MustFindStructFieldTag(
		t,
		"internal/ccudata/easymode.go",
		"OptionPresetVal", "LabelKey", `json:"label_key,omitempty"`,
	)
}
