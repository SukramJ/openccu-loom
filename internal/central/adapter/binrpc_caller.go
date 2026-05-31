// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// binrpcCaller bridges a [binrpc.Client] to the [backends.Caller]
// interface. BIN-RPC uses the same value types as XML-RPC but with a
// compact binary codec; the xmlrpcCaller conversion helpers are reused
// here because BIN-RPC values share the xmlrpc.Value hierarchy.
type binrpcCaller struct{ client *binrpc.Client }

func (c *binrpcCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, arg := range args {
		v, err := goToXMLRPCValue(arg)
		if err != nil {
			return nil, fmt.Errorf("arg to binrpc: %w", err)
		}
		params = append(params, v)
	}
	reply, err := c.client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToGo(reply), nil
}

// binrpcAnnouncer sends CUxD init/deinit calls via the outbound
// BIN-RPC client. CUxD expects the same two-arg init(callbackURL,
// interfaceID) shape as XML-RPC.
type binrpcAnnouncer struct{ client *binrpc.Client }

func newBINRPCAnnouncer(c *binrpc.Client) *binrpcAnnouncer {
	return &binrpcAnnouncer{client: c}
}

// Init registers the BIN-RPC callback URL with CUxD.
func (a *binrpcAnnouncer) Init(ctx context.Context, interfaceID, callbackURL string) error {
	_, err := a.client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL),
		xmlrpc.StringValue(interfaceID),
	})
	if err != nil {
		return fmt.Errorf("binrpc init: %w", err)
	}
	return nil
}

// Deinit deregisters the BIN-RPC callback URL with CUxD.
func (a *binrpcAnnouncer) Deinit(ctx context.Context, interfaceID string) error {
	_, err := a.client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(""),
		xmlrpc.StringValue(interfaceID),
	})
	if err != nil {
		return fmt.Errorf("binrpc deinit: %w", err)
	}
	return nil
}
