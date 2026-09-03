// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
)

// hmAdpFakeBackend is a distinguishable [backends.Operations] stand-in. Only
// identity matters here — the resolver must hand back the backend registered
// for the interface id it was asked about.
type hmAdpFakeBackend struct {
	backends.Operations
	name string
}

// TestHmAdpCUxDHotplugResolverUsesTheRegisteredBackend pins that the device
// ingest seam the CUxD wiring installs resolves per interface id. The seam is
// per unit, so it is reached with every interface's wire id — an announcement
// or an accepted deferred device on HmIP-RF included. Answering the CUxD
// backend for all of them hydrated an HmIP device's paramsets over CUxD's
// BIN-RPC connection, and silently: the ingestor's nil-backend skip is never
// reached when the resolver always returns something.
func TestHmAdpCUxDHotplugResolverUsesTheRegisteredBackend(t *testing.T) {
	t.Parallel()

	cuxd := &hmAdpFakeBackend{name: "cuxd"}
	hmip := &hmAdpFakeBackend{name: "hmip"}

	reg := newBackendRegistry()
	reg.put("ccu-01-CUxD", cuxd)
	reg.put("ccu-01-HmIP-RF", hmip)

	resolve := cuxdHotplugBackendResolver(reg, "ccu-01-CUxD", cuxd)

	cases := []struct {
		interfaceID string
		want        backends.Operations
	}{
		{"ccu-01-HmIP-RF", hmip},
		{"ccu-01-CUxD", cuxd},
		{"ccu-01-BidCos-RF", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := resolve(tc.interfaceID)
		if got != tc.want {
			gotName, wantName := "nil", "nil"
			if fb, ok := got.(*hmAdpFakeBackend); ok {
				gotName = fb.name
			}
			if fb, ok := tc.want.(*hmAdpFakeBackend); ok {
				wantName = fb.name
			}
			t.Fatalf("resolve(%q) = %s, want %s", tc.interfaceID, gotName, wantName)
		}
	}
}

// TestHmAdpCUxDHotplugResolverFallsBackForItsOwnID covers the CUxD-only setup:
// with no shared registry the resolver still answers for CUxD's own wire id,
// and for nothing else.
func TestHmAdpCUxDHotplugResolverFallsBackForItsOwnID(t *testing.T) {
	t.Parallel()

	cuxd := &hmAdpFakeBackend{name: "cuxd"}
	resolve := cuxdHotplugBackendResolver(nil, "ccu-01-CUxD", cuxd)

	if got := resolve("ccu-01-CUxD"); got != backends.Operations(cuxd) {
		t.Fatalf("resolve(own id) = %v, want the CUxD backend", got)
	}
	if got := resolve("ccu-01-HmIP-RF"); got != nil {
		t.Fatalf("resolve(foreign id) = %v, want nil so the ingest is skipped", got)
	}
}
