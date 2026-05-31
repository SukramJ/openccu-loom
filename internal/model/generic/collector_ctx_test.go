// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestContextWithCollectorRoundTrip(t *testing.T) {
	t.Parallel()

	coll := generic.NewCollector(newFakeCollectorBackend())
	ctx := generic.ContextWithCollector(context.Background(), coll)

	got := generic.CollectorFromContext(ctx)
	if got != coll {
		t.Fatalf("CollectorFromContext: got %p, want %p", got, coll)
	}
}

func TestCollectorFromContextNoneReturnsNil(t *testing.T) {
	t.Parallel()

	got := generic.CollectorFromContext(context.Background())
	if got != nil {
		t.Fatalf("CollectorFromContext on empty ctx: want nil, got %v", got)
	}
}

func TestNestedContextOverridesOuterCollector(t *testing.T) {
	t.Parallel()

	// Deepest-wins: wrapping a ctx with a second collector should
	// shadow the outer one.
	outer := generic.NewCollector(newFakeCollectorBackend())
	inner := generic.NewCollector(newFakeCollectorBackend())

	ctx := generic.ContextWithCollector(context.Background(), outer)
	ctx = generic.ContextWithCollector(ctx, inner)

	got := generic.CollectorFromContext(ctx)
	if got != inner {
		t.Fatalf("nested ctx: want inner collector, got %p (outer=%p inner=%p)", got, outer, inner)
	}
}

func TestContextWithCollectorDoesNotMutateParent(t *testing.T) {
	t.Parallel()

	parent := context.Background()
	coll := generic.NewCollector(newFakeCollectorBackend())
	_ = generic.ContextWithCollector(parent, coll)

	// The parent context must still have no collector.
	got := generic.CollectorFromContext(parent)
	if got != nil {
		t.Fatalf("parent context was mutated: expected nil collector, got %p", got)
	}
}

// newFakeCollectorBackend returns a CollectorBackend whose dispatch
// always succeeds; used by tests that only care about the
// context-round-trip, not the actual wire dispatch.
func newFakeCollectorBackend() generic.CollectorBackend {
	return &fakeCollectorBackend{}
}

type fakeCollectorBackend struct{}

func (f *fakeCollectorBackend) SetValue(
	_ context.Context,
	_ string,
	_ hmenum.Parameter,
	_ any,
	_ hmenum.CommandPriority,
) error {
	return nil
}

func (f *fakeCollectorBackend) PutParamset(
	_ context.Context,
	_ string,
	_ hmenum.ParamsetKey,
	_ map[string]any,
	_ hmenum.CommandPriority,
) error {
	return nil
}
