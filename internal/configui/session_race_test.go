// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestSessionConcurrentAccess runs multiple goroutines in parallel on the
// same session: Set / Undo / Redo / IsDirty / CurrentValue / ApplyPreset /
// Discard. Run with -race to expose any lock gaps in the session
// implementation.
//
// Run with: go test -race ./internal/configui/... -run
// TestSessionConcurrentAccess
func TestSessionConcurrentAccess(t *testing.T) {
	t.Parallel()

	desc := map[string]hmproto.ParameterData{
		"P1": {Type: "INTEGER"},
		"P2": {Type: "BOOL"},
		"P3": {Type: "INTEGER"},
	}
	initial := map[string]any{"P1": 0, "P2": false, "P3": 0}
	s := NewSession(desc, initial)

	const goroutines = 16
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(gid int) {
			defer wg.Done()
			for j := range iterations {
				switch (gid + j) % 8 {
				case 0:
					s.Set("P1", j%100)
				case 1:
					s.Set("P2", j%2 == 0)
				case 2:
					_ = s.IsDirty()
				case 3:
					_ = s.CurrentValue("P1")
				case 4:
					_ = s.Undo()
				case 5:
					_ = s.Redo()
				case 6:
					_ = s.ApplyPreset(map[string]any{"P3": j % 1000})
				case 7:
					_ = s.Changes()
				}
			}
		}(i)
	}
	wg.Wait()

	// Sanity: no map-size inconsistency from a race-corrupted state.
	changes := s.Changes()
	for k := range changes {
		if _, ok := desc[k]; !ok {
			t.Errorf("unknown parameter %q in changes — race corruption?", k)
		}
	}
}

// TestSessionDiscardConcurrentWrites runs Set and Discard in parallel on the
// same session. Discard clears currentValues and stacks; a concurrent Set
// must not write into a partially cleared state.
func TestSessionDiscardConcurrentWrites(t *testing.T) {
	t.Parallel()

	desc := map[string]hmproto.ParameterData{
		"P": {Type: "INTEGER"},
	}
	s := NewSession(desc, map[string]any{"P": 0})

	var wg sync.WaitGroup
	const writers = 8
	const discards = 4

	wg.Add(writers + discards)
	for i := range writers {
		go func(gid int) {
			defer wg.Done()
			for j := range 100 {
				s.Set("P", gid*100+j)
			}
		}(i)
	}
	for range discards {
		go func() {
			defer wg.Done()
			for range 50 {
				s.Discard()
			}
		}()
	}
	wg.Wait()

	// Final invariant: CurrentValue("P") returns either the initial
	// value (after Discard) or one of the writer values; not nil.
	if v := s.CurrentValue("P"); v == nil {
		t.Error("CurrentValue(P) is nil after concurrent Set+Discard — Discard left map in inconsistent state")
	}
}
