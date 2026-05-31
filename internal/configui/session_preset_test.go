// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func makePresetSession() *configui.Session {
	descs := map[string]hmproto.ParameterData{
		"LEVEL":   {Type: hmenum.ParameterTypeFloat},
		"ON_TIME": {Type: hmenum.ParameterTypeFloat},
	}
	initial := map[string]any{
		"LEVEL":   0.0,
		"ON_TIME": 5.0,
	}
	return configui.NewSession(descs, initial)
}

// TestSessionApplyPreset verifies that ApplyPreset applies known
// parameters and silently skips unknown ones (best-effort).
func TestSessionApplyPreset(t *testing.T) {
	s := makePresetSession()

	errs := s.ApplyPreset(map[string]any{
		"LEVEL":   0.8,
		"ON_TIME": 10.0,
		"UNKNOWN": "ignored",
	})
	if len(errs) != 0 {
		t.Fatalf("ApplyPreset returned errors: %v", errs)
	}

	if got := s.CurrentValue("LEVEL"); got != 0.8 {
		t.Errorf("LEVEL: got %v, want 0.8", got)
	}
	if got := s.CurrentValue("ON_TIME"); got != 10.0 {
		t.Errorf("ON_TIME: got %v, want 10.0", got)
	}
	if !s.IsDirty() {
		t.Error("session should be dirty after ApplyPreset")
	}
}

// TestSessionApplyPresetIsUndoable verifies that each applied value can
// be undone individually.
func TestSessionApplyPresetIsUndoable(t *testing.T) {
	s := makePresetSession()

	s.ApplyPreset(map[string]any{"LEVEL": 0.5, "ON_TIME": 20.0})

	// Two values applied → two undo steps.
	if !s.CanUndo() {
		t.Fatal("expected CanUndo after ApplyPreset")
	}
	s.Undo()
	if !s.CanUndo() {
		t.Fatal("expected a second undo step")
	}
	s.Undo()
	if s.IsDirty() {
		t.Error("session should be clean after two undos")
	}
}

// TestSessionApplyPresetSkipsUnknown verifies that unknown parameters
// are silently ignored and do not produce errors.
func TestSessionApplyPresetSkipsUnknown(t *testing.T) {
	s := makePresetSession()

	errs := s.ApplyPreset(map[string]any{"NO_SUCH_PARAM": 99})
	if len(errs) != 0 {
		t.Fatalf("expected no errors for unknown param, got: %v", errs)
	}
	if s.IsDirty() {
		t.Error("session should be clean when only unknown params were in preset")
	}
}
