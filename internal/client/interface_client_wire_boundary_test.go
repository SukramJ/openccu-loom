// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestWireBoundaryIDWithInitInterfaceID verifies that WireBoundaryID returns
// Config.InitInterfaceID verbatim when it is non-empty — the full
// <instance>-<central>-<interface> triple that distinguishes this daemon from a
// co-located peer on the same CCU.
func TestWireBoundaryIDWithInitInterfaceID(t *testing.T) {
	t.Parallel()
	const want = "Otto-OttoLoom-HmIP-RF"
	c, err := New(Config{
		CentralName:     "OttoLoom",
		Interface:       hmenum.InterfaceHmIPRF,
		InitInterfaceID: want,
		Caller:          CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.WireBoundaryID(); got != want {
		t.Errorf("WireBoundaryID() = %q, want %q", got, want)
	}
}

// TestWireBoundaryIDFallsBackToBareInterface verifies that when InitInterfaceID
// is empty, WireBoundaryID returns the bare interface name string — the
// backward-compatible single-daemon behaviour.
func TestWireBoundaryIDFallsBackToBareInterface(t *testing.T) {
	t.Parallel()
	c, err := New(Config{
		CentralName: "OttoLoom",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := string(hmenum.InterfaceHmIPRF)
	if got := c.WireBoundaryID(); got != want {
		t.Errorf("WireBoundaryID() = %q, want %q (bare interface name)", got, want)
	}
}

// TestCheckConnectionAvailabilityCallerIDEmbedsTriplet verifies that when
// InitInterfaceID is set and the backend declares PingPong capability, the
// caller_id sent for a ping has the full triple as prefix, not the bare
// interface name.
func TestCheckConnectionAvailabilityCallerIDEmbedsTriplet(t *testing.T) {
	t.Parallel()
	const triplet = "Otto-OttoLoom-HmIP-RF"
	var capturedParams []any
	c, err := New(Config{
		CentralName:     "OttoLoom",
		Interface:       hmenum.InterfaceHmIPRF,
		InitInterfaceID: triplet,
		Caller: CallerFunc(func(_ context.Context, _ string, params []any) (any, error) {
			capturedParams = params
			return nil, nil
		}),
		Capabilities: backends.CapabilityFor(backends.KindCCU),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ok := c.CheckConnectionAvailability(context.Background(), true)
	if !ok {
		t.Fatal("CheckConnectionAvailability returned false; want true")
	}
	if len(capturedParams) == 0 {
		t.Fatal("no params captured; the ping call was not issued")
	}
	callerID, _ := capturedParams[0].(string)
	if !strings.HasPrefix(callerID, triplet+"#") {
		t.Errorf("caller_id %q does not have prefix %q; must embed full wire-boundary triple", callerID, triplet+"#")
	}
	// The base must NOT be the bare interface name.
	if strings.HasPrefix(callerID, string(hmenum.InterfaceHmIPRF)+"#") && !strings.HasPrefix(callerID, triplet+"#") {
		t.Errorf("caller_id %q uses bare interface name as prefix; want full triple", callerID)
	}
}

// TestCheckConnectionAvailabilityCallerIDFallsBackToBareInterfaceWhenEmpty
// verifies that when InitInterfaceID is empty, the caller_id falls back to the
// bare interface name prefix — the single-daemon baseline.
func TestCheckConnectionAvailabilityCallerIDFallsBackToBareInterfaceWhenEmpty(t *testing.T) {
	t.Parallel()
	var capturedParams []any
	c, err := New(Config{
		CentralName: "OttoLoom",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: CallerFunc(func(_ context.Context, _ string, params []any) (any, error) {
			capturedParams = params
			return nil, nil
		}),
		Capabilities: backends.CapabilityFor(backends.KindCCU),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	c.CheckConnectionAvailability(context.Background(), true)
	if len(capturedParams) == 0 {
		t.Fatal("no params captured")
	}
	callerID, _ := capturedParams[0].(string)
	bare := string(hmenum.InterfaceHmIPRF)
	if !strings.HasPrefix(callerID, bare+"#") {
		t.Errorf("caller_id %q does not have expected fallback prefix %q#", callerID, bare)
	}
}
