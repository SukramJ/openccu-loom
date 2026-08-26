// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newMinimalEntry builds a ClientEntry with a functional InterfaceClient
// using the package-local nopCaller.
func newMinimalEntry(ifaceID string, iface hmenum.Interface) *ClientEntry {
	ic, _ := client.New(client.Config{
		CentralName: "test-central",
		Interface:   iface,
		Caller:      nopCaller,
	})
	return &ClientEntry{
		InterfaceID: ifaceID,
		Interface:   iface,
		Host:        "ccu.test",
		Client:      ic,
	}
}

// TestCreateClient_TCPProbeFailureReturnsNoConnection verifies that when the
// TCP pre-flight check cannot dial the given address, CreateClient returns an
// error wrapping [hmerr.ErrNoConnection] without calling the factory.
func TestCreateClient_TCPProbeFailureReturnsNoConnection(t *testing.T) {
	t.Parallel()

	// Bind a listener, capture its port, then close it so the port is
	// guaranteed to be unavailable when CreateClient fires.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	factoryCalled := false
	cfg := CreateClientConfig{
		Host:        "127.0.0.1",
		Port:        port,
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Factory: func(_ context.Context) (*ClientEntry, error) {
			factoryCalled = true
			return newMinimalEntry("HmIP-RF", hmenum.InterfaceHmIPRF), nil
		},
	}

	cc := NewClientCoordinator()
	_, gotErr := cc.CreateClient(context.Background(), cfg)
	if gotErr == nil {
		t.Fatal("expected error on closed port, got nil")
	}
	if !errors.Is(gotErr, hmerr.ErrNoConnection) {
		t.Errorf("error %v must wrap hmerr.ErrNoConnection", gotErr)
	}
	if factoryCalled {
		t.Error("factory must not be called when TCP probe fails")
	}
}

// TestCreateClient_AuthRetryRegistersOnSuccess verifies the auth-retry path:
// the factory returns ErrAuthFailure on the first two attempts and succeeds on
// the third, and the resulting entry is registered with the coordinator.
func TestCreateClient_AuthRetryRegistersOnSuccess(t *testing.T) {
	t.Parallel()

	// Shorten backoff so the test does not sleep for seconds.
	prev := createClientBackoffInitial
	prevMax := createClientBackoffMax
	createClientBackoffInitial = time.Millisecond
	createClientBackoffMax = 4 * time.Millisecond
	t.Cleanup(func() {
		createClientBackoffInitial = prev
		createClientBackoffMax = prevMax
	})

	attempts := 0
	cfg := CreateClientConfig{
		// Port == 0 skips the TCP probe.
		Host:        "ccu.test",
		Port:        0,
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Factory: func(_ context.Context) (*ClientEntry, error) {
			attempts++
			if attempts < 3 {
				return nil, fmt.Errorf("bad credentials: %w", hmerr.ErrAuthFailure)
			}
			return newMinimalEntry("BidCos-RF", hmenum.InterfaceBidCosRF), nil
		},
	}

	cc := NewClientCoordinator()
	entry, err := cc.CreateClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateClient: unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Errorf("factory called %d times, want 3", attempts)
	}
	if entry == nil || entry.InterfaceID != "BidCos-RF" {
		t.Errorf("returned entry = %v, want BidCos-RF", entry)
	}
	if !cc.HasClient("BidCos-RF") {
		t.Error("entry must be registered after CreateClient succeeds")
	}
}
