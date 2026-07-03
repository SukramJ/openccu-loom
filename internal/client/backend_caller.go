// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// BackendCaller adapts [*InterfaceClient] to [backends.Caller] so
// the backend wrappers can drive calls through the reliability
// stack.
type BackendCaller struct {
	client   *InterfaceClient
	priority hmenum.CommandPriority
}

// NewBackendCaller constructs an adapter with the given default
// priority. Most backend operations use `Low`; commands (SetValue)
// override per call.
func NewBackendCaller(c *InterfaceClient, priority hmenum.CommandPriority) *BackendCaller {
	return &BackendCaller{client: c, priority: priority}
}

// Call implements backends.Caller.
// For `setValue`, concurrent calls with the same (address, parameter,
// value) triple are coalesced — the first call wins and followers inherit
// its result. Two writes to the same data point with DIFFERENT values are
// NOT coalesced, so the follower's value still reaches the wire (collapsing
// them would silently drop the follower's write — a last-write-lost bug).
// Other methods pass through without coalescing.
func (c *BackendCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	coalesceKey := coalesceKeyFor(method, args)
	return c.client.Call(ctx, method, args, c.priority, coalesceKey)
}

// Priority exposes the configured priority, useful in tests.
func (c *BackendCaller) Priority() hmenum.CommandPriority { return c.priority }

// Verify BackendCaller satisfies backends.Caller at compile time.
var _ backends.Caller = (*BackendCaller)(nil)

// coalesceKeyFor derives a Coalescer dedup key from a setValue call and
// its wire arguments. The setValue wire layout is
// [address, parameter, value, (rxMode)] (see the b.xml/b.bin.Call sites in
// backends/{ccu,cuxd,homegear}.go), so the dedup key MUST include the
// value: two concurrent writes to the same data point with different
// values are genuinely different operations and both must reach the CCU.
// Keying on (address, parameter) alone would collapse them into a single
// wire call and silently drop the follower's value — a last-write-lost bug.
//
// Identical (address, parameter, value) writes still share a leader, so a
// burst of redundant repeats (e.g. "set STATE=true" fired twice) is
// deduplicated into one wire call.
//
// All other methods return "" (no coalescing).
func coalesceKeyFor(method string, args []any) string {
	if method != "setValue" || len(args) < 3 {
		return ""
	}
	address, ok1 := args[0].(string)
	parameter, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return ""
	}
	// args[2] is the value, which may be any wire type (bool, int, float,
	// string). Encode both the Go type and the value so that, e.g., the
	// bool true and the string "true" never collide on the same key.
	value := args[2]
	return fmt.Sprintf("setValue|%s|%s|%T|%v", address, parameter, value, value)
}
