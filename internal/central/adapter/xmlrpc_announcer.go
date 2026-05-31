// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// StaticCallbackBaseURL returns a func() string that always yields the
// fixed base URL provided at construction time. It is the backward-compat
// path used in daemon.go when the callback port does not change between
// reconnects (fixed-port mode). Dynamic-port mode builds a provider that
// calls the server's effective-port accessor on every invocation instead.
//
// loom:reachable:reason="used in daemon.go WireDeps.CallbackBaseURL assembly for fixed-port mode"
func StaticCallbackBaseURL(base string) func() string {
	return func() string { return base }
}

// xmlrpcAnnouncer speaks the CCU `init(url, interface_id)` method on
// the southbound XML-RPC endpoint. It is the glue between the daemon's
// callback server and the backend's [backends.Announcer] contract.
//
// The CCU convention is:
//
//	init(callbackURL, interfaceID)   — register this daemon as recipient
//	init("", interfaceID)            — deregister (URL emptied)
type xmlrpcAnnouncer struct {
	client *xmlrpc.Client
}

func newXMLRPCAnnouncer(c *xmlrpc.Client) *xmlrpcAnnouncer {
	return &xmlrpcAnnouncer{client: c}
}

// Init registers the callback URL on the CCU. Safe to call on every
// reconnect — the CCU is idempotent.
func (a *xmlrpcAnnouncer) Init(ctx context.Context, interfaceID, callbackURL string) error {
	_, err := a.client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL),
		xmlrpc.StringValue(interfaceID),
	})
	if err != nil {
		return fmt.Errorf("xmlrpc init: %w", err)
	}
	return nil
}

// Deinit tells the CCU to stop sending callbacks to the previously
// configured URL by sending an empty URL together with the interface id.
func (a *xmlrpcAnnouncer) Deinit(ctx context.Context, interfaceID string) error {
	_, err := a.client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(""),
		xmlrpc.StringValue(interfaceID),
	})
	if err != nil {
		return fmt.Errorf("xmlrpc deinit: %w", err)
	}
	return nil
}
