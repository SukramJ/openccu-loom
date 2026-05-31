// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
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
		return nil, fmt.Errorf("jsonrpcCaller: variadic args not supported for JSON-RPC; use a single map[string]any")
	}

	var out any
	if err := c.client.Call(ctx, method, params, &out); err != nil {
		return nil, err
	}
	return out, nil
}
