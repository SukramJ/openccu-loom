// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// jsonrpcCaller bridges a [jsonrpc.Client] to the [backends.Caller]
// interface. The JSON-RPC wire takes a single map[string]any for named
// parameters; the variadic args are collapsed into that map (first arg
// wins). When args is empty the call is made with no params. When the
// first arg is already a map[string]any it is passed through as-is.
type jsonrpcCaller struct{ client *jsonrpc.Client }

func (c *jsonrpcCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	var params map[string]any
	switch len(args) {
	case 0:
		// no params
	case 1:
		switch v := args[0].(type) {
		case map[string]any:
			params = v
		case nil:
			// nil-param call — leave params nil
		default:
			return nil, fmt.Errorf("jsonrpcCaller: single arg must be map[string]any, got %T", args[0])
		}
	default:
		return nil, errors.New("jsonrpcCaller: variadic args not supported for JSON-RPC; use a single map[string]any")
	}

	var out any
	if err := c.client.Call(ctx, method, params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CallAt implements backends.Caller. The JSON-RPC bridge issues its
// calls directly, without the command throttle and circuit breaker the
// XML-RPC path goes through, so there is no scheduler here to read a
// priority. It is accepted and ignored, which is the honest shape: the
// alternative is a caller that silently looks priority-aware.
func (c *jsonrpcCaller) CallAt(
	ctx context.Context, _ hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	return c.Call(ctx, method, args...)
}
