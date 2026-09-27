// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestScopeRefusalIsNotAWireFailure pins that a system refusing an
// operation for a missing credential scope does not count against the
// circuit breaker: the link is healthy and answered. Counting it would open
// the breaker over a permission and cut the interface off for everything
// the credential may do. A transport failure next to it still counts.
func TestScopeRefusalIsNotAWireFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	refusal := fmt.Errorf("setValue: %w", &hmerr.ScopeMissingError{Scope: "rpc:operate", Operation: "setValue"})
	if IsWireFailure(ctx, refusal) {
		t.Error("a scope refusal was counted as a wire failure")
	}
	if !IsWireFailure(ctx, fmt.Errorf("dial: %w", hmerr.ErrNoConnection)) {
		t.Error("a connection failure is no longer counted; the negative control lost its teeth")
	}
}
