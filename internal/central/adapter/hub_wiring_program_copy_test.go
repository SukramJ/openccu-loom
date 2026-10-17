// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// editorCopyPrograms is what Program.getAll returns while a program is open
// in the CCU WebUI editor: the original and its working copy, same name.
func editorCopyPrograms() []map[string]any {
	return []map[string]any{
		{"id": "p1", "name": "Heater", "isActive": true, "isInternal": false},
		{"id": "p2", "name": "Heater", "isActive": false, "isInternal": false},
	}
}

func loadEditorCopyScan(t *testing.T, regaOut string, h *hub.Hub) {
	t.Helper()
	s := newProgramSummaryServerWith(t, regaOut, editorCopyPrograms())
	jc, runner := newProgramSummaryRunner(t, s.srv)
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{enableProgramScan: true}); err != nil {
		t.Fatalf("loadPrograms: %v", err)
	}
}

// TestLoadProgramsSkipsTheWebUIEditorCopy pins issue 981: the copy the
// WebUI editor creates (is_copy from get_program_descriptions.fn) is not
// imported as a program of its own; the original is.
func TestLoadProgramsSkipsTheWebUIEditorCopy(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("c")
	loadEditorCopyScan(t, `[{"id":"p1","is_copy":false},{"id":"p2","is_copy":true}]`, h)
	if _, ok := h.Program("p1"); !ok {
		t.Error("the original program p1 was not imported")
	}
	if _, ok := h.Program("p2"); ok {
		t.Error("the editor copy p2 was imported as a program")
	}
}

// TestLoadProgramsRemovesAnEditorCopyImportedEarlier: a copy that entered
// the hub before (an older scan, or the editor left through another menu)
// is dropped by the next scan that flags it.
func TestLoadProgramsRemovesAnEditorCopyImportedEarlier(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("c")
	h.PutProgram(hub.NewProgram("c", "p2", "Heater", "", false, &noopProgramWriter{}))
	loadEditorCopyScan(t, `[{"id":"p1","is_copy":false},{"id":"p2","is_copy":true}]`, h)
	if _, ok := h.Program("p2"); ok {
		t.Error("the editor copy p2 survived the scan that flagged it")
	}
}

// TestLoadProgramsKeepsEveryProgramWithoutTheCopyFlag: an answer without
// is_copy (an older script, a simulator) filters nothing.
func TestLoadProgramsKeepsEveryProgramWithoutTheCopyFlag(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("c")
	loadEditorCopyScan(t, `[{"id":"p1"},{"id":"p2"}]`, h)
	for _, id := range []string{"p1", "p2"} {
		if _, ok := h.Program(id); !ok {
			t.Errorf("program %s missing although no copy flag was sent", id)
		}
	}
}
