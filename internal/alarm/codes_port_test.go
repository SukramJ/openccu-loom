// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
)

// TestWiredCodeValidatorAlsoMatchesDuress pins the port the engine
// resolves by interface assertion rather than by construction: the
// validator this service wires is expected to answer the duress
// question without the side effects of a full validation, and a
// validator that cannot silently loses duress detection on the verbs
// whose outcome is a no-op (a disarm of an already-disarmed zone).
func TestWiredCodeValidatorAlsoMatchesDuress(t *testing.T) {
	var validator engine.CodeValidator = codes.New(codes.Deps{})
	if _, ok := validator.(engine.DuressMatcher); !ok {
		t.Fatalf("the wired code validator (%T) does not implement engine.DuressMatcher", validator)
	}
}
