// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ActionTrigger fires a write-only parameter that carries no
// caller-supplied value — the "any write means do it now" shape.
//
// The interface exists because the concrete shape a write-only ACTION
// resolves to is not a property of the parameter's wire descriptor
// alone: a parameter listed as a button action becomes a [Button],
// everything else becomes an [Action]. Both are the same thing to a
// caller that only wants to fire it, and a caller that type-asserts one
// concrete shape silently stops working the moment the classification
// moves — with no compile error, because the assertion is a runtime
// check that simply yields false.
//
// Consumers that fire a parameter (RESET_MOTION, RESET_PRESENCE,
// SUBMIT, …) depend on this interface rather than on [Button] or
// [Action].
type ActionTrigger interface {
	// FireAction sends the parameter's trigger value to the CCU.
	FireAction(ctx context.Context, priority hmenum.CommandPriority) error
}

// Compile-time proof that every shape a write-only trigger can resolve
// to satisfies the interface. Without these, adding a shape to the
// resolver and forgetting the method is a runtime miss, which is the
// exact failure this interface exists to prevent.
var (
	_ ActionTrigger = (*Button)(nil)
	_ ActionTrigger = (*Action)(nil)
	_ ActionTrigger = (*ActionBoolean)(nil)
)
