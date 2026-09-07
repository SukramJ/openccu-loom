// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestAnnounceCUxDCallbackWithoutListenerLeavesTheClientConnected pins the
// read-through contract: an interface whose callback endpoint cannot be
// announced (the BIN-RPC listener failed to bind, so the callback address
// is empty) still serves every read and write and must say so in its
// client state. Left in CREATED, hasConnectionIssue() reported an outage
// for the daemon's life — every gated hub job of the central returned
// without running and check_connection published ConnectionLost every
// 30 s, the opposite of "still works, just without push events".
func TestAnnounceCUxDCallbackWithoutListenerLeavesTheClientConnected(t *testing.T) {
	t.Parallel()
	ic := newActivationTestClient(t)
	if got := ic.ClientState(); got != hmenum.ClientStateCreated {
		t.Fatalf("precondition: state = %s, want CREATED", got)
	}
	var backend backends.Operations // never called: no callback URL, no Init
	announceCUxDCallback(context.Background(), backend, ic, "ccu", "ccu-CUxD", "", discardLogger())
	if got := ic.ClientState(); got != hmenum.ClientStateConnected {
		t.Fatalf("client state = %s after a callback-less bring-up, want CONNECTED (read-through mode)", got)
	}
	// CONNECTED is a state check_connection accepts and the recovery
	// pipeline moves out of on a real outage; CREATED was neither.
	_ = client.Config{}
}
