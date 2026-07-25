// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"testing"
)

type stubUsageReader struct {
	programs []SysvarUsage
	err      error
}

func (s *stubUsageReader) SysvarUsagePrograms(_ context.Context, _ string) ([]SysvarUsage, error) {
	return s.programs, s.err
}

// fullUsageMutator satisfies the whole [Mutator] bundle (via the existing
// per-role stubs) plus [SysvarUsageReader], so SetMutator's opportunistic
// type-assert wires the usage reader.
type fullUsageMutator struct {
	stubSysvarMutator
	stubRoomMutator
	stubFunctionMutator
	stubBackupTrigger
	stubFirmwareUpdater
	stubInboxAccepter
	usage []SysvarUsage
}

func (f *fullUsageMutator) SysvarUsagePrograms(_ context.Context, _ string) ([]SysvarUsage, error) {
	return f.usage, nil
}

func TestHubSysvarUsageRemote_noReader(t *testing.T) {
	h := NewHub("ccu")
	if _, err := h.SysvarUsageRemote(context.Background(), "X"); !errors.Is(err, ErrNoSysvarUsageReader) {
		t.Fatalf("want ErrNoSysvarUsageReader, got %v", err)
	}
}

func TestHubSysvarUsageRemote_ok(t *testing.T) {
	h := NewHub("ccu")
	h.sysvarUsageReader = &stubUsageReader{programs: []SysvarUsage{{ID: "1", Name: "P1", Active: true}}}
	got, err := h.SysvarUsageRemote(context.Background(), "MyVar")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "1" || !got[0].Active {
		t.Fatalf("got=%v", got)
	}
}

func TestHubSysvarUsageRemote_propagatesError(t *testing.T) {
	h := NewHub("ccu")
	sentinel := errors.New("rega error")
	h.sysvarUsageReader = &stubUsageReader{err: sentinel}
	if _, err := h.SysvarUsageRemote(context.Background(), "X"); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
}

// TestHubSetMutatorWiresUsageReader asserts SetMutator opportunistically
// wires a mutator that also implements SysvarUsageReader.
func TestHubSetMutatorWiresUsageReader(t *testing.T) {
	h := NewHub("ccu")
	h.SetMutator(&fullUsageMutator{usage: []SysvarUsage{{ID: "9", Name: "P"}}})
	got, err := h.SysvarUsageRemote(context.Background(), "V")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "9" {
		t.Fatalf("SetMutator did not wire the usage reader; got=%v", got)
	}
}

// TestHubSetMutatorReaderlessLeavesNil asserts a mutator that does NOT
// implement SysvarUsageReader leaves the reader unwired (503 path).
func TestHubSetMutatorReaderlessLeavesNil(t *testing.T) {
	h := NewHub("ccu")
	h.SetMutator(&fullMutator{})
	if _, err := h.SysvarUsageRemote(context.Background(), "V"); !errors.Is(err, ErrNoSysvarUsageReader) {
		t.Fatalf("reader-less mutator must leave usage unwired, got %v", err)
	}
}

// fullMutator satisfies [Mutator] without SysvarUsageReader.
type fullMutator struct {
	stubSysvarMutator
	stubRoomMutator
	stubFunctionMutator
	stubBackupTrigger
	stubFirmwareUpdater
	stubInboxAccepter
}
