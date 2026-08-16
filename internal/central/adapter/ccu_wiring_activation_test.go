// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func testXMLRPCBackoff() []time.Duration {
	return []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
}

// newActivationTestClient builds a bare InterfaceClient that starts in CREATED,
// the state a fresh XML-RPC interface sits in before its ingest and init().
func newActivationTestClient(t *testing.T) *client.InterfaceClient {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		Enabled:     true,
		Logger:      discardLogger(),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return ic
}

// TestRunXMLRPCActivationExhaustsRetriesLeavesClientReconnectable is the
// regression guard for the XML-RPC ingest-exhaustion defect: activate() fails
// inside IngestFromBackend, before the init()/callback block that would advance
// the state machine, so an ingest that exhausted its retries used to leave the
// client stuck in CREATED. CanReconnect is false in CREATED, so the recovery
// pipeline rejected every trigger with "CanReconnect returned false" and the
// interface served zero devices until a daemon restart. The exhausted path must
// walk the client to DISCONNECTED, exactly as the CUxD sibling does.
func TestRunXMLRPCActivationExhaustsRetriesLeavesClientReconnectable(t *testing.T) {
	t.Parallel()

	ic := newActivationTestClient(t)
	if got := ic.ClientState(); got != hmenum.ClientStateCreated {
		t.Fatalf("precondition: fresh client state = %s, want %s", got, hmenum.ClientStateCreated)
	}

	backoff := testXMLRPCBackoff()
	calls := 0
	ingested := runXMLRPCActivation(context.Background(), backoff,
		func(context.Context) error {
			calls++
			return errors.New("ingest: connection refused")
		},
		ic, "ccu", "ccu-HmIP-RF", discardLogger())

	if ingested {
		t.Fatal("runXMLRPCActivation reported ingested=true for a permanently failing ingest")
	}
	if want := len(backoff) + 1; calls != want {
		t.Fatalf("activate calls = %d, want %d (every backoff step plus the final attempt)", calls, want)
	}
	if got := ic.ClientState(); got == hmenum.ClientStateCreated {
		t.Fatal("client stayed in CREATED after ingest exhaustion; the recovery pipeline can never reconnect it")
	}
	if got := ic.ClientState(); got != hmenum.ClientStateDisconnected {
		t.Fatalf("client state = %s, want %s", got, hmenum.ClientStateDisconnected)
	}
	if !ic.CanReconnect() {
		t.Fatal("CanReconnect() = false after ingest exhaustion; the recovery pipeline would reject every trigger")
	}
}

// TestRunXMLRPCActivationSucceedsReportsIngested pins the loaded signal the
// readiness tally depends on: a successful ingest reports ingested=true, so
// bringUpCentral counts the interface, and it does not touch the client state
// (the init()/callback block advances it in production).
func TestRunXMLRPCActivationSucceedsReportsIngested(t *testing.T) {
	t.Parallel()

	ic := newActivationTestClient(t)
	calls := 0
	ingested := runXMLRPCActivation(context.Background(), testXMLRPCBackoff(),
		func(context.Context) error {
			calls++
			if calls < 2 {
				return errors.New("ingest: 503 warming up")
			}
			return nil
		},
		ic, "ccu", "ccu-HmIP-RF", discardLogger())

	if !ingested {
		t.Fatal("runXMLRPCActivation reported ingested=false after the retry succeeded")
	}
	if calls != 2 {
		t.Fatalf("activate calls = %d, want 2 (one failure then success)", calls)
	}
}

// TestRunXMLRPCActivationStopsOnContextCancel proves a teardown during the retry
// window aborts immediately and still leaves the client reconnectable, so a
// re-init generation can pick it up.
func TestRunXMLRPCActivationStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ic := newActivationTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	ingested := runXMLRPCActivation(ctx, []time.Duration{time.Hour, time.Hour},
		func(context.Context) error {
			calls++
			cancel()
			return errors.New("ingest: connection refused")
		},
		ic, "ccu", "ccu-HmIP-RF", discardLogger())

	if ingested {
		t.Fatal("ingested=true after context cancel")
	}
	if calls != 1 {
		t.Fatalf("activate calls = %d, want 1 (the wait aborts on the first cancel)", calls)
	}
	if got := ic.ClientState(); got != hmenum.ClientStateDisconnected {
		t.Fatalf("client state = %s, want %s after a context-cancel abort", got, hmenum.ClientStateDisconnected)
	}
}
