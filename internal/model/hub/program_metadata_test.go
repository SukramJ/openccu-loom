// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// noopProgramWriter is an execution backend that records nothing; the
// metadata race test only needs distinct instances to swap in.
type noopProgramWriter struct{}

func (noopProgramWriter) ExecuteProgram(context.Context, string) error { return nil }

func (noopProgramWriter) SetProgramEnabled(context.Context, string, bool) error { return nil }

// TestUpdateMetadataSerialisesWithNameReaders is a race tripwire for the
// periodic program refresh: it rewrites the program name in place while
// REST renders the program list and log lines render the signature. The
// refresh must take the same lock those readers take. Run with -race.
func TestUpdateMetadataSerialisesWithNameReaders(t *testing.T) {
	t.Parallel()

	p := hub.NewProgram("race-central", "4711", "program0", "", false, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 500 {
			// A fresh writer every round: the refresh replaces the
			// execution backend in place while commands run against it.
			p.UpdateMetadata("program"+strconv.Itoa(i+1), i%2 == 0, noopProgramWriter{})
			// The hub refresh rewrites EnabledDefault on every pass too
			// (hub_wiring.go's loadPrograms), the same shape of in-place
			// rewrite UpdateMetadata guards for Name/IsInternal.
			p.SetEnabledDefault(i%3 == 0)
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				_ = p.LegacyName()
				_ = p.Signature()
				_ = p.FullName()
				// The north-bound assembly paths render the name too: the
				// REST/WS identity payload and the canonical unique id.
				_ = p.Info()
				_ = p.Internal()
				_ = p.CanonicalUniqueID("ABC123")
				// The MQTT discovery fan-out reads EnabledDefault via this
				// promoted-and-shadowed accessor while the refresh rewrites it.
				_ = p.EnabledByDefault()
				// The command path reads the writer the refresh swaps.
				_ = p.Execute(context.Background())
			}
		}()
	}
	wg.Wait()

	if got, want := p.LegacyName(), "program500"; got != want {
		t.Fatalf("LegacyName()=%q after 500 refreshes, want %q", got, want)
	}
}
