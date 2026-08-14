// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func sampleDescriptions() map[string]hmproto.ParameterData {
	return map[string]hmproto.ParameterData{
		"TEMPERATURE_OFFSET": {Type: hmenum.ParameterTypeFloat, Default: json.RawMessage("0")},
		"DECAL":              {Type: hmenum.ParameterTypeBool, Default: json.RawMessage("false")},
	}
}

func TestSessionInitiallyClean(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5, "DECAL": false})
	if s.IsDirty() {
		t.Fatal("fresh session must not be dirty")
	}
	if s.CanUndo() || s.CanRedo() {
		t.Fatal("fresh session must have empty stacks")
	}
	if len(s.Changes()) != 0 {
		t.Fatalf("Changes=%+v want empty", s.Changes())
	}
}

func TestSessionSetTracksUndoAndDirty(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	if !s.IsDirty() {
		t.Fatal("after Set must be dirty")
	}
	if !s.CanUndo() {
		t.Fatal("after Set must allow undo")
	}
	if got := s.Changes(); len(got) != 1 || got["TEMPERATURE_OFFSET"] != 1.5 {
		t.Fatalf("Changes=%+v", got)
	}
}

func TestSessionSetSameValueIsNoOp(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 0.5)
	if s.IsDirty() || s.CanUndo() {
		t.Fatal("Set with identical value must not push undo entry")
	}
}

func TestSessionUndoRedoRoundTrip(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	if !s.Undo() {
		t.Fatal("Undo must succeed")
	}
	if s.IsDirty() {
		t.Fatal("after Undo must be clean again")
	}
	if !s.CanRedo() {
		t.Fatal("Redo must be available after Undo")
	}
	if !s.Redo() {
		t.Fatal("Redo must succeed")
	}
	if !s.IsDirty() || s.CanRedo() {
		t.Fatal("after Redo: dirty + redo-stack drained")
	}
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != 1.5 {
		t.Fatalf("after Redo current=%v want 1.5", got)
	}
}

func TestSessionSetClearsRedoStack(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	s.Undo()
	if !s.CanRedo() {
		t.Fatal("redo must be available")
	}
	s.Set("TEMPERATURE_OFFSET", 2.5)
	if s.CanRedo() {
		t.Fatal("Set after Undo must clear the redo stack")
	}
}

func TestSessionDiscardRevertsAll(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5, "DECAL": false})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	s.Set("DECAL", true)
	s.Discard()
	if s.IsDirty() {
		t.Fatal("after Discard must be clean")
	}
	if s.CanUndo() || s.CanRedo() {
		t.Fatal("after Discard stacks must be empty")
	}
	if s.CurrentValue("TEMPERATURE_OFFSET") != 0.5 || s.CurrentValue("DECAL") != false {
		t.Fatalf("after Discard values not restored: %v %v", s.CurrentValue("TEMPERATURE_OFFSET"), s.CurrentValue("DECAL"))
	}
}

func TestSessionResetToDefaultsUsesDescriptors(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 1.5, "DECAL": true})
	s.ResetToDefaults()
	// JSON unmarshals "0" as float64; "false" as bool. We accept any
	// numeric float so tests stay portable.
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != float64(0) {
		t.Fatalf("TEMPERATURE_OFFSET=%v want 0", got)
	}
	if got := s.CurrentValue("DECAL"); got != false {
		t.Fatalf("DECAL=%v want false", got)
	}
	// Each reset is recorded — undo brings the originals back.
	for s.CanUndo() {
		s.Undo()
	}
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != 1.5 {
		t.Fatalf("after Undo TEMPERATURE_OFFSET=%v want 1.5 (original)", got)
	}
}

func TestSessionChangesOnlyShowsDelta(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5, "DECAL": false})
	s.Set("DECAL", true)
	got := s.Changes()
	if len(got) != 1 {
		t.Fatalf("Changes=%v want one entry", got)
	}
	if got["DECAL"] != true {
		t.Fatalf("DECAL=%v want true", got["DECAL"])
	}
}

func TestSessionChangedParametersCarriesFromTo(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	got := s.ChangedParameters()
	if len(got) != 1 {
		t.Fatalf("changed=%+v", got)
	}
	if got[0].Parameter != "TEMPERATURE_OFFSET" || got[0].From != 0.5 || got[0].To != 1.5 {
		t.Fatalf("change=%+v", got[0])
	}
}

// A WebSocket client controls the value of `config.session.set` verbatim, so a
// JSON array or object arrives as an uncomparable []any / map[string]any.
// Comparing two of those with Go's == panics, which used to take the whole
// connection down mid-frame. Every diffing path must survive them.
func TestSessionToleratesUncomparableValues(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})

	s.Set("TEMPERATURE_OFFSET", []any{1.0, 2.0})
	s.Set("TEMPERATURE_OFFSET", []any{1.0, 2.0})

	if !s.IsDirty() {
		t.Fatal("session must be dirty after storing an array value")
	}
	if got := len(s.ChangedParameters()); got != 1 {
		t.Fatalf("ChangedParameters=%d want 1", got)
	}
	if got := s.Changes(); len(got) != 1 {
		t.Fatalf("Changes=%v want one entry", got)
	}
	if got := s.ValidateChanges(nil); len(got) != 1 {
		t.Fatalf("ValidateChanges=%+v want one issue", got)
	}
	// The repeated Set carries an identical value, so it must not push a
	// second undo entry — one Undo restores the initial scalar.
	if !s.Undo() {
		t.Fatal("Undo must succeed")
	}
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != 0.5 {
		t.Fatalf("after Undo=%v want 0.5", got)
	}
	if s.IsDirty() {
		t.Fatal("session must be clean again after the single Undo")
	}
}

// Values that arrive uncomparable from the CCU snapshot itself must not blow up
// the diff either: the initial and the current side then hold the same
// uncomparable dynamic type, which is exactly the case Go's == rejects.
func TestSessionToleratesUncomparableInitialValues(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": []any{1.0}})

	if s.IsDirty() {
		t.Fatal("untouched session must be clean")
	}
	if got := s.Changes(); len(got) != 0 {
		t.Fatalf("Changes=%v want empty", got)
	}
	if got := s.ChangedParameters(); len(got) != 0 {
		t.Fatalf("ChangedParameters=%+v want empty", got)
	}
	if got := s.ValidateChanges(nil); len(got) != 0 {
		t.Fatalf("ValidateChanges=%+v want none", got)
	}
}
