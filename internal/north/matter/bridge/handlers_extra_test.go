// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the CaseAdapter and OperationalSessionLookup methods
// not covered by the existing handlers_test.go: SetResponder, SnapshotResponder,
// WithFabricResolver, FabricFor.
// Lives in package bridge to access unexported types.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
)

// ─── CaseAdapter.SetResponder / SnapshotResponder ────────────────────────────

func TestCaseAdapter_SetResponder_Nil(t *testing.T) {
	t.Parallel()
	a := NewCaseAdapter(nil)
	a.SetResponder(nil)
	if a.SnapshotResponder() != nil {
		t.Error("SnapshotResponder: expected nil after SetResponder(nil)")
	}
}

func TestCaseAdapter_SetResponder_RoundTrip(t *testing.T) {
	t.Parallel()
	r := &sigma.Responder{}
	a := NewCaseAdapter(nil)
	a.SetResponder(r)
	if got := a.SnapshotResponder(); got != r {
		t.Errorf("SnapshotResponder: want %p, got %p", r, got)
	}
}

func TestCaseAdapter_SetResponder_Replaces(t *testing.T) {
	t.Parallel()
	r1 := &sigma.Responder{}
	r2 := &sigma.Responder{}
	a := NewCaseAdapter(r1)
	a.SetResponder(r2)
	if got := a.SnapshotResponder(); got != r2 {
		t.Errorf("SnapshotResponder after replace: want r2, got %p", got)
	}
}

// ─── OperationalSessionLookup.WithFabricResolver / FabricFor ────────────────

func TestOperationalSessionLookup_FabricFor_NoResolver_ReturnsZero(t *testing.T) {
	t.Parallel()
	l := NewOperationalSessionLookup(func(_ uint16) (*channel.Session, bool) {
		return nil, false
	})
	idx, ok := l.FabricFor(1)
	if idx != 0 || ok {
		t.Errorf("FabricFor without resolver: want (0,false), got (%d,%v)", idx, ok)
	}
}

func TestOperationalSessionLookup_WithFabricResolver_Wires(t *testing.T) {
	t.Parallel()
	l := NewOperationalSessionLookup(nil).WithFabricResolver(func(id uint16) (uint8, bool) {
		if id == 5 {
			return 2, true
		}
		return 0, false
	})
	idx, ok := l.FabricFor(5)
	if idx != 2 || !ok {
		t.Errorf("FabricFor(5): want (2,true), got (%d,%v)", idx, ok)
	}
	idx, ok = l.FabricFor(99)
	if idx != 0 || ok {
		t.Errorf("FabricFor(99): want (0,false), got (%d,%v)", idx, ok)
	}
}

func TestOperationalSessionLookup_WithFabricResolver_ReturnsReceiver(t *testing.T) {
	t.Parallel()
	l := NewOperationalSessionLookup(nil)
	got := l.WithFabricResolver(nil)
	if got != l {
		t.Error("WithFabricResolver should return the receiver")
	}
}

func TestOperationalSessionLookup_WithFabricResolver_NilSelf(t *testing.T) {
	t.Parallel()
	var l *OperationalSessionLookup
	// Must not panic — nil receiver should return nil.
	got := l.WithFabricResolver(nil)
	if got != nil {
		t.Errorf("nil receiver: want nil return, got %v", got)
	}
}

// ─── randPBKDFRandom ─────────────────────────────────────────────────────────

// TestRandPBKDFRandom_NonZero verifies that randPBKDFRandom returns a
// fixed-size array of crypto/rand bytes (in practice non-zero on any sane OS).
func TestRandPBKDFRandom_NonZero(t *testing.T) {
	t.Parallel()
	r := randPBKDFRandom()
	// The returned value must be exactly spake2.PBKDFRandomSize bytes (the
	// function returns a fixed-size array, so len is always correct by type).
	allZero := true
	for _, b := range r {
		if b != 0 {
			allZero = false
			break
		}
	}
	// crypto/rand on Linux virtually never produces all-zero output.
	// Call it 3 times so a fluke zero-array becomes astronomically unlikely.
	if allZero {
		r2 := randPBKDFRandom()
		r3 := randPBKDFRandom()
		allZero = true
		for _, b := range r2 {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			allZero = true
			for _, b := range r3 {
				if b != 0 {
					allZero = false
					break
				}
			}
		}
	}
	if allZero {
		t.Error("randPBKDFRandom: all three results were all-zero (crypto/rand broken?)")
	}
}
