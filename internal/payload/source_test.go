// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time check: *ServiceRegistry implements the write-side half of
// Source (ServiceMethodNames + Invoke). This does NOT need a Test* function
// — the compiler verifies it at build time.
var _ interface {
	ServiceMethodNames() []string
	Invoke(ctx context.Context, name string, params map[string]any, priority hmenum.CommandPriority) error
} = (*ServiceRegistry)(nil)

// mockSource is a minimal implementation of the full Source interface.
// It embeds ServiceRegistry for the write half and provides trivial
// implementations of the three read methods.
type mockSource struct {
	ServiceRegistry
}

func (m *mockSource) Info() InfoPayload     { return nil }
func (m *mockSource) Config() ConfigPayload { return nil }
func (m *mockSource) State() StatePayload   { return nil }

// Compile-time check: *mockSource (with embedded ServiceRegistry) satisfies
// the complete Source interface. If Source gains a new method this will fail
// fast rather than silently at runtime.
var _ Source = (*mockSource)(nil)
