// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

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
			p.UpdateMetadata("program"+strconv.Itoa(i+1), false, nil)
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
			}
		}()
	}
	wg.Wait()

	if got, want := p.LegacyName(), "program500"; got != want {
		t.Fatalf("LegacyName()=%q after 500 refreshes, want %q", got, want)
	}
}
