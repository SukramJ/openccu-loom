// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	reply, err := c.callRaw(ctx, method, args)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToGo(reply), nil
}

// callRaw encodes the Go args into xmlrpc.Value params (CUxD shares the
// XML-RPC value set) and returns the decoded reply Value untouched.
func (c *binrpcCaller) callRaw(ctx context.Context, method string, args []any) (xmlrpc.Value, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, arg := range args {
		v, err := goToXMLRPCValue(arg)
		if err != nil {
			return nil, fmt.Errorf("arg to binrpc: %w", err)
		}
		params = append(params, v)
	}
	return c.client.Call(ctx, method, params)
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

// Deinit deregisters callbackURL with CUxD. The second parameter is omitted
// — see [xmlrpcAnnouncer.Deinit] for why the interface id must not be sent.
func (a *binrpcAnnouncer) Deinit(ctx context.Context, callbackURL string) error {
	_, err := a.client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL),
	})
	if err != nil {
		return fmt.Errorf("binrpc deinit: %w", err)
	}
	return nil
}
