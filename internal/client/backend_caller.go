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
// For `setValue`, concurrent calls with the same (interface, channel,
// parameter) triple are coalesced — the first call wins and followers
// inherit its result. Other methods pass through without coalescing.
func (c *BackendCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	coalesceKey := coalesceKeyFor(method, args)
	return c.client.Call(ctx, method, args, c.priority, coalesceKey)
}

// Priority exposes the configured priority, useful in tests.
func (c *BackendCaller) Priority() hmenum.CommandPriority { return c.priority }

// Verify BackendCaller satisfies backends.Caller at compile time.
var _ backends.Caller = (*BackendCaller)(nil)

// coalesceKeyFor derives a Coalescer dedup key from a method and its
// arguments. Only `setValue` is coalesced today; the key is the tuple
// (method, interface, channel-address, parameter). Callers that issue
// a new setValue for the same channel/parameter while one is in-flight
// will share the leader's result instead of queueing an extra wire call.
//
// All other methods return "" (no coalescing).
func coalesceKeyFor(method string, args []any) string {
	if method != "setValue" || len(args) < 3 {
		return ""
	}
	iface, ok1 := args[0].(string)
	channel, ok2 := args[1].(string)
	param, ok3 := args[2].(string)
	if !ok1 || !ok2 || !ok3 {
		return ""
	}
	return fmt.Sprintf("setValue:%s:%s:%s", iface, channel, param)
}
