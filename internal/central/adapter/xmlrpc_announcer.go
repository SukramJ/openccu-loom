// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// xmlrpcAnnouncer speaks the CCU `init(url, interface_id)` method on
// the southbound XML-RPC endpoint. It is the glue between the daemon's
// callback server and the backend's [backends.Announcer] contract.
//
// The CCU convention is:
//
//	init(callbackURL, interfaceID)   — register this daemon as recipient
//	init(callbackURL)                — deregister (interface id omitted,
//	                                    not emptied — see [Deinit])
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

// Deinit tells the CCU to stop sending callbacks to callbackURL by calling
// init with that URL and NO second parameter.
//
// The interface id must not be sent: the CCU keys the deregistration on the
// URL alone, and `init("", interface_id)` — the inverse shape — is read as a
// registration of the empty URL. rfd then keeps that entry and reports
// `XmlRpcClient error calling event(...) on uds://:/RPC2` for every event it
// tries to deliver to it, once per keepalive, until the CCU restarts.
// Measured against a live rfd and a live CUxD: after the inverse shape the
// keepalive PONGs kept arriving, after this one they stopped.
func (a *xmlrpcAnnouncer) Deinit(ctx context.Context, callbackURL string) error {
	_, err := a.client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL),
	})
	if err != nil {
		return fmt.Errorf("xmlrpc deinit: %w", err)
	}
	return nil
}
