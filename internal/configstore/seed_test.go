// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configstore

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestSeedSectionsFromConfigEmpty verifies that SeedSectionsFromConfig
// writes all marshalable sections into an empty store and returns n > 0.
func TestSeedSectionsFromConfigEmpty(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	s := New(defaultBootstrap(), sl, nil)

	n, err := s.SeedSectionsFromConfig(context.Background(), config.Default(), "test")
	if err != nil {
		t.Fatalf("SeedSectionsFromConfig: %v", err)
	}
	if n == 0 {
		t.Error("expected n > 0 sections seeded, got 0")
	}

	// north.mqtt must be present (it is a struct-backed section).
	rows, err := sl.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := make(map[string]bool, len(rows))
	for _, r := range rows {
		found[r.Section] = true
	}
	for _, sec := range []Section{SectionMQTT, SectionMatter} {
		if !found[string(sec)] {
			t.Errorf("expected section %q to be seeded, but it is absent", sec)
		}
	}
	// SectionSecurity has no config.Config target and must NOT be seeded.
	if found[string(SectionSecurity)] {
		t.Errorf("section %q must not be seeded (no config.Config source)", SectionSecurity)
	}
}

// TestSeedSectionsFromConfigNonEmptyIsNoOp verifies that SeedSectionsFromConfig
// is a no-op when the store already holds at least one section row.
func TestSeedSectionsFromConfigNonEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	// Pre-populate one row to simulate an already-seeded / SPA-edited store.
	if _, err := sl.Put(
		context.Background(),
		string(SectionMQTT),
		[]byte(`{"broker_url":"tcp://existing:1883"}`),
		"pre-seed",
	); err != nil {
		t.Fatalf("Put pre-seed: %v", err)
	}

	s := New(defaultBootstrap(), sl, nil)
	n, err := s.SeedSectionsFromConfig(context.Background(), config.Default(), "test")
	if err != nil {
		t.Fatalf("SeedSectionsFromConfig: %v", err)
	}
	if n != 0 {
		t.Errorf("expected n=0 for non-empty store, got %d", n)
	}

	// The only row must still be the original one — no additional rows written.
	rows, err := sl.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("row count=%d want 1 (no new rows must be added)", len(rows))
	}
	if string(rows[0].ValueJSON) != `{"broker_url":"tcp://existing:1883"}` {
		t.Errorf("existing row was modified: %s", rows[0].ValueJSON)
	}
}
