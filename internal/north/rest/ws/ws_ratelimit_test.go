// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests the per-identity WS command rate gate: a burst is allowed, the next
// call is throttled with CommandErrorRateLimited, and distinct identities have
// independent buckets.
package ws

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCommandRateLimiterBurstThenBlocks(t *testing.T) {
	t.Parallel()
	rl := newCommandRateLimiter(1, 3) // burst 3, slow (1/s) refill
	for i := range 3 {
		if !rl.allow("alice") {
			t.Fatalf("request %d within burst was blocked", i+1)
		}
	}
	if rl.allow("alice") {
		t.Fatal("request past the burst was allowed")
	}
	if !rl.allow("bob") {
		t.Fatal("a different identity must have its own bucket")
	}
}

func TestRouterDispatchRateLimited(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	r.Register("ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return "pong", nil
	})

	// The default per-identity burst is commandRateBurst; anonymous calls
	// share one bucket, so the (burst+1)-th call is throttled.
	for i := range commandRateBurst {
		if res := r.Dispatch(context.Background(), "ping", nil); res.Error != nil {
			t.Fatalf("dispatch %d within burst errored: %+v", i+1, res.Error)
		}
	}
	res := r.Dispatch(context.Background(), "ping", nil)
	if res.Error == nil || res.Error.Code != CommandErrorRateLimited {
		t.Fatalf("expected %q after the burst, got %+v", CommandErrorRateLimited, res.Error)
	}
}
