// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"testing"
)

// deleterStub implements ProgramDeleter (execute + set-enabled + delete).
type deleterStub struct {
	deletedID string
	deleteErr error
	calls     int
}

func (d *deleterStub) ExecuteProgram(context.Context, string) error { return nil }

func (d *deleterStub) SetProgramEnabled(context.Context, string, bool) error { return nil }

func (d *deleterStub) DeleteProgram(_ context.Context, id string) error {
	d.calls++
	d.deletedID = id
	return d.deleteErr
}

func TestProgramDelete_DispatchesToDeleter(t *testing.T) {
	t.Parallel()
	w := &deleterStub{}
	p := NewProgram("ccu", "P42", "Test", "", false, w)
	if err := p.Delete(context.Background()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if w.calls != 1 || w.deletedID != "P42" {
		t.Fatalf("expected DeleteProgram(P42) once, got calls=%d id=%q", w.calls, w.deletedID)
	}
}

func TestProgramDelete_NonDeleterWriter_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	// stubProgram implements ProgramWriter but not ProgramDeleter.
	p := NewProgram("ccu", "P1", "Test", "", false, &stubProgram{})
	err := p.Delete(context.Background())
	if !errors.Is(err, ErrProgramDeleteUnsupported) {
		t.Fatalf("expected ErrProgramDeleteUnsupported, got %v", err)
	}
}

func TestProgramDelete_NoWriter_ReturnsError(t *testing.T) {
	t.Parallel()
	p := NewProgram("ccu", "P1", "Test", "", false, nil)
	if err := p.Delete(context.Background()); err == nil {
		t.Fatal("expected an error for a program without a writer")
	}
}

func TestHubDeleteProgramRemote_RemovesAndNotifies(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu")
	w := &deleterStub{}
	p := NewProgram("ccu", "P7", "Test", "", false, w)
	var removed bool
	p.OnRemoved(func() { removed = true })
	h.PutProgram(p)

	if err := h.DeleteProgramRemote(context.Background(), "P7"); err != nil {
		t.Fatalf("DeleteProgramRemote: %v", err)
	}
	if w.calls != 1 {
		t.Fatalf("expected one CCU delete call, got %d", w.calls)
	}
	if _, ok := h.Program("P7"); ok {
		t.Fatal("program still present after DeleteProgramRemote")
	}
	if !removed {
		t.Fatal("NotifyRemoved hook did not fire")
	}
}

func TestHubDeleteProgramRemote_UnknownID_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu")
	err := h.DeleteProgramRemote(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrProgramNotFound) {
		t.Fatalf("expected ErrProgramNotFound, got %v", err)
	}
}

func TestHubDeleteProgramRemote_WriterError_KeepsEntry(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu")
	w := &deleterStub{deleteErr: errors.New("ccu unreachable")}
	p := NewProgram("ccu", "P9", "Test", "", false, w)
	h.PutProgram(p)

	if err := h.DeleteProgramRemote(context.Background(), "P9"); err == nil {
		t.Fatal("expected the writer error to propagate")
	}
	if _, ok := h.Program("P9"); !ok {
		t.Fatal("program removed from cache despite writer failure")
	}
}
