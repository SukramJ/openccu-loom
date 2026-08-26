// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rega

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCreateFunctionScriptUsesValidEnumType pins the CCU-observed EnumType
// for a function (Gewerk): OT_ENUM objects registered in ID_FUNCTIONS carry
// EnumType etFunction (=4), not the undefined identifier etSubsection. The
// ReGa runtime has no etSubsection constant, and referencing an undefined
// identifier aborts the whole script with no output at all — so
// create_function.fn used to run, produce nothing, and POST /functions
// would fail on every real CCU while looking, from the Go side, like an
// ordinary transport error.
//
// A/B against a live CCU: the same script body without the EnumType line
// prints etRoom=2, etFunction=4, etFavorite=6; with the etSubsection line
// present the CCU returns no output whatsoever.
func TestCreateFunctionScriptUsesValidEnumType(t *testing.T) {
	t.Parallel()
	body, err := loadScript(hmenum.RegaScriptCreateFunction)
	if err != nil {
		t.Fatalf("loadScript(create_function): %v", err)
	}
	if !strings.Contains(body, "o.EnumType(etFunction)") {
		t.Fatal("create_function.fn must set EnumType(etFunction) — a Gewerk is EnumType=4 on every real CCU")
	}
	if strings.Contains(body, "EnumType(etSubsection)") {
		t.Fatal("create_function.fn must not call EnumType(etSubsection) — it is not a defined ReGa constant and aborts the script silently")
	}
}
