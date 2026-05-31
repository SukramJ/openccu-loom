// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_OptionPresetVal_LabelKey_InEasymodeDecoder pins that the
// OptionPresetVal struct in ccudata/easymode.go declares the LabelKey field.
// The JSON tag `label_key` ensures that easymode archives with translation
// keys are decoded correctly; dropping the field would silently fall back to
// empty labels for all preset values that rely on i18n lookup.
func TestPin_OptionPresetVal_LabelKey_InEasymodeDecoder(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/ccudata/easymode.go",
		"internal/ccudata", "LabelKey",
	)
}
